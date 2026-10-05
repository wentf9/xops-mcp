# Dependency baseline / 依赖基线

## Current source / 当前固定版本

- Upstream module: `github.com/wentf9/xops-cli`.
- Source commit: [`0d4bd2fb866ce4d3cd4a79f90a2df63ff7560b08`](https://github.com/wentf9/xops-cli/commit/0d4bd2fb866ce4d3cd4a79f90a2df63ff7560b08).
- Exact version: `v0.13.1-0.20261003125647-0d4bd2fb866c`.
- Upstream change: pinned SSH host-key algorithm negotiation and multi-key consumer fixtures.
- Published ref: [`master`](https://github.com/wentf9/xops-cli/tree/master). The pin identifies the merged mainline commit, not a release tag. Its source tree matches the prior fix-branch commit.
- Previous fix-branch pin: `v0.13.1-0.20261003115559-57a6ad0a9246`.
- Shared-core baseline: `v0.13.1-0.20261003075415-7cb4e30bd97b`, introduced through [xops-cli PR #77](https://github.com/wentf9/xops-cli/pull/77).
- Go minimum: `1.26.0`; MCP SDK: `v1.8.0`.
- Checks use `GOWORK=off` and the downloaded module without local replacement.
- Bootstrap baseline: `v0.13.1-0.20260930042335-73892b0791e3`. Retained as historical source information; current consumer tests use only core.

当前版本通过远端下载接入公共 core；两个仓库仍为单向依赖，没有新增共享仓库、嵌套 module 或已提交的 replace/workspace。固定版本来自已合入 master 的主线提交，源码可达性不再依赖修复分支。相邻 checkout 的本地验证不能代替远端版本验收。

## Public boundaries / 公共边界

固定版本已包含 `core/ssh.HostKeyAlgorithmSource` 以及支持多个主机密钥的 SSH 测试夹具。消费者从真实下载的 module 使用这些入口，不依赖本地 workspace 或 replace。`internal/dependencycheck` 即使在 workspace 中运行，也强制通过 `GOWORK=off go build` 编译服务端产品，防止仅列出依赖包而漏掉未发布 API。

The pin includes `core/ssh.HostKeyAlgorithmSource` and the multi-host-key SSH fixture. Consumers use these APIs from the downloaded module without local replacements or workspace dependencies. Even when run inside a workspace, `internal/dependencycheck` compiles the product with `GOWORK=off`; dependency listing alone cannot detect missing exported APIs.

未发布的联合开发可在两个仓库的父目录创建 workspace，然后不设置 `GOWORK=off` 运行 build/test/lint。这个结果仅代表本地源码联调。

For unpublished joint development, create a workspace in the parent directory and run build/test/lint without `GOWORK=off`. This validates local sources only.

```sh
go work init ./xops-cli ./xops-mcp
cd xops-mcp
go build ./...
go test -race ./...
golangci-lint run ./...
```

| Boundary | Source at the pinned commit |
| --- | --- |
| Runtime composition and ownership | [core/mcp/runtime/server.go](https://github.com/wentf9/xops-cli/blob/0d4bd2fb866ce4d3cd4a79f90a2df63ff7560b08/core/mcp/runtime/server.go) |
| State, admission, execution and audit ports | [core/mcp/ports/types.go](https://github.com/wentf9/xops-cli/blob/0d4bd2fb866ce4d3cd4a79f90a2df63ff7560b08/core/mcp/ports/types.go) |
| Publication coordinator | [core/mcp/state](https://github.com/wentf9/xops-cli/tree/0d4bd2fb866ce4d3cd4a79f90a2df63ff7560b08/core/mcp/state) |
| Captured SSH plans and leases | [core/ssh/plan.go](https://github.com/wentf9/xops-cli/blob/0d4bd2fb866ce4d3cd4a79f90a2df63ff7560b08/core/ssh/plan.go) |

## Consumer checks / 消费者验证

All contracts below run through normal `go test ./...` and CI. Tests use exported upstream APIs and deployment-independent synthetic fixtures.

| Package | Evidence |
| --- | --- |
| `internal/coreconsumer` | Shared runtime HTTP authentication, MCP initialization and tool sets, inventory, real SSH command execution, binary SFTP upload/download, dynamic node disablement and cleanup |
| `internal/dependencycheck` | Exact version with no replacement; standalone product compilation with GOWORK=off even inside a workspace; core-only upstream imports in every production/test graph on Linux/Windows/macOS |
| `internal/storage/sqlite`, `internal/secure`, `internal/importer`, `internal/adapters/xops`, `internal/service` | M2 schema upgrades, optimistic transactions, encryption, bound material sources, publication barriers, uncertain commits and tombstones |
| `internal/server`, `internal/command` | M2 persistent HTTP/SSH/SFTP, encrypted keys and jumps, restart/unknown recovery, imports, exclusive ownership, startup/shutdown and leak checks |

`coreconsumer`, `server` and `command` use goleak. CI runs all tests with `-race`. Boundary checks cover the entire module, including tests; there is no exception for CLI configuration or credential adapters. The old `internal/legacycompat` probe has been removed.

## Remote-version acceptance / 远端版本验收

Validated on 2026-10-03, Linux/amd64, with Go 1.27.1 and golangci-lint 2.14.0:

```sh
GOWORK=off python3 scripts/check_core_consumer.py --version v0.13.1-0.20261003125647-0d4bd2fb866c
```

The fixed module was also downloaded with an empty module cache and `GOPROXY=direct`, independently of existing proxy/module caches. The pinned commit is reachable from upstream master and does not depend on retaining the fix branch.

The checker copies only `internal/coreconsumer` into a temporary module, requires the exact remote version, rejects replacements/version drift, checks three platform graphs and runs build, race tests and lint. This passed against the downloaded commit. Local previews remain available through `--upstream /path/to/xops-cli`, with replacement confined to a temporary module.

根模块还须通过 build、全量测试、race、lint、`go mod tidy -diff` 和 `go mod verify`。远端 CI 状态由对应 PR 的检查记录报告，不能由本地通过推断。

当前 pin 的根模块验收已通过以下检查，全部禁用 workspace：

```sh
GOWORK=off go build ./...
GOWORK=off go test -race -count=1 -timeout=120s ./...
GOWORK=off go vet ./...
GOWORK=off golangci-lint run ./...
GOWORK=off go mod tidy -diff
GOWORK=off go mod verify
```

The root-module checks above passed against this pin with workspaces disabled, including the standalone-build regression and real local SSH/SFTP multi-key tests. Windows/amd64 and macOS/amd64 product cross-builds also passed with `GOWORK=off`; these are not native platform executions.

## Limits / 验证范围

The tests exercise real protocols against temporary local SSH/SFTP peers, not deployed hosts. Three-platform import graphs and cross-builds are not native Windows/macOS execution. M2 adds SQLite transactions, persistent adapters, encrypted-credential rotation and a standalone product binary. M3 adds administrator sessions, the embedded Web console, live management APIs and deployment examples. PostgreSQL and multi-instance deployment remain unimplemented. Broader shared-core regression and native-platform coverage belongs to upstream CI.

独立消费者脚本仅验收公共 core 的固定版本消费；M2 数据库和服务能力另由根 module 的产品测试覆盖。当前运行说明见[服务指南](server.md)，M3 说明见 [Web 控制台指南](web-console.md)，M4 PostgreSQL 见[路线图](roadmap.md)；跨仓库职责见[架构](architecture.md)。
