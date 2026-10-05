# Running directly

[简体中文](../../user/install.md) · [User guide](README.md)

This page is for Linux users who already have an `xops-mcp` executable available on their command path. Otherwise, use [container deployment](container.md).

## Prepare the directory

Install in a new, empty directory:

```sh
mkdir -m 0700 xops-instance
cd xops-instance
umask 077
```

## Create keys and configuration

```sh
xops-mcp keygen --out master.key
xops-mcp keygen --out mcp.token
xops-mcp keygen --out admin.jwt.key
xops-mcp keygen --type rsa --out admin.encryption.key
xops-mcp keygen --out admin.setup
```

Output files must not already exist. Keep every key separate; never reuse the same contents for different purposes.

Save the following as `server.yaml`:

```yaml
database_driver: sqlite
data_dir: data
master_key_file: master.key
mcp_token_file: mcp.token
admin_jwt_key_file: admin.jwt.key
admin_encryption_key_file: admin.encryption.key
admin_bootstrap_token_file: admin.setup
listen: 127.0.0.1:8080
public_url: http://127.0.0.1:8080
web_enabled: true
web_listen: 127.0.0.1:8081
web_public_url: http://127.0.0.1:8081
web_base_path: ""
web_tls_enabled: false
tool_timeout: 5m
shutdown_timeout: 45s
```

Relative paths are resolved from the configuration file's directory. The data directory is created with mode `0700`. Configuration and key files must not be symlinks. Keep the master, management-signing and password-encryption keys outside `data_dir`.

## Start and create the administrator

```sh
xops-mcp migrate --config server.yaml
xops-mcp serve --config server.yaml
```

Leave the terminal running. Open `http://127.0.0.1:8081/` and use the code in `admin.setup` to create the administrator. You may remove that setup file afterward; retain the other keys.

A new service has no nodes. Use the [console guide](console.md) to add and enable nodes before connecting an MCP client. The MCP address is `http://127.0.0.1:8080/mcp`.

Press `Ctrl+C` to stop. Inspect the stopped deployment with:

```sh
xops-mcp status --config server.yaml
```

For LAN access, HTTPS or PostgreSQL, see [configuration](configuration.md) and [databases](postgresql.md).

## Run as a system service

Use the supplied [systemd configuration](../../../examples/deployment/systemd.yaml) and [service unit](../../../examples/deployment/xops-mcp.service). Install the executable at `/usr/local/bin/xops-mcp`, create the `xops-mcp` system account, and put configuration and keys under `/etc/xops-mcp/`. Keys must be owned by that account with mode `0600`.

The example uses Nginx for two domains. Replace `mcp.example.com` and `admin.example.com` with actual addresses and provide certificates. For direct HTTP, use actual HTTP public addresses instead. systemd creates `/var/lib/xops-mcp` for data.

After installing the unit at `/etc/systemd/system/xops-mcp.service`, run:

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now xops-mcp
sudo systemctl status xops-mcp
```

Read logs with `sudo journalctl -u xops-mcp -n 100`. Stop the service before offline commands and run them as `sudo -u xops-mcp xops-mcp ...` to preserve accessible file ownership.
