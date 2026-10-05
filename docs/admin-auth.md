# 管理员 JWT 与密码加密

管理员认证使用 `Authorization: Bearer <JWT>`，默认有效期 15 分钟。签名校验只依赖部署密钥、部署 ID、管理路径前缀和令牌内容，不读写会话表。登录时仍通过数据库中的 bcrypt 哈希验证密码；所有管理 API 与 MCP Token 的权限独立。

## 密钥与升级

Web 管理新增两个必填配置：

```yaml
admin_jwt_key_file: admin.jwt.key
admin_encryption_key_file: admin.encryption.key
```

路径相对于配置文件，必须使用数据目录之外的私有普通文件（权限 0600 或更严格）。JWT 使用独立的 256 位 HS256 密钥，不得与主密钥或 MCP Token 复用；启动时同时检查 MCP Token 原始字节和十六进制表示（去除文件两端空白），相同则拒绝启动；请求加密使用 PKCS8 RSA 私钥，支持 2048–4096 位，生成命令默认 3072 位：

```sh
bin/xops-mcp keygen --out .local/admin.jwt.key
bin/xops-mcp keygen --type rsa --out .local/admin.encryption.key
```

已有部署升级时先停止服务并备份数据库、journal、密钥及配置，再生成两个新密钥、更新配置并执行 `migrate`。SQLite v6 / PostgreSQL v3 移除旧 `admin_sessions` 表，保留管理员密码哈希、部署 ID、库存 revision 和历史绑定。旧 Cookie 不再认证，需要重新登录。没有 Web 管理时可设 `web_enabled: false`。

密钥属于部署配置，不包含在数据库导出中，必须单独备份并控制分发。拥有相同部署 ID、管理前缀及密钥的认证实例可以相互验签、解密请求，不依赖登录实例。轮换 JWT 密钥并重启所有实例会使旧 JWT 失效；轮换 RSA 密钥会使使用旧公钥的未完成请求失效，客户端需重新获取挑战。

## HTTP / HTTPS 配置

`web_tls_enabled` 控制服务自身的管理监听器，默认 `false`。关闭时使用 HTTP，不限制为本机访问；关闭状态不读取证书或 TLS 私钥，允许保留这些路径供之后开启。服务不强制 HTTPS、不自动重定向到 HTTPS，也不发送 HSTS 来固定浏览器协议。

直接 HTTP：

```yaml
web_listen: 0.0.0.0:8081
web_public_url: http://192.0.2.10:8081
web_tls_enabled: false
```

开启原生 HTTPS：

```yaml
web_listen: 0.0.0.0:8081
web_public_url: https://admin.example.com:8081
web_tls_enabled: true
web_tls_cert_file: /etc/xops-mcp/admin-tls.crt
web_tls_key_file: /etc/xops-mcp/admin-tls.key
```

开启时必须提供匹配的证书和私钥，私钥要求私有普通文件；证书需匹配公开地址并由客户端信任，支持 TLS 1.2 及以上。关闭原生 TLS 并直接使用 HTTP 时，将 `web_public_url` 一并改为 HTTP 地址。未显式配置公开地址时，根据开关自动推导 `http://` 或 `https://`；通配监听仍需要显式公开地址。

也可以在外部代理终止 TLS，后端关闭原生 TLS：

```yaml
web_listen: 127.0.0.1:8081
web_public_url: https://admin.example.com
web_tls_enabled: false
```

`web_public_url` 描述浏览器访问的外部 origin，用于 Host/Origin 检查，不决定后端是否启用 TLS。代理保留 Host 和路径前缀即可，应用不依赖 `X-Forwarded-Proto`。删除先前配置中的 `web_trusted_proxies` 和 `web_allow_insecure_loopback`，改用 `web_tls_enabled` 控制原生 TLS。见 [Nginx 示例](../examples/deployment/nginx.conf)。配置修改后重启服务生效。

HTTP 和 HTTPS 都保留 JWE 密码加密。浏览器有 Web Crypto 时使用原生实现；普通 HTTP 页面没有 SubtleCrypto 时使用随程序嵌入的 asmcrypto.js，密钥、IV 和 OAEP 随机种子仍全部来自浏览器的安全随机数接口，不使用 CDN。HTTP 不提供网页脚本、公钥或 JWT 传输的认证和完整性保护，JWE 的保护范围为密码请求内容。

## 请求格式

无论 HTTP 或 HTTPS，初始化、登录和改密均采用标准 compact JWE（RFC 7516），算法固定为 `RSA-OAEP-256` 和 `A256GCM`。HTTPS 认证服务端及其公钥，并保护 JWT；JWE 保护经过代理或应用请求记录的密码内容。

1. 获取 `GET <prefix>/api/v1/auth/challenge?action=login|setup|password`。改密请求需附带当前 Bearer JWT。响应包含 `publicKey`（RSA JWK）、`challenge`（签名 JWT）与 `expiresAt`。
2. 生成新的 256 位 AES 密钥和 96 位随机 IV，用 AES-GCM 加密 UTF-8 JSON：`{"challenge":"...","data":{...}}`，再用 RSA-OAEP-SHA256 包装 AES 密钥。受保护头固定为 `alg: RSA-OAEP-256`、`enc: A256GCM` 和返回公钥的 `kid`；头的 base64url 编码为 GCM additional authenticated data，tag 为 128 位。
3. 将 JWE 五段字符串作为唯一字段提交：`{"ciphertext":"protected.encryptedKey.iv.ciphertext.tag"}`。字段内容分别为登录的 `username/password`，初始化的 `username/password/token`，或改密的 `current/next`。直接提交这些明文字段会被拒绝。

挑战有效期 90 秒，绑定部署、管理前缀、用途，以及改密时的当前 JWT。篡改、压缩、错误密钥、错误用途或已过期的密文会被拒绝。不保存一次性 nonce 状态，因此有效窗口内相同请求可能重放；短窗口和登录限流约束重放范围；配置 HTTPS 时还提供传输保护，不宣称一次性请求语义。

成功登录返回 `accessToken`、`tokenType: Bearer`、`username` 和 `expiresAt`。前端只在当前标签页的 `sessionStorage` 保存 JWT，刷新页面可继续使用；所有管理请求显式携带 Authorization，使用 `credentials: omit`，不发送认证 Cookie 或 `X-CSRF-Token`。Host、Origin、跨站请求限制和配置编辑的 If-Match 校验继续生效。

## 失效与扩展边界

退出立即清除当前标签页令牌和页面数据。认证请求绑定客户端认证代次，旧状态检查及其错误响应不会清除新令牌、替换正在登录的表单或覆盖新登录页面。密码变更或离线重置后旧密码不能再次登录，已经签发的 JWT 保持有效至到期，不即时逐个吊销。重启也不会主动注销有效 JWT。没有刷新令牌、撤销表或会话粘滞；到期需要重新登录。发生密钥或令牌泄露时，可以轮换部署 JWT 密钥并重启，使所有旧令牌失效。

JWT 对 JavaScript 可见，页面继续通过 CSP 和输出转义降低 XSS 风险。初始化和改密的明文只在浏览器输入及服务端解密后用于 bcrypt 校验/哈希，不写入日志或数据库；密码仍按 12–72 个 UTF-8 字节校验。

本次实现使认证不依赖服务端会话。产品整体仍为单实例：数据库所有权锁、SSH/MCP 运行状态、主机公钥确认记录、进程内限流和本地传输 journal 尚未完成水平扩展改造。

验收覆盖跨认证实例验签/解密、错误算法/密钥/issuer/audience/过期拒绝、密码请求用途与 JWT 绑定、旧会话表迁移、原生 TLS 的开启/关闭、HTTP 直连和代理部署、数据库两后端业务集成，以及 HTTPS / 普通 HTTP 浏览器实际加密请求、刷新和退出。
