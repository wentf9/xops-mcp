//go:build webfixture

// This browser-test fixture creates a temporary deployment and isolated SSH
// peer. It never reads personal configuration or contacts existing hosts.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/wentf9/xops-cli/core/testutil/sshfixture"
	"github.com/wentf9/xops-mcp/internal/config"
	"github.com/wentf9/xops-mcp/internal/secure"
	"github.com/wentf9/xops-mcp/internal/server"
	cryptoSSH "golang.org/x/crypto/ssh"
)

func run() (retErr error) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()
	dir, err := os.MkdirTemp("", "xops-web-fixture-")
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, os.RemoveAll(dir)) }()
	peer, err := sshfixture.New(ctx)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, peer.Close()) }()
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer func() {
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			retErr = errors.Join(retErr, err)
		}
	}()
	mcpListener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer func() {
		if err := mcpListener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			retErr = errors.Join(retErr, err)
		}
	}()
	cfg := config.Config{DataDir: filepath.Join(dir, "data"), MasterKeyFile: filepath.Join(dir, "master.key"), MCPTokenFile: filepath.Join(dir, "mcp.token"), AdminBootstrapTokenFile: filepath.Join(dir, "admin.setup"), WebEnabled: true, Listen: mcpListener.Addr().String(), PublicURL: "http://" + mcpListener.Addr().String(), WebListen: listener.Addr().String(), WebPublicURL: "http://" + listener.Addr().String(), WebBasePath: "/console", ToolTimeout: 10 * time.Second, ShutdownTimeout: 3 * time.Second}
	setup := secure.ID() + secure.ID()
	token := secure.ID() + secure.ID()
	for path, value := range map[string]string{cfg.MasterKeyFile: secure.ID() + secure.ID(), cfg.MCPTokenFile: token, cfg.AdminBootstrapTokenFile: setup} {
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			return err
		}
	}
	app, err := server.NewApplication(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, app.Close()) }()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Observe metadata as received on the wire: browser interception may
		// omit these headers from Playwright's request object.
		if r.URL.Path == cfg.WebBasePath+"/" {
			for _, field := range []string{"Site", "Mode", "Dest"} {
				w.Header().Set("X-Fixture-Fetch-"+field, r.Header.Get("Sec-Fetch-"+field))
			}
		}
		app.AdminHandler().ServeHTTP(w, r)
	})
	listeners := []net.Listener{listener, mcpListener}
	servers := []*http.Server{
		{Handler: handler, ReadHeaderTimeout: time.Second, IdleTimeout: 10 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }},
		{Handler: app.Handler(), ReadHeaderTimeout: time.Second, IdleTimeout: 10 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }},
	}
	done := make(chan error, len(servers))
	for index, httpServer := range servers {
		// Each listener is stopped below on signal or unexpected completion;
		// every worker is joined before application cleanup.
		go func() {
			err := httpServer.Serve(listeners[index])
			if errors.Is(err, http.ErrServerClosed) {
				err = nil
			}
			done <- err
		}()
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]string{"url": cfg.WebPublicURL + cfg.WebBasePath, "mcpURL": cfg.PublicURL, "setupToken": setup, "username": "admin", "password": "synthetic-web-admin-password", "sshAddress": peer.Address, "sshPassword": sshfixture.Password, "sshHostKey": string(cryptoSSH.MarshalAuthorizedKey(peer.HostKey)), "sshFingerprint": cryptoSSH.FingerprintSHA256(peer.HostKey)}); err != nil {
		retErr = err
		cancel()
	}
	remaining := len(servers)
	select {
	case <-ctx.Done():
	case err := <-done:
		retErr = errors.Join(retErr, err)
		remaining--
	}
	cancel()
	cleanup, finish := context.WithTimeout(context.Background(), 3*time.Second)
	defer finish()
	for _, httpServer := range servers {
		if err := httpServer.Shutdown(cleanup); err != nil {
			retErr = errors.Join(retErr, err, httpServer.Close())
		}
	}
	for range remaining {
		retErr = errors.Join(retErr, <-done)
	}
	return retErr

}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
