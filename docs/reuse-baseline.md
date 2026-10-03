# Dependency baseline / 依赖基线

## Current source / 当前固定版本

- Upstream module: `github.com/wentf9/xops-cli`.
- Source commit: [`91671a7cf029893039a1a0036082576fa2c44273`](https://github.com/wentf9/xops-cli/commit/91671a7cf029893039a1a0036082576fa2c44273).
- Exact version: `v0.13.1-0.20261003013451-91671a7cf029`.
- Upstream change: [xops-cli PR #75](https://github.com/wentf9/xops-cli/pull/75). The pin identifies a remotely downloadable commit, not a release tag or a claim that the PR has merged.
- Go minimum: `1.26.0`; MCP SDK: `v1.8.0`.
- Checks use `GOWORK=off` and the downloaded module without local replacement.
- Bootstrap baseline: `v0.13.1-0.20260930042335-73892b0791e3`. The legacy tests now validate compatibility at the current core pin.

当前版本通过远端下载接入公共 core；两个仓库仍为单向依赖，没有新增共享仓库、嵌套 module 或已提交的 replace/workspace。

## Public boundaries / 公共边界

| Boundary | Source at the pinned commit |
| --- | --- |
| Runtime composition and ownership | [core/mcp/runtime/server.go](https://github.com/wentf9/xops-cli/blob/91671a7cf029893039a1a0036082576fa2c44273/core/mcp/runtime/server.go) |
| State, admission, execution and audit ports | [core/mcp/ports/types.go](https://github.com/wentf9/xops-cli/blob/91671a7cf029893039a1a0036082576fa2c44273/core/mcp/ports/types.go) |
| Publication coordinator | [core/mcp/state](https://github.com/wentf9/xops-cli/tree/91671a7cf029893039a1a0036082576fa2c44273/core/mcp/state) |
| Captured SSH plans and leases | [core/ssh/plan.go](https://github.com/wentf9/xops-cli/blob/91671a7cf029893039a1a0036082576fa2c44273/core/ssh/plan.go) |
| Legacy CLI facade | [pkg/mcpserver/compat.go](https://github.com/wentf9/xops-cli/blob/91671a7cf029893039a1a0036082576fa2c44273/pkg/mcpserver/compat.go) |

## Consumer checks / 消费者验证

All contracts below run through normal `go test ./...` and CI. Tests use exported upstream APIs and deployment-independent synthetic fixtures.

| Package | Evidence |
| --- | --- |
| `internal/coreconsumer` | Shared runtime HTTP authentication, MCP initialization and tool sets, inventory, real SSH command execution, binary SFTP upload/download, dynamic node disablement and cleanup |
| `internal/legacycompat` | Existing facade construction and HTTP discovery/inventory contracts; independent SSH provider errors and connector shutdown |
| `internal/dependencycheck` | Exact version with no replacement; no CLI/TUI presentation packages in the aggregate graph; core-only upstream imports in Linux/Windows/macOS consumer graphs |

`coreconsumer` and `legacycompat` both use goleak. CI runs all tests with `-race`. Boundary checks inspect only the core package when excluding legacy configuration/credential adapters; compatibility fixtures do not weaken the production/core requirement.

## Remote-version acceptance / 远端版本验收

Validated on 2026-10-03, Linux/amd64, with Go 1.26.7 and golangci-lint 2.14.0:

```sh
GOWORK=off python3 scripts/check_core_consumer.py --version v0.13.1-0.20261003013451-91671a7cf029
```

The checker copies only `internal/coreconsumer` into a temporary module, requires the exact remote version, rejects replacements/version drift, checks three platform graphs and runs build, race tests and lint. This passed against the downloaded commit. Local previews remain available through `--upstream /path/to/xops-cli`, with replacement confined to a temporary module.

根模块还须通过 build、全量测试、race、lint、`go mod tidy -diff` 和 `go mod verify`。远端 CI 状态由对应 PR 的检查记录报告，不能由本地通过推断。

## Limits / 验证范围

The tests exercise real protocols against temporary local SSH/SFTP peers, not deployed hosts. Three-platform import graphs are not native Windows/macOS execution. Database transactions, persistent server adapters, credential rotation against a real store, the Web console, a standalone product binary and multi-instance deployment remain unimplemented here. Broader shared-core regression and native-platform coverage belongs to upstream CI.

上述验收完成公共 core 的固定版本消费，不代表数据库/Web 产品验收完成。下一阶段为[路线图](roadmap.md)中的 M2 SQLite 与可独立启动服务；跨仓库职责见[架构](architecture.md)。
