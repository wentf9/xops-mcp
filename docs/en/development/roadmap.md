# Roadmap and acceptance criteria

The shared core uses a fixed remote version, and M2 implements SQLite and the standalone HTTP MCP server. See the [server guide](../user/install.md), [dependency baseline](reuse-baseline.md) and [interface contracts](interface-decoupling.md). M3 Web management/deployment and M4 PostgreSQL are implemented.

M0 bootstrap, M1 consumer integration and M2 standalone service are implemented. The upstream pin includes the published host-key algorithm negotiation repair; its source and acceptance are recorded in the dependency baseline. M4 implements independent PostgreSQL storage and explicit offline database migration; the sequence expresses dependency order.

## M0: repository and verifiable reuse baseline

Owner: `xops-mcp`.

- Independent Go module, MIT license, bilingual documentation, contributor instructions, and CI.
- A remotely available upstream pin without local replacements.
- Exported API composition for HTTP authentication, MCP initialization, tool discovery, synthetic inventory lookup, and cleanup.
- Dependency checks excluding CLI/TUI packages and documentation of remaining configuration/credential coupling.
- Ownership, upstream contract changes, storage boundaries, and phased acceptance criteria.

Evidence: [dependency baseline](reuse-baseline.md). This phase provides no server executable.

## M1: server-facing shared-core interfaces (integrated)

Owners: upstream public-package changes in `xops-cli`; external-consumer checks in `xops-mcp`.

See [interface decoupling](interface-decoupling.md). Upstream delivers D1 neutral leaves, D2 SSH/SFTP, D3 MCP runtime, D4 dynamic admission, D5 persisted tasks, and D6 consumer/extraction acceptance. The implementation lives in core and CLI adapters consume it directly; old Go API facades are removed without creating a separate repository.

Add execution-service and policy/audit injection with explicit ownership; operation snapshots and version-bound credentials; pre-execution revocation checks; connection generations/invalidation covering shared identities and multi-hop jumps; and explicit server host-trust/private-key integration. Preserve CLI behavior, transport tool sets, and transfer state semantics.

Acceptance: upstream gates and consumer contracts; regressions for editing during execution, rotating credentials after approval, disabling/deleting nodes, and shared identity/jump changes; cancellation, deadlines, idempotent Close, and goroutine cleanup; dependency graphs for Linux/Windows/macOS; and isolated module build/tests using only the core subtree without original-module dependencies or replacements. Publish a fixed upstream version before upgrading the server.

The consumer validates dynamic disablement with an in-memory coordinator, real SSH/SFTP and cleanup against the fixed remote version. M2 adds database and real-protocol product tests; Web editing belongs to M3.

## M2: SQLite and a runnable standalone MCP server (implemented)

Owner: `xops-mcp`, with missing shared capabilities implemented upstream.

Add process lifecycle and configuration, persistent directories, migrations, inventory/credential/policy transactions, import dry-run, version publication and invalidation, HTTP MCP authentication, transfer routes, and the existing dedicated journal. Client-local paths remain client-side; directories are archived by the client.

Acceptance: empty/existing database migrations, restart recovery, version conflicts and publication failures, encrypted-secret rotation, real SSH commands and SFTP uploads/downloads, transfer idempotency/unknown recovery, and unauthorized access rejection.

`cmd/xops-mcp` provides `keygen`, `migrate`, `serve`, `status`, `import` and `recover`. SQLite owns relational entities, stable deployment IDs, versioned AES-GCM ciphertext, historical sources and permanent node tombstones. `internal/service` uses the core coordinator for pre-transaction admission barriers and post-commit publication. `Reconcile` reloads authoritative state and retries publication without replaying the business write.

Tests cover schema upgrades/future-version rejection, rollback/revision conflicts, uncertain commits, publication failure, credential rotation, preservation of admitted targets, deleted-ID reuse prevention, and real local HTTP/SSH/SFTP with encrypted keys, ProxyJump, disablement, trust rejection, transfer idempotency and unknown restart/original-binding verification. Service and consumer tests use race checks and goleak with synthetic deployment-owned credentials.

M2 delivered offline imports and live-update service contracts; M3 now supplies their Web/API entry points. The server guide specifies CLI import fields and unresolved credential handling. Native Linux tests are distinct from platform graphs/cross-builds; M2 does not claim native Windows/macOS service acceptance, deployed-host validation or Web delivery.

## M3: Web management and deployment (implemented)

Owner: `xops-mcp`.

Add administrator bootstrap/login/JWT expiry and same-origin protection; inventory, identity, tag, credential, policy, connection-test, and host-trust management; revision/ETag edits; metadata-only credential reads; audit queries; task and revocation semantics; embedded Web assets; container/volume, backup/restore, and proxy examples.

Acceptance: browser CRUD, concurrent edit conflicts, MCP visibility after Web edits, secret redaction, administrator/MCP authentication separation, persistence after restart, and no accidental retargeting of in-flight work.

The implementation includes SQLite v4 administrator/session and v5 independent-tag/primary-key migrations, Web/offline bootstrap, 15-minute stateless JWTs, configurable HTTP/HTTPS and JWE password encryption, same-origin protection, If-Match management APIs, JWT/revision-bound host-key confirmation, authentication-only SSH tests, cursor audit queries and active-permit views. Management and MCP use separate listeners, with a fixed configurable prefix for the management page, assets and APIs, with JWTs bound to that prefix. Web assets are embedded directly; deployment examples cover Docker/Compose, systemd, Nginx and offline backup/restore.

Isolated SSH fixtures drive browser setup/login, CRUD, trust confirmation, real connections, edit conflicts, XSS escaping, tags/policy/audit, mobile layout and logout. Go integration tests cover live Web updates in existing MCP sessions, credential rotation, authentication isolation, migrations/restarts, and unknown-task/original-binding recovery from a backup copy. Production hosts and native Windows/macOS execution are not part of this evidence. Active operations track permits, not a replacement transfer journal. See the [Web console guide](../user/console.md).

## M4: PostgreSQL (implemented)

Owner: `xops-mcp`.

Implement PostgreSQL-specific migrations and queries. Run shared business contracts against both backends and backend-specific locking/migration tests. Moving existing SQLite data requires explicit offline export/import and verification; changing the connection string does not migrate data.

Acceptance: equivalent data behavior, rollback, optimistic concurrency, migration failure recovery, backup/restore, credential-key availability, and connection-pool cleanup.

The implementation includes independent PostgreSQL v1/v2 migrations, JSONB/BYTEA/identity queries, repeatable-read snapshots, revision transactions, an advisory lock on a pinned physical session, a local journal lock, and shutdown on ownership-connection loss. Configuration selects `database_driver` and a private `postgres_dsn_file`; SQLite remains the default. Versioned encrypted `db-export`, `db-import` and `db-verify` archives preserve deployment identity, historical credentials/sources, tombstones, administrator and audit. Restore is atomic and requires an empty target; sessions are excluded and journals/keys are backed up separately.

Shared storage contracts cover equivalent behavior, rollback, concurrent revisions, audit/administrator credentials and all four archive directions. The same service, management API, HTTP MCP, real SSH/SFTP/ProxyJump and unknown-recovery tests run on both backends. PostgreSQL-specific tests cover empty/existing schemas, failed migration rollback/retry, future-schema rejection, ownership across directories, cancelled lock waits, wrong keys and pool/listener cleanup. CI supplies PostgreSQL 18 and a two-backend race matrix. See the [PostgreSQL guide](../user/postgresql.md) for operation and backup. Evidence uses isolated Linux fixtures, excluding production hosts, native Windows/macOS and HA failover. Browser automation uses SQLite; PostgreSQL uses the same Go API acceptance suite.

PostgreSQL remains single-instance. High availability, distributed scheduling, shared sessions, multi-tenancy, and a separate core repository are outside the delivery commitment.

## Administrator authentication evolution (implemented)

Administrators use 15-minute JWTs with client-side logout and natural expiry after password changes. Verification never accesses session storage. Setup, login and password changes use standard JWE over configurable HTTP/HTTPS; 90-second signed challenges bind purpose and the password-change JWT. SQLite v6 / PostgreSQL v3 remove legacy sessions. Authentication instances share deployment ID, prefix and external keys; database locks, runtime state and the journal still keep the product single-instance. See [authentication contracts](admin-auth.md) for details and acceptance.

## MCP Token management (implemented)

The console manages multiple tokens with digests, record versions and separate client IDs, supporting creation, editing, independent node scopes, expiry, disablement and permanent revocation. SQLite v8 / PostgreSQL v5 and format 3 encrypted archives preserve identities, state and node bindings. An injectable core verifier isolates client sessions, operation bindings and file tasks. See [MCP client authentication](mcp-auth.md). Node lists and tool admission enforce token scopes; transfer-content and upload-commit admission recheck current authority. Existing tokens default to all nodes. Credential rotation and immediate identity revocation of running work remain future work.

## Continuing release requirements

Update tests and corresponding documentation with code changes. Before code pushes or PRs, pass `go build ./...`, `go test ./...`, and `golangci-lint run ./...`; verify modified lint configuration. Record real-host validation, unit tests, cross-builds, and native execution separately.

Fix shared bugs with upstream regression tests, then upgrade the server dependency. Fix database/Web bugs here. Consumer probes do not replace upstream tests or complete product acceptance.
