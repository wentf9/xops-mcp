package sqlite

import (
	"reflect"
	"testing"

	"github.com/wentf9/xops-mcp/internal/adapters/xops"
	"github.com/wentf9/xops-mcp/internal/storage"
)

func restoreLegacyTagSchema(t *testing.T, s *Store) {
	t.Helper()
	_, err := s.db.ExecContext(t.Context(), `DROP TABLE mcp_tokens; DROP TABLE node_tags; DROP TABLE tags;
CREATE TABLE tags (name TEXT PRIMARY KEY);
CREATE TABLE node_tags (node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
 tag TEXT NOT NULL REFERENCES tags(name), PRIMARY KEY(node_id,tag));`)
	if err != nil {
		t.Fatal(err)
	}
}

func TestV4TagMigrationPreservesIndependentRecordsAndRelations(t *testing.T) {
	s, vault, dir := openTest(t)
	restoreLegacyTagSchema(t, s)
	_, err := s.db.ExecContext(t.Context(), `
INSERT INTO hosts VALUES ('host','host','127.0.0.1',22,'');
INSERT INTO identities VALUES ('identity','identity','fixture',NULL);
INSERT INTO nodes VALUES ('node','node','host','identity',NULL,'none',NULL,1);
INSERT INTO tags VALUES ('staging'),('unused'),('迁移标签');
INSERT INTO node_tags VALUES ('node','staging');
UPDATE deployment SET revision=9;
PRAGMA user_version=4;`)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(t.Context(), dir, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := upgraded.Close(); err != nil {
			t.Error(err)
		}
	}()
	v, err := upgraded.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if v.Revision != 9 || len(v.Tags) != 3 || len(v.Nodes["node"].TagIDs) != 1 {
		t.Fatalf("lost tag records/relations: %+v", v)
	}
	if _, _, err := xops.Snapshot(v); err != nil {
		t.Fatalf("migrated inventory is invalid: %v", err)
	}
	id := v.Nodes["node"].TagIDs[0]
	if len(id) != 32 || v.Tags[id].Name != "staging" {
		t.Fatal("tag was not migrated to an opaque primary key")
	}
	v.Tags[id] = storage.Tag{ID: id, Name: "production"}
	v.Revision++
	if err := upgraded.Save(t.Context(), 9, v, nil); err != nil {
		t.Fatal(err)
	}
	reloaded, err := upgraded.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reloaded.Tags, v.Tags) || !reflect.DeepEqual(reloaded.Nodes["node"].TagIDs, []string{id}) {
		t.Fatal("rename or save changed tag identity/association")
	}
	broken := reloaded.Clone()
	node := broken.Nodes["node"]
	node.TagIDs = []string{"missing"}
	broken.Nodes["node"] = node
	broken.Revision++
	if err := upgraded.Save(t.Context(), reloaded.Revision, broken, nil); err == nil {
		t.Fatal("missing tag reference bypassed database foreign key")
	}
	if err := upgraded.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), dir, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	}()
	actual, err := reopened.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if actual.Revision != reloaded.Revision || !reflect.DeepEqual(actual.Tags, reloaded.Tags) || actual.Nodes["node"].TagIDs[0] != id {
		t.Fatal("restart changed migrated tag identities or committed a failed relation")
	}
}
