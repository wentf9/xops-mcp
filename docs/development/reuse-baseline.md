# 共享依赖基线

[English](../en/development/reuse-baseline.md) · [开发指南](README.md)

## 当前依赖

| 项目 | 固定值 |
| --- | --- |
| 上游 module | `github.com/wentf9/xops-cli` |
| 版本 | `v0.13.1-0.20261006023507-477fa537d454` |
| 提交 | [`477fa537d454163448ffde6228305c15981dfd0b`](https://github.com/wentf9/xops-cli/commit/477fa537d454163448ffde6228305c15981dfd0b) |
| Go 最低版本 | `1.26.0` |
| MCP SDK | `v1.8.0` |

该 pin 包含可注入的客户端认证、会话与文件任务隔离、独立认证并发额度，以及固定主机公钥算法协商修复。提交已通过 [PR #79](https://github.com/wentf9/xops-cli/pull/79) 合入上游 `master`，消费端固定到该主线提交。实际依据是当前 go.mod/go.sum；不得提交本地 replace 或 go.work。

生产与测试都只导入上游 `core/*`。`internal/dependencycheck` 验证 Linux/Windows/macOS 依赖图，并显式用远端 pin 编译产品；依赖枚举不能替代编译。

## 独立消费者

```sh
GOWORK=off python3 scripts/check_core_consumer.py --version v0.13.1-0.20261006023507-477fa537d454
```

脚本复制 `internal/coreconsumer` 到临时 module，校验固定版本、无替换、三平台依赖图，并执行 build、race、lint 和真实本地 SSH/SFTP。`--upstream /path/to/xops-cli` 仅用于临时联调，不能作为远端版本已发布的证据。

2026-10-06 在 Linux/amd64 上以空 module 缓存和 `GOPROXY=direct` 下载该版本，确认来源为上述远端提交。`GOWORK=off` 产品构建与 lint、SQLite/PostgreSQL 全量 race、独立消费者 build/race/lint、三平台依赖图、HTTP/HTTPS 浏览器流程均通过；这些结果不代表原生 Windows/macOS 服务验收或远端 CI 结果。

## 产品验证

- `internal/coreconsumer`：公共 runtime、鉴权、工具、真实 SSH/SFTP、动态禁用和关闭。
- `internal/storage/{sqlite,postgres}`、`internal/storage/contract`：独立迁移、事务、历史材料、归档和所有权。
- `internal/server`、`internal/command`：持久服务、Web/MCP 联动、命令、传输恢复和生命周期。
- `internal/adminauth`、`internal/config`、`web/test`：JWT/JWE、密钥隔离、可选 TLS、HTTP 加密兼容和浏览器竞态。

当前实现包括 SQLite、PostgreSQL、Web 管理及无状态管理员认证，仍限定单实例。完整产品 gates 见[构建与验证](build-testing.md)，历史阶段见[路线图](roadmap.md)。真实部署验证、fixture 协议验证、依赖图与交叉编译应分别报告。
