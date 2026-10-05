package config_test

import (
	"bytes"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/wentf9/xops-mcp/internal/config"
	"github.com/wentf9/xops-mcp/internal/testutil"
)

func TestAdministratorKeyFilesArePrivateAndIndependent(t *testing.T) {
	c := testutil.Config(t)
	signing, private, err := c.AdminKeys()
	if err != nil || len(signing) != 32 || private == nil {
		t.Fatal(err)
	}
	if err := os.Chmod(c.AdminJWTKeyFile, 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.AdminKeys(); err == nil {
		t.Fatal("public signing key file accepted")
	}
	if err := os.Chmod(c.AdminJWTKeyFile, 0600); err != nil {
		t.Fatal(err)
	}
	master, err := config.ReadFile(c.MasterKeyFile, 256, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.AdminJWTKeyFile, master, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.AdminKeys(); err == nil {
		t.Fatal("JWT reused master key")
	}
	if err := os.WriteFile(c.AdminJWTKeyFile, []byte(hex.EncodeToString(signing)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.MCPTokenFile, bytes.ToUpper([]byte(hex.EncodeToString(signing))), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.AdminKeys(); err == nil {
		t.Fatal("JWT reused hex-encoded MCP token")
	}
}

func TestAdministratorJWTKeyRejectsMCPSecretRepresentations(t *testing.T) {
	signing := []byte("0123456789abcdef0123456789abcdef")
	for _, tc := range []struct {
		name, token string
		reject      bool
	}{
		{"raw", string(signing), true},
		{"raw-with-file-whitespace", " \t" + string(signing) + "\r\n", true},
		{"hex", hex.EncodeToString(signing), true},
		{"uppercase-hex", string(bytes.ToUpper([]byte(hex.EncodeToString(signing)))), true},
		{"hex-with-file-whitespace", " \t" + hex.EncodeToString(signing) + "\r\n", true},
		{"independent-raw", "fedcba9876543210fedcba9876543210", false},
		{"independent-hex", hex.EncodeToString(bytes.Repeat([]byte{0x78}, 32)), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testutil.Config(t)
			if err := os.WriteFile(c.AdminJWTKeyFile, []byte(hex.EncodeToString(signing)), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(c.MCPTokenFile, []byte(tc.token), 0600); err != nil {
				t.Fatal(err)
			}
			key, private, err := c.AdminKeys()
			defer clear(key)
			if tc.reject {
				if err == nil || key != nil || private != nil {
					t.Fatal("shared MCP/JWT secret was accepted")
				}
				if strings.Contains(err.Error(), string(signing)) || strings.Contains(err.Error(), hex.EncodeToString(signing)) {
					t.Fatal("key validation leaked secret material")
				}
			} else if err != nil || !bytes.Equal(key, signing) || private == nil {
				t.Fatal("independent credentials rejected", err)
			}
		})
	}
}
