// Package storage owns the server's persistent business model. Credentials are
// ciphertext-only; these records must not be used as public API DTOs.
package storage

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"slices"

	"github.com/wentf9/xops-cli/core/mcp/policy"
	"github.com/wentf9/xops-cli/core/mcp/ports"
)

var ErrConflict = errors.New("inventory revision conflict")

type Host struct {
	ID, Name, Address, HostKey string
	Port                       int
}

type Identity struct {
	ID, Name, User, CredentialID string
}

type Tag struct {
	ID, Name string
}

type Node struct {
	ID, Name, HostID, IdentityID    string
	JumpIDs                         []string
	SudoMode, PrivilegeCredentialID string
	Aliases, TagIDs                 []string
	Disabled                        bool
}

type Credential struct {
	ID, Name, Kind, Version string
	Ciphertext              []byte `json:"-" yaml:"-"`
}

type Inventory struct {
	DomainID    string
	Revision    uint64
	Policy      policy.Config
	Hosts       map[string]Host
	Identities  map[string]Identity
	Nodes       map[string]Node
	Tags        map[string]Tag
	Credentials map[string]Credential
	Deleted     map[string]bool
}

func (v Inventory) Clone() Inventory {
	v.Policy = policy.Clone(v.Policy)
	v.Hosts = maps.Clone(v.Hosts)
	v.Identities = maps.Clone(v.Identities)
	v.Credentials = maps.Clone(v.Credentials)
	v.Nodes = maps.Clone(v.Nodes)
	v.Tags = maps.Clone(v.Tags)
	v.Deleted = maps.Clone(v.Deleted)
	for id, credential := range v.Credentials {
		credential.Ciphertext = bytes.Clone(credential.Ciphertext)
		v.Credentials[id] = credential
	}
	for id, node := range v.Nodes {
		node.JumpIDs = slices.Clone(node.JumpIDs)
		node.Aliases = slices.Clone(node.Aliases)
		node.TagIDs = slices.Clone(node.TagIDs)
		v.Nodes[id] = node
	}
	return v
}

// Source is an immutable, endpoint-bound reference to credential or trust
// material. Historical rows let already-admitted ordinary edits finish on the
// original endpoint; the core gate remains the only authority to execute.
type Source struct {
	Token, NodeID, Host, User, Purpose string
	Port                               int
	CredentialID, Version, HostKey     string
}

type Repository interface {
	Load(context.Context) (Inventory, error)
	Save(context.Context, uint64, Inventory, []Source) error
	Credential(context.Context, string, string) (Credential, error)
	Source(context.Context, Source) (Source, error)
	Append(context.Context, ports.AuditEvent) error
}

// Database is owned by the deployment host. Backup operations are offline and
// preserve identity/history; they are deliberately separate from API edits.
type Database interface {
	Repository
	AdminRepository
	Export(context.Context) (Backup, error)
	Restore(context.Context, Backup) error
	Close() error
}
