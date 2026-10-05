# Architecture and cross-repository reuse

The shared core is integrated through a fixed remote version. Normal CI covers core consumers and dependency boundaries. M2 SQLite, the standalone HTTP server and M3 Web management are implemented; PostgreSQL remains planned; see the [dependency baseline](../reuse-baseline.md) and [interface contracts](interface-decoupling.md).

Status: shared interfaces and the M2 standalone service are implemented. Web management and administrator sessions are implemented; PostgreSQL remains planned. See the [server guide](server.md).

Shared implementations now live in the independently extractable `xops-cli/core/` subtree, with direct CLI consumption and application-owned host adapters. All consumer production and test imports use core; old facade probes are removed. See [interface decoupling](interface-decoupling.md) for detailed contracts and acceptance.

## 1. Product boundaries

`xops-cli` retains CLI/TUI interaction, local YAML and credential stores, OpenSSH integration, and existing MCP entry points. `xops-mcp` owns the long-running server, web console, management API, business database, server credentials, and deployment.

The products release independently. The server requires Go 1.26+, keeps frontend and backend in one repository, and embeds static web assets with `go:embed`. The initial target is a single Linux instance using Streamable HTTP. Existing upstream stdio behavior remains compatible; HTTP tunnels and SOCKS tools are outside this scope.

## 2. Decision: one-way module dependency and one shared implementation

```text
xops-mcp core consumers ──pinned version──> core/mcp/runtime
xops-cli entry points ──CLI host adapters─> core/mcp/runtime
                                                  │
                                                  ▼
                                         core/ssh + core/sftp
```

The shared core remains in the `xops-cli` Go module for now. The diagram shows the implemented shared paths; SQLite adapters and the standalone entry point are implemented; Web management is implemented and PostgreSQL remains later work. Independent server entry points, storage, web code, and releases establish the new product boundary; versioned imports share protocol and execution behavior.

Do not copy the shared MCP/SSH/SFTP implementations, use Git submodules, or commit sibling-directory replacements. Upstream source, modules, and normal CI must not depend on the new server repository.

A third `xops-core` module/repository can be reconsidered after package decoupling and a concrete need such as another consumer, separate ownership, or release contention. Both products would then depend on that neutral module without a reciprocal dependency phase.

## 3. Ownership and imports

The table identifies the single source owner. CLI host adapters compose that implementation without re-exporting its API.

| Capability | Source owner | Server integration |
| --- | --- | --- |
| SSH, ProxyJump, privilege execution, cancellation | `xops-cli/core/ssh` | Public interfaces |
| SFTP operations and transfer mechanics | `xops-cli/core/sftp` | Public interfaces with existing timeout/permission semantics |
| MCP schemas, tools, guardrails, protocol behavior | `xops-cli/core/mcp` | Compose runtime and sshexec through public ports |
| HTTP transfer idempotency and recovery | `xops-cli/core/mcp/transfer` | Existing local journal first |
| Local YAML, CLI credential stores, terminal UI | `xops-cli` | No command imports or subprocess wrapping |
| Web, management API, database models/migrations | `xops-mcp` | Server-owned services |
| Database-to-core adapters | `xops-mcp/internal/adapters/xops` | Consumer implementations of shared contracts |

All production and test code uses upstream public `core/*` packages only. Configuration, models, credential stores, and terminal adapters remain outside the consumer graph. `internal/dependencycheck` checks the entire module on Linux/Windows/macOS and verifies the fixed remote version.

Do not import upstream `cmd`, `cmd/sftpshell`, `pkg/tui`, or upstream `internal` packages directly. Upstream packages may legally depend on their own internal packages; that does not prove a fully decoupled core.

## 4. Verified interfaces and host responsibilities

The fixed source and validation scope are recorded in the [baseline](../reuse-baseline.md).

- `core/mcp/runtime.NewRuntime` accepts State, Gate, Backend factory and Audit through `WithDependencies`, including resource cleanup on partial construction failure.
- `core/mcp/state.Coordinator` supplies coherent snapshots, publication barriers and atomic admission. Ordinary edits preserve admitted targets; disablement and revocation cancel affected cancellable work.
- SSH uses complete ConnectionPlan snapshots and versioned leases. The host supplies SecretResolver, KeySource and HostKeyVerifier without implicit personal-directory discovery.
- File tasks retain original authorization, v1/v2 journals and unknown-commit locks; streaming hands authority to commit on the same connection.
- CLI configuration, concrete credential stores, OpenSSH discovery and Windows input bridges remain in application host adapters outside the core compilation graph.
- M2 implements database transactions, stable domains/versions, encrypted credentials and host-trust storage through these interfaces. Product tests use real local protocols; M3 management uses the same service and publication coordinator.

## 5. Implemented shared-core contracts

`xops-cli/core` provides explicit ports consumed directly by CLI and server adapters:

1. Execution-service injection with explicit owned versus borrowed resource cleanup.
2. One immutable operation view covering node, identity, the entire jump chain, and versions across approval, secret resolution, and execution.
3. Current policy checks for new operations and a pre-execution check for disablement, revocation, and version changes after approval.
4. Connection generations covering target, identity/secrets, jump-chain dependencies, and trust. Retire old generations from reuse while active work finishes or follows explicit revocation rules.
5. Secret resolution bound to node, target, purpose, and version; stale requests fail explicitly.
6. Injectable policy retrieval and audit output while retaining one shared decision/approval implementation.

Implemented contracts are StateSource, ExecutionGate, Backend, AuditSink, and the SSH KeySource, HostKeyVerifier, and InputBridge. Pinned-version compilation and behavior tests validate these contracts. Web mutations must use publication coordination; removing `Frozen()` alone is insufficient, and recreating the whole Runtime on every edit would disconnect MCP sessions and transfers.

## 6. Server structure

```text
cmd/xops-mcp/              entry point and lifecycle
internal/api/             management API and administrator sessions
internal/service/         inventory, credentials, policy, and operations
internal/adapters/xops/    shared-core adapters
internal/storage/         business repositories and transactions
internal/storage/sqlite/
internal/storage/postgres/
internal/storage/sqlite/migrations/ embedded SQLite migrations
internal/coreconsumer/    existing core-consumer contracts

internal/dependencycheck/ existing dependency/version checks
web/                      source and embedded assets
```

`cmd/xops-mcp`, `internal/{command,config,server,service,adapters,storage,secure,importer}`, core consumers and dependency checks exist. The API, administrator authentication, active-operation tracking and embedded Web assets are implemented; PostgreSQL remains planned. Web and MCP use the same service rules. Two independent listeners expose separate surfaces: the MCP port routes only `/mcp` and `/v1/transfers/` to the shared handler; the management port owns assets and `/api/v1/` under its configured `web_base_path`. Management defaults to loopback and has independent Host/origin configuration. Administrator sessions, MCP tokens, and short-lived transfer credentials remain distinct.

Shared packages must not import server database code, API handlers, frontend code, ORMs, or server dependency containers.

## 7. Storage and update semantics

SQLite is first, using a persistent local file with foreign keys, WAL, bounded busy waits, transactions, and deadlines. PostgreSQL is the second specific backend; arbitrary SQL compatibility is not promised.

Hosts, identities, nodes, tags, encrypted credentials and metadata, policies and audit events are implemented. M3 adds administrator and session tables. Schema v5 gives independent tags an `id` primary key and unique `name`, with `node_tags(node_id,tag_id)` foreign-key relations. Unused tags persist; renaming retains IDs and associations, and deletion removes only those associations. Nodes use stable opaque IDs; editable addresses, ports, users, and aliases do not define identity. Imports record the mapping from old selectors.

Repositories expose business operations and atomic transactions across referenced entities. M2 imports use expected revisions; Web mutations use revisions/ETags and report conflicts. Avoid a generic table CRUD layer or a single YAML blob.

Publish a new configuration version only after database commit, and acknowledge activation only after publication. If publication fails, block affected new operations while reloading the committed version; do not continue accepting work against stale views or blindly repeat the mutation. Restart reconstructs the current view from the database.

Use versioned authenticated encryption for secrets and private keys, with a deployment-owned master key outside the database and an explicit key backup/recovery procedure. Encrypted storage alone does not solve runtime private-key loading.

M2 uses a separate MCP token. M3 adds a single administrator, session validation and CSRF protection, with metadata-only credential reads. Multi-tenancy and OAuth are not default additions.

The existing transfer journal initially remains in a dedicated local directory. Database support does not replace its state machine or ownership. Coordinate database, key, and journal backups. An `unknown` remote commit result must never be automatically retried or declared successful. External storage does not provide shared MCP sessions, SSH connections, or multi-instance journal ownership.

## 8. Versions, development, and release

- Commit exact module versions and checksums. Prefer release tags; otherwise use canonical pseudo-versions, never floating branches. An unmerged repair may pin an exact commit on a published fix branch; retain that branch until the pin is reachable from a durable ref or has been upgraded. Before removing branches, verify the pin with an empty module cache and `GOPROXY=direct`; existing local or proxy caches do not prove continued source availability.
- The current pin identifies the upstream master commit containing the host-key algorithm negotiation repair and does not depend on retaining the fix branch. See the dependency baseline for its source and validation. A canonical pseudo-version is not a release tag.
- Local joint development may use a workspace outside both repositories. Never commit it; CI uses `GOWORK=off`.
- Land and validate upstream interface changes first, make their commit remotely available, upgrade the server pin, run consumer/product checks, and then release the server.
- Review API and behavior changes even for v0 upgrades. Breaking stable Go APIs require a new major module path.
- Shared MCP schemas and behavior remain in the shared core. Server management features belong to the Web API.
- Upstream retains its own build/test/lint/platform gates and adds an isolated module extraction check using only core; the new repository tests consumer compatibility and server behavior without copying the upstream suite.

## 9. Migration and acceptance

Keep existing `xops mcp` entry points during bootstrap. Inventory import needs dry-run, conflict reporting, stable-ID mapping, and a database transaction. Credential import must be explicitly authorized and re-encrypted through the new store, rather than copying references that the new server cannot resolve. This is one-way migration, not concurrent writes to shared product data.

Release acceptance includes independent builds; actual MCP calls; live edits for new work; approval/target-change rejection; secret rotation and shared identity/jump-chain invalidation; shutdown; real SSH/SFTP operations; transfer idempotency/unknown recovery; migrations; and revision conflicts. See the [roadmap](roadmap.md).
