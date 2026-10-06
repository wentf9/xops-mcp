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
| `POST /nodes/{id}/test` | 测试当前绑定的 SSH 连接 |
| `PUT /policy` | 策略管理 |
| `GET /audit`、`GET /operations` | 审计分页与活跃准入操作 |
| `POST /reconcile` | 重试应用已提交配置 |

标签写入使用 `{"name":"production"}`，创建返回 `id`；修改和删除使用 `/tags/{id}`。节点 DTO 使用 `tagIDs` 数组关联标签主键，不再接受按名称写入的 `tags` 字段。库存的标签记录包含 `id`、`name` 和关联节点 `count`，未使用标签也会返回。

`/audit` 支持 `limit`（1–100，默认 50）、`before`、`nodeID`、`outcome`、`operationID`。管理 API 不启用跨域 CORS，写请求要求 `Origin` 与当前允许的访问来源一致；不根据未经验证的 Forwarded 头建立信任。登录有并发及来源速率限制。
