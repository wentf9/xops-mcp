package contract_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/wentf9/xops-mcp/internal/storage/archive"
)

func TestArchiveAcceptsOnlyCurrentFormat(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s, vault := open(t, backend)
			b, err := s.Export(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if b.Format != 2 {
				t.Fatalf("empty database exported format %d, want 2", b.Format)
			}
			encoded, err := archive.Encode(t.Context(), b, vault)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.HasPrefix(encoded, []byte("XOPSDB\x02")) {
				t.Fatal("export must use the v2 envelope")
			}
			decoded, err := archive.Decode(t.Context(), encoded, vault)
			if err != nil || decoded.Format != 2 {
				t.Fatalf("current archive round trip: format=%d err=%v", decoded.Format, err)
			}
			target, _ := open(t, backend)
			before, err := target.Export(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for _, version := range []int{0, 1, 3} {
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
					// unsupported payload version, including a v1 body in v2.
					for _, headerVersion := range []int{version, 2} {
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
