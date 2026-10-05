# PostgreSQL and offline database migration

M4 provides PostgreSQL and the offline `db-export`, `db-import`, and `db-verify` commands. SQLite remains the default. Both backends share Web, MCP, business services and credential encryption; PostgreSQL owns independent migrations and queries. Acceptance uses PostgreSQL 18 on Linux. The server remains single-instance with a local journal in `data_dir/transfers/`.

## Configuration and initialization

Create a dedicated database and a role that owns the database and its `public` schema. The role needs schema/table/index/sequence creation, alteration and business-data access. The application does not create databases or require superuser/CREATEDB privileges. Do not share the database with another application. Use a direct connection or a proxy preserving physical sessions; PgBouncer transaction/statement pooling is unsupported.

Start with the [PostgreSQL example](../../examples/postgres.yaml):

```yaml
database_driver: postgres
postgres_dsn_file: postgres.dsn
data_dir: postgres-data
master_key_file: master.key
admin_jwt_key_file: admin.jwt.key
admin_encryption_key_file: admin.encryption.key
mcp_token_file: mcp.token
web_enabled: true
web_tls_enabled: false
web_listen: 127.0.0.1:8081
web_public_url: http://127.0.0.1:8081
listen: 127.0.0.1:8080
public_url: http://127.0.0.1:8080
tool_timeout: 5m
shutdown_timeout: 45s
```

Paths are relative to the configuration file. `postgres.dsn` must be a mode-0600 regular file containing one URL, for example:

```text
postgresql://xops:URL_ENCODED_PASSWORD@db.example.com:5432/xops?sslmode=verify-full&sslrootcert=/etc/xops-mcp/postgres-ca.pem
```

Provide host, user, database and password explicitly; external authentication uses an explicit empty password, `user:@host`. Percent-encode password characters as needed. TLS defaults to `verify-full`. Allowed query parameters are `sslmode`, `sslrootcert`, `sslcert`, `sslkey` and `sslpassword`; supply absolute deployment-owned TLS file paths. Isolated loopback tests may explicitly use `sslmode=disable`. Personal `.pgpass` and service files are not read; startup rejects `PGSERVICE`. Connection parsing and connection-failure diagnostics do not print the URL or password.

The master key remains a deployment file outside the database. MCP tokens and administrator passwords remain independent. See the [server guide](server.md) to prepare keys and tokens. PostgreSQL does not remove the local directory or journal backup requirements.

```sh
bin/xops-mcp migrate --config .local/postgres.yaml
bin/xops-mcp status --config .local/postgres.yaml
bin/xops-mcp serve --config .local/postgres.yaml
```

`migrate` and `serve` initialize/upgrade schemas transactionally. Inspection and archive commands require an initialized database/local directory and never upgrade it. PostgreSQL has its own `schema_version`, currently v3, unrelated to SQLite v6. Failed migrations roll back completely and can be retried after resolving the cause. Future schemas are rejected. Migration, inspection and server startup all verify the deployment master key.

A local file lock protects the journal; a database advisory lock excludes instances using another local directory or machine. Queries run serially on the pinned physical session holding that lock. Read snapshots use repeatable read; writes use transactions and revision preconditions. Inventory, relations, sources, historical credentials and restored audit events use multi-row writes bounded by row count, parameter count and payload size. Tombstones and immutable credential versions use set-based checks to avoid per-row network round trips. The pool has at most one connection and never silently replaces the ownership session. SQL statement timeout is 5 seconds and lock timeout is 2 seconds; context cancellation sends a query-cancellation request and rolls back with a separate bounded cleanup context, preserving a healthy ownership session. Network failure still closes the connection after a one-second grace period. A one-second heartbeat detects connection loss, stops admission, cancels permits, shuts down listeners and releases the pool. Query cancellation that invalidates the connection also requires restarting the service. Restart reloads committed state without replaying uncertain business writes or remote file commits.

## Moving between SQLite and PostgreSQL

Changing configuration or the connection URL does not move data. Stop the single instance throughout export, import, verification and journal copying. The versioned AES-GCM archive requires the original master key. It includes deployment ID, revision, policy, inventory/relations, unused tags, tombstones, all historical encrypted credentials and source bindings, administrator password hash, and audit IDs/events. Export and import validate decryption of every historical credential. JWTs are not stored in the database; continued validity depends on restored deployment ID, management prefix, JWT key and expiry.

1. Export and verify the source database. Separately back up its complete data directory, master key, MCP token, JWT/RSA authentication keys and configuration.

   ```sh
   bin/xops-mcp db-export --config .local/sqlite.yaml --file /secure-backups/database.xops
   bin/xops-mcp db-verify --config .local/sqlite.yaml --file /secure-backups/database.xops
   ```

2. Configure the target with the original master key and MCP token, a separate private data directory and an empty database. Initialize, preview, apply and verify:

   ```sh
   bin/xops-mcp migrate --config .local/postgres.yaml
   bin/xops-mcp db-import --config .local/postgres.yaml --file /secure-backups/database.xops
   bin/xops-mcp db-import --config .local/postgres.yaml --file /secure-backups/database.xops --apply
   bin/xops-mcp db-verify --config .local/postgres.yaml --file /secure-backups/database.xops
   ```

   Without `--apply`, import validates the archive and reports domain, revision, node count and a logical-data SHA-256 without modifying the target. Applying checks that the target is empty and restores all records in one transaction; populated targets cannot be overwritten. Import automatically verifies equivalence afterward. Verification covers business data, history, ciphertext, administrator and audit, excluding sessions/journal. If commit acknowledgement or verification fails, inspect target state and run `db-verify` before considering a retry.

3. Copy the entire source `transfers/` directory to target `data_dir`, preserving contents, permissions and ownership, including completed records. Inspect offline, then start only the target:

   ```sh
   bin/xops-mcp recover --config .local/postgres.yaml
   bin/xops-mcp serve --config .local/postgres.yaml
   ```

Preserved deployment IDs, credential versions and source bindings keep unknown states and destination locks usable with their original bindings. `db-verify` checks neither the journal nor remote files. Use `recover --id ID --verify` where needed; unknown commits must never be retried automatically. Rollback requires stopping the target and accounting for data and remote operations since cutover before restoring a matching backup set. Never run both copies.

The same commands support PostgreSQL-to-SQLite migration and same-backend recovery. Export creates a new mode-0600 file and refuses overwrites; reads require a private regular file. stdin/stdout archives are unsupported. Archives are capped at 256 MiB and processed in memory; use native database backup for larger deployments. Archives exclude the master key, MCP token, connection file and journal.

## Native PostgreSQL backup

With the service stopped, use `pg_dump --format=custom` and restore into an empty database with `pg_restore --no-owner --no-privileges`, making the target role own tables and sequences. Database administrators manage roles, permissions and TLS separately. Use deployment-managed private client configuration for connection credentials rather than command logs. The database, entire local journal, master key, MCP token and service configuration must belong to the same offline backup. Run `migrate` and offline `recover` before starting one restored instance. A PostgreSQL dump alone is insufficient without the journal and keys.

## Development validation

Tests require an explicitly configured disposable PostgreSQL server and a role with CREATEDB. Every fixture creates a separate random database and drops it during cleanup. Never point this setting at production.

```sh
export GOWORK=off
export XOPS_TEST_POSTGRES_DSN='postgres://postgres:synthetic-test-password@127.0.0.1:5432/postgres?sslmode=disable'
XOPS_TEST_BACKEND=sqlite go test -race -count=1 -timeout=120s ./...
XOPS_TEST_BACKEND=postgres go test -race -count=1 -timeout=120s ./...
go build ./...
golangci-lint run ./...
```

Without a test DSN, PostgreSQL-specific tests skip. Explicit `XOPS_TEST_BACKEND=postgres` without a DSN fails, as do connection failures when configured. CI supplies PostgreSQL 18 for both backend matrix entries. Shared acceptance covers service publication, Web/MCP visibility, real local SSH/SFTP/ProxyJump, credential rotation, audit, JWTs, unknown-task recovery and original-binding verification after SQLite-to-PostgreSQL migration. Storage contracts cover rollback, optimistic concurrency, all four archive directions and credential availability. Backend-specific tests cover migration failure recovery, locks, cancellation and connection cleanup. Latency regressions use a real PostgreSQL TCP proxy adding 3 ms response latency to verify saves, edits and restores (including credential history/audit) at 400 and 4096 nodes within the existing five-second operation budget. Additional cases cover full rollback after a late batch failure, large credential payloads and permanent tombstones after clearing inventory. Isolated Linux acceptance also verifies initialization/import as a non-superuser database owner and archive equivalence after native `pg_dump`/`pg_restore`. Browser automation continues to use SQLite; PostgreSQL Web behavior uses the same Go API integration tests. This evidence excludes production hosts, PostgreSQL failover and native Windows/macOS execution.
