package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wentf9/xops-cli/core/mcp/ports"
	"github.com/wentf9/xops-cli/core/ssh"
	"github.com/wentf9/xops-mcp/internal/adapters/xops"
	"github.com/wentf9/xops-mcp/internal/adminauth"
	"github.com/wentf9/xops-mcp/internal/secure"
	"github.com/wentf9/xops-mcp/internal/service"
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
	res, err := s.probes.TestConnection(ctx, permit, r.PathValue("id"))
	err = errors.Join(err, permit.Close())
	event.Outcome = "executed"
	if err != nil {
		event.Outcome = "error"
	}
	cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	auditErr := s.store.Append(cleanup, event)
	var diags []xops.DiagnosticEvent
	if res != nil {
		diags = res.Diagnostics
	}
	if err != nil {
		problemWithDiagnostics(w, 502, "connection_failed", "SSH 连接失败，请检查地址、凭据和主机密钥", diags)
		return
	}
	if auditErr != nil {
		failure(w, auditErr)
		return
	}
	respond(w, 200, map[string]any{"connected": true, "elapsedMS": time.Since(start).Milliseconds(), "diagnostics": diags})
}
func (s *Server) testNodePrivilege(w http.ResponseWriter, r *http.Request) {
	rev, ok := expected(w, r)
	if !ok {
		return
	}
	release, ok := s.probeSlot(w)
	if !ok {
		return
	}
	defer release()
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
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
	binding, err := ports.Bind(view, "administrator", "admin.privilege_test", map[string]string{"nodeID": r.PathValue("id")})
	if err != nil {
		failure(w, err)
		return
	}
	operationID := secure.ID()
	event := ports.AuditEvent{OperationID: operationID, Tool: "admin.privilege_test", NodeID: r.PathValue("id"), Decision: "administrator", Outcome: "intent", RiskLevel: "safe"}
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
	res, err := s.probes.TestPrivilege(ctx, permit, r.PathValue("id"))
	err = errors.Join(err, permit.Close())
	event.Outcome = "executed"
	if err != nil {
		event.Outcome = "error"
	}
	cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	auditErr := s.store.Append(cleanup, event)
	var diags []xops.DiagnosticEvent
	if res != nil {
		diags = res.Diagnostics
	}
	if err != nil {
		if strings.Contains(err.Error(), "SSH connection failed") {
			problemWithDiagnostics(w, 502, "connection_failed", "SSH 连接失败，请检查地址、凭据和主机密钥", diags)
			return
		}
		if strings.Contains(err.Error(), "未配置提权方式") {
			problemWithDiagnostics(w, 422, "privilege_not_configured", err.Error(), diags)
			return
		}
		problemWithDiagnostics(w, 502, "privilege_failed", err.Error(), diags)
		return
	}
	if auditErr != nil {
		failure(w, auditErr)
		return
	}
	respond(w, 200, map[string]any{
		"escalated":   res.Escalated,
		"sudoMode":    res.SudoMode,
		"user":        res.User,
		"detail":      localizedMessage(w, res.Detail),
		"elapsedMS":   time.Since(start).Milliseconds(),
		"diagnostics": diags,
	})
}
func strconvRevision(revision uint64) string { return strconv.FormatUint(revision, 10) }

type privilegeProbeInput struct {
	NodeID                string               `json:"nodeID,omitempty"`
	HostID                string               `json:"hostID"`
	IdentityID            string               `json:"identityID"`
	JumpIDs               []string             `json:"jumpIDs"`
	SudoMode              string               `json:"sudoMode"`
	PrivilegeCredentialID string               `json:"privilegeCredentialID"`
	PasswordPromptPattern string               `json:"passwordPromptPattern,omitempty"`
	Execution             *ssh.ExecutionConfig `json:"execution,omitempty"`
}

func (s *Server) probePrivilege(w http.ResponseWriter, r *http.Request) {
	revision, ok := expected(w, r)
	if !ok {
		return
	}
	var input privilegeProbeInput
	if !decode(w, r, &input) {
		return
	}
	release, ok := s.probeSlot(w)
	if !ok {
		return
	}
	defer release()
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	v, err := s.editor.Service.Inventory(ctx)
	if err != nil {
		failure(w, err)
		return
	}
	if v.Revision != revision {
		failure(w, storage.ErrConflict)
		return
	}
	if s.editor.Service.Coordinator.Pending() {
		failure(w, service.ErrPending)
		return
	}
	id := input.NodeID
	node, exists := v.Nodes[id]
	if id != "" && !exists {
		failure(w, storage.ErrNotFound)
		return
	}
	if id == "" {
		id = secure.ID()
		node = storage.Node{ID: id, Name: "probe-" + id}
	}
	node.HostID, node.IdentityID = input.HostID, input.IdentityID
	node.JumpIDs = slices.Clone(input.JumpIDs)
	node.Disabled = false
	node.SudoMode = input.SudoMode
	node.PrivilegeCredentialID = input.PrivilegeCredentialID
	node.PasswordPromptPattern = input.PasswordPromptPattern
	node.Execution = input.Execution.Clone()
	v.Nodes[id] = node

	view, sources, err := xops.Snapshot(v)
	if err != nil {
		failure(w, fmt.Errorf("%w: %w", service.ErrInvalid, err))
		return
	}
	target := view.Targets[id]
	for _, hop := range target.Plan.Hops {
		if v.Nodes[hop.NodeID].Disabled {
			failure(w, ports.ErrNodeDisabled)
			return
		}
	}
	operationID := secure.ID()
	event := ports.AuditEvent{OperationID: operationID, Tool: "admin.privilege_probe", NodeID: input.NodeID, Decision: "administrator", Outcome: "intent", RiskLevel: "safe"}
	if err := s.store.Append(ctx, event); err != nil {
		failure(w, err)
		return
	}
	start := time.Now()
	res, probeErr := s.probes.ProbePrivilege(ctx, target.Plan, target.Execution, sources)
	event.Outcome = "executed"
	if probeErr != nil {
		event.Outcome = "error"
	}
	cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	auditErr := s.store.Append(cleanup, event)
	var diags []xops.DiagnosticEvent
	if res != nil {
		diags = res.Diagnostics
	}
	if probeErr != nil {
		if errors.Is(probeErr, context.Canceled) || errors.Is(probeErr, context.DeadlineExceeded) {
			failure(w, probeErr)
			return
		}
		if strings.Contains(probeErr.Error(), "SSH connection failed") {
			problemWithDiagnostics(w, 502, "connection_failed", "SSH 连接失败，请检查地址、凭据和主机密钥", diags)
			return
		}
		if strings.Contains(probeErr.Error(), "未配置提权方式") {
			problemWithDiagnostics(w, 422, "privilege_not_configured", probeErr.Error(), diags)
			return
		}
		problemWithDiagnostics(w, 502, "privilege_failed", probeErr.Error(), diags)
		return
	}
	if auditErr != nil {
		failure(w, auditErr)
		return
	}
	current, err := s.editor.Service.Inventory(ctx)
	if err != nil {
		failure(w, err)
		return
	}
	if current.Revision != revision {
		failure(w, storage.ErrConflict)
		return
	}
	if s.editor.Service.Coordinator.Pending() {
		failure(w, service.ErrPending)
		return
	}
	respond(w, 200, map[string]any{
		"escalated":   res.Escalated,
		"sudoMode":    res.SudoMode,
		"user":        res.User,
		"detail":      localizedMessage(w, res.Detail),
		"elapsedMS":   time.Since(start).Milliseconds(),
		"diagnostics": diags,
	})
}
