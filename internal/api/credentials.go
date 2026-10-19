package api

import (
	"errors"
	"net/http"

	"github.com/wentf9/xops-mcp/internal/secure"
)

type validateKeyInput struct {
	PrivateKey string `json:"privateKey"`
	Passphrase string `json:"passphrase"`
}

type validateKeyResult struct {
	Valid       bool   `json:"valid"`
	Protected   bool   `json:"protected"`
	KeyType     string `json:"keyType,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Message     string `json:"message,omitempty"`
}

func (s *Server) validateCredentialKey(w http.ResponseWriter, r *http.Request) {
	var in validateKeyInput
	if !decode(w, r, &in) {
		return
	}
	if in.PrivateKey == "" {
		problem(w, 400, "invalid_material", "请提供私钥内容")
		return
	}
	info, err := secure.InspectPrivateKey([]byte(in.PrivateKey), []byte(in.Passphrase))
	res := validateKeyResult{}
	if info != nil {
		res.Protected = info.Protected
		res.KeyType = info.KeyType
		res.Fingerprint = info.Fingerprint
	}
	if err != nil {
		res.Valid = false
		switch {
		case errors.Is(err, secure.ErrPassphraseRequired):
			res.Message = "该私钥受密码保护，请输入私钥口令"
		case errors.Is(err, secure.ErrPassphraseIncorrect):
			res.Message = "私钥口令不正确"
		case errors.Is(err, secure.ErrPassphraseUnnecessary):
			res.Message = "私钥未受密码保护，无需填写口令"
		case errors.Is(err, secure.ErrPrivateKeyInvalid):
			res.Message = "私钥格式不正确，无法解析"
		default:
			res.Message = err.Error()
		}
		res.Message = localizedMessage(w, res.Message)
		respond(w, 200, res)
		return
	}
	res.Valid = true
	res.Message = "私钥与口令验证通过"
	res.Message = localizedMessage(w, res.Message)
	respond(w, 200, res)
}
