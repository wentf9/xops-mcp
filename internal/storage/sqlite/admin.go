package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wentf9/xops-mcp/internal/storage"
)

var _ storage.AdminRepository = (*Store)(nil)

func (s *Store) Admin(ctx context.Context) (storage.Admin, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var a storage.Admin
	err := s.db.QueryRowContext(ctx, "SELECT username,password_hash,version FROM admin WHERE singleton=1").Scan(&a.Username, &a.PasswordHash, &a.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return a, storage.ErrNotFound
	}
	if err != nil {
		return a, fmt.Errorf("load administrator: %w", err)
	}
	return a, nil
}
func (s *Store) InitializeAdmin(ctx context.Context, a storage.Admin) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, err := s.db.ExecContext(ctx, "INSERT OR IGNORE INTO admin VALUES(1,?,?,?)", a.Username, a.PasswordHash, a.Version)
	if err != nil {
		return fmt.Errorf("initialize administrator: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return storage.ErrAdminExists
	}
	return nil
}
func (s *Store) ChangeAdminPassword(ctx context.Context, expected string, hash []byte, version string) (retErr error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx, &retErr)
	result, err := tx.ExecContext(ctx, "UPDATE admin SET password_hash=?,version=? WHERE singleton=1 AND version=?", hash, version, expected)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return storage.ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit administrator password: %w", err)
	}
	return nil
}
func (s *Store) Audit(ctx context.Context, q storage.AuditQuery) (_ []storage.AuditRecord, retErr error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if q.Limit < 1 || q.Limit > 100 || q.BeforeID < 0 {
		return nil, errors.New("invalid audit pagination")
	}
	var clauses []string
	var args []any
	if q.BeforeID > 0 {
		clauses = append(clauses, "id < ?")
		args = append(args, q.BeforeID)
	}
	if q.Outcome != "" {
		clauses = append(clauses, "outcome = ?")
		args = append(args, q.Outcome)
	}
	if q.OperationID != "" {
		clauses = append(clauses, "operation_id = ?")
		args = append(args, q.OperationID)
	}
	if q.NodeID != "" {
		clauses = append(clauses, "(json_extract(event,'$.node') = ? OR EXISTS(SELECT 1 FROM json_each(event,'$.nodes') WHERE value = ?))")
		args = append(args, q.NodeID, q.NodeID)
	}
	query := "SELECT id,event FROM audit_events"
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, q.Limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { retErr = errors.Join(retErr, rows.Close()) }()
	result := []storage.AuditRecord{}
	for rows.Next() {
		var record storage.AuditRecord
		var data []byte
		if err := rows.Scan(&record.ID, &data); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &record.Event); err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, rows.Err()
}
