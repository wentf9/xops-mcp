package server_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wentf9/xops-cli/core/mcp/ports"
	mcpruntime "github.com/wentf9/xops-cli/core/mcp/runtime"
	"github.com/wentf9/xops-cli/core/testutil/sshfixture"
	"github.com/wentf9/xops-mcp/internal/config"
	"github.com/wentf9/xops-mcp/internal/server"
	"github.com/wentf9/xops-mcp/internal/service"
	"github.com/wentf9/xops-mcp/internal/storage"
	"github.com/wentf9/xops-mcp/internal/storage/database"
	"github.com/wentf9/xops-mcp/internal/testutil"
)

const adminPassword = "synthetic-administrator-password"
const setupToken = "synthetic-admin-setup-token-0123456789"

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

type adminPeer struct {
	t           *testing.T
	app         *server.Application
	http        *httptest.Server
	mcp         *httptest.Server
	basePath    string
	client      *http.Client
	transport   *http.Transport
	token, etag string
	lastHeader  http.Header
	lastCookies []*http.Cookie
}

func newAdminPeer(t *testing.T, ctx context.Context, cfg config.Config) *adminPeer {
	t.Helper()
	httpServer := httptest.NewUnstartedServer(nil)
	mcpServer := httptest.NewUnstartedServer(nil)
	cfg.Listen = mcpServer.Listener.Addr().String()
	cfg.PublicURL = "http://" + cfg.Listen
	cfg.WebListen = httpServer.Listener.Addr().String()
	if cfg.WebPublicURL == "" {
		cfg.WebPublicURL = "http://" + cfg.WebListen
	}
	cfg.WebEnabled = true
	app, err := server.NewApplication(ctx, cfg)
	if err != nil {
		httpServer.Close()
		mcpServer.Close()
		t.Fatal(err)
	}
	httpServer.Config.Handler = app.AdminHandler()
	httpServer.Config.ReadHeaderTimeout = time.Second
	httpServer.Start()
	mcpServer.Config.Handler = app.Handler()
	mcpServer.Config.ReadHeaderTimeout = time.Second
	mcpServer.Start()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{DialContext: (&net.Dialer{Timeout: time.Second}).DialContext, ResponseHeaderTimeout: 8 * time.Second, IdleConnTimeout: time.Second}
	base, err := config.NormalizeWebBasePath(cfg.WebBasePath)
	if err != nil {
		t.Fatal(err)
	}
	p := &adminPeer{t: t, app: app, http: httpServer, mcp: mcpServer, basePath: base, transport: transport, client: &http.Client{Jar: jar, Transport: transport, Timeout: 10 * time.Second}, etag: "\"0\""}
	t.Cleanup(func() {
		testutil.Close(t, app)
		httpServer.Close()
		mcpServer.Close()
		transport.CloseIdleConnections()
	})
	return p
}
func adminConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := testutil.Config(t)
	cfg.AdminBootstrapTokenFile = filepath.Join(filepath.Dir(cfg.DataDir), "admin.setup")
	if err := os.WriteFile(cfg.AdminBootstrapTokenFile, []byte(setupToken), 0600); err != nil {
		t.Fatal(err)
	}
	return cfg
}
func (p *adminPeer) request(method, path string, body any, want int, alter func(*http.Request)) []byte {
	p.t.Helper()
	var input io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			p.t.Fatal(err)
		}
		if strings.HasPrefix(path, "/api/v1/auth/") && (path == "/api/v1/auth/login" || path == "/api/v1/auth/setup" || path == "/api/v1/auth/password") {
			action := strings.TrimPrefix(path, "/api/v1/auth/")
			challenge := p.request("GET", "/api/v1/auth/challenge?action="+action, nil, 200, nil)
			var parameters struct {
				PublicKey jose.JSONWebKey `json:"publicKey"`
				Challenge string          `json:"challenge"`
			}
			if err := json.Unmarshal(challenge, &parameters); err != nil {
				p.t.Fatal(err)
			}
			encrypter, err := jose.NewEncrypter(jose.A256GCM, jose.Recipient{Algorithm: jose.RSA_OAEP_256, Key: parameters.PublicKey.Key, KeyID: parameters.PublicKey.KeyID}, nil)
			if err != nil {
				p.t.Fatal(err)
			}
			plaintext, err := json.Marshal(map[string]any{"challenge": parameters.Challenge, "data": json.RawMessage(data)})
			if err != nil {
				p.t.Fatal(err)
			}
			object, err := encrypter.Encrypt(plaintext)
			clear(plaintext)
			if err != nil {
				p.t.Fatal(err)
			}
			ciphertext, err := object.CompactSerialize()
			if err != nil {
				p.t.Fatal(err)
			}
			data, err = json.Marshal(map[string]string{"ciphertext": ciphertext})
			if err != nil {
				p.t.Fatal(err)
			}
		}
		input = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(p.t.Context(), method, p.http.URL+p.basePath+path, input)
	if err != nil {
		p.t.Fatal(err)
	}
	if method != http.MethodGet {
		req.Header.Set("Origin", p.http.URL)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("If-Match", p.etag)
	}
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
	if alter != nil {
		alter(req)
	}
	response, err := p.client.Do(req)
	if err != nil {
		p.t.Fatal(err)
	}
	defer testutil.Close(p.t, response.Body)
	data, err := io.ReadAll(response.Body)
	if err != nil {
		p.t.Fatal(err)
	}
	if response.StatusCode != want {
		p.t.Fatalf("%s %s HTTP %d want %d: %s", method, path, response.StatusCode, want, data)
	}
	if response.StatusCode == 204 && (path == "/api/v1/auth/logout" || path == "/api/v1/auth/password") {
		p.token = ""
	}
	p.lastHeader = response.Header.Clone()
	p.lastCookies = response.Cookies()
	if tag := response.Header.Get("ETag"); tag != "" {
		p.etag = tag
	}
	return data
}
func (p *adminPeer) initialize() {
	p.request("POST", "/api/v1/auth/setup", map[string]string{"username": "admin", "password": adminPassword, "token": setupToken}, 201, nil)
	p.login(adminPassword)
	if len(p.lastCookies) != 0 {
		p.t.Fatal("JWT login set a cookie")
	}

}
func (p *adminPeer) login(password string) {
	data := p.request("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": password}, 200, nil)
	var result struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		p.t.Fatal(err)
	}
	p.token = result.AccessToken
	if len(strings.Split(p.token, ".")) != 3 {
		p.t.Fatal("login did not issue a JWT")
	}
}
func (p *adminPeer) save(path string, body any) string {
	data := p.request("POST", "/api/v1/"+path, body, 200, nil)
	var result service.EditResult
	if err := json.Unmarshal(data, &result); err != nil {
		p.t.Fatal(err)
	}
	return result.ID
}

func TestWebAdministrationControlsLiveMCP(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	peer, err := sshfixture.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, peer)
	cfg := adminConfig(t)
	p := newAdminPeer(t, ctx, cfg)
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
	credentialID := p.save("credentials", service.CredentialInput{Name: "login", Kind: "password", Secret: &service.MaterialInput{Password: sshfixture.Password}})
	identityID := p.save("identities", service.IdentityInput{Name: "operator", User: "fixture", CredentialID: credentialID})
	tagID := p.save("tags", service.TagInput{Name: "staging"})
	node := service.NodeInput{Name: "web-node", HostID: hostID, IdentityID: identityID, Disabled: true, TagIDs: []string{tagID}, SudoMode: "none"}
	nodeID := p.save("nodes", node)
	oldTag := p.etag
	p.request("PUT", "/api/v1/hosts/"+hostID, service.HostInput{Name: "ignored", Address: host, Port: port}, 412, func(r *http.Request) { r.Header.Set("If-Match", "\"0\"") })
	probe := p.request("POST", "/api/v1/hosts/"+hostID+"/probe", map[string]string{"algorithm": "ssh-ed25519"}, 200, nil)
	var observed struct {
		ProbeID string `json:"probeID"`
	}
	if err := json.Unmarshal(probe, &observed); err != nil {
		t.Fatal(err)
	}
	p.request("POST", "/api/v1/hosts/"+hostID+"/trust", map[string]string{"probeID": observed.ProbeID}, 200, nil)
	p.request("POST", "/api/v1/hosts/"+hostID+"/trust", map[string]string{"probeID": observed.ProbeID}, 409, nil)
	node.Disabled = false
	p.request("PUT", "/api/v1/nodes/"+nodeID, node, 200, nil)
	p.request("POST", "/api/v1/nodes/"+nodeID+"/test", map[string]any{}, 200, nil)
	if peer.Executed.Load() != 0 {
		t.Fatal("connection test executed a command")
	}
	data := p.request("GET", "/api/v1/inventory", nil, 200, nil)
	for _, secret := range []string{sshfixture.Password, adminPassword, "Ciphertext", "passwordHash", "privateKey"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatalf("inventory exposed %s", secret)
		}
	}
	p.request("PUT", "/api/v1/policy", service.PolicyInput{Enabled: false, NoElicitFallback: "deny", ApprovalThreshold: "moderate"}, 200, nil)
	options, err := cfg.HTTPOptions()
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "web-integration", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: p.mcp.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{p.transport, options.Token}, Timeout: 10 * time.Second}, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, session)
	r := &running{host: p.app.Host, session: session, http: p.mcp}
	call(t, ctx, r, "xops_ssh_run", map[string]string{"nodeID": "web-node", "command": "hostname"}, nil)
	// Full HTTP transfers exercise the tracker-wrapped permit handoff.
	payload := []byte("admin and MCP coexist\x00\xff")
	sum := sha256.Sum256(payload)
	var upload mcpruntime.PreparedTransferOutput
	call(t, ctx, r, "xops_prepare_upload", mcpruntime.PrepareUploadInput{NodeID: "web-node", RequestID: "web-upload", RemotePath: "/web", Size: int64(len(payload)), SHA256: hex.EncodeToString(sum[:])}, &upload)
	content(t, ctx, r, upload, bytes.NewReader(payload), 200)
	var failedUpload mcpruntime.PreparedTransferOutput
	call(t, ctx, r, "xops_prepare_upload", mcpruntime.PrepareUploadInput{NodeID: "web-node", RequestID: "web-upload-failed", RemotePath: "/web-failed", Size: int64(len(payload)), SHA256: hex.EncodeToString(sum[:])}, &failedUpload)
	corrupt := bytes.Clone(payload)
	corrupt[0] ^= 0xff
	content(t, ctx, r, failedUpload, bytes.NewReader(corrupt), http.StatusBadGateway)
	// Both transfer terminal states are stored verbatim and can be selected by
	// the console together with its node and cursor filters.
	for _, outcome := range []string{"completed", "failed"} {
		path := "/api/v1/audit?limit=1&nodeID=" + nodeID + "&outcome=" + outcome
		var audit struct {
			Entries []storage.AuditRecord `json:"entries"`
			Next    int64                 `json:"next"`
		}
		if err := json.Unmarshal(p.request("GET", path, nil, http.StatusOK, nil), &audit); err != nil {
			t.Fatal(err)
		}
		if len(audit.Entries) != 1 || audit.Entries[0].Event.Outcome != outcome || audit.Entries[0].Event.NodeID != nodeID {
			t.Fatalf("missing real %s upload audit: %+v", outcome, audit.Entries)
		}
		if audit.Next != audit.Entries[0].ID {
			t.Fatal("filtered audit did not provide the last row as its cursor")
		}
		if err := json.Unmarshal(p.request("GET", path+"&before="+strconv.FormatInt(audit.Next, 10), nil, http.StatusOK, nil), &audit); err != nil {
			t.Fatal(err)
		}
		if len(audit.Entries) != 0 || audit.Next != 0 {
			t.Fatalf("filtered audit pagination included an unrelated result: %+v", audit)
		}
	}
	view, err := p.app.Host.Service.Coordinator.Resolve(ctx, ports.ResolveRequest{Selectors: []string{nodeID}})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := ports.Bind(view, "test", "xops_ssh_run", map[string]string{"command": "hostname"})
	if err != nil {
		t.Fatal(err)
	}
	permit, err := p.app.Host.Service.Coordinator.Enter(ctx, ports.Admission{OperationID: "admitted", Phase: ports.Execute, Snapshot: view, Binding: binding})
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, permit)
	p.request("PUT", "/api/v1/hosts/"+hostID, service.HostInput{Name: "host", Address: "127.0.0.2", Port: port}, 200, nil)
	if permit.Context().Err() != nil || permit.Snapshot().Targets[nodeID].Plan.Hops[0].Address != host {
		t.Fatal("ordinary Web edit cancelled or redirected prior admission")
	}
	p.request("PUT", "/api/v1/hosts/"+hostID, service.HostInput{Name: "host", Address: host, Port: port}, 200, nil)
	p.request("PUT", "/api/v1/credentials/"+credentialID, service.CredentialInput{Name: "login", Kind: "password", Secret: &service.MaterialInput{Password: sshfixture.Password}}, 200, nil)
	if permit.Context().Err() == nil {
		t.Fatal("Web credential rotation did not revoke old admission")
	}
	call(t, ctx, r, "xops_ssh_run", map[string]string{"nodeID": "web-node", "command": "hostname"}, nil)
	node.Disabled = true
	p.request("PUT", "/api/v1/nodes/"+nodeID, node, 200, nil)
	denied, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "xops_ssh_run", Arguments: map[string]string{"nodeID": "web-node", "command": "hostname"}})
	if err != nil || !denied.IsError {
		t.Fatal("Web disable did not affect existing MCP session")
	}
	p.request("DELETE", "/api/v1/credentials/"+credentialID, nil, 409, nil)
	p.request("PUT", "/api/v1/tags/"+tagID, map[string]string{"name": "reviewed"}, 200, nil)
	audit := p.request("GET", "/api/v1/audit?limit=2", nil, 200, nil)
	if !bytes.Contains(audit, []byte("admin.tag.save")) {
		t.Fatalf("management audit missing: %s", audit)
	}
	if p.etag == oldTag {
		t.Fatal("Web updates did not publish revisions")
	}
	p.request("DELETE", "/api/v1/nodes/"+nodeID, nil, 200, nil)
	p.request("DELETE", "/api/v1/identities/"+identityID, nil, 200, nil)
	p.request("DELETE", "/api/v1/credentials/"+credentialID, nil, 200, nil)
	p.request("DELETE", "/api/v1/hosts/"+hostID, nil, 200, nil)
}

func TestWebPolicyUsesSharedGlobMatching(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	peer, err := sshfixture.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, peer)
	cfg := adminConfig(t)
	p := newAdminPeer(t, ctx, cfg)
	p.initialize()
	v, report := testutil.Plan(t, p.app.Host.Store, p.app.Host.Vault, fixtureDocument(t, peer))
	if _, err := p.app.Host.Service.Apply(ctx, 0, v); err != nil {
		t.Fatal(err)
	}
	p.request("GET", "/api/v1/inventory", nil, http.StatusOK, nil)
	options, err := cfg.HTTPOptions()
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "web-policy-contract", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: p.mcp.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{p.transport, options.Token}, Timeout: 10 * time.Second}, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, session)
	for _, scenario := range []struct {
		name, pattern, command string
		blocked                bool
	}{
		{"exact", "hostname", "hostname", true},
		{"exact includes arguments", "hostname", "hostname -f", false},
		{"star", "hostname*", "hostname -f", true},
		{"question mark", "hostnam?", "hostname", true},
		{"character class", "hostnam[e]", "hostname", true},
		{"unmatched", "hostname*", "whoami", false},
		{"regexp anchors are literal", "^hostname$", "hostname", false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			parent := p.t
			p.t = t
			defer func() { p.t = parent }()
			p.request("PUT", "/api/v1/policy", service.PolicyInput{Enabled: true, ApprovalThreshold: "dangerous", NoElicitFallback: "deny", BlockedPatterns: []string{scenario.pattern}}, http.StatusOK, nil)
			before := peer.Executed.Load()
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "xops_ssh_run", Arguments: map[string]string{"nodeID": report.NodeIDs["peer"], "command": scenario.command}})
			if err != nil {
				t.Fatal(err)
			}
			if result.IsError != scenario.blocked {
				t.Fatalf("pattern %q, command %q: %+v", scenario.pattern, scenario.command, result.Content)
			}
			want := before
			if !scenario.blocked {
				want++
			}
			if peer.Executed.Load() != want {
				t.Fatal("policy result disagrees with real SSH execution")
			}
		})
	}
	before := p.etag
	p.request("PUT", "/api/v1/policy", service.PolicyInput{Enabled: true, BlockedPatterns: []string{"["}}, http.StatusUnprocessableEntity, nil)
	p.request("GET", "/api/v1/inventory", nil, http.StatusOK, nil)
	if p.etag != before {
		t.Fatal("malformed glob changed the published policy")
	}
}

func TestAdminAuthenticationIsolationAndRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	defer cancel()
	cfg := adminConfig(t)
	p := newAdminPeer(t, ctx, cfg)
	p.request("GET", "/api/v1/inventory", nil, 401, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer synthetic-mcp-token-01234567890123456789")
	})
	p.request("POST", "/api/v1/auth/setup", map[string]string{"username": "admin", "password": adminPassword, "token": "wrong"}, 403, nil)
	p.initialize()
	p.request("POST", "/api/v1/auth/setup", map[string]string{"username": "other", "password": adminPassword, "token": setupToken}, 409, nil)
	p.request("POST", "/api/v1/hosts", service.HostInput{Name: "no-token", Address: "127.0.0.1", Port: 22}, 401, func(r *http.Request) { r.Header.Del("Authorization") })
	p.request("POST", "/api/v1/hosts", service.HostInput{Name: "cross-origin", Address: "127.0.0.1", Port: 22}, 403, func(r *http.Request) { r.Header.Set("Origin", "https://untrusted.example") })
	p.request("POST", "/api/v1/hosts", service.HostInput{Name: "no-version", Address: "127.0.0.1", Port: 22}, 428, func(r *http.Request) { r.Header.Del("If-Match") })
	p.request("GET", "/mcp", nil, 404, nil)
	p.request("GET", "/api/v1/inventory", nil, 403, func(r *http.Request) { r.Host = "untrusted.example" })
	root := p.request("GET", "/", nil, 200, nil)
	if !strings.Contains(p.lastHeader.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatal("console CSP missing")
	}
	if !strings.Contains(string(root), "/assets/app.js") {
		t.Fatal("embedded console missing")
	}
	hostID := p.save("hosts", service.HostInput{Name: "survives-restart", Address: "127.0.0.1", Port: 22})
	p.request("PUT", "/api/v1/auth/password", map[string]string{"current": "wrong", "next": "replacement-admin-password"}, 403, nil)
	p.request("PUT", "/api/v1/auth/password", map[string]string{"current": adminPassword, "next": "replacement-admin-password"}, 204, nil)
	p.request("GET", "/api/v1/inventory", nil, 401, nil)
	p.login("replacement-admin-password")
	p.request("POST", "/api/v1/auth/logout", map[string]any{}, 204, nil)
	p.request("GET", "/api/v1/inventory", nil, 401, nil)
	p.login("replacement-admin-password")
	oldToken := p.token
	testutil.Close(t, p.app)
	p.http.Close()
	p.transport.CloseIdleConnections()
	// Setup material can be removed after initialization. A shared signing
	// key keeps unexpired JWTs valid across process restart.
	if err := os.Remove(cfg.AdminBootstrapTokenFile); err != nil {
		t.Fatal(err)
	}
	next := newAdminPeer(t, ctx, cfg)
	next.token = oldToken
	next.request("GET", "/api/v1/inventory", nil, 200, nil)
	next.login("replacement-admin-password")
	data := next.request("GET", "/api/v1/inventory", nil, 200, nil)
	if !bytes.Contains(data, []byte(hostID)) {
		t.Fatal("restart lost inventory")
	}
	if _, err := next.app.Host.Store.Admin(ctx); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func restoredConfig(t *testing.T, cfg config.Config) config.Config {
	t.Helper()
	restored := testutil.Config(t)
	if err := os.CopyFS(restored.DataDir, os.DirFS(cfg.DataDir)); err != nil {
		t.Fatal(err)
	}
	if err := filepath.WalkDir(cfg.DataDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(cfg.DataDir, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return os.Chmod(filepath.Join(restored.DataDir, relative), info.Mode().Perm())
	}); err != nil {
		t.Fatal(err)
	}
	for destination, source := range map[string]string{restored.MasterKeyFile: cfg.MasterKeyFile, restored.MCPTokenFile: cfg.MCPTokenFile} {
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, data, 0600); err != nil {
			t.Fatal(err)
		}
		clear(data)
	}
	if cfg.DatabaseDriver == "postgres" || restored.DatabaseDriver == "postgres" {
		// A cross-backend restore copies the journal as above, then restores the
		// logical database. A copied SQLite file is not the target's active store.
		if cfg.DatabaseDriver != "postgres" && restored.DatabaseDriver == "postgres" {
			for _, name := range []string{"xops.db", "xops.db-wal", "xops.db-shm"} {
				if err := os.Remove(filepath.Join(restored.DataDir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
					t.Fatal(err)
				}
			}
		}
		vault, err := cfg.Vault()
		if err != nil {
			t.Fatal(err)
		}
		source, err := database.Open(t.Context(), cfg, vault, false)
		if err != nil {
			t.Fatal(err)
		}
		backup, err := source.Export(t.Context())
		testutil.Close(t, source)
		if err != nil {
			t.Fatal(err)
		}
		target, err := database.Open(t.Context(), restored, vault, true)
		if err != nil {
			t.Fatal(err)
		}
		defer testutil.Close(t, target)
		if err := target.Restore(t.Context(), backup); err != nil {
			t.Fatal(err)
		}
	}

	return restored
}
