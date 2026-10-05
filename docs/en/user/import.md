# Import hosts and credentials

[简体中文](../../user/import.md) · [User guide](README.md)

Stop the service before importing. The direct commands below run from the `xops-instance` directory described in [running directly](install.md), with a prepared `inventory.yaml`.

## Preview, then apply

```sh
xops-mcp import --config server.yaml --file inventory.yaml --dry-run
```

Preview does not change data. Review conflicts, warnings and `expected_revision`. The `0` below is valid only when that value appears in the preview; replace it with your current preview's version:

```sh
xops-mcp import --config server.yaml --file inventory.yaml --apply --expected-revision 0
```

By default, records merge by name and omitted records remain. `--replace` removes absent hosts, identities and nodes; credentials and tags still merge by name. A changed name is treated as another record.

## File format

This disabled-node example can be previewed and imported directly:

```yaml
version: 1
tags: [office]
hosts:
  example:
    address: 192.0.2.10
    port: 22
    host_key: ""
identities:
  operator:
    user: operator
    credential: ""
nodes:
  example:
    host: example
    identity: operator
    aliases: [demo]
    tags: [office]
    disabled: true
    sudo_mode: none
```

Add credentials and verify the host key in the console before enabling the node. Nodes missing either remain disabled, with a warning in the import report.

| Item | Fields |
| --- | --- |
| `hosts.<name>` | `address`, `port` (default 22), `host_key` |
| `identities.<name>` | `user`, `credential` (credential name) |
| `nodes.<name>` | `host`, `identity`, `aliases`, `tags`, `proxy_jump`, `disabled`, `sudo_mode`, `privilege_credential` |
| `tags` | Tag names, including unassigned tags |
| `credentials.<name>` | `kind: password` with `password`; or `kind: key`, `private_key` and optional `passphrase` |
| `policy` | Operation policy settings |

See the [console guide](console.md) for name rules. `proxy_jump` is a node name or an ordered comma-separated list such as `jump1,jump2`. Elevation modes are `none`, `root`, `sudo`, `sudoer` and `su`; `su` requires an elevation password.

Files containing passwords/private keys require mode `0600`, and both preview and apply require `--include-secrets`. Supply private-key contents, not a file path. Input is limited to 4 MiB. Keep real credentials out of public directories.

## Container imports

Containers cannot automatically read arbitrary host paths. With `inventory.yaml` in the current directory, stop the service and pass it through standard input:

```sh
docker compose -f examples/deployment/compose.yaml stop
docker compose -f examples/deployment/compose.yaml run --rm --no-deps -T xops-mcp import --config /etc/xops-mcp/server.yaml --file - --dry-run < inventory.yaml
```

After reviewing the preview, use its version:

```sh
docker compose -f examples/deployment/compose.yaml run --rm --no-deps -T xops-mcp import --config /etc/xops-mcp/server.yaml --file - --apply --expected-revision 0 < inventory.yaml
docker compose -f examples/deployment/compose.yaml up -d --no-build
```

Add `--include-secrets` to both import commands when the file includes credentials.

## Import from xops-cli

```sh
xops-mcp import --config server.yaml --format xops-cli --file exported-config.yaml --dry-run
```

Hosts, identities, nodes, aliases, tags, jumps and policies are supported. The source file is unchanged, and imported nodes remain disabled. Password stores, private-key paths and SSH agents are not automatically copied. Add credentials and verify host keys before enabling nodes.

Passwords written directly in older configuration formats require `--include-secrets`. Automatic elevation becomes no elevation with a warning. After reviewing the preview, add `--apply --expected-revision` to apply it. A successful preview alone does not make nodes ready for remote work.
