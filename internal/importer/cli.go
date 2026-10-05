package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/wentf9/xops-cli/core/mcp/policy"
	"gopkg.in/yaml.v3"
)

// These are migration DTOs for the serialized format, not copies of upstream
// application behavior. CLI-only stores and transport settings are ignored.
type cliDocument struct {
	SchemaVersion         int            `yaml:"schema_version"`
	Credential            yaml.Node      `yaml:"credential"`
	MCP                   yaml.Node      `yaml:"mcp"`
	PasswordPromptPattern string         `yaml:"password_prompt_pattern"`
	Guardrail             *policy.Config `yaml:"guardrail"`
	Hosts                 map[string]struct {
		Alias   []string `yaml:"alias"`
		Address string   `yaml:"address"`
		Port    int      `yaml:"port"`
	} `yaml:"hosts"`
	Identities map[string]struct {
		User             string    `yaml:"user"`
		AuthType         string    `yaml:"auth_type"`
		KeyPath          string    `yaml:"key_path"`
		KeyFingerprint   string    `yaml:"key_fingerprint"`
		Password         string    `yaml:"password"`
		Passphrase       string    `yaml:"passphrase"`
		LoginPasswordRef yaml.Node `yaml:"login_password_ref"`
		PassphraseRef    yaml.Node `yaml:"passphrase_ref"`
	} `yaml:"identities"`
	Nodes map[string]struct {
		HostRef               string    `yaml:"host_ref"`
		IdentityRef           string    `yaml:"identity_ref"`
		ProxyJump             string    `yaml:"proxy_jump"`
		Alias                 []string  `yaml:"alias"`
		Tags                  []string  `yaml:"tags"`
		SudoMode              string    `yaml:"sudo_mode"`
		SuPwd                 string    `yaml:"su_pwd"`
		PrivilegePasswordRef  yaml.Node `yaml:"privilege_password_ref"`
		PasswordPromptPattern string    `yaml:"password_prompt_pattern"`
	} `yaml:"nodes"`
}

func decodeCLI(data []byte) (Document, []string, error) {
	var source cliDocument
	if err := decode(data, &source); err != nil {
		return Document{}, nil, err
	}
	if source.SchemaVersion < 0 || source.SchemaVersion > 2 {
		return Document{}, nil, errors.New("unsupported xops-cli schema version")
	}
	if source.PasswordPromptPattern != "" {
		return Document{}, nil, errors.New("custom CLI password prompt patterns require a separate server adaptation")
	}
	d := Document{Version: 1, Hosts: map[string]Host{}, Identities: map[string]Identity{}, Nodes: map[string]Node{}, Credentials: map[string]Credential{}, Policy: source.Guardrail}
	warnings := []string{"CLI credential stores, key paths, SSH agent, known_hosts, and MCP listener settings are not imported; host keys must be pinned separately"}
	for name, h := range source.Hosts {
		d.Hosts[name] = Host{Address: h.Address, Port: h.Port}
	}
	for _, name := range slices.Sorted(maps.Keys(source.Identities)) {
		i := source.Identities[name]
		identity := Identity{User: i.User}
		if source.SchemaVersion == 2 && (i.Password != "" || i.Passphrase != "") {
			return d, nil, errors.New("schema v2 must not contain plaintext credentials")
		}
		if i.Password != "" && (i.AuthType == "password" || i.AuthType == "auto" || i.AuthType == "") {
			identity.Credential = cliCredentialName("login", name)
			d.Credentials[identity.Credential] = Credential{Kind: "password", Password: i.Password}
		} else {
			warnings = append(warnings, fmt.Sprintf("identity %q requires a server-owned credential", name))
		}
		d.Identities[name] = identity
	}
	for name, n := range source.Nodes {
		if n.PasswordPromptPattern != "" {
			return d, nil, errors.New("custom CLI password prompt patterns are not supported by this importer")
		}
		mode := n.SudoMode
		if mode == "auto" {
			mode = "none"
			warnings = append(warnings, fmt.Sprintf("node %q: automatic privilege detection replaced by none; configure an explicit mode", name))
		}
		node := Node{Host: n.HostRef, Identity: n.IdentityRef, ProxyJump: n.ProxyJump, Aliases: n.Alias, Tags: n.Tags, SudoMode: mode, Disabled: true}
		if source.SchemaVersion == 2 && n.SuPwd != "" {
			return d, nil, errors.New("schema v2 must not contain plaintext privilege credentials")
		}
		if n.SuPwd != "" {
			node.PrivilegeCredential = cliCredentialName("privilege", name)
			d.Credentials[node.PrivilegeCredential] = Credential{Kind: "password", Password: n.SuPwd}
		}
		d.Nodes[name] = node
	}
	// Normalize selectors before resolving database IDs. Keep explicit chains
	// ordered; do not rewrite a shared jump's own standalone route.
	aliases := make(map[string][]string)
	for name, node := range source.Nodes {
		for _, alias := range node.Alias {
			aliases[alias] = append(aliases[alias], name)
		}
	}
	for name, node := range d.Nodes {
		var normalized []string
		for _, selector := range jumpSelectors(node.ProxyJump) {
			canonical := selector
			if _, exists := source.Nodes[selector]; !exists {
				matches := slices.Compact(slices.Sorted(slices.Values(aliases[selector])))
				if len(matches) != 1 {
					return d, nil, fmt.Errorf("node %q references missing or ambiguous jump %q", name, selector)
				}
				canonical = matches[0]
			}
			normalized = append(normalized, canonical)
		}
		node.ProxyJump = strings.Join(normalized, ",")
		d.Nodes[name] = node
	}
	slices.Sort(warnings)
	return d, warnings, nil
}

// Purpose keeps login and privilege material separate; the digest gives even
// maximum-length Unicode source names a bounded, valid, deterministic name.
func cliCredentialName(purpose, name string) string {
	sum := sha256.Sum256([]byte(name))
	return purpose + "-" + hex.EncodeToString(sum[:])
}

func jumpSelectors(raw string) []string {
	var selectors []string
	for item := range strings.SplitSeq(raw, ",") {
		if item = strings.TrimSpace(item); item != "" {
			selectors = append(selectors, item)
		}
	}
	return selectors
}
