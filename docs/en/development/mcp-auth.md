# MCP client authentication

[简体中文](../../development/mcp-auth.md) · [Development guide](README.md)

Operator instructions are in [MCP Token management](../user/tokens.md). Administrator JWTs and MCP tokens have independent authentication paths.

## Storage and identity

`internal/mcpauth` manages credentials. `mcp_tokens` stores an ID, client ID, name, display prefix, SHA-256 digest, record version, enabled state, creation time, expiry and revocation time. SQLite schema v7 and PostgreSQL schema v4 implement the same contract. Tokens use system randomness and return their complete value only in the creation response. Neither recoverable plaintext nor ciphertext is retained. Archives include digests and state, preserving all IDs across backend restores.

Console creation assigns independent random token and client IDs. Editing names or expiry does not change identity. Each new token currently creates a new client identity; future client entities, credential rotation and separate permissions can reuse this distinction. Rotation must retain the stored client ID. All clients currently share inventory and policy.

Writes use token-record CAS independently of inventory revisions. Metadata and redacted audit changes commit atomically. Revocation keeps the record permanently retired. Optional `mcp_token_file` provisioning deduplicates by digest without changing an existing record's identity, enabled state or revocation.

On first registration, a file-provisioned token adopts the static-token runtime's SHA-256 scope as its persisted client ID. This preserves journal ownership, request-ID indexes, authorization bindings and unknown-destination locks without rewriting records. Subsequent authentication always reads the stored client ID.

## HTTP integration

The shared core's `HTTPOptions.TokenVerifier` calls `Manager.Verify`, querying storage for every MCP request and rejecting disabled, expired or revoked credentials. It returns `auth.TokenInfo.UserID = client_id`, `Extra["tokenID"] = id` and optional expiry. The deployment file is never an independent authentication bypass.

Database verification has its own admission limit, bounded by core `MaxRequests` (64 by default). Saturation immediately returns HTTP 429 `authentication_limit`. Capacity is held only during verification and released before protocol handling; ordinary requests and control messages retain their separate admission lanes.

The SDK binds sessions to `UserID`. Core carries verified per-request identity from `RequestExtra` into the tool context, exposed through `ClientIdentityFromContext`. Operation bindings, transfer scopes, retries, lookups and cancellation use client IDs. Dynamic authentication fails closed when identity is missing rather than falling back to a global scope.

Revocation controls subsequent MCP requests; already-admitted work is not guaranteed to stop. Transfer routes retain their per-task short-lived credentials, validity and original authorization bindings. Client-specific permissions, identity revocation of running tasks and multiple instances are not implemented.

## Management API

Paths are relative to `<web_base_path>/api/v1/`. Every route requires an administrator JWT; writes require same-origin requests.

| Route | Contract |
| --- | --- |
| `GET /mcp-tokens` | Returns `{tokens: [...]}` containing metadata only |
| `POST /mcp-tokens` | Accepts `name`, `enabled`, `expiresAt`; returns 201 `{item, token}`, showing the secret once |
| `PUT /mcp-tokens/{id}` | Same input, with `If-Match: "<record version>"` |
| `DELETE /mcp-tokens/{id}` | Permanently revokes the credential with a record version; retains identity and audit history |

Times are Unix seconds; zero expiry means unlimited validity. Creation or changing expiry requires a future time. An expired record may retain its old timestamp when disabled. PUT/DELETE return updated metadata and ETag. Missing versions return 428, conflicts or revoked records return 412. The UI never persists MCP secrets in browser storage; closing the one-time display removes its contents, and asynchronous responses are guarded by the administrator authentication generation.

## Validation

`internal/mcpauth` covers multiple credentials, lifecycle, expiry, restart and archives. `internal/server/admin_tokens_test.go` verifies real management/MCP HTTP, client isolation and administrator permission separation. `internal/server/token_history_test.go` uses real HTTP/SSH/SFTP to verify that moving from static-token to database authentication preserves task lookup/retry, client isolation, authorization bindings and unknown-destination locks. These tests run with SQLite and PostgreSQL. Core tests cover request identity, transfer isolation, missing identities and redacted failures. Browser checks exercise HTTP/HTTPS creation, one-time display, enable/disable, revoke and reload.
