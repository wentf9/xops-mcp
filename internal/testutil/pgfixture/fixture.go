// Package pgfixture allocates disposable databases on an explicitly configured
// test server. It never reads a deployment configuration or personal DSN.
package pgfixture

import (
	"context"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/wentf9/xops-mcp/internal/secure"
)

// DSN requires a disposable PostgreSQL server and a role with CREATEDB.
func DSN(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("XOPS_TEST_POSTGRES_DSN")
	if raw == "" {
		if os.Getenv("XOPS_TEST_BACKEND") == "postgres" {
			t.Fatal("XOPS_TEST_BACKEND=postgres requires XOPS_TEST_POSTGRES_DSN")
		}
		t.Skip("set XOPS_TEST_POSTGRES_DSN to a disposable PostgreSQL server")
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal("invalid test PostgreSQL URL")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, raw)
	if err != nil {
		t.Fatal("connect to disposable PostgreSQL server:", err)
	}
	name := "xops_test_" + secure.ID()
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE template0 ENCODING 'UTF8'"); err != nil {
		if closeErr := conn.Close(ctx); closeErr != nil {
			t.Error(closeErr)
		}
		t.Fatal(err)
	}
	if err := conn.Close(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		conn, err := pgx.Connect(cleanup, raw)
		if err != nil {
			t.Error(err)
			return
		}
		if _, err := conn.Exec(cleanup, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
		if err := conn.Close(cleanup); err != nil {
			t.Error(err)
		}
	})
	u.Path = "/" + name
	return u.String()
}
