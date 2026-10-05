package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/wentf9/xops-cli/core/mcp/ports"
	"github.com/wentf9/xops-mcp/internal/adapters/xops"
	"github.com/wentf9/xops-mcp/internal/adminauth"
	"github.com/wentf9/xops-mcp/internal/secure"
	"github.com/wentf9/xops-mcp/internal/storage"
	cryptoSSH "golang.org/x/crypto/ssh"
)

type observation struct {
	hostID, session, key string
	revision             uint64
	expires              time.Time
}

func (s *Server) probeSlot(w http.ResponseWriter) (func(), bool) {
	select {
	case s.probeSlots <- struct{}{}:
		return func() { <-s.probeSlots }, true
	default:
		problem(w, 429, "probe_busy", "连接测试繁忙，请稍后重试")
		return nil, false
	}
}
func (s *Server) probeHost(w http.ResponseWriter, r *http.Request) {
	rev, ok := expected(w, r)
	if !ok {
		return
	}
	var input struct {
		Algorithm string `json:"algorithm"`
	}
	if !decode(w, r, &input) {
		return
	}
	release, ok := s.probeSlot(w)
	if !ok {
		return
	}
	defer release()
	host, ok := s.hostAtRevision(w, r, rev)
	if !ok {
		return
	}
	key, err := xops.ProbeHostKey(r.Context(), host, input.Algorithm)
	if err != nil {
		problem(w, 502, "probe_failed", "无法获取主机公钥，请检查地址、端口和网络")
		return
	}
	if _, ok := s.hostAtRevision(w, r, rev); !ok {
		return
	}
	s.rememberHostKey(w, r, host, rev, key)
}

// previewHostKey prepares independent key enrollment without contacting the
// endpoint. Jump-only hosts can therefore be provisioned before any connection
// or authentication is possible. Trust still requires the separate confirmation.
func (s *Server) previewHostKey(w http.ResponseWriter, r *http.Request) {
	rev, ok := expected(w, r)
	if !ok {
		return
	}
	var input struct {
		HostKey string `json:"hostKey"`
	}
	if !decode(w, r, &input) {
		return
	}
	host, ok := s.hostAtRevision(w, r, rev)
	if !ok {
		return
	}
	key, err := xops.ParseHostKey(input.HostKey)
	if err != nil {
		problem(w, 422, "invalid_host_key", "请提供单个 SSH 主机公钥，不含选项或证书")
		return
	}
	s.rememberHostKey(w, r, host, rev, key)
}

func (s *Server) hostAtRevision(w http.ResponseWriter, r *http.Request, rev uint64) (storage.Host, bool) {
	v, err := s.store.Load(r.Context())
	if err != nil {
		failure(w, err)
		return storage.Host{}, false
	}
	if v.Revision != rev {
		failure(w, storage.ErrConflict)
		return storage.Host{}, false
	}
	host, ok := v.Hosts[r.PathValue("id")]
	if !ok {
		failure(w, storage.ErrNotFound)
		return storage.Host{}, false
	}
	return host, true
}

func (s *Server) rememberHostKey(w http.ResponseWriter, r *http.Request, host storage.Host, rev uint64, key cryptoSSH.PublicKey) {
	identity := r.Context().Value(contextKey{}).(authContext)
	id := secure.ID()
	public := string(cryptoSSH.MarshalAuthorizedKey(key))
	now := time.Now()
	s.mu.Lock()
	for id, o := range s.observations {
		if now.After(o.expires) {
			delete(s.observations, id)
		}
	}
	if len(s.observations) >= 128 {
		s.mu.Unlock()
		problem(w, 429, "probe_busy", "待确认的主机公钥过多，请稍后重试")
		return
	}
	s.observations[id] = observation{hostID: host.ID, session: string(adminauth.Digest(identity.token)), key: public, revision: rev, expires: now.Add(2 * time.Minute)}
	s.mu.Unlock()
	respond(w, 200, map[string]any{"probeID": id, "hostKey": public, "fingerprint": cryptoSSH.FingerprintSHA256(key), "algorithm": key.Type(), "address": host.Address, "port": host.Port, "expiresIn": 120})
}
func (s *Server) trustHost(w http.ResponseWriter, r *http.Request) {
	rev, ok := expected(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodDelete {
		result, err := s.editor.TrustHost(r.Context(), rev, r.PathValue("id"), "")
		finish(w, result, err)
		return
	}
	var input struct {
		ProbeID string `json:"probeID"`
	}
	if !decode(w, r, &input) {
		return
	}
	identity := r.Context().Value(contextKey{}).(authContext)
	s.mu.Lock()
	observed, exists := s.observations[input.ProbeID]
	if exists && observed.session == string(adminauth.Digest(identity.token)) {
		delete(s.observations, input.ProbeID)
	}
	s.mu.Unlock()
	if !exists || observed.session != string(adminauth.Digest(identity.token)) || observed.hostID != r.PathValue("id") || !time.Now().Before(observed.expires) {
		problem(w, 409, "probe_expired", "主机公钥确认已过期，请重新预览")
		return
	}
	if observed.revision != rev {
		failure(w, storage.ErrConflict)
		return
	}
	result, err := s.editor.TrustHost(r.Context(), rev, observed.hostID, observed.key)
	finish(w, result, err)
}
func (s *Server) testNode(w http.ResponseWriter, r *http.Request) {
	rev, ok := expected(w, r)
	if !ok {
		return
	}
	release, ok := s.probeSlot(w)
	if !ok {
		return
	}
	defer release()
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	view, err := s.editor.Service.Coordinator.Resolve(ctx, ports.ResolveRequest{Selectors: []string{r.PathValue("id")}})
	if err != nil {
		failure(w, err)
		return
	}
	if view.Revision != strconvRevision(rev) {
		failure(w, storage.ErrConflict)
		return
	}
	binding, err := ports.Bind(view, "administrator", "admin.connection_test", map[string]string{"nodeID": r.PathValue("id")})
	if err != nil {
		failure(w, err)
		return
	}
	operationID := secure.ID()
	event := ports.AuditEvent{OperationID: operationID, Tool: "admin.connection_test", NodeID: r.PathValue("id"), Decision: "administrator", Outcome: "intent", RiskLevel: "safe"}
	if err := s.store.Append(ctx, event); err != nil {
		failure(w, err)
		return
	}
	permit, err := s.tracker.Enter(ctx, ports.Admission{OperationID: operationID, Binding: binding, Snapshot: view, Phase: ports.Inspect})
	if err != nil {
		failure(w, err)
		return
	}
	start := time.Now()
	err = s.probes.TestConnection(ctx, permit, r.PathValue("id"))
	err = errors.Join(err, permit.Close())
	event.Outcome = "executed"
	if err != nil {
		event.Outcome = "error"
	}
	cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	auditErr := s.store.Append(cleanup, event)
	if err != nil {
		problem(w, 502, "connection_failed", "SSH 连接失败，请检查地址、凭据和主机密钥")
		return
	}
	if auditErr != nil {
		failure(w, auditErr)
		return
	}
	respond(w, 200, map[string]any{"connected": true, "elapsedMS": time.Since(start).Milliseconds()})
}
func strconvRevision(revision uint64) string { return strconv.FormatUint(revision, 10) }
