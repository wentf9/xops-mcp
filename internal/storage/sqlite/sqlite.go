// Package sqlite implements the single-instance SQLite repository.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wentf9/xops-cli/core/mcp/guardrail"
	"github.com/wentf9/xops-mcp/internal/secure"
	"github.com/wentf9/xops-mcp/internal/storage"
	"github.com/wentf9/xops-mcp/internal/storage/local"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

const SchemaVersion = 7

type Store struct {
	db    *sql.DB
	vault *secure.Vault
	close func() error
}

var _ storage.Repository = (*Store)(nil)

// Open holds a deployment-wide lock until Close, including during offline
// commands. SQLite alone cannot coordinate another process's in-memory gate.
func Open(ctx context.Context, dir string, vault *secure.Vault) (*Store, error) {
	return open(ctx, dir, vault, true)
}

// OpenExisting validates but never creates or upgrades a schema. Preview and
// inspection commands require an explicitly initialized deployment.
func OpenExisting(ctx context.Context, dir string, vault *secure.Vault) (*Store, error) {
	return open(ctx, dir, vault, false)
}

func open(ctx context.Context, dir string, vault *secure.Vault, allowMigration bool) (_ *Store, retErr error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if vault == nil {
		return nil, errors.New("credential vault is required")
	}
	if !allowMigration {
		if _, err := os.Stat(filepath.Join(dir, "xops.db")); err != nil {
			return nil, errors.New("deployment database is missing; run migrate first")
		}
	}
	lock, err := local.Lock(dir, allowMigration, "xops.db", "xops.db-wal", "xops.db-shm")
	if err != nil {
		return nil, err
	}
	owned := false
	defer func() {
		if !owned {
			retErr = errors.Join(retErr, lock.Close())
		}
	}()
	path := filepath.Join(dir, "xops.db")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("create database: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close database file: %w", err)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
	query := url.Values{}
	for _, pragma := range []string{"foreign_keys(1)", "journal_mode(WAL)", "busy_timeout(2000)", "synchronous(FULL)"} {
		query.Add("_pragma", pragma)
	}
	u.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	defer func() {
		if !owned {
			retErr = errors.Join(retErr, db.Close())
		}
	}()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{db: db, vault: vault}
	if err := s.migrate(ctx, vault, allowMigration); err != nil {
		return nil, err
	}
	s.close = sync.OnceValue(func() error { return errors.Join(db.Close(), lock.Close()) })
	owned = true
	return s, nil
}

func (s *Store) Close() error { return s.close() }

func rollback(tx *sql.Tx, result *error) {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		*result = errors.Join(*result, fmt.Errorf("roll back database transaction: %w", err))
	}
}

func (s *Store) migrate(ctx context.Context, vault *secure.Vault, allowMigration bool) (retErr error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin schema migration: %w", err)
	}
	defer rollback(tx, &retErr)
	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > SchemaVersion {
		return fmt.Errorf("database schema %d is newer than supported schema %d", version, SchemaVersion)
	}
	if !allowMigration && version != SchemaVersion {
		return errors.New("database requires migration; run migrate first")
	}
	names := []string{"001_inventory.sql", "002_audit.sql", "003_jump_chains.sql", "004_admin.sql", "005_tag_ids.sql", "006_stateless_admin.sql", "007_mcp_tokens.sql"}
	for index := version; index < len(names); index++ {
		script, err := migrations.ReadFile("migrations/" + names[index])
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(script)); err != nil {
			return fmt.Errorf("apply migration %d: %w", index+1, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", index+1)); err != nil {
			return err
		}
	}
	var domain string
	var check []byte
	err = tx.QueryRowContext(ctx, "SELECT domain_id, key_check FROM deployment WHERE singleton = 1").Scan(&domain, &check)
	if errors.Is(err, sql.ErrNoRows) {
		if !allowMigration {
			return errors.New("deployment is not initialized; run migrate first")
		}
		domain = secure.ID()
		check = vault.Seal("deployment:"+domain, []byte("xops-mcp master key v1"))
		if _, err = tx.ExecContext(ctx, "INSERT INTO deployment VALUES (1, ?, 0, ?)", domain, check); err != nil {
			return err
		}
		cfg := guardrail.DefaultGuardrailConfig()
		cfg.NoElicitFallback, cfg.AuditLog = "deny", ""
		data, err := json.Marshal(cfg)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO policies VALUES (1, ?)", data); err != nil {
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
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit schema migration: %w", err)
	}
	return nil
}
