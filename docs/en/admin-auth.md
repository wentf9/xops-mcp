# Administrator JWTs and password encryption

Administrator authentication uses `Authorization: Bearer <JWT>` with a 15-minute lifetime. Verification uses only deployment keys, deployment ID, management prefix and token contents; it never reads or writes a session table. Login still checks the stored bcrypt password hash. Administrator and MCP credentials retain separate authority.

## Keys and upgrade

Web management requires two new settings:

```yaml
admin_jwt_key_file: admin.jwt.key
admin_encryption_key_file: admin.encryption.key
```

Paths are relative to the configuration file. Both must be private regular files (0600 or stricter) outside the data directory. JWTs use a separate 256-bit HS256 key, never the master key or MCP token. Startup rejects a match with either the raw MCP token bytes or its hexadecimal representation after trimming surrounding file whitespace. Request encryption uses a PKCS8 RSA private key, supporting 2048–4096 bits; generation defaults to 3072 bits:

```sh
bin/xops-mcp keygen --out .local/admin.jwt.key
bin/xops-mcp keygen --type rsa --out .local/admin.encryption.key
```

For an existing deployment, stop the service and back up the database, journal, keys and configuration. Generate both keys, update configuration and run `migrate`. SQLite v6 / PostgreSQL v3 remove the old `admin_sessions` table while preserving administrator password hashes, deployment ID, inventory revision and historical bindings. Old cookies no longer authenticate; log in again. Use `web_enabled: false` when management is not needed.

Keys are deployment configuration and are excluded from database exports; back them up and distribute them separately. Authentication instances sharing deployment ID, management prefix and keys can verify tokens and decrypt requests issued by another instance. Rotating the JWT key and restarting every instance invalidates old JWTs. RSA key rotation invalidates pending requests using the old public key; clients must fetch a new challenge.

## HTTP / HTTPS configuration

`web_tls_enabled` controls the application's management listener and defaults to `false`. Disabled means HTTP, including remote/LAN access. Certificate/key paths may remain configured but are neither required nor read while TLS is disabled. The application neither requires HTTPS nor redirects to it, and sends no HSTS policy that would pin the browser's protocol.

Direct HTTP:

```yaml
web_listen: 0.0.0.0:8081
web_public_url: http://192.0.2.10:8081
web_tls_enabled: false
```

Native HTTPS:

```yaml
web_listen: 0.0.0.0:8081
web_public_url: https://admin.example.com:8081
web_tls_enabled: true
web_tls_cert_file: /etc/xops-mcp/admin-tls.crt
web_tls_key_file: /etc/xops-mcp/admin-tls.key
```

Enabling TLS requires a matching certificate and private regular key file. The certificate must match the public address and be trusted by clients. TLS 1.2 and later are supported. When disabling native TLS for direct HTTP access, also change `web_public_url` to HTTP. If no public URL is configured, its scheme follows the TLS switch; wildcard listeners still require an explicit public URL.

An external proxy can terminate TLS while the application uses HTTP upstream:

```yaml
web_listen: 127.0.0.1:8081
web_public_url: https://admin.example.com
web_tls_enabled: false
```

`web_public_url` describes the browser-facing origin for Host/Origin checks; it does not enable TLS on the backend. Preserve Host and the path prefix at the proxy. The application does not depend on `X-Forwarded-Proto`. Remove the former `web_trusted_proxies` and `web_allow_insecure_loopback` settings and use `web_tls_enabled` to control native TLS. See the [Nginx example](../../examples/deployment/nginx.conf). Restart the service after configuration changes.

Both HTTP and HTTPS retain JWE password encryption. Browsers use native Web Crypto when available; ordinary HTTP origins without SubtleCrypto use embedded asmcrypto.js. Keys, IVs and OAEP seeds always use the browser's secure random source; no CDN is needed. HTTP does not authenticate or integrity-protect page scripts, public keys or JWT transport; JWE protects the password request contents.

## Request format

Setup, login and password changes use standard compact JWE on both HTTP and HTTPS (RFC 7516), restricted to `RSA-OAEP-256` and `A256GCM`. HTTPS authenticates the server/public key and protects JWTs; JWE protects passwords in proxy/application request records.

1. Fetch `GET <prefix>/api/v1/auth/challenge?action=login|setup|password`. Password changes require the current Bearer JWT. The response contains `publicKey` (RSA JWK), a signed `challenge` JWT and `expiresAt`.
2. Generate a new 256-bit AES key and 96-bit random IV. Encrypt UTF-8 JSON `{"challenge":"...","data":{...}}` with AES-GCM and wrap the AES key with RSA-OAEP-SHA256. The protected header specifies `alg: RSA-OAEP-256`, `enc: A256GCM` and the returned key's `kid`. Its base64url representation is GCM additional authenticated data; use a 128-bit tag.
3. Submit the five-part JWE as the only field: `{"ciphertext":"protected.encryptedKey.iv.ciphertext.tag"}`. Encrypted data contains `username/password` for login, `username/password/token` for setup, or `current/next` for password changes. Direct plaintext fields are rejected.

Challenges expire after 90 seconds and bind deployment, management prefix, purpose and, for password changes, the current JWT. Tampering, compression, wrong keys/purposes and expired ciphertext are rejected. There is no single-use nonce store: requests may replay within their validity window. The short window and login rate limits bound replay; configuring HTTPS additionally protects transport; requests do not claim single-use semantics.

Successful login returns `accessToken`, `tokenType: Bearer`, `username` and `expiresAt`. The frontend stores JWTs only in the current tab's `sessionStorage`, allowing reloads. Requests explicitly set Authorization and `credentials: omit`; authentication cookies and `X-CSRF-Token` are no longer sent. Host, Origin, cross-site restrictions and configuration If-Match checks remain in force.

## Expiry and scaling boundaries

Logout immediately clears the current tab's token and page data. Authentication requests bind to a client authentication generation, so older status checks and errors cannot clear a new token, replace an in-progress login form or overwrite a newer login view. Password changes/offline resets prevent login with the old password; previously issued JWTs remain valid until expiry and are not individually revoked. Restart does not invalidate unexpired JWTs. There are no refresh tokens, revocation tables or sticky sessions; log in again after expiry. Rotate the deployment JWT key and restart to invalidate all existing tokens after a compromise.

JWTs are readable by JavaScript; CSP and output escaping continue to reduce XSS risk. Password plaintext exists only at browser input and after server decryption for bcrypt verification/hashing; it is neither logged nor stored in the database. The password contract remains 12–72 UTF-8 bytes.

Authentication no longer depends on server-side sessions. The overall product remains single-instance: database ownership locks, SSH/MCP runtime state, host-key confirmation records, process-local rate limiting and the local transfer journal still require future scaling work.

Acceptance covers cross-instance signing/decryption, algorithm/key/issuer/audience/expiry rejection, password purpose/JWT binding, legacy session migrations, native TLS enable/disable, direct HTTP and proxy deployments, both database backends and actual encrypted browser requests over HTTPS and ordinary HTTP, reloads and logout.
