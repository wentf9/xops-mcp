# Container deployment

[简体中文](../../user/container.md) · [User guide](README.md)

Use Linux, Docker Engine running as a system service, Docker Compose and an account with `sudo` access. Run these commands from the XOps MCP directory containing `Dockerfile` and `examples/deployment/`.

## 1. Prepare the image

```sh
docker compose -f examples/deployment/compose.yaml build --pull
```

All subsequent XOps commands run using this image.

## 2. Create keys for a new installation

The program runs as UID/GID `65532:65532`. Prepare an accessible directory:

```sh
sudo install -d -m 0700 -o 65532 -g 65532 examples/deployment/.secrets
```

Generate separate data, management signing, password encryption and administrator setup keys:

```sh
docker run --rm --network none --mount "type=bind,src=$PWD/examples/deployment/.secrets,dst=/secrets" xops-mcp:local keygen --out /secrets/master.key
docker run --rm --network none --mount "type=bind,src=$PWD/examples/deployment/.secrets,dst=/secrets" xops-mcp:local keygen --out /secrets/admin.jwt.key
docker run --rm --network none --mount "type=bind,src=$PWD/examples/deployment/.secrets,dst=/secrets" xops-mcp:local keygen --type rsa --out /secrets/admin.encryption.key
docker run --rm --network none --mount "type=bind,src=$PWD/examples/deployment/.secrets,dst=/secrets" xops-mcp:local keygen --out /secrets/admin.setup
```

Files use mode `0600` and existing files are never overwritten. Keep existing keys when a command reports `file exists`; do not delete the master key just to repeat these commands.

## 3. Set access addresses

Edit `examples/deployment/container.yaml`. For direct HTTP access from the host, use:

```yaml
data_dir: /var/lib/xops-mcp/data
master_key_file: /run/secrets/xops/master.key
admin_jwt_key_file: /run/secrets/xops/admin.jwt.key
admin_encryption_key_file: /run/secrets/xops/admin.encryption.key
admin_bootstrap_token_file: /run/secrets/xops/admin.setup
web_enabled: true
web_listen: 0.0.0.0:8081
web_public_url: http://127.0.0.1:8081
web_base_path: /console
web_tls_enabled: false
listen: 0.0.0.0:8080
public_url: http://127.0.0.1:8080
tool_timeout: 5m
shutdown_timeout: 45s
```

```sh
chmod 0644 examples/deployment/container.yaml
docker compose -f examples/deployment/compose.yaml config --quiet
```

Put key paths, not passwords, in the configuration. `/run/secrets/xops` in the container maps to `.secrets` on the host.

When disabling native HTTPS for direct HTTP access, also change `web_public_url` to the actual HTTP address. If a proxy provides HTTPS, retain its HTTPS address instead; see [addresses and HTTPS](configuration.md).

## 4. Initialize and start

```sh
docker compose -f examples/deployment/compose.yaml run --rm --no-deps xops-mcp migrate --config /etc/xops-mcp/server.yaml
docker compose -f examples/deployment/compose.yaml up -d --no-build
docker compose -f examples/deployment/compose.yaml logs --tail=50 xops-mcp
```

Open `http://127.0.0.1:8081/console/`. Read the setup code:

```sh
sudo cat examples/deployment/.secrets/admin.setup
```

Create the administrator in the browser, then create a client credential on the [MCP Token](tokens.md) page. The MCP address is `http://127.0.0.1:8080/mcp`; see [client connections](console.md).

The example publishes ports only on the host's loopback interface. For LAN access, change both Compose `ports` and the two public addresses. Changing only the container's `web_listen` is insufficient. For example, use `8080:8080` and `8081:8081` and set public addresses to the server's actual address.

## Stop and maintain

```sh
docker compose -f examples/deployment/compose.yaml stop
```

Stop the service before offline commands:

```sh
docker compose -f examples/deployment/compose.yaml run --rm --no-deps xops-mcp status --config /etc/xops-mcp/server.yaml
docker compose -f examples/deployment/compose.yaml up -d --no-build
```

Data is in the Compose named volume; keys are in `.secrets`. Back up both. Do not use `docker compose down -v` as an ordinary stop command. Retain the original Compose project name and file path to avoid creating another empty data volume.
