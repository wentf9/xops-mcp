package postgres

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
	if _, err := tx.ExecContext(ctx, "DELETE FROM admin_sessions"); err != nil {
		return err
	}
	if err := commit(ctx, tx); err != nil {
		return fmt.Errorf("commit administrator password: %w", err)
	}
	return nil
}
func (s *Store) CreateSession(ctx context.Context, session storage.Session) (retErr error) {
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
	if _, err := tx.ExecContext(ctx, "DELETE FROM admin_sessions WHERE expires_at <= $1", time.Now().Unix()); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO admin_sessions(digest,admin_version,expires_at)
SELECT $1,$2,$3 WHERE EXISTS(SELECT 1 FROM admin WHERE version=$4)
AND (SELECT count(*) FROM admin_sessions)<64`, session.Digest, session.AdminVersion, session.ExpiresAt.Unix(), session.AdminVersion)
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
		return fmt.Errorf("create administrator session: %w", err)
	}
	return nil
}
func (s *Store) Session(ctx context.Context, digest []byte, now time.Time) (storage.Session, error) {
	ctx, release, err := s.acquire(ctx)
	if err != nil {
		return storage.Session{}, err
	}
	defer release()
	var session storage.Session
	var expires int64
	err = s.db.QueryRowContext(ctx, `SELECT s.admin_version,s.expires_at FROM admin_sessions s JOIN admin a ON a.version=s.admin_version WHERE s.digest=$1 AND s.expires_at>$2`, digest, now.Unix()).Scan(&session.AdminVersion, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return session, storage.ErrNotFound
	}
	if err != nil {
		return session, err
	}
	session.Digest = digest
	session.ExpiresAt = time.Unix(expires, 0).UTC()
	return session, nil
}
func (s *Store) DeleteSession(ctx context.Context, digest []byte) error {
	ctx, release, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	_, err = s.db.ExecContext(ctx, "DELETE FROM admin_sessions WHERE digest=$1", digest)
	return err
}
func (s *Store) DeleteSessions(ctx context.Context) error {
	ctx, release, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	_, err = s.db.ExecContext(ctx, "DELETE FROM admin_sessions")
	return err
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
