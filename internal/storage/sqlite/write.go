package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/wentf9/xops-mcp/internal/storage"
)

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s *Store) Save(ctx context.Context, expected uint64, v storage.Inventory, sources []storage.Source) (retErr error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if v.Revision != expected+1 || expected >= 1<<63-1 {
		return storage.ErrConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin inventory transaction: %w", err)
	}
	defer rollback(tx, &retErr)
	v.Deleted = maps.Clone(v.Deleted)
	if v.Deleted == nil {
		v.Deleted = make(map[string]bool)
	}
	result, err := tx.ExecContext(ctx, "UPDATE deployment SET revision=? WHERE singleton=1 AND domain_id=? AND revision=?", v.Revision, v.DomainID, expected)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return storage.ErrConflict
	}
	// Retain permanent tombstones before replacing relational current state.
	if err := rows(ctx, tx, "SELECT id FROM nodes", func(r *sql.Rows) error {
		var id string
		if err := r.Scan(&id); err != nil {
			return err
		}
		if _, ok := v.Nodes[id]; !ok {
			v.Deleted[id] = true
		}
		return nil
	}); err != nil {
		return err
	}
	for id := range v.Deleted {
		if _, present := v.Nodes[id]; present {
			return errors.New("deleted node ID cannot be reused")
		}
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO tombstones VALUES(?)", id); err != nil {
			return err
		}
	}
	for id := range v.Nodes {
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM tombstones WHERE id=?", id).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return errors.New("deleted node ID cannot be reused")
		}
	}
	for _, table := range []string{"node_jumps", "aliases", "node_tags", "tags", "nodes", "identities", "hosts", "credentials"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return err
		}
	}
	for _, c := range v.Credentials {
		var oldKind string
		var old []byte
		err := tx.QueryRowContext(ctx, "SELECT kind,ciphertext FROM credential_versions WHERE id=? AND version=?", c.ID, c.Version).Scan(&oldKind, &old)
		if err == nil && (oldKind != c.Kind || !bytes.Equal(old, c.Ciphertext)) {
			return errors.New("credential versions are immutable")
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO credential_versions VALUES(?,?,?,?)", c.ID, c.Version, c.Kind, c.Ciphertext); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO credentials VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,kind=excluded.kind,version=excluded.version,ciphertext=excluded.ciphertext", c.ID, c.Name, c.Kind, c.Version, c.Ciphertext); err != nil {
			return err
		}
	}
	for _, h := range v.Hosts {
		if _, err := tx.ExecContext(ctx, "INSERT INTO hosts VALUES(?,?,?,?,?)", h.ID, h.Name, h.Address, h.Port, h.HostKey); err != nil {
			return err
		}
	}
	for _, i := range v.Identities {
		if _, err := tx.ExecContext(ctx, "INSERT INTO identities VALUES(?,?,?,?)", i.ID, i.Name, i.User, nullable(i.CredentialID)); err != nil {
			return err
		}
	}
	for _, tag := range v.Tags {
		if _, err := tx.ExecContext(ctx, "INSERT INTO tags(id,name) VALUES(?,?)", tag.ID, tag.Name); err != nil {
			return err
		}
	}
	for _, n := range v.Nodes {
		if _, err := tx.ExecContext(ctx, "INSERT INTO nodes VALUES(?,?,?,?,?,?,?,?)", n.ID, n.Name, n.HostID, n.IdentityID, nil, n.SudoMode, nullable(n.PrivilegeCredentialID), n.Disabled); err != nil {
			return err
		}
		for position, jump := range n.JumpIDs {
			if _, err := tx.ExecContext(ctx, "INSERT INTO node_jumps VALUES(?,?,?)", n.ID, position, jump); err != nil {
				return err
			}
		}
		for _, a := range n.Aliases {
			if _, err := tx.ExecContext(ctx, "INSERT INTO aliases VALUES(?,?)", a, n.ID); err != nil {
				return err
			}
		}
		for _, tag := range n.TagIDs {
			if _, err := tx.ExecContext(ctx, "INSERT INTO node_tags VALUES(?,?)", n.ID, tag); err != nil {
				return err
			}
		}
	}
	for _, r := range sources {
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO sources VALUES(?,?,?,?,?,?,?,?,?)", r.Token, r.NodeID, r.Host, r.Port, r.User, r.Purpose, r.CredentialID, r.Version, r.HostKey); err != nil {
			return err
		}
	}
	data, err := json.Marshal(v.Policy)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE policies SET config=? WHERE singleton=1", data); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit inventory (outcome requires reconciliation): %w", err)
	}
	return nil
}
