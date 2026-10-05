package importer_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/wentf9/xops-cli/core/ssh"
	"github.com/wentf9/xops-mcp/internal/importer"
	"github.com/wentf9/xops-mcp/internal/naming"
	"github.com/wentf9/xops-mcp/internal/service"
	"github.com/wentf9/xops-mcp/internal/testutil"
	"gopkg.in/yaml.v3"
)

func TestCLIInlineCredentialsHaveValidStableNamesAndReferences(t *testing.T) {
	s, vault, _ := testutil.Store(t)
	base, err := s.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"login", "privilege", "中文身份", strings.Repeat("a", 256), strings.Repeat("a", 255) + "b", strings.Repeat("中", 85)}
	identities, nodes := map[string]any{}, map[string]any{}
	for index, name := range names {
		identities[name] = map[string]any{"user": "fixture", "auth_type": "password", "password": fmt.Sprintf("synthetic-login-%d", index)}
		nodes[name] = map[string]any{"host_ref": "host", "identity_ref": name, "sudo_mode": "su", "su_pwd": fmt.Sprintf("synthetic-privilege-%d", index)}
	}
	data, err := yaml.Marshal(map[string]any{"schema_version": 1, "hosts": map[string]any{"host": map[string]any{"address": "127.0.0.1"}}, "identities": identities, "nodes": nodes})
	if err != nil {
		t.Fatal(err)
	}
	doc, _, err := importer.Decode(data, "xops-cli")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := importer.Plan(base, doc, vault, importer.Options{}); err == nil {
		t.Fatal("inline credentials imported without explicit authorization")
	}
	if len(doc.Credentials) != 2*len(names) {
		t.Fatal("generated credential names collided")
	}
	for name := range doc.Credentials {
		if err := naming.Validate(name); err != nil {
			t.Fatalf("generated name %q violates the product contract: %v", name, err)
		}
	}
	first, report, err := importer.Plan(base, doc, vault, importer.Options{IncludeSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	again, _, err := importer.Decode(data, "xops-cli")
	if err != nil {
		t.Fatal(err)
	}
	preview, _, err := importer.Plan(base, again, vault, importer.Options{IncludeSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if doc.Identities[name].Credential != again.Identities[name].Credential || doc.Nodes[name].PrivilegeCredential != again.Nodes[name].PrivilegeCredential {
			t.Fatal("decode changed a generated reference")
		}
	}
	for id, credential := range first.Credentials {
		if preview.Credentials[id].Name != credential.Name {
			t.Fatal("preview changed credential identity")
		}
	}
	svc, err := service.New(t.Context(), s, func(ctx context.Context, _ []ssh.ConnectionPlan) error { return ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, svc)
	if _, err := svc.Apply(t.Context(), base.Revision, first); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for index, name := range names {
		node := loaded.Nodes[report.NodeIDs[name]]
		loginID := loaded.Identities[node.IdentityID].CredentialID
		if !node.Disabled || loginID == node.PrivilegeCredentialID {
			t.Fatal("credential roles lost their separate bindings")
		}
		for id, want := range map[string]string{loginID: fmt.Sprintf("synthetic-login-%d", index), node.PrivilegeCredentialID: fmt.Sprintf("synthetic-privilege-%d", index)} {
			credential := loaded.Credentials[id]
			material, err := vault.Decrypt(loaded.DomainID, id, credential.Kind, credential.Version, credential.Ciphertext)
			if err != nil {
				t.Fatal(err)
			}
			matches := string(material.Password) == want
			material.Clear()
			if !matches {
				t.Fatal("generated credential reference points to the wrong secret")
			}
		}
	}
	reimported, _, err := importer.Plan(loaded, again, vault, importer.Options{IncludeSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(reimported.Credentials) != len(loaded.Credentials) {
		t.Fatal("reimport duplicated generated credentials")
	}
	for id, credential := range loaded.Credentials {
		if reimported.Credentials[id].Name != credential.Name {
			t.Fatal("reimport changed credential identity")
		}
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "synthetic-login") || strings.Contains(string(encoded), "synthetic-privilege") {
		t.Fatal("import report disclosed secret material")
	}
}
