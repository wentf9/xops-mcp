// Package adminauth owns administrator credentials and stateless JWTs independently
// of MCP service tokens and short-lived file transfer capabilities.
package adminauth

import (
	"context"
	"crypto/subtle"
	"errors"
	"strings"
	"time"

	"github.com/wentf9/xops-mcp/internal/secure"
	"github.com/wentf9/xops-mcp/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

var ErrUnauthorized = errors.New("invalid administrator credentials or token")
var ErrInvalid = errors.New("username must be 1-64 characters and password must be 12-72 bytes")

type Manager struct {
	Store  storage.AdminRepository
	Cost   int
	Tokens *Tokens
	dummy  []byte
}
type Login struct {
	Token     string
	ExpiresAt time.Time
}

func New(store storage.AdminRepository, cost int, tokens *Tokens) (*Manager, error) {
	if cost == 0 {
		cost = bcrypt.DefaultCost
	}
	dummy, err := bcrypt.GenerateFromPassword([]byte(secure.ID()), cost)
	if err != nil {
		return nil, err
	}
	return &Manager{Store: store, Cost: cost, Tokens: tokens, dummy: dummy}, nil
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
	if m.Tokens == nil {
		return Login{}, errors.New("administrator JWT signer is not configured")
	}
	return m.Tokens.Issue(ctx, admin)
}
func (m *Manager) Authenticate(ctx context.Context, token string) (Identity, error) {
	if m.Tokens == nil {
		return Identity{}, ErrUnauthorized
	}
	return m.Tokens.Authenticate(ctx, token)
}
func (m *Manager) ChangePassword(ctx context.Context, identity Identity, current, next string) error {
	if !validPassword(next) {
		return ErrInvalid
	}
	a, err := m.Store.Admin(ctx)
	if err != nil {
		return err
	}
	if a.Version != identity.AdminVersion || a.Username != identity.Username || bcrypt.CompareHashAndPassword(a.PasswordHash, []byte(current)) != nil {
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
