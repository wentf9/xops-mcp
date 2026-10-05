// Package xops adapts server-owned storage to the pinned shared core.
package xops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/wentf9/xops-cli/core/mcp/guardrail"
	"github.com/wentf9/xops-cli/core/mcp/policy"
	"github.com/wentf9/xops-cli/core/mcp/ports"
	"github.com/wentf9/xops-cli/core/ssh"
	"github.com/wentf9/xops-mcp/internal/naming"
	"github.com/wentf9/xops-mcp/internal/storage"
	cryptoSSH "golang.org/x/crypto/ssh"
)

func digest(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func validText(value string) bool {
	return value != "" && len(value) <= 256 && !strings.ContainsAny(value, "\x00\r\n")
}

func ParseHostKey(text string) (cryptoSSH.PublicKey, error) {
	key, _, options, rest, err := cryptoSSH.ParseAuthorizedKey([]byte(text))
	if err != nil || len(options) > 0 || strings.TrimSpace(string(rest)) != "" {
		return nil, errors.New("host key must be one SSH public key without options")
	}
	if _, certificate := key.(*cryptoSSH.Certificate); certificate {
		return nil, errors.New("host certificates require explicit CA support; use a pinned raw host key")
	}
	return key, nil
}

func Snapshot(v storage.Inventory) (ports.OperationSnapshot, []storage.Source, error) {
	view := ports.OperationSnapshot{DomainID: v.DomainID, Revision: strconv.FormatUint(v.Revision, 10), Policy: policy.Clone(v.Policy), Targets: map[string]ports.Target{}, Selectors: map[string]string{}}
	view.Policy.AuditLog = ""
	if view.Policy.NoElicitFallback == "" {
		view.Policy.NoElicitFallback = "deny"
	}
	if err := guardrail.ValidateConfig(&view.Policy); err != nil {
		return view, nil, err
	}
	policyVersion, err := digest(view.Policy)
	if err != nil {
		return view, nil, err
	}
	view.PolicyRevision = policyVersion
	if v.DomainID == "" || len(v.Nodes) > 4096 {
		return view, nil, errors.New("inventory requires a deployment identity and at most 4096 nodes")
	}
	tagNames := map[string]bool{}
	for id, tag := range v.Tags {
		if err := naming.Validate(tag.Name); err != nil {
			return view, nil, fmt.Errorf("tag name: %w", err)
		}
		if id != tag.ID || !validText(id) || tagNames[tag.Name] {
			return view, nil, errors.New("invalid or duplicate tag identity/name")
		}
		tagNames[tag.Name] = true
	}
	for id, c := range v.Credentials {
		if err := naming.Validate(c.Name); err != nil {
			return view, nil, fmt.Errorf("credential name: %w", err)
		}
		if id != c.ID || !validText(id) || c.Version == "" || len(c.Ciphertext) == 0 || (c.Kind != "password" && c.Kind != "key") {
			return view, nil, errors.New("invalid credential metadata")
		}
	}
	for id, h := range v.Hosts {
		if err := naming.Validate(h.Name); err != nil {
			return view, nil, fmt.Errorf("host name: %w", err)
		}
		if id != h.ID || !validText(id) || !validText(h.Address) || strings.ContainsAny(h.Address, " /\\\t[]") || h.Port < 1 || h.Port > 65535 {
			return view, nil, errors.New("invalid host endpoint")
		}
		if h.HostKey != "" {
			if _, err := ParseHostKey(h.HostKey); err != nil {
				return view, nil, fmt.Errorf("host %q: %w", h.Name, err)
			}
		}
	}
	for id, i := range v.Identities {
		if err := naming.Validate(i.Name); err != nil {
			return view, nil, fmt.Errorf("identity name: %w", err)
		}
		if id != i.ID || !validText(id) || !validText(i.User) {
			return view, nil, errors.New("invalid SSH identity")
		}
		if _, exists := v.Credentials[i.CredentialID]; i.CredentialID != "" && !exists {
			return view, nil, errors.New("identity references missing credential")
		}
	}
	var sources []storage.Source
	hops := snapshotConfigs{}
	for id, n := range v.Nodes {
		if err := naming.Validate(n.Name); err != nil {
			return view, nil, fmt.Errorf("node name: %w", err)
		}
		if id != n.ID || !validText(id) || v.Deleted[id] {
			return view, nil, errors.New("invalid or deleted node identity")
		}
		h, hOK := v.Hosts[n.HostID]
		i, iOK := v.Identities[n.IdentityID]
		if !hOK || !iOK {
			return view, nil, fmt.Errorf("node %q has missing host or identity", n.Name)
		}
		c := v.Credentials[i.CredentialID]
		if !n.Disabled && (h.HostKey == "" || c.ID == "") {
			return view, nil, fmt.Errorf("node %q must be disabled until credential and host key are configured", n.Name)
		}
		mode := ssh.SudoMode(n.SudoMode)
		switch mode {
		case "":
			mode = ssh.SudoModeNone
		case ssh.SudoModeNone, ssh.SudoModeRoot, ssh.SudoModeSudo, ssh.SudoModeSudoer, ssh.SudoModeSu:
		default:
			return view, nil, errors.New("server sudo mode must be explicit (none, root, sudo, sudoer, su)")
		}
		privilege := v.Credentials[n.PrivilegeCredentialID]
		if n.PrivilegeCredentialID != "" && (privilege.ID == "" || privilege.Kind != "password") {
			return view, nil, errors.New("privilege credential must be a password")
		}
		if mode == ssh.SudoModeSu && privilege.ID == "" && !n.Disabled {
			return view, nil, errors.New("su requires a configured privilege credential")
		}
		if mode == ssh.SudoModeSudo && privilege.ID == "" && c.Kind == "password" {
			privilege = c
		}
		authVersion, err := digest([]string{v.DomainID, id, c.ID, c.Kind, c.Version})
		if err != nil {
			return view, nil, err
		}
		sudoVersion, err := digest([]string{v.DomainID, id, string(mode), privilege.ID, privilege.Version})
		if err != nil {
			return view, nil, err
		}
		trustVersion, err := digest([]string{v.DomainID, id, h.HostKey})
		if err != nil {
			return view, nil, err
		}
		authType := c.Kind
		if authType == "" {
			authType = "password"
		}
		hop := ssh.ClientConfig{NodeID: id, Address: h.Address, Port: h.Port, User: i.User, AuthType: authType, AuthUpdateToken: authVersion, SudoMode: mode, SudoUpdateToken: sudoVersion, TrustVersion: trustVersion, ProxyJump: strings.Join(n.JumpIDs, ",")}
		if authType == "key" {
			hop.KeyRef = c.ID
		}
		hops[id] = hop
		for _, source := range []storage.Source{
			{Token: authVersion, Purpose: "auth", CredentialID: c.ID, Version: c.Version},
			{Token: sudoVersion, Purpose: "privilege", CredentialID: privilege.ID, Version: privilege.Version},
			{Token: trustVersion, Purpose: "trust", HostKey: h.HostKey},
		} {
			source.NodeID, source.Host, source.Port, source.User = id, h.Address, h.Port, i.User
			sources = append(sources, source)
		}
		aliases := append([]string{n.Name}, n.Aliases...)
		slices.Sort(aliases)
		aliases = slices.Compact(aliases)
		for _, alias := range aliases {
			if err := naming.Validate(alias); err != nil {
				return view, nil, fmt.Errorf("node alias: %w", err)
			}
			if other, exists := view.Selectors[alias]; exists && other != id {
				return view, nil, fmt.Errorf("ambiguous node selector %q", alias)
			}
			if _, canonical := v.Nodes[alias]; canonical && alias != id {
				return view, nil, errors.New("alias conflicts with a node ID")
			}
			view.Selectors[alias] = id
		}
		var tags []string
		for _, tagID := range n.TagIDs {
			tag, exists := v.Tags[tagID]
			if !exists {
				return view, nil, errors.New("node references missing tag")
			}
			tags = append(tags, tag.Name)
		}
		slices.Sort(tags)
		tags = slices.Compact(tags)
		if len(tags) == 0 {
			tags = nil
		}
		view.Targets[id] = ports.Target{Info: ports.NodeInfo{ID: id, Alias: aliases, Tags: tags, Address: net.JoinHostPort(h.Address, strconv.Itoa(h.Port)), User: i.User, AuthType: authType, ProxyJump: hop.ProxyJump}, Disabled: n.Disabled, Version: "1"}
	}
	for id, target := range view.Targets {
		// CapturePlan resolves only this bounded, immutable in-memory map. It
		// performs no database, credential, environment, or network I/O and owns
		// expansion of explicit SSH jump chains and recursive single jumps.
		plan, err := ssh.CapturePlan(context.Background(), hops, id, v.DomainID)
		if err != nil {
			return view, nil, fmt.Errorf("capture node %q connection plan: %w", target.Info.ID, err)
		}
		target.Plan = plan
		view.Targets[id] = target
	}
	if _, err := view.Digest(); err != nil {
		return view, nil, err
	}
	return view, sources, nil
}

type snapshotConfigs map[string]ssh.ClientConfig

func (s snapshotConfigs) GetConfig(id string) (*ssh.ClientConfig, error) {
	cfg, ok := s[id]
	if !ok {
		return nil, fmt.Errorf("jump node %q does not exist", id)
	}
	return &cfg, nil
}
