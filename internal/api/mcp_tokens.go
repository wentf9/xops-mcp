package api

import (
	"errors"
	"net/http"

	"github.com/wentf9/xops-mcp/internal/mcpauth"
)

func (s *Server) listMCPTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.mcpTokens.List(r.Context())
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, http.StatusOK, map[string]any{"tokens": tokens})
}

func (s *Server) createMCPToken(w http.ResponseWriter, r *http.Request) {
	var input mcpauth.Input
	if !decode(w, r, &input) {
		return
	}
	token, secret, err := s.mcpTokens.Create(r.Context(), input)
	if err != nil {
		mcpTokenFailure(w, err)
		return
	}
	w.Header().Set("ETag", etag(token.Version))
	respond(w, http.StatusCreated, map[string]any{"item": token, "token": secret})
}

func (s *Server) updateMCPToken(w http.ResponseWriter, r *http.Request) {
	version, ok := expected(w, r)
	if !ok {
		return
	}
	var input mcpauth.Input
	if !decode(w, r, &input) {
		return
	}
	token, err := s.mcpTokens.Update(r.Context(), r.PathValue("id"), version, input, false)
	if err != nil {
		mcpTokenFailure(w, err)
		return
	}
	w.Header().Set("ETag", etag(token.Version))
	respond(w, http.StatusOK, token)
}

func (s *Server) revokeMCPToken(w http.ResponseWriter, r *http.Request) {
	version, ok := expected(w, r)
	if !ok {
		return
	}
	token, err := s.mcpTokens.Update(r.Context(), r.PathValue("id"), version, mcpauth.Input{}, true)
	if err != nil {
		mcpTokenFailure(w, err)
		return
	}
	w.Header().Set("ETag", etag(token.Version))
	respond(w, http.StatusOK, token)
}

func mcpTokenFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, mcpauth.ErrInvalid) {
		problem(w, 422, "invalid_token_settings", "请填写有效名称和将来的到期时间")
		return
	}
	failure(w, err)
}
