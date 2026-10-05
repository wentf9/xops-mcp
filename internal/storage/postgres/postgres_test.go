package postgres

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/wentf9/xops-mcp/internal/secure"
	"github.com/wentf9/xops-mcp/internal/storage"
	"github.com/wentf9/xops-mcp/internal/testutil/pgfixture"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }
func fixture(t *testing.T) (*Store, *secure.Vault, string, string) {
	t.Helper()
	dsn := pgfixture.DSN(t)
	dir := filepath.Join(t.TempDir(), "data")
	vault, err := secure.New(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(t.Context(), dir, dsn, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s, vault, dir, dsn
}
func rawConnection(t *testing.T, dsn string) *pgx.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	c, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := c.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return c
}
func TestMigrationOwnershipWrongKeyAndPoolClose(t *testing.T) {
	s, vault, dir, dsn := fixture(t)
	base, err := s.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, otherDir := range []string{dir, filepath.Join(t.TempDir(), "second")} {
		other, err := Open(t.Context(), otherDir, dsn, vault)
		if err == nil {
			if err := other.Close(); err != nil {
				t.Error(err)
			}
			t.Fatal("duplicate database owner accepted")
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s.pool.Stats().OpenConnections != 0 {
		t.Fatal("connection pool retained a connection")
	}
	if _, err := s.Load(t.Context()); err == nil {
		t.Fatal("closed store reconnected")
	}
	wrong, _ := secure.New(bytes.Repeat([]byte{2}, 32))
	if other, err := Open(t.Context(), dir, dsn, wrong); err == nil {
		if err := other.Close(); err != nil {
			t.Error(err)
		}
		t.Fatal("wrong key accepted")
	}
	reopened, err := OpenExisting(t.Context(), dir, dsn, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	}()
	actual, err := reopened.Load(t.Context())
	if err != nil || actual.DomainID != base.DomainID || actual.Revision != 0 {
		t.Fatalf("identity changed after reopen: %v", err)
	}
}
func TestMigrationRollbackUpgradeAndFutureVersion(t *testing.T) {
	s, vault, dir, dsn := fixture(t)
	// Simulate the previous PostgreSQL schema. A conflicting table makes the
	// second migration fail after its first DDL; the whole upgrade must roll back.
	if _, err := s.db.ExecContext(t.Context(), `DROP TABLE IF EXISTS admin_sessions; DROP TABLE admin; DROP TABLE audit_events;
UPDATE schema_version SET version=1; CREATE TABLE admin(blocker TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if p, err := OpenExisting(t.Context(), dir, dsn, vault); err == nil {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
		t.Fatal("inspection upgraded schema")
	}
	if p, err := Open(t.Context(), dir, dsn, vault); err == nil {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
		t.Fatal("broken migration succeeded")
	}
	c := rawConnection(t, dsn)
	var version int
	var absent bool
	if err := c.QueryRow(t.Context(), "SELECT version FROM schema_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := c.QueryRow(t.Context(), "SELECT to_regclass('audit_events') IS NULL").Scan(&absent); err != nil {
		t.Fatal(err)
	}
	if version != 1 || !absent {
		t.Fatal("failed migration left a partial schema/version")
	}
	if _, err := c.Exec(t.Context(), "DROP TABLE admin"); err != nil {
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
	if _, err := upgraded.db.ExecContext(t.Context(), "UPDATE schema_version SET version=999"); err != nil {
		t.Fatal(err)
	}
	if err := upgraded.Close(); err != nil {
		t.Fatal(err)
	}
	if p, err := Open(t.Context(), dir, dsn, vault); err == nil {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
		t.Fatal("future schema accepted")
	}
}
func TestInitialMigrationRollbackAndExistingDoesNotCreate(t *testing.T) {
	dsn := pgfixture.DSN(t)
	dir := filepath.Join(t.TempDir(), "data")
	vault, _ := secure.New(make([]byte, 32))
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if p, err := OpenExisting(t.Context(), dir, dsn, vault); err == nil {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
		t.Fatal("inspection created schema")
	}
	c := rawConnection(t, dsn)
	if _, err := c.Exec(t.Context(), "CREATE TABLE hosts(blocker TEXT)"); err != nil {
		t.Fatal(err)
	}
	if p, err := Open(t.Context(), dir, dsn, vault); err == nil {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
		t.Fatal("partial migration succeeded")
	}
	var absent bool
	if err := c.QueryRow(t.Context(), "SELECT to_regclass('schema_version') IS NULL AND to_regclass('deployment') IS NULL").Scan(&absent); err != nil || !absent {
		t.Fatalf("initial migration did not roll back: %v", err)
	}
	if _, err := c.Exec(t.Context(), "DROP TABLE hosts"); err != nil {
		t.Fatal(err)
	}
	s, err := Open(t.Context(), dir, dsn, vault)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}
func TestOwnerConnectionLossFailsClosed(t *testing.T) {
	s, _, _, dsn := fixture(t)
	var pid int
	if err := s.db.QueryRowContext(t.Context(), "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	c := rawConnection(t, dsn)
	if _, err := c.Exec(t.Context(), "SELECT pg_terminate_backend($1)", pid); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Lifetime().Done():
	case <-time.After(7 * time.Second):
		t.Fatal("lost owner session was not detected")
	}
	if _, err := s.Load(t.Context()); err == nil {
		t.Fatal("lost store automatically reconnected")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s.pool.Stats().OpenConnections != 0 {
		t.Fatal("lost pool not closed")
	}
}
func TestLockWaitCancellationRollsBack(t *testing.T) {
	s, _, _, dsn := fixture(t)
	base, err := s.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	c := rawConnection(t, dsn)
	tx, err := c.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tx.Rollback(t.Context()); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Error(err)
		}
	}()
	if _, err := tx.Exec(t.Context(), "SELECT 1 FROM deployment FOR UPDATE"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	base.Revision++
	if err := s.Save(ctx, 0, base, nil); err == nil {
		t.Fatal("write ignored row lock/deadline")
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if actual, err := s.Load(t.Context()); err != nil || actual.Revision != 0 {
		t.Fatalf("cancelled transaction committed or lost ownership: %v", err)
	}
}
func TestConnectionConfigurationIsExplicitAndRedacted(t *testing.T) {
	for _, dsn := range []string{"", "password=secret", "postgres://u:secret@host/db?service=personal", "postgres://u@host/db", "postgres://u:secret@host/db?sslmode=disable&sslmode=require"} {
		_, err := connectionConfig(dsn)
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("invalid connection accepted or leaked password")
		}
	}
	t.Setenv("PGPASSWORD", "personal-secret")
	t.Setenv("PGHOST", "personal-host")
	t.Setenv("PGDATABASE", "personal-db")
	cfg, err := connectionConfig("postgres://fixture:deployment-password@localhost/test?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "localhost" || cfg.Database != "test" || cfg.Password != "deployment-password" {
		t.Fatal("deployment URL inherited personal credentials")
	}
	t.Setenv("PGSERVICE", "personal")
	if _, err := connectionConfig("postgres://u:p@localhost/test?sslmode=disable"); err == nil {
		t.Fatal("implicit service configuration accepted")
	}
}

func TestStatelessMigrationRemovesLegacySessions(t *testing.T) {
	s, vault, dir, dsn := fixture(t)
	if err := s.InitializeAdmin(t.Context(), storage.Admin{Username: "admin", PasswordHash: []byte("fixture-hash"), Version: "v1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), `CREATE TABLE admin_sessions(digest BYTEA PRIMARY KEY,admin_version TEXT,expires_at BIGINT);
INSERT INTO admin_sessions VALUES(decode(repeat('00',32),'hex'),'v1',9999999999); UPDATE schema_version SET version=2`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(t.Context(), dir, dsn, vault)
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
	var absent bool
	if err := upgraded.db.QueryRowContext(t.Context(), "SELECT to_regclass('public.admin_sessions') IS NULL").Scan(&absent); err != nil || !absent {
		t.Fatal("legacy session state retained", err)
	}
}
