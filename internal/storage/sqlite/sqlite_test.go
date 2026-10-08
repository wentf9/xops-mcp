package sqlite

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/wentf9/xops-cli/core/mcp/ports"
	"github.com/wentf9/xops-mcp/internal/secure"
	"github.com/wentf9/xops-mcp/internal/storage"
)

func openTest(t *testing.T) (*Store, *secure.Vault, string) {
	t.Helper()
	vault, err := secure.New(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "data")
	s, err := Open(t.Context(), dir, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s, vault, dir
}

func TestMigrationLockIdentityAndWrongKey(t *testing.T) {
	s, vault, dir := openTest(t)
	for query, want := range map[string]string{"PRAGMA journal_mode": "wal", "PRAGMA foreign_keys": "1", "PRAGMA user_version": "8"} {
		var got string
		if err := s.db.QueryRowContext(t.Context(), query).Scan(&got); err != nil || got != want {
			t.Fatalf("%s = %s: %v", query, got, err)
		}
	}
	if other, err := Open(t.Context(), dir, vault); err == nil {
		t.Error("duplicate deployment owner accepted")
		if err := other.Close(); err != nil {
			t.Error(err)
		}
	}
	base, err := s.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if base.Policy.NoElicitFallback != "deny" || !base.Policy.Enabled {
		t.Fatal("unsafe default policy")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	wrong, err := secure.New(bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatal(err)
	}
	if other, err := Open(t.Context(), dir, wrong); err == nil {
		t.Error("accepted wrong key")
		if err := other.Close(); err != nil {
			t.Error(err)
		}
	}
	reopened, err := Open(t.Context(), dir, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	}()
	loaded, err := reopened.Load(t.Context())
	if err != nil || loaded.DomainID != base.DomainID || loaded.Revision != base.Revision {
		t.Fatalf("deployment identity changed: %v", err)
	}
}

func TestUpgradeAndFutureVersionFailClosed(t *testing.T) {
	s, vault, dir := openTest(t)
	restoreLegacyTagSchema(t, s)
	if _, err := s.db.ExecContext(t.Context(), "DROP TABLE IF EXISTS admin_sessions; DROP TABLE admin; DROP TABLE node_jumps; DROP TABLE audit_events; PRAGMA user_version=1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if preview, err := OpenExisting(t.Context(), dir, vault); err == nil {
		t.Error("preview performed migration")
		if err := preview.Close(); err != nil {
			t.Error(err)
		}
	}
	upgraded, err := Open(t.Context(), dir, vault)
	if err != nil {
		t.Fatal(err)
	}
	if err := upgraded.Append(t.Context(), ports.AuditEvent{OperationID: "upgrade"}); err != nil {
		t.Fatal(err)
	}
	if _, err := upgraded.db.ExecContext(t.Context(), "PRAGMA user_version=999"); err != nil {
		t.Fatal(err)
	}
	if err := upgraded.Close(); err != nil {
		t.Fatal(err)
	}
	if future, err := Open(t.Context(), dir, vault); err == nil {
		t.Error("future schema accepted")
		if err := future.Close(); err != nil {
			t.Error(err)
		}
	}
}

func TestV2SingleJumpMigrationPreservesNodeIdentities(t *testing.T) {
	s, vault, dir := openTest(t)
	restoreLegacyTagSchema(t, s)
	_, err := s.db.ExecContext(t.Context(), `DROP TABLE IF EXISTS admin_sessions; DROP TABLE admin; DROP TABLE node_jumps;
INSERT INTO hosts VALUES ('host','host','127.0.0.1',22,'');
INSERT INTO identities VALUES ('identity','identity','fixture',NULL);
INSERT INTO nodes VALUES ('jump','jump','host','identity',NULL,'none',NULL,1);
INSERT INTO nodes VALUES ('target','target','host','identity','jump','none',NULL,1);
UPDATE deployment SET revision=7;
PRAGMA user_version=2;`)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(t.Context(), dir, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := upgraded.Close(); err != nil {
			t.Error(err)
		}
	}()
	v, err := upgraded.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if v.Revision != 7 || len(v.Nodes) != 2 || len(v.Nodes["target"].JumpIDs) != 1 || v.Nodes["target"].JumpIDs[0] != "jump" || len(v.Nodes["jump"].JumpIDs) != 0 {
		t.Fatalf("migration changed jump identity: %+v", v.Nodes)
	}
}

func TestTransactionConflictRollbackAndAuditRedaction(t *testing.T) {
	s, _, _ := openTest(t)
	v, err := s.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	v.Revision = 1
	v.Hosts["host"] = storage.Host{ID: "host", Name: "host", Address: "127.0.0.1", Port: 22}
	if err := s.Save(t.Context(), 0, v, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(t.Context(), 0, v, nil); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("stale write = %v", err)
	}
	v.Revision = 2
	v.Nodes["missing"] = storage.Node{ID: "missing", Name: "broken", HostID: "missing", IdentityID: "missing"}
	if err := s.Save(t.Context(), 1, v, nil); err == nil {
		t.Fatal("foreign key violation committed")
	}
	actual, err := s.Load(t.Context())
	if err != nil || actual.Revision != 1 || len(actual.Nodes) != 0 || len(actual.Hosts) != 1 {
		t.Fatalf("transaction not rolled back: %+v %v", actual, err)
	}
	event := ports.AuditEvent{OperationID: "operation", Tool: "tool", Outcome: "executed", Command: "synthetic-secret", Error: "synthetic-secret", Details: "synthetic-secret", Paths: []string{"synthetic-secret"}, Binding: "binding"}
	if err := s.Append(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	var data []byte
	if err := s.db.QueryRowContext(t.Context(), "SELECT event FROM audit_events").Scan(&data); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("synthetic-secret")) || !bytes.Contains(data, []byte("executed")) {
		t.Fatalf("audit redaction = %s", data)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.Append(ctx, event); err == nil {
		t.Fatal("cancelled audit succeeded")
	}
}

func TestPrivateDirectoryAndNoCreationDuringPreview(t *testing.T) {
	vault, err := secure.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "missing")
	if s, err := OpenExisting(t.Context(), dir, vault); err == nil {
		t.Error("created deployment in preview")
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("preview created path: %v", err)
	}
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(t.Context(), dir, vault); err == nil {
		t.Error("public data directory accepted")
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	}
}

func TestStatelessMigrationRemovesLegacySessions(t *testing.T) {
	s, vault, dir := openTest(t)
	if err := s.InitializeAdmin(t.Context(), storage.Admin{Username: "admin", PasswordHash: []byte("fixture-hash"), Version: "v1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), `DROP TABLE mcp_tokens; CREATE TABLE admin_sessions(digest BLOB PRIMARY KEY,admin_version TEXT,expires_at INTEGER);
INSERT INTO admin_sessions VALUES(zeroblob(32),'v1',9999999999); PRAGMA user_version=5`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(t.Context(), dir, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := upgraded.Close(); err != nil {
			t.Error(err)
		}
	}()
	a, err := upgraded.Admin(t.Context())
	if err != nil || a.Version != "v1" || string(a.PasswordHash) != "fixture-hash" {
		t.Fatal("administrator changed during auth migration", err)
	}
	var count int
	if err := upgraded.db.QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_master WHERE name='admin_sessions'").Scan(&count); err != nil || count != 0 {
		t.Fatal("legacy session state retained", err)
	}
}
