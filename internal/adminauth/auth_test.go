package adminauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wentf9/xops-mcp/internal/storage"
	"github.com/wentf9/xops-mcp/internal/testutil"
	"golang.org/x/crypto/bcrypt"
)

func TestAdministratorSessionsAndPasswordRotation(t *testing.T) {
	store, _, _ := testutil.Store(t)
	manager, err := New(store, bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	const password = "synthetic-admin-password"
	if err := manager.Initialize(t.Context(), "admin", password); err != nil {
		t.Fatal(err)
	}
	if err := manager.Initialize(t.Context(), "other", password); !errors.Is(err, storage.ErrAdminExists) {
		t.Fatalf("duplicate admin: %v", err)
	}
	if _, err := manager.Login(t.Context(), "other", password); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("wrong username logged in")
	}
	if _, err := manager.Login(t.Context(), "admin", "wrong"); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("wrong password logged in")
	}
	first, err := manager.Login(t.Context(), "admin", password)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Login(t.Context(), "admin", password)
	if err != nil {
		t.Fatal(err)
	}
	if first.Token == second.Token || first.CSRF == second.CSRF || first.Token == first.CSRF {
		t.Fatal("session/CSRF tokens are not independent")
	}
	session, err := manager.Authenticate(t.Context(), first.Token)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ChangePassword(t.Context(), session, password, "rotated-admin-password"); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{first.Token, second.Token} {
		if _, err := manager.Authenticate(t.Context(), token); !errors.Is(err, ErrUnauthorized) {
			t.Fatal("password change retained old session")
		}
	}
	third, err := manager.Login(t.Context(), "admin", "rotated-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	manager.now = func() time.Time { return third.ExpiresAt.Add(time.Second) }
	if _, err := manager.Authenticate(t.Context(), third.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("expired session accepted")
	}
	manager.now = time.Now
	if err := manager.Logout(t.Context(), third.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Authenticate(t.Context(), third.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("logout did not revoke session")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := manager.Login(ctx, "admin", "rotated-admin-password"); err == nil {
		t.Fatal("cancelled login succeeded")
	}
}
