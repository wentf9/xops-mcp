# PostgreSQL and database migration

[简体中文](../../user/postgresql.md) · [User guide](README.md)

SQLite needs no separate database service. For PostgreSQL, prepare a dedicated database and an account owning that database and its tables/sequences. Do not share it with other applications. The service neither creates databases nor requires superuser privileges. PostgreSQL still supports only one running XOps MCP instance.

## Configure PostgreSQL

Keep the service configuration, keys and local data directory, and set:

```yaml
database_driver: postgres
postgres_dsn_file: postgres.dsn
```

Save the connection URL in a mode-0600 `postgres.dsn` file:

```text
postgresql://xops:URL_ENCODED_PASSWORD@db.example.com:5432/xops?sslmode=verify-full&sslrootcert=/etc/xops-mcp/postgres-ca.pem
```

Replace the host, user, database and password. URL-encode special password characters. Include the password component explicitly; external authentication may use an empty password as `user:@host`. Certificate paths must be absolute and readable by the service.

Database certificate/hostname validation is enabled by default. Use `sslmode=disable` only when that connection deliberately needs no database TLS. Supported parameters are `sslmode`, `sslrootcert`, `sslcert`, `sslkey` and `sslpassword`. Leave `PGSERVICE` unset; personal `.pgpass` and service files are not used.

Use a direct database connection or a proxy retaining full connections, not PgBouncer transaction/statement pooling. External databases must be reachable from the container; `127.0.0.1` inside a container refers to that container.

For containers, place the file at `.secrets/postgres.dsn`, owned by `65532:65532` with mode `0600`, and configure `/run/secrets/xops/postgres.dsn`.

Initialize an empty database and start:

```sh
xops-mcp migrate --config server.yaml
xops-mcp serve --config server.yaml
```

Container users should use the image commands in the [container guide](container.md). Changing a connection URL does not move existing SQLite data.

## Move between databases

Stop both source and target services for the whole operation and retain a complete source backup. Prepare `source.yaml` for the source and `target.yaml` for an empty target database and separate data directory. Use the same master key, management keys and MCP access credential in the target.

Run from the directory containing these files:

```sh
xops-mcp db-export --config source.yaml --file database.xops
xops-mcp db-verify --config source.yaml --file database.xops
xops-mcp migrate --config target.yaml
xops-mcp db-import --config target.yaml --file database.xops
```

The last command only checks the archive and reports its contents. After confirming that the target is empty, apply and verify:

```sh
xops-mcp db-import --config target.yaml --file database.xops --apply
xops-mcp db-verify --config target.yaml --file database.xops
```

Expect `verified: true`. Populated targets are rejected; do not overwrite them and retry. If applying fails, first run `db-verify` to determine target state.

Then copy the entire source `data_dir/transfers/` directory to the target, preserving ownership and permissions, and start only the target. Archives exclude transfer records, configuration and all deployment keys. Moving a database alone is insufficient. Keep the source service stopped.

Exports are encrypted and require the original master key. Files use mode `0600`, existing outputs are never overwritten, and each archive is limited to 256 MiB. The same commands support PostgreSQL-to-SQLite and same-database recovery.

For container migration, configuration, connection and archive paths must be visible inside the container. Configurations containing key paths and private archives can use the `.secrets` mount, addressed as `/run/secrets/xops/source.yaml`, `/run/secrets/xops/target.yaml` and `/run/secrets/xops/database.xops`. Export needs a writable mount; do not export into the default read-only `.secrets` mount. Export to `/var/lib/xops-mcp/database.xops` and copy it from a stopped container if needed.

## Back up PostgreSQL

Stop XOps MCP, use `pg_dump --format=custom`, and back up the local `data_dir`, configuration and every key. Restore into an empty database as its owner with `pg_restore --no-owner --no-privileges`, then run `migrate` before starting one instance.

The database and local files must come from the same stopped period. Losing the master key prevents credential recovery; missing transfer records lose unfinished-task information. Use native database backup tools for larger databases.
