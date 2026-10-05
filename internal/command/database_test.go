package command_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wentf9/xops-mcp/internal/config"
	"github.com/wentf9/xops-mcp/internal/server"
	"github.com/wentf9/xops-mcp/internal/testutil"
)

func TestDatabaseArchiveCommands(t *testing.T) {
	cfg, path := configuration(t)
	if _, err := run(t, "migrate", "--config", path); err != nil {
		t.Fatal(err)
	}
	host, err := server.OpenExistingHost(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testutil.Close(t, host) })
	v, _ := testutil.Plan(t, host.Store, host.Vault, testutil.Document(t))
	if _, err := host.Service.Apply(t.Context(), 0, v); err != nil {
		t.Fatal(err)
	}
	testutil.Close(t, host)
	file := filepath.Join(t.TempDir(), "database.xops")
	exported, err := run(t, "db-export", "--config", path, "--file", file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(exported, "synthetic-credential") {
		t.Fatal("export report leaks credentials")
	}
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("archive not private", err)
	}
	if _, err := run(t, "db-export", "--config", path, "--file", file); err == nil {
		t.Fatal("archive was overwritten")
	}
	if _, err := run(t, "db-verify", "--config", path, "--file", file); err != nil {
		t.Fatal(err)
	}
	target, targetPath := configuration(t)
	if _, err := run(t, "migrate", "--config", targetPath); err != nil {
		t.Fatal(err)
	}
	before, err := run(t, "status", "--config", targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "db-import", "--config", targetPath, "--file", file); err != nil {
		t.Fatal(err)
	}
	after, err := run(t, "status", "--config", targetPath)
	if err != nil || after != before {
		t.Fatal("preview changed target", err)
	}
	if _, err := run(t, "db-verify", "--config", targetPath, "--file", file); err == nil {
		t.Fatal("different deployment verified")
	}
	restored, err := run(t, "db-import", "--config", targetPath, "--file", file, "--apply")
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Applied, Verified bool
		SHA256            string
	}
	if err := json.Unmarshal([]byte(restored), &report); err != nil || !report.Applied || !report.Verified || report.SHA256 == "" {
		t.Fatalf("restore report: %s %v", restored, err)
	}
	if _, err := run(t, "db-import", "--config", targetPath, "--file", file, "--apply"); err == nil {
		t.Fatal("populated target overwritten")
	}
	if _, err := run(t, "db-verify", "--config", targetPath, "--file", file); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "db-import", "--config", targetPath, "--file", file); err == nil {
		t.Fatal("public archive accepted")
	}
	if err := os.Chmod(file, 0600); err != nil {
		t.Fatal(err)
	}
	data, err := config.ReadFile(file, 1<<20, true)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 1
	damaged := filepath.Join(t.TempDir(), "damaged.xops")
	if err := os.WriteFile(damaged, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "db-import", "--config", targetPath, "--file", damaged); err == nil {
		t.Fatal("damaged archive accepted")
	}
	if err := os.WriteFile(target.MasterKeyFile, []byte(strings.Repeat("00", 32)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "db-verify", "--config", targetPath, "--file", file); err == nil {
		t.Fatal("wrong deployment key accepted")
	}
}
