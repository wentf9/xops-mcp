package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/wentf9/xops-mcp/internal/storage"
)

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s *Store) Save(ctx context.Context, expected uint64, v storage.Inventory, sources []storage.Source) (retErr error) {
	ctx, release, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	if v.Revision != expected+1 || expected >= 1<<63-1 {
		return storage.ErrConflict
	}
	tx, finish, err := s.begin(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin inventory transaction: %w", err)
	}
	defer finish(&retErr)
	if err := saveInventory(ctx, tx, expected, v, sources); err != nil {
		return err
	}
	if err := commit(ctx, tx); err != nil {
		return fmt.Errorf("commit inventory (outcome requires reconciliation): %w", err)
	}
	return nil
}

func saveInventory(ctx context.Context, tx *sql.Tx, expected uint64, v storage.Inventory, sources []storage.Source) error {
	result, err := tx.ExecContext(ctx, "UPDATE deployment SET revision=$1 WHERE singleton=1 AND domain_id=$2 AND revision=$3", v.Revision, v.DomainID, expected)
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
	// Keep an explicitly empty slice: NOT (id = ANY(NULL)) would fail to
	// capture deletions when the inventory is cleared completely.
	nodeIDs := make([]string, 0, len(v.Nodes))
	for id := range v.Nodes {
		nodeIDs = append(nodeIDs, id)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO tombstones(id)
SELECT id FROM nodes WHERE NOT (id = ANY($1::text[])) ON CONFLICT(id) DO NOTHING`, nodeIDs); err != nil {
		return err
	}
	deleted := newInsertBatch(ctx, tx, "INSERT INTO tombstones(id)", " ON CONFLICT(id) DO NOTHING")
	for id := range v.Deleted {
		if _, present := v.Nodes[id]; present {
			return errors.New("deleted node ID cannot be reused")
		}
		if err := deleted.add(id); err != nil {
			return err
		}
	}
	if err := deleted.flush(); err != nil {
		return err
	}
	var reused bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM tombstones WHERE id = ANY($1::text[]))", nodeIDs).Scan(&reused); err != nil {
		return err
	}
	if reused {
		return errors.New("deleted node ID cannot be reused")
	}
	if err := checkCredentialVersions(ctx, tx, v.Credentials); err != nil {
		return err
	}
	for _, table := range []string{"node_jumps", "aliases", "node_tags", "tags", "nodes", "identities", "hosts", "credentials"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return err
		}
	}
	versions := newInsertBatch(ctx, tx, "INSERT INTO credential_versions(id,version,kind,ciphertext)", " ON CONFLICT(id,version) DO NOTHING")
	credentials := newInsertBatch(ctx, tx, "INSERT INTO credentials(id,name,kind,version,ciphertext)", "")
	for _, c := range v.Credentials {
		if err := versions.add(c.ID, c.Version, c.Kind, c.Ciphertext); err != nil {
			return err
		}
		if err := credentials.add(c.ID, c.Name, c.Kind, c.Version, c.Ciphertext); err != nil {
			return err
		}
	}
	if err := versions.flush(); err != nil {
		return err
	}
	if err := credentials.flush(); err != nil {
		return err
	}
	hosts := newInsertBatch(ctx, tx, "INSERT INTO hosts(id,name,address,port,host_key)", "")
	for _, h := range v.Hosts {
		if err := hosts.add(h.ID, h.Name, h.Address, h.Port, h.HostKey); err != nil {
			return err
		}
	}
	if err := hosts.flush(); err != nil {
		return err
	}
	identities := newInsertBatch(ctx, tx, "INSERT INTO identities(id,name,username,credential_id)", "")
	for _, i := range v.Identities {
		if err := identities.add(i.ID, i.Name, i.User, nullable(i.CredentialID)); err != nil {
			return err
		}
	}
	if err := identities.flush(); err != nil {
		return err
	}
	tags := newInsertBatch(ctx, tx, "INSERT INTO tags(id,name)", "")
	for _, tag := range v.Tags {
		if err := tags.add(tag.ID, tag.Name); err != nil {
			return err
		}
	}
	if err := tags.flush(); err != nil {
		return err
	}
	nodes := newInsertBatch(ctx, tx, "INSERT INTO nodes(id,name,host_id,identity_id,sudo_mode,privilege_credential_id,disabled)", "")
	for _, n := range v.Nodes {
		if err := nodes.add(n.ID, n.Name, n.HostID, n.IdentityID, n.SudoMode, nullable(n.PrivilegeCredentialID), n.Disabled); err != nil {
			return err
		}
	}
	if err := nodes.flush(); err != nil {
		return err
	}
	jumps := newInsertBatch(ctx, tx, "INSERT INTO node_jumps(node_id,position,jump_id)", "")
	aliases := newInsertBatch(ctx, tx, "INSERT INTO aliases(alias,node_id)", "")
	nodeTags := newInsertBatch(ctx, tx, "INSERT INTO node_tags(node_id,tag_id)", "")
	for _, n := range v.Nodes {
		for position, jump := range n.JumpIDs {
			if err := jumps.add(n.ID, position, jump); err != nil {
				return err
			}
		}
		for _, alias := range n.Aliases {
			if err := aliases.add(alias, n.ID); err != nil {
				return err
			}
		}
		for _, tag := range n.TagIDs {
			if err := nodeTags.add(n.ID, tag); err != nil {
				return err
			}
		}
	}
	if err := jumps.flush(); err != nil {
		return err
	}
	if err := aliases.flush(); err != nil {
		return err
	}
	if err := nodeTags.flush(); err != nil {
		return err
	}
	bindings := newInsertBatch(ctx, tx, "INSERT INTO sources(token,node_id,host,port,username,purpose,credential_id,version,host_key)", " ON CONFLICT(token,node_id,host,port,username,purpose) DO NOTHING")
	for _, r := range sources {
		if err := bindings.add(r.Token, r.NodeID, r.Host, r.Port, r.User, r.Purpose, r.CredentialID, r.Version, r.HostKey); err != nil {
			return err
		}
	}
	if err := bindings.flush(); err != nil {
		return err
	}
	data, err := json.Marshal(v.Policy)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE policies SET config=$1 WHERE singleton=1", string(data)); err != nil {
		return err
	}
	return nil
}

// Match only incoming (ID, version) pairs, regardless of how much historical
// material is retained. The deployment row lock already serializes writers.
func checkCredentialVersions(ctx context.Context, tx *sql.Tx, credentials map[string]storage.Credential) (retErr error) {
	if len(credentials) == 0 {
		return nil
	}
	ids, versions := make([]string, 0, len(credentials)), make([]string, 0, len(credentials))
	for _, c := range credentials {
		ids = append(ids, c.ID)
		versions = append(versions, c.Version)
	}
	rows, err := tx.QueryContext(ctx, `SELECT old.id,old.version,old.kind,old.ciphertext
FROM credential_versions old JOIN unnest($1::text[], $2::text[]) incoming(id,version)
ON old.id=incoming.id AND old.version=incoming.version`, ids, versions)
	if err != nil {
		return fmt.Errorf("check credential versions: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, rows.Close()) }()
	for rows.Next() {
		var id, version, kind string
		var ciphertext []byte
		if err := rows.Scan(&id, &version, &kind, &ciphertext); err != nil {
			return err
		}
		c := credentials[id]
		if c.Version != version || c.Kind != kind || !bytes.Equal(c.Ciphertext, ciphertext) {
			return errors.New("credential versions are immutable")
		}
	}
	return rows.Err()
}
