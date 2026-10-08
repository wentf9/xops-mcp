package command_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/wentf9/xops-mcp/internal/secure"
	"github.com/wentf9/xops-mcp/internal/server"
	"github.com/wentf9/xops-mcp/internal/testutil"
)

// Re-seal current-format payloads so the absent-field case cannot accidentally be
// normalized by the current archive encoder before the import is exercised.
func archiveTokenList(t *testing.T, encrypted []byte, vault *secure.Vault, tokens json.RawMessage) []byte {
	t.Helper()
	const magic = "XOPSDB\x03"
	const purpose = "xops-mcp database backup v3"
	plain, err := vault.Open(purpose, encrypted[len(magic):])
	if err != nil {
		t.Fatal(err)
	}
	defer clear(plain)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(plain, &fields); err != nil {
		t.Fatal(err)
	}
	if tokens == nil {
		delete(fields, "MCPTokens")
	} else {
		fields["MCPTokens"] = tokens
	}
	payload, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(payload)
	return append([]byte(magic), vault.Seal(purpose, payload)...)
}

func TestArchiveMissingAndEmptyTokensVerifyAfterImport(t *testing.T) {
	cfg, path := configuration(t)
	if _, err := run(t, "migrate", "--config", path); err != nil {
		t.Fatal(err)
	}
	host, err := server.OpenExistingHost(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testutil.Close(t, host) })
	candidate, _ := testutil.Plan(t, host.Store, host.Vault, testutil.Document(t))
	if _, err := host.Service.Apply(t.Context(), 0, candidate); err != nil {
		t.Fatal(err)
	}
	testutil.Close(t, host)
	file := filepath.Join(t.TempDir(), "source.xops")
	exported, err := run(t, "db-export", "--config", path, "--file", file)
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		SHA256 string `json:"sha256"`
	}
	if err := json.Unmarshal([]byte(exported), &source); err != nil {
		t.Fatal(err)
	}
	encrypted, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		tokens json.RawMessage
	}{
		{"missing", nil}, {"null", json.RawMessage("null")}, {"empty", json.RawMessage("[]")},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, targetPath := configuration(t)
			if _, err := run(t, "migrate", "--config", targetPath); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(t.TempDir(), "archive.xops")
			if err := os.WriteFile(file, archiveTokenList(t, encrypted, host.Vault, test.tokens), 0600); err != nil {
				t.Fatal(err)
			}
			for _, command := range []string{"db-import", "db-verify"} {
				args := []string{command, "--config", targetPath, "--file", file}
				if command == "db-import" {
					args = append(args, "--apply")
				}
				output, err := run(t, args...)
				if err != nil {
					t.Errorf("%s failed with %s token list: %v", command, test.name, err)
					continue
				}
				var report struct {
					SHA256   string `json:"sha256"`
					Verified bool   `json:"verified"`
				}
				if err := json.Unmarshal([]byte(output), &report); err != nil {
					t.Fatal(err)
				}
				if !report.Verified || report.SHA256 != source.SHA256 {
					t.Fatalf("%s verification differs from original export: %s", command, output)
				}
			}
		})
	}
}
