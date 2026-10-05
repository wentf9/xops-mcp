# Backups and troubleshooting

[简体中文](../../user/maintenance.md) · [User guide](README.md)

Direct commands below use the `xops-mcp` executable. Container deployments use the same image and `/etc/xops-mcp/server.yaml`; see the [container guide](container.md). Stop the service before offline management commands other than `keygen`.

## Create or reset the administrator

Without Web setup, store a password in a mode-0600 regular file readable by the service account:

```sh
xops-mcp admin-init --config server.yaml --username admin --password-file /secure/admin-password.txt
```

For a forgotten password:

```sh
xops-mcp admin-reset --config server.yaml --password-file /secure/new-admin-password.txt
```

Alternatively, use `--password-stdin`. This container example reads a host-side file without mounting it:

```sh
docker compose -f examples/deployment/compose.yaml stop
docker compose -f examples/deployment/compose.yaml run --rm --no-deps -T xops-mcp admin-reset --config /etc/xops-mcp/server.yaml --password-stdin < /secure/new-admin-password.txt
docker compose -f examples/deployment/compose.yaml up -d --no-build
```

Never place passwords directly in command arguments. Existing logins expire at their original deadline. To end every management login immediately, stop the service, generate a new management-signing key, update `admin_jwt_key_file` and restart. Do not replace the data master key for this purpose.

## Back up and restore

For SQLite, stop and back up the entire `data_dir`, including transfer records. Separately retain configuration, the master key, MCP credential, management keys and any certificates. Copying only `xops.db` is insufficient.

If the direct-run example keeps everything under `xops-instance`, run from its parent directory:

```sh
umask 077
tar -czf "xops-instance-$(date +%Y%m%d-%H%M%S).tar.gz" xops-instance
```

Restore into a new directory with original ownership/permissions and keys, run `migrate`, then start. Never run both copies. [PostgreSQL](postgresql.md) also needs a database backup.

Container backups include both the named volume and `.secrets`. This example locates the correct volume through the stopped service container; do not remove that container first:

```sh
docker compose -f examples/deployment/compose.yaml stop
mkdir -m 0700 backup
docker run --rm --network none --volumes-from "$(docker compose -f examples/deployment/compose.yaml ps -aq xops-mcp):ro" --mount "type=bind,src=$PWD/backup,dst=/backup" busybox:1.37 sh -c 'umask 077; tar -czf /backup/data.tgz -C /var/lib/xops-mcp . && tar -czf /backup/secrets.tgz -C /run/secrets/xops . && cp /etc/xops-mcp/server.yaml /backup/server.yaml'
```

Use a new empty backup directory each time, or safely move old backups first. Outputs belong to the container's administrator account and may require `sudo` to read. Restore `secrets.tgz` into `.secrets`, restore configuration, and extract `data.tgz` into the service volume at `/var/lib/xops-mcp`, preserving ownership and permissions. Run offline `migrate` before starting. Restore only into empty targets, never over active data.

## Unknown file-task results

List tasks in a stopped deployment:

```sh
xops-mcp recover --config server.yaml
```

Replace `TRANSFER_ID` with the task to inspect:

```sh
xops-mcp recover --config server.yaml --id TRANSFER_ID --verify
```

If uncertain, inspect the destination manually instead of repeating the upload. Use `--cleanup` only to remove the task's temporary file. After manual handling, acknowledge it to permit future writes to the destination:

```sh
xops-mcp recover --config server.yaml --id TRANSFER_ID --resolve-unknown --reason 'Destination file checked manually'
```

This never repeats the transfer or changes an unknown result into success. If addresses, credentials or host keys changed, automatic verification may be refused; inspect the original destination manually.

## Troubleshooting

| Message or symptom | Action |
| --- | --- |
| `no such file or directory` | Create the output directory and check current/container paths; key generation does not create parent directories |
| `permission denied` | Check account, ownership and permissions; the image uses UID/GID 65532, directories 0700 and key files 0600 |
| `file exists` | Key generation and database export refuse overwrites; retain keys or choose another export filename |
| `origin_denied` or saves fail | Browser scheme, hostname and port must match `web_public_url`; direct HTTP cannot retain an HTTPS public address |
| HTTPS handshake fails | Check the switch, certificate/key, domain and browser trust; use HTTP when HTTPS is disabled |
| `deployment is already in use` | Stop the running service or other offline command; do not start a second instance |
| Master-key mismatch | Restore the deployment's original key instead of generating another |
| Node cannot enable/connect | Complete credentials and identity, verify host key/jumps and check connectivity |

Read container logs with `docker compose -f examples/deployment/compose.yaml logs --tail=100 xops-mcp`. Keep passwords, keys and access credentials out of public issue reports.
