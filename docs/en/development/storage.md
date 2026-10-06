# Storage and runtime contracts

[简体中文](../../development/storage.md) · [Development guide](README.md)

Operational instructions are in [databases](../user/postgresql.md) and [maintenance](../user/maintenance.md). This repository owns business interfaces/adapters; SSH/SFTP/MCP execution remains an upstream public-core dependency.

## Data and publication

SQLite currently uses schema v7 and PostgreSQL v4, with independent migration histories. Entities include hosts, identities, nodes, tags, credentials, credential_versions, policies, sources, tombstones, admin, mcp_tokens and audit_events. node_jumps preserves ordered jumps; node_tags references stable tag IDs. Legacy administrator sessions have been removed.

Writes use expected revisions. The service creates an admission barrier before persistence and publishes the snapshot after commit. Uncertain commits/publication failures reconcile by reloading committed state rather than replaying writes. Ordinary edits retain admitted targets; disablement, policy and credential revocation follow shared gate contracts.

Credentials are versioned AES-GCM ciphertext with authenticated deployment, ID, kind and version. Historical credentials/sources retain original-target verification; deleted nodes keep permanent tombstones. Secrets must not enter Web DTOs or free-form audit text.

## Backend differences

SQLite enables foreign keys, WAL, FULL synchronization, a two-second busy wait and one connection. Permission checks and a deployment file lock protect both database and local journal.

PostgreSQL owns distinct SQL, JSONB/BYTEA fields and identity sequences. Reads use repeatable read; writes serialize through the deployment revision condition. Queries share one pinned physical session holding an advisory lock, preventing silent ownership changes through pool reconnects. Statement timeout is five seconds and lock timeout two seconds; rollback uses a separate bounded cleanup context.

Inventory rewrites use multi-row INSERT chunks bounded by rows, parameters and payload size. Tombstones and immutable credential versions use set-based checks instead of per-node network requests. Restored history and audit are also batched. Latency regressions cover save/edit/restore at 400/4096 nodes with three-millisecond response delay within the unchanged five-second operation budget.

Losing the ownership connection stops admission/listeners instead of reconnecting into an old snapshot. Close cancels and joins the heartbeat, closes the session/pool, then releases the local lock. An external database does not remove single-instance constraints.

## Offline archives

Only archive format 2 is supported: header `XOPSDB\x02`, authenticated encryption context `xops-mcp database backup v2`, and payload `Format: 2`. Version 1 and other versions are rejected without compatibility reads or conversion. Archives use the deployment master key with a 256 MiB limit. They include inventory, key check, all historical credentials/sources, administrator hash, MCP token records and audit; they exclude configuration, external keys and journal.

Restore requires an initialized empty target. One transaction preserves domain/revision/IDs, data and audit sequence. Historical ciphertext must decrypt, and current sources must match the executable view. Archive validation normalizes absent, null and empty `MCPTokens` to an empty array so restored content hashes agree. Archive verification does not verify the journal or remote files. JWTs are not stored in the database; validity depends on keys, domain, prefix and expiry.

## Validation scope

Shared contracts cover transactions, conflicts, credential immutability, permanent tombstones, administrator versions and all four archive directions on both backends. Product integration includes real local HTTP/SSH/SFTP, encrypted private keys, ProxyJump, Web-to-MCP visibility and unknown recovery. Backend checks cover migration failure, locks, wrong keys, cancellation and cleanup.

Transfers retain the upstream journal. Client-local paths never become server-local paths. Online backup, automatic master-key rotation, distributed task scheduling and multi-instance journal ownership are not implemented. See [builds and validation](build-testing.md).
