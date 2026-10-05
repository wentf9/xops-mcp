package server_test

import (
	"context"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/wentf9/xops-cli/core/mcp/ports"
	"github.com/wentf9/xops-mcp/internal/server"
	"github.com/wentf9/xops-mcp/internal/testutil"
)

func TestPostgresOwnershipLossStopsAdmissionAndListeners(t *testing.T) {
	if os.Getenv("XOPS_TEST_BACKEND") != "postgres" {
		t.Skip("PostgreSQL backend acceptance")
	}
	cfg := testutil.Config(t)
	cfg.Listen = "127.0.0.1:0"
	cfg.PublicURL = "http://localhost"
	dsn, err := os.ReadFile(cfg.PostgresDSNFile)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(dsn)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	control, err := pgx.Connect(ctx, string(dsn))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := control.Close(cleanup); err != nil {
			t.Error(err)
		}
	}()
	killOwner := func() {
		t.Helper()
		var killed bool
		if err := control.QueryRow(ctx, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=current_database() AND application_name='xops-mcp'").Scan(&killed); err != nil || !killed {
			t.Fatalf("terminate fixture owner: %v", err)
		}
	}
	// The application wrapper must stop existing permits even when embedded by
	// a caller that does not use Run's listener loop.
	app, err := server.NewApplication(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testutil.Close(t, app) })
	v, report := testutil.Plan(t, app.Host.Store, app.Host.Vault, testutil.Document(t))
	if _, err := app.Host.Service.Apply(ctx, 0, v); err != nil {
		t.Fatal(err)
	}
	view, err := app.Host.Service.Coordinator.Resolve(ctx, ports.ResolveRequest{Selectors: []string{report.NodeIDs["peer"]}})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := ports.Bind(view, "owner-loss", "xops_ssh_run", map[string]string{"command": "hostname"})
	if err != nil {
		t.Fatal(err)
	}
	permit, err := app.Host.Service.Coordinator.Enter(ctx, ports.Admission{OperationID: "owner-loss", Snapshot: view, Binding: binding, Phase: ports.Execute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testutil.Close(t, permit) })
	killOwner()
	select {
	case <-permit.Context().Done():
	case <-ctx.Done():
		t.Fatal("ownership loss retained admitted work")
	}
	if _, err := app.Host.Service.Coordinator.Resolve(ctx, ports.ResolveRequest{Selectors: []string{"peer"}}); err == nil {
		t.Fatal("lost owner admitted work")
	}
	testutil.Close(t, permit)
	testutil.Close(t, app)
	logs := listenerLog{lines: make(chan string, 1)}
	done := make(chan error, 1)
	// Run is bounded by ctx and is always joined before control/database cleanup.
	go func() { done <- server.Run(ctx, cfg, logs) }()
	joined := false
	defer func() {
		cancel()
		if !joined {
			<-done
		}
	}()
	var address string
	select {
	case line := <-logs.lines:
		address = strings.Fields(line)[4]
	case err := <-done:
		joined = true
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal("server startup timeout")
	}
	killOwner()
	select {
	case err := <-done:
		joined = true
		if err == nil || !strings.Contains(err.Error(), "ownership") {
			t.Fatalf("lost owner exit = %v", err)
		}
	case <-ctx.Done():
		t.Fatal("ownership loss did not stop listener")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal("listener retained after ownership loss", err)
	}
	testutil.Close(t, listener)
	reopened, err := server.OpenExistingHost(t.Context(), cfg)
	if err != nil {
		t.Fatal("failed server retained ownership", err)
	}
	testutil.Close(t, reopened)
}
