package server_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wentf9/xops-cli/core/mcp/policy"
	mcpruntime "github.com/wentf9/xops-cli/core/mcp/runtime"
	"github.com/wentf9/xops-cli/core/mcp/transfer"
	"github.com/wentf9/xops-cli/core/testutil/sshfixture"
	"github.com/wentf9/xops-mcp/internal/config"
	"github.com/wentf9/xops-mcp/internal/importer"
	"github.com/wentf9/xops-mcp/internal/server"
	"github.com/wentf9/xops-mcp/internal/testutil"
	"go.uber.org/goleak"
	cryptoSSH "golang.org/x/crypto/ssh"
)

func mixedHostKeys(t *testing.T, kind string) []cryptoSSH.Signer {
	t.Helper()
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var pinned any
	if kind == "rsa" {
		pinned, err = rsa.GenerateKey(rand.Reader, 2048)
	} else {
		_, pinned, err = ed25519.GenerateKey(rand.Reader)
	}
	if err != nil {
		t.Fatal(err)
	}
	var signers []cryptoSSH.Signer
	for _, key := range []any{pinned, ecKey} {
		signer, err := cryptoSSH.NewSignerFromKey(key)
		if err != nil {
			t.Fatal(err)
		}
		signers = append(signers, signer)
	}
	return signers
}

func TestPinnedHostAlgorithmSSHAndSFTPThroughJump(t *testing.T) {
	for _, kind := range []string{"ed25519", "rsa"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			peer, err := sshfixture.NewWithOptions(ctx, sshfixture.Options{HostKeys: mixedHostKeys(t, kind)})
			if err != nil {
				t.Fatal(err)
			}
			defer testutil.Close(t, peer)
			doc := fixtureDocument(t, peer)
			jump := doc.Nodes["peer"]
			jump.Aliases = nil
			doc.Nodes["jump"] = jump
			doc.Nodes["jump2"] = jump
			target := doc.Nodes["peer"]
			target.ProxyJump = "jump,jump2"
			doc.Nodes["peer"] = target
			r := start(t, ctx, testutil.Config(t), &doc)
			call(t, ctx, r, "xops_ssh_run", map[string]any{"nodeID": "peer", "command": "hostname"}, nil)
			payload := []byte("mixed host algorithms\x00\xff")
			sum := sha256.Sum256(payload)
			var upload, download mcpruntime.PreparedTransferOutput
			call(t, ctx, r, "xops_prepare_upload", mcpruntime.PrepareUploadInput{NodeID: "peer", RequestID: "mixed-upload", RemotePath: "/mixed", Size: int64(len(payload)), SHA256: hex.EncodeToString(sum[:])}, &upload)
			content(t, ctx, r, upload, bytes.NewReader(payload), http.StatusOK)
			call(t, ctx, r, "xops_prepare_download", mcpruntime.PrepareDownloadInput{NodeID: "peer", RequestID: "mixed-download", RemotePath: "/mixed"}, &download)
			if got := content(t, ctx, r, download, nil, http.StatusOK); !bytes.Equal(got, payload) {
				t.Fatal("SFTP data changed")
			}
			if peer.Forwards.Load() < 2 {
				t.Fatal("explicit jump chain was bypassed")
			}
			// Keeping the negotiated algorithm must not authorize a different key
			// of that type, nor fall back to the peer's unpinned ECDSA key.
			v, err := r.host.Store.Load(ctx)
			if err != nil {
				t.Fatal(err)
			}
			wrong := mixedHostKeys(t, kind)[0].PublicKey()
			for id, h := range v.Hosts {
				h.HostKey = string(cryptoSSH.MarshalAuthorizedKey(wrong))
				v.Hosts[id] = h
			}
			if _, err := r.host.Service.Apply(ctx, v.Revision, v); err != nil {
				t.Fatal(err)
			}
			result, err := r.session.CallTool(ctx, &mcp.CallToolParams{Name: "xops_ssh_run", Arguments: map[string]any{"nodeID": "peer", "command": "hostname"}})
			if err != nil || !result.IsError || peer.Executed.Load() != 1 {
				t.Fatalf("unpinned key accepted: %+v %v", result, err)
			}
			testutil.Close(t, r)
		})
	}
}

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

type running struct {
	host    *server.Host
	session *mcp.ClientSession
	http    *httptest.Server
	close   func() error
}

func (r *running) Close() error { return r.close() }

type bearer struct {
	base  http.RoundTripper
	token string
}

func (b bearer) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(req)
}

func start(t *testing.T, ctx context.Context, cfg config.Config, document *importer.Document) *running {
	t.Helper()
	httpServer := httptest.NewUnstartedServer(nil)
	cfg.Listen = httpServer.Listener.Addr().String()
	options, err := cfg.HTTPOptions()
	if err != nil {
		t.Fatal(err)
	}
	host, err := server.OpenHost(ctx, cfg)
	if err != nil {
		httpServer.Close()
		t.Fatal(err)
	}
	if document != nil {
		v, _ := testutil.Plan(t, host.Store, host.Vault, *document)
		if _, err := host.Service.Apply(ctx, v.Revision, v); err != nil {
			testutil.Close(t, host)
			httpServer.Close()
			t.Fatal(err)
		}
	}
	runtime, err := mcpruntime.NewRuntime(ctx, mcpruntime.WithDependencies(host.Dependencies()), mcpruntime.WithHTTP(options))
	if err != nil {
		testutil.Close(t, host)
		httpServer.Close()
		t.Fatal(err)
	}
	handler, err := runtime.HTTPHandler()
	if err != nil {
		t.Fatal(err)
	}
	httpServer.Config.Handler = handler
	httpServer.Config.ReadHeaderTimeout = time.Second
	httpServer.Start()
	transport := &http.Transport{DialContext: (&net.Dialer{Timeout: time.Second}).DialContext, ResponseHeaderTimeout: 5 * time.Second, IdleConnTimeout: time.Second}
	client := mcp.NewClient(&mcp.Implementation{Name: "server-fixture", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{transport, options.Token}, Timeout: 10 * time.Second}, MaxRetries: -1}, nil)
	if err != nil {
		testutil.Close(t, runtime)
		httpServer.Close()
		testutil.Close(t, host)
		transport.CloseIdleConnections()
		t.Fatal(err)
	}
	r := &running{host: host, session: session, http: httpServer}
	r.close = sync.OnceValue(func() error {
		a := session.Close()
		b := runtime.Close()
		httpServer.Close()
		transport.CloseIdleConnections()
		return errors.Join(a, b, host.Close())
	})
	t.Cleanup(func() { testutil.Close(t, r) })
	return r
}

func fixtureDocument(t *testing.T, peer *sshfixture.Server) importer.Document {
	t.Helper()
	doc := testutil.Document(t)
	host, portText, err := net.SplitHostPort(peer.Address)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	doc.Hosts["peer"] = importer.Host{Address: host, Port: port, HostKey: string(cryptoSSH.MarshalAuthorizedKey(peer.HostKey))}
	doc.Credentials["login"] = importer.Credential{Kind: "password", Password: sshfixture.Password}
	doc.Policy = &policy.Config{Enabled: false, NoElicitFallback: "deny"}
	return doc
}

func call(t *testing.T, ctx context.Context, r *running, name string, input, output any) {
	t.Helper()
	result, err := r.session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: input})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("%s failed: %+v", name, result.Content)
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(sshfixture.Password)) {
		t.Fatal("MCP response exposed stored credential")
	}
	if output != nil {
		if err := json.Unmarshal(data, output); err != nil {
			t.Fatal(err)
		}
	}
}

func content(t *testing.T, ctx context.Context, r *running, p mcpruntime.PreparedTransferOutput, body io.Reader, want int) []byte {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, p.Method, p.URL, body)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range p.Headers {
		req.Header.Set(k, v)
	}
	client := r.http.Client()
	client.Timeout = 10 * time.Second
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, response.Body)
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != want {
		t.Fatalf("transfer HTTP %d, want %d: %s", response.StatusCode, want, data)
	}
	return data
}

func TestPersistentHTTPCommandTransferAndUnknownRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	peer, err := sshfixture.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, peer)
	cfg := testutil.Config(t)
	doc := fixtureDocument(t, peer)
	r := start(t, ctx, cfg, &doc)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, r.http.URL+"/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := r.http.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	testutil.Close(t, response.Body)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatal("unauthenticated access accepted")
	}
	call(t, ctx, r, "xops_list_nodes", map[string]any{}, nil)
	call(t, ctx, r, "xops_ssh_run", map[string]any{"nodeID": "peer", "command": "hostname"}, nil)
	if peer.Executed.Load() != 1 {
		t.Fatal("command did not reach SSH fixture")
	}
	payload := []byte("server SQLite credential transfer\x00\xff\n")
	sum := sha256.Sum256(payload)
	input := mcpruntime.PrepareUploadInput{RequestID: "upload", NodeID: "peer", RemotePath: "/persistent", Size: int64(len(payload)), SHA256: hex.EncodeToString(sum[:])}
	var first, upload mcpruntime.PreparedTransferOutput
	call(t, ctx, r, "xops_prepare_upload", input, &first)
	call(t, ctx, r, "xops_prepare_upload", input, &upload)
	if first.Task.ID != upload.Task.ID {
		t.Fatal("prepare retry created another task")
	}
	content(t, ctx, r, first, bytes.NewReader(payload), http.StatusUnauthorized)
	content(t, ctx, r, upload, bytes.NewReader(payload), http.StatusOK)
	content(t, ctx, r, upload, bytes.NewReader(payload), http.StatusConflict)
	var download mcpruntime.PreparedTransferOutput
	call(t, ctx, r, "xops_prepare_download", mcpruntime.PrepareDownloadInput{RequestID: "download", NodeID: "peer", RemotePath: "/persistent"}, &download)
	if got := content(t, ctx, r, download, nil, http.StatusOK); !bytes.Equal(got, payload) {
		t.Fatal("binary SFTP round trip changed bytes")
	}
	original := r.host.Service.Coordinator.Snapshot()
	testutil.Close(t, r)
	// Model a crash after remote commit but before acknowledgement was made
	// durable. No remote operation is repeated when this evidence is reopened.
	journal, err := transfer.OpenJournal(filepath.Join(cfg.DataDir, "transfers"))
	if err != nil {
		t.Fatal(err)
	}
	records, err := journal.Load(4096)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.ID == upload.Task.ID {
			record.State = transfer.Committing
			if err := journal.Save(record); err != nil {
				t.Fatal(err)
			}
		}
	}
	testutil.Close(t, journal)
	cfg = restoredConfig(t, cfg)
	r = start(t, ctx, cfg, nil)
	current := r.host.Service.Coordinator.Snapshot()
	oldDigest, err := original.Digest()
	if err != nil {
		t.Fatal(err)
	}
	newDigest, err := current.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if current.DomainID != original.DomainID || newDigest != oldDigest {
		t.Fatal("restart changed persistent binding")
	}
	var status transfer.Status
	call(t, ctx, r, "xops_transfer_status", map[string]string{"transferID": upload.Task.ID}, &status)
	if status.State != transfer.Unknown {
		t.Fatalf("interrupted commit became %q", status.State)
	}
	input.RequestID, input.Overwrite = "blocked-overwrite", true
	result, err := r.session.CallTool(ctx, &mcp.CallToolParams{Name: "xops_prepare_upload", Arguments: input})
	if err != nil || !result.IsError {
		t.Fatalf("unknown destination lock lost: %+v %v", result, err)
	}
	testutil.Close(t, r)
	host, err := server.OpenExistingHost(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := mcpruntime.RecoverTransfers(ctx, mcpruntime.RecoveryOptions{StateDir: filepath.Join(cfg.DataDir, "transfers"), TransferID: upload.Task.ID, Verify: true, MaxRecords: 4096}, mcpruntime.WithDependencies(host.Dependencies()))
	testutil.Close(t, host)
	if err != nil || len(entries) != 1 || !entries[0].MatchesExpected || entries[0].Task.State != transfer.Unknown || entries[0].Task.Resolved {
		t.Fatalf("recovery invented result or failed original binding: %+v %v", entries, err)
	}
	if peer.Executed.Load() != 1 {
		t.Fatal("recovery repeated SSH command")
	}
	for _, name := range []string{filepath.Join(cfg.DataDir, "xops.db"), filepath.Join(cfg.DataDir, "transfers", upload.Task.ID+".json")} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(sshfixture.Password)) {
			t.Fatal("persistent data contains plaintext password")
		}
	}
}

func TestEncryptedPrivateKeyProxyJumpTrustAndDisable(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := cryptoSSH.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	block, err := cryptoSSH.MarshalPrivateKeyWithPassphrase(key, "fixture", []byte("synthetic-passphrase"))
	if err != nil {
		t.Fatal(err)
	}
	peer, err := sshfixture.NewWithOptions(ctx, sshfixture.Options{PublicKeys: []cryptoSSH.PublicKey{signer.PublicKey()}})
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, peer)
	cfg := testutil.Config(t)
	doc := fixtureDocument(t, peer)
	doc.Credentials["login"] = importer.Credential{Kind: "key", PrivateKey: string(pem.EncodeToMemory(block)), Passphrase: "synthetic-passphrase"}
	node := doc.Nodes["peer"]
	node.Aliases = nil
	doc.Nodes["jump"] = node
	node.ProxyJump = "jump"
	doc.Nodes["peer"] = node
	r := start(t, ctx, cfg, &doc)
	call(t, ctx, r, "xops_ssh_run", map[string]any{"nodeID": "peer", "command": "hostname"}, nil)
	if peer.Forwards.Load() == 0 {
		t.Fatal("captured ProxyJump chain was bypassed")
	}
	view, err := r.host.Store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for id, h := range view.Hosts {
		h.HostKey = testutil.HostKey(t)
		view.Hosts[id] = h
	}
	if _, err := r.host.Service.Apply(ctx, view.Revision, view); err != nil {
		t.Fatal(err)
	}
	result, err := r.session.CallTool(ctx, &mcp.CallToolParams{Name: "xops_ssh_run", Arguments: map[string]any{"nodeID": "peer", "command": "hostname"}})
	if err != nil || !result.IsError || peer.Executed.Load() != 1 {
		t.Fatalf("trust rotation did not reject mismatch: %+v %v", result, err)
	}
	view, err = r.host.Store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for id, n := range view.Nodes {
		if n.Name == "jump" {
			n.Disabled = true
			view.Nodes[id] = n
		}
	}
	if _, err := r.host.Service.Apply(ctx, view.Revision, view); err != nil {
		t.Fatal(err)
	}
	result, err = r.session.CallTool(ctx, &mcp.CallToolParams{Name: "xops_ssh_run", Arguments: map[string]any{"nodeID": "peer", "command": "hostname"}})
	if err != nil || !result.IsError || peer.Executed.Load() != 1 {
		t.Fatal("disabled shared jump did not block downstream node")
	}
	testutil.Close(t, r)
}
