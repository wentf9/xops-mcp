package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/wentf9/xops-mcp/internal/storage"
	"github.com/wentf9/xops-mcp/internal/storage/archive"
)

func (s *Store) Export(ctx context.Context) (_ storage.Backup, retErr error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return storage.Backup{}, err
	}
	defer rollback(tx, &retErr)
	b := storage.Backup{Format: storage.BackupFormat}
	b.Inventory, err = loadInventory(ctx, tx)
	if err != nil {
		return b, err
	}
	if err := tx.QueryRowContext(ctx, "SELECT key_check FROM deployment WHERE singleton=1").Scan(&b.KeyCheck); err != nil {
		return b, err
	}
	b.MCPTokens, err = loadMCPTokens(ctx, tx)
	if err != nil {
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
	if err := tx.Commit(); err != nil {
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
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := archive.Validate(ctx, &b, s.vault); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx, &retErr)
	var revision uint64
	if err := tx.QueryRowContext(ctx, "SELECT revision FROM deployment WHERE singleton=1").Scan(&revision); err != nil {
		return err
	}
	if revision != 0 {
		return errors.New("database restore requires an empty target")
	}
	for _, table := range []string{"hosts", "identities", "nodes", "credentials", "credential_versions", "tags", "tombstones", "sources", "audit_events", "admin", "mcp_tokens"} {
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return errors.New("database restore requires an empty target")
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE deployment SET domain_id=?,key_check=? WHERE singleton=1", b.Inventory.DomainID, b.KeyCheck); err != nil {
		return err
	}
	if err := saveInventory(ctx, tx, 0, b.Inventory, b.Sources); err != nil {
		return err
	}
	for _, v := range b.Versions {
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO credential_versions VALUES(?,?,?,?)", v.ID, v.Version, v.Kind, v.Ciphertext); err != nil {
			return err
		}
	}
	for _, a := range b.Audit {
		data, err := json.Marshal(a.Event)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO audit_events(id,occurred_at,operation_id,tool,outcome,event) VALUES(?,?,?,?,?,?)", a.ID, a.Event.Timestamp.Format(time.RFC3339Nano), a.Event.OperationID, a.Event.Tool, a.Event.Outcome, data); err != nil {
			return err
		}
	}
	for _, record := range b.MCPTokens {
		t := record.Token
		if _, err := tx.ExecContext(ctx, "INSERT INTO mcp_tokens("+mcpTokenColumns+") VALUES(?,?,?,?,?,?,?,?,?,?)", t.ID, t.ClientID, t.Name, t.Prefix, record.Digest, t.Version, t.Enabled, t.CreatedAt, t.ExpiresAt, t.RevokedAt); err != nil {
			return err
		}
	}
	if b.Admin != nil {
		if _, err := tx.ExecContext(ctx, "INSERT INTO admin VALUES(1,?,?,?)", b.Admin.Username, b.Admin.PasswordHash, b.Admin.Version); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit database restore (verify before retry): %w", err)
	}
	return nil
}
