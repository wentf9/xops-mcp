# 架构与跨仓库依赖复用

共享 core 已通过固定远端版本接入；core 消费与依赖边界检查已纳入常规 CI。M2 SQLite 与独立 HTTP 服务已实现，Web 与 PostgreSQL 仍属后续阶段，版本与证据见[依赖基线](reuse-baseline.md)，接入契约见[接口解耦](interface-decoupling.md)。

状态：共享接口和 M2 独立服务已实现；Web、管理 API、管理员会话和 PostgreSQL 尚未实现。运行说明见[服务指南](server.md)。

上游公共实现已收敛到可独立抽取的 `xops-cli/core/`，CLI 直接导入 core，必要的宿主装配留在应用层。消费者的所有生产和测试代码仅使用上游 core 包；旧入口探针已删除。详细接口、更新一致性和验收见[公共接口解耦](interface-decoupling.md)。

## 1. 产品边界

`xops-cli` 保留交互式 CLI/TUI、本地 YAML 与凭据库、OpenSSH 集成和现有 MCP 入口。`xops-mcp` 负责长期运行的服务端、Web 控制台、管理 API、业务数据库、服务端凭据与部署。

两个产品独立发布。服务端使用 Go 1.26+，前后端同仓库，计划通过 `go:embed` 将 Web 构建产物打包进单个程序。首版以 Linux 单实例和 Streamable HTTP 为目标，保留上游 stdio 行为；不扩展 HTTP 隧道或 SOCKS 工具。

## 2. 决策：单向模块依赖，一份共享内核

```mermaid
flowchart TD
    Server[xops-mcp: 服务装配、Web、管理 API] --> Adapters[xops-mcp: 数据库与运行时适配器]
    Adapters --> MCP[xops-cli/core/mcp/runtime]
    CLI[xops-cli: CLI / TUI / MCP 入口] --> Host[internal/mcphost + internal/sshenv + pkg/adapter]
    Host --> MCP
    MCP --> SSH[xops-cli/core/ssh]
    MCP --> SFTP[xops-cli/core/sftp]
    SFTP --> SSH
    Adapters --> DB[SQLite / PostgreSQL]
```

共享内核暂时保留在 `xops-cli` Go module 中。图中为已实现的公共复用路径；SQLite 适配器和服务入口已交付，Web 与 PostgreSQL 按后续阶段实施。新仓库的独立性体现为独立入口、业务存储、Web 和发布周期；共享协议与执行实现依靠版本化依赖复用。

不复制 `pkg/mcpserver`、`pkg/ssh` 或 `pkg/sftp` 建立长期分叉，不使用 Git submodule，不提交指向相邻目录的 `replace`。`xops-cli` 的源码、模块及常规 CI 均不得依赖新服务仓库，避免依赖环和私有服务逻辑侵入 CLI。

第三个 `xops-core` module/repository 仅在共享包已经解耦，并且出现额外消费者、独立维护团队或实际发布阻塞时重新评估。届时两个产品同时依赖新的中立模块；不形成两个仓库互相导入的过渡状态。

## 3. 源码归属与导入边界

下表列出唯一实现归属；CLI 宿主只负责装配，不形成第二份实现。

| 能力 | 唯一源码归属 | 新服务的接入方式 |
| --- | --- | --- |
| SSH 连接、ProxyJump、提权、取消和回收 | `xops-cli/core/ssh` | 公共接口，禁止复制实现 |
| SFTP 子系统与文件操作 | `xops-cli/core/sftp` | 公共接口，保留超时、权限与替换语义 |
| MCP 工具 schema、处理器、护栏与协议约束 | `xops-cli/core/mcp` | 通过公共 ports 装配 `runtime` 和 `sshexec` |
| HTTP 文件传输、幂等与恢复状态机 | `xops-cli/core/mcp/transfer` | 首期沿用独立本地 journal；后续仅通过明确的存储接口扩展 |
| 本地 YAML、CLI 凭据库、TUI、命令入口 | `xops-cli` | CLI 自己使用；服务端不调用命令或启动子进程包装 CLI |
| Web、管理 API、管理员会话、数据库迁移 | `xops-mcp` | 服务端业务层直接拥有 |
| 数据库到共享内核的转换与凭据解析 | `xops-mcp/internal/adapters/xops` | 消费方实现接口，禁止共享内核回调服务端具体包 |
| SQLite/PostgreSQL 实体与查询 | `xops-mcp/internal/storage`（SQLite 已实现，PostgreSQL 计划） | 不作为 MCP schema 或 CLI 数据结构公开 |

所有生产代码和测试仅使用上游 `core/*` 公共包。配置、模型、凭据与终端适配器不进入消费者依赖图。`internal/dependencycheck` 在 Linux/Windows/macOS 检查整个 module 的生产和测试图，并验证固定远端版本。

禁止导入上游 `cmd`、`cmd/sftpshell`、`pkg/tui`，或从新模块直接导入上游 `internal/*`。上游公开包在其自身 module 内间接使用 `internal/*` 符合 Go 规则，不能据此声称已经完成底层解耦。

## 4. 已验证的接缝与宿主职责

固定版本与验证范围见[依赖基线](reuse-baseline.md)。

- `core/mcp/runtime.NewRuntime` 通过 `WithDependencies` 接收 State、Gate、Backend factory 和 Audit；部分构造失败也回收已拥有资源。
- `core/mcp/state.Coordinator` 提供一致快照、发布屏障和原子准入，普通编辑保留已准入目标；禁用和撤销取消可取消的相关工作。
- SSH 通过完整 ConnectionPlan 与版本化租约复用连接；SecretResolver、KeySource 和 HostKeyVerifier 由宿主注入，不自动读取个人目录或凭据。
- 文件任务保留原授权绑定、v1/v2 journal 和未知提交锁；流式传输与提交在同一个连接上交接许可。
- CLI 配置、具体凭据库、OpenSSH 发现和 Windows 输入桥留在 internal/mcphost、internal/sshenv 及应用适配器，core 编译图不包含它们。
- M2 已实现数据库事务、稳定域/版本、凭据加密和主机信任存储，并通过这些接口接入。服务端测试覆盖真实本地协议；Web 管理仍属后续阶段。

## 5. 共享内核的接入契约（已实现）

`xops-cli/core` 提供消费方向明确的能力接口，CLI 直接使用核心 API，应用适配器保持命令、配置和协议行为：

1. **执行依赖注入**：允许 `Runtime` 接收 SSH/SFTP 执行服务及其生命周期所有权；运行时自建资源由运行时关闭，借用资源由宿主关闭，契约必须明确，禁止隐式双重所有权。
2. **每次操作的配置视图**：在工具开始或传输准备时获取一致的节点、身份、完整 ProxyJump 链及配置版本；该视图贯穿审批、凭据解析与执行。不能让一次操作多次读取不同版本。
3. **策略与禁用状态**：新操作读取最新策略；已批准但尚未执行的操作在实际执行前检查禁用、撤销及版本条件。普通编辑不把已开始的操作切换到新目标。
4. **连接代际与失效**：连接按目标、身份/凭据、完整跳板链和信任版本区分代际；变更使旧代际退出复用。运行中的操作保持原视图或按明确撤销规则取消；不能通过关闭所有连接实现普通单节点编辑。
5. **凭据绑定**：解析请求绑定节点、目标、用途和版本，版本冲突明确失败；秘密不得经 Web DTO、MCP 节点列表或审计日志泄露。
6. **策略/审计适配**：保留共享决策和审批逻辑，提供可注入的策略读取及审计输出；具体数据库实现留在新仓库。

已实现接口包括 StateSource、ExecutionGate、Backend 与 AuditSink，以及 SSH 的 KeySource、HostKeyVerifier 和 InputBridge；消费者通过固定版本编译和行为契约验证这些接口。Web 更新须接入发布协调器，不能仅删除 `Frozen()` 或为每次修改重建整个 MCP runtime；后者会终止已有会话和传输。

## 6. 新服务内部边界

```text
cmd/xops-mcp/             进程入口、配置、信号与生命周期装配
internal/api/            Web 管理 API 与管理员会话
internal/service/        节点、身份、凭据、策略及运行操作
internal/adapters/xops/  共享内核的配置、凭据、执行与审计适配器
internal/storage/       业务存储接口与事务边界
internal/storage/sqlite/
internal/storage/postgres/
internal/storage/sqlite/migrations/  嵌入的 SQLite schema 迁移
internal/coreconsumer/   已有的 core 消费者契约

internal/dependencycheck/ 已有的依赖边界与版本检查
web/                    Web 源码与嵌入资源
```

`cmd/xops-mcp`、`internal/{command,config,server,service,adapters,storage,secure,importer}`、消费者和依赖检查已建立；`internal/api`、PostgreSQL 和 Web 仍属计划。Web API 和 MCP 共用 service 层规则。HTTP 路由由宿主组合：`/mcp` 和 `/v1/transfers/` 使用共享 handler；`/api/v1/` 和 Web 静态路由计划由新仓库提供，目前没有这些路由。各自鉴权独立，不能把管理 Cookie、MCP Token 和短期传输凭据混为一类。

共享包不得 import 新服务的数据库、HTTP API、前端、ORM 或依赖注入容器。

## 7. 数据持久化与更新语义

SQLite 为首个实现，文件位于持久化本地数据目录，启用外键、WAL 和有界 busy 等待；应用层仍需事务、超时和并发控制。PostgreSQL 是明确的第二种后端；首期不承诺任意 SQL 数据库兼容。

已实现 hosts、identities、nodes、tags、credential metadata/ciphertext、policies 和 audit events；admin sessions 属于 M3。节点使用稳定的不透明 ID，地址、端口、用户名和别名均可编辑；导入时记录旧 selector 到新 ID 的映射。

存储接口按业务操作设计，支持跨节点、身份、凭据引用的原子事务和版本前置条件。M2 导入使用 expected revision，Web 更新计划使用 revision/ETag；冲突返回明确错误。避免仅提供通用表 CRUD 或把完整 YAML 存成单个 blob。

数据库事务提交后发布新的配置版本；发布前不向调用者宣称新配置已生效。若发布失败，停止受影响的新操作并重试加载已提交版本；不能使用旧视图继续接纳操作，也不能盲目重复数据库变更。进程重启从数据库重建当前版本。

密码、私钥及 passphrase 使用带版本的认证加密，主密钥由数据库外的部署配置提供。备份方案同时明确密钥备份和恢复责任。数据库持有密文不等于已解决运行时 SSH 密钥加载。

M2 使用独立 MCP Token；M3 计划使用单管理员会话和 CSRF 防护，凭据读取只返回元数据，不扩展多租户或 OAuth。

传输 journal 初期继续使用共享实现的专属本地目录；数据库保存业务实体不会自动替代它。备份/恢复应协调业务数据库、密钥和 journal。`unknown` 表示远端提交结果不确定，不能自动重试或把它转换成成功。多实例部署不在首版范围内；外部数据库无法自动共享 MCP 会话、SSH 连接或 journal 所有权。

## 8. 版本、开发与发布

- 每次发布都提交确定的 `go.mod` / `go.sum`。正式 tag 可用时优先采用；必要时使用规范 pseudo-version，不使用浮动分支。未合入主线的修复可先固定到已发布修复分支的精确提交，保留该分支直到当前 pin 由长期维护引用保留或升级。清理分支前，以空模块缓存和 `GOPROXY=direct` 验证固定版本仍可下载，不能只依靠已有缓存或代理缓存。
- 当前基线固定到包含主机密钥算法协商修复的上游 master 提交，不依赖保留修复分支；版本、来源和校验记录见依赖基线。规范 pseudo-version 不等于正式发布 tag。
- 本地跨仓库联调可在两个仓库之外建立 `go.work`。其文件不进入任何产品仓库；CI 使用 `GOWORK=off`，验证真实模块下载与版本依赖。
- 发布顺序为：上游核心改动和回归测试通过、提交可从远端获取、新仓库升级固定版本、消费者测试与产品测试通过、新服务发布。版本升级不能依赖未提交的相邻 checkout。
- `v0` 版本升级同样需要检查公开 API 和行为差异。正式破坏性 Go API 升级遵循新的主版本 module path。
- MCP 名称、schema、传输行为与错误语义由共享内核统一维护；必要的变化需要两种产品兼容测试。服务端专属管理功能留在 Web API。
- 上游 CLI 保留原有构建、测试、lint 和平台验证，并增加仅复制 core 子树的独立 module 抽取检查；新仓库验证外部调用契约及服务端能力，不重复复制整套 CLI 测试。

## 9. 兼容迁移与验收

现有 `xops mcp` 入口继续可用，不在初始化阶段删除或改变默认行为。配置导入先支持 dry-run、冲突报告、稳定 ID 映射和数据库事务；凭据须单独授权导入并通过新的加密存储写入，不能复制旧引用后假定仍可解析。配置导入是一次性迁移，不建立两个产品同时写同一份数据的关系。

上线前至少验证：固定版本独立构建、MCP 工具发现与实际调用、Web 修改后新操作生效、审批后修改目标的拒绝、凭据轮换与共享身份/跳板变更失效、资源关闭、SSH/SFTP 实际闭环、传输幂等/unknown 恢复、数据库迁移和版本冲突。具体里程碑见 [路线图](roadmap.md)。
