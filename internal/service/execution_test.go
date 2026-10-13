package service_test

import (
	"context"
	"errors"
	"github.com/wentf9/xops-cli/core/mcp/ports"
	"github.com/wentf9/xops-cli/core/ssh"
	"github.com/wentf9/xops-mcp/internal/service"
	"github.com/wentf9/xops-mcp/internal/testutil"
	"testing"
)

func TestExecutionEditInvalidatesBindingAndPreservesAdmittedSnapshot(t *testing.T) {
	store, vault, _ := testutil.Store(t)
	svc, err := service.New(t.Context(), store, func(ctx context.Context, _ []ssh.ConnectionPlan) error { return ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, svc)
	v, report := testutil.Plan(t, store, vault, testutil.Document(t))
	if _, err := svc.Apply(t.Context(), 0, v); err != nil {
		t.Fatal(err)
	}
	id := report.NodeIDs["peer"]
	permit := acquire(t, svc, id)
	before := permit.Snapshot()
	binding, err := ports.Bind(before, "test", "xops_ssh_run", "hostname")
	if err != nil {
		t.Fatal(err)
	}
	node := v.Nodes[id]
	login := false
	input := service.NodeInput{Name: node.Name, HostID: node.HostID, IdentityID: node.IdentityID, Execution: &ssh.ExecutionConfig{Interpreter: ssh.InterpreterZsh, LaunchDialect: ssh.LaunchPOSIX, Login: &login}}
	editor := &service.Editor{Service: svc, Vault: vault}
	if _, err := editor.SaveNode(t.Context(), 1, id, input); err != nil {
		t.Fatal(err)
	}
	login = true
	after, err := svc.Coordinator.Resolve(t.Context(), ports.ResolveRequest{Selectors: []string{id}})
	if err != nil {
		t.Fatal(err)
	}
	if *after.Targets[id].Execution.Login {
		t.Fatal("execution retained caller-owned pointer")
	}
	if before.Targets[id].ExecutionVersion == after.Targets[id].ExecutionVersion {
		t.Fatal("execution edit kept same dependency version")
	}
	if permit.Snapshot().Targets[id].Execution.Interpreter != ssh.InterpreterServer {
		t.Fatal("admitted target changed")
	}
	if err := permit.Context().Err(); err != nil {
		t.Fatalf("ordinary execution edit canceled admitted work: %v", err)
	}
	stale, err := svc.Coordinator.Enter(t.Context(), ports.Admission{OperationID: "stale", Binding: binding, Snapshot: before, Phase: ports.Execute})
	if stale != nil {
		testutil.Close(t, stale)
	}
	if !errors.Is(err, ports.ErrStaleBinding) {
		t.Fatalf("old binding accepted after execution edit: %v", err)
	}
	input.Execution.Interpreter = "invalid"
	if _, err := editor.SaveNode(t.Context(), 2, id, input); err == nil {
		t.Fatal("invalid execution saved")
	}
}
