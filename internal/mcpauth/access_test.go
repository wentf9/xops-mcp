package mcpauth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wentf9/xops-cli/core/mcp/ports"
	"github.com/wentf9/xops-mcp/internal/mcpauth"
	"github.com/wentf9/xops-mcp/internal/service"
	"github.com/wentf9/xops-mcp/internal/testutil"
)

func TestDetachedTransferAdmissionRechecksTokenScope(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	store, vault, _ := testutil.Store(t)
	svc, err := service.New(ctx, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, svc)
	doc := testutil.Document(t)
	other := doc.Nodes["peer"]
	other.Aliases = nil
	doc.Nodes["other"] = other
	view, _ := testutil.Plan(t, store, vault, doc)
	if _, err := svc.Apply(ctx, view.Revision, view); err != nil {
		t.Fatal(err)
	}
	one, err := svc.Coordinator.Resolve(ctx, ports.ResolveRequest{Selectors: []string{"peer"}})
	if err != nil {
		t.Fatal(err)
	}
	nodeID, _, err := one.Resolve("peer")
	if err != nil {
		t.Fatal(err)
	}
	manager := &mcpauth.Manager{Store: store}
	token, _, err := manager.Create(ctx, mcpauth.Input{Name: "scoped", Enabled: true, NodeScope: "selected", NodeIDs: []string{nodeID}})
	if err != nil {
		t.Fatal(err)
	}
	access := &mcpauth.Access{Manager: manager, State: svc.Coordinator, Gate: svc.Coordinator}
	binding, err := ports.Bind(one, token.ClientID, "xops_prepare_upload", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	admission := ports.Admission{OperationID: "fixture-transfer", Phase: ports.TransferStart, Snapshot: one, Binding: binding}
	stream, err := access.Enter(ctx, admission)
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, stream)

	// Neither an unauthenticated tool invocation nor another client scope can
	// borrow a valid transfer binding to execute arbitrary node operations.
	for _, phase := range []ports.Phase{ports.Inspect, ports.Execute} {
		attempt := admission
		attempt.Phase = phase
		if _, err := access.Enter(ctx, attempt); !errors.Is(err, mcpauth.ErrNodeAccess) {
			t.Fatalf("missing tool identity accepted for %s: %v", phase, err)
		}
	}
	unknown := admission
	unknown.Binding.Scope = "unknown-client"
	if _, err := access.Enter(ctx, unknown); !errors.Is(err, mcpauth.ErrNodeAccess) {
		t.Fatalf("unknown scope accepted: %v", err)
	}
	if _, err := access.List(ctx, ports.NodeQuery{}); !errors.Is(err, mcpauth.ErrNodeAccess) {
		t.Fatalf("anonymous inventory accepted: %v", err)
	}
	if _, err := access.Resolve(ctx, ports.ResolveRequest{Selectors: []string{"peer"}}); !errors.Is(err, mcpauth.ErrNodeAccess) {
		t.Fatalf("anonymous resolution accepted: %v", err)
	}
	policy, err := access.Resolve(ctx, ports.ResolveRequest{})
	if err != nil || len(policy.Targets) != 0 || len(policy.Selectors) != 0 {
		t.Fatalf("runtime policy initialization failed: %+v %v", policy, err)
	}

	// A mixed target snapshot is rejected as a whole, before any permit exists.
	multiple, err := svc.Coordinator.Resolve(ctx, ports.ResolveRequest{Selectors: []string{"peer", "other"}})
	if err != nil {
		t.Fatal(err)
	}
	mixed := admission
	mixed.Snapshot = multiple
	mixed.Binding, err = ports.Bind(multiple, token.ClientID, "xops_prepare_upload", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := access.Enter(ctx, mixed); !errors.Is(err, mcpauth.ErrNodeAccess) {
		t.Fatalf("partially unauthorized targets admitted: %v", err)
	}

	// Removing access between streaming and commit rejects the phase handoff;
	// it leaves the original permit available for the runtime to close safely.
	token, err = manager.Update(ctx, token.ID, token.Version, mcpauth.Input{Name: token.Name, Enabled: true, NodeScope: "selected"}, false)
	if err != nil {
		t.Fatal(err)
	}
	commit := admission
	commit.Phase, commit.Previous = ports.Commit, stream
	if _, err := access.Enter(ctx, commit); !errors.Is(err, ports.ErrStaleBinding) {
		t.Fatalf("commit after scope removal accepted: %v", err)
	}
	if err := stream.Context().Err(); err != nil {
		t.Fatal("denied commit consumed original permit", err)
	}

	// A revoked token cannot start or commit a transfer, while the runtime can
	// still obtain a cleanup-only permit for its originally authorized task.
	if _, err := manager.Update(ctx, token.ID, token.Version, mcpauth.Input{}, true); err != nil {
		t.Fatal(err)
	}
	for _, attempt := range []ports.Admission{admission, commit} {
		if _, err := access.Enter(ctx, attempt); !errors.Is(err, mcpauth.ErrNodeAccess) {
			t.Fatalf("revoked credential admitted %s: %v", attempt.Phase, err)
		}
	}
	cleanup := admission
	cleanup.Phase = ports.Recovery
	permit, err := access.Enter(ctx, cleanup)
	if err != nil {
		t.Fatal("scope removal blocked temporary-file cleanup", err)
	}
	defer testutil.Close(t, permit)
	_, stop, err := ports.WorkContext(ctx, permit, ports.Execute)
	if err == nil {
		stop()
		t.Fatal("cleanup permit allowed command execution")
	}
}
