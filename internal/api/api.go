// Package api provides the administrator HTTP API. Browser sessions do not
// authenticate MCP requests, and MCP/transfer tokens never grant admin access.
package api

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wentf9/xops-cli/core/mcp/guardrail"
	"github.com/wentf9/xops-cli/core/mcp/ports"
	"github.com/wentf9/xops-cli/core/mcp/state"
	"github.com/wentf9/xops-mcp/internal/adminauth"
	"github.com/wentf9/xops-mcp/internal/config"
	"github.com/wentf9/xops-mcp/internal/mcpauth"
	"github.com/wentf9/xops-mcp/internal/operations"
	"github.com/wentf9/xops-mcp/internal/service"
	"github.com/wentf9/xops-mcp/internal/storage"
)

type Store interface {
	storage.Repository
	storage.AdminRepository
	storage.MCPTokenRepository
}
type Probes interface {
	TestConnection(context.Context, ports.Permit, string) error
}
type Options struct {
	MCPTokens     *mcpauth.Manager
	PublicURL     string
	BasePath      string
	AllowedHosts  []string
	SetupToken    string
	PasswordCost  int
	JWTKey        []byte
	EncryptionKey *rsa.PrivateKey
}
type Server struct {
	store        Store
	editor       *service.Editor
	auth         *adminauth.Manager
	mcpTokens    *mcpauth.Manager
	cipher       *adminauth.RequestCipher
	probes       Probes
	tracker      *operations.Tracker
	hosts        map[string]bool
	scheme       string
	basePath     string
	setupHash    [32]byte
	setupEnabled bool
	requests     chan struct{}
	logins       chan struct{}
	probeSlots   chan struct{}
	mu           sync.Mutex
	attempts     map[string]attempt
	observations map[string]observation
}
type attempt struct {
	count int
	until time.Time
}
type authContext struct {
	token    string
	identity adminauth.Identity
}
type contextKey struct{}

func canonicalHost(raw, scheme string) (string, error) {
	u, err := url.Parse(scheme + "://" + raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(raw, "*\\ \t\r\n") {
		return "", errors.New("invalid administrator HTTP host")
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", errors.New("invalid administrator HTTP port")
		}
	}
	host := strings.ToLower(u.Hostname())
	if port == "" || port == "80" && scheme == "http" || port == "443" && scheme == "https" {
		return host, nil
	}
	return net.JoinHostPort(host, port), nil
}

func New(store Store, editor *service.Editor, probes Probes, tracker *operations.Tracker, options Options) (*Server, error) {
	u, err := url.Parse(options.PublicURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("administrator interface requires a valid public origin")
	}
	basePath, err := config.NormalizeWebBasePath(options.BasePath)
	if err != nil {
		return nil, err
	}
	tokens, err := adminauth.NewTokens(options.JWTKey, editor.Service.Coordinator.DomainID(), basePath)
	if err != nil {
		return nil, err
	}
	auth, err := adminauth.New(store, options.PasswordCost, tokens)
	if err != nil {
		return nil, err
	}
	cipher, err := adminauth.NewRequestCipher(options.EncryptionKey, tokens)
	if err != nil {
		return nil, err
	}
	s := &Server{store: store, editor: editor, auth: auth, cipher: cipher, probes: probes, tracker: tracker, hosts: map[string]bool{}, scheme: u.Scheme, requests: make(chan struct{}, 32), logins: make(chan struct{}, 2), probeSlots: make(chan struct{}, 4), attempts: map[string]attempt{}, observations: map[string]observation{}}
	s.basePath = basePath
	s.mcpTokens = &mcpauth.Manager{Store: store}
	if options.MCPTokens != nil {
		s.mcpTokens = options.MCPTokens
	}
	for _, raw := range append([]string{u.Host}, options.AllowedHosts...) {
		host, err := canonicalHost(raw, u.Scheme)
		if err != nil {
			return nil, err
		}
		s.hosts[host] = true
	}
	if options.SetupToken != "" {
		if len(options.SetupToken) < 32 || len(options.SetupToken) > 4096 || strings.ContainsAny(options.SetupToken, " \t\r\n") {
			return nil, errors.New("setup token must contain 32-4096 bytes without whitespace")
		}
		s.setupHash = sha256.Sum256([]byte(options.SetupToken))
		s.setupEnabled = true
	}
	return s, nil
}

func (s *Server) Handler(assets http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/auth/session", s.session)
	mux.HandleFunc("GET /api/v1/auth/challenge", s.challenge)
	mux.HandleFunc("POST /api/v1/auth/setup", s.setup)
	mux.HandleFunc("POST /api/v1/auth/login", s.login)
	mux.Handle("POST /api/v1/auth/logout", s.authorize(http.HandlerFunc(s.logout)))
	mux.Handle("PUT /api/v1/auth/password", s.authorize(http.HandlerFunc(s.password)))
	mux.Handle("GET /api/v1/inventory", s.authorize(http.HandlerFunc(s.inventory)))
	mux.Handle("GET /api/v1/mcp-tokens", s.authorize(http.HandlerFunc(s.listMCPTokens)))
	mux.Handle("POST /api/v1/mcp-tokens", s.authorize(http.HandlerFunc(s.createMCPToken)))
	mux.Handle("PUT /api/v1/mcp-tokens/{id}", s.authorize(http.HandlerFunc(s.updateMCPToken)))
	mux.Handle("DELETE /api/v1/mcp-tokens/{id}", s.authorize(http.HandlerFunc(s.revokeMCPToken)))
	for _, kind := range []string{"hosts", "identities", "nodes", "credentials", "tags"} {
		mux.Handle("POST /api/v1/"+kind, s.authorize(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.save(w, r, kind) })))
		mux.Handle("PUT /api/v1/"+kind+"/{id}", s.authorize(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.save(w, r, kind) })))
		mux.Handle("DELETE /api/v1/"+kind+"/{id}", s.authorize(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.remove(w, r, kind) })))
	}
	mux.Handle("PUT /api/v1/policy", s.authorize(http.HandlerFunc(s.policy)))
	mux.Handle("POST /api/v1/hosts/{id}/probe", s.authorize(http.HandlerFunc(s.probeHost)))
	mux.Handle("POST /api/v1/hosts/{id}/key-preview", s.authorize(http.HandlerFunc(s.previewHostKey)))
	mux.Handle("POST /api/v1/hosts/{id}/trust", s.authorize(http.HandlerFunc(s.trustHost)))
	mux.Handle("DELETE /api/v1/hosts/{id}/trust", s.authorize(http.HandlerFunc(s.trustHost)))
	mux.Handle("POST /api/v1/nodes/{id}/test", s.authorize(http.HandlerFunc(s.testNode)))
	mux.Handle("GET /api/v1/audit", s.authorize(http.HandlerFunc(s.audit)))
	mux.Handle("GET /api/v1/operations", s.authorize(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, map[string]any{"operations": s.tracker.List()})
	})))
	mux.Handle("POST /api/v1/reconcile", s.authorize(http.HandlerFunc(s.reconcile)))
	mux.Handle("/", assets)
	if s.basePath != "" {
		prefixed := http.NewServeMux()
		prefixed.Handle(s.basePath+"/", http.StripPrefix(s.basePath, mux))
		prefixed.HandleFunc("GET "+s.basePath, func(w http.ResponseWriter, r *http.Request) {
			target := s.basePath + "/"
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, target, http.StatusPermanentRedirect)
		})
		return s.Protect(prefixed)
	}
	return s.Protect(mux)
}

// Protect also wraps embedded assets so Host validation precedes login/setup.
func (s *Server) Protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		host, err := canonicalHost(r.Host, s.scheme)
		if err != nil || !s.hosts[host] {
			problem(w, 403, "origin_denied", "请求来源不被允许")
			return
		}
		origins := r.Header.Values("Origin")
		unsafe := r.Method != http.MethodGet && r.Method != http.MethodHead
		// The public console document may be opened from an external link.
		// Fetches, frames and API routes retain the cross-site restriction.
		documentPath := r.URL.Path == s.basePath+"/" || s.basePath != "" && r.URL.Path == s.basePath
		documentNavigation := !unsafe && documentPath && r.Header.Get("Sec-Fetch-Mode") == "navigate" && r.Header.Get("Sec-Fetch-Dest") == "document"
		if len(origins) > 1 || unsafe && len(origins) != 1 || r.Header.Get("Sec-Fetch-Site") == "cross-site" && !documentNavigation {
			problem(w, 403, "origin_denied", "请求来源不被允许")
			return
		}
		if len(origins) == 1 {
			u, err := url.Parse(origins[0])
			originHost := ""
			if err == nil {
				originHost, err = canonicalHost(u.Host, u.Scheme)
			}
			if err != nil || u.Scheme != s.scheme || originHost != host || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
				problem(w, 403, "origin_denied", "请求来源不被允许")
				return
			}
		}
		select {
		case s.requests <- struct{}{}:
			defer func() { <-s.requests }()
		default:
			problem(w, 429, "busy", "请求较多，请稍后重试")
			return
		}
		controller := http.NewResponseController(w)
		if err := controller.SetReadDeadline(time.Now().Add(15 * time.Second)); err != nil {
			problem(w, 500, "deadline_unavailable", "无法设置请求期限")
			return
		}
		if err := controller.SetWriteDeadline(time.Now().Add(20 * time.Second)); err != nil {
			problem(w, 500, "deadline_unavailable", "无法设置响应期限")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if value != nil {
		if err := json.NewEncoder(w).Encode(value); err != nil {
			slog.Debug("administrator response interrupted", "error", err)
		}
	}
}
func problem(w http.ResponseWriter, status int, code, message string) {
	respond(w, status, map[string]string{"code": code, "message": message})
}
func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		problem(w, 415, "json_required", "请发送 JSON 请求")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 512<<10)
	defer func() {
		if err := r.Body.Close(); err != nil {
			slog.Debug("close administrator request", "error", err)
		}
	}()
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		problem(w, 400, "invalid_json", "请求内容或字段不正确")
		return false
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		problem(w, 400, "invalid_json", "请求必须只包含一个 JSON 对象")
		return false
	}
	return true
}
func etag(revision uint64) string { return fmt.Sprintf("\"%d\"", revision) }
func expected(w http.ResponseWriter, r *http.Request) (uint64, bool) {
	if len(r.Header.Values("If-Match")) != 1 {
		problem(w, 428, "revision_required", "请刷新页面后再保存")
		return 0, false
	}
	raw := r.Header.Get("If-Match")
	if len(raw) < 3 {
		problem(w, 400, "invalid_revision", "版本标记不正确")
		return 0, false
	}
	n, err := strconv.ParseUint(strings.Trim(raw, "\""), 10, 63)
	if err != nil || raw != etag(n) {
		problem(w, 400, "invalid_revision", "版本标记不正确")
		return 0, false
	}
	return n, true
}
func failure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storage.ErrConflict), errors.Is(err, ports.ErrStaleBinding):
		problem(w, 412, "revision_conflict", "配置已被其他会话修改，请刷新后重试")
	case errors.Is(err, storage.ErrNotFound), errors.Is(err, ports.ErrNotFound):
		problem(w, 404, "not_found", "记录不存在或已删除")
	case errors.Is(err, service.ErrInUse):
		problem(w, 409, "resource_in_use", "该记录仍被引用，请先解除关联")
	case errors.Is(err, service.ErrPending), errors.Is(err, state.ErrUpdating):
		problem(w, 503, "publication_pending", "当前有配置等待应用，请刷新状态后重试应用，勿重复提交")
	case errors.Is(err, service.ErrInvalid):
		problem(w, 422, "invalid_configuration", err.Error())
	case errors.Is(err, ports.ErrNodeDisabled):
		problem(w, 409, "node_disabled", "节点或上游跳板尚未启用")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		problem(w, 408, "request_timeout", "操作已取消或超时，请刷新状态")
	default:
		problem(w, 500, "operation_failed", "操作失败，请刷新状态后检查服务")
	}
}
func finish(w http.ResponseWriter, result service.EditResult, err error) {
	if err != nil {
		var audit *guardrail.ExecutedPostAuditError
		if errors.As(err, &audit) {
			w.Header().Set("ETag", etag(result.Revision))
			respond(w, 202, map[string]any{"revision": result.Revision, "id": result.ID, "warning": "配置已应用，但审计写入失败，请勿重复提交"})
			return
		}
		failure(w, err)
		return
	}
	w.Header().Set("ETag", etag(result.Revision))
	respond(w, 200, result)
}
