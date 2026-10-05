package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/wentf9/xops-cli/core/mcp/ports"
	"github.com/wentf9/xops-mcp/internal/storage"
)

type querier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func rows(ctx context.Context, db querier, query string, scan func(*sql.Rows) error) (retErr error) {
	result, err := db.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("query inventory: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, result.Close()) }()
	for result.Next() {
		if err := scan(result); err != nil {
			return fmt.Errorf("decode inventory: %w", err)
		}
	}
	return result.Err()
}

func (s *Store) Load(ctx context.Context) (_ storage.Inventory, retErr error) {
	ctx, release, err := s.acquire(ctx)
	if err != nil {
		return storage.Inventory{}, err
	}
	defer release()
	tx, finish, err := s.begin(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return storage.Inventory{}, err
	}
	defer finish(&retErr)
	v, err := loadInventory(ctx, tx)
	if err != nil {
		return v, err
	}
	return v, commit(ctx, tx)
}

func loadInventory(ctx context.Context, tx *sql.Tx) (storage.Inventory, error) {
	v := storage.Inventory{Hosts: map[string]storage.Host{}, Identities: map[string]storage.Identity{}, Nodes: map[string]storage.Node{}, Credentials: map[string]storage.Credential{}, Deleted: map[string]bool{}}
	v.Tags = map[string]storage.Tag{}
	var policy []byte
	if err := tx.QueryRowContext(ctx, "SELECT domain_id, revision FROM deployment WHERE singleton = 1").Scan(&v.DomainID, &v.Revision); err != nil {
		return v, err
	}
	if err := tx.QueryRowContext(ctx, "SELECT config FROM policies WHERE singleton = 1").Scan(&policy); err != nil {
		return v, err
	}
	if err := json.Unmarshal(policy, &v.Policy); err != nil {
		return v, fmt.Errorf("decode stored policy: %w", err)
	}
	steps := []struct {
		query string
		scan  func(*sql.Rows) error
	}{
		{"SELECT id,name,address,port,host_key FROM hosts", func(r *sql.Rows) error {
			var x storage.Host
			err := r.Scan(&x.ID, &x.Name, &x.Address, &x.Port, &x.HostKey)
			v.Hosts[x.ID] = x
			return err
		}},
		{"SELECT id,name,username,COALESCE(credential_id,'') FROM identities", func(r *sql.Rows) error {
			var x storage.Identity
			err := r.Scan(&x.ID, &x.Name, &x.User, &x.CredentialID)
			v.Identities[x.ID] = x
			return err
		}},
		{"SELECT id,name,kind,version,ciphertext FROM credentials", func(r *sql.Rows) error {
			var x storage.Credential
			err := r.Scan(&x.ID, &x.Name, &x.Kind, &x.Version, &x.Ciphertext)
			v.Credentials[x.ID] = x
			return err
		}},
		{"SELECT id,name,host_id,identity_id,sudo_mode,COALESCE(privilege_credential_id,''),disabled FROM nodes", func(r *sql.Rows) error {
			var x storage.Node
			err := r.Scan(&x.ID, &x.Name, &x.HostID, &x.IdentityID, &x.SudoMode, &x.PrivilegeCredentialID, &x.Disabled)
			v.Nodes[x.ID] = x
			return err
		}},
		{"SELECT node_id,jump_id FROM node_jumps ORDER BY node_id,position", func(r *sql.Rows) error {
			var id, jump string
			if err := r.Scan(&id, &jump); err != nil {
				return err
			}
			n := v.Nodes[id]
			n.JumpIDs = append(n.JumpIDs, jump)
			v.Nodes[id] = n
			return nil
		}},
		{"SELECT node_id,alias FROM aliases ORDER BY alias", func(r *sql.Rows) error {
			var id, alias string
			if err := r.Scan(&id, &alias); err != nil {
				return err
			}
			x := v.Nodes[id]
			x.Aliases = append(x.Aliases, alias)
			v.Nodes[id] = x
			return nil
		}},
		{"SELECT id,name FROM tags", func(r *sql.Rows) error {
			var x storage.Tag
			err := r.Scan(&x.ID, &x.Name)
			v.Tags[x.ID] = x
			return err
		}},
		{"SELECT node_id,tag_id FROM node_tags ORDER BY tag_id", func(r *sql.Rows) error {
			var id, tag string
			if err := r.Scan(&id, &tag); err != nil {
				return err
			}
			x := v.Nodes[id]
			x.TagIDs = append(x.TagIDs, tag)
			v.Nodes[id] = x
			return nil
		}},
		{"SELECT id FROM tombstones", func(r *sql.Rows) error {
			var id string
			if err := r.Scan(&id); err != nil {
				return err
			}
			v.Deleted[id] = true
			return nil
		}},
	}
	for _, step := range steps {
		if err := rows(ctx, tx, step.query, step.scan); err != nil {
			return v, err
		}
	}
	return v, nil
}

func (s *Store) Credential(ctx context.Context, id, version string) (storage.Credential, error) {
	ctx, release, err := s.acquire(ctx)
	if err != nil {
		return storage.Credential{}, err
	}
	defer release()
	c := storage.Credential{ID: id, Version: version}
	err = s.db.QueryRowContext(ctx, "SELECT kind,ciphertext FROM credential_versions WHERE id=$1 AND version=$2", id, version).Scan(&c.Kind, &c.Ciphertext)
	if err != nil {
		return c, fmt.Errorf("read bound credential: %w", err)
	}
	return c, nil
}

func (s *Store) Source(ctx context.Context, q storage.Source) (storage.Source, error) {
	ctx, release, err := s.acquire(ctx)
	if err != nil {
		return q, err
	}
	defer release()
	err = s.db.QueryRowContext(ctx, "SELECT credential_id,version,host_key FROM sources WHERE token=$1 AND node_id=$2 AND host=$3 AND port=$4 AND username=$5 AND purpose=$6", q.Token, q.NodeID, q.Host, q.Port, q.User, q.Purpose).Scan(&q.CredentialID, &q.Version, &q.HostKey)
	if err != nil {
		return q, fmt.Errorf("read bound material reference: %w", err)
	}
	return q, nil
}

func (s *Store) Append(ctx context.Context, event ports.AuditEvent) error {
	ctx, release, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	// Commands, paths, free-form diagnostics, and details can contain secrets
	// supplied by clients. Persist only the operation's structured outcome.
	event.Command, event.Paths, event.Error, event.Details = "", nil, "", ""
	event.Timestamp = time.Now().UTC()
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode audit event: %w", err)
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO audit_events(occurred_at,operation_id,tool,outcome,event) VALUES($1,$2,$3,$4,$5)", event.Timestamp, event.OperationID, event.Tool, event.Outcome, string(data))
	if err != nil {
		return fmt.Errorf("persist audit event: %w", err)
	}
	return nil
}
