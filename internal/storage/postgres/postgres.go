// Package postgres implements the single-instance PostgreSQL repository.
package postgres

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgconn/ctxwatch"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/wentf9/xops-cli/core/mcp/guardrail"
	"github.com/wentf9/xops-mcp/internal/secure"
	"github.com/wentf9/xops-mcp/internal/storage"
	"github.com/wentf9/xops-mcp/internal/storage/local"
)

//go:embed migrations/*.sql
var migrations embed.FS

const SchemaVersion = 3
const ownerLock int64 = 0x584f50534d4350

type Store struct {
	db       *sql.Conn
	pool     *sql.DB
	vault    *secure.Vault
	serial   chan struct{}
	lifetime context.Context
	cancel   context.CancelCauseFunc
	done     chan struct{}
	close    func() error
}

var _ storage.Database = (*Store)(nil)

func Open(ctx context.Context, dir, dsn string, vault *secure.Vault) (*Store, error) {
	return open(ctx, dir, dsn, vault, true)
}
func OpenExisting(ctx context.Context, dir, dsn string, vault *secure.Vault) (*Store, error) {
	return open(ctx, dir, dsn, vault, false)
}
func open(ctx context.Context, dir, dsn string, vault *secure.Vault, migrate bool) (_ *Store, retErr error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if vault == nil {
		return nil, errors.New("credential vault is required")
	}
	if dsn == "" {
		return nil, errors.New("PostgreSQL connection string is required")
	}
	cfg, err := connectionConfig(dsn)
	if err != nil {
		return nil, err // connectionConfig returns only sanitized diagnostics
	}
	// Cancel a query first, retaining the ownership session when PostgreSQL
	// responds. A broken network still has a bounded one-second close fallback.
	cfg.BuildContextWatcherHandler = func(conn *pgconn.PgConn) ctxwatch.Handler {
		return &pgconn.CancelRequestContextWatcherHandler{Conn: conn, DeadlineDelay: time.Second}
	}
	cfg.ConnectTimeout = 5 * time.Second
	cfg.RuntimeParams["search_path"] = "public"
	cfg.RuntimeParams["application_name"] = "xops-mcp"
	cfg.RuntimeParams["statement_timeout"] = "5000"
	cfg.RuntimeParams["lock_timeout"] = "2000"
	cfg.RuntimeParams["idle_in_transaction_session_timeout"] = "10000"
	lock, err := local.Lock(dir, migrate)
	if err != nil {
		return nil, err
	}
	owned := false
	defer func() {
		if !owned {
			retErr = errors.Join(retErr, lock.Close())
		}
	}()
	pool := stdlib.OpenDB(*cfg)
	// All operations use one pinned physical session, serialized below. Its
	// advisory lock cannot be lost and silently reacquired by a pool reconnect.
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(0)
	defer func() {
		if !owned {
			retErr = errors.Join(retErr, pool.Close())
		}
	}()
	conn, err := pool.Conn(ctx)
	if err != nil {
		return nil, errors.New("connect to PostgreSQL failed; check the database and connection file")
	}
	defer func() {
		if !owned {
			retErr = errors.Join(retErr, closeConnection(conn))
		}
	}()
	var locked bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", ownerLock).Scan(&locked); err != nil {
		return nil, errors.New("acquire PostgreSQL deployment lock failed")
	}
	if !locked {
		return nil, errors.New("PostgreSQL deployment is already in use")
	}
	lifetime, stop := context.WithCancelCause(context.Background())
	s := &Store{db: conn, pool: pool, vault: vault, serial: make(chan struct{}, 1), lifetime: lifetime, cancel: stop, done: make(chan struct{})}
	if err := s.migrate(ctx, vault, migrate); err != nil {
		stop(err)
		return nil, err
	}
	// Close cancels queries, joins the bounded heartbeat, then closes the pinned
	// session/pool (releasing the database lock) before releasing the local lock.
	s.close = sync.OnceValue(func() error {
		stop(errors.New("PostgreSQL store closed"))
		<-s.done
		return errors.Join(closeConnection(conn), pool.Close(), lock.Close())
	})
	go s.watch()
	owned = true
	return s, nil
}
func closeConnection(conn *sql.Conn) error {
	err := conn.Close()
	if errors.Is(err, sql.ErrConnDone) {
		return nil
	}
	return err
}

func (s *Store) Close() error              { return s.close() }
func (s *Store) Lifetime() context.Context { return s.lifetime }

func (s *Store) acquire(parent context.Context) (context.Context, func(), error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	stop := context.AfterFunc(s.lifetime, cancel)
	release := func() { stop(); cancel() }
	if s.lifetime.Err() != nil {
		release()
		return nil, nil, context.Cause(s.lifetime)
	}
	select {
	case s.serial <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-s.serial
			release()
			return nil, nil, err
		}
		return ctx, func() { <-s.serial; release() }, nil
	case <-ctx.Done():
		release()
		return nil, nil, ctx.Err()
	}
}
func (s *Store) watch() {
	defer close(s.done)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.lifetime.Done():
			return
		case <-ticker.C:
			ctx, release, err := s.acquire(s.lifetime)
			if err != nil {
				if s.lifetime.Err() == nil && !errors.Is(err, context.DeadlineExceeded) {
					s.cancel(errors.New("PostgreSQL ownership connection lost"))
				}
				continue
			}
			err = s.db.PingContext(ctx)
			release()
			if err != nil {
				s.cancel(errors.New("PostgreSQL ownership connection lost"))
				return
			}
		}
	}
}

// The stdlib driver retains BeginTx's context for rollback, and database/sql
// discards its connection on transaction-context cancellation. Keep a bounded
// cleanup lifetime separate from the request so cancellation can roll back on
// the SAME ownership session. Every statement still uses the request context;
// the caller must finish before releasing serial admission.
func (s *Store) begin(ctx context.Context, options *sql.TxOptions) (*sql.Tx, func(*error), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(5 * time.Second)
	}
	cleanup, cancel := context.WithDeadline(context.WithoutCancel(ctx), deadline.Add(2*time.Second))
	tx, err := s.db.BeginTx(cleanup, options)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	finish := func(result *error) { rollback(tx, result); cancel() }
	if err := ctx.Err(); err != nil {
		finish(&err)
		return nil, nil, err
	}
	return tx, finish, nil
}

func commit(ctx context.Context, tx *sql.Tx) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return tx.Commit()
}

func rollback(tx *sql.Tx, result *error) {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		*result = errors.Join(*result, fmt.Errorf("roll back database transaction: %w", err))
	}
}
func (s *Store) migrate(ctx context.Context, vault *secure.Vault, allow bool) (retErr error) {
	tx, finish, err := s.begin(ctx, nil)
	if err != nil {
		return err
	}
	defer finish(&retErr)
	var exists bool
	if err := tx.QueryRowContext(ctx, "SELECT to_regclass('public.schema_version') IS NOT NULL").Scan(&exists); err != nil {
		return err
	}
	version := 0
	if !exists {
		if !allow {
			return errors.New("deployment database is missing; run migrate first")
		}
		if _, err := tx.ExecContext(ctx, "CREATE TABLE schema_version(singleton SMALLINT PRIMARY KEY CHECK(singleton=1),version INTEGER NOT NULL CHECK(version>=0)); INSERT INTO schema_version VALUES(1,0)"); err != nil {
			return fmt.Errorf("initialize PostgreSQL schema: %w", err)
		}
	} else if err := tx.QueryRowContext(ctx, "SELECT version FROM schema_version WHERE singleton=1").Scan(&version); err != nil {
		return err
	}
	if version > SchemaVersion {
		return fmt.Errorf("database schema %d is newer than supported schema %d", version, SchemaVersion)
	}
	if !allow && version != SchemaVersion {
		return errors.New("database requires migration; run migrate first")
	}
	names := []string{"001_inventory.sql", "002_management.sql", "003_stateless_admin.sql"}
	for index := version; index < len(names); index++ {
		script, err := migrations.ReadFile("migrations/" + names[index])
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(script)); err != nil {
			return fmt.Errorf("apply PostgreSQL migration %d: %w", index+1, err)
		}
		if _, err := tx.ExecContext(ctx, "UPDATE schema_version SET version=$1 WHERE singleton=1", index+1); err != nil {
			return err
		}
	}
	var domain string
	var check []byte
	err = tx.QueryRowContext(ctx, "SELECT domain_id,key_check FROM deployment WHERE singleton=1").Scan(&domain, &check)
	if errors.Is(err, sql.ErrNoRows) {
		if !allow {
			return errors.New("deployment is not initialized; run migrate first")
		}
		domain = secure.ID()
		check = vault.Seal("deployment:"+domain, []byte("xops-mcp master key v1"))
		if _, err := tx.ExecContext(ctx, "INSERT INTO deployment VALUES(1,$1,0,$2)", domain, check); err != nil {
			return err
		}
		cfg := guardrail.DefaultGuardrailConfig()
		cfg.NoElicitFallback, cfg.AuditLog = "deny", ""
		data, err := json.Marshal(cfg)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO policies VALUES(1,$1)", string(data)); err != nil {
			return err
		}
	} else if err != nil {
		return fmt.Errorf("read deployment identity: %w", err)
	}
	plain, err := vault.Open("deployment:"+domain, check)
	if err != nil {
		return errors.New("master key does not match this deployment")
	}
	defer clear(plain)
	if string(plain) != "xops-mcp master key v1" {
		return errors.New("invalid deployment key check")
	}
	if err := commit(ctx, tx); err != nil {
		return fmt.Errorf("commit PostgreSQL migration: %w", err)
	}
	return nil
}
