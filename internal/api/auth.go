package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/wentf9/xops-mcp/internal/adminauth"
	"github.com/wentf9/xops-mcp/internal/storage"
)

func (s *Server) token(r *http.Request) string {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return ""
	}
	parts := strings.Split(values[0], " ")
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return ""
	}
	return parts[1]
}
func (s *Server) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := s.token(r)
		identity, err := s.auth.Authenticate(r.Context(), token)
		if err != nil {
			if errors.Is(err, adminauth.ErrUnauthorized) {
				w.Header().Set("WWW-Authenticate", `Bearer realm="xops-admin"`)
				problem(w, 401, "login_required", "请先登录")
			} else {
				failure(w, err)
			}
			return
		}
		ctx := context.WithValue(r.Context(), contextKey{}, authContext{token, identity})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	if identity, err := s.auth.Authenticate(r.Context(), s.token(r)); err == nil {
		respond(w, 200, map[string]any{"authenticated": true, "username": identity.Username, "expiresAt": identity.ExpiresAt})
		return
	} else if !errors.Is(err, adminauth.ErrUnauthorized) {
		failure(w, err)
		return
	}
	_, err := s.store.Admin(r.Context())
	if errors.Is(err, storage.ErrNotFound) {
		respond(w, 200, map[string]any{"authenticated": false, "setupRequired": true, "setupEnabled": s.setupEnabled})
		return
	}
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, map[string]any{"authenticated": false, "setupRequired": false})
}
func (s *Server) challenge(w http.ResponseWriter, r *http.Request) {
	action := r.URL.Query().Get("action")
	token := ""
	if action == "password" {
		token = s.token(r)
		if _, err := s.auth.Authenticate(r.Context(), token); err != nil {
			problem(w, 401, "login_required", "请先登录")
			return
		}
	}
	parameters, err := s.cipher.Challenge(r.Context(), action, token)
	if err != nil {
		problem(w, 400, "invalid_challenge", "加密请求用途不正确")
		return
	}
	respond(w, 200, parameters)
}
func (s *Server) encryptedInput(w http.ResponseWriter, r *http.Request, action string, value any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, adminauth.MaxEncryptedRequest)
	var input struct {
		Ciphertext string `json:"ciphertext"`
	}
	if !decode(w, r, &input) {
		return false
	}
	token := ""
	if action == "password" {
		token = s.token(r)
	}
	if err := s.cipher.Decrypt(r.Context(), input.Ciphertext, action, token, value); err != nil {
		problem(w, 400, "encrypted_request_invalid", "加密请求无效或已过期，请重新提交")
		return false
	}
	return true
}
func (s *Server) loginSlot(w http.ResponseWriter, r *http.Request) (func(), bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	now := time.Now()
	s.mu.Lock()
	for key, entry := range s.attempts {
		if now.After(entry.until) {
			delete(s.attempts, key)
		}
	}
	entry := s.attempts[host]
	limited := entry.count >= 10 || len(s.attempts) >= 512 && entry.count == 0
	if !limited {
		if entry.count == 0 {
			entry.until = now.Add(time.Minute)
		}
		entry.count++
		s.attempts[host] = entry
	}
	s.mu.Unlock()
	if limited {
		w.Header().Set("Retry-After", "60")
		problem(w, 429, "login_limited", "尝试次数过多，请稍后重试")
		return nil, false
	}
	select {
	case s.logins <- struct{}{}:
		return func() { <-s.logins }, true
	default:
		problem(w, 429, "login_busy", "登录繁忙，请稍后重试")
		return nil, false
	}
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	release, ok := s.loginSlot(w, r)
	if !ok {
		return
	}
	defer release()
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !s.encryptedInput(w, r, "login", &input) {
		return
	}
	login, err := s.auth.Login(r.Context(), input.Username, input.Password)
	if errors.Is(err, adminauth.ErrUnauthorized) {
		problem(w, 401, "invalid_login", "用户名或密码不正确")
		return
	}
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, map[string]any{"username": input.Username, "accessToken": login.Token, "tokenType": "Bearer", "expiresAt": login.ExpiresAt})
}
func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	release, ok := s.loginSlot(w, r)
	if !ok {
		return
	}
	defer release()
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Token    string `json:"token"`
	}
	if !s.encryptedInput(w, r, "setup", &input) {
		return
	}
	sum := sha256.Sum256([]byte(input.Token))
	if !s.setupEnabled || subtle.ConstantTimeCompare(sum[:], s.setupHash[:]) != 1 {
		problem(w, 403, "setup_denied", "初始化凭据不正确或未启用 Web 初始化")
		return
	}
	if err := s.auth.Initialize(r.Context(), input.Username, input.Password); err != nil {
		if errors.Is(err, storage.ErrAdminExists) {
			problem(w, 409, "already_initialized", "管理员已初始化，请登录")
		} else if errors.Is(err, adminauth.ErrInvalid) {
			problem(w, 422, "invalid_password", "用户名需为 1–64 字符，密码需为 12–72 字节")
		} else {
			failure(w, err)
		}
		return
	}
	respond(w, 201, map[string]bool{"initialized": true})
}
func (s *Server) logout(w http.ResponseWriter, _ *http.Request) {
	// Stateless logout is client-side deletion; issued JWTs expire naturally.
	respond(w, 204, nil)
}
func (s *Server) password(w http.ResponseWriter, r *http.Request) {
	release, ok := s.loginSlot(w, r)
	if !ok {
		return
	}
	defer release()
	var input struct {
		Current string `json:"current"`
		Next    string `json:"next"`
	}
	if !s.encryptedInput(w, r, "password", &input) {
		return
	}
	identity := r.Context().Value(contextKey{}).(authContext)
	err := s.auth.ChangePassword(r.Context(), identity.identity, input.Current, input.Next)
	if errors.Is(err, adminauth.ErrUnauthorized) {
		problem(w, 403, "invalid_password", "当前密码不正确")
		return
	}
	if errors.Is(err, adminauth.ErrInvalid) {
		problem(w, 422, "invalid_password", "新密码需为 12–72 字节")
		return
	}
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 204, nil)
}
