# 管理 API

[English](../en/development/api.md) · [开发指南](README.md)

认证与密码请求格式见[认证契约](admin-auth.md)。

## 路由

管理 API 位于管理端口的 `<web_base_path>/api/v1/`，下表路径均相对此目录。写请求必须为同源请求，使用 JSON、`Authorization: Bearer <JWT>`，并在配置操作中携带 `If-Match`。`GET /inventory` 返回强 ETag，例如 `"7"`；缺少条件返回 428，版本冲突返回 412。凭据输入仅用于写入，返回数据不含密码、私钥、口令、密文或密码哈希。

| 接口 | 用途 |
| --- | --- |
| `GET` / `POST /mcp-tokens`、`PUT` / `DELETE /mcp-tokens/{id}` | [MCP Token 管理与记录版本](mcp-auth.md) |
| `GET /auth/challenge?action=...` | 返回 RSA JWK 与 90 秒签名挑战；改密需 Bearer JWT |
| `GET /auth/session` | 登录/初始化状态与 JWT 有效期 |
| `POST /auth/setup`、`POST /auth/login` | JWE `ciphertext`；解密后为 `username/password`，初始化另含 `token` |
| `POST /auth/logout`、`PUT /auth/password` | 客户端退出确认，或通过 JWE 加密的 `current/next` 修改密码 |
| `GET /inventory` | 主机、身份、节点、凭据元数据、标签、策略和发布状态 |
| `POST /hosts`、`/identities`、`/nodes`、`/credentials`、`/tags` | 创建记录 |
| `PUT` / `DELETE /{资源}/{id}` | 修改或删除记录 |
| `POST /hosts/{id}/probe` | 直连获取未受信的主机公钥；可指定 `algorithm` |
| `POST /hosts/{id}/key-preview` | 预览独立获取的 `hostKey` 公钥及指纹，不连接主机；返回用于确认的 `probeID` |
| `POST` / `DELETE /hosts/{id}/trust` | 用直连获取或输入预览返回的 `probeID` 确认，或撤销信任 |
| `POST /credentials/validate-key` | 验证私钥内容与口令有效性 |
| `POST /nodes/{id}/test` | 测试当前绑定的 SSH 连接 |
| `POST /nodes/{id}/test-privilege` | 测试已保存节点的提权能力与提权配置 |
| `POST /nodes/privilege-probe` | 探测草稿或新建节点的提权能力与提权配置 |
| `PUT /policy` | 策略管理 |
| `GET /audit`、`GET /operations` | 审计分页与活跃准入操作 |
| `POST /reconcile` | 重试应用已提交配置 |

标签写入使用 `{"name":"production"}`，创建返回 `id`；修改和删除使用 `/tags/{id}`。节点 DTO 使用 `tagIDs` 数组关联标签主键，不再接受按名称写入的 `tags` 字段。库存的标签记录包含 `id`、`name` 和关联节点 `count`，未使用标签也会返回。

`/audit` 支持 `limit`（1–100，默认 50）、`before`、`nodeID`、`outcome`、`operationID`。管理 API 不启用跨域 CORS，写请求要求 `Origin` 与当前允许的访问来源一致；不根据未经验证的 Forwarded 头建立信任。登录有并发及来源速率限制。

## 节点执行配置

节点创建、修改 DTO 与库存响应支持可选的 `execution: {"interpreter":"zsh","launchDialect":"posix","login":false}`。省略或 null 启用运行时探测；login 区分省略、true、false。更新使用库存 ETag 并替换节点配置，执行配置解码或校验失败返回 400。执行配置参与操作绑定，并为已准入操作保留副本。数据库迁移为 SQLite v9 / PostgreSQL v6；旧节点默认 null。

`POST /nodes/execution-probe` 接收 `{ "nodeID": "可选的已有节点ID", "hostID": "...", "identityID": "...", "jumpIDs": [] }`，携带库存 `If-Match`。返回 core `ProbeResult`（`availableInterpreters`、`suggestedConfig`、`detectedDialect` 等），不改变库存或 revision。使用当前表单的连接引用、固定信任和版本化凭据；显式管理员检查允许新建/停用目标，拒绝停用跳板。仅运行 core 固定诊断，限时 25 秒，共用四个探测并发额度，记录意图与结果审计。网络操作前后检查 revision，过期结果返回 412；缺少信任/凭据或无效链返回 422，连接/探测失败返回 502，超时返回 408。用户通过正常节点创建/更新接口保存选中的配置。

`POST /nodes/{id}/test-privilege` 使用已保存的节点绑定；`POST /nodes/privilege-probe` 接收 `{ "nodeID": "可选的已有节点ID", "hostID": "...", "identityID": "...", "jumpIDs": [], "sudoMode": "none", "privilegeCredentialID": "", "execution": {"interpreter":"sh","launchDialect":"posix"} }`，检查草稿而不保存。两者携带库存 `If-Match`，返回 `escalated`、`sudoMode`、`user`、`detail` 和 `elapsedMS`。所有成功结果均须验证实际 UID 为 0 且返回用户名；执行环境省略、null 或未指定解释器与启动 Shell 时，通过 core 的有界探测选择临时执行配置，不写回节点；显式配置保持不变。`none` 模式按执行环境检查登录身份，再执行免密 sudo 身份检查，不以 `sudo true` 成功替代身份验证。手动修改表单或关闭后丢弃旧提权结果。Shell 探测不改变执行配置时保留已有结果；应用不同的执行配置时，若当前表单已请求过提权测试，则取消旧请求并自动用新配置重新验证。未请求过提权测试，或已手动更改连接/提权设置后，不自动发起验证。

## 私钥验证

`POST /credentials/validate-key` 接收 `{ "privateKey": "...", "passphrase": "..." }`，对提供的私钥内容及口令进行解密与有效性验证，不修改库存及版本号。返回 `{ "valid": true/false, "protected": true/false, "keyType": "...", "fingerprint": "...", "message": "..." }`。对于受保护私钥（如带有口令的 OpenSSH / PEM 密钥），若未提供口令或口令不正确，返回对应提示与保护状态；未受保护私钥若填写口令亦会给出警告。凭据保存（`POST/PUT /credentials`）接口在入库前亦会执行此验证，口令缺失或错误返回 422。

浏览器上传、验证和保存统一去除私钥内容两端的空白，私钥口令保持原文。私钥验证结果绑定发起请求时规范化后的私钥、口令、凭据类型与认证状态；有效输入变化、重新上传、提交或关闭弹窗时取消验证并丢弃旧结果。仅失焦且内容未变时保留验证结果。
