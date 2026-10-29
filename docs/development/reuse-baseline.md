# 共享依赖基线

[English](../en/development/reuse-baseline.md) · [开发指南](README.md)

## 当前依赖

| 项目 | 固定值 |
| --- | --- |
| 上游 module | `github.com/wentf9/xops-cli` |
| 版本 | `v0.15.1-0.20261029074656-7a52a01fbf14` |
| 提交 | [`7a52a01fbf1462ed4bb2ac2992b7e286b4887246`](https://github.com/wentf9/xops-cli/commit/7a52a01fbf1462ed4bb2ac2992b7e286b4887246) |
| Go 最低版本 | `1.26.0` |
| MCP SDK | `v1.8.0` |

消费端固定到上游 `v0.15.1-0.20261029074656-7a52a01fbf14`，包含 `ConnectPlan` 连接计划协议诊断观察者透传修复、原生全平台可取消输出管线（`BindOutput`）、命令解释器组合限制解禁与动态脚本覆盖、OpenSSH 风格全链路多级诊断体系、SFTP 词法分词与引号转义、存储空间预检与大文件传输进度跟踪、SCP 覆盖语义精确化，以及此前的有界命令执行与结构化结果、单次调用执行配置覆盖、自动 Shell 探测、客户端认证与传输隔离机制。实际依据是 go.mod/go.sum；不得提交本地 replace 或 go.work。

节点可选执行配置持久化于 SQLite v9 / PostgreSQL v6，并通过管理 API 和控制台编辑。适配器将配置副本放入 target，并提供 `ports.ExecutionVersion`，确保修改后旧准入绑定失效。CLI 导入将全局与节点配置继承结果展开到各节点。运行时自动探测结果仅用于操作，不写入库存。

生产与测试都只导入上游 `core/*`。`internal/dependencycheck` 验证 Linux/Windows/macOS 依赖图，并显式用远端 pin 编译产品；依赖枚举不能替代编译。

## 独立消费者

```sh
GOWORK=off python3 scripts/check_core_consumer.py --version v0.15.1-0.20261029074656-7a52a01fbf14
```

脚本复制 `internal/coreconsumer` 到临时 module，校验固定版本、无替换、三平台依赖图，并执行 build、race、lint 和真实本地 SSH/SFTP。`--upstream /path/to/xops-cli` 仅用于临时联调，不能作为远端版本已发布的证据。

2026-10-29 在 Linux/amd64 上完成验收：空缓存 `GOPROXY=direct` 下载并核对 tag、提交和校验和；产品 build/lint、SQLite/PostgreSQL 全量 race、独立消费者 build/race/lint 与三平台依赖图、Web 资源检查、浏览器回归及 HTTP/HTTPS 流程均通过。协议夹具不代表部署主机上的原生 Shell 执行验收，也不代表原生 Windows/macOS 服务验收。

## 产品验证

- `internal/coreconsumer`：公共 runtime、鉴权、工具、真实 SSH/SFTP、动态禁用和关闭。
- `internal/storage/{sqlite,postgres}`、`internal/storage/contract`：独立迁移、事务、历史材料、归档和所有权。
- `internal/server`、`internal/command`：持久服务、Web/MCP 联动、命令、传输恢复和生命周期。
- `internal/adminauth`、`internal/config`、`web/test`：JWT/JWE、密钥隔离、可选 TLS、HTTP 加密兼容和浏览器竞态。

当前实现包括 SQLite、PostgreSQL、Web 管理及无状态管理员认证，仍限定单实例。完整产品 gates 见[构建与验证](build-testing.md)，历史阶段见[路线图](roadmap.md)。真实部署验证、fixture 协议验证、依赖图与交叉编译应分别报告。
