package contract_test

import (
	"bytes"
	"errors"
	"slices"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/wentf9/xops-mcp/internal/mcpauth"
	"github.com/wentf9/xops-mcp/internal/storage"
	"github.com/wentf9/xops-mcp/internal/storage/archive"
)

func TestMCPTokenArchiveDirections(t *testing.T) {
	for _, source := range []string{"sqlite", "postgres"} {
		for _, target := range []string{"sqlite", "postgres"} {
			t.Run(source+"-"+target, func(t *testing.T) {
				s, vault := open(t, source)
				m := &mcpauth.Manager{Store: s}
				ctx := t.Context()
				ids := tokenNodes(t, s, vault)
				active, secret, err := m.Create(ctx, mcpauth.Input{Name: "active", Enabled: true, NodeScope: storage.MCPTokenNodeScopeSelected, NodeIDs: ids})
				if err != nil {
					t.Fatal(err)
				}
				revoked, deadSecret, err := m.Create(ctx, mcpauth.Input{Name: "revoked", Enabled: true})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := m.Update(ctx, revoked.ID, revoked.Version, mcpauth.Input{}, true); err != nil {
					t.Fatal(err)
				}
				empty, _, err := m.Create(ctx, mcpauth.Input{Name: "empty", Enabled: true, NodeScope: storage.MCPTokenNodeScopeSelected})
				if err != nil {
					t.Fatal(err)
				}
				v := inventory(t, s)
				delete(v.Nodes, ids[1])
				save(t, s, v)
				b, err := s.Export(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if b.Format != 3 {
					t.Fatalf("token records exported in unsafe archive format %d", b.Format)
				}
				encoded, err := archive.Encode(ctx, b, vault)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.HasPrefix(encoded, []byte("XOPSDB\x03")) {
					t.Fatal("archive header does not reject v2 readers")
				}
				b, err = archive.Decode(ctx, encoded, vault)
				if err != nil {
					t.Fatal(err)
				}
				dest, _ := open(t, target)
				if err := dest.Restore(ctx, b); err != nil {
					t.Fatal(err)
				}
				restored, err := dest.MCPTokenByID(ctx, active.ID)
				if err != nil || restored.Token.NodeScope != storage.MCPTokenNodeScopeSelected || !slices.Equal(restored.Token.NodeIDs, ids) {
					t.Fatal("archive lost active or deleted node bindings", err)
				}
				restored, err = dest.MCPTokenByID(ctx, empty.ID)
				if err != nil || restored.Token.NodeScope != storage.MCPTokenNodeScopeSelected || len(restored.Token.NodeIDs) != 0 {
					t.Fatal("archive broadened empty selected scope", err)
				}
				m.Store = dest
				info, err := m.Verify(ctx, secret, nil)
				if err != nil || info.UserID != active.ClientID || info.Extra["tokenID"] != active.ID {
					t.Fatal("restore changed identity", err)
				}
				if _, err := m.Verify(ctx, deadSecret, nil); !errors.Is(err, auth.ErrInvalidToken) {
					t.Fatal("restore resurrected revoked token")
				}
				if err := dest.Restore(ctx, b); err == nil {
					t.Fatal("restore overwrote token database")
				}
			})
		}
	}
}
