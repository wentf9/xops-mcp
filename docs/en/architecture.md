# Architecture and cross-repository reuse

Status: selected implementation plan. Proposed interfaces and product features below are not implemented yet.

## 1. Product boundaries

`xops-cli` retains CLI/TUI interaction, local YAML and credential stores, OpenSSH integration, and existing MCP entry points. `xops-mcp` owns the long-running server, web console, management API, business database, server credentials, and deployment.

The products release independently. The server requires Go 1.26+, keeps frontend and backend in one repository, and plans to embed built web assets with `go:embed`. The initial target is a single Linux instance using Streamable HTTP. Existing upstream stdio behavior remains compatible; HTTP tunnels and SOCKS tools are outside this scope.

## 2. Decision: one-way module dependency and one shared implementation

```text
xops-mcp server and adapters ──pinned version──> xops-cli/pkg/mcpserver
                                                         │
xops-cli command entry points ────────────────────────────┤
                                                         ▼
                                             pkg/ssh + pkg/sftp
```

The shared core remains in the `xops-cli` Go module for now. Independent server entry points, storage, web code, and releases establish the new product boundary; versioned imports share protocol and execution behavior.

Do not copy the shared MCP/SSH/SFTP implementations, use Git submodules, or commit sibling-directory replacements. Upstream source, modules, and normal CI must not depend on the new server repository.

A third `xops-core` module/repository can be reconsidered after package decoupling and a concrete need such as another consumer, separate ownership, or release contention. Both products would then depend on that neutral module without a reciprocal dependency phase.

## 3. Ownership and imports

| Capability | Source owner | Server integration |
| --- | --- | --- |
| SSH, ProxyJump, privilege execution, cancellation | `xops-cli/pkg/ssh` | Public interfaces |
| SFTP operations and transfer mechanics | `xops-cli/pkg/sftp` | Public interfaces with existing timeout/permission semantics |
| MCP schemas, tools, guardrails, protocol behavior | `xops-cli/pkg/mcpserver` and subpackages | Shared Runtime and proposed neutral injection points |
| HTTP transfer idempotency and recovery | `xops-cli/pkg/mcpserver/transfer` | Existing local journal first |
| Local YAML, CLI credential stores, terminal UI | `xops-cli` | No command imports or subprocess wrapping |
| Web, management API, database models/migrations | `xops-mcp` | Server-owned services |
| Database-to-core adapters | Planned `xops-mcp/internal/adapters` | Consumer implementations of shared contracts |

Shared public entry points are `pkg/mcpserver` and its public subpackages, `pkg/ssh`, `pkg/sftp`, and `pkg/logger`. Transitional adapters and compatibility tests may use `pkg/config`, `pkg/models`, `pkg/credential`, `pkg/adapter`, and `pkg/utils/concurrent`. Their types must not become database entities or Web API DTOs.

Do not import upstream `cmd`, `cmd/sftpshell`, `pkg/tui`, or upstream `internal` packages directly. Upstream packages may legally depend on their own internal packages; that does not prove a fully decoupled core.

## 4. Existing seams and coupling

The pinned source and verification scope are recorded in the [baseline](../reuse-baseline.md).

- Exported Runtime construction, configuration/credential injection, HTTP handler access, and Close allow external composition.
- SSH already exposes separate `ConnectionProvider`, `SecretResolver`, and `CredentialRecorder` interfaces.
- Runtime still constructs its connector and consumes `config.ConfigProvider`, full configuration snapshots, and the existing adapter. Complete service injection is not available.
- Several configuration queries lack context. Do not perform unbounded database I/O in those methods; obtain data through bounded service calls or new context-aware ports.
- SSH errors still depend on configuration, credential recovery depends on the credential package, and Windows input uses upstream terminal internals. The graph still includes local configuration and encrypted-store support.
- HTTP inventory/OpenSSH snapshots and guardrail configuration are captured at startup. Reusing the HTTP handler does not enable live Web edits.
- Connection reuse is keyed by node identity and needs version-aware invalidation when targets, identities, secrets, jump chains, or host trust change.
- Host trust currently uses a local known-hosts file. An isolated service-owned file is an initial option; database-backed trust needs an explicit interface. Encrypted database private keys also need a loading interface or controlled file adapter.

Resolve these boundaries through upstream interfaces instead of source copies or simulated CLI configuration files.

## 5. Planned shared-core contracts

Keep existing public constructors and CLI behavior compatible while adding:

1. Execution-service injection with explicit owned versus borrowed resource cleanup.
2. One immutable operation view covering node, identity, the entire jump chain, and versions across approval, secret resolution, and execution.
3. Current policy checks for new operations and a pre-execution check for disablement, revocation, and version changes after approval.
4. Connection generations covering target, identity/secrets, jump-chain dependencies, and trust. Retire old generations from reuse while active work finishes or follows explicit revocation rules.
5. Secret resolution bound to node, target, purpose, and version; stale requests fail explicitly.
6. Injectable policy retrieval and audit output while retaining one shared decision/approval implementation.

Names and signatures will be finalized in implementation PRs. Removing `Frozen()` alone is insufficient, and recreating the whole Runtime on every edit would disconnect MCP sessions and transfers.

## 6. Planned server structure

```text
cmd/xops-mcp/              entry point and lifecycle
internal/api/             management API and administrator sessions
internal/service/         inventory, credentials, policy, and operations
internal/adapters/xops/    shared-core adapters
internal/storage/         business repositories and transactions
internal/storage/sqlite/
internal/storage/postgres/
internal/migrations/      dialect-specific migrations
internal/compat/          existing external-consumer checks
web/                      source and embedded assets
```

Only `internal/compat` exists today. Web and MCP use the same service rules. The host routes `/mcp` and `/v1/transfers/` to the shared handler, and owns `/api/v1/` plus static Web routes. Administrator sessions, MCP tokens, and short-lived transfer credentials remain distinct.

Shared packages must not import server database code, API handlers, frontend code, ORMs, or server dependency containers.

## 7. Storage and update semantics

SQLite is first, using a persistent local file with foreign keys, WAL, bounded busy waits, transactions, and deadlines. PostgreSQL is the second specific backend; arbitrary SQL compatibility is not promised.

Model hosts, identities, nodes, tags, encrypted credentials and metadata, policies, administrator sessions, and audit events. Nodes use stable opaque IDs; editable addresses, ports, users, and aliases do not define identity. Imports record the mapping from old selectors.

Repositories expose business operations and atomic transactions across referenced entities. Web mutations use revisions/ETags and report conflicts. Avoid a generic table CRUD layer or a single YAML blob.

Publish a new configuration version only after database commit, and acknowledge activation only after publication. If publication fails, block affected new operations while reloading the committed version; do not continue accepting work against stale views or blindly repeat the mutation. Restart reconstructs the current view from the database.

Use versioned authenticated encryption for secrets and private keys, with a deployment-owned master key outside the database and an explicit key backup/recovery procedure. Encrypted storage alone does not solve runtime private-key loading.

Start with one administrator and separate MCP authentication. Web writes require session validation and CSRF protection; credential reads return metadata. Multi-tenancy and OAuth are not default additions.

The existing transfer journal initially remains in a dedicated local directory. Database support does not replace its state machine or ownership. Coordinate database, key, and journal backups. An `unknown` remote commit result must never be automatically retried or declared successful. External storage does not provide shared MCP sessions, SSH connections, or multi-instance journal ownership.

## 8. Versions, development, and release

- Commit exact module versions and checksums. Prefer release tags; use canonical commit-bound pseudo-versions when needed.
- The current pin includes a sudo prompt fix after v0.13.0; it is not a newly published upstream release.
- Local joint development may use a workspace outside both repositories. Never commit it; CI uses `GOWORK=off`.
- Land and validate upstream interface changes first, make their commit remotely available, upgrade the server pin, run consumer/product checks, and then release the server.
- Review API and behavior changes even for v0 upgrades. Breaking stable Go APIs require a new major module path.
- Shared MCP schemas and behavior remain in the shared core. Server management features belong to the Web API.
- Upstream retains its own build/test/lint/platform gates; the new repository tests consumer compatibility and server behavior without copying the upstream suite.

## 9. Migration and acceptance

Keep existing `xops mcp` entry points during bootstrap. Inventory import needs dry-run, conflict reporting, stable-ID mapping, and a database transaction. Credential import must be explicitly authorized and re-encrypted through the new store, rather than copying references that the new server cannot resolve. This is one-way migration, not concurrent writes to shared product data.

Release acceptance includes independent builds; actual MCP calls; live edits for new work; approval/target-change rejection; secret rotation and shared identity/jump-chain invalidation; shutdown; real SSH/SFTP operations; transfer idempotency/unknown recovery; migrations; and revision conflicts. See the [roadmap](roadmap.md).
