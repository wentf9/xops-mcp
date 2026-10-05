package server_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/wentf9/xops-mcp/internal/server"
	"github.com/wentf9/xops-mcp/internal/testutil"
)

type listenerLog struct{ lines chan string }

func (l listenerLog) Write(p []byte) (int, error) { l.lines <- string(p); return len(p), nil }

func TestSeparateListenersRunAndShutdown(t *testing.T) {
	testListenersRunAndShutdown(t, true)
}

func TestDisabledManagementDoesNotStartListener(t *testing.T) {
	testListenersRunAndShutdown(t, false)
}

func testListenersRunAndShutdown(t *testing.T, webEnabled bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	cfg := adminConfig(t)
	cfg.Listen, cfg.WebListen = "127.0.0.1:0", "127.0.0.1:0"
	cfg.PublicURL, cfg.WebPublicURL = "http://localhost", "http://localhost"
	cfg.WebBasePath, cfg.WebEnabled = "/ops", webEnabled
	wantListeners := 2
	if !webEnabled {
		cfg.WebListen, cfg.WebBasePath = "invalid-disabled-address", "/invalid%prefix"
		wantListeners = 1
	}
	logs := listenerLog{lines: make(chan string, 2)}
	done := make(chan error, 1)
	// Run owns and joins the HTTP workers; cancellation below bounds this worker.
	go func() { done <- server.Run(ctx, cfg, logs) }()
	joined := false
	t.Cleanup(func() {
		cancel()
		if !joined {
			select {
			case err := <-done:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(5 * time.Second):
				t.Error("service did not shut down")
			}
		}
	})
	addresses := map[string]string{}
	for range wantListeners {
		select {
		case line := <-logs.lines:
			parts := strings.Fields(line)
			if len(parts) != 5 {
				t.Fatalf("unexpected startup diagnostic: %s", line)
			}
			addresses[parts[0]] = parts[4]
		case err := <-done:
			joined = true
			t.Fatalf("service exited during startup: %v", err)
		case <-ctx.Done():
			t.Fatal("listener startup timeout")
		}
	}
	if addresses["MCP"] == addresses["Web"] {
		t.Fatal("listeners share a socket")
	}
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	for _, check := range []struct {
		listener, path string
		status         int
	}{
		{"Web", "/ops/", 200}, {"Web", "/ops/api/v1/auth/session", 200}, {"Web", "/mcp", 404},
		{"MCP", "/ops/", 404}, {"MCP", "/ops/api/v1/auth/session", 404}, {"MCP", "/mcp", 401},
	} {
		if check.listener == "Web" && !webEnabled {
			continue
		}
		req, err := http.NewRequestWithContext(ctx, "GET", "http://"+addresses[check.listener]+check.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = "localhost"
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.Copy(io.Discard, response.Body)
		testutil.Close(t, response.Body)
		if err != nil || response.StatusCode != check.status {
			t.Fatalf("%s %s: %d, %v", check.listener, check.path, response.StatusCode, err)
		}
	}
	cancel()
	select {
	case err := <-done:
		joined = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dual listener shutdown timed out")
	}
	for _, address := range addresses {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			t.Fatalf("listener leaked after shutdown: %v", err)
		}
		testutil.Close(t, listener)
	}
	host, err := server.OpenHost(t.Context(), cfg)
	if err != nil {
		t.Fatalf("deployment lock leaked: %v", err)
	}
	testutil.Close(t, host)
}

func TestAdminBindFailureReleasesMCPAndDeployment(t *testing.T) {
	cfg := adminConfig(t)
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, occupied)
	available, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Listen = available.Addr().String()
	testutil.Close(t, available)
	cfg.WebListen, cfg.WebEnabled = occupied.Addr().String(), true
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := server.Run(ctx, cfg, io.Discard); err == nil || !strings.Contains(err.Error(), "listen for Web HTTP") {
		t.Fatalf("unexpected bind result: %v", err)
	}
	available, err = net.Listen("tcp", cfg.Listen)
	if err != nil {
		t.Fatalf("MCP listener leaked on administrator bind failure: %v", err)
	}
	testutil.Close(t, available)
	host, err := server.OpenHost(t.Context(), cfg)
	if err != nil {
		t.Fatalf("deployment remained locked: %v", err)
	}
	testutil.Close(t, host)
}
