# Shared interfaces and server integration

Status: upstream D1–D5 and D6 independent extraction are implemented. This repository pins a remotely downloadable core commit that passed acceptance without replacements. Core tests have moved from testdata to internal/coreconsumer and run in normal CI with dependency checks covering the entire module. See the [dependency baseline](../reuse-baseline.md) for the exact pin and PR links. M2 database adapters and product entry points are implemented; Web management remains planned.

The authoritative shared-source design is `docs/en/development/shared-core-decoupling.md` in [xops-cli](https://github.com/wentf9/xops-cli). This document defines consumer obligations. Database injection alone does not establish an independently maintainable shared core.

## 1. Target dependency

```text
xops-mcp services/storage
       -> internal/adapters/xops
       -> pinned github.com/wentf9/xops-cli/core/mcp/runtime
       -> shared core/mcp/sshexec, core/ssh, sftp, auth, log
```

Upstream auth, log, concurrent, ssh, sftp, and MCP policy/guardrail/transfer/tunnel/remotefile/ports/sshexec packages now exist; runtime composition has also migrated. The consumer imports core through a fixed remote version. Upstream consolidates a self-contained core subtree in the existing module. The CLI consumes core directly; obsolete Go API facades are removed while application composition stays outside core. No nested module or third repository is created at this stage.

The server production graph must eventually exclude CLI configuration/models/adapters, vault backends, i18n, TUI, and root terminal internals. Future externalization changes dependency paths in adapters/composition without redesigning server business services.

## 2. Four dependency roles

| Public port | Server implementation | Constraint |
| --- | --- | --- |
| StateSource.List/Resolve | Published inventory, complete destination/jump plans, policy | Context-bound coherent values, no entities/plaintext secrets |
| ExecutionGate.Enter | Same version coordinator as updates | Final recheck and registration; equal nonempty DomainID with StateSource |
| NewBackend(ctx) | Shared sshexec with server credential/trust sources | Runtime owns the returned handle, not the DB |
| AuditSink.Append | Server audit adapter | Intent failure prevents execution; outcome audit failure never retries remote work |

Policy belongs to the operation snapshot. Inspection, execution, transfer start, commit, and recovery permits have different capabilities; inspection cannot acquire a writable SFTP client.

The host owns the database and administrator sessions. Runtime neither understands an ORM nor discovers personal config, keys, known_hosts, or SSH_AUTH_SOCK.

## 3. Authentication and environment

- Implement version-bound SecretResolver; fail on mismatch instead of reading the newest credential.
- Implement KeySource returning a signer/Close lease after database decryption/parsing. Do not write keys into personal directories or expose private paths in inventory/API DTOs.
- Implement HostKeyVerifier for endpoint, trust revision, and presented public key. Web trust enrollment is a separate management action, not a terminal prompt inside MCP.
- Inject logging; public Nop must not initialize CLI color/output. The server supplies no local interactive InputBridge.
- Upstream internal/sshenv and application adapters preserve CLI environment discovery and native Windows input behavior without entering the server graph.

## 4. Update flow

```text
validate Web mutation/revision
  -> affected inventory/identity/jump admission barrier
  -> bounded database transaction
  -> publish coherent state and retire old connection generations
  -> resume admission and return the revision
```

Rollback removes the barrier. A committed-but-unpublished update keeps affected admission blocked while reloading; do not blindly repeat the transaction. Barrier registration uses short in-memory critical sections, never locks held across database/network I/O.

Approval holds no transaction. Target, credential, jump-chain, or policy changes before final admission invalidate the approval binding. Do not execute updated targets under old approval; unrelated node updates need not invalidate it.

Ordinary edits preserve the destination of admitted work. Deletion, disablement, and credential/trust revocation block new admission and cancel cancellable affected work. Already dispatched commands and committing uploads cannot be promised rollback.

## 5. Four distinct identities

| Identity | Purpose |
| --- | --- |
| NodeID | Stable business ID, never reused after deletion |
| TargetID | Canonical address/default port/account resource lock preserving unknown-commit protection |
| ConnectionKey | Scope, complete jump chain, auth/privilege/trust versions |
| Binding | Full input, target set, relevant configuration and policy versions |

Credential rotation changes ConnectionKey, not TargetID. Aliases or different nodes reaching the same destination retain shared write protection. RequestDigest represents request idempotency separately from configuration-dependent Binding.

## 6. Transfers, shutdown, and recovery

Keep the shared local journal first. Inventory in SQL does not imply task storage migration. Validate the binding during prepare, claim, commit, and recovery/cleanup.

Commit reservation orders commit against revocation; send rename only after reservation and durable intent succeed. Cancellation cannot be promised once that phase is reserved; uncertain confirmation remains unknown. The transfer manager owns task state, without a competing database state machine.

Version the new journal while reading old records. Do not replay unbound unfinished records against current inventory; preserve unknown destination locks. Deletion or retargeting must not cause cleanup on a new same-named node.

Shutdown stops management writes and new MCP operations, closes runtimes with bounded commit/journal settlement, then closes audit and database resources. Partial startup also releases acquired backends, journal locks, and listeners.

## 7. Acceptance scenarios

| Scenario | Required result |
| --- | --- |
| Core runtime and server adapters only | No legacy config/models/adapters/vault/terminal UI in the compilation graph |
| Two runtimes | Independent inventory, credentials, logging, and shutdown without environment mutation |
| Edit target after approval | Stale binding, no connection/execution against the new target |
| Shared identity/multi-hop edits | All dependent generations retire; unrelated work is unaffected |
| Display-only changes outside policy | No global pool/approval invalidation |
| Commit succeeds, publication fails | Admission remains blocked and reloads without repeated mutation |
| Revocation races upload commit | Reservation ordering respected; possible commits never reported as cancelled |
| Old/future journal versions | Read compatible records, reject unknown format, preserve unknown locks |
| Audit/connection/construction failures | Deterministic cleanup; no replay after outcome-audit failure |
| Download result | Streamed does not imply client-local promotion |
| Isolated core module | Builds without original source/module/replacements |

Use explicit synchronization barriers, not sleeps or timeout inflation. Report real SSH/SFTP, discovery, cross-builds, and native execution separately.

## 8. Upgrade sequence

1. Upstream D1/D2: neutral leaves and SSH/SFTP boundaries; add independent core probes.
2. D3: shared MCP runtime and CLI host composition; preserve schemas, results, and shutdown.
3. D4/D5: dynamic admission/generations and deferred binding; validate using an in-memory source before requiring a database.
4. D6: isolated extraction passes and consumer tests use public core entry points.
5. Validate a remotely downloadable fixed version without replacements, update go.mod/go.sum and run root gates before starting SQLite and Web product work.

Keep only internal/coreconsumer probes. internal/dependencycheck enforces core-only upstream dependencies for every production/test package on Linux/Windows/macOS and verifies the remote pin. M2 product entry points, storage and adapters are included in the same graph check.

See the [roadmap](roadmap.md) and [architecture](architecture.md).


## Reproducible consumer checks

The root module validates core contracts against the fixed remote pin with `GOWORK=off go build ./...`, `GOWORK=off go test ./...`, and `golangci-lint run ./...`. All production and test dependencies are subject to the core-only boundary.

`python3 scripts/check_core_consumer.py --upstream /path/to/xops-cli` copies internal/coreconsumer into a disposable module and temporarily replaces upstream there. It verifies Linux/Windows/macOS import graphs, race tests on the current platform, lint, real HTTP/SSH commands, binary SFTP roundtrips and dynamic node disablement. No replacement, workspace or rewritten pin enters either repository. Normal Go discovery and CI now include the core tests.

Before upgrading, run `python3 scripts/check_core_consumer.py --version EXACT_VERSION` against the downloadable pin without replacement, update root go.mod/go.sum and run root gates. The current fixed version passed this acceptance; the dependency baseline records its exact commit and validation scope.

TransferSession now supports ReserveCommit(ctx, permit): streaming and commit share the retained transport while using separately admitted phases. v2 journals carry secret-free original authorization and losslessly encoded credential versions. Claim, credential reissue and recovery validate that binding. Old v1 records retain status and unknown locks but cannot acquire remote authority. M2 SQL business storage and adapters persist domain IDs and dependency versions across restart; transfers retain the core journal, and Web management remains planned.
