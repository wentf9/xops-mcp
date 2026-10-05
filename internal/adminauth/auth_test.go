package adminauth

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/wentf9/xops-mcp/internal/storage"
	"github.com/wentf9/xops-mcp/internal/testutil"
	"golang.org/x/crypto/bcrypt"
)

func TestStatelessJWTAndPasswordRotation(t *testing.T) {
	store, _, _ := testutil.Store(t)
	key := bytes.Repeat([]byte{5}, 32)
	tokens, err := NewTokens(key, "fixture-domain", "/ops")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := New(store, bcrypt.MinCost, tokens)
	if err != nil {
		t.Fatal(err)
	}
	const password = "synthetic-admin-password"
	if err := manager.Initialize(t.Context(), "admin", password); err != nil {
		t.Fatal(err)
	}
	if err := manager.Initialize(t.Context(), "other", password); !errors.Is(err, storage.ErrAdminExists) {
		t.Fatal(err)
	}
	for _, input := range [][2]string{{"other", password}, {"admin", "wrong"}} {
		if _, err := manager.Login(t.Context(), input[0], input[1]); !errors.Is(err, ErrUnauthorized) {
			t.Fatal("wrong credentials accepted")
		}
	}
	first, err := manager.Login(t.Context(), "admin", password)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Login(t.Context(), "admin", password)
	if err != nil {
		t.Fatal(err)
	}
	if first.Token == second.Token || len(strings.Split(first.Token, ".")) != 3 {
		t.Fatal("login did not issue independent JWTs")
	}
	identity, err := manager.Authenticate(t.Context(), first.Token)
	if err != nil {
		t.Fatal(err)
	}
	// A separate instance can validate without any administrator/session store.
	other, err := NewTokens(key, "fixture-domain", "/ops")
	if err != nil {
		t.Fatal(err)
	}
	offline, err := New(nil, bcrypt.MinCost, other)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := offline.Authenticate(t.Context(), first.Token); err != nil || got != identity {
		t.Fatal("JWT required server-side authentication state", err)
	}
	if err := manager.ChangePassword(t.Context(), identity, password, "rotated-admin-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Login(t.Context(), "admin", password); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("old password accepted")
	}
	if _, err := manager.Authenticate(t.Context(), first.Token); err != nil {
		t.Fatal("stateless token invalidated before expiry")
	}
	if err := manager.ChangePassword(t.Context(), identity, "rotated-admin-password", "another-admin-password"); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("stale identity changed password")
	}
	third, err := manager.Login(t.Context(), "admin", "rotated-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	tokens.now = func() time.Time { return third.ExpiresAt }
	if _, err := manager.Authenticate(t.Context(), third.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("expired JWT accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := manager.Authenticate(ctx, first.Token); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled validation succeeded")
	}
}

func TestJWTValidationAndKeyIsolation(t *testing.T) {
	key := bytes.Repeat([]byte{6}, 32)
	tokens, err := NewTokens(key, "domain", "/ops")
	if err != nil {
		t.Fatal(err)
	}
	clear(key) // The codec must own its key; callers clear deployment buffers.
	login, err := tokens.Issue(t.Context(), storage.Admin{Username: "admin", Version: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tokens.Authenticate(t.Context(), login.Token); err != nil {
		t.Fatal(err)
	}
	for _, other := range []struct {
		key          byte
		domain, path string
	}{{7, "domain", "/ops"}, {6, "other", "/ops"}, {6, "domain", "/other"}} {
		codec, err := NewTokens(bytes.Repeat([]byte{other.key}, 32), other.domain, other.path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := codec.Authenticate(t.Context(), login.Token); !errors.Is(err, ErrUnauthorized) {
			t.Fatal("wrong key/deployment/audience accepted")
		}
	}
	now := time.Now().UTC().Truncate(time.Second)
	valid := tokenClaims{Claims: jwt.Claims{Issuer: tokens.issuer, Subject: "admin", Audience: jwt.Audience{tokens.audience}, IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now), Expiry: jwt.NewNumericDate(now.Add(TokenLifetime)), ID: "fixture-jti"}, AdminVersion: "v1"}
	for _, mutate := range []func(*tokenClaims){
		func(c *tokenClaims) { c.Expiry = nil }, func(c *tokenClaims) { c.IssuedAt = nil }, func(c *tokenClaims) { c.NotBefore = nil }, func(c *tokenClaims) { c.Subject = "" }, func(c *tokenClaims) { c.AdminVersion = "" }, func(c *tokenClaims) { c.ID = "" },
		func(c *tokenClaims) { c.Audience = jwt.Audience{"mcp"} }, func(c *tokenClaims) { c.Issuer = "other" }, func(c *tokenClaims) { c.Expiry = jwt.NewNumericDate(now.Add(time.Hour)) },
		func(c *tokenClaims) {
			c.IssuedAt = jwt.NewNumericDate(now.Add(time.Hour))
			c.NotBefore = c.IssuedAt
			c.Expiry = jwt.NewNumericDate(now.Add(time.Hour + TokenLifetime))
		},
	} {
		claims := valid
		mutate(&claims)
		raw, err := jwt.Signed(tokens.signer).Claims(claims).Serialize()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tokens.Authenticate(t.Context(), raw); !errors.Is(err, ErrUnauthorized) {
			t.Fatal("invalid claims accepted")
		}
	}
	wrongAlgorithm, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.HS384, Key: bytes.Repeat([]byte{6}, 48)}, (&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := jwt.Signed(wrongAlgorithm).Claims(valid).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{raw, "eyJhbGciOiJub25lIn0.e30.", strings.Repeat("a", 64), login.Token + "x"} {
		if _, err := tokens.Authenticate(t.Context(), raw); !errors.Is(err, ErrUnauthorized) {
			t.Fatal("invalid JWT/algorithm accepted")
		}
	}
}
