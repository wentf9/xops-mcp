# Roadmap and acceptance criteria

The shared core uses a fixed remote version, and M2 implements SQLite and the standalone HTTP MCP server. See the [server guide](server.md), [dependency baseline](../reuse-baseline.md) and [interface contracts](interface-decoupling.md). Web management and PostgreSQL remain later milestones.

M0 bootstrap, M1 consumer integration and M2 standalone service are implemented. The upstream pin includes the published host-key algorithm negotiation repair; its source and acceptance are recorded in the dependency baseline. M3–M4 describe planned product work and dependency order, not delivery dates.

## M0: repository and verifiable reuse baseline

Owner: `xops-mcp`.

- Independent Go module, MIT license, bilingual documentation, contributor instructions, and CI.
- A remotely available upstream pin without local replacements.
- Exported API composition for HTTP authentication, MCP initialization, tool discovery, synthetic inventory lookup, and cleanup.
- Dependency checks excluding CLI/TUI packages and documentation of remaining configuration/credential coupling.
- Ownership, upstream contract changes, storage boundaries, and phased acceptance criteria.

Evidence: [dependency baseline](../reuse-baseline.md). This phase provides no server executable.

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

M2 management uses offline imports. Live updates are verified through the service contract but have no Web/API endpoint yet. The server guide specifies CLI import fields and unresolved credential handling. Native Linux tests are distinct from platform graphs/cross-builds; M2 does not claim native Windows/macOS service acceptance, deployed-host validation or Web delivery.

## M3: Web management and deployment

Owner: `xops-mcp`.

Add administrator bootstrap/login/session invalidation and CSRF protection; inventory, identity, tag, credential, policy, connection-test, and host-trust management; revision/ETag edits; metadata-only credential reads; audit queries; task and revocation semantics; embedded Web assets; container/volume, backup/restore, and proxy examples.

Acceptance: browser CRUD, concurrent edit conflicts, MCP visibility after Web edits, secret redaction, administrator/MCP authentication separation, persistence after restart, and no accidental retargeting of in-flight work.

## M4: PostgreSQL

Owner: `xops-mcp`.

Implement PostgreSQL-specific migrations and queries. Run shared business contracts against both backends and backend-specific locking/migration tests. Moving existing SQLite data requires explicit offline export/import and verification; changing the connection string does not migrate data.

Acceptance: equivalent data behavior, rollback, optimistic concurrency, migration failure recovery, backup/restore, credential-key availability, and connection-pool cleanup.

PostgreSQL remains single-instance. High availability, distributed scheduling, shared sessions, multi-tenancy, and a separate core repository are outside the delivery commitment.

## Continuing release requirements

Update tests and corresponding documentation with code changes. Before code pushes or PRs, pass `go build ./...`, `go test ./...`, and `golangci-lint run ./...`; verify modified lint configuration. Record real-host validation, unit tests, cross-builds, and native execution separately.

Fix shared bugs with upstream regression tests, then upgrade the server dependency. Fix database/Web bugs here. Consumer probes do not replace upstream tests or complete product acceptance.
