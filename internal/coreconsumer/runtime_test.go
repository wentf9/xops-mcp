package coreconsumer_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wentf9/xops-cli/core/mcp/policy"
	"github.com/wentf9/xops-cli/core/mcp/ports"
	mcpserver "github.com/wentf9/xops-cli/core/mcp/runtime"
	"github.com/wentf9/xops-cli/core/mcp/sshexec"
	"github.com/wentf9/xops-cli/core/mcp/state"
	"github.com/wentf9/xops-cli/core/ssh"
	"github.com/wentf9/xops-cli/core/testutil/sshfixture"
	"go.uber.org/goleak"
	"strconv"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// This test exercises only exported upstream APIs from a separate Go module.
// It performs commands and file transfers against an isolated SSH/SFTP peer.
func TestCoreRuntimeHTTPAndSSHContract(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	server := httptest.NewUnstartedServer(nil)
	defer server.Close()

	options := mcpserver.DefaultHTTPOptions()
	options.Listen = server.Listener.Addr().String()
	options.PublicURL = "http://" + options.Listen
	options.StateDir = filepath.Join(t.TempDir(), "transfers")
	options.Token = "synthetic-compatibility-token-0123456789"
	options.ShutdownTimeout = 3 * time.Second

	peer, err := sshfixture.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer closeResource(t, peer)
	host, portText, err := net.SplitHostPort(peer.Address)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	view := ports.OperationSnapshot{DomainID: "consumer", Revision: "1", PolicyRevision: "1", Policy: policy.Config{Enabled: false}, Targets: map[string]ports.Target{
		"compat-node": {Info: ports.NodeInfo{ID: "compat-node", Tags: []string{"compatibility"}}, Version: "1", Plan: ssh.ConnectionPlan{Scope: "consumer", Hops: []ssh.ConnectionConfig{{NodeID: "compat-node", Address: host, Port: port, User: "fixture", AuthType: "password", AuthUpdateToken: "1", TrustVersion: "1"}}}},
	}}
	coordinator, err := state.New(view, state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer closeResource(t, coordinator)
	var backend *sshexec.Backend
	dependencies := ports.Dependencies{State: coordinator, Gate: coordinator, Audit: ports.NoopAudit{}, NewBackend: func(ctx context.Context) (ports.Backend, error) {
		created, err := sshexec.New(ctx, sshexec.Options{SSH: []ssh.Option{ssh.WithSecretResolver(fixtureSecrets{}), ssh.WithHostKeyVerifier(fixtureTrust{peer.HostKey})}})
		backend = created
		return created, err
	}}
	runtime, err := mcpserver.NewRuntime(ctx,
		mcpserver.WithDependencies(dependencies),
		mcpserver.WithHTTP(options),
	)
	if err != nil {
		t.Fatalf("construct shared runtime: %v", err)
	}
	defer closeResource(t, runtime)
	server.Config.Handler, err = runtime.HTTPHandler()
	if err != nil {
		t.Fatalf("obtain HTTP handler: %v", err)
	}
	server.Config.ReadHeaderTimeout = 3 * time.Second
	server.Config.IdleTimeout = 3 * time.Second
	server.Config.BaseContext = func(net.Listener) context.Context { return ctx }
	server.Start()

	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 3 * time.Second}).DialContext,
		ResponseHeaderTimeout: 3 * time.Second,
		IdleConnTimeout:       3 * time.Second,
	}
	defer transport.CloseIdleConnections()
	assertUnauthorized(t, ctx, server.URL+"/mcp", transport)

	client := mcp.NewClient(&mcp.Implementation{Name: "xops-mcp-compat", Version: "0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: server.URL + "/mcp",
		HTTPClient: &http.Client{
			Transport: bearerTransport{base: transport, token: options.Token},
			Timeout:   5 * time.Second,
		},
		MaxRetries: -1,
	}, &mcp.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
	if err != nil {
		t.Fatalf("initialize external MCP consumer: %v", err)
	}
	defer closeResource(t, session)

	assertHTTPTools(t, ctx, session)
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "xops_list_nodes", Arguments: map[string]any{"tag": "compatibility"},
	})
	if err != nil {
		t.Fatalf("call inventory tool: %v", err)
	}
	if result.IsError {
		t.Fatalf("inventory tool returned an error: %+v", result.Content)
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("encode structured inventory response: %v", err)
	}
	var output struct {
		Status string `json:"status"`
		Nodes  []struct {
			ID string `json:"id"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(data, &output); err != nil {
		t.Fatalf("decode public inventory response: %v", err)
	}
	if output.Status != "success" || len(output.Nodes) != 1 || output.Nodes[0].ID != "compat-node" {
		t.Fatalf("unexpected inventory response: %s", data)
	}
	exerciseCoreTools(t, ctx, session, server.Client())
	next := coordinator.Snapshot()
	next.Revision = "2"
	node := next.Targets["compat-node"]
	node.Disabled = true
	next.Targets["compat-node"] = node
	update, err := coordinator.BeginUpdate(ctx, "1", next)
	if err != nil {
		t.Fatal(err)
	}
	if err := update.BeginPersistence(); err != nil {
		t.Fatal(err)
	}
	if err := update.ConfirmCommit(); err != nil {
		t.Fatal(err)
	}
	if err := update.Publish(ctx, backend.RetirePlans); err != nil {
		t.Fatal(err)
	}
	denied, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "xops_ssh_run", Arguments: map[string]any{"nodeID": "compat-node", "command": "hostname"}})
	if err != nil || !denied.IsError {
		t.Fatalf("disabled node still executed: %+v %v", denied, err)
	}

}

func assertHTTPTools(t *testing.T, ctx context.Context, session *mcp.ClientSession) {
	t.Helper()
	result, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("discover HTTP tools: %v", err)
	}
	tools := make(map[string]bool)
	for _, tool := range result.Tools {
		tools[tool.Name] = true
	}
	for _, name := range []string{
		"xops_list_nodes", "xops_ssh_run", "xops_read_file", "xops_write_file",
		"xops_prepare_upload", "xops_prepare_download", "xops_transfer_status", "xops_transfer_cancel",
	} {
		if !tools[name] {
			t.Errorf("required shared HTTP tool is missing: %s", name)
		}
	}
	for _, name := range []string{
		"xops_upload", "xops_download", "xops_tunnel_create", "xops_tunnel_list",
		"xops_tunnel_status", "xops_tunnel_stop",
	} {
		if tools[name] {
			t.Errorf("stdio-only tool exposed over HTTP: %s", name)
		}
	}
}

func assertUnauthorized(t *testing.T, ctx context.Context, endpoint string, transport http.RoundTripper) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatalf("construct unauthenticated request: %v", err)
	}
	response, err := (&http.Client{Transport: transport, Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("send unauthenticated request: %v", err)
	}
	defer closeResource(t, response.Body)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
}

type bearerTransport struct {
	base  http.RoundTripper
	token string
}

func (b bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	cloned.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(cloned)
}

func closeResource(t *testing.T, resource io.Closer) {
	t.Helper()
	if err := resource.Close(); err != nil {
		t.Errorf("close test resource: %v", err)
	}
}

var errMissingNode = errors.New("synthetic node is absent")

type missingNodeProvider struct{}

func (missingNodeProvider) GetConfig(string) (*ssh.ClientConfig, error) {
	return nil, errMissingNode
}

var _ ssh.ConnectionProvider = missingNodeProvider{}

func TestIndependentSSHProviderContract(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	connector := ssh.NewConnector(missingNodeProvider{}, ssh.WithHandshakeTimeout(time.Second))
	defer func() {
		if err := connector.CloseAll(); err != nil {
			t.Errorf("close connector: %v", err)
		}
	}()
	if _, err := connector.Connect(ctx, "missing"); !errors.Is(err, errMissingNode) {
		t.Fatalf("external provider error = %v, want %v", err, errMissingNode)
	}
	if err := connector.CloseAll(); err != nil {
		t.Fatalf("close independent connector: %v", err)
	}
	if _, err := connector.Connect(ctx, "missing"); !errors.Is(err, ssh.ErrConnectorClosed) {
		t.Fatalf("closed connector error = %v, want %v", err, ssh.ErrConnectorClosed)
	}
}
