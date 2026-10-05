package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/wentf9/xops-cli/core/mcp/ports"
	"github.com/wentf9/xops-cli/core/mcp/state"
	"github.com/wentf9/xops-mcp/internal/adapters/xops"
	"github.com/wentf9/xops-mcp/internal/testutil"
)

func TestTargetlessOperationSerializesEmptyNodeList(t *testing.T) {
	s, _, _ := testutil.Store(t)
	v, err := s.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	view, _, err := xops.Snapshot(v)
	if err != nil {
		t.Fatal(err)
	}
	c, err := state.New(view, state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, c)
	tracker := New(c)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	binding, err := ports.Bind(view, "test", "xops_list_nodes", struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	permit, err := tracker.Enter(ctx, ports.Admission{OperationID: "inventory", Phase: ports.Execute, Snapshot: view, Binding: binding})
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, permit)
	entries := tracker.List()
	if len(entries) != 1 {
		t.Fatalf("active entries = %d", len(entries))
	}
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	if entries[0].NodeIDs == nil || !bytes.Contains(data, []byte(`"nodeIDs":[]`)) {
		t.Fatalf("targetless operation must serialize an empty node array: %s", data)
	}
}

func TestTrackedTransferHandoffKeepsOriginalPermitContext(t *testing.T) {
	s, vault, _ := testutil.Store(t)
	v, _ := testutil.Plan(t, s, vault, testutil.Document(t))
	view, _, err := xops.Snapshot(v)
	if err != nil {
		t.Fatal(err)
	}
	c, err := state.New(view, state.Options{MaxActive: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, c)
	tracker := New(c)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	binding, err := ports.Bind(view, "test", "transfer", struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := tracker.Enter(ctx, ports.Admission{OperationID: "handoff", Phase: ports.TransferStart, Snapshot: view, Binding: binding})
	if err != nil {
		t.Fatal(err)
	}
	commit, err := tracker.Enter(ctx, ports.Admission{OperationID: "handoff", Phase: ports.Commit, Snapshot: view, Binding: binding, Previous: stream})
	if err != nil {
		t.Fatal(err)
	}
	testutil.Close(t, stream)
	if entries := tracker.List(); len(entries) != 1 || entries[0].Phase != ports.Commit {
		t.Fatalf("handoff metadata = %+v", entries)
	}
	testutil.Close(t, commit)
	if len(tracker.List()) != 0 {
		t.Fatal("closed permit retained")
	}
}
