// Package importer provides one-way, explicit inventory migration. It never
// loads personal CLI vaults, private-key paths, agents, or known_hosts files.
package importer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"path"
	"slices"
	"strconv"

	"github.com/wentf9/xops-cli/core/mcp/policy"
	"github.com/wentf9/xops-mcp/internal/adapters/xops"
	"github.com/wentf9/xops-mcp/internal/secure"
	"github.com/wentf9/xops-mcp/internal/storage"
	cryptoSSH "golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"
)

type Document struct {
	Version     int                   `yaml:"version"`
	Hosts       map[string]Host       `yaml:"hosts"`
	Identities  map[string]Identity   `yaml:"identities"`
	Nodes       map[string]Node       `yaml:"nodes"`
	Credentials map[string]Credential `yaml:"credentials"`
	Policy      *policy.Config        `yaml:"policy,omitempty"`
}
type Host struct {
	Address string `yaml:"address"`
	Port    int    `yaml:"port"`
	HostKey string `yaml:"host_key"`
}
type Identity struct {
	User       string `yaml:"user"`
	Credential string `yaml:"credential"`
}
type Node struct {
	Host                string   `yaml:"host"`
	Identity            string   `yaml:"identity"`
	ProxyJump           string   `yaml:"proxy_jump"`
	Aliases             []string `yaml:"aliases"`
	Tags                []string `yaml:"tags"`
	Disabled            bool     `yaml:"disabled"`
	SudoMode            string   `yaml:"sudo_mode"`
	PrivilegeCredential string   `yaml:"privilege_credential"`
}
type Credential struct {
	Kind       string `yaml:"kind"`
	Password   string `yaml:"password"`
	PrivateKey string `yaml:"private_key"`
	Passphrase string `yaml:"passphrase"`
}
type Options struct{ IncludeSecrets, Replace bool }
type Report struct {
	Revision          uint64            `json:"expected_revision"`
	NodeIDs           map[string]string `json:"node_ids"`
	Removed           []string          `json:"removed_node_ids,omitempty"`
	CredentialChanges []string          `json:"credential_changes,omitempty"`
	Warnings          []string          `json:"warnings,omitempty"`
	Conflicts         []string          `json:"conflicts,omitempty"`
}

func decode(data []byte, target any) error {
	if len(data) > 4<<20 {
		return errors.New("inventory exceeds 4 MiB")
	}
	d := yaml.NewDecoder(bytes.NewReader(data))
	d.KnownFields(true)
	if err := d.Decode(target); err != nil {
		return errors.New("invalid inventory YAML or unsupported field")
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("inventory must contain exactly one YAML document")
	}
	return nil
}

func Decode(data []byte, format string) (Document, []string, error) {
	if format == "xops-cli" {
		return decodeCLI(data)
	}
	if format != "server" {
		return Document{}, nil, errors.New("import format must be server or xops-cli")
	}
	var d Document
	if err := decode(data, &d); err != nil {
		return d, nil, err
	}
	if d.Version != 1 {
		return d, nil, errors.New("server inventory version must be 1")
	}
	return d, nil, nil
}

// New mappings are deterministic for a deployment and expected revision, so a
// reviewed dry-run and its corresponding apply produce identical opaque IDs.
func newID(base storage.Inventory, kind, name string) string {
	sum := sha256.Sum256([]byte(base.DomainID + "\x00" + strconv.FormatUint(base.Revision, 10) + "\x00" + kind + "\x00" + name))
	return hex.EncodeToString(sum[:16])
}

func Plan(base storage.Inventory, doc Document, vault *secure.Vault, options Options) (candidate storage.Inventory, report Report, retErr error) {
	report = Report{Revision: base.Revision, NodeIDs: map[string]string{}}
	defer func() {
		if retErr != nil {
			report.Conflicts = append(report.Conflicts, retErr.Error())
		}
	}()
	candidate = base
	candidate.Hosts = maps.Clone(base.Hosts)
	candidate.Identities = maps.Clone(base.Identities)
	candidate.Nodes = maps.Clone(base.Nodes)
	candidate.Credentials = maps.Clone(base.Credentials)
	candidate.Deleted = maps.Clone(base.Deleted)
	if options.Replace {
		candidate.Hosts = map[string]storage.Host{}
		candidate.Identities = map[string]storage.Identity{}
		candidate.Nodes = map[string]storage.Node{}
	}
	hosts, identities, nodes, credentials := map[string]string{}, map[string]string{}, map[string]string{}, map[string]string{}
	for id, v := range base.Hosts {
		hosts[v.Name] = id
	}
	for id, v := range base.Identities {
		identities[v.Name] = id
	}
	for id, v := range base.Nodes {
		nodes[v.Name] = id
	}
	for id, v := range base.Credentials {
		credentials[v.Name] = id
	}
	for name := range doc.Hosts {
		if hosts[name] == "" {
			hosts[name] = newID(base, "host", name)
		}
	}
	for name := range doc.Identities {
		if identities[name] == "" {
			identities[name] = newID(base, "identity", name)
		}
	}
	for name := range doc.Nodes {
		if nodes[name] == "" {
			nodes[name] = newID(base, "node", name)
		}
	}
	if len(doc.Credentials) > 0 && !options.IncludeSecrets {
		return candidate, report, errors.New("credential import requires --include-secrets")
	}
	for _, name := range slices.Sorted(maps.Keys(doc.Credentials)) {
		input := doc.Credentials[name]
		if credentials[name] == "" {
			credentials[name] = newID(base, "credential", name)
		}
		material := secure.Material{Password: []byte(input.Password), PrivateKey: []byte(input.PrivateKey), Passphrase: []byte(input.Passphrase)}
		if err := validateMaterial(input.Kind, material); err != nil {
			material.Clear()
			return candidate, report, fmt.Errorf("credential %q: %w", name, err)
		}
		c := storage.Credential{ID: credentials[name], Name: name, Kind: input.Kind, Version: secure.ID()}
		ciphertext, err := vault.Encrypt(base.DomainID, c.ID, c.Kind, c.Version, material)
		material.Clear()
		if err != nil {
			return candidate, report, err
		}
		c.Ciphertext = ciphertext
		candidate.Credentials[c.ID] = c
		report.CredentialChanges = append(report.CredentialChanges, name)
	}
	for name, input := range doc.Hosts {
		if input.Port == 0 {
			input.Port = 22
		}
		if input.HostKey != "" {
			key, err := xops.ParseHostKey(input.HostKey)
			if err != nil {
				return candidate, report, fmt.Errorf("host %q: %w", name, err)
			}
			input.HostKey = string(cryptoSSH.MarshalAuthorizedKey(key))
		}
		id := hosts[name]
		candidate.Hosts[id] = storage.Host{ID: id, Name: name, Address: input.Address, Port: input.Port, HostKey: input.HostKey}
	}
	for name, input := range doc.Identities {
		if input.Credential != "" && credentials[input.Credential] == "" {
			return candidate, report, fmt.Errorf("identity %q references a missing credential", name)
		}
		id := identities[name]
		candidate.Identities[id] = storage.Identity{ID: id, Name: name, User: input.User, CredentialID: credentials[input.Credential]}
	}
	for _, name := range slices.Sorted(maps.Keys(doc.Nodes)) {
		input := doc.Nodes[name]
		id := nodes[name]
		if hosts[input.Host] == "" || identities[input.Identity] == "" {
			return candidate, report, fmt.Errorf("node %q references missing host or identity", name)
		}
		var jumps []string
		for _, selector := range jumpSelectors(input.ProxyJump) {
			if nodes[selector] == "" {
				return candidate, report, fmt.Errorf("node %q references missing jump %q", name, selector)
			}
			jumps = append(jumps, nodes[selector])
		}
		if input.PrivilegeCredential != "" && credentials[input.PrivilegeCredential] == "" {
			return candidate, report, fmt.Errorf("node %q references missing privilege credential", name)
		}
		aliases, tags := slices.Clone(input.Aliases), slices.Clone(input.Tags)
		slices.Sort(aliases)
		slices.Sort(tags)
		n := storage.Node{ID: id, Name: name, HostID: hosts[input.Host], IdentityID: identities[input.Identity], JumpIDs: jumps, Aliases: slices.Compact(aliases), Tags: slices.Compact(tags), Disabled: input.Disabled, SudoMode: input.SudoMode, PrivilegeCredentialID: credentials[input.PrivilegeCredential]}
		if candidate.Hosts[n.HostID].HostKey == "" || candidate.Identities[n.IdentityID].CredentialID == "" {
			n.Disabled = true
			report.Warnings = append(report.Warnings, fmt.Sprintf("node %q imported disabled: credential or host key is not configured", name))
		}
		candidate.Nodes[id] = n
		report.NodeIDs[name] = id
		for _, alias := range aliases {
			report.NodeIDs[alias] = id
		}
	}
	for id := range base.Nodes {
		if _, exists := candidate.Nodes[id]; !exists {
			candidate.Deleted[id] = true
			report.Removed = append(report.Removed, id)
		}
	}
	slices.Sort(report.Removed)
	if doc.Policy != nil {
		candidate.Policy = policy.Clone(*doc.Policy)
		candidate.Policy.AuditLog = ""
		candidate.Policy.NodeOverrides = map[string]policy.NodeConfig{}
		// Translate selector patterns at import, never mix old CLI selectors
		// with the stable server IDs seen by the policy evaluator.
		for _, pattern := range slices.Sorted(maps.Keys(doc.Policy.NodeOverrides)) {
			rule := doc.Policy.NodeOverrides[pattern]
			matched := false
			for id, n := range candidate.Nodes {
				for _, selector := range append([]string{id, n.Name}, n.Aliases...) {
					ok, err := path.Match(pattern, selector)
					if err != nil {
						return candidate, report, errors.New("invalid policy node pattern")
					}
					if ok {
						if previous, exists := candidate.Policy.NodeOverrides[id]; exists && previous != rule {
							return candidate, report, errors.New("overlapping policy patterns have conflicting rules")
						}
						candidate.Policy.NodeOverrides[id] = rule
						matched = true
						break
					}
				}
			}
			if !matched {
				return candidate, report, fmt.Errorf("policy pattern %q matches no imported node", pattern)
			}
		}
	}
	if _, _, err := xops.Snapshot(candidate); err != nil {
		return candidate, report, err
	}
	return candidate, report, nil
}

func validateMaterial(kind string, material secure.Material) error {
	switch kind {
	case "password":
		if len(material.Password) == 0 || len(material.Password) > 64<<10 || len(material.PrivateKey) > 0 || len(material.Passphrase) > 0 {
			return errors.New("password material must contain only a nonempty password up to 64 KiB")
		}
	case "key":
		if len(material.Password) > 0 || len(material.PrivateKey) == 0 || len(material.PrivateKey) > 128<<10 || len(material.Passphrase) > 64<<10 {
			return errors.New("invalid private key material size or fields")
		}
		var err error
		if len(material.Passphrase) > 0 {
			_, err = cryptoSSH.ParsePrivateKeyWithPassphrase(material.PrivateKey, material.Passphrase)
		} else {
			_, err = cryptoSSH.ParsePrivateKey(material.PrivateKey)
		}
		if err != nil {
			return errors.New("private key or passphrase is invalid")
		}
	default:
		return errors.New("credential kind must be password or key")
	}
	return nil
}
