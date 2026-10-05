package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	mcpruntime "github.com/wentf9/xops-cli/core/mcp/runtime"
	"github.com/wentf9/xops-mcp/internal/api"
	"github.com/wentf9/xops-mcp/internal/config"
	"github.com/wentf9/xops-mcp/internal/operations"
	"github.com/wentf9/xops-mcp/internal/service"
	"github.com/wentf9/xops-mcp/internal/storage"
	"github.com/wentf9/xops-mcp/web"
)

type Application struct {
	Host         *Host
	Runtime      *mcpruntime.Runtime
	handler      http.Handler
	adminHandler http.Handler
	close        func() error
	options      mcpruntime.HTTPOptions
	webOptions   config.WebOptions
}

func NewApplication(ctx context.Context, cfg config.Config) (_ *Application, retErr error) {
	options, err := cfg.HTTPOptions()
	if err != nil {
		return nil, err
	}
	var webOptions config.WebOptions
	if cfg.WebEnabled {
		webOptions, err = cfg.WebOptions()
		if err != nil {
			return nil, err
		}
	}
	host, err := OpenHost(ctx, cfg)
	if err != nil {
		return nil, err
	}
	owned := false
	defer func() {
		if !owned {
			retErr = errors.Join(retErr, host.Close())
		}
	}()
	tracker := operations.New(host.Service.Coordinator)
	dependencies := host.Dependencies()
	dependencies.Gate = tracker
	lifetime, cancel := context.WithCancel(ctx)
	runtime, err := mcpruntime.NewRuntime(lifetime, mcpruntime.WithDependencies(dependencies), mcpruntime.WithHTTP(options))
	if err != nil {
		cancel()
		return nil, err
	}
	defer func() {
		if !owned {
			cancel()
			retErr = errors.Join(retErr, runtime.Close())
		}
	}()
	handler, err := runtime.HTTPHandler()
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	mux.Handle("/v1/transfers/", handler)
	adminHandler := http.NotFoundHandler()
	if cfg.WebEnabled {
		if err := host.Store.DeleteSessions(ctx); err != nil {
			return nil, err
		}
		setup := ""
		if _, err := host.Store.Admin(ctx); errors.Is(err, storage.ErrNotFound) && cfg.AdminBootstrapTokenFile != "" {
			data, err := config.ReadFile(cfg.AdminBootstrapTokenFile, 4098, true)
			if err != nil {
				return nil, err
			}
			setup = string(bytes.TrimSpace(data))
			clear(data)
			if setup == options.Token {
				return nil, errors.New("administrator setup and MCP must use different tokens")
			}
		} else if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return nil, err
		}
		admin, err := api.New(host.Store, &service.Editor{Service: host.Service, Vault: host.Vault}, host.materials, tracker, api.Options{PublicURL: webOptions.PublicURL, BasePath: webOptions.BasePath, AllowedHosts: webOptions.AllowedHosts, SetupToken: setup})
		if err != nil {
			return nil, err
		}
		adminHandler = admin.Handler(web.Handler())
	}
	options.Token = ""
	app := &Application{Host: host, Runtime: runtime, options: options, webOptions: webOptions}
	wrap := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Bound every response before routing, admission or rejection. The
			// core's streaming handlers can replace and renew this deadline.
			if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(options.StreamIdle)); err != nil {
				http.Error(w, "write_deadline_unavailable", http.StatusInternalServerError)
				return
			}
			if lifetime.Err() != nil {
				http.Error(w, "service_stopping", http.StatusServiceUnavailable)
				return
			}
			work, stop := context.WithCancel(r.Context())
			defer stop()
			stopCancel := context.AfterFunc(lifetime, stop)
			defer stopCancel()
			next.ServeHTTP(w, r.WithContext(work))
		})
	}
	app.handler, app.adminHandler = wrap(mux), wrap(adminHandler)
	app.close = sync.OnceValue(func() error { cancel(); return errors.Join(runtime.Close(), host.Close()) })
	owned = true
	return app, nil
}
func (a *Application) Handler() http.Handler      { return a.handler }
func (a *Application) AdminHandler() http.Handler { return a.adminHandler }
func (a *Application) Close() error               { return a.close() }

func Run(ctx context.Context, cfg config.Config, diagnostics io.Writer) (retErr error) {
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	app, err := NewApplication(work, cfg)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, app.Close()) }()
	type endpoint struct {
		name, address string
		handler       http.Handler
	}
	endpoints := []endpoint{{"MCP", cfg.Listen, app.Handler()}}
	if cfg.WebEnabled {
		endpoints = append(endpoints, endpoint{"Web", app.webOptions.Listen, app.AdminHandler()})
	}
	var servers []*http.Server
	var listeners []net.Listener
	defer func() {
		for _, listener := range listeners {
			if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				retErr = errors.Join(retErr, err)
			}
		}
	}()
	for _, endpoint := range endpoints {
		listener, err := (&net.ListenConfig{}).Listen(work, "tcp", endpoint.address)
		if err != nil {
			return fmt.Errorf("listen for %s HTTP: %w", endpoint.name, err)
		}
		listeners = append(listeners, listener)
		servers = append(servers, &http.Server{Handler: endpoint.handler, ReadHeaderTimeout: app.options.HeaderTimeout, ReadTimeout: app.options.BodyTimeout, WriteTimeout: app.options.StreamIdle, IdleTimeout: app.options.StreamIdle, MaxHeaderBytes: 16 << 10, BaseContext: func(net.Listener) context.Context { return work }})
	}
	for index, endpoint := range endpoints {
		if _, err := fmt.Fprintf(diagnostics, "%s HTTP listening on %s\n", endpoint.name, listeners[index].Addr()); err != nil {
			return err
		}
	}
	done := make(chan error, len(servers))
	for index, server := range servers {
		// Each Serve exits when its listener is shut down below. All workers
		// are joined before runtime/journal/database resources are closed.
		go func() {
			err := server.Serve(listeners[index])
			if errors.Is(err, http.ErrServerClosed) {
				err = nil
			}
			done <- err
		}()
	}
	remaining := len(servers)
	select {
	case err := <-done:
		retErr = errors.Join(retErr, err)
		remaining--
	case <-work.Done():
	}
	cancel()
	cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), app.options.ShutdownTimeout)
	defer stop()
	stopped := make(chan error, len(servers))
	for _, server := range servers {
		// Both listeners stop accepting immediately; their drain shares one
		// deadline and falls back to Close. Join every shutdown worker.
		go func() {
			err := server.Shutdown(cleanup)
			if err != nil {
				err = errors.Join(err, server.Close())
			}
			stopped <- err
		}()
	}
	for range servers {
		retErr = errors.Join(retErr, <-stopped)
	}
	for range remaining {
		retErr = errors.Join(retErr, <-done)
	}
	return retErr
}
