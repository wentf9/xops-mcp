// Package adminauth owns administrator credentials and sessions independently
// of MCP service tokens and short-lived file transfer capabilities.
package adminauth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/wentf9/xops-mcp/internal/secure"
	"github.com/wentf9/xops-mcp/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

var ErrUnauthorized = errors.New("invalid administrator credentials or session")
var ErrInvalid = errors.New("username must be 1-64 characters and password must be 12-72 bytes")

type Manager struct {
	Store storage.AdminRepository
	Cost  int
	now   func() time.Time
	dummy []byte
}
type Login struct {
	Token     string
	CSRF      string
	ExpiresAt time.Time
}

func New(store storage.AdminRepository, cost int) (*Manager, error) {
	if cost == 0 {
		cost = bcrypt.DefaultCost
	}
	dummy, err := bcrypt.GenerateFromPassword([]byte(secure.ID()), cost)
	if err != nil {
		return nil, err
	}
	return &Manager{Store: store, Cost: cost, now: time.Now, dummy: dummy}, nil
}
func validPassword(password string) bool {
	return len(password) >= 12 && len(password) <= 72 && !strings.ContainsAny(password, "\x00\r\n")
}
func (m *Manager) Initialize(ctx context.Context, user, password string) error {
	if user == "" || len(user) > 64 || strings.TrimSpace(user) != user || strings.ContainsAny(user, "\x00\r\n") || !validPassword(password) {
		return ErrInvalid
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), m.Cost)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return m.Store.InitializeAdmin(ctx, storage.Admin{Username: user, PasswordHash: hash, Version: secure.ID()})
}
func (m *Manager) Login(ctx context.Context, user, password string) (Login, error) {
	admin, err := m.Store.Admin(ctx)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return Login{}, err
	}
	hash := admin.PasswordHash
	if err != nil {
		hash = m.dummy
	}
	check := bcrypt.CompareHashAndPassword(hash, []byte(password))
	if check != nil || subtle.ConstantTimeCompare([]byte(user), []byte(admin.Username)) != 1 || err != nil {
		return Login{}, ErrUnauthorized
	}
	if err := ctx.Err(); err != nil {
		return Login{}, err
	}
	login := Login{Token: secure.ID() + secure.ID(), ExpiresAt: m.now().Add(12 * time.Hour).UTC()}
	login.CSRF = CSRF(login.Token)
	if err := m.Store.CreateSession(ctx, storage.Session{Digest: Digest(login.Token), AdminVersion: admin.Version, ExpiresAt: login.ExpiresAt}); err != nil {
		return Login{}, err
	}
	return login, nil
}
func Digest(token string) []byte { sum := sha256.Sum256([]byte(token)); return sum[:] }
func CSRF(token string) string {
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte("xops-admin-csrf-v1"))
	return hex.EncodeToString(mac.Sum(nil))
}
func (m *Manager) Authenticate(ctx context.Context, token string) (storage.Session, error) {
	if len(token) != 64 {
		return storage.Session{}, ErrUnauthorized
	}
	if _, err := hex.DecodeString(token); err != nil {
		return storage.Session{}, ErrUnauthorized
	}
	session, err := m.Store.Session(ctx, Digest(token), m.now())
	if errors.Is(err, storage.ErrNotFound) {
		return session, ErrUnauthorized
	}
	return session, err
}
func (m *Manager) Logout(ctx context.Context, token string) error {
	return m.Store.DeleteSession(ctx, Digest(token))
}
func (m *Manager) ChangePassword(ctx context.Context, session storage.Session, current, next string) error {
	if !validPassword(next) {
		return ErrInvalid
	}
	a, err := m.Store.Admin(ctx)
	if err != nil {
		return err
	}
	if a.Version != session.AdminVersion || bcrypt.CompareHashAndPassword(a.PasswordHash, []byte(current)) != nil {
		return ErrUnauthorized
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(next), m.Cost)
	if err != nil {
		return err
	}
	return m.Store.ChangeAdminPassword(ctx, a.Version, hash, secure.ID())
}

// ResetPassword is exclusively for the offline deployment-owner command.
func (m *Manager) ResetPassword(ctx context.Context, next string) error {
	if !validPassword(next) {
		return ErrInvalid
	}
	a, err := m.Store.Admin(ctx)
	if err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(next), m.Cost)
	if err != nil {
		return err
	}
	return m.Store.ChangeAdminPassword(ctx, a.Version, hash, secure.ID())
}
