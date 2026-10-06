package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/wentf9/xops-cli/core/mcp/ports"
	"github.com/wentf9/xops-mcp/internal/storage"
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
	ctx, release, err := s.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return loadMCPTokens(ctx, s.db)
}

func (s *Store) MCPTokenByDigest(ctx context.Context, digest string) (storage.MCPTokenRecord, error) {
	ctx, release, err := s.acquire(ctx)
	if err != nil {
		return storage.MCPTokenRecord{}, err
	}
	defer release()
	return scanMCPToken(s.db.QueryRowContext(ctx, "SELECT "+mcpTokenColumns+" FROM mcp_tokens WHERE digest=$1", digest))
}

func (s *Store) SaveMCPToken(ctx context.Context, expected uint64, v storage.MCPTokenRecord, event ports.AuditEvent) (retErr error) {
	ctx, release, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	if err := v.Validate(); err != nil {
		return err
	}
	t := v.Token
	if expected >= 1<<63-1 || t.Version != expected+1 {
		return storage.ErrConflict
	}
	tx, finish, err := s.begin(ctx, nil)
	if err != nil {
		return err
	}
	defer finish(&retErr)
	if expected == 0 {
		result, err := tx.ExecContext(ctx, "INSERT INTO mcp_tokens("+mcpTokenColumns+") VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT DO NOTHING",
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
		result, err := tx.ExecContext(ctx, "UPDATE mcp_tokens SET name=$1,version=$2,enabled=$3,expires_at=$4,revoked_at=$5 WHERE id=$6 AND version=$7 AND digest=$8 AND client_id=$9 AND prefix=$10 AND created_at=$11 AND revoked_at=0",
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
	if _, err := tx.ExecContext(ctx, "INSERT INTO audit_events(occurred_at,operation_id,tool,outcome,event) VALUES($1,$2,$3,$4,$5)", event.Timestamp, event.OperationID, event.Tool, event.Outcome, string(data)); err != nil {
		return err
	}
	return commit(ctx, tx)
}
