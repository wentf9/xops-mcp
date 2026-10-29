package xops_test

import (
	"strings"
	"testing"

	"github.com/wentf9/xops-cli/core/ssh"
	"github.com/wentf9/xops-mcp/internal/adapters/xops"
	"github.com/wentf9/xops-mcp/internal/storage"
)

func sampleInventory() storage.Inventory {
	return storage.Inventory{
		DomainID: "domain-1",
		Revision: 1,
		Hosts: map[string]storage.Host{
			"host-1": {ID: "host-1", Name: "host-1", Address: "192.0.2.1", Port: 22, HostKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIG8Q7v+P8qI0QxYl6mFz7v6R4wB9V3gL3yV8xP4k8q1a"},
		},
		Identities: map[string]storage.Identity{
			"id-1": {ID: "id-1", Name: "id-1", User: "root", CredentialID: "cred-1"},
		},
		Credentials: map[string]storage.Credential{
			"cred-1": {ID: "cred-1", Name: "cred-1", Kind: "password", Version: "1", Ciphertext: []byte("cipher")},
		},
		Nodes: map[string]storage.Node{
			"node-1": {
				ID:                    "node-1",
				Name:                  "node-1",
				HostID:                "host-1",
				IdentityID:            "id-1",
				SudoMode:              "sudo",
				PrivilegeCredentialID: "cred-1",
			},
		},
	}
}

func TestSnapshotPasswordPromptPattern(t *testing.T) {
	inv := sampleInventory()
	node := inv.Nodes["node-1"]
	node.PasswordPromptPattern = `.*\[sudo\] password:.*`
	inv.Nodes["node-1"] = node

	view1, sources1, err := xops.Snapshot(inv)
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	target1 := view1.Targets["node-1"]
	if len(target1.Plan.Hops) == 0 {
		t.Fatal("expected hops in target plan")
	}
	hop := target1.Plan.Hops[0]
	if hop.PasswordPromptPattern != `.*\[sudo\] password:.*` {
		t.Fatalf("expected hop pattern %q, got %q", `.*\[sudo\] password:.*`, hop.PasswordPromptPattern)
	}
	key1, err := ssh.ConnectionKey(target1.Plan)
	if err != nil {
		t.Fatalf("connection key 1: %v", err)
	}
	digest1, err := view1.Digest()
	if err != nil {
		t.Fatalf("digest 1: %v", err)
	}

	inv2 := sampleInventory()
	node2 := inv2.Nodes["node-1"]
	node2.PasswordPromptPattern = `.*Password:.*`
	inv2.Nodes["node-1"] = node2
	view2, sources2, err := xops.Snapshot(inv2)
	if err != nil {
		t.Fatalf("snapshot 2 failed: %v", err)
	}
	target2 := view2.Targets["node-1"]
	key2, err := ssh.ConnectionKey(target2.Plan)
	if err != nil {
		t.Fatalf("connection key 2: %v", err)
	}
	digest2, err := view2.Digest()
	if err != nil {
		t.Fatalf("digest 2: %v", err)
	}
	if key1 == key2 {
		t.Fatal("connection key did not change when password prompt pattern changed")
	}
	if digest1 == digest2 {
		t.Fatal("snapshot digest did not change when password prompt pattern changed")
	}

	// Verify privilege source token remains stable across prompt pattern edits
	// so existing stored sources and archives stay valid across schema upgrades.
	var sudoToken1, sudoToken2 string
	for _, s := range sources1 {
		if s.Purpose == "privilege" {
			sudoToken1 = s.Token
			break
		}
	}
	for _, s := range sources2 {
		if s.Purpose == "privilege" {
			sudoToken2 = s.Token
			break
		}
	}
	if sudoToken1 == "" || sudoToken1 != sudoToken2 {
		t.Fatalf("expected identical privilege source token across prompt edits, got %q vs %q", sudoToken1, sudoToken2)
	}
}

func TestSnapshotRejectsInvalidPasswordPromptPattern(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
	}{
		{"invalid regex", `[a-z`},
		{"empty match", `.*`},
		{"empty match question mark", `a?`},
		{"contains newline", "password:\n"},
		{"contains null", "pass\x00word:"},
		{"too long", strings.Repeat("a", 257)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inv := sampleInventory()
			node := inv.Nodes["node-1"]
			node.PasswordPromptPattern = tc.pattern
			inv.Nodes["node-1"] = node

			_, _, err := xops.Snapshot(inv)
			if err == nil {
				t.Fatalf("expected snapshot error for pattern %q, got nil", tc.pattern)
			}
		})
	}
}
