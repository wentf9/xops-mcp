package service_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wentf9/xops-cli/core/mcp/ports"
	"github.com/wentf9/xops-cli/core/mcp/state"
	"github.com/wentf9/xops-cli/core/ssh"
	"github.com/wentf9/xops-mcp/internal/importer"
	"github.com/wentf9/xops-mcp/internal/service"
	"github.com/wentf9/xops-mcp/internal/storage"
	"github.com/wentf9/xops-mcp/internal/testutil"
)

func acquire(t *testing.T, svc *service.Service, id string) ports.Permit {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	view, err := svc.Coordinator.Resolve(ctx, ports.ResolveRequest{Selectors: []string{id}})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := ports.Bind(view, "test", "xops_ssh_run", map[string]string{"command": "hostname"})
	if err != nil {
		t.Fatal(err)
	}
	permit, err := svc.Coordinator.Enter(ctx, ports.Admission{OperationID: "test", Binding: binding, Snapshot: view, Phase: ports.Execute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testutil.Close(t, permit) })
	return permit
}

func TestPublicationBarrierOrdinaryEditAndCredentialRevocation(t *testing.T) {
	s, vault, _ := testutil.Store(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var block atomic.Bool
	svc, err := service.New(t.Context(), s, func(ctx context.Context, _ []ssh.ConnectionPlan) error {
		if block.CompareAndSwap(true, false) {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, svc)
	doc := testutil.Document(t)
	doc.Hosts["other"] = doc.Hosts["peer"]
	doc.Nodes["other"] = importer.Node{Host: "other", Identity: "login"}
	v, report := testutil.Plan(t, s, vault, doc)
	if _, err := svc.Apply(t.Context(), 0, v); err != nil {
		t.Fatal(err)
	}
	old := acquire(t, svc, report.NodeIDs["peer"])
	v, err = s.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	node := v.Nodes[report.NodeIDs["peer"]]
	host := v.Hosts[node.HostID]
	host.Address = "127.0.0.2"
	v.Hosts[host.ID] = host
	block.Store(true)
	done := make(chan error, 1)
	// Apply has a 10-second budget; either release or cancellation terminates
	// the retirement callback, and this test always joins the result.
	go func() { _, err := svc.Apply(t.Context(), 1, v); done <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("publication did not reach retirement")
	}
	if _, err := svc.Coordinator.Resolve(t.Context(), ports.ResolveRequest{Selectors: []string{node.ID}}); !errors.Is(err, state.ErrUpdating) {
		t.Errorf("affected admission was not blocked: %v", err)
	}
	if _, err := svc.Coordinator.Resolve(t.Context(), ports.ResolveRequest{Selectors: []string{report.NodeIDs["other"]}}); err != nil {
		t.Errorf("unrelated node blocked: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if old.Context().Err() != nil || old.Snapshot().Targets[node.ID].Plan.Hops[0].Address != "127.0.0.1" {
		t.Fatal("ordinary edit cancelled or redirected admitted operation")
	}
	before := old.Snapshot()
	if _, err := svc.Coordinator.Enter(old.Context(), ports.Admission{OperationID: "stale", Snapshot: before, Binding: old.Binding(), Phase: ports.Execute}); !errors.Is(err, ports.ErrStaleBinding) {
		t.Fatalf("approval binding survived edit: %v", err)
	}
	current := acquire(t, svc, node.ID)
	doc = importer.Document{Credentials: map[string]importer.Credential{"login": {Kind: "password", Password: "rotated-synthetic-password"}}}
	v, _ = testutil.Plan(t, s, vault, doc)
	if _, err := svc.Apply(t.Context(), 2, v); err != nil {
		t.Fatal(err)
	}
	if current.Context().Err() == nil {
		t.Fatal("credential rotation did not revoke admitted operation")
	}
}

type faultyStore struct {
	storage.Repository
	saves       int
	afterCommit bool
	failLoad    bool
}

func (s *faultyStore) Save(ctx context.Context, revision uint64, v storage.Inventory, sources []storage.Source) error {
	s.saves++
	if !s.afterCommit {
		return errors.New("synthetic pre-commit failure")
	}
	if err := s.Repository.Save(ctx, revision, v, sources); err != nil {
		return err
	}
	s.failLoad = true
	return errors.New("synthetic lost commit acknowledgement")
}
func (s *faultyStore) Load(ctx context.Context) (storage.Inventory, error) {
	if s.failLoad {
		return storage.Inventory{}, errors.New("synthetic unavailable storage")
	}
	return s.Repository.Load(ctx)
}

func TestUncertainCommitAndPublicationFailureReconcileWithoutReplay(t *testing.T) {
	s, vault, _ := testutil.Store(t)
	wrapped := &faultyStore{Repository: s}
	var retireErr error
	svc, err := service.New(t.Context(), wrapped, func(context.Context, []ssh.ConnectionPlan) error { return retireErr })
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, svc)
	v, report := testutil.Plan(t, s, vault, testutil.Document(t))
	if _, err := svc.Apply(t.Context(), 0, v); err == nil || svc.Coordinator.Pending() {
		t.Fatal("known rollback did not release barrier")
	}
	wrapped.afterCommit = true
	if _, err := svc.Apply(t.Context(), 0, v); !errors.Is(err, service.ErrPending) || !svc.Coordinator.Pending() {
		t.Fatalf("uncertain commit did not retain barrier: %v", err)
	}
	if _, err := svc.Apply(t.Context(), 0, v); !errors.Is(err, service.ErrPending) {
		t.Fatalf("pending mutation was replayed: %v", err)
	}
	// A caller may reuse its candidate after an error; the retained publication
	// must not change with that caller-owned map.
	for id, node := range v.Nodes {
		node.Name = "caller-edited"
		v.Nodes[id] = node
	}
	wrapped.failLoad = false
	retireErr = errors.New("synthetic activation failure")
	if err := svc.Reconcile(t.Context()); !errors.Is(err, service.ErrPending) {
		t.Fatal(err)
	}
	if _, err := svc.Coordinator.Resolve(t.Context(), ports.ResolveRequest{Selectors: []string{report.NodeIDs["peer"]}}); !errors.Is(err, state.ErrUpdating) {
		t.Fatalf("publication failure admitted operation: %v", err)
	}
	retireErr = nil
	if err := svc.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if wrapped.saves != 2 || svc.Coordinator.Pending() {
		t.Fatalf("write repeated or barrier retained: saves=%d", wrapped.saves)
	}
	loaded, err := s.Load(t.Context())
	if err != nil || loaded.Revision != 1 {
		t.Fatalf("committed revision lost: %v", err)
	}
	acquire(t, svc, report.NodeIDs["peer"])
}

func TestDeletionTombstoneSurvivesServiceRestart(t *testing.T) {
	s, vault, _ := testutil.Store(t)
	retire := func(ctx context.Context, _ []ssh.ConnectionPlan) error { return ctx.Err() }
	svc, err := service.New(t.Context(), s, retire)
	if err != nil {
		t.Fatal(err)
	}
	v, report := testutil.Plan(t, s, vault, testutil.Document(t))
	if _, err := svc.Apply(t.Context(), 0, v); err != nil {
		t.Fatal(err)
	}
	v, err = s.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	original := v.Nodes[report.NodeIDs["peer"]]
	delete(v.Nodes, original.ID)
	if _, err := svc.Apply(t.Context(), 1, v); err != nil {
		t.Fatal(err)
	}
	testutil.Close(t, svc)
	svc, err = service.New(t.Context(), s, retire)
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, svc)
	v, err = s.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	v.Nodes[original.ID] = original
	if _, err := svc.Apply(t.Context(), 2, v); err == nil {
		t.Fatal("reused deleted ID after restart")
	}
}
