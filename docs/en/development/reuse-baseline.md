# Shared dependency baseline

[简体中文](../../development/reuse-baseline.md) · [Development guide](README.md)

## Current pin

| Item | Fixed value |
| --- | --- |
| Upstream module | `github.com/wentf9/xops-cli` |
| Version | `v0.13.1-0.20261005131907-09af1745dc9e` |
| Commit | [`09af1745dc9e65c06e381e20f5f1c892133ff0de`](https://github.com/wentf9/xops-cli/commit/09af1745dc9e65c06e381e20f5f1c892133ff0de) |
| Minimum Go | `1.26.0` |
| MCP SDK | `v1.8.0` |

The pin includes injectable client authentication, session and transfer isolation, bounded authentication admission, and the pinned host-key algorithm repair. It is published on `feat/mcp-client-auth`. Retain this branch until the commit is reachable from mainline or the consumer has switched to a verified, downloadable mainline pin. go.mod/go.sum are authoritative; do not commit local replacements or workspaces.

Production and tests import only upstream `core/*`. `internal/dependencycheck` checks Linux/Windows/macOS dependency graphs and explicitly compiles the product against the remote pin. Listing dependencies alone does not replace compilation.

## Independent consumer

```sh
GOWORK=off python3 scripts/check_core_consumer.py --version v0.13.1-0.20261005131907-09af1745dc9e
```

The script copies `internal/coreconsumer` into a temporary module, checks the fixed version without replacements and all three platform graphs, then runs build, race, lint and real local SSH/SFTP. `--upstream /path/to/xops-cli` is only a local preview, not evidence of a published version.

On 2026-10-05, a fresh module cache with `GOPROXY=direct` on Linux/amd64 downloaded this version and verified the exact remote commit above. Product build/lint and full SQLite/PostgreSQL race suites with `GOWORK=off`, independent consumer build/race/lint, all three platform dependency graphs, and HTTP/HTTPS browser flows passed. These results do not establish native Windows/macOS service acceptance or remote CI status.

## Product validation

- `internal/coreconsumer`: public runtime, authentication, tools, real SSH/SFTP, disablement and shutdown.
- `internal/storage/{sqlite,postgres}` and `internal/storage/contract`: independent migrations, transactions, historical materials, archives and ownership.
- `internal/server` and `internal/command`: persistent service, Web/MCP integration, commands, recovery and lifecycle.
- `internal/adminauth`, `internal/config` and `web/test`: JWT/JWE, key separation, optional TLS, HTTP encryption compatibility and browser races.

The current product implements SQLite, PostgreSQL, Web management and stateless administrator authentication, while remaining single-instance. See [builds and validation](build-testing.md) for product gates and the [roadmap](roadmap.md) for history. Report deployed-host checks, fixture protocols, dependency graphs and cross-builds separately.
