package contract_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/wentf9/xops-mcp/internal/mcpauth"
	"github.com/wentf9/xops-mcp/internal/storage/archive"
)

func TestMCPTokenArchiveDirections(t *testing.T) {
	for _, source := range []string{"sqlite", "postgres"} {
		for _, target := range []string{"sqlite", "postgres"} {
			t.Run(source+"-"+target, func(t *testing.T) {
				s, vault := open(t, source)
				m := &mcpauth.Manager{Store: s}
				ctx := t.Context()
				active, secret, err := m.Create(ctx, mcpauth.Input{Name: "active", Enabled: true})
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
				b, err := s.Export(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if b.Format != 2 {
					t.Fatalf("token records exported in unsafe archive format %d", b.Format)
				}
				encoded, err := archive.Encode(ctx, b, vault)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.HasPrefix(encoded, []byte("XOPSDB\x02")) {
					t.Fatal("archive header does not reject v1 readers")
				}
				b, err = archive.Decode(ctx, encoded, vault)
				if err != nil {
					t.Fatal(err)
				}
				dest, _ := open(t, target)
				if err := dest.Restore(ctx, b); err != nil {
					t.Fatal(err)
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
