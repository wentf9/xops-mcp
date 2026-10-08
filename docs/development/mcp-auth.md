# MCP 客户端认证

[English](../en/development/mcp-auth.md) · [开发指南](README.md)

用户操作见 [MCP Token 管理](../user/tokens.md)。管理员 JWT 与 MCP Token 使用独立的认证入口。

## 存储与身份

`internal/mcpauth` 管理凭据，`mcp_tokens` 表保存 `id`、`client_id`、名称、展示前缀、SHA-256 摘要、记录版本、启用状态、节点范围、创建时间、到期时间及吊销时间。SQLite schema v8 和 PostgreSQL schema v5 提供相同契约。Token 使用系统随机源生成，完整值仅在创建响应中返回；不保存可恢复的明文或密文。format 3 备份归档包含摘要、状态和节点绑定，跨后端恢复保留所有标识。

控制台创建 Token 时分配独立的随机 Token ID 和客户端 ID。编辑名称、节点范围或有效期不改变身份。当前一个新 Token 对应一个新客户端；后续可增加客户端实体和同一客户端的凭据轮换，轮换应保留已存储的客户端 ID。所有客户端共享操作策略，节点授权按各 Token 的范围检查。

`nodeScope: "all"` 允许所有当前及未来节点；`nodeScope: "selected"` 仅允许 `nodeIDs` 中的稳定节点 ID，空数组表示不允许任何节点。数据库升级把已有 Token 设为 `all`，预置初始 Token 也默认为 `all`。节点名称和别名不是授权依据，改名不影响绑定；删除保留原绑定，永久墓碑阻止 ID 复用，新建同名节点不会继承权限。编辑可保留已有的删除节点绑定，但新增绑定必须指向现存节点。

Token 写入使用记录自身的版本进行 CAS，和库存 revision 分离；元数据变更与脱敏审计在同一事务提交。吊销保留记录且不可恢复。可选 `mcp_token_file` 仅用于初始凭据登记，启动时按摘要查重，不覆盖已有记录的身份、启停或吊销状态。

预置文件首次登记时，客户端 ID 沿用固定 Token 模式的 SHA-256 作用域，随后持久保存。这样保留现有 journal 的任务归属、request-ID 索引和授权 Binding，已存记录及 unknown 目标锁无需改写；后续认证始终读取记录中的客户端 ID。

## HTTP 接入

共享内核 `HTTPOptions.TokenVerifier` 由 `Manager.Verify` 提供，每次 MCP 请求查询数据库；禁用、过期或吊销即拒绝。返回 `auth.TokenInfo.UserID = client_id`、`Extra["tokenID"] = id`，以及可选到期时间。服务不再将部署文件作为独立的认证旁路。

数据库验证前使用独立的认证并发额度，上限为内核 `MaxRequests`（默认 64）；饱和时立即返回 HTTP 429 `authentication_limit`。额度仅覆盖验证阶段，验证返回即释放，普通请求和控制消息继续分别受各自通道限制。

SDK 将会话绑定到 `UserID`。内核将每个工具请求的已验证身份从 `RequestExtra` 带入执行上下文，`ClientIdentityFromContext` 可供宿主使用。操作 Binding、文件任务 Scope、重试、查询及取消使用客户端 ID，防止跨客户端访问任务。缺少身份时拒绝动态认证调用，不能回退到全局身份。

节点列表按当前 Token 的范围过滤；工具解析名称、别名或 ID 后检查稳定目标 ID，批量目标逐个检查。授权检查覆盖 SSH、SFTP、文件操作及传输准备，执行准入再次读取最新范围。目标节点的配置跳板属于连接依赖，允许其转发流量不授予直接执行跳板节点操作的权限。

文件传输路由保留单任务短期凭据及原授权绑定，并在内容传输准入和上传提交准入时检查原客户端的最新 Token 状态、有效期及目标范围，不能用已签发凭据绕过收窄后的范围。同一有效客户端仍可查询或取消历史任务，以及取得已完成或不再处于 ready 状态的准备结果；这些操作不会重新获取远端访问权限。失败任务清理保留原授权以移除临时数据。

撤销与范围变更控制后续准入；已经准入的工作不保证取消。运行中任务的即时身份撤销与多实例支持尚未实现。

## 管理 API

路径相对于 `<web_base_path>/api/v1/`。全部要求管理 JWT；写入要求同源 Origin。

| 路由 | 契约 |
| --- | --- |
| `GET /mcp-tokens` | 返回 `{tokens: [...]}`，只含元数据 |
| `POST /mcp-tokens` | 接受 `name`、`enabled`、`expiresAt`、`nodeScope`、`nodeIDs`，返回 201 `{item, token}`；完整 Token 只返回一次 |
| `PUT /mcp-tokens/{id}` | 同创建输入，要求 `If-Match: "<记录 version>"`；省略两个节点范围字段时保留原授权 |
| `DELETE /mcp-tokens/{id}` | 永久吊销，要求记录版本；不删除审计与身份记录 |

时间字段为 Unix 秒，`expiresAt: 0` 表示永不过期。创建或调整有效期时必须使用未来时间；已过期记录可以保留原时间后停用。PUT/DELETE 返回更新后的元数据和 ETag。缺少版本返回 428，冲突或已吊销返回 412。UI 不在持久化浏览器存储中保存 MCP Token；一次性展示对话框关闭后清除内容，异步响应按管理员认证代次隔离。

创建时省略 `nodeScope` 和 `nodeIDs` 默认为全部节点；更新时同时省略则保留现有范围，不能让旧管理客户端的普通编辑扩大授权。调整范围须显式提供 `nodeScope`；`all` 不允许非空 `nodeIDs`。`selected` 的 ID 列表为集合，重复 ID 会拒绝，保存时按 ID 排序。元数据始终返回范围与绑定，Web 编辑按稳定 ID 恢复勾选，已删除绑定单独标记。

## 验证

`internal/mcpauth` 覆盖多 Token、生命周期、过期、节点范围、重启和归档；`internal/server/admin_tokens_test.go` 验证真实 HTTP 管理与 MCP 会话、客户端隔离及管理员权限隔离。`internal/server/token_history_test.go` 通过真实 HTTP/SSH/SFTP 验证静态 Token 转为数据库认证后仍可查询、重试原任务，并保持客户端隔离、授权绑定及 unknown 目标锁。上述测试均运行 SQLite/PostgreSQL。内核测试覆盖请求身份、文件任务隔离、空身份拒绝和错误脱敏；浏览器测试覆盖 HTTP/HTTPS 创建、一次性展示、节点绑定与刷新保留、空范围、搜索与转义、启停及吊销。
