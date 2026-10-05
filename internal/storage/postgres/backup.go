package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/wentf9/xops-mcp/internal/storage"
	"github.com/wentf9/xops-mcp/internal/storage/archive"
)

func (s *Store) Export(ctx context.Context) (_ storage.Backup, retErr error) {
	ctx, release, err := s.acquire(ctx)
	if err != nil {
		return storage.Backup{}, err
	}
	defer release()
	tx, finish, err := s.begin(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return storage.Backup{}, err
	}
	defer finish(&retErr)
	b := storage.Backup{Format: 1}
	b.Inventory, err = loadInventory(ctx, tx)
	if err != nil {
		return b, err
	}
	if err := tx.QueryRowContext(ctx, "SELECT key_check FROM deployment WHERE singleton=1").Scan(&b.KeyCheck); err != nil {
		return b, err
	}
	a := storage.BackupAdmin{}
	err = tx.QueryRowContext(ctx, "SELECT username,password_hash,version FROM admin WHERE singleton=1").Scan(&a.Username, &a.PasswordHash, &a.Version)
	if err == nil {
		b.Admin = &a
	} else if !errors.Is(err, sql.ErrNoRows) {
		return b, err
	}
	if err := rows(ctx, tx, "SELECT id,version,kind,ciphertext FROM credential_versions", func(r *sql.Rows) error {
		var v storage.CredentialVersion
		err := r.Scan(&v.ID, &v.Version, &v.Kind, &v.Ciphertext)
		b.Versions = append(b.Versions, v)
		return err
	}); err != nil {
		return b, err
	}
	if err := rows(ctx, tx, "SELECT token,node_id,host,port,username,purpose,credential_id,version,host_key FROM sources", func(r *sql.Rows) error {
		var v storage.Source
		err := r.Scan(&v.Token, &v.NodeID, &v.Host, &v.Port, &v.User, &v.Purpose, &v.CredentialID, &v.Version, &v.HostKey)
		b.Sources = append(b.Sources, v)
		return err
	}); err != nil {
		return b, err
	}
	if err := rows(ctx, tx, "SELECT id,event FROM audit_events ORDER BY id", func(r *sql.Rows) error {
		var v storage.AuditRecord
		var data []byte
		if err := r.Scan(&v.ID, &data); err != nil {
			return err
		}
		if err := json.Unmarshal(data, &v.Event); err != nil {
			return err
		}
		b.Audit = append(b.Audit, v)
		return nil
	}); err != nil {
		return b, err
	}
	if err := commit(ctx, tx); err != nil {
		return b, err
	}
	if err := archive.Validate(ctx, &b, s.vault); err != nil {
		return b, err
	}
	return b, nil
}

// Restore replaces only an initialized, empty database, in one transaction.
// Existing deployments are never overwritten. Sessions are not transferable.
func (s *Store) Restore(ctx context.Context, b storage.Backup) (retErr error) {
	ctx, release, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	if err := archive.Validate(ctx, &b, s.vault); err != nil {
		return err
	}
	tx, finish, err := s.begin(ctx, nil)
	if err != nil {
		return err
	}
	defer finish(&retErr)
	var revision uint64
	if err := tx.QueryRowContext(ctx, "SELECT revision FROM deployment WHERE singleton=1 FOR UPDATE").Scan(&revision); err != nil {
		return err
	}
	if revision != 0 {
		return errors.New("database restore requires an empty target")
	}
	for _, table := range []string{"hosts", "identities", "nodes", "credentials", "credential_versions", "tags", "tombstones", "sources", "audit_events", "admin"} {
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return errors.New("database restore requires an empty target")
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE deployment SET domain_id=$1,key_check=$2 WHERE singleton=1", b.Inventory.DomainID, b.KeyCheck); err != nil {
		return err
	}
	if err := saveInventory(ctx, tx, 0, b.Inventory, b.Sources); err != nil {
		return err
	}
	versions := newInsertBatch(ctx, tx, "INSERT INTO credential_versions(id,version,kind,ciphertext)", " ON CONFLICT(id,version) DO NOTHING")
	for _, v := range b.Versions {
		if err := versions.add(v.ID, v.Version, v.Kind, v.Ciphertext); err != nil {
			return err
		}
	}
	if err := versions.flush(); err != nil {
		return err
	}
	audit := newInsertBatch(ctx, tx, "INSERT INTO audit_events(id,occurred_at,operation_id,tool,outcome,event)", "")
	for _, a := range b.Audit {
		data, err := json.Marshal(a.Event)
		if err != nil {
			return err
		}
		if err := audit.add(a.ID, a.Event.Timestamp, a.Event.OperationID, a.Event.Tool, a.Event.Outcome, string(data)); err != nil {
			return err
		}
	}
	if err := audit.flush(); err != nil {
		return err
	}
	// RESTART is transactional (unlike setval), so failed restore also rolls
	// back the identity sequence. The next audit ID follows imported records.
	next := int64(1)
	if len(b.Audit) > 0 {
		next = b.Audit[len(b.Audit)-1].ID + 1
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("ALTER TABLE audit_events ALTER COLUMN id RESTART WITH %d", next)); err != nil {
		return err
	}
	if b.Admin != nil {
		if _, err := tx.ExecContext(ctx, "INSERT INTO admin VALUES(1,$1,$2,$3)", b.Admin.Username, b.Admin.PasswordHash, b.Admin.Version); err != nil {
			return err
		}
	}
	if err := commit(ctx, tx); err != nil {
		return fmt.Errorf("commit database restore (verify before retry): %w", err)
	}
	return nil
}
