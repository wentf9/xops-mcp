package server_test

import (
	"context"
	"maps"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wentf9/xops-cli/core/testutil/sshfixture"
	"github.com/wentf9/xops-mcp/internal/service"
	"github.com/wentf9/xops-mcp/internal/testutil"
)

func TestResourceNamesAndAliasesRejectInvalidInput(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	p := newAdminPeer(t, ctx, adminConfig(t))
	p.initialize()
	host := p.save("hosts", service.HostInput{Name: "主机_01", Address: "127.0.0.1", Port: 22})
	credential := p.save("credentials", service.CredentialInput{Name: "ключ-01", Kind: "password", Secret: &service.MaterialInput{Password: "synthetic-password"}})
	identity := p.save("identities", service.IdentityInput{Name: "身份_01", User: "fixture", CredentialID: credential})
	node := p.save("nodes", service.NodeInput{Name: "节点-01", HostID: host, IdentityID: identity, Disabled: true, Aliases: []string{"运维_生产", "e\u0301cole"}})
	tag := p.save("tags", service.TagInput{Name: "हिन्दी"})
	for _, resource := range []struct {
		kind, id string
		body     map[string]any
	}{
		{"hosts", host, map[string]any{"address": "127.0.0.1", "port": 22}},
		{"credentials", credential, map[string]any{"kind": "password", "secret": service.MaterialInput{Password: "synthetic-password"}}},
		{"identities", identity, map[string]any{"user": "fixture", "credentialID": credential}},
		{"nodes", node, map[string]any{"hostID": host, "identityID": identity, "disabled": true}},
		{"tags", tag, map[string]any{}},
	} {
		before := p.etag
		for _, value := range []string{"", " leading", "trailing ", "inner space", "a\tb", "a\nb", "ops,prod", "node.name", "a/b", "a\\b", "a@b", "<script>", "node🙂", "a\u200b", "a\u034f", "\u0301a"} {
			body := maps.Clone(resource.body)
			body["name"] = value
			p.request("POST", "/api/v1/"+resource.kind, body, 422, nil)
			p.request("PUT", "/api/v1/"+resource.kind+"/"+resource.id, body, 422, nil)
		}
		p.request("GET", "/api/v1/inventory", nil, 200, nil)
		if p.etag != before {
			t.Fatal("rejected names changed inventory revision")
		}
		body := maps.Clone(resource.body)
		body["name"] = "更新_" + resource.kind + "-α"
		p.request("PUT", "/api/v1/"+resource.kind+"/"+resource.id, body, 200, nil)
		p.request("POST", "/api/v1/"+resource.kind, body, 422, nil)
	}
	before := p.etag
	for _, value := range []string{"", " leading", "trailing ", "inner space", "ops,prod", "a.b", "a\nb", "a\tb", "node🙂", "a\u200d", "\u0301a"} {
		input := service.NodeInput{Name: "更新_nodes-α", HostID: host, IdentityID: identity, Disabled: true, Aliases: []string{value}}
		p.request("PUT", "/api/v1/nodes/"+node, input, 422, nil)
		input.Name = "待创建节点"
		p.request("POST", "/api/v1/nodes", input, 422, nil)
	}
	p.request("GET", "/api/v1/inventory", nil, 200, nil)
	if p.etag != before {
		t.Fatal("rejected aliases changed inventory revision")
	}
}

func TestUnicodeNamesAndAliasesReachSSH(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	peer, err := sshfixture.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, peer)
	cfg := adminConfig(t)
	p := newAdminPeer(t, ctx, cfg)
	p.initialize()
	doc := fixtureDocument(t, peer)
	input := doc.Nodes["peer"]
	input.Aliases = []string{"ops-prod", "运维_生产", "e\u0301cole", "हिन्दी", "مرحبا-١٢٣", "Ελλάδα"}
	doc.Nodes["peer"] = input
	v, report := testutil.Plan(t, p.app.Host.Store, p.app.Host.Vault, doc)
	if _, err := p.app.Host.Service.Apply(ctx, 0, v); err != nil {
		t.Fatal(err)
	}
	p.request("GET", "/api/v1/inventory", nil, 200, nil)
	node := v.Nodes[report.NodeIDs["peer"]]
	edit := service.NodeInput{Name: "服务器_01", HostID: node.HostID, IdentityID: node.IdentityID, JumpIDs: node.JumpIDs, Aliases: slices.Clone(node.Aliases), TagIDs: node.TagIDs, Disabled: node.Disabled, SudoMode: node.SudoMode, PrivilegeCredentialID: node.PrivilegeCredentialID}
	p.request("PUT", "/api/v1/nodes/"+node.ID, edit, 200, nil)
	actual, err := p.app.Host.Store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(actual.Nodes[node.ID].Aliases, node.Aliases) {
		t.Fatal("valid multilingual aliases were rewritten")
	}
	options, err := cfg.HTTPOptions()
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "unicode-names", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: p.mcp.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{p.transport, options.Token}, Timeout: 10 * time.Second}, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, session)
	r := &running{host: p.app.Host, session: session, http: p.mcp}
	for _, alias := range input.Aliases {
		call(t, ctx, r, "xops_ssh_run", map[string]string{"nodeID": alias, "command": "hostname"}, nil)
	}
	if peer.Executed.Load() != int32(len(input.Aliases)) {
		t.Fatal("multilingual selectors did not reach SSH execution")
	}
}
