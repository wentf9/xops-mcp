# Shared dependency baseline

[简体中文](../../development/reuse-baseline.md) · [Development guide](README.md)

## Current pin

| Item | Fixed value |
| --- | --- |
| Upstream module | `github.com/wentf9/xops-cli` |
| Version | `v0.15.1-0.20261029074656-7a52a01fbf14` |
| Commit | [`7a52a01fbf1462ed4bb2ac2992b7e286b4887246`](https://github.com/wentf9/xops-cli/commit/7a52a01fbf1462ed4bb2ac2992b7e286b4887246) |
| Minimum Go | `1.26.0` |
| MCP SDK | `v1.8.0` |

The consumer pins upstream `v0.15.1-0.20261029074656-7a52a01fbf14`. It incorporates `ConnectPlan` diagnostic observer forwarding, native cross-platform cancelable output pipelines (`BindOutput`), unrestricted interpreter combinations with dynamic script overrides, OpenSSH-style leveled diagnostics, SFTP lexical tokenization and quote escaping, storage space pre-checks and large-file progress tracking, and refined SCP overwrite semantics, alongside bounded command execution with structured outcomes, per-call execution overrides, lazy shell probing, client authentication, and transfer isolation. go.mod/go.sum are authoritative; do not commit local replacements or workspaces.

Nodes persist optional execution settings in SQLite v9 / PostgreSQL v6 and expose them through the management API and console. The adapter clones settings into each target and supplies `ports.ExecutionVersion` so a setting change invalidates old admissions. CLI import resolves global/node execution inheritance into each imported node. Runtime probes remain transient; they do not write inventory.

Production and tests import only upstream `core/*`. `internal/dependencycheck` checks Linux/Windows/macOS dependency graphs and explicitly compiles the product against the remote pin. Listing dependencies alone does not replace compilation.

## Independent consumer

```sh
GOWORK=off python3 scripts/check_core_consumer.py --version v0.15.1-0.20261029074656-7a52a01fbf14
```

The script copies `internal/coreconsumer` into a temporary module, checks the fixed version without replacements and all three platform graphs, then runs build, race, lint and real local SSH/SFTP. `--upstream /path/to/xops-cli` is only a local preview, not evidence of a published version.

On 2026-10-29, Linux/amd64 acceptance passed: fresh-cache `GOPROXY=direct` download with tag/commit and checksums verified; product build and lint; full SQLite/PostgreSQL race suites; independent consumer build/race/lint and three-platform dependency graphs; Web resource checks, browser regressions, and HTTP/HTTPS browser flows. Protocol fixtures do not establish native shell execution on deployed hosts or native Windows/macOS server acceptance.

## Product validation

- `internal/coreconsumer`: public runtime, authentication, tools, real SSH/SFTP, disablement and shutdown.
- `internal/storage/{sqlite,postgres}` and `internal/storage/contract`: independent migrations, transactions, historical materials, archives and ownership.
- `internal/server` and `internal/command`: persistent service, Web/MCP integration, commands, recovery and lifecycle.
- `internal/adminauth`, `internal/config` and `web/test`: JWT/JWE, key separation, optional TLS, HTTP encryption compatibility and browser races.

The current product implements SQLite, PostgreSQL, Web management and stateless administrator authentication, while remaining single-instance. See [builds and validation](build-testing.md) for product gates and the [roadmap](roadmap.md) for history. Report deployed-host checks, fixture protocols, dependency graphs and cross-builds separately.
