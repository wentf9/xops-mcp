# SQLite server deployment and inventory import

[简体中文](../server.md)

The server targets a single Linux instance and exposes Streamable HTTP MCP and file transfers. Build with Go 1.26+. Inventory can be managed live through the [Web console](web-console.md) or through offline commands. PostgreSQL is not available yet.

## Start the server

```sh
GOWORK=off go build -o bin/xops-mcp ./cmd/xops-mcp
mkdir -m 700 -p .local
cp examples/server.yaml .local/server.yaml
bin/xops-mcp keygen --out .local/master.key
bin/xops-mcp keygen --out .local/mcp.token
bin/xops-mcp keygen --out .local/admin.setup
bin/xops-mcp migrate --config .local/server.yaml
bin/xops-mcp serve --config .local/server.yaml
```

`keygen` creates a random 0600 file and never overwrites an existing file. The master key contains 64 hexadecimal characters. The MCP token is a separate credential. Relative paths resolve against the configuration file's directory. The data directory must be private (0700), and the master key must live outside it. Deployment files and database files must not be symlinks.

MCP defaults to `127.0.0.1:8080`; management separately defaults to `127.0.0.1:8081` (see [Web configuration](web-console.md)); the MCP endpoint is `http://127.0.0.1:8080/mcp`. Clients send `Authorization: Bearer <contents of mcp.token>`. Status and import reports do not print tokens, passwords or private keys. An empty database has no executable nodes.

LAN deployment requires an explicit `listen` and `public_url`, for example `0.0.0.0:8080` and `http://192.0.2.20:8080`. The public URL is the client-visible HTTP(S) origin without `/mcp`, path prefixes, credentials or query parameters. A reverse proxy must preserve `/mcp` and `/v1/transfers/`, forward an allowed Host, and provide HTTPS for external access; the process itself serves HTTP. Configure additional `allowed_hosts` and `allowed_origins` explicitly when required.

`tool_timeout` defaults to 5 minutes and `shutdown_timeout` to 45 seconds. File transfers retain independent shared-core limits: 10 GiB per file, 4 concurrent tasks, 2 tasks per target, a 5-minute start window, a 2-hour total deadline and a 30-second commit deadline. SIGINT/SIGTERM stops new requests, settles runtime/journal work, then closes the database.

Both MCP and management listeners set a default 60-second response write deadline before routing and security checks, including unmatched routes, rejected requests and shutdown responses. Admitted management requests use a 20-second write deadline; MCP and file-transfer streams renew their deadlines using the shared core's idle limit. Clients that stop reading responses cannot retain a blocked write indefinitely.

## Persistence and migrations

`data_dir/xops.db` stores the deployment ID, revision, hosts, identities, nodes, tags, policy, encrypted credentials, historical versions, connection sources and audit events. `data_dir/transfers/` owns the separate transfer journal. SQLite enables foreign keys, WAL, full synchronization and bounded busy waits. A deployment lock excludes additional instances and offline commands while the server is running.

`migrate` initializes an empty database or upgrades supported schemas transactionally. `serve` also applies supported migrations. Schema v3 migrates single-jump references into the ordered `node_jumps` relation; v4 adds administrator/session tables; v5 assigns independent tag primary keys and migrates node relations while preserving names and unused tags. All preserve deployment ID, node IDs and revision. Future schemas are rejected. `status`, import previews and `recover` never create or upgrade a database; run `migrate` first.

```sh
bin/xops-mcp status --config .local/server.yaml
```

Audit records retain operation ID, tool, nodes, binding digest, risk, decision and outcome. Original commands, paths and free-form error/details fields are omitted because client input may contain secrets. Credentials use versioned AES-256-GCM authenticated encryption bound to deployment, credential ID, kind and material version. Private keys are decrypted and parsed in memory, never staged in temporary files. Historical ciphertext and sources remain available for previously admitted operations. Automatic history cleanup and master-key rotation are not implemented.

## Preview and apply inventory

Stop the server before importing:

```sh
bin/xops-mcp import --config .local/server.yaml --file examples/inventory.yaml --dry-run
bin/xops-mcp import --config .local/server.yaml --file examples/inventory.yaml --apply --expected-revision 0
```

Preview is the default. Reports include `expected_revision`, mappings from original node names/aliases and tag names to stable IDs (`node_ids`, `tag_ids`), credential change names, removed nodes and conflicts. Applying requires the reviewed revision; stale revisions fail. New IDs are identical between preview and apply for the same deployment/revision. Existing nodes retain their IDs; reimporting a deleted name creates a new ID.

Imports merge by name unless `--replace` replaces hosts, identities and nodes, permanently tombstoning missing node IDs. Credentials and independent tags always merge by name, and node deletion does not erase historical ciphertext. Input node names are the import matching key; renaming one creates another node.

`examples/inventory.yaml` is an executable disabled-node example. Supply an independently verified `host_key`, a server credential and `disabled: false` before enabling a real target. Unknown host keys are never automatically accepted. Nodes missing a credential or host key are imported disabled with a report warning.

When a peer offers multiple host keys, negotiation selects the pinned key's algorithm and still verifies the exact public key. Another key of the same type is not accepted. RSA pins use `rsa-sha2-512` / `rsa-sha2-256` signatures.

Resource names, tags and node aliases in both server-format and xops-cli imports follow the [Web console character rules](web-console.md#manage-nodes): Unicode letters, decimal digits, `_`, `-` and combining marks after letters, within 1–256 UTF-8 bytes. Whitespace and other punctuation/symbols cause import rejection; older invalid values are neither exempted nor rewritten.

Server inventory uses `version: 1`:

| Field | Contents |
| --- | --- |
| `tags` | Optional independent tag-name list, including tags with no nodes |
| `hosts.<name>` | `address`, `port` (22 if omitted), `host_key` (one SSH public key) |
| `identities.<name>` | `user`, `credential` (credential name) |
| `nodes.<name>` | `host`, `identity`, `aliases`, `tags`, `proxy_jump` (node name), `disabled`, `sudo_mode`, `privilege_credential` |
| `credentials.<name>` | `kind: password` and `password`, or `kind: key` and `private_key`, optional `passphrase` |
| `policy` | Shared guardrail fields; `no_elicit_fallback` defaults to `deny` |

Node `tags` remain a list of names in import files. Import creates missing tag records and resolves names to stable IDs, retaining IDs for existing names. `--replace` preserves independent tags; removing the final node association does not delete a tag.

Privilege mode is explicit: `none`, `root`, `sudo`, `sudoer` or `su`. `su` requires a separate privilege password. `sudo` can use the identity's login password if no privilege credential is configured. `proxy_jump` accepts a node name or an ordered comma-separated chain such as `jump1,jump2`. A single jump follows that node's own upstream route; an explicit chain connects directly to its first hop and proceeds in order without changing shared nodes' standalone routes. At most 32 hops including the target are allowed; cycles/missing references are rejected. Host certificates and automatically discovered SSH agents are outside the current credential adapter.

`policy.nodes` accepts node names, aliases, stable IDs or globs and expands them to stable IDs during this import. Unmatched or conflicting patterns fail. Resubmit relevant pattern rules when adding nodes. The CLI `audit_log` path is not used.

Credential material requires explicit `--include-secrets`. Secret input files must have mode 0600; `--file -` reads stdin instead. Inventory is limited to 4 MiB. Keep secrets out of command-line arguments and version control.

Stdin imports observe the offline command's 30-second deadline and SIGINT/SIGTERM cancellation. Cancellation ends reading and releases the deployment lock without waiting for EOF. Linux polls descriptor readiness and preserves borrowed stdin; platforms without cancellable stream input require a regular input file instead.

```yaml
version: 1
credentials:
  operator-password:
    kind: password
    password: REPLACE_WITH_DEPLOYMENT_PASSWORD
identities:
  operator:
    user: operator
    credential: operator-password
```

This fragment updates only the credential and identity. Import new material under the same credential name to rotate it. All dependent identities/jump plans move to new connection generations. Ordinary endpoint edits preserve the target of admitted work. Credential/trust revocation or disablement cancels affected cancellable work; already-admitted file commits retain the shared core's commit protection.

## One-way migration from xops-cli

```sh
bin/xops-mcp import --config .local/server.yaml --format xops-cli --file exported-config.yaml --dry-run
```

The importer supports CLI schema v1/v2 hosts, identities, nodes, node aliases, tags, jumps and guardrails. ProxyJump node aliases are resolved to canonical names, and comma-separated chains retain their order; missing or ambiguous selectors are rejected. CLI host aliases are not imported as server node selectors. Selector mappings include node names and node aliases. Source YAML is never modified.

CLI credential stores, password/passphrase/privilege references, private-key paths, SSH agents and known_hosts are never automatically read or copied. All CLI-imported nodes are disabled. Use server-format imports to supply verified host keys and credentials before enabling them. V1 inline passwords can be re-encrypted with explicit `--include-secrets`; plaintext in schema v2 is rejected. Automatic privilege mode becomes `none` with a warning. Custom password-prompt patterns are unsupported and explicitly rejected. The products never share concurrent writes to the same inventory.

V1 inline `password` and `su_pwd` values generate credential names `login-<SHA256 of name>` and `privilege-<SHA256 of name>`, respectively, with matching identity/node references. The digest uses only the source resource name, never password material. Generated names meet the character and length limits, remain stable across preview/apply/reimport, and keep login and privilege credentials separate.

## Transfers and recovery

Clients call `xops_prepare_upload` / `xops_prepare_download`, then use the returned URL and short-lived credential to transfer binary content. MCP tokens and transfer credentials are separate. Local paths are interpreted only by clients; clients archive directories first. `streamed` does not prove local saving: verify size and checksum before promoting the downloaded file.

With the service stopped:

```sh
bin/xops-mcp recover --config .local/server.yaml
bin/xops-mcp recover --config .local/server.yaml --id TRANSFER_ID --verify
bin/xops-mcp recover --config .local/server.yaml --id TRANSFER_ID --cleanup
bin/xops-mcp recover --config .local/server.yaml --id TRANSFER_ID --resolve-unknown --reason 'Original target checked manually'
```

Listing does not contact SSH or modify the journal. `--verify` inspects the original destination; `--cleanup` removes only task-owned temporary files. `--resolve-unknown` records operator acknowledgement and releases destination protection, without retransmitting or changing `unknown` into success. Changed node/credential/trust bindings prevent remote verification and cleanup; inspect the original target manually.

Restarts preserve deployment identity, relevant versions and unknown-result locks. Stop the service before backing up the complete data directory (database and journal), and protect the master key, MCP token and configuration separately. Restore with the same master key; losing it makes credentials unrecoverable. The current server is single-instance and has no online-backup, automatic master-key rotation or PostgreSQL migration command.
