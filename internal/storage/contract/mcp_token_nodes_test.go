package contract_test

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/wentf9/xops-cli/core/mcp/ports"
	"github.com/wentf9/xops-mcp/internal/importer"
	"github.com/wentf9/xops-mcp/internal/secure"
	"github.com/wentf9/xops-mcp/internal/storage"
	"github.com/wentf9/xops-mcp/internal/testutil"
)

func tokenNodes(t *testing.T, s storage.Database, vault *secure.Vault) []string {
	t.Helper()
	doc := testutil.Document(t)
	doc.Nodes["second"] = importer.Node{Host: "peer", Identity: "login"}
	v, report := testutil.Plan(t, s, vault, doc)
	save(t, s, v)
	ids := []string{report.NodeIDs["peer"], report.NodeIDs["second"]}
	slices.Sort(ids)
	return ids
}

func scopedToken() storage.MCPTokenRecord {
	return storage.MCPTokenRecord{Token: storage.MCPToken{ID: "token", ClientID: "client", Name: "test", Version: 1, CreatedAt: 1, Enabled: true}, Digest: strings.Repeat("a", 64)}
}

func TestMCPTokenNodeScopes(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s, vault := open(t, backend)
			ids := tokenNodes(t, s, vault)
			record := scopedToken()
			record.Token.NodeScope = storage.MCPTokenNodeScopeSelected
			record.Token.NodeIDs = []string{ids[1], ids[0]}
			if err := s.SaveMCPToken(t.Context(), 0, record, ports.AuditEvent{}); err != nil {
				t.Fatal(err)
			}
			if record.Token.NodeIDs[0] != ids[1] {
				t.Fatal("save mutated caller-owned node IDs")
			}
			for name, lookup := range map[string]func() (storage.MCPTokenRecord, error){
				"id": func() (storage.MCPTokenRecord, error) { return s.MCPTokenByID(t.Context(), record.Token.ID) },
				"client": func() (storage.MCPTokenRecord, error) {
					return s.MCPTokenByClientID(t.Context(), record.Token.ClientID)
				},
				"digest": func() (storage.MCPTokenRecord, error) { return s.MCPTokenByDigest(t.Context(), record.Digest) },
			} {
				got, err := lookup()
				if err != nil || got.Token.NodeScope != storage.MCPTokenNodeScopeSelected || !slices.Equal(got.Token.NodeIDs, ids) {
					t.Fatalf("%s lost restriction: %+v %v", name, got, err)
				}
			}
			// Renaming a target preserves its ID; removing another retains the
			// original restriction even through unrelated token edits.
			v := inventory(t, s)
			node := v.Nodes[ids[0]]
			node.Name = "renamed"
			v.Nodes[ids[0]] = node
			delete(v.Nodes, ids[1])
			save(t, s, v)
			record.Token.Version = 2
			record.Token.Name = "edited"
			if err := s.SaveMCPToken(t.Context(), 1, record, ports.AuditEvent{}); err != nil {
				t.Fatal("existing deleted binding prevented metadata edit", err)
			}
			before, err := s.MCPTokenByID(t.Context(), record.Token.ID)
			if err != nil || !slices.Equal(before.Token.NodeIDs, ids) {
				t.Fatal("node deletion changed token scope", err)
			}
			bad := record
			bad.Token.Version = 3
			bad.Token.NodeIDs = append(slices.Clone(ids), "missing")
			if err := s.SaveMCPToken(t.Context(), 2, bad, ports.AuditEvent{}); !errors.Is(err, storage.ErrInvalidMCPTokenScope) {
				t.Fatal("granted missing node", err)
			}
			if err := s.SaveMCPToken(t.Context(), 1, record, ports.AuditEvent{}); !errors.Is(err, storage.ErrConflict) {
				t.Fatal("stale scope edit accepted", err)
			}
			after, err := s.MCPTokenByID(t.Context(), record.Token.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("failed edit changed token", err)
			}
			record.Token.Version = 3
			record.Token.NodeIDs = ids[:1]
			if err := s.SaveMCPToken(t.Context(), 2, record, ports.AuditEvent{}); err != nil {
				t.Fatal(err)
			}
			record.Token.Version = 4
			record.Token.NodeIDs = ids
			if err := s.SaveMCPToken(t.Context(), 3, record, ports.AuditEvent{}); !errors.Is(err, storage.ErrInvalidMCPTokenScope) {
				t.Fatal("regranted deleted node", err)
			}
			record.Token.NodeScope, record.Token.NodeIDs = storage.MCPTokenNodeScopeAll, nil
			if err := s.SaveMCPToken(t.Context(), 3, record, ports.AuditEvent{}); err != nil {
				t.Fatal(err)
			}
			record.Token.Version = 5
			record.Token.NodeScope = storage.MCPTokenNodeScopeSelected
			if err := s.SaveMCPToken(t.Context(), 4, record, ports.AuditEvent{}); err != nil {
				t.Fatal(err)
			}
			got, err := s.MCPTokenByID(t.Context(), record.Token.ID)
			if err != nil || got.Token.NodeScope != storage.MCPTokenNodeScopeSelected || got.Token.NodeIDs == nil || len(got.Token.NodeIDs) != 0 {
				t.Fatal("empty selected scope became unrestricted", err)
			}
		})
	}
}

func TestMCPTokenScopeValidationAndCAS(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s, vault := open(t, backend)
			ids := tokenNodes(t, s, vault)
			for _, invalid := range []storage.MCPToken{
				{NodeScope: "unknown"},
				{NodeScope: storage.MCPTokenNodeScopeAll, NodeIDs: ids},
				{NodeScope: storage.MCPTokenNodeScopeSelected, NodeIDs: []string{""}},
				{NodeScope: storage.MCPTokenNodeScopeSelected, NodeIDs: []string{"has space"}},
				{NodeScope: storage.MCPTokenNodeScopeSelected, NodeIDs: []string{"has\x00control"}},
				{NodeScope: storage.MCPTokenNodeScopeSelected, NodeIDs: []string{strings.Repeat("x", 129)}},
				{NodeScope: storage.MCPTokenNodeScopeSelected, NodeIDs: []string{ids[0], ids[0]}},
				{NodeScope: storage.MCPTokenNodeScopeSelected, NodeIDs: make([]string, storage.MaxMCPTokenNodeIDs+1)},
				{NodeScope: storage.MCPTokenNodeScopeSelected, NodeIDs: []string{"missing"}},
			} {
				record := scopedToken()
				record.Token.NodeScope, record.Token.NodeIDs = invalid.NodeScope, invalid.NodeIDs
				if err := s.SaveMCPToken(t.Context(), 0, record, ports.AuditEvent{}); !errors.Is(err, storage.ErrInvalidMCPTokenScope) {
					t.Fatalf("invalid scope accepted: %+v %v", invalid, err)
				}
			}
			record := scopedToken()
			if err := s.SaveMCPToken(t.Context(), 0, record, ports.AuditEvent{}); err != nil {
				t.Fatal(err)
			}
			got, err := s.MCPTokenByID(t.Context(), record.Token.ID)
			if err != nil || got.Token.NodeScope != storage.MCPTokenNodeScopeAll || got.Token.NodeIDs == nil || len(got.Token.NodeIDs) != 0 {
				t.Fatal("legacy create did not normalize to all", err)
			}
			begin := make(chan struct{})
			results := make(chan error, 2)
			var workers sync.WaitGroup
			for _, id := range ids {
				workers.Go(func() {
					next := record
					next.Token.Version = 2
					next.Token.NodeScope, next.Token.NodeIDs = storage.MCPTokenNodeScopeSelected, []string{id}
					<-begin
					results <- s.SaveMCPToken(t.Context(), 1, next, ports.AuditEvent{})
				})
			}
			close(begin)
			workers.Wait()
			close(results)
			ok, conflicts := 0, 0
			for err := range results {
				switch {
				case err == nil:
					ok++
				case errors.Is(err, storage.ErrConflict):
					conflicts++
				default:
					t.Fatal(err)
				}
			}
			if ok != 1 || conflicts != 1 {
				t.Fatalf("scope CAS: successes=%d conflicts=%d", ok, conflicts)
			}
			duplicate := scopedToken()
			duplicate.Token.ID, duplicate.Digest = "other", strings.Repeat("b", 64)
			if err := s.SaveMCPToken(t.Context(), 0, duplicate, ports.AuditEvent{}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.MCPTokenByClientID(t.Context(), record.Token.ClientID); !errors.Is(err, storage.ErrNotFound) {
				t.Fatal("ambiguous client lookup did not fail closed", err)
			}
		})
	}
}
