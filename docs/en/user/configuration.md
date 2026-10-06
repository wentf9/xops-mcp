# Configuration and access addresses

[简体中文](../../user/configuration.md) · [User guide](README.md)

Use `--config` to select a configuration file. Restart after editing it. Paths inside the file are relative to its directory; command-line input/output paths are relative to the terminal's current directory.

## Storage and keys

| Setting | Purpose |
| --- | --- |
| `data_dir` | Required; stores data and transfer records, with directory mode `0700` |
| `database_driver` | `sqlite` (default) or `postgres` |
| `postgres_dsn_file` | Required for PostgreSQL; private database connection file |
| `master_key_file` | Required data master key; saved remote credentials cannot be recovered without it |
| `mcp_token_file` | Optional private file for an initial MCP token; normally create tokens in the console |
| `admin_jwt_key_file` | Required for management; login-signing key |
| `admin_encryption_key_file` | Required for management; generate with `keygen --type rsa` |
| `admin_bootstrap_token_file` | Optional setup code for first-time administrator creation |

Keys, connection files and password-containing input files require mode `0600` and must be readable by the service account. Never reuse master, MCP access or management-signing keys. Keep the master and both management keys outside `data_dir`. Without a setup code, create the administrator using an [offline command](maintenance.md).

## Separate access points

| Setting | Default or meaning |
| --- | --- |
| `listen` | MCP listener, default `127.0.0.1:8080` |
| `public_url` | Actual MCP client scheme, hostname and port, without `/mcp` or another path |
| `allowed_hosts` | Optional additional MCP hostnames and ports |
| `allowed_origins` | Optional additional MCP browser origins |
| `web_enabled` | Default `true`; disabling removes the management page |
| `web_listen` | Management listener, default `127.0.0.1:8081` |
| `web_public_url` | Browser-facing scheme, hostname and port, without a path |
| `web_allowed_hosts` | Optional additional management hostnames and ports |
| `web_base_path` | Empty by default; a prefix such as `/console` |
| `tool_timeout` | Operation timeout, default `5m` |
| `shutdown_timeout` | Shutdown wait, default `45s` |

Wildcard listeners (`0.0.0.0` or `::`) require an explicit public address. MCP and management listener addresses/ports must not overlap. `localhost` or `127.0.0.1` in a public address refers to the visitor's own device; use the server's address for remote access.

For example, LAN HTTP:

```yaml
listen: 0.0.0.0:8080
public_url: http://192.0.2.20:8080
web_listen: 0.0.0.0:8081
web_public_url: http://192.0.2.20:8081
web_base_path: /console
web_tls_enabled: false
```

Replace the example IP. The console is then `http://192.0.2.20:8081/console/` and MCP is `http://192.0.2.20:8080/mcp`. Container deployments must also publish the corresponding host ports.

## HTTPS switch

`web_tls_enabled` defaults to `false`, using HTTP without certificates. Enable native management HTTPS with:

```yaml
web_tls_enabled: true
web_tls_cert_file: /etc/xops-mcp/admin-tls.crt
web_tls_key_file: /etc/xops-mcp/admin-tls.key
web_public_url: https://admin.example.com:8081
```

Use a browser-trusted certificate matching the actual address and a mode-0600 private key. Container paths must reference mounted files; with the `.secrets` mount, use `/run/secrets/xops/admin-tls.crt` and `/run/secrets/xops/admin-tls.key`.

To return to direct HTTP, set `web_tls_enabled: false` and change `web_public_url` to the actual HTTP address. Disabled TLS does not read certificates, so their paths may remain. Login passwords receive additional encryption over both HTTP and HTTPS; HTTP does not encrypt other connection contents.

This switch controls only management. The application's MCP port serves HTTP; use a reverse proxy for MCP HTTPS.

## Reverse proxy

The [Nginx example](../../../examples/deployment/nginx.conf) proxies MCP and management separately. Replace domains and certificate paths; make both public URLs match external addresses.

When the proxy provides management HTTPS, the backend may use HTTP:

```yaml
web_listen: 127.0.0.1:8081
web_public_url: https://admin.example.com
web_base_path: /console
web_tls_enabled: false
```

Preserve Host and the path prefix. For management, `proxy_pass http://127.0.0.1:8081` has no trailing `/` that would remove the prefix. Preserve both `/mcp` and `/v1/transfers/`; proxying only `/mcp` breaks file transfers. No `web_trusted_proxies` or `web_allow_insecure_loopback` setting is needed.
