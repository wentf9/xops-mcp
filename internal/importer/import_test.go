package importer_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/wentf9/xops-cli/core/ssh"
	"github.com/wentf9/xops-mcp/internal/adapters/xops"
	"github.com/wentf9/xops-mcp/internal/importer"
	"github.com/wentf9/xops-mcp/internal/service"
	"github.com/wentf9/xops-mcp/internal/testutil"
)

func TestCLIJumpAliasAndExplicitChainRoundTrip(t *testing.T) {
	for _, jump := range []string{"first", " jump1 , second "} {
		t.Run(jump, func(t *testing.T) {
			data := `schema_version: 2
hosts: {host: {address: 127.0.0.1, port: 22}}
identities: {login: {user: fixture, auth_type: password}}
nodes:
  direct: {host_ref: host, identity_ref: login}
  jump1: {host_ref: host, identity_ref: login, alias: [first], proxy_jump: direct}
  jump2: {host_ref: host, identity_ref: login, alias: [second]}
  target: {host_ref: host, identity_ref: login, proxy_jump: "` + jump + `"}
`
			doc, _, err := importer.Decode([]byte(data), "xops-cli")
			if err != nil {
				t.Fatal(err)
			}
			s, vault, _ := testutil.Store(t)
			candidate, report := testutil.Plan(t, s, vault, doc)
			svc, err := service.New(t.Context(), s, func(ctx context.Context, _ []ssh.ConnectionPlan) error { return ctx.Err() })
			if err != nil {
				t.Fatal(err)
			}
			defer testutil.Close(t, svc)
			if _, err := svc.Apply(t.Context(), 0, candidate); err != nil {
				t.Fatal(err)
			}
			loaded, err := s.Load(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			view, _, err := xops.Snapshot(loaded)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{report.NodeIDs["direct"], report.NodeIDs["jump1"], report.NodeIDs["target"]}
			if strings.Contains(jump, ",") {
				want = []string{report.NodeIDs["jump1"], report.NodeIDs["jump2"], report.NodeIDs["target"]}
			}
			var got []string
			for _, hop := range view.Targets[report.NodeIDs["target"]].Plan.Hops {
				got = append(got, hop.NodeID)
			}
			if !slices.Equal(got, want) {
				t.Fatalf("jump plan = %v, want %v", got, want)
			}
			if hops := view.Targets[report.NodeIDs["jump1"]].Plan.Hops; len(hops) != 2 || hops[0].NodeID != report.NodeIDs["direct"] {
				t.Fatal("explicit route changed the shared jump's standalone route")
			}
			if len(view.Targets[report.NodeIDs["jump2"]].Plan.Hops) != 1 {
				t.Fatal("explicit route changed a standalone direct node")
			}
		})
	}
}

func TestCLIJumpConflictsAndCyclesAreRejected(t *testing.T) {
	for _, jump := range []string{"missing", "target", "jump1,target", "jump1,first", "ambiguous"} {
		t.Run(jump, func(t *testing.T) {
			data := `schema_version: 2
hosts: {host: {address: 127.0.0.1}}
identities: {login: {user: fixture, auth_type: password}}
nodes:
  jump1: {host_ref: host, identity_ref: login, alias: [first, ambiguous]}
  jump2: {host_ref: host, identity_ref: login, alias: [ambiguous]}
  target: {host_ref: host, identity_ref: login, proxy_jump: "` + jump + `"}
`
			if jump != "ambiguous" {
				data = strings.Replace(data, "alias: [ambiguous]", "alias: [second]", 1)
			}
			doc, _, err := importer.Decode([]byte(data), "xops-cli")
			if err == nil {
				s, vault, _ := testutil.Store(t)
				base, loadErr := s.Load(t.Context())
				if loadErr != nil {
					t.Fatal(loadErr)
				}
				_, _, err = importer.Plan(base, doc, vault, importer.Options{})
			}
			if err == nil {
				t.Fatal("invalid chain imported")
			}
		})
	}
}

func TestPreviewStableMappingNoWritesAndConflicts(t *testing.T) {
	s, vault, _ := testutil.Store(t)
	doc := testutil.Document(t)
	base, err := s.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, report, err := importer.Plan(base, doc, vault, importer.Options{})
	if err == nil || len(report.Conflicts) == 0 {
		t.Fatal("secret import lacked explicit authorization")
	}
	_, first, err := importer.Plan(base, doc, vault, importer.Options{IncludeSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := importer.Plan(base, doc, vault, importer.Options{IncludeSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	if first.NodeIDs["peer"] == "peer" || first.NodeIDs["peer"] != second.NodeIDs["peer"] {
		t.Fatal("preview mapping is not stable and opaque")
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), doc.Credentials["login"].Password) {
		t.Fatal("preview disclosed credential")
	}
	unchanged, err := s.Load(t.Context())
	if err != nil || unchanged.Revision != 0 || len(unchanged.Nodes) != 0 {
		t.Fatalf("preview wrote database: %v", err)
	}
	n := doc.Nodes["peer"]
	n.ProxyJump = "peer"
	doc.Nodes["peer"] = n
	if _, report, err := importer.Plan(base, doc, vault, importer.Options{IncludeSecrets: true}); err == nil || len(report.Conflicts) == 0 {
		t.Fatal("cyclic jump was accepted")
	}
	n.ProxyJump = ""
	doc.Nodes["peer"] = n
	doc.Nodes["other"] = n
	if _, _, err := importer.Plan(base, doc, vault, importer.Options{IncludeSecrets: true}); err == nil {
		t.Fatal("alias conflict was accepted")
	}
}

func TestCLIImportDisablesUnresolvedNodesAndNeverReadsPaths(t *testing.T) {
	doc, warnings, err := importer.Decode([]byte(`schema_version: 2
credential:
  stores:
    personal: {type: helper, command: must-not-run}
hosts:
  remote: {address: 127.0.0.1, port: 22}
identities:
  login:
    user: fixture
    auth_type: key
    key_path: /must/not/read
    login_password_ref: {store: personal, item: never-read}
nodes:
  remote: {host_ref: remote, identity_ref: login, alias: [r], sudo_mode: auto}
`), "xops-cli")
	if err != nil || len(warnings) == 0 {
		t.Fatalf("decode CLI: %v", err)
	}
	s, vault, _ := testutil.Store(t)
	base, err := s.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	candidate, report, err := importer.Plan(base, doc, vault, importer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	n := candidate.Nodes[report.NodeIDs["remote"]]
	if !n.Disabled || n.SudoMode != "none" || len(candidate.Credentials) != 0 || report.NodeIDs["r"] != n.ID {
		t.Fatal("unsafe or unmapped CLI import")
	}
	for _, input := range []string{"version: 1\nunknown: value\n", "version: 1\n---\nversion: 1", "version: 4", "credentials: {a: {password: synthetic-secret, kind: []}}"} {
		if _, _, err := importer.Decode([]byte(input), "server"); err == nil || strings.Contains(err.Error(), "synthetic-secret") {
			t.Fatalf("invalid input diagnostic: %v", err)
		}
	}
}
