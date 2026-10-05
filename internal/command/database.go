package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/wentf9/xops-mcp/internal/config"
	"github.com/wentf9/xops-mcp/internal/storage"
	"github.com/wentf9/xops-mcp/internal/storage/archive"
	"github.com/wentf9/xops-mcp/internal/storage/database"
)

func databaseCommand(ctx context.Context, cfg config.Config, command, file string, apply bool, out io.Writer) (retErr error) {
	if file == "" || file == "-" {
		return errors.New("database commands require --file with a regular file path")
	}
	vault, err := cfg.Vault()
	if err != nil {
		return err
	}
	store, err := database.Open(ctx, cfg, vault, false)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, store.Close()) }()
	var b storage.Backup
	if command == "db-export" {
		b, err = store.Export(ctx)
		if err != nil {
			return err
		}
		data, err := archive.Encode(ctx, b, vault)
		if err != nil {
			return err
		}
		defer clear(data)
		if err := writeArchive(file, data); err != nil {
			return err
		}
	} else {
		data, err := config.ReadFile(file, archive.MaxSize, true)
		if err != nil {
			return err
		}
		defer clear(data)
		b, err = archive.Decode(ctx, data, vault)
		if err != nil {
			return err
		}
		if command == "db-import" && apply {
			if err := store.Restore(ctx, b); err != nil {
				return err
			}
		}
	}
	digest, err := archive.Digest(ctx, b, vault)
	if err != nil {
		return err
	}
	verified := command == "db-verify" || command == "db-import" && apply
	if verified {
		actual, err := store.Export(ctx)
		if err != nil {
			return err
		}
		got, err := archive.Digest(ctx, actual, vault)
		if err != nil {
			return err
		}
		if got != digest {
			return errors.New("database differs from archive; stop and inspect before retrying")
		}
	}
	return json.NewEncoder(out).Encode(struct {
		DomainID string `json:"domain_id"`
		Revision uint64 `json:"revision"`
		Nodes    int    `json:"nodes"`
		SHA256   string `json:"sha256"`
		Applied  bool   `json:"applied"`
		Verified bool   `json:"verified"`
	}{b.Inventory.DomainID, b.Inventory.Revision, len(b.Inventory.Nodes), digest, apply, verified})
}

func writeArchive(path string, data []byte) (retErr error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("create database archive: %w", err)
	}
	defer func() {
		retErr = errors.Join(retErr, f.Close())
		if retErr != nil {
			retErr = errors.Join(retErr, os.Remove(path))
		}
	}()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write database archive: %w", err)
	}
	return f.Sync()
}
