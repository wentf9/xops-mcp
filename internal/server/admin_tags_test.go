package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/wentf9/xops-mcp/internal/adapters/xops"
	"github.com/wentf9/xops-mcp/internal/service"
	"github.com/wentf9/xops-mcp/internal/testutil"
)

func TestIndependentTagsUseStableIDs(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	cfg := adminConfig(t)
	p := newAdminPeer(t, ctx, cfg)
	p.initialize()
	tagID := p.save("tags", service.TagInput{Name: "staging"})
	unusedID := p.save("tags", service.TagInput{Name: "unused"})
	check := func(peer *adminPeer, want map[string]int) {
		t.Helper()
		data := peer.request("GET", "/api/v1/inventory", nil, 200, nil)
		var inventory struct {
			Tags []struct {
				ID, Name string
				Count    int
			}
		}
		if err := json.Unmarshal(data, &inventory); err != nil {
			t.Fatal(err)
		}
		got := map[string]int{}
		for _, tag := range inventory.Tags {
			got[tag.ID] = tag.Count
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("tag records/counts = %v, want %v", got, want)
		}
	}
	check(p, map[string]int{tagID: 0, unusedID: 0})
	p.request("POST", "/api/v1/tags", service.TagInput{Name: "staging"}, 422, nil)
	p.request("POST", "/api/v1/tags", service.TagInput{Name: "   "}, 422, nil)
	p.request("PUT", "/api/v1/tags/"+unusedID, service.TagInput{Name: "staging"}, 422, nil)
	p.request("PUT", "/api/v1/tags/missing", service.TagInput{Name: "other"}, 404, nil)
	hostID := p.save("hosts", service.HostInput{Name: "host", Address: "127.0.0.1", Port: 22})
	identityID := p.save("identities", service.IdentityInput{Name: "identity", User: "fixture"})
	input := service.NodeInput{Name: "node", HostID: hostID, IdentityID: identityID, Disabled: true, TagIDs: []string{tagID}}
	nodeID := p.save("nodes", input)
	check(p, map[string]int{tagID: 1, unusedID: 0})
	old := p.etag
	p.request("PUT", "/api/v1/tags/"+tagID, service.TagInput{Name: "production"}, 200, nil)
	v, err := p.app.Host.Store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v.Tags[tagID].Name != "production" || !reflect.DeepEqual(v.Nodes[nodeID].TagIDs, []string{tagID}) {
		t.Fatal("rename changed the tag primary key or node link")
	}
	view, _, err := xops.Snapshot(v)
	if err != nil || !reflect.DeepEqual(view.Targets[nodeID].Info.Tags, []string{"production"}) {
		t.Fatalf("shared core did not receive current tag names: %v", err)
	}
	p.request("PUT", "/api/v1/tags/"+tagID, service.TagInput{Name: "stale"}, 412, func(r *http.Request) { r.Header.Set("If-Match", old) })
	input.TagIDs = []string{"missing"}
	p.request("PUT", "/api/v1/nodes/"+nodeID, input, 422, nil)
	p.request("DELETE", "/api/v1/tags/"+tagID, nil, 200, nil)
	check(p, map[string]int{unusedID: 0})
	v, err = p.app.Host.Store.Load(ctx)
	if err != nil || len(v.Nodes[nodeID].TagIDs) != 0 {
		t.Fatal("tag deletion did not detach its node relation")
	}
	recreatedID := p.save("tags", service.TagInput{Name: "production"})
	if recreatedID == tagID {
		t.Fatal("recreated tag reused deleted identity")
	}
	testutil.Close(t, p.app)
	p.http.Close()
	p.mcp.Close()
	next := newAdminPeer(t, ctx, cfg)
	next.login(adminPassword)
	check(next, map[string]int{unusedID: 0, recreatedID: 0})
}
