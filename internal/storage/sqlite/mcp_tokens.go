package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

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
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return loadMCPTokens(ctx, s.db)
}

func (s *Store) MCPTokenByDigest(ctx context.Context, digest string) (storage.MCPTokenRecord, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return scanMCPToken(s.db.QueryRowContext(ctx, "SELECT "+mcpTokenColumns+" FROM mcp_tokens WHERE digest=?", digest))
}

func (s *Store) MCPTokenByID(ctx context.Context, id string) (storage.MCPTokenRecord, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return scanMCPToken(s.db.QueryRowContext(ctx, "SELECT "+mcpTokenColumns+" FROM mcp_tokens WHERE id=?", id))
}

// Ambiguous client identities fail closed instead of choosing an arbitrary token.
func (s *Store) MCPTokenByClientID(ctx context.Context, clientID string) (storage.MCPTokenRecord, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return scanMCPToken(s.db.QueryRowContext(ctx, "SELECT "+mcpTokenColumns+" FROM mcp_tokens WHERE client_id=? AND (SELECT count(*) FROM mcp_tokens WHERE client_id=?)=1", clientID, clientID))
}

func (s *Store) SaveMCPToken(ctx context.Context, expected uint64, v storage.MCPTokenRecord, event ports.AuditEvent) (retErr error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx, &retErr)
	var previous storage.MCPTokenRecord
	if expected > 0 {
		previous, err = scanMCPToken(tx.QueryRowContext(ctx, "SELECT "+mcpTokenColumns+" FROM mcp_tokens WHERE id=?", t.ID))
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
		data, err := json.Marshal(newIDs)
		if err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM nodes WHERE id IN (SELECT value FROM json_each(?))", string(data)).Scan(&count); err != nil {
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
		result, err := tx.ExecContext(ctx, "INSERT INTO mcp_tokens("+mcpTokenColumns+") VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING",
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
		result, err := tx.ExecContext(ctx, "UPDATE mcp_tokens SET name=?,version=?,enabled=?,expires_at=?,revoked_at=?,node_scope=?,node_ids=? WHERE id=? AND version=? AND digest=? AND client_id=? AND prefix=? AND created_at=? AND revoked_at=0",
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
	if _, err := tx.ExecContext(ctx, "INSERT INTO audit_events(occurred_at,operation_id,tool,outcome,event) VALUES(?,?,?,?,?)", event.Timestamp.Format(time.RFC3339Nano), event.OperationID, event.Tool, event.Outcome, data); err != nil {
		return err
	}
	return tx.Commit()
}
