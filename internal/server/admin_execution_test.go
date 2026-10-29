package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wentf9/xops-cli/core/ssh"
	"github.com/wentf9/xops-cli/core/testutil/sshfixture"
	"github.com/wentf9/xops-mcp/internal/adapters/xops"
	"github.com/wentf9/xops-mcp/internal/service"
	"github.com/wentf9/xops-mcp/internal/storage"
	"github.com/wentf9/xops-mcp/internal/testutil"
	cryptoSSH "golang.org/x/crypto/ssh"
)

func TestNodeExecutionManagementAPI(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	p := newAdminPeer(t, ctx, adminConfig(t))
	p.initialize()
	hostID := p.save("hosts", service.HostInput{Name: "host", Address: "192.0.2.10", Port: 22})
	identityID := p.save("identities", service.IdentityInput{Name: "login", User: "fixture"})
	login := false
	input := service.NodeInput{Name: "node", HostID: hostID, IdentityID: identityID, Disabled: true, Execution: &ssh.ExecutionConfig{Interpreter: ssh.InterpreterZsh, LaunchDialect: ssh.LaunchPOSIX, Login: &login}}
	id := p.save("nodes", input)
	check := func(want *ssh.ExecutionConfig) {
		t.Helper()
		data := p.request("GET", "/api/v1/inventory", nil, 200, nil)
		var inventory struct {
			Nodes []struct {
				ID        string
				Execution *ssh.ExecutionConfig
			}
		}
		if err := json.Unmarshal(data, &inventory); err != nil {
			t.Fatal(err)
		}
		if len(inventory.Nodes) != 1 || inventory.Nodes[0].ID != id {
			t.Fatal("node missing from inventory")
		}
		got := inventory.Nodes[0].Execution
		if want == nil {
			if got != nil {
				t.Fatalf("execution not cleared: %+v", got)
			}
			return
		}
		if got == nil || got.Interpreter != want.Interpreter || got.LaunchDialect != want.LaunchDialect || got.Login == nil || *got.Login != *want.Login {
			t.Fatalf("execution not preserved: %+v", got)
		}
	}
	check(input.Execution)
	p.request("PUT", "/api/v1/nodes/"+id, map[string]any{"name": "node", "hostID": hostID, "identityID": identityID, "disabled": true, "execution": map[string]any{"interpreter": "cmd", "launchDialect": "posix"}}, 400, nil)
	check(input.Execution)
	input.Execution = nil
	p.request("PUT", "/api/v1/nodes/"+id, input, 200, nil)
	check(nil)
}

// Probe draft connection settings over real SSH, without inserting a node or
// source bindings. Recommendations become durable only through SaveNode.
func TestExecutionProbePreviewAndSave(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	peer, err := sshfixture.NewWithOptions(ctx, sshfixture.Options{ExecHandler: func(command string, channel cryptoSSH.Channel) {
		output := "__XOPS_DIALECT_POSIX__:Linux:/bin/zsh\n"
		if strings.Contains(command, "__XOPS_INTERP_END__") {
			output = "__XOPS_INTERP__:sh\n__XOPS_INTERP__:zsh\n__XOPS_INTERP_END__\n"
		}
		if _, err := io.WriteString(channel, output); err != nil {
			t.Error(err)
			return
		}
		if _, err := channel.SendRequest("exit-status", false, cryptoSSH.Marshal(struct{ Status uint32 }{0})); err != nil {
			t.Error(err)
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, peer)
	p := newAdminPeer(t, ctx, adminConfig(t))
	p.initialize()
	doc := fixtureDocument(t, peer)
	v, _ := testutil.Plan(t, p.app.Host.Store, p.app.Host.Vault, doc)
	if _, err := p.app.Host.Service.Apply(ctx, v.Revision, v); err != nil {
		t.Fatal(err)
	}
	p.request("GET", "/api/v1/inventory", nil, 200, nil)
	v, err = p.app.Host.Store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var original storage.Node
	for _, n := range v.Nodes {
		original = n
	}
	before, err := p.app.Host.Store.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"hostID": original.HostID, "identityID": original.IdentityID, "jumpIDs": []string{}}
	response := p.request("POST", "/api/v1/nodes/execution-probe", input, 200, nil)
	var raw struct {
		ssh.ProbeResult
		Diagnostics []xops.DiagnosticEvent `json:"diagnostics,omitempty"`
	}
	if err := json.Unmarshal(response, &raw); err != nil {
		t.Fatal(err)
	}
	result := raw.ProbeResult
	if result.SuggestedConfig.Interpreter != ssh.InterpreterZsh || result.DetectedDialect != ssh.LaunchPOSIX || !slices.Equal(result.AvailableInterpreters, []ssh.Interpreter{ssh.InterpreterSh, ssh.InterpreterZsh}) {
		t.Fatalf("unexpected recommendation: %+v", result)
	}
	after, err := p.app.Host.Store.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.Inventory, after.Inventory) || !reflect.DeepEqual(before.Sources, after.Sources) {
		t.Fatal("probe persisted draft inventory or material bindings")
	}
	node := service.NodeInput{Name: "new-node", HostID: original.HostID, IdentityID: original.IdentityID, Disabled: true, Execution: result.SuggestedConfig.Clone()}
	id := p.save("nodes", node)
	saved, err := p.app.Host.Store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(saved.Nodes[id].Execution, result.SuggestedConfig.Clone()) {
		t.Fatal("save did not preserve recommendation")
	}
	// Explicit administrator diagnostics may inspect a disabled target, without enabling it.
	input["nodeID"] = id
	p.request("POST", "/api/v1/nodes/execution-probe", input, 200, nil)
	saved, err = p.app.Host.Store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !saved.Nodes[id].Disabled {
		t.Fatal("probe enabled the target")
	}
	count := peer.Executed.Load()
	p.request("POST", "/api/v1/nodes/execution-probe", input, 412, func(r *http.Request) { r.Header.Set("If-Match", `"0"`) })
	input["command"] = "arbitrary command"
	p.request("POST", "/api/v1/nodes/execution-probe", input, 400, nil)
	delete(input, "command")
	input["nodeID"] = "missing"
	p.request("POST", "/api/v1/nodes/execution-probe", input, 404, nil)
	input["nodeID"] = id
	input["hostID"] = "missing"
	p.request("POST", "/api/v1/nodes/execution-probe", input, 422, nil)
	if peer.Executed.Load() != count {
		t.Fatal("invalid probe reached SSH")
	}
}

func TestExecutionProbeRejectsConcurrentInventoryEdit(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	started, release := make(chan struct{}), make(chan struct{})
	var first sync.Once
	peer, err := sshfixture.NewWithOptions(ctx, sshfixture.Options{ExecHandler: func(command string, channel cryptoSSH.Channel) {
		first.Do(func() {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
			}
		})
		output := "__XOPS_DIALECT_POSIX__:Linux:/bin/sh\n"
		if strings.Contains(command, "__XOPS_INTERP_END__") {
			output = "__XOPS_INTERP__:sh\n__XOPS_INTERP_END__\n"
		}
		if _, err := io.WriteString(channel, output); err != nil {
			t.Error(err)
			return
		}
		if _, err := channel.SendRequest("exit-status", false, cryptoSSH.Marshal(struct{ Status uint32 }{0})); err != nil {
			t.Error(err)
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, peer)
	p := newAdminPeer(t, ctx, adminConfig(t))
	p.initialize()
	v, _ := testutil.Plan(t, p.app.Host.Store, p.app.Host.Vault, fixtureDocument(t, peer))
	if _, err := p.app.Host.Service.Apply(ctx, v.Revision, v); err != nil {
		t.Fatal(err)
	}
	p.request("GET", "/api/v1/inventory", nil, 200, nil)
	v, err = p.app.Host.Store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var node storage.Node
	for _, n := range v.Nodes {
		node = n
	}
	input := map[string]any{"hostID": node.HostID, "identityID": node.IdentityID, "jumpIDs": []string{}}
	p.request("POST", "/api/v1/nodes/execution-probe", input, 401, func(r *http.Request) { r.Header.Del("Authorization") })
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", p.http.URL+"/api/v1/nodes/execution-probe", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Origin", p.http.URL)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("If-Match", p.etag)
	type outcome struct {
		status int
		err    error
	}
	done := make(chan outcome, 1)
	// The context bounds the request; release or cancellation ends the peer wait.
	// Join the worker before the test closes its transports.
	go func() {
		response, err := p.client.Do(req)
		if err != nil {
			done <- outcome{err: err}
			return
		}
		done <- outcome{status: response.StatusCode, err: response.Body.Close()}
	}()
	select {
	case <-started:
	case <-ctx.Done():
		cancel()
		<-done
		t.Fatal("probe never reached SSH")
	}
	p.save("tags", service.TagInput{Name: "changed-during-probe"})
	close(release)
	got := <-done
	if got.err != nil || got.status != 412 {
		t.Fatalf("stale recommendation returned: %+v", got)
	}
}
