package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wentf9/xops-cli/core/mcp/ports"
	"github.com/wentf9/xops-cli/core/ssh"
	"github.com/wentf9/xops-cli/core/testutil/sshfixture"
	"github.com/wentf9/xops-mcp/internal/adapters/xops"
	"github.com/wentf9/xops-mcp/internal/service"
	"github.com/wentf9/xops-mcp/internal/testutil"
	cryptoSSH "golang.org/x/crypto/ssh"
)

func TestNodePrivilegeTestAPI(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	execHandler := func(cmd string, ch cryptoSSH.Channel) {
		switch {
		case strings.Contains(cmd, "__XOPS_INTERP_END__"):
			_, _ = io.WriteString(ch, "__XOPS_INTERP__:sh\n__XOPS_INTERP_END__\n")
		case strings.Contains(cmd, "__XOPS_DIALECT_POSIX__"):
			_, _ = io.WriteString(ch, "__XOPS_DIALECT_POSIX__:Linux:/bin/sh\n")
		case strings.Contains(cmd, "id -u") && strings.Contains(cmd, "id -un"):
			_, _ = io.WriteString(ch, "0\nroot\n")
		case strings.Contains(cmd, "id -u"):
			_, _ = io.WriteString(ch, "0\n")
		case strings.Contains(cmd, "id -un"):
			_, _ = io.WriteString(ch, "root\n")
		case strings.Contains(cmd, "sudo -n true"):
			// Sudo without password succeeds
		default:
			_, _ = io.WriteString(ch, "fixture-output\n")
		}
		_, _ = ch.SendRequest("exit-status", false, cryptoSSH.Marshal(struct{ Status uint32 }{0}))
	}

	peer, err := sshfixture.NewWithOptions(ctx, sshfixture.Options{ExecHandler: execHandler})
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, peer)

	cfg := adminConfig(t)
	p := newAdminPeer(t, ctx, cfg)
	p.initialize()

	host, portStr, err := net.SplitHostPort(peer.Address)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}

	hostID := p.save("hosts", service.HostInput{Name: "host", Address: host, Port: port})
	credID := p.save("credentials", service.CredentialInput{Name: "login", Kind: "password", Secret: &service.MaterialInput{Password: sshfixture.Password}})
	identityID := p.save("identities", service.IdentityInput{Name: "operator", User: "fixture", CredentialID: credID})

	// Setup and trust host key
	probe := p.request("POST", "/api/v1/hosts/"+hostID+"/probe", map[string]string{"algorithm": "ssh-ed25519"}, 200, nil)
	var observed struct {
		ProbeID string `json:"probeID"`
	}
	if err := json.Unmarshal(probe, &observed); err != nil {
		t.Fatal(err)
	}
	p.request("POST", "/api/v1/hosts/"+hostID+"/trust", map[string]string{"probeID": observed.ProbeID}, 200, nil)

	// Create disabled node
	node := service.NodeInput{Name: "node-priv", HostID: hostID, IdentityID: identityID, Disabled: true, SudoMode: "root"}
	nodeID := p.save("nodes", node)

	// Test privilege on disabled node -> should fail with 409 (node_disabled)
	p.request("POST", "/api/v1/nodes/"+nodeID+"/test-privilege", map[string]any{}, 409, nil)

	// Enable node
	node.Disabled = false
	p.request("PUT", "/api/v1/nodes/"+nodeID, node, 200, nil)

	// Test privilege on enabled root node -> 200
	resp := p.request("POST", "/api/v1/nodes/"+nodeID+"/test-privilege", map[string]any{}, 200, nil)
	var result struct {
		Escalated   bool                   `json:"escalated"`
		SudoMode    string                 `json:"sudoMode"`
		User        string                 `json:"user"`
		ElapsedMS   int64                  `json:"elapsedMS"`
		Diagnostics []xops.DiagnosticEvent `json:"diagnostics,omitempty"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Escalated || result.SudoMode != "root" || result.User != "root" {
		t.Fatalf("unexpected privilege test result: %+v", result)
	}

	// Test with sudoMode "none" (probes passwordless sudo)
	node.SudoMode = "none"
	p.request("PUT", "/api/v1/nodes/"+nodeID, node, 200, nil)
	resp = p.request("POST", "/api/v1/nodes/"+nodeID+"/test-privilege", map[string]any{}, 200, nil)
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Escalated || result.User == "" {
		t.Fatalf("unexpected probe result for none mode: %+v", result)
	}

	// Verify audit log has admin.privilege_test
	auditData := p.request("GET", "/api/v1/audit?nodeID="+nodeID, nil, 200, nil)
	var auditResp struct {
		Entries []struct {
			ID    int64            `json:"id"`
			Event ports.AuditEvent `json:"event"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(auditData, &auditResp); err != nil {
		t.Fatal(err)
	}
	if len(auditResp.Entries) == 0 {
		t.Fatal("expected audit entries for node")
	}
	found := false
	for _, entry := range auditResp.Entries {
		if entry.Event.Tool == "admin.privilege_test" && entry.Event.NodeID == nodeID && entry.Event.Outcome == "executed" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected executed admin.privilege_test event in audit, got %+v", auditResp.Entries)
	}

	// Verify conflict with stale revision
	p.request("POST", "/api/v1/nodes/"+nodeID+"/test-privilege", map[string]any{}, 412, func(r *http.Request) {
		r.Header.Set("If-Match", "\"0\"")
	})

	// Test privilege-probe on a brand new draft node (NodeID: "")
	probeResp := p.request("POST", "/api/v1/nodes/privilege-probe", map[string]any{
		"nodeID":     "",
		"hostID":     hostID,
		"identityID": identityID,
		"jumpIDs":    []string{},
		"sudoMode":   "root",
	}, 200, nil)
	var probeResult struct {
		Escalated bool   `json:"escalated"`
		SudoMode  string `json:"sudoMode"`
		User      string `json:"user"`
		ElapsedMS int64  `json:"elapsedMS"`
	}
	if err := json.Unmarshal(probeResp, &probeResult); err != nil {
		t.Fatal(err)
	}
	if !probeResult.Escalated || probeResult.SudoMode != "root" || probeResult.User != "root" {
		t.Fatalf("unexpected new node privilege probe result: %+v", probeResult)
	}

	// Test privilege-probe on new draft node with sudoMode "none"
	probeResp = p.request("POST", "/api/v1/nodes/privilege-probe", map[string]any{
		"nodeID":                "",
		"hostID":                hostID,
		"identityID":            identityID,
		"jumpIDs":               []string{},
		"sudoMode":              "none",
		"passwordPromptPattern": `.*password:.*`,
	}, 200, nil)
	if err := json.Unmarshal(probeResp, &probeResult); err != nil {
		t.Fatal(err)
	}

	// Test privilege-probe with invalid passwordPromptPattern -> 422
	p.request("POST", "/api/v1/nodes/privilege-probe", map[string]any{
		"nodeID":                "",
		"hostID":                hostID,
		"identityID":            identityID,
		"jumpIDs":               []string{},
		"sudoMode":              "none",
		"passwordPromptPattern": `[invalid`,
	}, 422, nil)

	// Save node with passwordPromptPattern and verify in inventory
	node.PasswordPromptPattern = `.*\[sudo\] password:.*`
	p.request("PUT", "/api/v1/nodes/"+nodeID, node, 200, nil)
	invData := p.request("GET", "/api/v1/inventory", nil, 200, nil)
	var inv struct {
		Nodes []struct {
			ID                    string `json:"id"`
			PasswordPromptPattern string `json:"passwordPromptPattern"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(invData, &inv); err != nil {
		t.Fatal(err)
	}
	var foundPattern string
	for _, n := range inv.Nodes {
		if n.ID == nodeID {
			foundPattern = n.PasswordPromptPattern
		}
	}
	if foundPattern != `.*\[sudo\] password:.*` {
		t.Fatalf("expected node passwordPromptPattern in inventory, got %q", foundPattern)
	}
	if !probeResult.Escalated || probeResult.User == "" {
		t.Fatalf("unexpected new node probe result for none mode: %+v", probeResult)
	}

	// Test privilege-probe with stale ETag -> 412
	p.request("POST", "/api/v1/nodes/privilege-probe", map[string]any{
		"nodeID":     "",
		"hostID":     hostID,
		"identityID": identityID,
		"jumpIDs":    []string{},
		"sudoMode":   "root",
	}, 412, func(r *http.Request) {
		r.Header.Set("If-Match", "\"0\"")
	})
}

// Exercise both saved permits and unsaved draft plans against real SSH channels.
func TestPrivilegeIdentityValidation(t *testing.T) {
	sh := &ssh.ExecutionConfig{Interpreter: ssh.InterpreterSh, LaunchDialect: ssh.LaunchPOSIX}
	for _, tc := range []struct {
		name, mode, loginOutput, sudoOutput string
		sudoStatus                          uint32
		noBash, probeFails                  bool
		execution                           *ssh.ExecutionConfig
		wantStatus                          int
		wantMode                            string
	}{
		{name: "automatic sh login root", mode: "none", noBash: true, loginOutput: "0\nroot\n", wantStatus: 200, wantMode: "root"},
		{name: "automatic sh passwordless root", mode: "none", noBash: true, loginOutput: "1000\nfixture\n", sudoOutput: "0\nroot\n", wantStatus: 200, wantMode: "sudoer"},
		{name: "automatic sh configured root", mode: "root", noBash: true, loginOutput: "0\nroot\n", wantStatus: 200, wantMode: "root"},
		{name: "automatic sh configured sudoer", mode: "sudoer", noBash: true, sudoOutput: "0\nroot\n", wantStatus: 200, wantMode: "sudoer"},
		{name: "empty execution probes sh", mode: "none", execution: &ssh.ExecutionConfig{}, noBash: true, loginOutput: "0\nroot\n", wantStatus: 200, wantMode: "root"},
		{name: "failed discovery stops diagnostics", mode: "none", probeFails: true, loginOutput: "0\nroot\n", wantStatus: 502},
		{name: "login root", mode: "none", loginOutput: "0\nroot\n", wantStatus: 200, wantMode: "root"},
		{name: "passwordless root", mode: "none", loginOutput: "1000\nfixture\n", sudoOutput: "0\nroot\n", wantStatus: 200, wantMode: "sudoer"},
		{name: "only true is permitted", mode: "none", loginOutput: "1000\nfixture\n", sudoStatus: 1, wantStatus: 422},
		{name: "sudo identity missing", mode: "none", loginOutput: "1000\nfixture\n", sudoOutput: "0\n", wantStatus: 502},
		{name: "sudo default is nonroot", mode: "none", loginOutput: "1000\nfixture\n", sudoOutput: "1001\noperator\n", wantStatus: 502},
		{name: "configured sudoer is nonroot", mode: "sudoer", sudoOutput: "1001\noperator\n", wantStatus: 502},
		{name: "configured root is nonroot", mode: "root", loginOutput: "1000\nfixture\n", wantStatus: 502},
		{name: "configured sudoer empty output", mode: "sudoer", wantStatus: 502},
		{name: "configured sudoer root with banner", mode: "sudoer", sudoOutput: "fixture banner\n0\nroot\n", wantStatus: 200, wantMode: "sudoer"},
		{name: "sh login root without bash", mode: "none", execution: sh, loginOutput: "0\nroot\n", wantStatus: 200, wantMode: "root"},
		{name: "sh passwordless root without bash", mode: "none", execution: sh, loginOutput: "1000\nfixture\n", sudoOutput: "0\nroot\n", wantStatus: 200, wantMode: "sudoer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			peer, err := sshfixture.NewWithOptions(ctx, sshfixture.Options{ExecHandler: func(command string, channel cryptoSSH.Channel) {
				output, status := tc.loginOutput, uint32(0)
				if strings.Contains(command, "sudo") {
					output, status = tc.sudoOutput, tc.sudoStatus
					if strings.Contains(command, "sudo -n true") {
						output, status = "", 0
					}
				}
				switch {
				case strings.Contains(command, "__XOPS_INTERP_END__"):
					output = "__XOPS_INTERP__:sh\n__XOPS_INTERP_END__\n"
				case strings.Contains(command, "__XOPS_DIALECT_POSIX__"):
					output = "__XOPS_DIALECT_POSIX__:Linux:/bin/sh\n"
				default:
					if tc.probeFails && !strings.Contains(command, "__XOPS_") {
						t.Error("identity command ran after failed environment discovery")
					}
				}
				if strings.Contains(command, "__XOPS_") {
					if !ssh.NeedsExecutionProbe(tc.execution) {
						t.Error("explicit execution settings unexpectedly triggered discovery")
					}
					if tc.probeFails {
						output = "invalid-probe-frame"
					}
				}
				// Simulate targets without Bash for both explicit and automatic settings.
				if (tc.noBash || tc.execution != nil) && strings.Contains(command, "bash") && !strings.Contains(command, "__XOPS_") {
					output, status = "bash: not found\n", 127
				}
				if _, err := io.WriteString(channel, output); err != nil {
					t.Errorf("write diagnostic output: %v", err)
				}
				if _, err := channel.SendRequest("exit-status", false, cryptoSSH.Marshal(struct{ Status uint32 }{status})); err != nil {
					t.Errorf("send diagnostic status: %v", err)
				}
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer testutil.Close(t, peer)
			p := newAdminPeer(t, ctx, adminConfig(t))
			p.initialize()
			host, portText, err := net.SplitHostPort(peer.Address)
			if err != nil {
				t.Fatal(err)
			}
			port, err := strconv.Atoi(portText)
			if err != nil {
				t.Fatal(err)
			}
			hostID := p.save("hosts", service.HostInput{Name: "host", Address: host, Port: port})
			var observed struct {
				ProbeID string `json:"probeID"`
			}
			if err := json.Unmarshal(p.request("POST", "/api/v1/hosts/"+hostID+"/key-preview", map[string]string{"hostKey": string(cryptoSSH.MarshalAuthorizedKey(peer.HostKey))}, 200, nil), &observed); err != nil {
				t.Fatal(err)
			}
			p.request("POST", "/api/v1/hosts/"+hostID+"/trust", map[string]string{"probeID": observed.ProbeID}, 200, nil)
			credentialID := p.save("credentials", service.CredentialInput{Name: "login", Kind: "password", Secret: &service.MaterialInput{Password: sshfixture.Password}})
			identityID := p.save("identities", service.IdentityInput{Name: "identity", User: "fixture", CredentialID: credentialID})
			nodeID := p.save("nodes", service.NodeInput{Name: "node", HostID: hostID, IdentityID: identityID, SudoMode: tc.mode, Execution: tc.execution})
			for _, draft := range []bool{false, true} {
				path, body := "/api/v1/nodes/"+nodeID+"/test-privilege", map[string]any{}
				if draft {
					path = "/api/v1/nodes/privilege-probe"
					body = map[string]any{"hostID": hostID, "identityID": identityID, "sudoMode": tc.mode, "execution": tc.execution}
				}
				data := p.request("POST", path, body, tc.wantStatus, nil)
				if tc.wantStatus == 200 {
					var result struct {
						Escalated bool   `json:"escalated"`
						User      string `json:"user"`
						SudoMode  string `json:"sudoMode"`
					}
					if err := json.Unmarshal(data, &result); err != nil {
						t.Fatal(err)
					}
					if !result.Escalated || result.User != "root" || result.SudoMode != tc.wantMode {
						t.Fatalf("unexpected identity (draft=%v): %+v", draft, result)
					}
				}
			}
			inventory, err := p.app.Host.Store.Load(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(inventory.Nodes[nodeID].Execution, tc.execution) {
				t.Fatal("privilege inspection persisted discovered execution settings")
			}
		})
	}
}
