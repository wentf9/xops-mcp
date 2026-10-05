# XOps MCP

[English](README_en.md)

面向自托管部署的 MCP 运维服务端，使用 SQLite 或 PostgreSQL 保存节点与加密凭据，复用 [xops-cli](https://github.com/wentf9/xops-cli) 的 SSH、SFTP 和 MCP 能力。

**当前阶段：M4 PostgreSQL 后端。** 已提供单管理员控制台、在线节点与凭据管理、主机公钥确认、策略、审计，以及 SQLite、HTTP MCP 和文件传输。面向 Linux 单实例；已提供 PostgreSQL、加密离线数据库导出/导入和等价校验。

## 启动服务

```sh
GOWORK=off go build -o bin/xops-mcp ./cmd/xops-mcp
mkdir -m 700 -p .local
cp examples/server.yaml .local/server.yaml
bin/xops-mcp keygen --out .local/master.key
bin/xops-mcp keygen --out .local/mcp.token
bin/xops-mcp keygen --out .local/admin.setup
bin/xops-mcp migrate --config .local/server.yaml
bin/xops-mcp serve --config .local/server.yaml
```

打开 `http://127.0.0.1:8081/`，用 `admin.setup` 中的凭据初始化管理员，再通过控制台管理节点。MCP 独立监听 `http://127.0.0.1:8080/mcp`，客户端使用独立的 `mcp.token`。已有部署保留原主密钥与数据，迁移后配置管理员即可。详见 [Web 控制台指南](docs/web-console.md)。

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
- 公共实现已收敛到上游可独立抽取的 `core/` 子树，CLI 直接导入 core，必要的宿主装配留在应用层；本仓库直接验证 core，并使用固定远端提交而非本地替换。
- 初期按单实例设计：SQLite 为默认存储，PostgreSQL 为可选外部数据库。

## 设计与实施

- [Web 控制台、API 与部署](docs/web-console.md)

- [服务部署与配置导入](docs/server.md)
- [PostgreSQL 与离线数据库迁移](docs/postgresql.md)
- [架构与依赖复用决策](docs/architecture.md)
- [公共接口解耦与服务端接入](docs/interface-decoupling.md)
- [实施路线与验收条件](docs/roadmap.md)
- [当前依赖基线与验证范围](docs/reuse-baseline.md)
- [开发约定](AGENTS.md)

## 开发验证

要求 Go 1.26+、golangci-lint v2。测试使用临时本地 HTTP 与 SSH/SFTP fixture、合成节点和凭据，不读取个人 XOps/OpenSSH 配置，也不连接部署环境中的主机。

```sh
GOWORK=off go build ./...
GOWORK=off go test ./...
golangci-lint config verify
golangci-lint run ./...
go test -race -timeout=120s ./...
```

测试覆盖 SQLite/PostgreSQL 迁移和事务、跨后端归档等价性、数据库所有权和连接池关闭、加密凭据与绑定、原子发布/不确定提交恢复、HTTP 鉴权、真实本地 SSH/SFTP、加密私钥与 ProxyJump、journal 重启/unknown 锁和进程关闭。`internal/dependencycheck` 检查 Linux/Windows/macOS 全量生产与测试依赖图；图检查不代表 Windows/macOS 原生运行验证。

固定版本的独立消费者验收：`python3 scripts/check_core_consumer.py --version v0.13.1-0.20261003125647-0d4bd2fb866c`。本地联调仍可使用 `--upstream /path/to/xops-cli`，替换仅写入临时 module。详见[接口接入状态](docs/interface-decoupling.md)。

## 许可证

[MIT](LICENSE)，与上游项目保持一致。
