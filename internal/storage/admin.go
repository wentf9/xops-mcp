package storage

import (
	"context"
	"errors"

	"github.com/wentf9/xops-cli/core/mcp/ports"
)

var (
	ErrNotFound    = errors.New("record not found")
	ErrAdminExists = errors.New("administrator already initialized")
)

type Admin struct {
	Username     string
	PasswordHash []byte `json:"-"`
	Version      string
}
type AuditQuery struct {
	BeforeID                     int64
	Limit                        int
	NodeID, Outcome, OperationID string
}
type AuditRecord struct {
	ID    int64            `json:"id"`
	Event ports.AuditEvent `json:"event"`
}

type AdminRepository interface {
	Admin(context.Context) (Admin, error)
	InitializeAdmin(context.Context, Admin) error
	ChangeAdminPassword(context.Context, string, []byte, string) error
	Audit(context.Context, AuditQuery) ([]AuditRecord, error)
}
