package mcpauth_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/wentf9/xops-cli/core/mcp/ports"
	"github.com/wentf9/xops-mcp/internal/mcpauth"
	"github.com/wentf9/xops-mcp/internal/storage"
	"github.com/wentf9/xops-mcp/internal/storage/archive"
	"github.com/wentf9/xops-mcp/internal/storage/database"
	"github.com/wentf9/xops-mcp/internal/testutil"
)

func TestLifecyclePersistenceAndBackup(t *testing.T) {
	store, vault, cfg := testutil.Store(t)
	m := &mcpauth.Manager{Store: store}
	ctx := t.Context()
	one, secret, err := m.Create(ctx, mcpauth.Input{Name: "client-one", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	two, secret2, err := m.Create(ctx, mcpauth.Input{Name: "client-two", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if one.ID == two.ID || one.ClientID == two.ClientID || secret == secret2 {
		t.Fatal("client credentials reused")
	}
	for raw, id := range map[string]string{secret: one.ClientID, secret2: two.ClientID} {
		info, err := m.Verify(ctx, raw, nil)
		if err != nil || info.UserID != id {
			t.Fatalf("verify: %v", err)
		}
	}
	if _, err := m.Verify(ctx, strings.Repeat("x", 64), nil); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatal(err)
	}
	list, err := m.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) || strings.Contains(string(data), "Digest") {
		t.Fatal("secret escaped metadata")
	}
	one, err = m.Update(ctx, one.ID, one.Version, mcpauth.Input{Name: "renamed", Enabled: false}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Verify(ctx, secret, nil); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatal("disabled token accepted")
	}
	if _, err := m.Update(ctx, one.ID, 1, mcpauth.Input{Name: "stale", Enabled: true}, false); !errors.Is(err, storage.ErrConflict) {
		t.Fatal("stale token update accepted")
	}
	one, err = m.Update(ctx, one.ID, one.Version, mcpauth.Input{Name: "renamed", Enabled: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	info, err := m.Verify(ctx, secret, nil)
	if err != nil || info.UserID != one.ClientID {
		t.Fatal("edit changed client identity", err)
	}
	if _, err = m.Update(ctx, one.ID, one.Version, mcpauth.Input{}, true); err != nil {
		t.Fatal(err)
	}
	if err = m.Seed(ctx, secret); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Verify(ctx, secret, nil); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatal("seed resurrected revoked token")
	}
	backup, err := store.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := archive.Encode(ctx, backup, vault)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := archive.Decode(ctx, encoded, vault)
	if err != nil || len(decoded.MCPTokens) != 2 {
		t.Fatal("token backup lost", err)
	}
	testutil.Close(t, store)
	reopened, err := database.Open(ctx, cfg, vault, false)
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, reopened)
	m.Store = reopened
	if _, err = m.Verify(ctx, secret, nil); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatal("restarted revoked token accepted")
	}
	if info, err = m.Verify(ctx, secret2, nil); err != nil || info.UserID != two.ClientID {
		t.Fatal("restarted token lost identity", err)
	}
}

func TestExpiryAndValidation(t *testing.T) {
	store, _, _ := testutil.Store(t)
	m := &mcpauth.Manager{Store: store}
	ctx := t.Context()
	for _, in := range []mcpauth.Input{{Name: ""}, {Name: "bad name"}, {Name: "ok", ExpiresAt: -1}, {Name: "ok", ExpiresAt: storage.MaxMCPTokenTime + 1}, {Name: "ok", ExpiresAt: time.Now().Unix() - 1}} {
		if _, _, err := m.Create(ctx, in); !errors.Is(err, mcpauth.ErrInvalid) {
			t.Fatal("invalid input accepted", err)
		}
	}
	token, raw, err := m.Create(ctx, mcpauth.Input{Name: "expiry", Enabled: true, ExpiresAt: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	info, err := m.Verify(ctx, raw, nil)
	if err != nil || info.Expiration.Unix() != token.ExpiresAt {
		t.Fatal(err)
	}
	records, err := store.MCPTokens(ctx)
	if err != nil {
		t.Fatal(err)
	}
	record := records[0]
	record.Token.Version++
	record.Token.ExpiresAt = time.Now().Unix() - 1
	if err := store.SaveMCPToken(ctx, 1, record, ports.AuditEvent{Timestamp: time.Now(), OperationID: "expiry", Tool: "test", Outcome: "executed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Verify(ctx, raw, nil); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatal("expired token accepted")
	}
}
