package config

import (
	"bytes"
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"strings"
)

func readHexKey(path string) ([]byte, error) {
	data, err := ReadFile(path, 256, true)
	if err != nil {
		return nil, err
	}
	defer clear(data)
	data = bytes.TrimSpace(data)
	if len(data) != 64 {
		return nil, errors.New("key file must contain exactly 64 hexadecimal characters")
	}
	key := make([]byte, 32)
	if _, err := hex.Decode(key, data); err != nil {
		clear(key)
		return nil, errors.New("invalid hexadecimal key file")
	}
	return key, nil
}
func (c Config) AdminKeys() ([]byte, *rsa.PrivateKey, error) {
	key, err := readHexKey(c.AdminJWTKeyFile)
	if err != nil {
		return nil, nil, err
	}
	owned := false
	defer func() {
		if !owned {
			clear(key)
		}
	}()
	master, err := readHexKey(c.MasterKeyFile)
	if err != nil {
		return nil, nil, err
	}
	defer clear(master)
	if bytes.Equal(key, master) {
		return nil, nil, errors.New("administrator JWT and master key must differ")
	}
	if c.MCPTokenFile != "" {
		mcp, err := ReadFile(c.MCPTokenFile, 4098, true)
		if err != nil {
			return nil, nil, err
		}
		defer clear(mcp)
		// HTTPOptions uses the trimmed file bytes as the bearer secret. Reject
		// that same secret both literally and in its hexadecimal representation.
		mcpToken := bytes.TrimSpace(mcp)
		if bytes.Equal(mcpToken, key) || strings.EqualFold(string(mcpToken), hex.EncodeToString(key)) {
			return nil, nil, errors.New("administrator JWT and MCP token must differ")
		}
	}
	data, err := ReadFile(c.AdminEncryptionKeyFile, 16<<10, true)
	if err != nil {
		return nil, nil, err
	}
	defer clear(data)
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, nil, errors.New("administrator encryption key must be one PKCS8 RSA private key")
	}
	private, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, nil, errors.New("invalid administrator encryption key")
	}
	rsaKey, ok := private.(*rsa.PrivateKey)
	if !ok || rsaKey.N.BitLen() < 2048 || rsaKey.N.BitLen() > 4096 {
		return nil, nil, errors.New("administrator encryption key must be 2048-4096 bit RSA")
	}
	if err := rsaKey.Validate(); err != nil {
		return nil, nil, errors.New("invalid administrator encryption key")
	}
	owned = true
	return key, rsaKey, nil
}
