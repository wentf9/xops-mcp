package adminauth

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/wentf9/xops-mcp/internal/testutil"
)

func encryptFixture(t *testing.T, c Challenge, data any, options *jose.EncrypterOptions) string {
	t.Helper()
	enc, err := jose.NewEncrypter(jose.A256GCM, jose.Recipient{Algorithm: jose.RSA_OAEP_256, Key: c.PublicKey.Key, KeyID: c.PublicKey.KeyID}, options)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := json.Marshal(map[string]any{"challenge": c.Challenge, "data": data})
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := enc.Encrypt(plain)
	clear(plain)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := encrypted.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func TestEncryptedPasswordRequestsAcrossInstances(t *testing.T) {
	cfg := testutil.Config(t)
	key, private, err := cfg.AdminKeys()
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := NewTokens(key, "domain", "/ops")
	if err != nil {
		t.Fatal(err)
	}
	first, err := NewRequestCipher(private, tokens)
	if err != nil {
		t.Fatal(err)
	}
	otherTokens, err := NewTokens(key, "domain", "/ops")
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewRequestCipher(private, otherTokens)
	if err != nil {
		t.Fatal(err)
	}
	parameters, err := first.Challenge(t.Context(), "login", "")
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]string{"username": "admin", "password": "加密的登录密码123"}
	raw := encryptFixture(t, parameters, input, nil)
	if bytes.Contains([]byte(raw), []byte(input["password"])) {
		t.Fatal("password appeared in wire payload")
	}
	var decoded struct{ Username, Password string }
	if err := second.Decrypt(t.Context(), raw, "login", "", &decoded); err != nil || decoded.Password != input["password"] {
		t.Fatal("cross-instance request failed", err)
	}
	for _, action := range []string{"setup", "password", "invalid"} {
		if err := second.Decrypt(t.Context(), raw, action, "", &decoded); !errors.Is(err, ErrEncryptedRequest) {
			t.Fatal("cross-purpose replay accepted")
		}
	}
	// The stateless challenge has a bounded replay window, not one-use state.
	if err := second.Decrypt(t.Context(), raw, "login", "", &decoded); err != nil {
		t.Fatal(err)
	}
	otherTokens.now = func() time.Time { return parameters.ExpiresAt }
	if err := second.Decrypt(t.Context(), raw, "login", "", &decoded); !errors.Is(err, ErrEncryptedRequest) {
		t.Fatal("expired request accepted")
	}
	otherTokens.now = time.Now
	if err := second.Decrypt(t.Context(), raw+"a", "login", "", &decoded); !errors.Is(err, ErrEncryptedRequest) {
		t.Fatal("tampered JWE accepted")
	}
	compressed := encryptFixture(t, parameters, input, &jose.EncrypterOptions{Compression: jose.DEFLATE})
	if err := second.Decrypt(t.Context(), compressed, "login", "", &decoded); !errors.Is(err, ErrEncryptedRequest) {
		t.Fatal("compressed request accepted")
	}
	parameters, err = first.Challenge(t.Context(), "password", "jwt-one")
	if err != nil {
		t.Fatal(err)
	}
	raw = encryptFixture(t, parameters, map[string]string{"current": "old", "next": "new"}, nil)
	var update struct{ Current, Next string }
	if err := second.Decrypt(t.Context(), raw, "password", "jwt-two", &update); !errors.Is(err, ErrEncryptedRequest) {
		t.Fatal("password request moved between JWTs")
	}
	if err := second.Decrypt(t.Context(), raw, "password", "jwt-one", &update); err != nil {
		t.Fatal(err)
	}
}
