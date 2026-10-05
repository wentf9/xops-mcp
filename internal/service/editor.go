package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wentf9/xops-cli/core/mcp/guardrail"
	"github.com/wentf9/xops-cli/core/mcp/policy"
	"github.com/wentf9/xops-cli/core/mcp/ports"
	"github.com/wentf9/xops-mcp/internal/adapters/xops"
	"github.com/wentf9/xops-mcp/internal/secure"
	"github.com/wentf9/xops-mcp/internal/storage"
)

var ErrInvalid = errors.New("invalid configuration")
var ErrInUse = errors.New("resource is still referenced")

type Editor struct {
	Service *Service
	Vault   *secure.Vault
}
type HostInput struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Port    int    `json:"port"`
}
type TagInput struct {
	Name string `json:"name"`
}
type IdentityInput struct {
	Name         string `json:"name"`
	User         string `json:"user"`
	CredentialID string `json:"credentialID"`
}
type NodeInput struct {
	Name                  string   `json:"name"`
	HostID                string   `json:"hostID"`
	IdentityID            string   `json:"identityID"`
	JumpIDs               []string `json:"jumpIDs"`
	Aliases               []string `json:"aliases"`
	TagIDs                []string `json:"tagIDs"`
	Disabled              bool     `json:"disabled"`
	SudoMode              string   `json:"sudoMode"`
	PrivilegeCredentialID string   `json:"privilegeCredentialID"`
}

// MaterialInput is write-only. Public credential DTOs contain metadata only.
type MaterialInput struct {
	Password   string `json:"password"`
	PrivateKey string `json:"privateKey"`
	Passphrase string `json:"passphrase"`
}
type CredentialInput struct {
	Name   string         `json:"name"`
	Kind   string         `json:"kind"`
	Secret *MaterialInput `json:"secret,omitempty"`
}
type PolicyInput struct {
	Enabled           bool              `json:"enabled"`
	ApprovalThreshold string            `json:"approvalThreshold"`
	NoElicitFallback  string            `json:"noElicitFallback"`
	BlockedPatterns   []string          `json:"blockedPatterns"`
	ProtectedPaths    []string          `json:"protectedPaths"`
	Nodes             map[string]string `json:"nodes"`
}
type EditResult struct {
	ID       string `json:"id,omitempty"`
	Revision uint64 `json:"revision"`
}

func (s *Service) Inventory(ctx context.Context) (storage.Inventory, error) { return s.store.Load(ctx) }
func (e *Editor) change(ctx context.Context, expected uint64, tool, id string, change func(*storage.Inventory) error) (EditResult, error) {
	v, err := e.Service.Inventory(ctx)
	if err != nil {
		return EditResult{}, err
	}
	if v.Revision != expected {
		return EditResult{Revision: v.Revision}, storage.ErrConflict
	}
	if e.Service.Coordinator.Pending() {
		return EditResult{}, ErrPending
	}
	if err := change(&v); err != nil {
		return EditResult{}, err
	}
	if err := validateNames(v); err != nil {
		return EditResult{}, err
	}
	if _, _, err := xops.Snapshot(v); err != nil {
		return EditResult{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	op := secure.ID()
	event := ports.AuditEvent{OperationID: op, Tool: "admin." + tool, Decision: "administrator", Outcome: "intent", RiskLevel: "moderate"}
	if _, ok := v.Nodes[id]; ok || strings.HasPrefix(tool, "node") {
		event.NodeID = id
	}
	if err := e.Service.store.Append(ctx, event); err != nil {
		return EditResult{}, err
	}
	revision, applyErr := e.Service.Apply(ctx, expected, v)
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	event.Outcome = "executed"
	if applyErr != nil {
		event.Outcome = "error"
	}
	auditErr := e.Service.store.Append(cleanup, event)
	if applyErr == nil && auditErr != nil {
		return EditResult{ID: id, Revision: revision}, &guardrail.ExecutedPostAuditError{OperationID: op, Err: auditErr}
	}
	return EditResult{ID: id, Revision: revision}, errors.Join(applyErr, auditErr)
}
func validateNames(v storage.Inventory) error {
	for _, names := range []map[string]string{hostNames(v), identityNames(v), credentialNames(v)} {
		seen := map[string]bool{}
		for _, name := range names {
			if name == "" || seen[name] {
				return fmt.Errorf("%w: names must be nonempty and unique", ErrInvalid)
			}
			seen[name] = true
		}
	}
	return nil
}
func hostNames(v storage.Inventory) map[string]string {
	m := map[string]string{}
	for id, h := range v.Hosts {
		m[id] = h.Name
	}
	return m
}
func identityNames(v storage.Inventory) map[string]string {
	m := map[string]string{}
	for id, i := range v.Identities {
		m[id] = i.Name
	}
	return m
}
func credentialNames(v storage.Inventory) map[string]string {
	m := map[string]string{}
	for id, c := range v.Credentials {
		m[id] = c.Name
	}
	return m
}
func cleanList(values []string) []string {
	result := []string{}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			result = append(result, v)
		}
	}
	slices.Sort(result)
	return slices.Compact(result)
}

func (e *Editor) SaveHost(ctx context.Context, rev uint64, id string, in HostInput) (EditResult, error) {
	create := id == ""
	if create {
		id = secure.ID()
	}
	return e.change(ctx, rev, "host.save", id, func(v *storage.Inventory) error {
		h, exists := v.Hosts[id]
		if !create && !exists {
			return storage.ErrNotFound
		}
		if in.Port == 0 {
			in.Port = 22
		}
		h.ID, h.Name, h.Address, h.Port = id, in.Name, in.Address, in.Port
		v.Hosts[id] = h
		return nil
	})
}
func (e *Editor) SaveIdentity(ctx context.Context, rev uint64, id string, in IdentityInput) (EditResult, error) {
	create := id == ""
	if create {
		id = secure.ID()
	}
	return e.change(ctx, rev, "identity.save", id, func(v *storage.Inventory) error {
		if _, exists := v.Identities[id]; !create && !exists {
			return storage.ErrNotFound
		}
		v.Identities[id] = storage.Identity{ID: id, Name: in.Name, User: in.User, CredentialID: in.CredentialID}
		return nil
	})
}
func (e *Editor) SaveNode(ctx context.Context, rev uint64, id string, in NodeInput) (EditResult, error) {
	create := id == ""
	if create {
		id = secure.ID()
	}
	return e.change(ctx, rev, "node.save", id, func(v *storage.Inventory) error {
		if _, exists := v.Nodes[id]; !create && !exists {
			return storage.ErrNotFound
		}
		// Deduplicate without rewriting characters. Snapshot applies the same
		// name/alias validation to Web mutations and inventory imports.
		aliases := slices.Clone(in.Aliases)
		slices.Sort(aliases)
		aliases = slices.Compact(aliases)
		v.Nodes[id] = storage.Node{ID: id, Name: in.Name, HostID: in.HostID, IdentityID: in.IdentityID, JumpIDs: slices.Clone(in.JumpIDs), Aliases: aliases, TagIDs: cleanList(in.TagIDs), Disabled: in.Disabled, SudoMode: in.SudoMode, PrivilegeCredentialID: in.PrivilegeCredentialID}
		return nil
	})
}
func (e *Editor) SaveCredential(ctx context.Context, rev uint64, id string, in CredentialInput) (EditResult, error) {
	create := id == ""
	if create {
		id = secure.ID()
	}
	return e.change(ctx, rev, "credential.save", id, func(v *storage.Inventory) error {
		c, exists := v.Credentials[id]
		if !create && !exists {
			return storage.ErrNotFound
		}
		if in.Secret == nil {
			if create || in.Kind != c.Kind {
				return fmt.Errorf("%w: credential material is required", ErrInvalid)
			}
			c.Name = in.Name
			v.Credentials[id] = c
			return nil
		}
		material := secure.Material{Password: []byte(in.Secret.Password), PrivateKey: []byte(in.Secret.PrivateKey), Passphrase: []byte(in.Secret.Passphrase)}
		defer material.Clear()
		if err := secure.ValidateMaterial(in.Kind, material); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		c = storage.Credential{ID: id, Name: in.Name, Kind: in.Kind, Version: secure.ID()}
		cipher, err := e.Vault.Encrypt(v.DomainID, c.ID, c.Kind, c.Version, material)
		if err != nil {
			return err
		}
		c.Ciphertext = cipher
		v.Credentials[id] = c
		return nil
	})
}
func (e *Editor) Delete(ctx context.Context, rev uint64, kind, id string) (EditResult, error) {
	return e.change(ctx, rev, kind+".delete", id, func(v *storage.Inventory) error {
		switch kind {
		case "tags":
			if _, exists := v.Tags[id]; !exists {
				return storage.ErrNotFound
			}
			delete(v.Tags, id)
			for nodeID, node := range v.Nodes {
				node.TagIDs = slices.DeleteFunc(node.TagIDs, func(tagID string) bool { return tagID == id })
				v.Nodes[nodeID] = node
			}
		case "hosts":
			if _, ok := v.Hosts[id]; !ok {
				return storage.ErrNotFound
			}
			for _, n := range v.Nodes {
				if n.HostID == id {
					return ErrInUse
				}
			}
			delete(v.Hosts, id)
		case "identities":
			if _, ok := v.Identities[id]; !ok {
				return storage.ErrNotFound
			}
			for _, n := range v.Nodes {
				if n.IdentityID == id {
					return ErrInUse
				}
			}
			delete(v.Identities, id)
		case "credentials":
			if _, ok := v.Credentials[id]; !ok {
				return storage.ErrNotFound
			}
			for _, i := range v.Identities {
				if i.CredentialID == id {
					return ErrInUse
				}
			}
			for _, n := range v.Nodes {
				if n.PrivilegeCredentialID == id {
					return ErrInUse
				}
			}
			delete(v.Credentials, id)
		case "nodes":
			if _, ok := v.Nodes[id]; !ok {
				return storage.ErrNotFound
			}
			for _, n := range v.Nodes {
				if slices.Contains(n.JumpIDs, id) {
					return ErrInUse
				}
			}
			delete(v.Nodes, id)
			delete(v.Policy.NodeOverrides, id)
		default:
			return ErrInvalid
		}
		return nil
	})
}
func (e *Editor) TrustHost(ctx context.Context, rev uint64, id, key string) (EditResult, error) {
	return e.change(ctx, rev, "host.trust", id, func(v *storage.Inventory) error {
		h, ok := v.Hosts[id]
		if !ok {
			return storage.ErrNotFound
		}
		if key != "" {
			if _, err := xops.ParseHostKey(key); err != nil {
				return ErrInvalid
			}
		}
		h.HostKey = key
		v.Hosts[id] = h
		if key == "" {
			for id, n := range v.Nodes {
				if n.HostID == h.ID {
					n.Disabled = true
					v.Nodes[id] = n
				}
			}
		}
		return nil
	})
}
func (e *Editor) SavePolicy(ctx context.Context, rev uint64, in PolicyInput) (EditResult, error) {
	return e.change(ctx, rev, "policy.save", "", func(v *storage.Inventory) error {
		cfg := policy.Config{Enabled: in.Enabled, ApprovalThreshold: in.ApprovalThreshold, NoElicitFallback: in.NoElicitFallback, BlockedPatterns: slices.Clone(in.BlockedPatterns), ProtectedPaths: slices.Clone(in.ProtectedPaths), NodeOverrides: map[string]policy.NodeConfig{}}
		for id, threshold := range in.Nodes {
			if _, ok := v.Nodes[id]; !ok {
				return fmt.Errorf("%w: policy references missing node", ErrInvalid)
			}
			cfg.NodeOverrides[id] = policy.NodeConfig{ApprovalThreshold: threshold}
		}
		v.Policy = cfg
		return nil
	})
}
func (e *Editor) SaveTag(ctx context.Context, rev uint64, id string, in TagInput) (EditResult, error) {
	create := id == ""
	if create {
		id = secure.ID()
	}
	return e.change(ctx, rev, "tag.save", id, func(v *storage.Inventory) error {
		if _, exists := v.Tags[id]; !create && !exists {
			return storage.ErrNotFound
		}
		v.Tags[id] = storage.Tag{ID: id, Name: in.Name}
		return nil
	})
}
