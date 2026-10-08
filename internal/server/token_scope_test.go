package server_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	mcpruntime "github.com/wentf9/xops-cli/core/mcp/runtime"
	"github.com/wentf9/xops-cli/core/mcp/transfer"
	"github.com/wentf9/xops-cli/core/testutil/sshfixture"
	"github.com/wentf9/xops-mcp/internal/mcpauth"
	"github.com/wentf9/xops-mcp/internal/storage"
	"github.com/wentf9/xops-mcp/internal/testutil"
)

func TestMCPTokenNodeScopeLiveProtocol(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	peer, err := sshfixture.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testutil.Close(t, peer) })
	cfg := adminConfig(t)
	cfg.MCPTokenFile = ""
	p := newAdminPeer(t, ctx, cfg)
	doc := fixtureDocument(t, peer)
	// The permitted destination needs a jump that the token cannot execute on
	// directly. Both use the local fixture so forwarding is proven, not mocked.
	jump := doc.Nodes["peer"]
	jump.Aliases = []string{"jump-alias"}
	doc.Nodes["jump"] = jump
	destination := doc.Nodes["peer"]
	destination.ProxyJump = "jump"
	doc.Nodes["peer"] = destination
	view, _ := testutil.Plan(t, p.app.Host.Store, p.app.Host.Vault, doc)
	if _, err := p.app.Host.Service.Apply(ctx, view.Revision, view); err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for id, node := range view.Nodes {
		ids[node.Name] = id
	}
	if ids["peer"] == "" || ids["jump"] == "" {
		t.Fatal("fixture nodes missing")
	}
	p.initialize()
	type issued struct {
		Item  storage.MCPToken `json:"item"`
		Token string           `json:"token"`
	}
	create := func(in mcpauth.Input) issued {
		t.Helper()
		var out issued
		if err := json.Unmarshal(p.request("POST", "/api/v1/mcp-tokens", in, http.StatusCreated, nil), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	update := func(item *storage.MCPToken, scope string, nodes []string) {
		t.Helper()
		in := mcpauth.Input{Name: item.Name, Enabled: true, NodeScope: scope, NodeIDs: nodes}
		body := p.request("PUT", "/api/v1/mcp-tokens/"+item.ID, in, http.StatusOK, func(r *http.Request) {
			r.Header.Set("If-Match", fmt.Sprintf("\"%d\"", item.Version))
		})
		if err := json.Unmarshal(body, item); err != nil {
			t.Fatal(err)
		}
	}
	connect := func(token string) *running {
		t.Helper()
		client := mcp.NewClient(&mcp.Implementation{Name: "node-scope-test", Version: "1"}, nil)
		session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
			Endpoint: p.mcp.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{base: p.mcp.Client().Transport, token: token}}, MaxRetries: -1,
		}, &mcp.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { testutil.Close(t, session) })
		return &running{host: p.app.Host, session: session, http: p.mcp}
	}
	assertNodes := func(run *running, tag string, want ...string) {
		t.Helper()
		var out mcpruntime.ListNodesOutput
		call(t, ctx, run, "xops_list_nodes", map[string]string{"tag": tag}, &out)
		got := make([]string, 0, len(out.Nodes))
		for _, node := range out.Nodes {
			got = append(got, node.ID)
		}
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Fatalf("visible nodes %v, want %v", got, want)
		}
	}
	deny := func(run *running, tool string, input any) {
		t.Helper()
		result, err := run.session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: input})
		if err != nil || !result.IsError {
			t.Fatalf("unauthorized %s accepted: %+v %v", tool, result, err)
		}
	}

	selected := create(mcpauth.Input{Name: "selected", Enabled: true, NodeScope: "selected", NodeIDs: []string{ids["peer"]}})
	empty := create(mcpauth.Input{Name: "empty", Enabled: true, NodeScope: "selected", NodeIDs: []string{}})
	all := create(mcpauth.Input{Name: "all", Enabled: true, NodeScope: "all"})
	run, emptyRun, allRun := connect(selected.Token), connect(empty.Token), connect(all.Token)
	assertNodes(run, "", ids["peer"])
	assertNodes(run, "fixture", ids["peer"])
	assertNodes(emptyRun, "")
	assertNodes(allRun, "", ids["peer"], ids["jump"])
	var inventory mcpruntime.ListNodesOutput
	call(t, ctx, run, "xops_list_nodes", map[string]any{}, &inventory)
	if inventory.Nodes[0].ProxyJump != "" {
		t.Fatal("filtered inventory disclosed the ungranted jump identity")
	}
	for _, selector := range []string{ids["peer"], "peer", "test"} {
		call(t, ctx, run, "xops_ssh_run", map[string]string{"nodeID": selector, "command": "hostname"}, nil)
	}
	if peer.Executed.Load() != 3 || peer.Forwards.Load() == 0 {
		t.Fatal("permitted destination did not execute through its configured jump")
	}
	for _, selector := range []string{ids["jump"], "jump", "jump-alias"} {
		deny(run, "xops_ssh_run", map[string]string{"nodeID": selector, "command": "hostname"})
	}
	deny(emptyRun, "xops_ssh_run", map[string]string{"nodeID": "peer", "command": "hostname"})
	deny(run, "xops_fs_ls", map[string]string{"nodeID": "jump", "path": "/"})
	deny(run, "xops_read_file", map[string]string{"nodeID": "jump-alias", "path": "/secret"})
	if peer.Executed.Load() != 3 {
		t.Fatal("denied command reached SSH")
	}

	// Grants are canonical IDs, never selectors that can move to another node.
	for _, invalid := range []mcpauth.Input{
		{Name: "missing", Enabled: true, NodeScope: "selected", NodeIDs: []string{"does-not-exist"}},
		{Name: "alias", Enabled: true, NodeScope: "selected", NodeIDs: []string{"test"}},
		{Name: "mixed", Enabled: true, NodeScope: "all", NodeIDs: []string{ids["peer"]}},
		{Name: "invalid-scope", Enabled: true, NodeScope: "unknown"},
	} {
		p.request("POST", "/api/v1/mcp-tokens", invalid, http.StatusUnprocessableEntity, nil)
	}
	p.request("PUT", "/api/v1/mcp-tokens/"+selected.Item.ID, mcpauth.Input{Name: selected.Item.Name, Enabled: true, NodeScope: "selected", NodeIDs: []string{"missing"}}, http.StatusUnprocessableEntity, func(r *http.Request) {
		r.Header.Set("If-Match", fmt.Sprintf("\"%d\"", selected.Item.Version))
	})
	assertNodes(run, "", ids["peer"])
	// Clients predating node scopes can edit the name without broadening an
	// existing restricted token by omitting the new fields.
	updated := p.request("PUT", "/api/v1/mcp-tokens/"+selected.Item.ID, mcpauth.Input{Name: "renamed-selected", Enabled: true}, http.StatusOK, func(r *http.Request) {
		r.Header.Set("If-Match", fmt.Sprintf("\"%d\"", selected.Item.Version))
	})
	if err := json.Unmarshal(updated, &selected.Item); err != nil {
		t.Fatal(err)
	}
	assertNodes(run, "", ids["peer"])

	payload := []byte("scoped-token-transfer\x00\xff\n")
	sum := sha256.Sum256(payload)
	input := mcpruntime.PrepareUploadInput{RequestID: "allowed-upload", NodeID: "test", RemotePath: "/scope-allowed", Size: int64(len(payload)), SHA256: hex.EncodeToString(sum[:])}
	var upload, revokedReady, download mcpruntime.PreparedTransferOutput
	call(t, ctx, run, "xops_prepare_upload", input, &upload)
	content(t, ctx, run, upload, bytes.NewReader(payload), http.StatusOK)
	call(t, ctx, run, "xops_prepare_download", mcpruntime.PrepareDownloadInput{RequestID: "allowed-download", NodeID: "peer", RemotePath: "/scope-allowed"}, &download)
	if got := content(t, ctx, run, download, nil, http.StatusOK); !bytes.Equal(got, payload) {
		t.Fatal("scoped transfer changed bytes")
	}
	input.RequestID, input.RemotePath = "prepared-before-shrink", "/scope-shrunk"
	call(t, ctx, run, "xops_prepare_upload", input, &revokedReady)
	deniedInput := input
	deniedInput.RequestID, deniedInput.NodeID = "denied-jump-upload", "jump"
	deny(run, "xops_prepare_upload", deniedInput)

	// The existing MCP session and a separately issued HTTP transfer credential
	// must both observe the update without a runtime or session restart.
	originalSession := run.session.ID()
	update(&selected.Item, "selected", []string{})
	assertNodes(run, "")
	deny(run, "xops_ssh_run", map[string]string{"nodeID": "test", "command": "hostname"})
	content(t, ctx, run, revokedReady, bytes.NewReader(payload), http.StatusConflict)
	deny(run, "xops_prepare_upload", input)
	if run.session.ID() != originalSession || peer.Executed.Load() != 3 {
		t.Fatal("scope update restarted session or executed denied work")
	}
	// History and cancellation stay client-owned, even after the node grant
	// is removed, so clients can inspect completed work and settle old tasks.
	var status transfer.Status
	call(t, ctx, run, "xops_transfer_status", mcpruntime.TransferTaskInput{TransferID: upload.Task.ID}, &status)
	if status.State != transfer.Completed {
		t.Fatalf("lost completed transfer history: %+v", status)
	}
	var replay mcpruntime.PreparedTransferOutput
	call(t, ctx, run, "xops_prepare_upload", mcpruntime.PrepareUploadInput{RequestID: "allowed-upload", NodeID: "test", RemotePath: "/scope-allowed", Size: int64(len(payload)), SHA256: hex.EncodeToString(sum[:])}, &replay)
	if replay.Task.ID != upload.Task.ID || replay.Task.State != transfer.Completed || replay.URL != "" {
		t.Fatal("completed replay granted a new data transfer")
	}
	call(t, ctx, run, "xops_transfer_cancel", mcpruntime.TransferTaskInput{TransferID: revokedReady.Task.ID}, nil)
	deny(allRun, "xops_transfer_status", mcpruntime.TransferTaskInput{TransferID: upload.Task.ID})

	update(&selected.Item, "selected", []string{ids["jump"]})
	assertNodes(run, "", ids["jump"])
	call(t, ctx, run, "xops_ssh_run", map[string]string{"nodeID": "jump-alias", "command": "hostname"}, nil)
	deny(run, "xops_ssh_run", map[string]string{"nodeID": "peer", "command": "hostname"})
	update(&selected.Item, "all", nil)
	assertNodes(run, "", ids["peer"], ids["jump"])
	call(t, ctx, run, "xops_ssh_run", map[string]string{"nodeID": "test", "command": "hostname"}, nil)
	if peer.Executed.Load() != 5 {
		t.Fatal("live grant expansion was not applied")
	}
}
