package adminauth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/wentf9/xops-mcp/internal/secure"
	"github.com/wentf9/xops-mcp/internal/storage"
)

const TokenLifetime = 15 * time.Minute
const requestLifetime = 90 * time.Second

type Tokens struct {
	key              []byte
	signer           jose.Signer
	issuer, audience string
	now              func() time.Time
}
type Identity struct {
	Username, AdminVersion, ID string
	ExpiresAt                  time.Time
}
type tokenClaims struct {
	jwt.Claims
	AdminVersion string `json:"av,omitempty"`
}

// All instances of a deployment use the same external signing key, domain and
// API scope. Verification reads no database or process-local session state.
func NewTokens(key []byte, domain, basePath string) (*Tokens, error) {
	if len(key) != 32 || domain == "" {
		return nil, errors.New("administrator JWT requires a 256-bit signing key and deployment identity")
	}
	key = bytes.Clone(key)
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.HS256, Key: key}, (&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		return nil, err
	}
	return &Tokens{key: key, signer: signer, issuer: "xops-mcp:" + domain, audience: "xops-mcp/admin" + basePath, now: time.Now}, nil
}
func (t *Tokens) sign(subject, version, audience string, lifetime time.Duration) (string, time.Time, error) {
	now := t.now().UTC().Truncate(time.Second)
	expires := now.Add(lifetime)
	claims := tokenClaims{Claims: jwt.Claims{Issuer: t.issuer, Subject: subject, Audience: jwt.Audience{audience}, IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now), Expiry: jwt.NewNumericDate(expires), ID: secure.ID()}, AdminVersion: version}
	token, err := jwt.Signed(t.signer).Claims(claims).Serialize()
	return token, expires, err
}
func (t *Tokens) verify(raw, audience string, lifetime time.Duration) (tokenClaims, error) {
	var claims tokenClaims
	if len(raw) == 0 || len(raw) > 4096 {
		return claims, ErrUnauthorized
	}
	token, err := jwt.ParseSigned(raw, []jose.SignatureAlgorithm{jose.HS256})
	if err != nil || len(token.Headers) != 1 || token.Headers[0].ExtraHeaders[jose.HeaderType] != "JWT" {
		return claims, ErrUnauthorized
	}
	if err := token.Claims(t.key, &claims); err != nil {
		return claims, ErrUnauthorized
	}
	now := t.now()
	if claims.Subject == "" || claims.ID == "" || claims.Expiry == nil || claims.IssuedAt == nil || claims.NotBefore == nil || len(claims.Audience) != 1 || !now.Before(claims.Expiry.Time()) || claims.Expiry.Time().Sub(claims.IssuedAt.Time()) != lifetime || claims.NotBefore.Time() != claims.IssuedAt.Time() {
		return claims, ErrUnauthorized
	}
	if err := claims.ValidateWithLeeway(jwt.Expected{Issuer: t.issuer, AnyAudience: jwt.Audience{audience}, Time: now}, 0); err != nil {
		return claims, ErrUnauthorized
	}
	return claims, nil
}
func (t *Tokens) Issue(ctx context.Context, a storage.Admin) (Login, error) {
	if err := ctx.Err(); err != nil {
		return Login{}, err
	}
	token, expiry, err := t.sign(a.Username, a.Version, t.audience, TokenLifetime)
	return Login{Token: token, ExpiresAt: expiry}, err
}
func (t *Tokens) Authenticate(ctx context.Context, token string) (Identity, error) {
	if err := ctx.Err(); err != nil {
		return Identity{}, err
	}
	claims, err := t.verify(token, t.audience, TokenLifetime)
	if err != nil || claims.AdminVersion == "" {
		return Identity{}, ErrUnauthorized
	}
	return Identity{Username: claims.Subject, AdminVersion: claims.AdminVersion, ID: claims.ID, ExpiresAt: claims.Expiry.Time()}, nil
}
func Digest(token string) []byte         { sum := sha256.Sum256([]byte(token)); return sum[:] }
func requestSubject(token string) string { return hex.EncodeToString(Digest(token)) }
