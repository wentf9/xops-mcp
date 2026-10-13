package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/wentf9/xops-cli/core/mcp/ports"
	"github.com/wentf9/xops-mcp/internal/adapters/xops"
	"github.com/wentf9/xops-mcp/internal/secure"
	"github.com/wentf9/xops-mcp/internal/service"
	"github.com/wentf9/xops-mcp/internal/storage"
)

type executionProbeInput struct {
	NodeID     string   `json:"nodeID,omitempty"`
	HostID     string   `json:"hostID"`
	IdentityID string   `json:"identityID"`
	JumpIDs    []string `json:"jumpIDs"`
}

// probeExecution previews the current form's connection without saving a node,
// enabling it, publishing an inventory, or accepting arbitrary shell commands.
func (s *Server) probeExecution(w http.ResponseWriter, r *http.Request) {
	revision, ok := expected(w, r)
	if !ok {
		return
	}
	var input executionProbeInput
	if !decode(w, r, &input) {
		return
	}
	release, ok := s.probeSlot(w)
	if !ok {
		return
	}
	defer release()
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	v, err := s.editor.Service.Inventory(ctx)
	if err != nil {
		failure(w, err)
		return
	}
	if v.Revision != revision {
		failure(w, storage.ErrConflict)
		return
	}
	if s.editor.Service.Coordinator.Pending() {
		failure(w, service.ErrPending)
		return
	}
	id := input.NodeID
	node, exists := v.Nodes[id]
	if id != "" && !exists {
		failure(w, storage.ErrNotFound)
		return
	}
	if id == "" {
		id = secure.ID()
		node = storage.Node{ID: id, Name: "probe-" + id}
	}
	node.HostID, node.IdentityID = input.HostID, input.IdentityID
	node.JumpIDs = slices.Clone(input.JumpIDs)
	// An explicit admin inspection may test a disabled or new target. It never
	// enables the stored node. Disabled transit nodes remain unavailable.
	node.Disabled, node.Execution = false, nil
	node.SudoMode, node.PrivilegeCredentialID = "none", ""
	v.Nodes[id] = node
	view, sources, err := xops.Snapshot(v)
	if err != nil {
		failure(w, fmt.Errorf("%w: %w", service.ErrInvalid, err))
		return
	}
	target := view.Targets[id]
	for _, hop := range target.Plan.Hops {
		if v.Nodes[hop.NodeID].Disabled {
			failure(w, ports.ErrNodeDisabled)
			return
		}
	}
	event := ports.AuditEvent{OperationID: secure.ID(), Tool: "admin.execution_probe", NodeID: input.NodeID, Decision: "administrator", Outcome: "intent", RiskLevel: "safe"}
	if err := s.store.Append(ctx, event); err != nil {
		failure(w, err)
		return
	}
	result, probeErr := s.probes.ProbeExecution(ctx, target.Plan, sources)
	event.Outcome = "executed"
	if probeErr != nil {
		event.Outcome = "error"
	}
	cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	if err := s.store.Append(cleanup, event); err != nil {
		failure(w, err)
		return
	}
	if probeErr != nil {
		if errors.Is(probeErr, context.Canceled) || errors.Is(probeErr, context.DeadlineExceeded) {
			failure(w, probeErr)
			return
		}
		problem(w, 502, "execution_probe_failed", "无法探测远端 Shell，请检查凭据、主机公钥、网络及远端诊断命令权限")
		return
	}
	// Never present a recommendation from an inventory that changed during I/O.
	current, err := s.editor.Service.Inventory(ctx)
	if err != nil {
		failure(w, err)
		return
	}
	if current.Revision != revision {
		failure(w, storage.ErrConflict)
		return
	}
	if s.editor.Service.Coordinator.Pending() {
		failure(w, service.ErrPending)
		return
	}
	w.Header().Set("ETag", etag(revision))
	respond(w, 200, result)
}
