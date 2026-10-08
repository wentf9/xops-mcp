// Package mcpauth owns MCP client credentials independently of administrator JWTs.
package mcpauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/wentf9/xops-cli/core/mcp/ports"
	"github.com/wentf9/xops-mcp/internal/naming"
	"github.com/wentf9/xops-mcp/internal/storage"
)

var ErrInvalid = errors.New("invalid MCP token settings")

type Manager struct {
	Store storage.MCPTokenRepository
	// Updates and final admission share this lock so a completed scope edit
	// cannot race a new permit issued using the previous permissions.
	mu sync.RWMutex
}
type Input struct {
	Name      string   `json:"name"`
	Enabled   bool     `json:"enabled"`
	ExpiresAt int64    `json:"expiresAt"`
	NodeScope string   `json:"nodeScope"`
	NodeIDs   []string `json:"nodeIDs"`
}

func digest(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func (m *Manager) Verify(ctx context.Context, raw string, _ *http.Request) (*auth.TokenInfo, error) {
	if len(raw) < 32 || len(raw) > 4096 || strings.ContainsAny(raw, " \t\r\n") {
		return nil, auth.ErrInvalidToken
	}
	record, err := m.Store.MCPTokenByDigest(ctx, digest(raw))
	if errors.Is(err, storage.ErrNotFound) {
		return nil, auth.ErrInvalidToken
	}
	if err != nil {
		return nil, err
	}
	t := record.Token
	if !t.Enabled || t.RevokedAt != 0 || t.ExpiresAt != 0 && t.ExpiresAt <= time.Now().Unix() {
		return nil, auth.ErrInvalidToken
	}
	info := &auth.TokenInfo{UserID: t.ClientID, Extra: map[string]any{"tokenID": t.ID}}
	if t.ExpiresAt != 0 {
		info.Expiration = time.Unix(t.ExpiresAt, 0)
	}
	return info, nil
}

func (m *Manager) List(ctx context.Context) ([]storage.MCPToken, error) {
	records, err := m.Store.MCPTokens(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]storage.MCPToken, 0, len(records))
	for _, record := range records {
		result = append(result, record.Token)
	}
	return result, nil
}

func validate(in Input) error {
	if naming.Validate(in.Name) != nil || in.ExpiresAt < 0 || in.ExpiresAt > storage.MaxMCPTokenTime || in.ExpiresAt != 0 && in.ExpiresAt <= time.Now().Unix() {
		return ErrInvalid
	}
	return nil
}

func (m *Manager) Create(ctx context.Context, in Input) (storage.MCPToken, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := validate(in); err != nil {
		return storage.MCPToken{}, "", err
	}
	raw := "xmcp_" + rand.Text() + rand.Text()
	t := storage.MCPToken{ID: rand.Text(), ClientID: rand.Text(), Name: in.Name, Prefix: raw[:12], Version: 1, Enabled: in.Enabled, CreatedAt: time.Now().Unix(), ExpiresAt: in.ExpiresAt, NodeScope: in.NodeScope, NodeIDs: in.NodeIDs}
	if err := t.NormalizeNodeScope(); err != nil {
		return storage.MCPToken{}, "", errors.Join(ErrInvalid, err)
	}
	err := m.Store.SaveMCPToken(ctx, 0, storage.MCPTokenRecord{Token: t, Digest: digest(raw)}, event("create", t.ID))
	if err != nil {
		return storage.MCPToken{}, "", err
	}
	return t, raw, nil
}

// Seed registers an optional deployment-provided initial token. A disabled or
// revoked record is retained and never resurrected on restart.
// Its initial client ID preserves the static-token runtime's SHA-256 scope;
// retained journal records and their authorization bindings remain valid.
func (m *Manager) Seed(ctx context.Context, raw string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if raw == "" {
		return nil
	}
	if len(raw) < 32 || len(raw) > 4096 || strings.ContainsAny(raw, " \t\r\n") {
		return ErrInvalid
	}
	hash := digest(raw)
	if _, err := m.Store.MCPTokenByDigest(ctx, hash); !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	t := storage.MCPToken{ID: rand.Text(), ClientID: hash, Name: "initial-token", Prefix: raw[:8], Version: 1, Enabled: true, CreatedAt: time.Now().Unix()}
	return m.Store.SaveMCPToken(ctx, 0, storage.MCPTokenRecord{Token: t, Digest: hash}, event("initialize", t.ID))
}

func (m *Manager) Update(ctx context.Context, id string, version uint64, in Input, revoke bool) (storage.MCPToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	records, err := m.Store.MCPTokens(ctx)
	if err != nil {
		return storage.MCPToken{}, err
	}
	for _, record := range records {
		t := &record.Token
		if t.ID != id {
			continue
		}
		if t.Version != version || t.RevokedAt != 0 {
			return storage.MCPToken{}, storage.ErrConflict
		}
		action := "update"
		if revoke {
			t.Enabled = false
			t.RevokedAt = time.Now().Unix()
			action = "revoke"
		} else {
			if naming.Validate(in.Name) != nil || in.ExpiresAt < 0 || in.ExpiresAt > storage.MaxMCPTokenTime || in.ExpiresAt != t.ExpiresAt && in.ExpiresAt != 0 && in.ExpiresAt <= time.Now().Unix() {
				return storage.MCPToken{}, ErrInvalid
			}
			t.Name, t.Enabled, t.ExpiresAt = in.Name, in.Enabled, in.ExpiresAt
			// Older clients may edit metadata without knowing node scopes. Never
			// let an omitted scope silently broaden an existing restriction.
			if in.NodeScope != "" {
				t.NodeScope, t.NodeIDs = in.NodeScope, in.NodeIDs
			} else if in.NodeIDs != nil {
				return storage.MCPToken{}, ErrInvalid
			}
			if err := t.NormalizeNodeScope(); err != nil {
				return storage.MCPToken{}, errors.Join(ErrInvalid, err)
			}
		}
		t.Version++
		if err := m.Store.SaveMCPToken(ctx, version, record, event(action, id)); err != nil {
			return storage.MCPToken{}, err
		}
		return *t, nil
	}
	return storage.MCPToken{}, storage.ErrNotFound
}

func event(action, id string) ports.AuditEvent {
	return ports.AuditEvent{Timestamp: time.Now().UTC(), OperationID: rand.Text(), Tool: "web.mcp_tokens." + action, Outcome: "executed", Binding: id}
}
