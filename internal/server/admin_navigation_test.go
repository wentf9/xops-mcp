package server_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/wentf9/xops-mcp/internal/service"
)

func TestConsoleCrossSiteNavigation(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	p := newAdminPeer(t, ctx, adminConfig(t))
	navigate := func(r *http.Request) {
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		r.Header.Set("Sec-Fetch-Mode", "navigate")
		r.Header.Set("Sec-Fetch-Dest", "document")
	}
	root := p.request("GET", "/", nil, 200, navigate)
	if !strings.Contains(string(root), "/assets/app.js") || !strings.Contains(p.lastHeader.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatal("navigation did not serve the protected console document")
	}
	p.request("HEAD", "/", nil, 200, func(r *http.Request) {
		navigate(r)
		r.Header.Del("If-Match")
	})
	for _, path := range []string{"/api/v1/auth/session", "/api/v1/inventory", "/assets/app.js"} {
		p.request("GET", path, nil, 403, navigate)
	}
	p.request("GET", "/", nil, 403, func(r *http.Request) {
		navigate(r)
		r.Host = "untrusted.example"
	})
	p.request("GET", "/", nil, 403, func(r *http.Request) {
		navigate(r)
		r.Header.Set("Origin", "https://untrusted.example")
	})
	p.request("GET", "/", nil, 403, func(r *http.Request) {
		navigate(r)
		r.Header.Add("Origin", p.http.URL)
		r.Header.Add("Origin", p.http.URL)
	})
	for _, destination := range []string{"iframe", "empty", ""} {
		p.request("GET", "/", nil, 403, func(r *http.Request) {
			navigate(r)
			r.Header.Set("Sec-Fetch-Dest", destination)
		})
	}
	p.request("GET", "/", nil, 403, func(r *http.Request) {
		navigate(r)
		r.Header.Set("Sec-Fetch-Mode", "cors")
	})
	p.request("POST", "/", map[string]any{}, 403, navigate)
	p.initialize()
	input := service.HostInput{Name: "denied", Address: "127.0.0.1", Port: 22}
	p.request("POST", "/api/v1/hosts", input, 403, navigate)
	p.request("POST", "/api/v1/hosts", input, 403, func(r *http.Request) { r.Header.Del("X-CSRF-Token") })
	p.request("POST", "/api/v1/hosts", input, 403, func(r *http.Request) { r.Header.Del("Origin") })
	p.request("POST", "/api/v1/hosts", input, 403, func(r *http.Request) { r.Header.Set("Origin", "https://untrusted.example") })
	p.request("GET", "/api/v1/inventory", nil, 403, navigate)
}
