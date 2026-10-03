// Package server assembles the persistent host and the shared runtime.
package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/wentf9/xops-cli/core/mcp/ports"
	mcpruntime "github.com/wentf9/xops-cli/core/mcp/runtime"
	"github.com/wentf9/xops-cli/core/mcp/sshexec"
	"github.com/wentf9/xops-cli/core/ssh"
	"github.com/wentf9/xops-mcp/internal/adapters/xops"
	"github.com/wentf9/xops-mcp/internal/config"
	"github.com/wentf9/xops-mcp/internal/secure"
	"github.com/wentf9/xops-mcp/internal/service"
	"github.com/wentf9/xops-mcp/internal/storage/sqlite"
)

type Host struct {
	Store        *sqlite.Store
	Service      *service.Service
	Vault        *secure.Vault
	dependencies ports.Dependencies
	close        func() error
}

func OpenHost(ctx context.Context, cfg config.Config) (*Host, error) {
	return openHost(ctx, cfg, true)
}

func OpenExistingHost(ctx context.Context, cfg config.Config) (*Host, error) {
	return openHost(ctx, cfg, false)
}

func openHost(ctx context.Context, cfg config.Config, allowMigration bool) (_ *Host, retErr error) {
	vault, err := cfg.Vault()
	if err != nil {
		return nil, err
	}
	openStore := sqlite.OpenExisting
	if allowMigration {
		openStore = sqlite.Open
	}
	store, err := openStore(ctx, cfg.DataDir, vault)
	if err != nil {
		return nil, err
	}
	owned := false
	defer func() {
		if !owned {
			retErr = errors.Join(retErr, store.Close())
		}
	}()
	var mu sync.Mutex
	var backend *sshexec.Backend
	svc, err := service.New(ctx, store, func(ctx context.Context, plans []ssh.ConnectionPlan) error {
		mu.Lock()
		current := backend
		mu.Unlock()
		if current == nil {
			return ctx.Err()
		}
		return current.RetirePlans(ctx, plans)
	})
	if err != nil {
		return nil, err
	}
	h := &Host{Store: store, Service: svc, Vault: vault}
	materials := &xops.Materials{Store: store, Vault: vault, DomainID: svc.Coordinator.DomainID()}
	h.dependencies = ports.Dependencies{State: svc.Coordinator, Gate: svc.Coordinator, Audit: store, NewBackend: func(ctx context.Context) (ports.Backend, error) {
		mu.Lock()
		defer mu.Unlock()
		if backend != nil {
			return nil, errors.New("host already owns a runtime backend")
		}
		created, err := sshexec.New(ctx, sshexec.Options{SSH: []ssh.Option{ssh.WithSecretResolver(materials), ssh.WithKeySource(materials), ssh.WithHostKeyVerifier(materials), ssh.WithHandshakeTimeout(10 * time.Second)}})
		if err != nil {
			return nil, err
		}
		backend = created
		return created, nil
	}}
	h.close = sync.OnceValue(func() error { return errors.Join(svc.Close(), store.Close()) })
	owned = true
	return h, nil
}

// The caller must close the runtime before Host.Close; runtime owns backend,
// while the host owns the coordinator, audit repository, and database.
func (h *Host) Dependencies() ports.Dependencies { return h.dependencies }
func (h *Host) Close() error                     { return h.close() }

func Run(ctx context.Context, cfg config.Config, diagnostics io.Writer) (retErr error) {
	options, err := cfg.HTTPOptions()
	if err != nil {
		return err
	}
	host, err := OpenHost(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, host.Close()) }()
	runtime, err := mcpruntime.NewRuntime(ctx, mcpruntime.WithDependencies(host.Dependencies()), mcpruntime.WithHTTP(options))
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, runtime.Close()) }()
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen for MCP HTTP: %w", err)
	}
	if _, err := fmt.Fprintf(diagnostics, "MCP HTTP listening on %s\n", listener.Addr()); err != nil {
		return errors.Join(err, listener.Close())
	}
	return runtime.ServeHTTP(listener)
}
