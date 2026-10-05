# Shared dependency baseline

[简体中文](../../development/reuse-baseline.md) · [Development guide](README.md)

## Current pin

| Item | Fixed value |
| --- | --- |
| Upstream module | `github.com/wentf9/xops-cli` |
| Version | `v0.13.1-0.20261003125647-0d4bd2fb866c` |
| Commit | [`0d4bd2fb866ce4d3cd4a79f90a2df63ff7560b08`](https://github.com/wentf9/xops-cli/commit/0d4bd2fb866ce4d3cd4a79f90a2df63ff7560b08) |
| Minimum Go | `1.26.0` |
| MCP SDK | `v1.8.0` |

The pin includes pinned host-key algorithm negotiation and multi-key fixtures. It is reachable from upstream master and does not depend on a deleted feature branch. go.mod/go.sum are authoritative; do not commit local replacements or workspaces.

Production and tests import only upstream `core/*`. `internal/dependencycheck` checks Linux/Windows/macOS dependency graphs and explicitly compiles the product against the remote pin. Listing dependencies alone does not replace compilation.

## Independent consumer

```sh
GOWORK=off python3 scripts/check_core_consumer.py --version v0.13.1-0.20261003125647-0d4bd2fb866c
```

The script copies `internal/coreconsumer` into a temporary module, checks the fixed version without replacements and all three platform graphs, then runs build, race, lint and real local SSH/SFTP. `--upstream /path/to/xops-cli` is only a local preview, not evidence of a published version.

The 2026-10-03 record covers independent Linux/amd64 consumption, a fresh-cache `GOPROXY=direct` download and Windows/macOS product cross-builds. This historical evidence does not establish current remote CI or native Windows/macOS service acceptance.

## Product validation

- `internal/coreconsumer`: public runtime, authentication, tools, real SSH/SFTP, disablement and shutdown.
- `internal/storage/{sqlite,postgres}` and `internal/storage/contract`: independent migrations, transactions, historical materials, archives and ownership.
- `internal/server` and `internal/command`: persistent service, Web/MCP integration, commands, recovery and lifecycle.
- `internal/adminauth`, `internal/config` and `web/test`: JWT/JWE, key separation, optional TLS, HTTP encryption compatibility and browser races.

The current product implements SQLite, PostgreSQL, Web management and stateless administrator authentication, while remaining single-instance. See [builds and validation](build-testing.md) for product gates and the [roadmap](roadmap.md) for history. Report deployed-host checks, fixture protocols, dependency graphs and cross-builds separately.
