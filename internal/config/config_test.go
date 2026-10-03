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
