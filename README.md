# XOps MCP

[English](README_en.md)

面向自托管部署的 MCP 运维服务端，计划通过 Web 控制台管理节点，使用 SQLite 或 PostgreSQL 保存业务数据，复用 [xops-cli](https://github.com/wentf9/xops-cli) 的 SSH、SFTP 和 MCP 能力。

**当前阶段：公共核心接入与消费者验证。** 已固定可从远端下载的共享 core 版本，常规 CI 覆盖 core 消费与旧入口兼容性；尚未提供可启动的服务端程序、Web 控制台或数据库实现。

## 两个仓库的关系

```text
xops-mcp（独立发布的服务端产品）
  Web / 管理 API / 数据库 / 服务端适配器
                     │ 固定 Go module 版本
                     ▼
xops-cli（CLI 产品与现阶段共享内核的源码仓库）
  core/mcp/runtime / core/ssh / core/sftp
                     ▲
                     │ 同仓库复用
  CLI / TUI / xops mcp
```

- `xops-mcp` 单向依赖 `xops-cli` 的公开 Go 包，`xops-cli` 不依赖 `xops-mcp`。
- MCP 工具、护栏、传输状态机和 SSH/SFTP 修复只维护一份共享实现。
- Web、管理 API、数据库模型、迁移及服务端凭据适配器归 `xops-mcp` 所有。
- 暂不创建第三个共享仓库；共享包的位置和版本策略见架构文档。
- 公共实现已收敛到上游可独立抽取的 `core/` 子树，旧 `pkg/*` 保留兼容入口；本仓库直接验证 core，并使用固定远端提交而非本地替换。
- 初期按单实例设计：SQLite 为默认存储，PostgreSQL 为后续外部数据库选项。

## 设计与实施

- [架构与依赖复用决策](docs/architecture.md)
- [公共接口解耦与服务端接入](docs/interface-decoupling.md)
- [实施路线与验收条件](docs/roadmap.md)
- [当前依赖基线与验证范围](docs/reuse-baseline.md)
- [开发约定](AGENTS.md)

## 验证当前骨架

要求 Go 1.26+、golangci-lint v2。测试使用临时本地 HTTP 与 SSH/SFTP fixture、合成节点和凭据，不读取个人 XOps/OpenSSH 配置，也不连接部署环境中的主机。

```sh
GOWORK=off go build ./...
GOWORK=off go test ./...
golangci-lint config verify
golangci-lint run ./...
go test -race -timeout=120s ./...
```

`go build ./...` 目前验证消费者与兼容性包，不生成服务端二进制。测试覆盖 HTTP 鉴权、MCP 握手与工具集合、SSH 命令、二进制 SFTP 上传/下载、内存协调器动态禁用及资源回收。`internal/dependencycheck` 单独检查 core 消费图的 Linux/Windows/macOS 导入边界；这些图检查不是原生平台运行证据。数据库/Web 尚未实现。

固定版本的独立消费者验收：`python3 scripts/check_core_consumer.py --version v0.13.1-0.20261003020948-b8ee0ed9f1a0`。本地联调仍可使用 `--upstream /path/to/xops-cli`，替换仅写入临时 module。详见[接口接入状态](docs/interface-decoupling.md)。

## 许可证

[MIT](LICENSE)，与上游项目保持一致。
