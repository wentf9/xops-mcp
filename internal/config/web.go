package config

import (
	"errors"
	"net"
	"net/url"
	"path"
	"strconv"
	"strings"
)

type WebOptions struct {
	Listen, PublicURL, BasePath string
	AllowedHosts                []string
}

// NormalizeWebBasePath accepts a fixed URL prefix made of unreserved path
// characters. Encoded separators, traversal, cookie delimiters and wildcards
// are rejected rather than given different meanings by a proxy and the server.
func NormalizeWebBasePath(raw string) (string, error) {
	if raw == "" || raw == "/" {
		return "", nil
	}
	base := strings.TrimSuffix(raw, "/")
	if !strings.HasPrefix(base, "/") || path.Clean(base) != base {
		return "", errors.New("web_base_path must be an absolute path without empty or relative segments")
	}
	for _, c := range base {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("/-._~", c) {
			continue
		}
		return "", errors.New("web_base_path contains unsupported URL characters")
	}
	return base, nil
}

func (c Config) WebOptions() (WebOptions, error) {
	options := WebOptions{Listen: c.WebListen, PublicURL: c.WebPublicURL, AllowedHosts: c.WebAllowedHosts}
	if options.Listen == "" {
		options.Listen = "127.0.0.1:8081"
	}
	host, port, err := net.SplitHostPort(options.Listen)
	n, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || n < 0 || n > 65535 {
		return options, errors.New("web_listen requires a host and numeric TCP port")
	}
	if options.Listen == c.Listen && n != 0 {
		return options, errors.New("web_listen must differ from the MCP listen address")
	}
	if options.PublicURL == "" {
		if host == "" || host == "0.0.0.0" || host == "::" {
			return options, errors.New("web_public_url is required for a wildcard web_listen address")
		}
		options.PublicURL = "http://" + options.Listen
	}
	u, err := url.Parse(options.PublicURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return options, errors.New("web_public_url must be an HTTP(S) origin; configure its path separately with web_base_path")
	}
	options.BasePath, err = NormalizeWebBasePath(c.WebBasePath)
	return options, err
}
