package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/wentf9/xops-cli/core/mcp/ports"
	"github.com/wentf9/xops-mcp/internal/storage"
	"time"
)

const mcpTokenColumns = "id,client_id,name,prefix,digest,version,enabled,created_at,expires_at,revoked_at"

func scanMCPToken(row interface{ Scan(...any) error }) (storage.MCPTokenRecord, error) {
	var v storage.MCPTokenRecord
	t := &v.Token
	err := row.Scan(&t.ID, &t.ClientID, &t.Name, &t.Prefix, &v.Digest, &t.Version, &t.Enabled, &t.CreatedAt, &t.ExpiresAt, &t.RevokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return v, storage.ErrNotFound
	}
	return v, err
}

func loadMCPTokens(ctx context.Context, tx querier) ([]storage.MCPTokenRecord, error) {
	result := []storage.MCPTokenRecord{}
	err := rows(ctx, tx, "SELECT "+mcpTokenColumns+" FROM mcp_tokens ORDER BY created_at,id", func(r *sql.Rows) error {
		v, err := scanMCPToken(r)
		if err == nil {
			result = append(result, v)
		}
		return err
	})
	return result, err
}

func (s *Store) MCPTokens(ctx context.Context) ([]storage.MCPTokenRecord, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return loadMCPTokens(ctx, s.db)
}

func (s *Store) MCPTokenByDigest(ctx context.Context, digest string) (storage.MCPTokenRecord, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return scanMCPToken(s.db.QueryRowContext(ctx, "SELECT "+mcpTokenColumns+" FROM mcp_tokens WHERE digest=?", digest))
}

func (s *Store) SaveMCPToken(ctx context.Context, expected uint64, v storage.MCPTokenRecord, event ports.AuditEvent) (retErr error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := v.Validate(); err != nil {
		return err
	}
	t := v.Token
	if expected >= 1<<63-1 || t.Version != expected+1 {
		return storage.ErrConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx, &retErr)
	if expected == 0 {
		result, err := tx.ExecContext(ctx, "INSERT INTO mcp_tokens("+mcpTokenColumns+") VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING",
			t.ID, t.ClientID, t.Name, t.Prefix, v.Digest, t.Version, t.Enabled, t.CreatedAt, t.ExpiresAt, t.RevokedAt)
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
	} else {
		result, err := tx.ExecContext(ctx, "UPDATE mcp_tokens SET name=?,version=?,enabled=?,expires_at=?,revoked_at=? WHERE id=? AND version=? AND digest=? AND client_id=? AND prefix=? AND created_at=? AND revoked_at=0",
			t.Name, t.Version, t.Enabled, t.ExpiresAt, t.RevokedAt, t.ID, expected, v.Digest, t.ClientID, t.Prefix, t.CreatedAt)
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
	}
	// Management metadata and its redacted audit event commit atomically.
	event.Command, event.Error, event.Details = "", "", ""
	event.Paths = nil
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO audit_events(occurred_at,operation_id,tool,outcome,event) VALUES(?,?,?,?,?)", event.Timestamp.Format(time.RFC3339Nano), event.OperationID, event.Tool, event.Outcome, data); err != nil {
		return err
	}
	return tx.Commit()
}
