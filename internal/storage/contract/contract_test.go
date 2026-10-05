package contract_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/wentf9/xops-cli/core/mcp/ports"
	"github.com/wentf9/xops-mcp/internal/adapters/xops"
	"github.com/wentf9/xops-mcp/internal/adminauth"
	"github.com/wentf9/xops-mcp/internal/importer"
	"github.com/wentf9/xops-mcp/internal/secure"
	"github.com/wentf9/xops-mcp/internal/service"
	"github.com/wentf9/xops-mcp/internal/storage"
	"github.com/wentf9/xops-mcp/internal/storage/archive"
	"github.com/wentf9/xops-mcp/internal/storage/postgres"
	"github.com/wentf9/xops-mcp/internal/storage/sqlite"
	"github.com/wentf9/xops-mcp/internal/testutil"
	"github.com/wentf9/xops-mcp/internal/testutil/pgfixture"
)

func open(t *testing.T, backend string) (storage.Database, *secure.Vault) {
	t.Helper()
	v, err := secure.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "data")
	var s storage.Database
	if backend == "postgres" {
		s, err = postgres.Open(t.Context(), dir, pgfixture.DSN(t), v)
	} else {
		s, err = sqlite.Open(t.Context(), dir, v)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testutil.Close(t, s) })
	return s, v
}
func inventory(t *testing.T, s storage.Repository) storage.Inventory {
	t.Helper()
	v, err := s.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func save(t *testing.T, s storage.Repository, v storage.Inventory) {
	t.Helper()
	expected := v.Revision
	v.Revision++
	_, sources, err := xops.Snapshot(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(t.Context(), expected, v, sources); err != nil {
		t.Fatal(err)
	}
}
func seed(t *testing.T, s storage.Database, vault *secure.Vault) storage.Backup {
	t.Helper()
	doc := testutil.Document(t)
	doc.Nodes["removed"] = importer.Node{Host: "peer", Identity: "login", Disabled: true}
	doc.Tags = []string{"unused"}
	v, report := testutil.Plan(t, s, vault, doc)
	save(t, s, v)
	v = inventory(t, s)
	delete(v.Nodes, report.NodeIDs["removed"])
	save(t, s, v)
	rotated := importer.Document{Credentials: map[string]importer.Credential{"login": {Kind: "password", Password: "rotated-fixture-password"}}}
	v, _ = testutil.Plan(t, s, vault, rotated)
	save(t, s, v)
	auth, err := adminauth.New(s, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.Initialize(t.Context(), "admin", "fixture-administrator-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Login(t.Context(), "admin", "fixture-administrator-password"); err != nil {
		t.Fatal(err)
	}
	for _, e := range []ports.AuditEvent{{OperationID: "one", NodeID: report.NodeIDs["peer"], Outcome: "executed", Command: "secret", Paths: []string{"secret"}}, {OperationID: "two", NodeIDs: []string{report.NodeIDs["peer"]}, Outcome: "denied", Error: "secret", Details: "secret"}} {
		if err := s.Append(t.Context(), e); err != nil {
			t.Fatal(err)
		}
	}
	b, err := s.Export(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestRepositories(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			t.Run("transactions-history-audit", func(t *testing.T) {
				s, vault := open(t, backend)
				b := seed(t, s, vault)
				if len(b.Versions) != 2 || len(b.Inventory.Deleted) != 1 || b.Admin == nil || len(b.Sources) < 7 {
					t.Fatal("export dropped history or management data")
				}
				for _, v := range b.Versions {
					c, err := s.Credential(t.Context(), v.ID, v.Version)
					if err != nil || !bytes.Equal(c.Ciphertext, v.Ciphertext) {
						t.Fatal("historical credential changed", err)
					}
				}
				for _, q := range b.Sources {
					got, err := s.Source(t.Context(), q)
					if err != nil || got != q {
						t.Fatal("historical binding changed", err)
					}
					q.Port++
					if _, err := s.Source(t.Context(), q); err == nil {
						t.Fatal("endpoint binding not checked")
					}
				}
				var nodeID string
				for id := range b.Inventory.Nodes {
					nodeID = id
				}
				events, err := s.Audit(t.Context(), storage.AuditQuery{NodeID: nodeID, Limit: 1})
				if err != nil || len(events) != 1 || events[0].Event.OperationID != "two" {
					t.Fatalf("batch audit filter: %+v %v", events, err)
				}
				next, err := s.Audit(t.Context(), storage.AuditQuery{NodeID: nodeID, BeforeID: events[0].ID, Outcome: "executed", OperationID: "one", Limit: 10})
				if err != nil || len(next) != 1 {
					t.Fatal("audit cursor/filter", err)
				}
				data, _ := json.Marshal(b.Audit)
				if bytes.Contains(data, []byte("secret")) {
					t.Fatal("audit leaked free-form secrets")
				}
				before := inventory(t, s)
				bad := before.Clone()
				bad.Revision++
				bad.Nodes["broken"] = storage.Node{ID: "broken", Name: "broken", HostID: "absent", IdentityID: "absent"}
				if err := s.Save(t.Context(), before.Revision, bad, nil); err == nil {
					t.Fatal("foreign key violation committed")
				}
				if got := inventory(t, s); got.Revision != before.Revision || len(got.Nodes) != len(before.Nodes) {
					t.Fatal("failed save not rolled back")
				}
				bad = before.Clone()
				bad.Revision++
				for id, c := range bad.Credentials {
					c.Ciphertext = bytes.Clone(c.Ciphertext)
					c.Ciphertext[1] ^= 1
					bad.Credentials[id] = c
				}
				if err := s.Save(t.Context(), before.Revision, bad, nil); err == nil {
					t.Fatal("immutable version overwritten")
				}
				bad = before.Clone()
				bad.Revision++
				for id := range before.Deleted {
					bad.Nodes[id] = storage.Node{ID: id, Name: "reused", HostID: "absent", IdentityID: "absent"}
					delete(bad.Deleted, id)
				}
				if err := s.Save(t.Context(), before.Revision, bad, nil); err == nil {
					t.Fatal("tombstone reused")
				}
			})
			t.Run("optimistic-concurrency-and-cancellation", func(t *testing.T) {
				s, _ := open(t, backend)
				v := inventory(t, s)
				v.Revision++
				begin := make(chan struct{})
				results := make(chan error, 2)
				var workers sync.WaitGroup
				for range 2 {
					workers.Go(func() { <-begin; results <- s.Save(t.Context(), 0, v, nil) })
				}
				close(begin)
				workers.Wait()
				close(results)
				ok, conflicts := 0, 0
				for err := range results {
					if err == nil {
						ok++
					} else if errors.Is(err, storage.ErrConflict) {
						conflicts++
					} else {
						t.Fatal(err)
					}
				}
				if ok != 1 || conflicts != 1 {
					t.Fatal("concurrent revisions did not conflict")
				}
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				if err := s.Append(ctx, ports.AuditEvent{}); err == nil {
					t.Fatal("cancelled operation wrote data")
				}
			})
			t.Run("session-limit-expiry-and-revocation", func(t *testing.T) {
				s, _ := open(t, backend)
				if err := s.InitializeAdmin(t.Context(), storage.Admin{Username: "admin", PasswordHash: []byte("fixture"), Version: "v1"}); err != nil {
					t.Fatal(err)
				}
				if err := s.InitializeAdmin(t.Context(), storage.Admin{Username: "other", PasswordHash: []byte("fixture"), Version: "v2"}); !errors.Is(err, storage.ErrAdminExists) {
					t.Fatal("admin replaced", err)
				}
				now := time.Now()
				digest := make([]byte, 32)
				for i := range 64 {
					digest = bytes.Repeat([]byte{byte(i)}, 32)
					if err := s.CreateSession(t.Context(), storage.Session{Digest: digest, AdminVersion: "v1", ExpiresAt: now.Add(time.Hour)}); err != nil {
						t.Fatal(err)
					}
				}
				if err := s.CreateSession(t.Context(), storage.Session{Digest: bytes.Repeat([]byte{65}, 32), AdminVersion: "v1", ExpiresAt: now.Add(time.Hour)}); !errors.Is(err, storage.ErrConflict) {
					t.Fatal("session limit bypassed", err)
				}
				if _, err := s.Session(t.Context(), digest, now.Add(2*time.Hour)); !errors.Is(err, storage.ErrNotFound) {
					t.Fatal("expired session accepted")
				}
				if err := s.ChangeAdminPassword(t.Context(), "stale", []byte("new"), "v2"); !errors.Is(err, storage.ErrConflict) {
					t.Fatal("stale password changed")
				}
				if _, err := s.Session(t.Context(), digest, now); err != nil {
					t.Fatal("failed password edit revoked session")
				}
				if err := s.ChangeAdminPassword(t.Context(), "v1", []byte("new"), "v2"); err != nil {
					t.Fatal(err)
				}
				if _, err := s.Session(t.Context(), digest, now); !errors.Is(err, storage.ErrNotFound) {
					t.Fatal("password reset retained session")
				}
				if err := s.CreateSession(t.Context(), storage.Session{Digest: digest, AdminVersion: "v1", ExpiresAt: now.Add(time.Hour)}); !errors.Is(err, storage.ErrConflict) {
					t.Fatal("login race restored stale session")
				}
			})
		})
	}
}
func TestCrossBackendArchive(t *testing.T) {
	for _, from := range []string{"sqlite", "postgres"} {
		for _, to := range []string{"sqlite", "postgres"} {
			t.Run(from+"-to-"+to, func(t *testing.T) {
				source, vault := open(t, from)
				b := seed(t, source, vault)
				encoded, err := archive.Encode(t.Context(), b, vault)
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Contains(encoded, []byte("fixture")) {
					t.Fatal("archive is not encrypted")
				}
				wrong, _ := secure.New(make([]byte, 32))
				if _, err := archive.Decode(t.Context(), encoded, wrong); err == nil {
					t.Fatal("wrong archive key accepted")
				}
				damaged := bytes.Clone(encoded)
				damaged[len(damaged)-1] ^= 1
				if _, err := archive.Decode(t.Context(), damaged, vault); err == nil {
					t.Fatal("damaged archive accepted")
				}
				decoded, err := archive.Decode(t.Context(), encoded, vault)
				if err != nil {
					t.Fatal(err)
				}
				target, _ := open(t, to)
				before := inventory(t, target)
				invalid := decoded
				invalid.Inventory = decoded.Inventory.Clone()
				for id, h := range invalid.Inventory.Hosts {
					h.Address = "bad address"
					invalid.Inventory.Hosts[id] = h
				}
				if err := target.Restore(t.Context(), invalid); err == nil {
					t.Fatal("invalid restore succeeded")
				}
				if got := inventory(t, target); got.DomainID != before.DomainID || got.Revision != 0 {
					t.Fatal("failed restore changed target")
				}
				// A database constraint failing after deployment identity/inventory writes
				// must roll back the entire restore, including its new domain and history.
				invalid = decoded
				invalid.Inventory = decoded.Inventory.Clone()
				for _, h := range invalid.Inventory.Hosts {
					h.ID = "duplicate-host-name"
					invalid.Inventory.Hosts[h.ID] = h
					break
				}
				if err := target.Restore(t.Context(), invalid); err == nil {
					t.Fatal("constraint violation in restore committed")
				}
				if got := inventory(t, target); got.DomainID != before.DomainID || got.Revision != 0 {
					t.Fatal("restore constraint rollback changed target")
				}
				if err := target.Restore(t.Context(), decoded); err != nil {
					t.Fatal(err)
				}
				actual, err := target.Export(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				a, err := archive.Digest(t.Context(), b, vault)
				if err != nil {
					t.Fatal(err)
				}
				c, err := archive.Digest(t.Context(), actual, vault)
				if err != nil || a != c {
					t.Fatalf("backend data differs: %s %s %v", a, c, err)
				}
				if err := target.Restore(t.Context(), decoded); err == nil {
					t.Fatal("restore overwrote populated target")
				}
				manager, err := adminauth.New(target, 4)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := manager.Login(t.Context(), "admin", "fixture-administrator-password"); err != nil {
					t.Fatal("restored password unusable", err)
				}
				svc, err := service.New(t.Context(), target, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer testutil.Close(t, svc)
				if _, err := svc.Coordinator.Resolve(t.Context(), ports.ResolveRequest{Selectors: []string{"peer"}}); err != nil {
					t.Fatal("restored inventory is unusable", err)
				}
				if err := target.Append(t.Context(), ports.AuditEvent{OperationID: "after-restore"}); err != nil {
					t.Fatal(err)
				}
				events, err := target.Audit(t.Context(), storage.AuditQuery{Limit: 1})
				if err != nil || len(events) != 1 || events[0].ID <= b.Audit[len(b.Audit)-1].ID {
					t.Fatal("restored audit sequence did not advance", err)
				}
			})
		}
	}
}
