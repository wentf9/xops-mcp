package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	"github.com/wentf9/xops-cli/core/mcp/ports"
	"github.com/wentf9/xops-mcp/internal/storage"
)

const mcpTokenColumns = "id,client_id,name,prefix,digest,version,enabled,created_at,expires_at,revoked_at,node_scope,node_ids"

func scanMCPToken(row interface{ Scan(...any) error }) (storage.MCPTokenRecord, error) {
	var v storage.MCPTokenRecord
	t := &v.Token
	var nodeIDs []byte
	err := row.Scan(&t.ID, &t.ClientID, &t.Name, &t.Prefix, &v.Digest, &t.Version, &t.Enabled, &t.CreatedAt, &t.ExpiresAt, &t.RevokedAt, &t.NodeScope, &nodeIDs)
	if errors.Is(err, sql.ErrNoRows) {
		return v, storage.ErrNotFound
	}
	if err != nil {
		return v, err
	}
	if err := json.Unmarshal(nodeIDs, &t.NodeIDs); err != nil {
		return v, err
	}
	if err := t.NormalizeNodeScope(); err != nil {
		return v, err
	}
	return v, nil
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

func (s *Store) MCPTokenByID(ctx context.Context, id string) (storage.MCPTokenRecord, error) {
	ctx, release, err := s.acquire(ctx)
	if err != nil {
		return storage.MCPTokenRecord{}, err
	}
	defer release()
	return scanMCPToken(s.db.QueryRowContext(ctx, "SELECT "+mcpTokenColumns+" FROM mcp_tokens WHERE id=$1", id))
}

// Ambiguous client identities fail closed instead of choosing an arbitrary token.
func (s *Store) MCPTokenByClientID(ctx context.Context, clientID string) (storage.MCPTokenRecord, error) {
	ctx, release, err := s.acquire(ctx)
	if err != nil {
		return storage.MCPTokenRecord{}, err
	}
	defer release()
	return scanMCPToken(s.db.QueryRowContext(ctx, "SELECT "+mcpTokenColumns+" FROM mcp_tokens WHERE client_id=$1 AND (SELECT count(*) FROM mcp_tokens WHERE client_id=$1)=1", clientID))
}

func (s *Store) SaveMCPToken(ctx context.Context, expected uint64, v storage.MCPTokenRecord, event ports.AuditEvent) (retErr error) {
	ctx, release, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	if err := v.Token.NormalizeNodeScope(); err != nil {
		return err
	}
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
	var previous storage.MCPTokenRecord
	if expected > 0 {
		previous, err = scanMCPToken(tx.QueryRowContext(ctx, "SELECT "+mcpTokenColumns+" FROM mcp_tokens WHERE id=$1", t.ID))
		if errors.Is(err, storage.ErrNotFound) || err == nil && previous.Token.Version != expected {
			return storage.ErrConflict
		}
		if err != nil {
			return err
		}
	}
	// Deleted nodes remain in an existing restriction; only newly granted IDs
	// must currently exist. No deletion can turn a selected scope into all nodes.
	newIDs := []string{}
	for _, id := range t.NodeIDs {
		_, alreadyBound := slices.BinarySearch(previous.Token.NodeIDs, id)
		if previous.Token.NodeScope != storage.MCPTokenNodeScopeSelected || !alreadyBound {
			newIDs = append(newIDs, id)
		}
	}
	if len(newIDs) > 0 {
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM nodes WHERE id = ANY($1::text[])", newIDs).Scan(&count); err != nil {
			return err
		}
		if count != len(newIDs) {
			return storage.ErrInvalidMCPTokenScope
		}
	}

	nodeIDs, err := json.Marshal(t.NodeIDs)
	if err != nil {
		return err
	}

	if expected == 0 {
		result, err := tx.ExecContext(ctx, "INSERT INTO mcp_tokens("+mcpTokenColumns+") VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT DO NOTHING",
			t.ID, t.ClientID, t.Name, t.Prefix, v.Digest, t.Version, t.Enabled, t.CreatedAt, t.ExpiresAt, t.RevokedAt, t.NodeScope, string(nodeIDs))
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
		result, err := tx.ExecContext(ctx, "UPDATE mcp_tokens SET name=$1,version=$2,enabled=$3,expires_at=$4,revoked_at=$5,node_scope=$6,node_ids=$7 WHERE id=$8 AND version=$9 AND digest=$10 AND client_id=$11 AND prefix=$12 AND created_at=$13 AND revoked_at=0",
			t.Name, t.Version, t.Enabled, t.ExpiresAt, t.RevokedAt, t.NodeScope, string(nodeIDs), t.ID, expected, v.Digest, t.ClientID, t.Prefix, t.CreatedAt)
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
