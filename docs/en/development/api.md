# Management API

[简体中文](../../development/api.md) · [Development guide](README.md)

See [authentication](admin-auth.md) for JWT/JWE contracts.

## Routes

Administrator routes are under `<web_base_path>/api/v1/` on the management port; table paths are relative to that directory. Writes require same-origin JSON and `Authorization: Bearer <JWT>`. Configuration operations additionally require `If-Match`, using the strong ETag returned by `GET /inventory`, for example `"7"`. Missing preconditions return 428 and stale revisions return 412. Credentials are write-only: reads omit passwords, private keys, passphrases, ciphertext and password hashes.

| Endpoint | Purpose |
| --- | --- |
| `GET` / `POST /mcp-tokens`, `PUT` / `DELETE /mcp-tokens/{id}` | [MCP token management and record versions](mcp-auth.md) |
| `GET /auth/challenge?action=...` | RSA JWK and a 90-second challenge for login/setup/password; password changes require a Bearer JWT |
| `GET /auth/session` | Authentication/setup state and JWT expiry |
| `POST /auth/setup`, `POST /auth/login` | JWE `ciphertext`, decrypting to `username/password`; setup additionally includes `token` |
| `POST /auth/logout`, `PUT /auth/password` | Client logout confirmation or JWE-encrypted `current/next` password change |
| `GET /inventory` | Inventory, credential metadata, tags, policy and publication state |
| `POST /hosts`, `/identities`, `/nodes`, `/credentials`, `/tags` | Create resources |
| `PUT` / `DELETE /{resource}/{id}` | Update or delete a resource |
| `POST /hosts/{id}/probe` | Observe an untrusted host key through a direct connection; optional `algorithm` |
| `POST /hosts/{id}/key-preview` | Preview an independently obtained `hostKey` and its fingerprint without connecting; returns a `probeID` for confirmation |
| `POST` / `DELETE /hosts/{id}/trust` | Confirm the `probeID` returned by direct discovery or pasted-key preview, or revoke trust |
| `POST /credentials/validate-key` | Validate private key content and passphrase validity |
| `POST /nodes/{id}/test` | Test the currently bound SSH connection |
| `POST /nodes/{id}/test-privilege` | Test privilege escalation capability and configuration for an existing node |
| `POST /nodes/privilege-probe` | Probe privilege escalation capability and configuration for draft or new nodes |
| `PUT /policy` | Policy management |
| `GET /audit`, `GET /operations` | Audit pagination and active permits |
| `POST /reconcile` | Retry activation of committed configuration |

Tag writes use `{"name":"production"}` and creation returns an `id`; update/delete use `/tags/{id}`. Node DTOs use a `tagIDs` array referencing tag primary keys and no longer accept name-based `tags` writes. Inventory tag records contain `id`, `name` and the associated-node `count`, including unused tags.

Audit accepts `limit` (1–100, default 50), `before`, `nodeID`, `outcome` and `operationID`. The API has no cross-origin CORS access. Write requests require an Origin matching the configured access origin; untrusted Forwarded headers do not establish trust. Login concurrency and per-source attempts are bounded.

## Node execution configuration

Node create/update DTOs and inventory responses accept optional `execution: {"interpreter":"zsh","launchDialect":"posix","login":false}`. Omitted or null configuration enables runtime discovery. Login distinguishes omitted, true and false. Updates replace the node configuration and use the inventory ETag; invalid execution JSON or settings return 400. Execution settings participate in operation bindings and are cloned for admitted work. Storage migrations are SQLite v9 and PostgreSQL v6; old rows default to null.

`POST /nodes/execution-probe` accepts `{ "nodeID": "optional-existing-ID", "hostID": "...", "identityID": "...", "jumpIDs": [] }` plus the inventory `If-Match`. It returns the core `ProbeResult` (`availableInterpreters`, `suggestedConfig`, `detectedDialect`, etc.) without changing inventory or revision. It uses current draft connection references, pinned trust and versioned credentials; new/disabled targets are allowed for this explicit admin inspection, while disabled jumps are rejected. Only fixed core diagnostics run, within 25 seconds and the shared four-request probe limit. Intent/outcome are audited. Revision is checked before and after network I/O; stale results return 412. Missing trust/credentials or invalid chains return 422, connection/probe errors 502, and timeouts 408. Save the chosen configuration through normal node create/update.

`POST /nodes/{id}/test-privilege` tests the saved node binding. `POST /nodes/privilege-probe` accepts `{ "nodeID": "optional-existing-ID", "hostID": "...", "identityID": "...", "jumpIDs": [], "sudoMode": "none", "privilegeCredentialID": "", "execution": {"interpreter":"sh","launchDialect":"posix"} }` to inspect a draft without saving it. Both require the inventory `If-Match` and return `escalated`, `sudoMode`, `user`, `detail`, and `elapsedMS`. Every successful result requires an actual UID of 0 and a returned username. Omitted, null, or unspecified interpreter/launch settings use the core bounded discovery to select a temporary execution configuration without updating the node; explicit settings are preserved. In `none` mode, diagnostics honor the execution environment and check the login identity before testing the passwordless sudo identity; success of `sudo true` alone is insufficient. Manual edits or closing the form discard stale privilege results. Shell discovery preserves the result when execution settings stay unchanged. If discovery applies different settings and a privilege test was requested for the current form configuration, the browser cancels the old request and automatically rechecks with the new settings. It does not initiate a test that was never requested or repeat one after manual connection/privilege edits.

## Private key validation

`POST /credentials/validate-key` accepts `{ "privateKey": "...", "passphrase": "..." }` and validates the decryption and format of the supplied key material without modifying inventory or revisions. It returns `{ "valid": true/false, "protected": true/false, "keyType": "...", "fingerprint": "...", "message": "..." }`. For passphrase-protected keys (such as encrypted OpenSSH or PEM keys), omitting or providing an incorrect passphrase returns an error message and flags the key as protected; supplying a passphrase for an unencrypted key returns a warning. The normal credential save endpoints (`POST/PUT /credentials`) also enforce this validation, rejecting missing or invalid passphrases with 422.

Browser uploads, validation, and saves consistently trim surrounding whitespace from private keys while preserving passphrases exactly. Validation results are bound to the normalized key, passphrase, credential type, and authentication state. Changes to effective input, replacing an upload, submitting, or closing the dialog cancel validation and discard stale results. Losing focus without changing content preserves the result.

## Message language

The management API selects `zh-CN` or `en` using `Accept-Language` preferences and quality weights, including regional variants. Missing or unsupported languages retain Chinese for existing API clients. Responses include `Content-Language` and `Vary: Accept-Language`. Error codes, HTTP status, field names, audit data, and business records remain unchanged; only server-owned `message`, `warning`, and privilege `detail` text is localized. The frontend explicitly sends its language, including for JWE challenge requests. See [i18n](i18n.md) for catalogs and extension rules.
