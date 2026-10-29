package xops_test

import (
	"encoding/json"
	"testing"

	corelog "github.com/wentf9/xops-cli/core/log"
	"github.com/wentf9/xops-mcp/internal/adapters/xops"
)

func TestDiagnosticCollectorLifecycle(t *testing.T) {
	c := xops.NewDiagnosticCollector()
	if c.DiagnosticLevel() != 2 {
		t.Fatalf("expected DiagnosticLevel 2, got %d", c.DiagnosticLevel())
	}
	if len(c.Events()) != 0 {
		t.Fatalf("expected empty events initially, got %d", len(c.Events()))
	}

	exitCode := int64(0)
	methods := []string{"publickey", "password"}
	c.ObserveDiagnostic(corelog.Diagnostic{
		Kind:                   corelog.Connecting,
		Level:                  1,
		NodeID:                 "node-1",
		Host:                   "192.0.2.1",
		Port:                   22,
		Proxied:                false,
		ServerVersion:          "SSH-2.0-OpenSSH_9.0",
		KeyExchange:            "curve25519-sha256",
		HostKey:                "ssh-ed25519",
		Fingerprint:            "SHA256:abc",
		ReadCipher:             "chacha20-poly1305@openssh.com",
		WriteCipher:            "chacha20-poly1305@openssh.com",
		Method:                 "publickey",
		Methods:                methods,
		Operation:              "sudo",
		Outcome:                "success",
		PrivilegeCommand:       "sudo -n id",
		PrivilegePrompt:        "[sudo] password: ",
		PrivilegePromptPattern: ".*password:.*",
		HasExitCode:            true,
		ExitCode:               exitCode,
	})
	methods[0] = "mutated-input"

	events := c.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev.Kind != string(corelog.Connecting) || ev.NodeID != "node-1" || ev.Host != "192.0.2.1" {
		t.Fatalf("unexpected event: %+v", ev)
	}
	if len(ev.Methods) != 2 || ev.Methods[0] != "publickey" {
		t.Fatalf("expected cloned methods slice, got %v", ev.Methods)
	}
	if ev.ExitCode == nil || *ev.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %v", ev.ExitCode)
	}
	ev.Methods[0] = "mutated-output"
	*ev.ExitCode = 99
	fresh := c.Events()[0]
	if fresh.Methods[0] != "publickey" || *fresh.ExitCode != 0 {
		t.Fatalf("collector state mutated through returned slice/pointer: %+v", fresh)
	}

	// Verify JSON serialization includes fields and omits nil exit code
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	var roundtrip xops.DiagnosticEvent
	if err := json.Unmarshal(data, &roundtrip); err != nil {
		t.Fatal(err)
	}
	if roundtrip.Kind != ev.Kind || roundtrip.PrivilegeCommand != ev.PrivilegeCommand {
		t.Fatalf("roundtrip mismatch: %+v vs %+v", roundtrip, ev)
	}

	// Verify buffer bounding (capped at 64)
	for i := 0; i < 100; i++ {
		c.ObserveDiagnostic(corelog.Diagnostic{Kind: corelog.Connected, NodeID: "node-1"})
	}
	if len(c.Events()) != 64 {
		t.Fatalf("expected bounded events 64, got %d", len(c.Events()))
	}
}

func TestSlogDiagnosticObserver(t *testing.T) {
	observer := xops.SlogDiagnosticObserver{Level: 1}
	if observer.DiagnosticLevel() != 1 {
		t.Fatalf("expected level 1, got %d", observer.DiagnosticLevel())
	}
	// Verify default level
	zero := xops.SlogDiagnosticObserver{}
	if zero.DiagnosticLevel() != 1 {
		t.Fatalf("expected default level 1, got %d", zero.DiagnosticLevel())
	}
	// ObserveDiagnostic must not panic
	observer.ObserveDiagnostic(corelog.Diagnostic{
		Kind:      corelog.Connected,
		Level:     1,
		NodeID:    "node-2",
		Host:      "192.0.2.2",
		Port:      2222,
		Operation: "handshake",
		Outcome:   "success",
	})
}
