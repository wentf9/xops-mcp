package server_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wentf9/xops-mcp/internal/server"
	"github.com/wentf9/xops-mcp/internal/testutil"
)

func TestAdministratorRejectsPlaintextAndCookieCredentials(t *testing.T) {
	p := newAdminPeer(t, t.Context(), adminConfig(t))
	// Bypass the test client's JWE helper to exercise the actual wire contract.
	req, err := http.NewRequestWithContext(t.Context(), "POST", p.http.URL+"/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"synthetic-password"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", p.http.URL)
	req.Header.Set("Content-Type", "application/json")
	res, err := p.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	testutil.Close(t, res.Body)
	if res.StatusCode != 400 {
		t.Fatalf("plaintext login HTTP %d", res.StatusCode)
	}
	p.initialize()
	token := p.token
	p.token = ""
	p.request("GET", "/api/v1/inventory", nil, 401, func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "xops_admin_legacy", Value: token}) })
	p.request("GET", "/api/v1/inventory", nil, 401, func(r *http.Request) {
		r.Header.Add("Authorization", "Bearer "+token)
		r.Header.Add("Authorization", "Bearer "+token)
	})
	p.token = token
	p.request("GET", "/api/v1/inventory", nil, 200, nil)
	p.request("POST", "/api/v1/auth/logout", map[string]any{}, 204, nil)
	p.request("GET", "/api/v1/inventory", nil, 401, nil)
	// Logout deletes client state; a previously copied JWT remains valid until
	// its advertised expiry, as required by the stateless authentication model.
	p.request("GET", "/api/v1/inventory", nil, 200, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) })
}

func TestNativeManagementHTTPS(t *testing.T) {
	cfg := adminConfig(t)
	cfg.Listen, cfg.WebListen = "127.0.0.1:0", "127.0.0.1:0"
	cfg.PublicURL, cfg.WebPublicURL = "http://localhost", "https://localhost"
	cfg.WebEnabled = true
	cfg.WebTLSEnabled = true
	cfg.WebTLSCertFile = filepath.Join(filepath.Dir(cfg.DataDir), "admin.crt")
	cfg.WebTLSKeyFile = filepath.Join(filepath.Dir(cfg.DataDir), "admin-tls.key")
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.WebTLSCertFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.WebTLSKeyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}), 0600); err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, IdleConnTimeout: time.Second}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	ctx, cancel := context.WithCancel(t.Context())
	logs := listenerLog{lines: make(chan string, 2)}
	done := make(chan error, 1)
	// Cancellation shuts down both listeners, and cleanup joins Run before any
	// temporary certificate or database resources are removed.
	go func() { done <- server.Run(ctx, cfg, logs) }()
	joined := false
	defer func() {
		cancel()
		if !joined {
			if err := <-done; err != nil {
				t.Error(err)
			}
		}
	}()
	address := ""
	for range 2 {
		select {
		case line := <-logs.lines:
			parts := strings.Fields(line)
			if parts[0] == "Web" {
				address = parts[4]
			}
		case err := <-done:
			joined = true
			t.Fatal(err)
		case <-time.After(5 * time.Second):
			t.Fatal("TLS server startup timeout")
		}
	}
	req, err := http.NewRequestWithContext(t.Context(), "GET", "https://"+address+"/api/v1/auth/challenge?action=login", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "localhost"
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	testutil.Close(t, res.Body)
	if res.StatusCode != 200 || res.TLS == nil || res.Header.Get("Strict-Transport-Security") != "" {
		t.Fatal("management did not serve authenticated TLS")
	}
	req, err = http.NewRequestWithContext(t.Context(), "POST", "https://"+address+"/api/v1/auth/login", bytes.NewBufferString(`{"password":"plaintext"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "localhost"
	req.Header.Set("Origin", "https://localhost")
	req.Header.Set("Content-Type", "application/json")
	res, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	testutil.Close(t, res.Body)
	if res.StatusCode != 400 {
		t.Fatal("HTTPS accepted an unencrypted login payload")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	joined = true
	// Turning off TLS reuses the deployment without loading its old certificate
	// paths. HTTP must still support the encrypted authentication flow.
	cfg.WebTLSEnabled = false
	cfg.WebPublicURL = ""
	cfg.WebTLSCertFile = filepath.Join(t.TempDir(), "missing.crt")
	cfg.WebTLSKeyFile = filepath.Join(t.TempDir(), "missing.key")
	plain := newAdminPeer(t, t.Context(), cfg)
	plain.initialize()
	plain.request("GET", "/api/v1/inventory", nil, 200, nil)
	if plain.lastHeader.Get("Strict-Transport-Security") != "" {
		t.Fatal("HTTP retained an HTTPS-only policy")
	}
}

func TestAdministratorStartupRejectsRawMCPTokenAsJWTKey(t *testing.T) {
	cfg := adminConfig(t)
	cfg.WebEnabled = true
	original, err := os.ReadFile(cfg.AdminJWTKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	token := []byte("0123456789abcdef0123456789abcdef")
	if err := os.WriteFile(cfg.MCPTokenFile, append(bytes.Clone(token), '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.AdminJWTKeyFile, []byte(hex.EncodeToString(token)), 0600); err != nil {
		t.Fatal(err)
	}
	app, err := server.NewApplication(t.Context(), cfg)
	if app != nil {
		testutil.Close(t, app)
	}
	if err == nil {
		t.Fatal("administrator handler accepted a signing key known to an MCP-only client")
	}
	if !strings.Contains(err.Error(), "administrator JWT and MCP token must differ") {
		t.Fatal("unexpected startup failure", err)
	}
	if err := os.WriteFile(cfg.AdminJWTKeyFile, original, 0600); err != nil {
		t.Fatal(err)
	}
	// Failed validation must release deployment/runtime ownership for a corrected
	// configuration; a distinct signing key restores normal encrypted login.
	p := newAdminPeer(t, t.Context(), cfg)
	p.initialize()
	p.request("GET", "/api/v1/inventory", nil, 200, nil)
}
