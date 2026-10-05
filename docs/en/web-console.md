# Web console and deployment

[简体中文](../web-console.md)

The console uses a separate management HTTP listener, defaulting to `127.0.0.1:8081`; MCP defaults to `127.0.0.1:8080`. The management port serves only Web assets and administrator APIs; the MCP port serves only `/mcp` and `/v1/transfers/`. All HTML, CSS and JavaScript are embedded in the Go binary; deployment needs no Node.js, frontend server or CDN. The target is one Linux instance; SQLite and PostgreSQL share this console. Multiple administrators and OAuth remain outside scope.

## Management listener and path prefix

| Configuration | Default and purpose |
| --- | --- |
| `web_enabled` | `true`; `false` disables the management listener |
| `web_listen` | `127.0.0.1:8081`, separate from the MCP `listen` address |
| `web_public_url` | Defaults to the management listen origin; the external HTTP(S) origin without a path. Required for wildcard listen addresses |
| `web_allowed_hosts` | Additional management Host values; independent of MCP `allowed_hosts` |
| `web_base_path` | Empty by default, serving `/`; examples include `/console` or `/platform/ops`. Applies to pages, assets and administrator APIs |

Example behind a reverse proxy:

```yaml
listen: 127.0.0.1:8080
public_url: https://mcp.example.com
web_listen: 127.0.0.1:8081
web_public_url: https://admin.example.com
web_base_path: /console
```

Open `https://admin.example.com/console/`; management APIs are under `/console/api/v1/`. `/console` redirects to the trailing-slash URL. Preserve the prefix and Host at the proxy, using `proxy_pass http://127.0.0.1:8081` without a trailing slash. The prefix comes from configuration, never `X-Forwarded-Prefix`, and does not affect MCP/transfer paths. Path segments accept letters, digits and `-._~`; relative segments, repeated slashes and encoded separators are rejected. Both listeners share bounded shutdown; a bind failure releases the other listener and deployment lock.

Both public origins may use the same hostname. In that case combine the two sets of example `location` blocks into one proxy virtual host, still routing management and MCP paths to their separate upstream ports.

## Initialization and login

With [server.yaml](../../examples/server.yaml), create three separate files:

```sh
bin/xops-mcp keygen --out .local/master.key
bin/xops-mcp keygen --out .local/mcp.token
bin/xops-mcp keygen --out .local/admin.setup
bin/xops-mcp migrate --config .local/server.yaml
bin/xops-mcp serve --config .local/server.yaml
```

Open `http://127.0.0.1:8081/` and enter a username, password and the setup credential from `admin.setup`. The setup credential must differ from the MCP token. Only one administrator can be initialized; later setup attempts cannot overwrite it. Administrator passwords contain 12–72 bytes and are stored as bcrypt hashes.

Initialization and password changes validate UTF-8 byte length, not character count. For example, `管理员新密码` occupies 18 bytes and is valid; 24 common Chinese characters usually occupy 72 bytes. Both frontend and backend reject new passwords below 12 bytes, above 72 bytes, or containing NUL, CR or LF. Confirmation must match exactly.

After initialization, remove `admin.setup` if desired; initialized deployments no longer read it. Without `admin_bootstrap_token_file`, Web setup is disabled. Offline commands remain available:

```sh
bin/xops-mcp admin-init --config .local/server.yaml --username admin --password-file /secure/admin-password.txt
bin/xops-mcp admin-reset --config .local/server.yaml --password-file /secure/new-admin-password.txt
```

Password files require mode 0600; `--password-stdin` is also supported. Passwords are never command-line values. Stop the service first to release its deployment lock. Initialization cannot overwrite an account; reset is for the deployment owner with access to its master key and revokes all sessions.

Administrator cookies are HttpOnly, SameSite=Strict, scoped to `<web_base_path>/api/v1` and valid for 12 hours. An HTTPS `web_public_url` enables Secure cookies. Logout revokes the current session; password changes and server restarts revoke every administrator session. MCP and short-lived transfer credentials cannot log in to the console, and administrator cookies cannot invoke MCP tools.

Links from other websites can open the console home page. The cross-site exception covers only top-level `GET`/`HEAD` document navigation to the configured console root (including its trailing-slash redirect). Host validation and validation of any supplied Origin remain active; cross-site frames, resource fetches and administrator API requests remain denied.

## Manage nodes

1. Create an SSH address/port under hosts.
2. Open the host-key dialog, fetch the key directly or paste an independently verified public key, then review its fingerprint and explicitly confirm it. Direct probing stops before SSH authentication; pasted-key previews make no connection. Neither method creates trust automatically. A confirmation is bound to the administrator session, host and configuration revision, expires in two minutes and is single-use.
3. Create a password or private-key credential, optionally with a passphrase. Reads expose metadata only. Leave secret fields empty to retain existing material during edits.
4. Create an identity linking a remote SSH username to its credential.
5. Create the desired tags on the tag page, then create a node with its host, identity, selected tags and ordered jump chain, then enable it. Connection tests perform SSH handshake/authentication without running a remote command; the node and its jumps must be enabled.

Host, identity, credential, node and tag names, plus node aliases, share one character rule: Unicode letters, decimal digits, ASCII underscore `_` and hyphen `-`. Combining accents and vowel marks may follow letters, supporting accented names and scripts such as Hindi. Values must occupy 1–256 UTF-8 bytes. Whitespace, other punctuation, symbols, emoji, invisible characters and standalone combining marks are rejected. Examples: `运维_生产-01`, `école` and `हिन्दी` are valid; `ops prod`, `ops,prod` and `node.example` are invalid. Names remain unique within each resource type.

The frontend validates during input and submission; the backend applies the same rule to API writes, imports and inventory loading. There are no exceptions for older invalid names or aliases, and invalid values are not automatically trimmed, split or rewritten. Development data must follow the rule too. Enter one node alias per line; empty lines are ignored and an empty field means no additional aliases.

For hosts reachable only through ProxyJump, choose the independently verified key input when first provisioning or rotating trust. Read the host's `.pub` file (for example `/etc/ssh/ssh_host_ed25519_key.pub`) through its console or an already verified management connection, paste one SSH public key, and check the previewed SHA256 fingerprint through a trusted channel before confirming. Private keys, public-key options and SSH certificates are rejected. Changing the source or key content clears the previous preview and confirmation checkbox; preview and confirm again. Select the jump chain in the node editor.

Edits publish through the existing coordinator and affect subsequent calls in existing MCP sessions without rebuilding the runtime. Forms retain their original ETag. Concurrent changes produce a conflict and preserve input; close, refresh and reopen the editor before retrying.

Ordinary endpoint edits never redirect admitted operations. Credential rotation, trust revocation, disablement and policy changes revoke affected cancellable work. Already-admitted upload commits retain their commit deadline; cancellation does not imply remote rollback. Revoking host trust disables directly referencing nodes and makes their dependent jump routes unavailable.

Referenced hosts, identities, credentials and jump nodes cannot be deleted. Deleted node IDs are never reused. Create, rename and delete tags independently on the tag page, including tags with no nodes. Select existing tags in the node editor. Tags have stable opaque primary keys and unique names; renaming preserves their IDs and node links. Deleting a tag removes its associations without deleting nodes. SQLite v5 assigns IDs to existing tags while preserving all associations and unused records. MCP continues to expose tag names.

## Policy, audit and running operations

Policy settings cover global and per-node approval thresholds, behavior when a client cannot confirm, additional blocked command patterns and protected paths. Evaluation uses the shared core.

An imported policy with an omitted or empty global `approval_threshold` uses the core's `dangerous` default, shown as high-risk operations. Saving unrelated policy fields preserves that effective threshold; explicitly configured thresholds retain their values in the editor and on save.

Additional blocked command patterns use the core's `filepath.Match` glob syntax, one per line, matching the entire command string. `hostname` matches that exact command; `hostname*` also matches `hostname -f`. Supported wildcards include `*`, `?` and `[abc]`; `*` and `?` do not cross the server platform's path separator. These are not regular expressions: the `^` and `$` in `^hostname$` are literal characters and do not block `hostname`. Invalid globs, such as an unclosed `[`, are rejected on save.

Audit supports node/outcome filters and ID-based pagination. Records retain tool, node, authorization, binding and outcome metadata, never original commands, paths or free-form diagnostics. Running operations show current admitted work, its phase, nodes and start time. This is not another transfer state machine and does not list unstarted ready tasks. MCP task status and offline journal recovery remain authoritative for transfer results; `unknown` is never automatically converted to success.

Outcome filters match stored states exactly: operation completion/failure selects `executed`/`error`, while transfer completion/failure selects `completed`/`failed`. Each works with node filters and pagination. Downloads in `streamed` remain labeled as streamed, which does not confirm that the client saved the file; all states remain visible under all outcomes.

Changing audit filters immediately clears the previous rows and cursor. Pagination is disabled while the first or next page is loading; delayed responses for older filters cannot replace or append to the current results. If an inventory refresh finds that the selected node was deleted, the node filter resets to all nodes, clears the old rows and cursor, and retains the outcome filter. If the new first page fails, its filters remain selected and retry starts again from the newest matching records.

Targetless operations, such as inventory queries, return an empty node list and display “—”. Closing an editor while a save is pending does not guarantee cancellation of the submitted request. Its completion cannot close a later editor or erase newer policy/password page drafts. Edits made while a policy save is pending are also preserved, with their original revision precondition intact during background refreshes. Conflicts retain input and request a reload. An explicit refresh reloads the form, while preserving any input added after that refresh began.

A committed configuration that fails publication keeps affected new operations paused. The console shows pending activation; retry reloads and publishes the committed state without repeating the database write. Every configuration mutation displays any post-action audit warning from the server instead of an ordinary success message, making clear that the configuration was applied but its audit write failed. This does not roll back or automatically replay the change. A subsequent page-refresh failure requests another read without treating the completed write as a failed submission.

## HTTP API

Administrator routes are under `<web_base_path>/api/v1/` on the management port; table paths are relative to that directory. Writes require a same-origin JSON request and the session's `X-CSRF-Token`. Configuration operations additionally require `If-Match`, using the strong ETag returned by `GET /inventory`, for example `"7"`. Missing preconditions return 428 and stale revisions return 412. Credentials are write-only: reads omit passwords, private keys, passphrases, ciphertext and password hashes.

| Endpoint | Purpose |
| --- | --- |
| `GET /auth/session` | Authentication/setup state and current CSRF token |
| `POST /auth/setup`, `POST /auth/login` | `username` and `password`; setup also requires `token` |
| `POST /auth/logout`, `PUT /auth/password` | Logout or change password with `current` and `next` |
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

## Native and container deployment

Use the example [systemd configuration](../../examples/deployment/systemd.yaml), [service unit](../../examples/deployment/xops-mcp.service) and [Nginx configuration](../../examples/deployment/nginx.conf). Create a dedicated `xops-mcp` user, install the binary at `/usr/local/bin/xops-mcp`, and place its configuration and user-owned 0600 key files under `/etc/xops-mcp/`. systemd creates a private 0700 state directory at `/var/lib/xops-mcp`.

Replace example hostnames, certificate paths, `public_url` and `web_public_url`, install the unit, then run `systemctl daemon-reload` and `systemctl enable --now xops-mcp`. Keep both native listeners on loopback. Nginx forwards `/mcp` and `/v1/transfers/` to port 8080, and `/console/` on the management origin to port 8081, preserving Host and paths. Disable buffering for MCP streams. The management prefix must match `web_base_path`.

The [Dockerfile](../../Dockerfile) builds an embedded single-binary image running as UID/GID 65532. The [Compose example](../../examples/deployment/compose.yaml) uses a named data volume, read-only root filesystem and separate read-only key mount:

```sh
mkdir -m 700 -p examples/deployment/.secrets
bin/xops-mcp keygen --out examples/deployment/.secrets/master.key
bin/xops-mcp keygen --out examples/deployment/.secrets/mcp.token
bin/xops-mcp keygen --out examples/deployment/.secrets/admin.setup
sudo chown -R 65532:65532 examples/deployment/.secrets
docker compose -f examples/deployment/compose.yaml up -d --build
```

The dot-prefixed `.secrets` directory is skipped by Go's `./...` package traversal, so the checkout owner can run builds and tests directly. Compose requires the directory to exist and will not create an empty secrets directory automatically. Keep the secrets directory at mode 0700 and its files at 0600, owned by UID/GID 65532. [.dockerignore](../../.dockerignore) excludes the entire `examples/deployment` example directory so a non-root Docker user never traverses its private `.secrets` directory or sends its contents in the build context. Compose still reads its configuration from the host; configuration and key files are mounted read-only at runtime.

For an existing real `examples/deployment/secrets` directory, rename it to `.secrets`, preserving its files, ownership and permissions; do not regenerate the keys. If an existing container still refers to the old path, retain a `secrets -> .secrets` symlink. Go does not traverse that link and existing mounts remain usable. Updated Compose mounts `.secrets` directly; the old link can be removed after the container is recreated.

Compose publishes each port separately on host loopback; the example console URL is `http://127.0.0.1:8081/console/`.

Update the public origins in [container.yaml](../../examples/deployment/container.yaml) for reverse-proxy deployment. The volume root is owned by UID/GID 65532; the application creates its private 0700 `data/` subdirectory there. Existing data directories require the same ownership and private permissions. Keep the master key outside the data volume.

## Backup and restore

For PostgreSQL database backups and migration, see the [PostgreSQL guide](postgresql.md); copying the local directory cannot back up an external database. For SQLite, stop the single instance before copying the complete data directory: SQLite, any WAL/SHM files and the entire transfers journal. Protect configuration, master key and MCP token separately. A native deployment can use:

```sh
sudo systemctl stop xops-mcp
sudo tar --numeric-owner -czf /secure-backups/xops-data.tgz -C /var/lib/xops-mcp .
# Back up configuration, master key and MCP token from /etc/xops-mcp separately
sudo systemctl start xops-mcp
```

For containers, stop Compose before backing up the named volume and key mount. Restore the complete private directory with its original ownership and permissions, configure the same master key/token, run `migrate`, then start one instance. Credentials cannot be decrypted without the master key. Restart clears old administrator sessions but retains inventory, policy, password hashes, stable identities and unknown-result locks. Never run the original deployment and restored copy concurrently.

## Development verification

Go builds directly embed `web/assets`. Node.js and Playwright are needed only for browser acceptance:

```sh
npm --prefix web ci --ignore-scripts
cd web
npx playwright install chromium
npm run check
npm run test:browser
```

The test launches a temporary deployment and isolated SSH peer, exercising external console links, setup/login, direct and independently supplied key confirmation, CRUD, real connection tests, edit conflicts, XSS escaping, policy, audit, mobile layout and logout. Go integration tests also cover key enrollment and rotation for an unreachable address, real jump connections, mismatched-key rejection, and session/host/revision binding with single-use confirmation. Set `XOPS_TEST_CHROME=/path/to/chrome` to use an installed browser. Screenshots go to ignored `web/test-results/`; no personal credentials or deployed hosts are used.
