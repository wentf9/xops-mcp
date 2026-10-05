package config_test

import (
	"path/filepath"
	"testing"

	"github.com/wentf9/xops-mcp/internal/config"
)

func TestDeploymentExamplesUseSeparateListeners(t *testing.T) {
	for _, name := range []string{"server.yaml", "deployment/container.yaml", "deployment/systemd.yaml"} {
		t.Run(name, func(t *testing.T) {
			cfg, err := config.Load(filepath.Join("..", "..", "examples", name))
			if err != nil {
				t.Fatal(err)
			}
			web, err := cfg.WebOptions()
			if err != nil || web.Listen == cfg.Listen || web.PublicURL == "" {
				t.Fatalf("invalid deployment listeners: %+v, %v", web, err)
			}
		})
	}
}

func TestWebPrefixAndIndependentListenerConfiguration(t *testing.T) {
	for input, want := range map[string]string{"": "", "/": "", "/ops": "/ops", "/ops/": "/ops", "/platform/xops-v1": "/platform/xops-v1"} {
		got, err := config.NormalizeWebBasePath(input)
		if err != nil || got != want {
			t.Fatalf("prefix %q: %q, %v", input, got, err)
		}
	}
	for _, input := range []string{"ops", "//ops", "/a//b", "/a/../b", "/a/./b", "/a%2fb", "/a?x", "/a;b", "/a\\b", "/a\n", "/a/*", "/a//"} {
		if _, err := config.NormalizeWebBasePath(input); err == nil {
			t.Fatalf("accepted unsafe prefix %q", input)
		}
	}
	c := config.Config{Listen: "127.0.0.1:8080", PublicURL: "https://mcp.example.test"}
	w, err := c.WebOptions()
	if err != nil || w.Listen != "127.0.0.1:8081" || w.PublicURL != "http://127.0.0.1:8081" {
		t.Fatalf("admin defaults inherited MCP exposure: %+v, %v", w, err)
	}
	c.WebListen, c.WebPublicURL, c.WebBasePath = "0.0.0.0:8081", "https://admin.example.test", "/ops/"
	w, err = c.WebOptions()
	if err != nil || w.BasePath != "/ops" || w.PublicURL != c.WebPublicURL {
		t.Fatalf("web configuration: %+v, %v", w, err)
	}
	c.WebPublicURL = "https://admin.example.test/ops"
	if _, err := c.WebOptions(); err == nil {
		t.Fatal("accepted conflicting public URL path")
	}
	c.WebPublicURL = ""
	if _, err := c.WebOptions(); err == nil {
		t.Fatal("wildcard listener has no usable public origin")
	}
	c.WebListen = c.Listen
	if _, err := c.WebOptions(); err == nil {
		t.Fatal("allowed shared listener")
	}
}
