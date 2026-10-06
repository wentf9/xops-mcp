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
| `POST /nodes/{id}/test` | Test the currently bound SSH connection |
| `PUT /policy` | Policy management |
| `GET /audit`, `GET /operations` | Audit pagination and active permits |
| `POST /reconcile` | Retry activation of committed configuration |

Tag writes use `{"name":"production"}` and creation returns an `id`; update/delete use `/tags/{id}`. Node DTOs use a `tagIDs` array referencing tag primary keys and no longer accept name-based `tags` writes. Inventory tag records contain `id`, `name` and the associated-node `count`, including unused tags.

Audit accepts `limit` (1–100, default 50), `before`, `nodeID`, `outcome` and `operationID`. The API has no cross-origin CORS access. Write requests require an Origin matching the configured access origin; untrusted Forwarded headers do not establish trust. Login concurrency and per-source attempts are bounded.
