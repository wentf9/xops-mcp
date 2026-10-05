# 管理认证契约

[English](../en/development/admin-auth.md) · [开发指南](README.md)

部署步骤位于[用户配置说明](../user/configuration.md)。本页定义实现和客户端集成契约。

## JWT

管理 API 仅接受一个 `Authorization: Bearer <JWT>`，不使用 Cookie。HS256 签名密钥为独立的 32 字节密钥，从 64 字符十六进制私有文件加载。必须拒绝它与主密钥、原始 MCP Token 字节或 Token 的十六进制表示复用；文件两端空白与实际 HTTP Token 加载规则一致。

JWT 有效期固定 15 分钟，要求 `iss`、`sub`、`aud`、`iat`、`nbf`、`exp`、`jti` 和 `av`。issuer 为 `xops-mcp:<domain_id>`，audience 为 `xops-mcp/admin<web_base_path>`。仅允许 HS256，验证固定 issuer/audience、时间和完整字段，不动态读取外部 JWK。

认证不读写会话表。登录读取管理员 bcrypt 哈希；密码更新另外校验管理员版本。退出只清除客户端令牌，旧 JWT 自然到期，重启保持有效令牌。没有刷新令牌或撤销表。SQLite v6 / PostgreSQL v3 移除旧 `admin_sessions`，保留管理员和库存版本。

## JWE 密码请求

获取 `GET /api/v1/auth/challenge?action=login|setup|password`，返回 `publicKey`（RSA JWK）、`challenge`（签名 JWT）和 `expiresAt`。改密挑战需要当前管理 JWT。

挑战有效期 90 秒，audience 在管理 audience 后追加 `/request/<action>`；subject 是当前 JWT 的 SHA-256 十六进制值，登录/初始化使用空字符串的摘要。它绑定部署、前缀、用途和改密 JWT，不维护一次性 nonce 表；窗口内可重放，不宣称 exactly-once。

请求只接受 `{"ciphertext":"..."}`。内部明文为 `{"challenge":"...","data":{...}}`，字段如下：

| 用途 | data 字段 |
| --- | --- |
| login | `username`、`password` |
| setup | `username`、`password`、`token` |
| password | `current`、`next` |

算法固定为 compact JWE、RSA-OAEP-256、A256GCM。AES 密钥 256 位，IV 96 位，tag 128 位；protected header 的 base64url 编码作为 AAD。header 的 `kid` 为 RSA 公钥的 SHA-256 JWK thumbprint。拒绝压缩、错误用途、过期挑战和错误 JWT 绑定。服务端 RSA 私钥为 PKCS8，支持 2048–4096 位，keygen 默认 3072 位。

前端优先使用 Web Crypto；普通 HTTP 无 SubtleCrypto 时使用嵌入的 asmcrypto.js。AES 密钥、IV 与 OAEP seed 全部来自 `crypto.getRandomValues`。依赖资源必须与锁定 npm 包一致，不能从 CDN 动态获取。

## HTTP 与前端状态

`web_tls_enabled` 只控制管理监听器，默认 false。证书仅在启用时加载。`web_public_url` 为外部 origin，即使后端使用 HTTP，也可指向 HTTPS 代理。没有强制 HTTPS、自动升级或 HSTS。Host/Origin、跨站保护和 If-Match 仍生效。HTTP 下 JWE 不认证网页、公钥及 JWT 的传输完整性。

前端使用 `sessionStorage`，fetch 设置 `credentials: omit`。每次登录尝试及令牌变更递增认证代次，状态检查、库存加载、401 处理与异步认证结果只应用于发起时的代次。旧 logout/session 响应或错误不得覆盖新的登录。

认证实例共享密钥、部署 ID 和前缀后可互相验证，但整体服务仍有单实例数据库锁、SSH/MCP 状态、本地 journal 和进程内主机公钥确认记录，不能因此宣称产品已支持水平扩展。

验证入口：`internal/adminauth`、`internal/config`、`internal/server/admin_security_test.go` 和 `web/test/regressions.mjs`；完整命令见[构建与验证](build-testing.md)。
