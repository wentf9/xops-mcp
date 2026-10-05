package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/wentf9/xops-mcp/internal/storage"
)

var _ storage.AdminRepository = (*Store)(nil)

func (s *Store) Admin(ctx context.Context) (storage.Admin, error) {
	ctx, release, err := s.acquire(ctx)
	if err != nil {
		return storage.Admin{}, err
	}
	defer release()
	var a storage.Admin
	err = s.db.QueryRowContext(ctx, "SELECT username,password_hash,version FROM admin WHERE singleton=1").Scan(&a.Username, &a.PasswordHash, &a.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return a, storage.ErrNotFound
	}
	if err != nil {
		return a, fmt.Errorf("load administrator: %w", err)
	}
	return a, nil
}
func (s *Store) InitializeAdmin(ctx context.Context, a storage.Admin) error {
	ctx, release, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	result, err := s.db.ExecContext(ctx, "INSERT INTO admin VALUES(1,$1,$2,$3) ON CONFLICT(singleton) DO NOTHING", a.Username, a.PasswordHash, a.Version)
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
	ctx, release, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	tx, finish, err := s.begin(ctx, nil)
	if err != nil {
		return err
	}
	defer finish(&retErr)
	result, err := tx.ExecContext(ctx, "UPDATE admin SET password_hash=$1,version=$2 WHERE singleton=1 AND version=$3", hash, version, expected)
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
	if err := commit(ctx, tx); err != nil {
		return fmt.Errorf("commit administrator password: %w", err)
	}
	return nil
}
func (s *Store) Audit(ctx context.Context, q storage.AuditQuery) (_ []storage.AuditRecord, retErr error) {
	ctx, release, err := s.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	if q.Limit < 1 || q.Limit > 100 || q.BeforeID < 0 {
		return nil, errors.New("invalid audit pagination")
	}
	var clauses []string
	var args []any
	add := func(clause string, value any) {
		args = append(args, value)
		clauses = append(clauses, fmt.Sprintf(clause, len(args)))
	}
	if q.BeforeID > 0 {
		add("id < $%d", q.BeforeID)
	}
	if q.Outcome != "" {
		add("outcome = $%d", q.Outcome)
	}
	if q.OperationID != "" {
		add("operation_id = $%d", q.OperationID)
	}
	if q.NodeID != "" {
		add("(event->>'node' = $%[1]d OR (event->'nodes') ? $%[1]d)", q.NodeID)
	}
	query := "SELECT id,event FROM audit_events"
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	args = append(args, q.Limit)
	query += fmt.Sprintf(" ORDER BY id DESC LIMIT $%d", len(args))
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
