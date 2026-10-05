// Package operations exposes active permit metadata, never a second transfer
// state machine. A cancelling permit does not imply rollback of remote work.
package operations

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/wentf9/xops-cli/core/mcp/ports"
)

type Entry struct {
	OperationID string      `json:"operationID"`
	Tool        string      `json:"tool"`
	NodeIDs     []string    `json:"nodeIDs"`
	Phase       ports.Phase `json:"phase"`
	State       string      `json:"state"`
	StartedAt   time.Time   `json:"startedAt"`
	Deadline    time.Time   `json:"deadline"`
}
type Tracker struct {
	gate   ports.ExecutionGate
	mu     sync.Mutex
	active map[*permit]Entry
}
type permit struct {
	ports.Permit
	owner *Tracker
	stop  func() bool
	once  sync.Once
	err   error
}

func New(gate ports.ExecutionGate) *Tracker { return &Tracker{gate: gate, active: map[*permit]Entry{}} }
func (t *Tracker) DomainID() string         { return t.gate.DomainID() }
func (t *Tracker) Enter(ctx context.Context, a ports.Admission) (ports.Permit, error) {
	p, err := t.gate.Enter(ctx, a)
	if err != nil {
		return nil, err
	}
	wrapper := &permit{Permit: p, owner: t}
	entry := Entry{OperationID: a.OperationID, Tool: a.Binding.Tool, NodeIDs: make([]string, 0, len(a.Snapshot.Targets)), Phase: a.Phase, State: "active", StartedAt: time.Now().UTC()}
	entry.Deadline, _ = p.Context().Deadline()
	for id := range a.Snapshot.Targets {
		entry.NodeIDs = append(entry.NodeIDs, id)
	}
	slices.Sort(entry.NodeIDs)
	t.mu.Lock()
	t.active[wrapper] = entry
	t.mu.Unlock()
	// The callback only annotates bounded metadata. Close removes the entry;
	// the underlying gate remains responsible for cancellation and capacity.
	wrapper.stop = context.AfterFunc(p.Context(), func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		if e, exists := t.active[wrapper]; exists {
			e.State = "ending"
			t.active[wrapper] = e
		}
	})
	return wrapper, nil
}
func (p *permit) Close() error {
	p.once.Do(func() {
		p.err = p.Permit.Close()
		p.stop()
		p.owner.mu.Lock()
		delete(p.owner.active, p)
		p.owner.mu.Unlock()
	})
	return p.err
}
func (t *Tracker) List() []Entry {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make([]Entry, 0, len(t.active))
	for _, entry := range t.active {
		entry.NodeIDs = slices.Clone(entry.NodeIDs)
		result = append(result, entry)
	}
	slices.SortFunc(result, func(a, b Entry) int { return b.StartedAt.Compare(a.StartedAt) })
	return result
}
