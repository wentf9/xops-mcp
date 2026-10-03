# XOps MCP

[English](README_en.md)

面向自托管部署的 MCP 运维服务端，计划通过 Web 控制台管理节点，使用 SQLite 或 PostgreSQL 保存业务数据，复用 [xops-cli](https://github.com/wentf9/xops-cli) 的 SSH、SFTP 和 MCP 能力。

**当前阶段：仓库初始化与架构规划。** 已包含固定版本依赖、跨仓库兼容性测试和 CI；尚未提供可启动的服务端程序、Web 控制台或数据库实现。

## 两个仓库的关系

```text
xops-mcp（独立发布的服务端产品）
  Web / 管理 API / 数据库 / 服务端适配器
                     │ 固定 Go module 版本
                     ▼
xops-cli（CLI 产品与现阶段共享内核的源码仓库）
  pkg/mcpserver / pkg/ssh / pkg/sftp
                     ▲
                     │ 同仓库复用
  CLI / TUI / xops mcp
```

- `xops-mcp` 单向依赖 `xops-cli` 的公开 Go 包，`xops-cli` 不依赖 `xops-mcp`。
- MCP 工具、护栏、传输状态机和 SSH/SFTP 修复只维护一份共享实现。
- Web、管理 API、数据库模型、迁移及服务端凭据适配器归 `xops-mcp` 所有。
- 暂不创建第三个共享仓库；共享包的位置和版本策略见架构文档。
- 公共代码计划在上游收敛到可独立抽取的 `core/` 子树，旧 `pkg/*` 路径保留为兼容入口；上游本地迁移及独立抽取已通过验证，当前发布 pin 尚未升级。
- 初期按单实例设计：SQLite 为默认存储，PostgreSQL 为后续外部数据库选项。

## 设计与实施

- [架构与依赖复用决策](docs/architecture.md)
- [公共接口解耦与服务端接入](docs/interface-decoupling.md)
- [实施路线与验收条件](docs/roadmap.md)
- [当前依赖基线与验证范围](docs/reuse-baseline.md)
- [开发约定](AGENTS.md)

## 验证当前骨架

要求 Go 1.26+、golangci-lint v2。测试使用临时本地 HTTP 服务和合成节点，不连接真实 SSH 服务器，也不加载个人 XOps/OpenSSH 配置。

```sh
go build ./...
go test ./...
golangci-lint config verify
golangci-lint run ./...
go test -race -timeout=120s ./...
```

`go build ./...` 目前仅验证兼容性包，不生成服务端二进制。协议测试覆盖外部模块装配、HTTP 鉴权、MCP 握手、工具发现、节点查询和资源关闭；它不代表数据库、动态节点修改或实际 SSH/SFTP 传输已经实现或验证。

## 许可证

[MIT](LICENSE)，与上游项目保持一致。


新 core 接口另有隔离消费探针：`python3 scripts/check_core_consumer.py --upstream /path/to/xops-cli`。本地预览覆盖实际 SSH/SFTP、动态准入和关闭；它使用临时 module，不能替代发布后的 `--version EXACT_VERSION` 验收。详见[接口接入状态](docs/interface-decoupling.md)。
