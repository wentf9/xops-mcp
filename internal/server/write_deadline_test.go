package server_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wentf9/xops-mcp/internal/server"
	"github.com/wentf9/xops-mcp/internal/testutil"
)

type pipeListener struct {
	conn   net.Conn
	addr   net.Addr
	closed chan struct{}
	once   sync.Once
}

func (l *pipeListener) Accept() (net.Conn, error) {
	if l.conn != nil {
		conn := l.conn
		l.conn = nil
		return conn, nil
	}
	<-l.closed
	return nil, net.ErrClosed
}
func (l *pipeListener) Addr() net.Addr { return l.addr }
func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

type writeResult struct {
	response string
	err      error
}

type shortDeadlineConn struct {
	net.Conn
	writes chan writeResult
}

func (c *shortDeadlineConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345}
}

func (c *shortDeadlineConn) SetWriteDeadline(deadline time.Time) error {
	// Keep real net.Conn deadline behavior without waiting for the production
	// timeout. A missing or cleared deadline still leaves the write blocked.
	if !deadline.IsZero() && time.Until(deadline) > 100*time.Millisecond {
		deadline = time.Now().Add(100 * time.Millisecond)
	}
	return c.Conn.SetWriteDeadline(deadline)
}
func (c *shortDeadlineConn) Write(data []byte) (int, error) {
	n, err := c.Conn.Write(data)
	select {
	case c.writes <- writeResult{response: string(data), err: err}:
	default:
	}
	return n, err
}

func TestHTTPResponsesBoundUnreadWrites(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cfg := adminConfig(t)
	cfg.WebEnabled, cfg.WebPublicURL, cfg.WebBasePath = true, "http://localhost", "/ops"
	app, err := server.NewApplication(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, app)
	for _, check := range []struct {
		name, path, host, headers string
		admin, stopping           bool
		status                    int
	}{
		{name: "MCP unmatched", path: "/missing", status: 404},
		{name: "MCP redirect", path: "/v1/transfers", status: 301},
		{name: "MCP authentication", path: "/mcp", host: "127.0.0.1:8080", status: 401},
		{name: "admin Host", path: "/ops/", host: "untrusted.invalid", admin: true, status: 403},
		{name: "admin Origin", path: "/ops/", headers: "Origin: http://untrusted.invalid\r\n", admin: true, status: 403},
		{name: "admin cross site", path: "/ops/api/v1/auth/session", headers: "Sec-Fetch-Site: cross-site\r\n", admin: true, status: 403},
		{name: "admin unmatched", path: "/missing", admin: true, status: 404},
		{name: "admin document", path: "/ops/", admin: true, status: 200},
		{name: "MCP stopping", path: "/mcp", stopping: true, status: 503},
		{name: "admin stopping", path: "/ops/", admin: true, stopping: true, status: 503},
	} {
		t.Run(check.name, func(t *testing.T) {
			if check.stopping {
				cancel()
			}
			handler := app.Handler()
			if check.admin {
				handler = app.AdminHandler()
			}
			serverConn, clientConn := net.Pipe()
			conn := &shortDeadlineConn{Conn: serverConn, writes: make(chan writeResult, 1)}
			listener := &pipeListener{conn: conn, addr: conn.LocalAddr(), closed: make(chan struct{})}
			closed, served := make(chan struct{}), make(chan error, 1)
			srv := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second, IdleTimeout: time.Second, ConnState: func(_ net.Conn, state http.ConnState) {
				if state == http.StateClosed {
					close(closed)
				}
			}}
			// Cleanup closes both pipe ends and the listener, then joins Serve and
			// the connection worker even when the deadline regression is present.
			go func() { served <- srv.Serve(listener) }()
			t.Cleanup(func() {
				testutil.Close(t, clientConn)
				testutil.Close(t, srv)
				if err := <-served; !errors.Is(err, http.ErrServerClosed) {
					t.Errorf("serve pipe: %v", err)
				}
				select {
				case <-closed:
				case <-time.After(2 * time.Second):
					t.Error("HTTP connection worker did not stop")
				}
			})
			if err := clientConn.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
				t.Fatal(err)
			}
			host := check.host
			if host == "" {
				host = "localhost"
			}
			request := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\n%s\r\n", check.path, host, check.headers)
			// A pipe has no send buffer: the client pipelines requests but never
			// reads, reproducing a full network send buffer deterministically.
			if _, err := io.WriteString(clientConn, request+request); err != nil {
				t.Fatal(err)
			}
			select {
			case result := <-conn.writes:
				// ServeMux's redirect status differs across supported Go versions.
				redirect := check.status/100 == 3 && strings.HasPrefix(result.response, "HTTP/1.1 3")
				if !redirect && !strings.HasPrefix(result.response, fmt.Sprintf("HTTP/1.1 %d ", check.status)) {
					t.Fatalf("unexpected response: %s", result.response)
				}
				if !errors.Is(result.err, os.ErrDeadlineExceeded) {
					t.Fatalf("write ended without a deadline: %v", result.err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("HTTP response write remained blocked without a deadline")
			}
			select {
			case <-closed:
			case <-time.After(2 * time.Second):
				t.Fatal("expired write retained the HTTP connection worker")
			}
		})
	}
}
