# 实施路线与验收条件

共享 core 已通过固定远端版本接入；M2 SQLite 与独立 HTTP MCP 服务已实现，运行说明见[服务指南](../user/install.md)。M3 Web 管理与部署及 M4 PostgreSQL 后端已实现，版本与证据见[依赖基线](reuse-baseline.md)，接入契约见[接口解耦](interface-decoupling.md)。

M0 仓库骨架、M1 共享接口消费和 M2 独立服务已实现；上游已固定到包含主机密钥算法协商修复的远端版本，来源与验收见依赖基线。M4 已实现独立 PostgreSQL 存储及显式离线数据库迁移；阶段顺序表达依赖关系。

## M0：仓库与可验证的复用基线

归属：`xops-mcp`。

- 建立独立 Go module、MIT 许可证、双语说明、开发约定和 CI。
- 固定远端可获取的 `xops-cli` 提交，禁止提交本地 `replace`。
- 通过公共 API 装配 HTTP MCP runtime，验证鉴权、握手、工具发现、合成节点查询及关闭。
- 检查实际编译依赖不包含 CLI/TUI；记录尚存配置/凭据耦合。
- 输出源码归属、上游改造契约、数据库边界和分阶段验收条件。

完成证据：[依赖基线](reuse-baseline.md)。本阶段没有服务端产品二进制。

## M1：共享内核的服务端接缝（已接入）

归属：`xops-cli` 改造公开包；`xops-mcp` 在独立模块中增加消费者验收。

详细设计见[公共接口解耦](interface-decoupling.md)。上游按 D1 公共叶子层、D2 SSH/SFTP、D3 MCP runtime、D4 动态准入、D5 持久任务、D6 消费与抽取验收逐步实施。公共实现移动到 `core/`，CLI 直接导入 core，旧 Go API 兼容外观已移除；暂不创建独立共享仓库。

1. 增加 Runtime 的执行服务、策略/审计注入点，明确所有权和关闭契约。
2. 增加每次操作的不可变配置视图、版本化凭据解析、执行前撤销检查。
3. 提供连接代际/定向失效，覆盖共享身份与多级 ProxyJump 的依赖关系。
4. 明确服务端主机密钥存储及数据库私钥到 SSH 的接入方式；不隐式访问运行用户的个人配置。
5. 保持 CLI 默认行为、stdio 与 HTTP 工具集合、传输状态语义兼容。

验收：现有 CLI 全量 gates；消费者接口测试；操作中修改目标、审批后轮换凭据、删除/禁用节点、共享身份及跳板链修改的回归；取消、超时、Close 幂等与 goroutine 回收；Linux/Windows/macOS 依赖图检查，以及只复制 core 子树、无原仓库依赖/replace 的独立 module 构建测试。通过后发布可固定的上游版本，再升级新仓库。

消费者通过内存协调器验证动态禁用、真实 SSH/SFTP 和资源回收；固定远端版本验收通过。M2 另有数据库与真实协议测试，Web 编辑由 M3 交付。

## M2：SQLite 与可独立启动的 MCP 服务（已实现）

归属：`xops-mcp`；共享内核缺失能力继续回到 `xops-cli` 实现。

- 建立进程入口、配置、持久化目录、迁移命令和优雅退出。
- 实现节点、主机、身份、标签、密文凭据与策略的业务事务；实现单向配置导入 dry-run。
- 建立数据库适配器，原子发布配置版本，接入连接失效和执行前检查。
- 提供 MCP HTTP、独立 Token、现有文件传输路由及专属本地 journal。
- 保持客户端本地路径只在客户端解释；目录由客户端归档。

验收：空库迁移、已有库升级、重启恢复、版本冲突与发布失败、凭据加解密/轮换、真实 SSH 命令和 SFTP 上传/下载、幂等与 unknown 恢复，以及未登录访问拒绝。

实现入口为 `cmd/xops-mcp`，提供 `keygen`、`migrate`、`serve`、`status`、`import`、`recover`。SQLite 使用独立关系表、稳定部署 ID、版本化 AES-GCM 密文、历史来源和删除墓碑；`internal/service` 用 core 协调器执行事务前屏障和事务后发布。`Reconcile` 只重读已提交数据并重试发布，不重放业务写入。

验证覆盖数据库 schema 升级/未来版本拒绝、回滚与 revision 冲突、提交结果不确定、发布失败、凭据旋转、普通编辑保留原目标、删除后 ID 不复用，以及真实本地 HTTP/SSH/SFTP、加密私钥、ProxyJump、禁用、主机密钥拒绝、传输幂等和 unknown 重启/原绑定核验。服务和消费者分别运行 race 与 goleak；测试不使用个人或部署环境凭据。

M2 交付离线导入和运行中更新的 service 契约；M3 已接入 Web/API 调用入口。CLI 导入支持的字段和未迁移凭据的处理见服务指南。Linux 原生测试与多平台依赖/交叉构建分开记录；M2 不宣称 Windows/macOS 原生服务验收、线上主机验收或 Web 产品完成。

## M3：Web 节点管理与部署（已实现）

归属：`xops-mcp`。

- 实现单管理员初始化、登录、JWT 过期与同源保护。
- 实现节点/身份/标签管理、连接测试、主机密钥确认、凭据更新和策略管理。
- 编辑接口使用 ETag/revision；凭据 API 仅显示元数据。
- 提供审计查询，明确运行中任务状态与节点禁用/撤销语义。
- 构建 Web 资源并嵌入 Go 程序，提供容器、数据卷、备份/恢复和反向代理示例。

验收：浏览器 CRUD 流程、并发编辑冲突、Web 修改后 MCP 可见性、凭据不泄露、会话与 MCP Token 权限隔离、重启后持久化、已运行任务不被普通编辑错误重定向。

实现包含 SQLite v4 管理员与会话迁移、v5 独立标签与主键关联迁移、Web/离线管理员初始化、15 分钟无状态 JWT、可配置 HTTP/HTTPS 和 JWE 密码请求加密、同源保护、If-Match 管理 API、绑定 JWT 与配置版本的公钥确认、只做 SSH 认证的连接测试、游标审计查询和活跃许可视图。管理端口与 MCP 端口分离，管理页面、资源、API 支持固定前缀，JWT 绑定此前缀。Web 资源直接嵌入二进制；部署示例覆盖 Docker/Compose、systemd、Nginx 和离线备份恢复。

自动化验收使用隔离 SSH fixture，覆盖浏览器初始化/登录、CRUD、公钥确认、真实连接、并发编辑冲突、XSS 转义、标签/策略/审计、移动布局及退出；Go 集成测试覆盖 Web 更新与同一 MCP 会话、凭据轮换、权限隔离、迁移与重启、备份副本中的 unknown 任务和原绑定核验。当前未在生产主机或原生 Windows/macOS 上验收；运行中视图仅跟踪许可，不替代传输 journal。使用方式见 [Web 控制台指南](../user/console.md)。

## M4：PostgreSQL 后端（已实现）

归属：`xops-mcp`。

- 实现独立 PostgreSQL schema 迁移和查询，不假定 SQLite SQL 可以直接重用。
- 对两个后端运行同一组业务契约测试，加上各自锁/事务与迁移测试。
- 需要从 SQLite 迁移时提供显式离线导入导出和校验；切换连接字符串不会自动搬迁数据。

验收：数据等价、事务回滚、乐观并发、迁移失败恢复、备份恢复、凭据密钥可用性和连接池关闭。

实现包括独立 PostgreSQL v1/v2 迁移、JSONB/BYTEA/identity 查询、repeatable read 读快照、revision 事务、固定物理会话上的数据库 advisory lock、本地 journal 文件锁和连接丢失后的关闭流程。配置使用 `database_driver` 与私有 `postgres_dsn_file`；默认仍为 SQLite。`db-export`、`db-import`、`db-verify` 使用版本化加密归档，保留部署身份、历史凭据/来源、墓碑、管理员及审计，只允许向空目标原子恢复；会话不迁移，journal 和密钥单独备份。

共同存储契约验证两后端的数据行为、回滚、并发 revision、审计/管理员凭据和四种归档方向；同一组 service、管理 API、HTTP MCP、真实 SSH/SFTP/ProxyJump 及 unknown 恢复测试分别使用两后端。PostgreSQL 专项覆盖空库、已有 schema 升级、失败迁移回滚重试、未来版本拒绝、跨目录所有权排他、锁等待取消、错误密钥和连接池/监听器回收。CI 提供 PostgreSQL 18 并运行双后端 race 矩阵。运行与备份说明见 [PostgreSQL 指南](../user/postgresql.md)。当前证据来自 Linux 隔离 fixture，未验收生产主机、原生 Windows/macOS 或高可用故障切换；浏览器自动化使用 SQLite，PostgreSQL 使用相同 Go API 集成验收。

PostgreSQL 支持仍限定单服务实例。高可用、分布式任务调度、共享会话、多租户和独立 `xops-core` 不属于本轮路线的交付承诺。

## 管理认证演进（已实现）

管理员认证使用 15 分钟 JWT，退出清除客户端令牌，改密后的旧 JWT 自然到期；验签不访问会话存储。初始化、登录和改密使用标准 JWE，并通过配置选择 HTTP 或 HTTPS，90 秒签名挑战绑定用途及改密 JWT。SQLite v6 / PostgreSQL v3 移除旧会话表。认证实例共享部署 ID、前缀和外部密钥即可互通，完整产品的数据库锁、运行状态和 journal 仍为单实例。认证细节和验收见[认证契约](admin-auth.md)。

## MCP Token 管理（已实现）

控制台管理多个 Token，保存摘要、记录版本和独立客户端 ID，支持创建、编辑、独立绑定节点范围、过期、停用及永久吊销。SQLite v8 / PostgreSQL v5 和 format 3 加密归档保存相同身份、状态与节点绑定。共享内核使用可注入认证入口并隔离客户端会话、操作绑定和文件任务。详见 [MCP 客户端认证](mcp-auth.md)。节点列表与工具准入按 Token 范围检查，文件内容传输与上传提交准入重新检查最新权限；已有 Token 默认允许全部节点。凭据轮换和运行中任务的即时身份撤销留待后续实现。

## 持续发布约束

两边代码改动必须同步测试与相关文档。任何代码 push 或 PR 之前执行 `go build ./...`、`go test ./...`、`golangci-lint run ./...`；修改 lint 配置后执行 `golangci-lint config verify`。真实远端验证、单元测试、跨平台编译和原生平台运行分别记录。

共享 Bug 在 `xops-cli` 增加回归并修复，新服务升级依赖获得修复；服务端数据库/Web Bug 在新仓库修复。兼容性探针不是完整产品验收，也不替代上游测试。
