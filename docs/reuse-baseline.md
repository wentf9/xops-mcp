# Dependency baseline / 依赖基线

## Source

- Upstream module: `github.com/wentf9/xops-cli`.
- Source commit: [`73892b0791e3222bbd08abee1f21ed067b1c41c8`](https://github.com/wentf9/xops-cli/commit/73892b0791e3222bbd08abee1f21ed067b1c41c8).
- Exact version: `v0.13.1-0.20260930042335-73892b0791e3`.
- Previous release: `v0.13.0`; the pin also includes the subsequent sudo prompt/feedback fix.
- Go language minimum: `1.26.0`; MCP SDK: `v1.8.0`.
- Checks use the downloaded module, with no local replacement and `GOWORK=off`.

## Existing public boundaries

| Boundary | Source at the pinned commit |
| --- | --- |
| MCP Runtime construction, injection, Close | [server.go](https://github.com/wentf9/xops-cli/blob/73892b0791e3222bbd08abee1f21ed067b1c41c8/pkg/mcpserver/server.go) |
| HTTP handler and listener ownership | [http.go](https://github.com/wentf9/xops-cli/blob/73892b0791e3222bbd08abee1f21ed067b1c41c8/pkg/mcpserver/http.go) |
| SSH provider/resolver/recorder ports | [types_api.go](https://github.com/wentf9/xops-cli/blob/73892b0791e3222bbd08abee1f21ed067b1c41c8/pkg/ssh/types_api.go) |
| Broad configuration provider | [types.go](https://github.com/wentf9/xops-cli/blob/73892b0791e3222bbd08abee1f21ed067b1c41c8/pkg/config/types.go) |
| SSH/config error coupling | [errors.go](https://github.com/wentf9/xops-cli/blob/73892b0791e3222bbd08abee1f21ed067b1c41c8/pkg/ssh/errors.go) |

## Consumer checks

The checks in `internal/compat` are authored in this independent module and use exported upstream APIs.

| Check | Evidence |
| --- | --- |
| Shared HTTP runtime | Initialize using the MCP SDK, discover required HTTP tools, reject stdio-only tools, query a synthetic node |
| Authentication | An unauthenticated request receives HTTP 401 |
| Independent SSH provider | Consumer-owned provider errors propagate; a closed connector rejects new work without dialing |
| Dependency boundary | CLI/TUI packages are absent from the compiled consumer dependency graph |
| Version boundary | The upstream module has a concrete version and no replacement |
| Resource lifetime | Deferred resource cleanup and goleak verification; race check in CI |

## Local validation / 本地验证

Verified on 2026-09-30, Linux/amd64:

| Gate | Result |
| --- | --- |
| Go 1.27.1 build and all four consumer tests | Passed |
| Go 1.26.7 build and all four tests with `-race` | Passed |
| `go mod verify` | All downloaded modules verified |
| golangci-lint 2.14.0 | Configuration valid; zero issues |
| Markdown relative links | All targets exist |
| Workflow YAML and job structure | Valid; read-only repository permission and `GOWORK=off` |

These are local results. The repository's CI repeats module, build, race-test,
and lint checks on Linux; remote run results are reported by GitHub Actions.

## Limits / 验证范围

These checks do not connect to real SSH hosts or exercise SFTP file transfer. They do not establish Web, database, dynamic inventory, credential rotation, multi-instance, or native Windows/macOS support. The initial CI target is Linux.

上述检查验证外部模块的复用接缝，不代表产品功能已经实现。现阶段保留上游启动快照、本地传输 journal 和配置/凭据依赖；后续改造及验收以[架构决策](architecture.md)和[路线图](roadmap.md)为准。
