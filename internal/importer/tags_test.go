package importer_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/wentf9/xops-cli/core/ssh"
	"github.com/wentf9/xops-mcp/internal/importer"
	"github.com/wentf9/xops-mcp/internal/service"
	"github.com/wentf9/xops-mcp/internal/testutil"
)

func TestImportResolvesTagNamesAndPreservesUnusedRecords(t *testing.T) {
	s, vault, _ := testutil.Store(t)
	base, err := s.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	doc := testutil.Document(t)
	doc.Tags = []string{"unused", "fixture"}
	opts := importer.Options{IncludeSecrets: true}
	first, report, err := importer.Plan(base, doc, vault, opts)
	if err != nil {
		t.Fatal(err)
	}
	preview, previewReport, err := importer.Plan(base, doc, vault, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Tags, preview.Tags) || !reflect.DeepEqual(report.TagIDs, previewReport.TagIDs) || len(report.TagIDs) != 2 {
		t.Fatal("preview/apply tag identities differ")
	}
	node := first.Nodes[report.NodeIDs["peer"]]
	if !reflect.DeepEqual(node.TagIDs, []string{report.TagIDs["fixture"]}) {
		t.Fatal("node tag name was not resolved to a primary key")
	}
	svc, err := service.New(t.Context(), s, func(ctx context.Context, _ []ssh.ConnectionPlan) error { return ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, svc)
	if _, err := svc.Apply(t.Context(), base.Revision, first); err != nil {
		t.Fatal(err)
	}
	base, err = s.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	replaced, _, err := importer.Plan(base, importer.Document{Version: 1}, vault, importer.Options{Replace: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(replaced.Nodes) != 0 || !reflect.DeepEqual(replaced.Tags, base.Tags) {
		t.Fatal("node replacement removed independent tags")
	}
	if _, err := svc.Apply(t.Context(), base.Revision, replaced); err != nil {
		t.Fatal(err)
	}
	base, err = s.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, again, err := importer.Plan(base, doc, vault, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again.TagIDs, report.TagIDs) {
		t.Fatal("reimport replaced existing tag IDs")
	}
}
