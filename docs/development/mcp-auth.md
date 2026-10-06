# MCP 客户端认证

[English](../en/development/mcp-auth.md) · [开发指南](README.md)

用户操作见 [MCP Token 管理](../user/tokens.md)。管理员 JWT 与 MCP Token 使用独立的认证入口。

## 存储与身份

`internal/mcpauth` 管理凭据，`mcp_tokens` 表保存 `id`、`client_id`、名称、展示前缀、SHA-256 摘要、记录版本、启用状态、创建时间、到期时间及吊销时间。SQLite schema v7 和 PostgreSQL schema v4 提供相同契约。Token 使用系统随机源生成，完整值仅在创建响应中返回；不保存可恢复的明文或密文。备份归档包含摘要与状态，跨后端恢复保留所有标识。

控制台创建 Token 时分配独立的随机 Token ID 和客户端 ID。编辑名称或有效期不改变身份。当前一个新 Token 对应一个新客户端；后续可增加客户端实体、同一客户端的凭据轮换和独立权限，轮换应保留已存储的客户端 ID。当前所有客户端共享库存和策略。

Token 写入使用记录自身的版本进行 CAS，和库存 revision 分离；元数据变更与脱敏审计在同一事务提交。吊销保留记录且不可恢复。可选 `mcp_token_file` 仅用于初始凭据登记，启动时按摘要查重，不覆盖已有记录的身份、启停或吊销状态。

预置文件首次登记时，客户端 ID 沿用固定 Token 模式的 SHA-256 作用域，随后持久保存。这样保留现有 journal 的任务归属、request-ID 索引和授权 Binding，已存记录及 unknown 目标锁无需改写；后续认证始终读取记录中的客户端 ID。

## HTTP 接入

共享内核 `HTTPOptions.TokenVerifier` 由 `Manager.Verify` 提供，每次 MCP 请求查询数据库；禁用、过期或吊销即拒绝。返回 `auth.TokenInfo.UserID = client_id`、`Extra["tokenID"] = id`，以及可选到期时间。服务不再将部署文件作为独立的认证旁路。

数据库验证前使用独立的认证并发额度，上限为内核 `MaxRequests`（默认 64）；饱和时立即返回 HTTP 429 `authentication_limit`。额度仅覆盖验证阶段，验证返回即释放，普通请求和控制消息继续分别受各自通道限制。

SDK 将会话绑定到 `UserID`。内核将每个工具请求的已验证身份从 `RequestExtra` 带入执行上下文，`ClientIdentityFromContext` 可供宿主使用。操作 Binding、文件任务 Scope、重试、查询及取消使用客户端 ID，防止跨客户端访问任务。缺少身份时拒绝动态认证调用，不能回退到全局身份。

此处撤销控制后续 MCP 请求；已经准入的工作不保证取消。文件传输路由继续使用单任务短期凭据，按其有效期和原授权绑定执行。客户端级权限、运行中任务的身份撤销与多实例支持尚未实现。

## 管理 API

路径相对于 `<web_base_path>/api/v1/`。全部要求管理 JWT；写入要求同源 Origin。

| 路由 | 契约 |
| --- | --- |
| `GET /mcp-tokens` | 返回 `{tokens: [...]}`，只含元数据 |
| `POST /mcp-tokens` | 接受 `name`、`enabled`、`expiresAt`，返回 201 `{item, token}`；完整 Token 只返回一次 |
| `PUT /mcp-tokens/{id}` | 同创建输入，要求 `If-Match: "<记录 version>"` |
| `DELETE /mcp-tokens/{id}` | 永久吊销，要求记录版本；不删除审计与身份记录 |

时间字段为 Unix 秒，`expiresAt: 0` 表示永不过期。创建或调整有效期时必须使用未来时间；已过期记录可以保留原时间后停用。PUT/DELETE 返回更新后的元数据和 ETag。缺少版本返回 428，冲突或已吊销返回 412。UI 不在持久化浏览器存储中保存 MCP Token；一次性展示对话框关闭后清除内容，异步响应按管理员认证代次隔离。

## 验证

`internal/mcpauth` 覆盖多 Token、生命周期、过期、重启和归档；`internal/server/admin_tokens_test.go` 验证真实 HTTP 管理与 MCP 会话、客户端隔离及管理员权限隔离。`internal/server/token_history_test.go` 通过真实 HTTP/SSH/SFTP 验证静态 Token 转为数据库认证后仍可查询、重试原任务，并保持客户端隔离、授权绑定及 unknown 目标锁。上述测试均运行 SQLite/PostgreSQL。内核测试覆盖请求身份、文件任务隔离、空身份拒绝和错误脱敏；浏览器测试覆盖 HTTP/HTTPS 创建、一次性展示、启停、吊销和刷新。
