// Package config reads only explicitly selected deployment files.
package config

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	mcpruntime "github.com/wentf9/xops-cli/core/mcp/runtime"
	"github.com/wentf9/xops-mcp/internal/secure"
	"gopkg.in/yaml.v3"
)

type Config struct {
	AdminJWTKeyFile         string        `yaml:"admin_jwt_key_file"`
	AdminEncryptionKeyFile  string        `yaml:"admin_encryption_key_file"`
	WebTLSCertFile          string        `yaml:"web_tls_cert_file"`
	WebTLSKeyFile           string        `yaml:"web_tls_key_file"`
	WebTLSEnabled           bool          `yaml:"web_tls_enabled"`
	DatabaseDriver          string        `yaml:"database_driver"`
	PostgresDSNFile         string        `yaml:"postgres_dsn_file"`
	DataDir                 string        `yaml:"data_dir"`
	MasterKeyFile           string        `yaml:"master_key_file"`
	MCPTokenFile            string        `yaml:"mcp_token_file"`
	Listen                  string        `yaml:"listen"`
	PublicURL               string        `yaml:"public_url"`
	AllowedHosts            []string      `yaml:"allowed_hosts"`
	AllowedOrigins          []string      `yaml:"allowed_origins"`
	ToolTimeout             time.Duration `yaml:"tool_timeout"`
	ShutdownTimeout         time.Duration `yaml:"shutdown_timeout"`
	WebEnabled              bool          `yaml:"web_enabled"`
	WebListen               string        `yaml:"web_listen"`
	WebPublicURL            string        `yaml:"web_public_url"`
	WebAllowedHosts         []string      `yaml:"web_allowed_hosts"`
	WebBasePath             string        `yaml:"web_base_path"`
	AdminBootstrapTokenFile string        `yaml:"admin_bootstrap_token_file"`
}

func Load(path string) (Config, error) {
	data, err := ReadFile(path, 64<<10, false)
	if err != nil {
		return Config{}, err
	}
	c := Config{Listen: "127.0.0.1:8080", ToolTimeout: 5 * time.Minute, ShutdownTimeout: 45 * time.Second, WebEnabled: true}
	d := yaml.NewDecoder(bytes.NewReader(data))
	d.KnownFields(true)
	if err := d.Decode(&c); err != nil {
		return c, errors.New("invalid server configuration or unknown field")
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return c, errors.New("configuration must contain one YAML document")
	}
	if c.DataDir == "" || c.MasterKeyFile == "" {
		return c, errors.New("data_dir and master_key_file are required")
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return c, err
	}
	for _, value := range []*string{&c.DataDir, &c.MasterKeyFile, &c.MCPTokenFile, &c.AdminBootstrapTokenFile, &c.PostgresDSNFile, &c.AdminJWTKeyFile, &c.AdminEncryptionKeyFile, &c.WebTLSCertFile, &c.WebTLSKeyFile} {
		if *value != "" && !filepath.IsAbs(*value) {
			*value = filepath.Join(base, *value)
		}
	}
	if err := c.ValidateDatabase(); err != nil {
		return c, err
	}
	for _, keyFile := range []string{c.MasterKeyFile, c.AdminJWTKeyFile, c.AdminEncryptionKeyFile} {
		if keyFile == "" {
			continue
		}
		relative, err := filepath.Rel(c.DataDir, keyFile)
		if err != nil {
			return c, err
		}
		if relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return c, errors.New("deployment keys must be stored outside data_dir")
		}
	}
	if c.ToolTimeout <= 0 || c.ShutdownTimeout <= 0 {
		return c, errors.New("server timeouts must be positive")
	}
	if c.WebEnabled {
		if _, err := c.WebOptions(); err != nil {
			return c, err
		}
	}
	return c, nil
}

func (c Config) ValidateDatabase() error {
	switch c.DatabaseDriver {
	case "", "sqlite":
		if c.PostgresDSNFile != "" {
			return errors.New("postgres_dsn_file requires database_driver: postgres")
		}
	case "postgres":
		if c.PostgresDSNFile == "" {
			return errors.New("postgres_dsn_file is required for PostgreSQL")
		}
	default:
		return errors.New("database_driver must be sqlite or postgres")
	}
	return nil
}

func ReadFile(path string, limit int64, private bool) (_ []byte, retErr error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect deployment file: %w", err)
	}
	if !info.Mode().IsRegular() || private && info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("deployment file must be regular, with mode 0600 for secrets")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open deployment file: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, f.Close()) }()
	opened, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect opened deployment file: %w", err)
	}
	if !os.SameFile(info, opened) || !opened.Mode().IsRegular() || private && opened.Mode().Perm()&0077 != 0 {
		return nil, errors.New("deployment file changed while opening or has unsafe permissions")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		clear(data)
		return nil, errors.New("deployment file exceeds size limit")
	}
	return data, nil
}

func (c Config) Vault() (*secure.Vault, error) {
	data, err := ReadFile(c.MasterKeyFile, 256, true)
	if err != nil {
		return nil, err
	}
	defer clear(data)
	key := make([]byte, 32)
	defer clear(key)
	encoded := bytes.TrimSpace(data)
	if len(encoded) != 64 {
		return nil, errors.New("master key file must contain exactly 64 hexadecimal characters")
	}
	n, err := hex.Decode(key, encoded)
	if err != nil || n != 32 {
		return nil, errors.New("master key file must contain exactly 64 hexadecimal characters")
	}
	return secure.New(key)
}

func (c Config) HTTPOptions() (mcpruntime.HTTPOptions, error) {
	options := mcpruntime.DefaultHTTPOptions()
	options.Listen, options.PublicURL = c.Listen, c.PublicURL
	options.StateDir = filepath.Join(c.DataDir, "transfers")
	options.AllowedHosts, options.AllowedOrigins = c.AllowedHosts, c.AllowedOrigins
	options.ToolTimeout, options.ShutdownTimeout = c.ToolTimeout, c.ShutdownTimeout
	data, err := ReadFile(c.MCPTokenFile, 4098, true)
	if err != nil {
		return options, err
	}
	defer clear(data)
	options.Token = string(bytes.TrimSpace(data))
	return options, nil
}
