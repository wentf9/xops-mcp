package server_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	mcpruntime "github.com/wentf9/xops-cli/core/mcp/runtime"
	"github.com/wentf9/xops-cli/core/testutil/sshfixture"
	"github.com/wentf9/xops-mcp/internal/mcpauth"
	"github.com/wentf9/xops-mcp/internal/storage"
	"github.com/wentf9/xops-mcp/internal/testutil"
)

func TestConsoleMCPTokensAndClientIsolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	t.Cleanup(cancel)
	peer, err := sshfixture.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testutil.Close(t, peer) })
	cfg := adminConfig(t)
	cfg.MCPTokenFile = ""
	p := newAdminPeer(t, t.Context(), cfg)
	v, _ := testutil.Plan(t, p.app.Host.Store, p.app.Host.Vault, fixtureDocument(t, peer))
	if _, err := p.app.Host.Service.Apply(t.Context(), v.Revision, v); err != nil {
		t.Fatal(err)
	}
	p.request("GET", "/api/v1/mcp-tokens", nil, 401, nil)
	p.initialize()
	type issued struct {
		Item  storage.MCPToken `json:"item"`
		Token string           `json:"token"`
	}
	create := func(name string) issued {
		t.Helper()
		var result issued
		data := p.request("POST", "/api/v1/mcp-tokens", mcpauth.Input{Name: name, Enabled: true}, 201, nil)
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	a, b := create("client-a"), create("client-b")
	if a.Item.ClientID == b.Item.ClientID || a.Token == b.Token {
		t.Fatal("clients share identity")
	}
	list := p.request("GET", "/api/v1/mcp-tokens", nil, 200, nil)
	if strings.Contains(string(list), a.Token) || strings.Contains(string(list), "Digest") {
		t.Fatal("list disclosed token")
	}
	connect := func(token string) *mcp.ClientSession {
		t.Helper()
		client := mcp.NewClient(&mcp.Implementation{Name: "token-test", Version: "1"}, nil)
		session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: p.mcp.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{base: p.mcp.Client().Transport, token: token}}, MaxRetries: -1}, &mcp.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
		if err != nil {
			t.Fatal(err)
		}
		return session
	}
	sa, sb := connect(a.Token), connect(b.Token)
	for _, session := range []*mcp.ClientSession{sa, sb} {
		if _, err := session.ListTools(t.Context(), nil); err != nil {
			t.Fatal(err)
		}
	}
	request := func(token, session string, want int) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), "GET", p.mcp.URL+"/mcp", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Mcp-Session-Id", session)
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("MCP-Protocol-Version", "2025-11-25")
		resp, err := p.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer testutil.Close(t, resp.Body)
		if resp.StatusCode != want {
			data, readErr := io.ReadAll(resp.Body)
			t.Fatalf("MCP HTTP %d want %d: %s (%v)", resp.StatusCode, want, data, readErr)
		}
	}
	request(b.Token, sa.ID(), 403)
	// Reusing a request ID in two client scopes creates independent tasks.
	ra, rb := &running{session: sa, http: p.mcp}, &running{session: sb, http: p.mcp}
	payload := []byte("per-client-transfer-fixture")
	sum := sha256.Sum256(payload)
	var uploadA, uploadB, download mcpruntime.PreparedTransferOutput
	for run, out := range map[*running]*mcpruntime.PreparedTransferOutput{ra: &uploadA, rb: &uploadB} {
		path := "/client-a"
		if run == rb {
			path = "/client-b"
		}
		call(t, t.Context(), run, "xops_prepare_upload", mcpruntime.PrepareUploadInput{NodeID: "peer", RequestID: "shared-request-id", RemotePath: path, Size: int64(len(payload)), SHA256: hex.EncodeToString(sum[:])}, out)
	}
	if uploadA.Task.ID == uploadB.Task.ID {
		t.Fatal("request IDs crossed client scopes")
	}
	for _, tool := range []string{"xops_transfer_status", "xops_transfer_cancel"} {
		result, err := sb.CallTool(t.Context(), &mcp.CallToolParams{Name: tool, Arguments: map[string]any{"transferID": uploadA.Task.ID}})
		if err != nil || !result.IsError {
			t.Fatalf("cross-client %s allowed: %v %+v", tool, err, result)
		}
	}
	content(t, t.Context(), ra, uploadA, bytes.NewReader(payload), 200)
	content(t, t.Context(), rb, uploadB, bytes.NewReader(payload), 200)
	call(t, t.Context(), ra, "xops_prepare_download", mcpruntime.PrepareDownloadInput{NodeID: "peer", RequestID: "download", RemotePath: "/client-a"}, &download)
	if data := content(t, t.Context(), ra, download, nil, 200); !bytes.Equal(data, payload) {
		t.Fatal("client transfer corrupted")
	}
	request(p.token, sa.ID(), 401)
	p.request("GET", "/api/v1/mcp-tokens", nil, 401, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+a.Token) })
	testutil.Close(t, sa)
	testutil.Close(t, sb)
	p.request("PUT", "/api/v1/mcp-tokens/"+a.Item.ID, mcpauth.Input{Name: "client-a", Enabled: false}, 200, func(r *http.Request) { r.Header.Set("If-Match", "\"1\"") })
	request(a.Token, sa.ID(), 401)
	p.request("PUT", "/api/v1/mcp-tokens/"+a.Item.ID, mcpauth.Input{Name: "stale", Enabled: true}, 412, func(r *http.Request) { r.Header.Set("If-Match", "\"1\"") })
	p.request("DELETE", "/api/v1/mcp-tokens/"+b.Item.ID, nil, 200, func(r *http.Request) { r.Header.Set("If-Match", "\"1\"") })
	request(b.Token, sb.ID(), 401)
}
