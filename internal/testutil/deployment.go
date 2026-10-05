// Package testutil builds deployment-owned synthetic fixtures for server tests.
package testutil

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wentf9/xops-mcp/internal/config"
	"github.com/wentf9/xops-mcp/internal/importer"
	"github.com/wentf9/xops-mcp/internal/secure"
	"github.com/wentf9/xops-mcp/internal/storage"
	"github.com/wentf9/xops-mcp/internal/storage/database"
	"github.com/wentf9/xops-mcp/internal/testutil/pgfixture"
	cryptoSSH "golang.org/x/crypto/ssh"
)

func Close(t *testing.T, resource io.Closer) {
	t.Helper()
	if err := resource.Close(); err != nil {
		t.Errorf("close fixture: %v", err)
	}
}
func Config(t *testing.T) config.Config {
	t.Helper()
	dir := t.TempDir()
	c := config.Config{DataDir: filepath.Join(dir, "data"), MasterKeyFile: filepath.Join(dir, "master.key"), MCPTokenFile: filepath.Join(dir, "mcp.token"), Listen: "127.0.0.1:8080", ToolTimeout: 5 * time.Second, ShutdownTimeout: 2 * time.Second}
	key := bytes.Repeat([]byte{0x3f}, 32)
	if err := os.WriteFile(c.MasterKeyFile, []byte(hex.EncodeToString(key)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.MCPTokenFile, []byte("synthetic-mcp-token-01234567890123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("XOPS_TEST_BACKEND") == "postgres" {
		c.DatabaseDriver = "postgres"
		c.PostgresDSNFile = filepath.Join(dir, "postgres.dsn")
		if err := os.WriteFile(c.PostgresDSNFile, []byte(pgfixture.DSN(t)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return c
}
func Store(t *testing.T) (storage.Database, *secure.Vault, config.Config) {
	t.Helper()
	cfg := Config(t)
	vault, err := cfg.Vault()
	if err != nil {
		t.Fatal(err)
	}
	s, err := database.Open(t.Context(), cfg, vault, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { Close(t, s) })
	return s, vault, cfg
}
func HostKey(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := cryptoSSH.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return string(cryptoSSH.MarshalAuthorizedKey(key))
}
func Document(t *testing.T) importer.Document {
	t.Helper()
	return importer.Document{Version: 1, Hosts: map[string]importer.Host{"peer": {Address: "127.0.0.1", Port: 22, HostKey: HostKey(t)}}, Identities: map[string]importer.Identity{"login": {User: "fixture", Credential: "login"}}, Credentials: map[string]importer.Credential{"login": {Kind: "password", Password: "synthetic-credential-unique-78261"}}, Nodes: map[string]importer.Node{"peer": {Host: "peer", Identity: "login", Aliases: []string{"test"}, Tags: []string{"fixture"}}}}
}
func Plan(t *testing.T, s storage.Repository, vault *secure.Vault, doc importer.Document) (storage.Inventory, importer.Report) {
	t.Helper()
	base, err := s.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	candidate, report, err := importer.Plan(base, doc, vault, importer.Options{IncludeSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	return candidate, report
}
