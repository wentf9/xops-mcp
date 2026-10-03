package command_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wentf9/xops-mcp/internal/command"
	"github.com/wentf9/xops-mcp/internal/config"
	"github.com/wentf9/xops-mcp/internal/testutil"
	"go.uber.org/goleak"
	"gopkg.in/yaml.v3"
)

type observedInput struct {
	*io.PipeReader
	started chan struct{}
	exited  chan struct{}
	once    sync.Once
}

func (r *observedInput) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	n, err := r.PipeReader.Read(p)
	close(r.exited)
	return n, err
}

func TestImportStdinCancellationReleasesDeployment(t *testing.T) {
	_, path := configuration(t)
	if _, err := run(t, "migrate", "--config", path); err != nil {
		t.Fatal(err)
	}
	reader, writer := io.Pipe()
	defer testutil.Close(t, writer)
	input := &observedInput{PipeReader: reader, started: make(chan struct{}), exited: make(chan struct{})}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- command.Run(ctx, []string{"import", "--config", path, "--file", "-"}, input, io.Discard, io.Discard)
	}()
	select {
	case <-input.started:
	case <-time.After(2 * time.Second):
		t.Fatal("stdin read did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("cancel = %v", err)
		}
	case <-time.After(time.Second):
		t.Error("stdin import ignored cancellation")
		testutil.Close(t, reader)
		<-done
	}
	select {
	case <-input.exited:
	default:
		t.Error("stdin reader outlived command")
	}
	if _, err := run(t, "status", "--config", path); err != nil {
		t.Errorf("cancelled import retained database lock: %v", err)
	}
}

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func configuration(t *testing.T) (config.Config, string) {
	t.Helper()
	cfg := testutil.Config(t)
	cfg.Listen = "127.0.0.1:0"
	cfg.PublicURL = "http://localhost"
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(cfg.DataDir), "server.yaml")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return cfg, path
}
func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out, diagnostics bytes.Buffer
	err := command.Run(t.Context(), args, strings.NewReader(""), &out, &diagnostics)
	return out.String(), err
}

func TestDeploymentCommandsPreviewApplyAndRecovery(t *testing.T) {
	cfg, path := configuration(t)
	if _, err := run(t, "status", "--config", path); err == nil {
		t.Fatal("status created a database")
	}
	if _, err := run(t, "migrate", "--config", path); err != nil {
		t.Fatal(err)
	}
	doc := testutil.Document(t)
	data, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	inventory := filepath.Join(filepath.Dir(cfg.DataDir), "inventory.yaml")
	if err := os.WriteFile(inventory, data, 0600); err != nil {
		t.Fatal(err)
	}
	preview, err := run(t, "import", "--config", path, "--file", inventory, "--include-secrets")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(preview, doc.Credentials["login"].Password) {
		t.Fatal("preview exposed password")
	}
	status, err := run(t, "status", "--config", path)
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Revision uint64 `json:"revision"`
		Nodes    int    `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(status), &state); err != nil {
		t.Fatal(err)
	}
	if state.Revision != 0 || state.Nodes != 0 {
		t.Fatal("preview changed inventory")
	}
	if _, err := run(t, "import", "--config", path, "--file", inventory, "--include-secrets", "--apply", "--expected-revision", "0"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "import", "--config", path, "--file", inventory, "--include-secrets", "--apply", "--expected-revision", "0"); err == nil {
		t.Fatal("stale revision applied")
	}
	output, err := run(t, "recover", "--config", path)
	if err != nil || strings.TrimSpace(output) != "[]" {
		t.Fatalf("empty recovery: %s %v", output, err)
	}
	keyfile := filepath.Join(filepath.Dir(cfg.DataDir), "new.key")
	if _, err := run(t, "keygen", "--out", keyfile); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "keygen", "--out", keyfile); err == nil {
		t.Fatal("keygen overwrote secret")
	}
	info, err := os.Stat(keyfile)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("keygen file permissions")
	}
}

type readyWriter struct{ ready chan string }

func (w readyWriter) Write(data []byte) (int, error) { w.ready <- string(data); return len(data), nil }

func TestServeCancellationReleasesListenerJournalAndDatabase(t *testing.T) {
	_, path := configuration(t)
	for range 2 {
		ctx, cancel := context.WithCancel(t.Context())
		ready := make(chan string, 1)
		done := make(chan error, 1)
		// Run exits on cancellation and is joined below before the next open.
		go func() {
			done <- command.Run(ctx, []string{"serve", "--config", path}, strings.NewReader(""), io.Discard, readyWriter{ready})
		}()
		var address string
		select {
		case line := <-ready:
			if _, err := fmt.Sscanf(line, "MCP HTTP listening on %s", &address); err != nil {
				cancel()
				t.Fatal(err)
			}
		case err := <-done:
			cancel()
			t.Fatal(err)
		case <-time.After(5 * time.Second):
			cancel()
			t.Fatal("server did not start")
		}
		requestCtx, stop := context.WithTimeout(t.Context(), 3*time.Second)
		req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, "http://"+address+"/mcp", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = "localhost"
		transport := &http.Transport{DisableKeepAlives: true}
		response, err := (&http.Client{Transport: transport, Timeout: 3 * time.Second}).Do(req)
		if err != nil {
			t.Error(err)
		} else {
			testutil.Close(t, response.Body)
			if response.StatusCode != http.StatusUnauthorized {
				t.Errorf("unauthorized HTTP=%d", response.StatusCode)
			}
		}
		stop()
		transport.CloseIdleConnections()
		if _, err := run(t, "status", "--config", path); err == nil {
			t.Error("offline writer entered running deployment")
		}
		cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("server did not shut down")
		}
		if _, err := run(t, "status", "--config", path); err != nil {
			t.Fatalf("shutdown retained database lock: %v", err)
		}
	}
}

func TestFailedRuntimeConstructionReleasesDeployment(t *testing.T) {
	cfg, path := configuration(t)
	if err := os.WriteFile(cfg.MCPTokenFile, []byte("short"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "serve", "--config", path); err == nil {
		t.Fatal("invalid token accepted")
	}
	if _, err := run(t, "status", "--config", path); err != nil {
		t.Fatalf("failed startup retained resources: %v", err)
	}
}
