package postgres

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"path/filepath"
	"reflect"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/wentf9/xops-cli/core/mcp/ports"
	"github.com/wentf9/xops-mcp/internal/adapters/xops"
	"github.com/wentf9/xops-mcp/internal/secure"
	"github.com/wentf9/xops-mcp/internal/storage"
	"github.com/wentf9/xops-mcp/internal/storage/archive"
	"github.com/wentf9/xops-mcp/internal/testutil/pgfixture"
	cryptoSSH "golang.org/x/crypto/ssh"
)

// Each backend response gains at least delay before reaching the client. This
// uses the real PostgreSQL protocol and production deadlines, not a SQL mock.
func latencyURL(t *testing.T, dsn string, delay time.Duration) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	port := u.Port()
	if port == "" {
		port = "5432"
	}
	address := net.JoinHostPort(u.Hostname(), port)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	var mu sync.Mutex
	var connections []net.Conn
	closing := false
	closeConn := func(c net.Conn) {
		if err := c.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Errorf("close latency fixture connection: %v", err)
		}
	}
	track := func(c net.Conn) bool {
		mu.Lock()
		defer mu.Unlock()
		if closing {
			return false
		}
		connections = append(connections, c)
		return true
	}
	var workers sync.WaitGroup
	// Closing the listener and all tracked sockets cancels Accept, Dial and both
	// copy directions. The accept worker remains counted while adding relays;
	// cleanup joins every relay before the disposable database is dropped.
	workers.Go(func() {
		for {
			client, err := listener.Accept()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					t.Errorf("accept latency fixture connection: %v", err)
				}
				return
			}
			if !track(client) {
				closeConn(client)
				return
			}
			workers.Go(func() {
				defer closeConn(client)
				upstream, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", address)
				if err != nil {
					if ctx.Err() == nil {
						t.Errorf("dial latency fixture: %v", err)
					}
					return
				}
				defer closeConn(upstream)
				if !track(upstream) {
					return
				}
				done := make(chan error, 2)
				go func() { _, err := io.Copy(upstream, client); done <- err }()
				go func() {
					_, err := io.CopyBuffer(client, &delayedReader{ctx: ctx, reader: upstream, delay: delay}, make([]byte, 256<<10))
					done <- err
				}()
				first := <-done
				closeConn(client)
				closeConn(upstream)
				second := <-done
				for _, err := range []error{first, second} {
					if err != nil && ctx.Err() == nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, syscall.ECONNRESET) && !errors.Is(err, syscall.EPIPE) {
						t.Errorf("relay latency fixture: %v", err)
					}
				}
			})
		}
	})
	t.Cleanup(func() {
		cancel()
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
		mu.Lock()
		closing = true
		active := append([]net.Conn(nil), connections...)
		mu.Unlock()
		for _, c := range active {
			closeConn(c)
		}
		workers.Wait()
	})
	u.Host = listener.Addr().String()
	return u.String()
}

type delayedReader struct {
	ctx    context.Context
	reader io.Reader
	delay  time.Duration
}

func (r *delayedReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		timer := time.NewTimer(r.delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-r.ctx.Done():
			return 0, r.ctx.Err()
		}
	}
	return n, err
}

func latencyStore(t *testing.T) (*Store, *secure.Vault) {
	t.Helper()
	dsn := latencyURL(t, pgfixture.DSN(t), 3*time.Millisecond)
	vault, err := secure.New(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "data"), dsn, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s, vault
}

func largeBackup(t *testing.T, s *Store, vault *secure.Vault, count int) storage.Backup {
	t.Helper()
	b, err := s.Export(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	b.Inventory.Revision = 1
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := cryptoSSH.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	hostKey := string(cryptoSSH.MarshalAuthorizedKey(key))
	b.Inventory.Tags["tag"] = storage.Tag{ID: "tag", Name: "tag"}
	for i := range count {
		id := fmt.Sprintf("item-%04d", i)
		for _, version := range []string{"old", "current"} {
			ciphertext, err := vault.Encrypt(b.Inventory.DomainID, id, "password", version, secure.Material{Password: []byte("synthetic-latency-password")})
			if err != nil {
				t.Fatal(err)
			}
			b.Versions = append(b.Versions, storage.CredentialVersion{ID: id, Version: version, Kind: "password", Ciphertext: ciphertext})
			if version == "current" {
				b.Inventory.Credentials[id] = storage.Credential{ID: id, Name: id, Kind: "password", Version: version, Ciphertext: ciphertext}
			}
		}
		b.Inventory.Hosts[id] = storage.Host{ID: id, Name: id, Address: "127.0.0.1", Port: 22, HostKey: hostKey}
		b.Inventory.Identities[id] = storage.Identity{ID: id, Name: id, User: "fixture", CredentialID: id}
		n := storage.Node{ID: id, Name: id, HostID: id, IdentityID: id, SudoMode: "sudo", PrivilegeCredentialID: id, Aliases: []string{"alias-" + id}, TagIDs: []string{"tag"}}
		if i > 0 {
			n.JumpIDs = []string{"item-0000"}
		}
		b.Inventory.Nodes[id] = n
		b.Inventory.Deleted["deleted-"+id] = true
		b.Audit = append(b.Audit, storage.AuditRecord{ID: int64(i + 1), Event: ports.AuditEvent{Timestamp: time.Now().UTC(), OperationID: id, NodeID: id, Tool: "xops_ssh_run", Outcome: "executed"}})
	}
	_, b.Sources, err = xops.Snapshot(b.Inventory)
	if err != nil {
		t.Fatal(err)
	}
	if err := archive.Validate(t.Context(), &b, vault); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestInventoryUnderLatency(t *testing.T) {
	for _, count := range []int{400, 4096} {
		t.Run(fmt.Sprintf("save-%d", count), func(t *testing.T) {
			s, vault := latencyStore(t)
			b := largeBackup(t, s, vault, count)
			started := time.Now()
			err := s.Save(t.Context(), 0, b.Inventory, b.Sources)
			t.Logf("save %d nodes with 3 ms response latency: %s", count, time.Since(started))
			if err != nil {
				actual, loadErr := s.Load(t.Context())
				if loadErr != nil || actual.Revision != 0 {
					t.Errorf("failed save did not roll back: revision=%d error=%v", actual.Revision, loadErr)
				}
				t.Fatalf("save supported inventory: %v", err)
			}
			// Ordinary edits still rewrite the inventory, and must retain immutable
			// versions while preserving all relations within the same fixed budget.
			v, err := s.Load(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			host := v.Hosts["item-0001"]
			host.Address = "127.0.0.2"
			v.Hosts[host.ID] = host
			v.Revision++
			_, sources, err := xops.Snapshot(v)
			if err != nil {
				t.Fatal(err)
			}
			started = time.Now()
			if err := s.Save(t.Context(), 1, v, sources); err != nil {
				t.Fatal("edit supported inventory:", err)
			}
			t.Logf("edit %d nodes with 3 ms response latency: %s", count, time.Since(started))
			actual, err := s.Load(t.Context())
			if err != nil || !reflect.DeepEqual(actual, v) {
				t.Fatalf("edit changed inventory: %v", err)
			}
		})
		t.Run(fmt.Sprintf("restore-%d", count), func(t *testing.T) {
			s, vault := latencyStore(t)
			b := largeBackup(t, s, vault, count)
			started := time.Now()
			if err := s.Restore(t.Context(), b); err != nil {
				t.Fatalf("restore supported inventory: %v", err)
			}
			t.Logf("restore %d nodes/history/audit with 3 ms response latency: %s", count, time.Since(started))
			actual, err := s.Export(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			want, err := archive.Digest(t.Context(), b, vault)
			if err != nil {
				t.Fatal(err)
			}
			got, err := archive.Digest(t.Context(), actual, vault)
			if err != nil || got != want {
				t.Fatalf("restore changed archive contents: %v", err)
			}
		})
	}
}
