package adminauth

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

var ErrEncryptedRequest = errors.New("invalid or expired encrypted administrator request")

const MaxEncryptedRequest = 24 << 10

type RequestCipher struct {
	key    *rsa.PrivateKey
	public jose.JSONWebKey
	tokens *Tokens
}
type Challenge struct {
	PublicKey jose.JSONWebKey `json:"publicKey"`
	Challenge string          `json:"challenge"`
	ExpiresAt time.Time       `json:"expiresAt"`
}
type encryptedPayload struct {
	Challenge string          `json:"challenge"`
	Data      json.RawMessage `json:"data"`
}

func NewRequestCipher(key *rsa.PrivateKey, tokens *Tokens) (*RequestCipher, error) {
	if key == nil || key.N.BitLen() < 2048 || key.N.BitLen() > 4096 || tokens == nil {
		return nil, errors.New("administrator request encryption requires a 2048-4096 bit RSA private key and JWT signer")
	}
	if err := key.Validate(); err != nil {
		return nil, errors.New("invalid administrator RSA key")
	}
	public := jose.JSONWebKey{Key: &key.PublicKey, Algorithm: string(jose.RSA_OAEP_256), Use: "enc"}
	thumbprint, err := public.Thumbprint(crypto.SHA256)
	if err != nil {
		return nil, err
	}
	public.KeyID = base64.RawURLEncoding.EncodeToString(thumbprint)
	return &RequestCipher{key: key, public: public, tokens: tokens}, nil
}
func validAction(action string) bool {
	return action == "login" || action == "setup" || action == "password"
}
func (c *RequestCipher) Challenge(ctx context.Context, action, token string) (Challenge, error) {
	if !validAction(action) {
		return Challenge{}, ErrEncryptedRequest
	}
	if err := ctx.Err(); err != nil {
		return Challenge{}, err
	}
	challenge, expires, err := c.tokens.sign(requestSubject(token), "", c.tokens.audience+"/request/"+action, requestLifetime)
	return Challenge{PublicKey: c.public, Challenge: challenge, ExpiresAt: expires}, err
}
func strictJSON(data []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return ErrEncryptedRequest
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return ErrEncryptedRequest
	}
	return nil
}
func (c *RequestCipher) Decrypt(ctx context.Context, raw, action, token string, value any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validAction(action) || len(raw) == 0 || len(raw) > MaxEncryptedRequest {
		return ErrEncryptedRequest
	}
	object, err := jose.ParseEncryptedCompact(raw, []jose.KeyAlgorithm{jose.RSA_OAEP_256}, []jose.ContentEncryption{jose.A256GCM})
	if err != nil {
		return ErrEncryptedRequest
	}
	// Compression is unnecessary for credentials and may expand untrusted input.
	if object.Header.KeyID != c.public.KeyID || object.Header.ExtraHeaders["zip"] != nil {
		return ErrEncryptedRequest
	}
	plaintext, err := object.Decrypt(c.key)
	if err != nil {
		return ErrEncryptedRequest
	}
	defer clear(plaintext)
	var payload encryptedPayload
	if err := strictJSON(plaintext, &payload); err != nil {
		return err
	}
	claims, err := c.tokens.verify(payload.Challenge, c.tokens.audience+"/request/"+action, requestLifetime)
	if err != nil || claims.Subject != requestSubject(token) || len(payload.Data) == 0 {
		return ErrEncryptedRequest
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return strictJSON(payload.Data, value)
}
