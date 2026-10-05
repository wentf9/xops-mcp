package server_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"strings"
	"testing"
	"time"

	"github.com/wentf9/xops-mcp/internal/service"
	"github.com/wentf9/xops-mcp/internal/testutil"
)

func TestAdminPrefixAssetsJWTAndPortIsolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	cfg := adminConfig(t)
	cfg.WebBasePath = "/platform/xops/"
	p := newAdminPeer(t, ctx, cfg)
	navigate := func(r *http.Request) {
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		r.Header.Set("Sec-Fetch-Mode", "navigate")
		r.Header.Set("Sec-Fetch-Dest", "document")
	}
	p.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	p.request("GET", "?from=portal", nil, http.StatusPermanentRedirect, navigate)
	if p.lastHeader.Get("Location") != "/platform/xops/?from=portal" {
		t.Fatal("prefix redirect lost path or query")
	}
	p.client.CheckRedirect = nil
	body := p.request("GET", "/", nil, 200, navigate)
	if !strings.Contains(string(body), `src="./assets/app.js"`) {
		t.Fatal("assets are not relative to the mounted document")
	}
	p.request("GET", "/assets/app.js", nil, 200, nil)
	p.request("GET", "/assets/style.css", nil, 200, nil)
	p.initialize()
	p.request("GET", "/api/v1/inventory", nil, 200, nil)
	p.request("GET", "/api/v1/inventory", nil, 403, navigate)
	p.request("POST", "/api/v1/auth/logout", map[string]any{}, 403, func(r *http.Request) { r.Header.Set("Origin", p.mcp.URL) })
	for _, test := range []struct {
		origin, path string
		status       int
	}{
		{p.http.URL, "/", 404}, {p.http.URL, "/api/v1/auth/session", 404},
		{p.http.URL, "/platform/xops-other/", 404}, {p.http.URL, "/mcp", 404},
		{p.http.URL, p.basePath + "/mcp", 404}, {p.http.URL, p.basePath + "/v1/transfers/", 404},
		{p.mcp.URL, "/", 404}, {p.mcp.URL, p.basePath + "/", 404},
		{p.mcp.URL, "/api/v1/auth/session", 404}, {p.mcp.URL, p.basePath + "/api/v1/inventory", 404},
		{p.mcp.URL, "/mcp", 401},
	} {
		response, err := p.client.Get(test.origin + test.path)
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.Copy(io.Discard, response.Body)
		testutil.Close(t, response.Body)
		if err != nil || response.StatusCode != test.status {
			t.Fatalf("%s%s: HTTP %d, %v", test.origin, test.path, response.StatusCode, err)
		}
	}
	p.request("POST", "/api/v1/auth/logout", map[string]any{}, 204, nil)
	if len(p.lastCookies) != 0 {
		t.Fatal("logout set a cookie")
	}

	p.request("GET", "/api/v1/inventory", nil, 401, nil)
}

func TestAdminPrefixBehindReverseProxy(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	proxy := httptest.NewUnstartedServer(nil)
	t.Cleanup(proxy.Close)
	cfg := adminConfig(t)
	cfg.WebBasePath = "/proxy/console"
	cfg.WebPublicURL = "http://" + proxy.Listener.Addr().String()
	p := newAdminPeer(t, ctx, cfg)
	backend := p.http
	proxy.Config.Handler = httputil.NewSingleHostReverseProxy(mustURL(t, backend.URL))
	proxy.Config.ReadHeaderTimeout = time.Second
	proxy.Start()
	p.http = proxy
	p.request("GET", "/", nil, 200, nil)
	p.request("GET", "/assets/app.js", nil, 200, nil)
	p.initialize()
	tagID := p.save("tags", service.TagInput{Name: "through-proxy"})
	data := p.request("GET", "/api/v1/inventory", nil, 200, nil)
	if !strings.Contains(string(data), tagID) {
		t.Fatal("prefixed proxy did not preserve the authenticated API")
	}
	p.request("GET", "/", nil, 403, func(r *http.Request) { r.Host = "untrusted.example" })
	p.request("POST", "/api/v1/auth/logout", map[string]any{}, 204, nil)
}

func TestPrefixChangeRejectsJWTFromOldAudience(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	cfg := adminConfig(t)
	old := newAdminPeer(t, ctx, cfg)
	old.initialize()
	previousToken := old.token
	testutil.Close(t, old.app)
	old.http.Close()
	old.mcp.Close()
	// Access tokens are bound to the management API prefix.
	cfg.WebBasePath = "/api/v1/console"
	next := newAdminPeer(t, ctx, cfg)
	next.token = previousToken
	next.request("GET", "/api/v1/inventory", nil, 401, nil)
	next.login(adminPassword)
	next.request("GET", "/api/v1/inventory", nil, 200, nil)
}
