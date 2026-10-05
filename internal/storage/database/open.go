// Package database selects the deployment's explicitly configured backend.
package database

import (
	"bytes"
	"context"

	"github.com/wentf9/xops-mcp/internal/config"
	"github.com/wentf9/xops-mcp/internal/secure"
	"github.com/wentf9/xops-mcp/internal/storage"
	"github.com/wentf9/xops-mcp/internal/storage/postgres"
	"github.com/wentf9/xops-mcp/internal/storage/sqlite"
)

func Open(ctx context.Context, cfg config.Config, vault *secure.Vault, migrate bool) (storage.Database, error) {
	if err := cfg.ValidateDatabase(); err != nil {
		return nil, err
	}
	if cfg.DatabaseDriver == "postgres" {
		data, err := config.ReadFile(cfg.PostgresDSNFile, 16<<10, true)
		if err != nil {
			return nil, err
		}
		defer clear(data)
		dsn := string(bytes.TrimSpace(data))
		if migrate {
			return postgres.Open(ctx, cfg.DataDir, dsn, vault)
		}
		return postgres.OpenExisting(ctx, cfg.DataDir, dsn, vault)
	}
	if migrate {
		return sqlite.Open(ctx, cfg.DataDir, vault)
	}
	return sqlite.OpenExisting(ctx, cfg.DataDir, vault)
}
