package contract_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/wentf9/xops-cli/core/mcp/ports"
	"github.com/wentf9/xops-mcp/internal/storage"
	"github.com/wentf9/xops-mcp/internal/storage/archive"
)

func TestArchiveRequiresExplicitTokenScope(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s, vault := open(t, backend)
			record := scopedToken()
			record.Token.NodeScope = storage.MCPTokenNodeScopeSelected
			if err := s.SaveMCPToken(t.Context(), 0, record, ports.AuditEvent{}); err != nil {
				t.Fatal(err)
			}
			b, err := s.Export(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(b)
			if err != nil {
				t.Fatal(err)
			}
			for name, replacement := range map[string]string{"missing": "", "null": `"nodeScope":null,`, "empty": `"nodeScope":"",`} {
				t.Run(name, func(t *testing.T) {
					// Re-seal an otherwise valid v3 payload whose selected-empty
					// restriction lost its mode before reaching the decoder.
					malformed := bytes.Replace(payload, []byte(`"nodeScope":"selected",`), []byte(replacement), 1)
					if bytes.Equal(malformed, payload) {
						t.Fatal("fixture did not remove the selected scope")
					}
					encrypted := append([]byte("XOPSDB\x03"), vault.Seal("xops-mcp database backup v3", malformed)...)
					if _, err := archive.Decode(t.Context(), encrypted, vault); !errors.Is(err, storage.ErrInvalidMCPTokenScope) {
						t.Fatal("archive silently broadened an omitted scope", err)
					}
				})
			}
			b.MCPTokens[0].Token.NodeScope = ""
			if _, err := archive.Encode(t.Context(), b, vault); !errors.Is(err, storage.ErrInvalidMCPTokenScope) {
				t.Fatal("encoded archive with implicit scope", err)
			}
			if _, err := archive.Digest(t.Context(), b, vault); !errors.Is(err, storage.ErrInvalidMCPTokenScope) {
				t.Fatal("hashed archive with implicit scope", err)
			}
			target, _ := open(t, backend)
			if err := target.Restore(t.Context(), b); !errors.Is(err, storage.ErrInvalidMCPTokenScope) {
				t.Fatal("restored archive with implicit scope", err)
			}
			tokens, err := target.MCPTokens(t.Context())
			if err != nil || len(tokens) != 0 {
				t.Fatal("rejected scope changed target database", err)
			}
		})
	}
}

func TestArchiveAcceptsOnlyCurrentFormat(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s, vault := open(t, backend)
			b, err := s.Export(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if b.Format != 3 {
				t.Fatalf("empty database exported format %d, want 3", b.Format)
			}
			encoded, err := archive.Encode(t.Context(), b, vault)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.HasPrefix(encoded, []byte("XOPSDB\x03")) {
				t.Fatal("export must use the v3 envelope")
			}
			decoded, err := archive.Decode(t.Context(), encoded, vault)
			if err != nil || decoded.Format != 3 {
				t.Fatalf("current archive round trip: format=%d err=%v", decoded.Format, err)
			}
			target, _ := open(t, backend)
			before, err := target.Export(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for _, version := range []int{0, 1, 2, 4} {
				t.Run(fmt.Sprint(version), func(t *testing.T) {
					unsupported := b
					unsupported.Format = version
					if _, err := archive.Encode(t.Context(), unsupported, vault); err == nil {
						t.Fatal("encoded unsupported payload format")
					}
					if _, err := archive.Digest(t.Context(), unsupported, vault); err == nil {
						t.Fatal("hashed unsupported payload format")
					}
					if err := target.Restore(t.Context(), unsupported); err == nil {
						t.Fatal("restored unsupported payload format")
					}
					payload, err := json.Marshal(unsupported)
					if err != nil {
						t.Fatal(err)
					}
					// A correctly authenticated envelope cannot legitimize an
					// unsupported payload version, including a v2 body in v3.
					for _, headerVersion := range []int{version, 3} {
						header := append([]byte("XOPSDB"), byte(headerVersion))
						input := append(header, vault.Seal(fmt.Sprintf("xops-mcp database backup v%d", headerVersion), payload)...)
						if _, err := archive.Decode(t.Context(), input, vault); err == nil {
							t.Fatalf("accepted envelope v%d with payload v%d", headerVersion, version)
						}
					}
				})
			}
			changed := bytes.Clone(encoded)
			changed[6] = 1
			if _, err := archive.Decode(t.Context(), changed, vault); err == nil {
				t.Fatal("accepted downgraded envelope header")
			}
			actual, err := target.Export(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			beforeDigest, err := archive.Digest(t.Context(), before, vault)
			if err != nil {
				t.Fatal(err)
			}
			after, err := archive.Digest(t.Context(), actual, vault)
			if err != nil || beforeDigest != after {
				t.Fatal("format rejection changed the target database", err)
			}
		})
	}
}
