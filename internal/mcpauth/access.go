package mcpauth

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/wentf9/xops-cli/core/mcp/ports"
	mcpruntime "github.com/wentf9/xops-cli/core/mcp/runtime"
	"github.com/wentf9/xops-mcp/internal/storage"
)

// ErrNodeAccess does not disclose whether a denied selector exists.
var ErrNodeAccess = errors.New("MCP token does not allow access to this node")

// Access applies the product's token permissions at the shared runtime's
// inventory and admission ports. The underlying coordinator still owns
// snapshots, permit lifetimes, transfer handoffs and connection generations.
type Access struct {
	Manager *Manager
	State   ports.StateSource
	Gate    ports.ExecutionGate
}

func (a *Access) DomainID() string { return a.State.DomainID() }

func active(t storage.MCPToken) bool {
	return t.Enabled && t.RevokedAt == 0 && (t.ExpiresAt == 0 || t.ExpiresAt > time.Now().Unix())
}

func allows(t storage.MCPToken, id string) bool {
	if t.NodeScope == storage.MCPTokenNodeScopeAll {
		return true
	}
	// Storage normalizes ID order on every read and write.
	_, found := slices.BinarySearch(t.NodeIDs, id)
	return t.NodeScope == storage.MCPTokenNodeScopeSelected && found
}

func (a *Access) token(ctx context.Context, scope string, transfer bool) (storage.MCPToken, error) {
	identity, ok := mcpruntime.ClientIdentityFromContext(ctx)
	var record storage.MCPTokenRecord
	var err error
	if ok {
		if identity.TokenID == "" || identity.ClientID == "" || scope != "" && scope != identity.ClientID {
			return storage.MCPToken{}, ErrNodeAccess
		}
		record, err = a.Manager.Store.MCPTokenByID(ctx, identity.TokenID)
		if err == nil && record.Token.ClientID != identity.ClientID {
			return storage.MCPToken{}, ErrNodeAccess
		}
	} else if transfer && scope != "" {
		// The shared runtime validates the short-lived transfer credential and
		// supplies its persisted binding, never a client-provided scope.
		record, err = a.Manager.Store.MCPTokenByClientID(ctx, scope)
	} else {
		return storage.MCPToken{}, ErrNodeAccess
	}
	if errors.Is(err, storage.ErrNotFound) {
		return storage.MCPToken{}, ErrNodeAccess
	}
	if err != nil {
		return storage.MCPToken{}, err
	}
	if !active(record.Token) {
		return storage.MCPToken{}, ErrNodeAccess
	}
	return record.Token, nil
}

func (a *Access) List(ctx context.Context, query ports.NodeQuery) (ports.InventorySnapshot, error) {
	a.Manager.mu.RLock()
	defer a.Manager.mu.RUnlock()
	token, err := a.token(ctx, "", false)
	if err != nil {
		return ports.InventorySnapshot{}, err
	}
	view, err := a.State.List(ctx, query)
	if err != nil {
		return ports.InventorySnapshot{}, err
	}
	view = view.Clone()
	nodes := make([]ports.NodeInfo, 0, len(view.Nodes))
	for _, node := range view.Nodes {
		if !allows(token, node.ID) {
			continue
		}
		// Transit through configured jumps does not grant direct node access.
		// Keep inaccessible jump identifiers out of the filtered inventory.
		for _, jump := range strings.Split(node.ProxyJump, ",") {
			if jump != "" && !allows(token, jump) {
				node.ProxyJump = ""
				break
			}
		}
		nodes = append(nodes, node)
	}
	view.Nodes = nodes
	return view, nil
}

func (a *Access) Resolve(ctx context.Context, request ports.ResolveRequest) (ports.OperationSnapshot, error) {
	a.Manager.mu.RLock()
	defer a.Manager.mu.RUnlock()
	if _, ok := mcpruntime.ClientIdentityFromContext(ctx); !ok && len(request.Selectors) == 0 {
		// Runtime initialization reads only policy. This path cannot issue a
		// permit, reveal nodes or resolve a target without a verified identity.
		view, err := a.State.Resolve(ctx, request)
		if err == nil && (len(view.Targets) != 0 || len(view.Selectors) != 0) {
			return ports.OperationSnapshot{}, ErrNodeAccess
		}
		return view, err
	}
	token, err := a.token(ctx, "", false)
	if err != nil {
		return ports.OperationSnapshot{}, err
	}
	view, err := a.State.Resolve(ctx, request)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) || errors.Is(err, ports.ErrNodeDisabled) {
			return ports.OperationSnapshot{}, ErrNodeAccess
		}
		return ports.OperationSnapshot{}, err
	}
	for id := range view.Targets {
		if !allows(token, id) {
			return ports.OperationSnapshot{}, ErrNodeAccess
		}
	}
	return view, nil
}

func (a *Access) Enter(ctx context.Context, admission ports.Admission) (ports.Permit, error) {
	if err := admission.Binding.Validate(admission.Snapshot); err != nil {
		return nil, err
	}
	// This HTTP runtime requests Recovery only for private temporary-file
	// cleanup; clients cannot choose the phase. Scope edits must not prevent
	// cleanup of work authorized before the edit.
	if admission.Phase == ports.Recovery {
		return a.Gate.Enter(ctx, admission)
	}
	a.Manager.mu.RLock()
	defer a.Manager.mu.RUnlock()
	token, err := a.token(ctx, admission.Binding.Scope, admission.Phase == ports.TransferStart || admission.Phase == ports.Commit)
	if err != nil {
		if errors.Is(err, ErrNodeAccess) {
			return nil, errors.Join(ports.ErrStaleBinding, err)
		}
		return nil, err
	}
	for id := range admission.Snapshot.Targets {
		if !allows(token, id) {
			return nil, errors.Join(ports.ErrStaleBinding, ErrNodeAccess)
		}
	}
	return a.Gate.Enter(ctx, admission)
}
