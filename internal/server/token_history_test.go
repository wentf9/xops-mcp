package server_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	mcpruntime "github.com/wentf9/xops-cli/core/mcp/runtime"
	"github.com/wentf9/xops-cli/core/mcp/transfer"
	"github.com/wentf9/xops-cli/core/testutil/sshfixture"
	"github.com/wentf9/xops-mcp/internal/mcpauth"
	"github.com/wentf9/xops-mcp/internal/testutil"
)

func TestSeedPreservesExistingMCPTransferHistory(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	peer, err := sshfixture.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testutil.Close(t, peer) })
	cfg := adminConfig(t)
	doc := fixtureDocument(t, peer)
	// start uses the core's static-token HTTP configuration, as deployments
	// did before database-managed client credentials, with real SSH/SFTP.
	old := start(t, ctx, cfg, &doc)
	payload := []byte("retained transfer history")
	sum := sha256.Sum256(payload)
	input := mcpruntime.PrepareUploadInput{RequestID: "retained-upload", NodeID: "peer", RemotePath: "/retained", Size: int64(len(payload)), SHA256: hex.EncodeToString(sum[:])}
	var original mcpruntime.PreparedTransferOutput
	call(t, ctx, old, "xops_prepare_upload", input, &original)
	content(t, ctx, old, original, bytes.NewReader(payload), http.StatusOK)
	testutil.Close(t, old)
	journal, err := transfer.OpenJournal(filepath.Join(cfg.DataDir, "transfers"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testutil.Close(t, journal) })
	records, err := journal.Load(4096)
	if err != nil || len(records) != 1 {
		t.Fatalf("retained journal: %v %v", records, err)
	}
	before := records[0]
	if before.Authorization == nil {
		t.Fatal("fixture has no original authorization")
	}
	// Simulate a restart after the remote commit but before its acknowledgement
	// became durable. Neither scope preservation nor retry may replay this write.
	before.State = transfer.Committing
	if err := journal.Save(before); err != nil {
		t.Fatal(err)
	}
	testutil.Close(t, journal)

	p := newAdminPeer(t, ctx, cfg)
	connect := func(token string) *running {
		t.Helper()
		client := mcp.NewClient(&mcp.Implementation{Name: "retained-client", Version: "1"}, nil)
		session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: p.mcp.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{base: p.mcp.Client().Transport, token: token}}, MaxRetries: -1}, &mcp.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { testutil.Close(t, session) })
		return &running{host: p.app.Host, session: session, http: p.mcp}
	}
	data, err := os.ReadFile(cfg.MCPTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	owner := connect(string(bytes.TrimSpace(data)))
	var status transfer.Status
	call(t, ctx, owner, "xops_transfer_status", map[string]string{"transferID": original.Task.ID}, &status)
	if status.State != transfer.Unknown || status.Resolved {
		t.Fatalf("retained result changed: %+v", status)
	}
	var retried mcpruntime.PreparedTransferOutput
	call(t, ctx, owner, "xops_prepare_upload", input, &retried)
	if retried.Task.ID != original.Task.ID || retried.Task.State != transfer.Unknown {
		t.Fatalf("request-ID retry lost original task: %+v", retried.Task)
	}

	manager := &mcpauth.Manager{Store: p.app.Host.Store}
	_, otherToken, err := manager.Create(ctx, mcpauth.Input{Name: "other-client", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	other := connect(otherToken)
	for _, tool := range []string{"xops_transfer_status", "xops_transfer_cancel"} {
		result, err := other.session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: map[string]string{"transferID": original.Task.ID}})
		if err != nil || !result.IsError {
			t.Fatalf("another client accessed retained task: %s %v %+v", tool, err, result)
		}
	}
	input.RequestID, input.Overwrite = "new-overwrite", true
	result, err := owner.session.CallTool(ctx, &mcp.CallToolParams{Name: "xops_prepare_upload", Arguments: input})
	if err != nil || !result.IsError {
		t.Fatalf("unknown destination lock was lost: %v %+v", err, result)
	}
	// Read the unchanged authorization while the journal remains owned by the
	// runtime; this must not require rewriting persisted bindings or snapshots.
	data, err = os.ReadFile(filepath.Join(cfg.DataDir, "transfers", original.Task.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var after transfer.Record
	if err := json.Unmarshal(data, &after); err != nil {
		t.Fatal(err)
	}
	if after.Spec.Scope != before.Spec.Scope || after.Authorization == nil || after.Authorization.Binding != before.Authorization.Binding {
		t.Fatal("seeding rewrote existing transfer authorization")
	}
}
