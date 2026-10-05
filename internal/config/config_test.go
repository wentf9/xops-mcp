package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wentf9/xops-mcp/internal/config"
	"github.com/wentf9/xops-mcp/internal/testutil"
)

func TestMasterKeyValidationAndExplicitPaths(t *testing.T) {
	c := testutil.Config(t)
	for _, text := range []string{"", strings.Repeat("a", 62), strings.Repeat("a", 66), strings.Repeat("g", 64)} {
		if err := os.WriteFile(c.MasterKeyFile, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Vault(); err == nil {
			t.Fatal("invalid key accepted")
		}
	}
	if err := os.Chmod(c.MasterKeyFile, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.ReadFile(c.MasterKeyFile, 256, true); err == nil {
		t.Fatal("public secret file accepted")
	}
	path := filepath.Join(t.TempDir(), "server.yaml")
	if err := os.WriteFile(path, []byte("data_dir: data\nmaster_key_file: master.key\nmcp_token_file: token\ntool_timeout: 2s\n"), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path)
	if err != nil || !filepath.IsAbs(loaded.DataDir) {
		t.Fatalf("config: %v", err)
	}
	if err := os.WriteFile(path, []byte("data_dir: data\nmaster_key_file: data/master.key\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(path); err == nil {
		t.Fatal("master key stored with database")
	}
}

func TestDatabaseSelectionAndConnectionFilePaths(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yaml")
	for _, tc := range []struct {
		value string
		valid bool
	}{
		{"", true}, {"database_driver: sqlite\n", true},
		{"database_driver: mysql\n", false},
		{"database_driver: postgres\n", false},
		{"postgres_dsn_file: secret.dsn\n", false},
		{"database_driver: postgres\npostgres_dsn_file: secret.dsn\n", true},
	} {
		if err := os.WriteFile(path, []byte("data_dir: data\nmaster_key_file: master.key\n"+tc.value), 0600); err != nil {
			t.Fatal(err)
		}
		cfg, err := config.Load(path)
		if (err == nil) != tc.valid {
			t.Fatalf("database selection %q: %v", tc.value, err)
		}
		if err == nil && cfg.DatabaseDriver == "postgres" && cfg.PostgresDSNFile != filepath.Join(dir, "secret.dsn") {
			t.Fatal("DSN file is not relative to config")
		}
	}
}
