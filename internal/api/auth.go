package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/wentf9/xops-mcp/internal/adminauth"
	"github.com/wentf9/xops-mcp/internal/storage"
)

func (s *Server) cookie(w http.ResponseWriter, login adminauth.Login) {
	c := &http.Cookie{Name: s.cookieName, Value: login.Token, Path: s.basePath + "/api/v1", HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteStrictMode, Expires: login.ExpiresAt, MaxAge: int(time.Until(login.ExpiresAt).Seconds())}
	if login.Token == "" {
		c.MaxAge = -1
		c.Expires = time.Unix(1, 0)
	}
	http.SetCookie(w, c)
}
func (s *Server) token(r *http.Request) string {
	values := r.CookiesNamed(s.cookieName)
	if len(values) != 1 {
		return ""
	}
	return values[0].Value
}
func (s *Server) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := s.token(r)
		session, err := s.auth.Authenticate(r.Context(), token)
		if err != nil {
			if errors.Is(err, adminauth.ErrUnauthorized) {
				s.cookie(w, adminauth.Login{})
				problem(w, 401, "login_required", "请先登录")
			} else {
				failure(w, err)
			}
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if len(r.Header.Values("X-CSRF-Token")) != 1 || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(adminauth.CSRF(token))) != 1 {
				problem(w, 403, "csrf_invalid", "会话校验失败，请刷新页面")
				return
			}
		}
		ctx := context.WithValue(r.Context(), contextKey{}, authContext{token, session})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	a, err := s.store.Admin(r.Context())
	if errors.Is(err, storage.ErrNotFound) {
		respond(w, 200, map[string]any{"authenticated": false, "setupRequired": true, "setupEnabled": s.setupEnabled})
		return
	}
	if err != nil {
		failure(w, err)
		return
	}
	token := s.token(r)
	session, err := s.auth.Authenticate(r.Context(), token)
	if errors.Is(err, adminauth.ErrUnauthorized) {
		respond(w, 200, map[string]any{"authenticated": false, "setupRequired": false})
		return
	}
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, map[string]any{"authenticated": true, "username": a.Username, "csrf": adminauth.CSRF(token), "expiresAt": session.ExpiresAt})
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
	if !decode(w, r, &input) {
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
	s.cookie(w, login)
	respond(w, 200, map[string]any{"username": input.Username, "csrf": login.CSRF, "expiresAt": login.ExpiresAt})
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
	if !decode(w, r, &input) {
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
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	identity := r.Context().Value(contextKey{}).(authContext)
	if err := s.auth.Logout(r.Context(), identity.token); err != nil {
		failure(w, err)
		return
	}
	s.cookie(w, adminauth.Login{})
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
	if !decode(w, r, &input) {
		return
	}
	identity := r.Context().Value(contextKey{}).(authContext)
	err := s.auth.ChangePassword(r.Context(), identity.session, input.Current, input.Next)
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
	s.cookie(w, adminauth.Login{})
	respond(w, 204, nil)
}
