// Package server assembles the persistent host and the shared runtime.
package server

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/wentf9/xops-cli/core/mcp/ports"
	"github.com/wentf9/xops-cli/core/mcp/sshexec"
	"github.com/wentf9/xops-cli/core/ssh"
	"github.com/wentf9/xops-mcp/internal/adapters/xops"
	"github.com/wentf9/xops-mcp/internal/config"
	"github.com/wentf9/xops-mcp/internal/secure"
	"github.com/wentf9/xops-mcp/internal/service"
	"github.com/wentf9/xops-mcp/internal/storage"
	"github.com/wentf9/xops-mcp/internal/storage/database"
)

type Host struct {
	Store        storage.Database
	Service      *service.Service
	Vault        *secure.Vault
	dependencies ports.Dependencies
	materials    *xops.Materials
	close        func() error
	lifetime     context.Context
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
	store, err := database.Open(ctx, cfg, vault, allowMigration)
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
	lifetime, cancel := context.WithCancelCause(context.Background())
	stopWatch := func() bool { return true }
	watchDone := make(chan struct{})
	var watchErr error
	if watched, ok := store.(interface{ Lifetime() context.Context }); ok {
		stopWatch = context.AfterFunc(watched.Lifetime(), func() {
			cancel(context.Cause(watched.Lifetime()))
			// A lost ownership session permanently closes admission and cancels
			// existing permits; it never reconnects into an old runtime view.
			watchErr = svc.Coordinator.Close()
			close(watchDone)
		})
	}
	h := &Host{Store: store, Service: svc, Vault: vault, lifetime: lifetime}
	materials := &xops.Materials{Store: store, Vault: vault, DomainID: svc.Coordinator.DomainID()}
	h.materials = materials
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
	h.close = sync.OnceValue(func() error {
		cancel(context.Canceled)
		if !stopWatch() {
			<-watchDone
		}
		return errors.Join(watchErr, svc.Close(), store.Close())
	})
	owned = true
	return h, nil
}

// The caller must close the runtime before Host.Close; runtime owns backend,
// while the host owns the coordinator, audit repository, and database.
func (h *Host) Dependencies() ports.Dependencies { return h.dependencies }
func (h *Host) Close() error                     { return h.close() }
