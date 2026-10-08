package postgres

import (
	"strings"
	"testing"

	"github.com/wentf9/xops-cli/core/mcp/ports"
	"github.com/wentf9/xops-mcp/internal/storage"
)

func TestTokenNodeScopeMigrationPreservesExistingTokens(t *testing.T) {
	s, vault, dir, dsn := fixture(t)
	original := storage.MCPTokenRecord{Token: storage.MCPToken{ID: "legacy-token", ClientID: "legacy-client", Name: "legacy", Version: 1, CreatedAt: 1, Enabled: true}, Digest: strings.Repeat("a", 64)}
	if err := s.SaveMCPToken(t.Context(), 0, original, ports.AuditEvent{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), `ALTER TABLE mcp_tokens DROP COLUMN node_ids; ALTER TABLE mcp_tokens DROP COLUMN node_scope; UPDATE schema_version SET version=4`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(t.Context(), dir, dsn, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := upgraded.Close(); err != nil {
			t.Error(err)
		}
	})
	got, err := upgraded.MCPTokenByID(t.Context(), original.Token.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Token.NodeScope != storage.MCPTokenNodeScopeAll || got.Token.NodeIDs == nil || len(got.Token.NodeIDs) != 0 {
		t.Fatalf("migration changed legacy permissions: %+v", got.Token)
	}
	got.Token.NodeScope, got.Token.NodeIDs = "", nil
	if got.Digest != original.Digest || got.Token.ID != original.Token.ID || got.Token.ClientID != original.Token.ClientID || got.Token.Version != original.Token.Version || got.Token.Enabled != original.Token.Enabled {
		t.Fatal("migration changed token identity or state")
	}
}
