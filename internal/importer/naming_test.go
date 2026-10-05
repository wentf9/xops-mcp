package importer_test

import (
	"errors"
	"testing"

	"github.com/wentf9/xops-mcp/internal/importer"
	"github.com/wentf9/xops-mcp/internal/naming"
	"github.com/wentf9/xops-mcp/internal/testutil"
	"gopkg.in/yaml.v3"
)

func TestImportsValidateNamesAndAliases(t *testing.T) {
	s, vault, _ := testutil.Store(t)
	base, err := s.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"hosts", "identities", "credentials", "nodes", "aliases", "tags", "node-tags"} {
		for _, value := range []string{" legacy ", "a b", "ops,prod", "a.b", "a/b", "emoji🙂", "a\u200b"} {
			doc := testutil.Document(t)
			node := doc.Nodes["peer"]
			switch kind {
			case "hosts":
				doc.Hosts[value] = doc.Hosts["peer"]
				delete(doc.Hosts, "peer")
				node.Host = value
			case "identities":
				doc.Identities[value] = doc.Identities["login"]
				delete(doc.Identities, "login")
				node.Identity = value
			case "credentials":
				doc.Credentials[value] = doc.Credentials["login"]
				delete(doc.Credentials, "login")
				i := doc.Identities["login"]
				i.Credential = value
				doc.Identities["login"] = i
			case "aliases":
				node.Aliases = []string{value}
			case "tags":
				doc.Tags = []string{value}
			case "node-tags":
				node.Tags = []string{value}
			}
			doc.Nodes["peer"] = node
			if kind == "nodes" {
				doc.Nodes[value] = node
				delete(doc.Nodes, "peer")
			}
			data, err := yaml.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			decoded, _, err := importer.Decode(data, "server")
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := importer.Plan(base, decoded, vault, importer.Options{IncludeSecrets: true}); !errors.Is(err, naming.ErrInvalid) {
				t.Fatalf("%s %q: wanted naming rejection, got %v", kind, value, err)
			}
		}
	}
	for _, value := range []string{" old-alias ", "ops,prod", "a/b", "中文,别名"} {
		data, err := yaml.Marshal(map[string]any{
			"schema_version": 2,
			"hosts":          map[string]any{"host": map[string]any{"address": "127.0.0.1"}},
			"identities":     map[string]any{"identity": map[string]any{"user": "fixture", "auth_type": "password"}},
			"nodes":          map[string]any{"node": map[string]any{"host_ref": "host", "identity_ref": "identity", "alias": []string{value}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		doc, _, err := importer.Decode(data, "xops-cli")
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := importer.Plan(base, doc, vault, importer.Options{}); !errors.Is(err, naming.ErrInvalid) {
			t.Fatalf("CLI alias %q bypassed validation: %v", value, err)
		}
	}
	after, err := s.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != base.Revision || len(after.Nodes) != 0 {
		t.Fatal("rejected imports changed inventory")
	}
}
