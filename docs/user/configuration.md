# 配置与访问地址

[English](../en/user/configuration.md) · [使用指南](README.md)

所有运行命令用 `--config` 指定配置文件。修改配置后重启服务生效。配置中的相对路径以配置文件所在目录为准；命令行中的输入、输出路径则相对于当前终端目录。

## 存储和密钥

| 配置 | 作用 |
| --- | --- |
| `data_dir` | 必填，保存运行数据和文件传输记录，目录权限 `0700` |
| `database_driver` | `sqlite`（默认）或 `postgres` |
| `postgres_dsn_file` | 使用 PostgreSQL 时必填，数据库连接文件 |
| `master_key_file` | 必填，数据主密钥；丢失后无法恢复保存的远程凭据 |
| `mcp_token_file` | 可选，预置初始 MCP Token 的私有文件；正常使用可直接在控制台创建 |
| `admin_jwt_key_file` | 启用管理页面时必填，管理登录签名密钥 |
| `admin_encryption_key_file` | 启用管理页面时必填，密码加密密钥，用 `keygen --type rsa` 生成 |
| `admin_bootstrap_token_file` | 可选，首次创建管理员所需的初始化码 |

密钥、连接文件和包含密码的输入文件使用 `0600` 权限，并由运行服务的账户读取。不要复用主密钥、MCP 访问凭据和管理登录密钥，主密钥、管理登录签名密钥和密码加密密钥必须位于 `data_dir` 外。没有初始化码时，可以通过[离线命令](maintenance.md)创建管理员。

## 两个访问入口

| 配置 | 默认值或说明 |
| --- | --- |
| `listen` | MCP 监听地址，默认 `127.0.0.1:8080` |
| `public_url` | MCP 客户端实际使用的协议、主机名和端口，不含 `/mcp` 或其他路径 |
| `allowed_hosts` | 可选，MCP 额外允许的主机名和端口 |
| `allowed_origins` | 可选，MCP 额外允许的浏览器来源地址 |
| `web_enabled` | 默认 `true`；关闭后不提供管理页面 |
| `web_listen` | 管理监听地址，默认 `127.0.0.1:8081` |
| `web_public_url` | 浏览器实际访问的协议、主机名和端口，不含路径 |
| `web_allowed_hosts` | 可选，管理页面额外允许的主机名和端口 |
| `web_base_path` | 默认空；可设 `/console` 等路径前缀 |
| `tool_timeout` | 命令执行时限，默认 `5m` |
| `shutdown_timeout` | 停止服务的等待时间，默认 `45s` |

监听 `0.0.0.0` 或 `::` 时，必须填写对应的公开地址。MCP 和管理页面的监听地址与端口不能重叠。公开地址中的 `localhost` 或 `127.0.0.1` 指访问者自己的设备；远程访问时应填写服务器地址。

例如局域网 HTTP：

```yaml
listen: 0.0.0.0:8080
public_url: http://192.0.2.20:8080
web_listen: 0.0.0.0:8081
web_public_url: http://192.0.2.20:8081
web_base_path: /console
web_tls_enabled: false
```

将示例 IP 改为实际地址。此时控制台为 `http://192.0.2.20:8081/console/`，MCP 为 `http://192.0.2.20:8080/mcp`。容器部署还需发布相应宿主机端口。

## HTTPS 开关

`web_tls_enabled` 默认 `false`，管理页面使用 HTTP，不需要证书。启用服务自身的 HTTPS：

```yaml
web_tls_enabled: true
web_tls_cert_file: /etc/xops-mcp/admin-tls.crt
web_tls_key_file: /etc/xops-mcp/admin-tls.key
web_public_url: https://admin.example.com:8081
```

证书必须匹配实际访问地址并被浏览器信任，私钥文件须为 `0600`。容器内路径必须对应已挂载文件；使用 `.secrets` 挂载时可设置为 `/run/secrets/xops/admin-tls.crt` 和 `/run/secrets/xops/admin-tls.key`。

切回直接 HTTP 时，将 `web_tls_enabled` 设为 `false`，并将 `web_public_url` 改为实际 HTTP 地址。关闭时不会读取证书，可以保留证书路径。HTTP 和 HTTPS 均对登录密码进行额外加密；HTTP 不加密整个连接中的其他内容。

该开关只控制管理端口。MCP 端口由程序提供 HTTP；需要 MCP HTTPS 时使用反向代理。

## 反向代理

[Nginx 示例](../../examples/deployment/nginx.conf)分别代理 MCP 和管理端口。替换域名、证书路径，并让 `public_url`、`web_public_url` 与外部地址一致。

管理页面由代理提供 HTTPS 时，后端可保持 HTTP：

```yaml
web_listen: 127.0.0.1:8081
web_public_url: https://admin.example.com
web_base_path: /console
web_tls_enabled: false
```

保留 Host 和路径前缀；例如管理页面使用 `proxy_pass http://127.0.0.1:8081`，不要在末尾添加 `/` 来删除前缀。MCP 的 `/mcp` 和 `/v1/transfers/` 均须保留，文件传输不能只代理 `/mcp`。应用不需要 `web_trusted_proxies` 或 `web_allow_insecure_loopback` 配置项。
