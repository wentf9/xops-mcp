package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/wentf9/xops-mcp/internal/service"
)

func TestAdministratorLanguages(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	p := newAdminPeer(t, ctx, adminConfig(t))
	p.initialize()
	tagID := p.save("tags", service.TagInput{Name: "标签"})
	for _, tc := range []struct{ language, response, login, invalidKey string }{
		{"en-US,en;q=0.9,zh;q=0.5", "en", "Sign in first", "Invalid or unsupported private key format"},
		{"zh-TW,en;q=0.5", "zh-CN", "请先登录", "私钥格式不正确，无法解析"},
		{"", "zh-CN", "请先登录", "私钥格式不正确，无法解析"},
	} {
		t.Run(tc.response+tc.language, func(t *testing.T) {
			headers := func(r *http.Request) { r.Header.Set("Accept-Language", tc.language) }
			data := p.request("POST", "/api/v1/credentials/validate-key", map[string]string{"privateKey": "invalid"}, 200, headers)
			var validation struct {
				Valid   bool   `json:"valid"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(data, &validation); err != nil {
				t.Fatal(err)
			}
			if validation.Valid || validation.Message != tc.invalidKey {
				t.Fatalf("validation: %+v", validation)
			}
			if got := p.lastHeader.Get("Content-Language"); got != tc.response {
				t.Fatalf("Content-Language=%q", got)
			}
			if !strings.Contains(p.lastHeader.Get("Vary"), "Accept-Language") {
				t.Fatal("missing language Vary")
			}
			data = p.request("GET", "/api/v1/inventory", nil, 200, headers)
			var inventory struct {
				Tags []struct{ ID, Name string } `json:"tags"`
			}
			if err := json.Unmarshal(data, &inventory); err != nil {
				t.Fatal(err)
			}
			if len(inventory.Tags) != 1 || inventory.Tags[0].ID != tagID || inventory.Tags[0].Name != "标签" {
				t.Fatalf("business data translated: %+v", inventory.Tags)
			}
			data = p.request("GET", "/api/v1/inventory", nil, 401, func(r *http.Request) { headers(r); r.Header.Del("Authorization") })
			var problem struct{ Code, Message string }
			if err := json.Unmarshal(data, &problem); err != nil {
				t.Fatal(err)
			}
			if problem.Code != "login_required" || problem.Message != tc.login {
				t.Fatalf("problem=%+v", problem)
			}
		})
	}
}
