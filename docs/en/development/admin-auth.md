# Administrator authentication contracts

[简体中文](../../development/admin-auth.md) · [Development guide](README.md)

Deployment procedures belong in [user configuration](../user/configuration.md). This page specifies implementation and client-integration contracts.

## JWT

Management accepts exactly one `Authorization: Bearer <JWT>`, never cookies. HS256 uses an independent 32-byte key loaded from a private 64-character hex file. Reject reuse of the master key, raw MCP token bytes or the token's hexadecimal representation. Surrounding whitespace follows actual HTTP token loading.

JWTs last exactly 15 minutes and require `iss`, `sub`, `aud`, `iat`, `nbf`, `exp`, `jti` and `av`. Issuer is `xops-mcp:<domain_id>`; audience is `xops-mcp/admin<web_base_path>`. Only HS256 is allowed. Verify fixed issuer/audience, times and required claims without external JWK lookup.

Verification uses no session table. Login reads the bcrypt administrator hash; password updates also check the administrator version. Logout clears client tokens, old JWTs expire naturally, and restart preserves valid tokens. No refresh tokens or revocation table exist. SQLite v6 / PostgreSQL v3 remove `admin_sessions` while retaining administrator and inventory versions.

## JWE password requests

`GET /api/v1/auth/challenge?action=login|setup|password` returns `publicKey` (RSA JWK), signed `challenge` JWT and `expiresAt`. Password-change challenges require the current administrator JWT.

Challenges last 90 seconds. Audience appends `/request/<action>` to the management audience; subject is the current JWT's hex SHA-256, or the empty string's digest for login/setup. This binds deployment, prefix, action and password-change JWT without single-use nonce storage. Replay within the window is possible; there is no exactly-once claim.

The outer request accepts only `{"ciphertext":"..."}`. Decrypted JSON is `{"challenge":"...","data":{...}}`:

| Action | data fields |
| --- | --- |
| login | `username`, `password` |
| setup | `username`, `password`, `token` |
| password | `current`, `next` |

Algorithms are compact JWE, RSA-OAEP-256 and A256GCM. Use a 256-bit AES key, 96-bit IV and 128-bit tag; the protected header's base64url form is AAD. `kid` is the RSA public key's SHA-256 JWK thumbprint. Reject compression, wrong action, expired challenge and mismatched JWT binding. Server RSA keys are PKCS8, 2048–4096 bits; keygen defaults to 3072 bits.

The browser prefers Web Crypto and uses embedded asmcrypto.js when ordinary HTTP lacks SubtleCrypto. AES keys, IVs and OAEP seeds always use `crypto.getRandomValues`. Embedded assets must match the locked npm distribution and never load from a CDN.

## HTTP and frontend state

`web_tls_enabled` controls only the management listener, default false. Certificates load only when enabled. `web_public_url` describes the external origin, including HTTPS proxies over HTTP backends. There is no forced HTTPS, automatic upgrade or HSTS. Host/Origin, cross-site and If-Match checks remain. Over HTTP, JWE does not authenticate the transport of pages, public keys or JWTs.

The frontend uses `sessionStorage` and `credentials: omit`. Login attempts and token changes advance an authentication generation. Session checks, inventory loads, 401 processing and asynchronous authentication results apply only to their starting generation. Old logout/session responses or errors must not overwrite a newer login.

Instances sharing keys, domain and prefix can verify authentication across instances. The product still has exclusive database ownership, SSH/MCP state, a local journal and process-local host-key observations, so this does not establish horizontal scaling support.

Validation lives in `internal/adminauth`, `internal/config`, `internal/server/admin_security_test.go` and `web/test/regressions.mjs`. See [builds and validation](build-testing.md) for commands.
