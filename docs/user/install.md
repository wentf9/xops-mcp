# 直接运行

[English](../en/user/install.md) · [使用指南](README.md)

本页适用于已经持有 `xops-mcp` 可执行程序的 Linux 用户，并假设该程序可直接从命令行运行。尚未准备程序时，可按[容器部署](container.md)安装。

## 准备目录

在一个新的空目录中安装：

```sh
mkdir -m 0700 xops-instance
cd xops-instance
umask 077
```

## 创建密钥和配置

```sh
xops-mcp keygen --out master.key
xops-mcp keygen --out admin.jwt.key
xops-mcp keygen --type rsa --out admin.encryption.key
xops-mcp keygen --out admin.setup
```

所有文件都不能与已有文件重名。各密钥须独立保存，不能复用同一份内容。

将以下内容保存为 `server.yaml`：

```yaml
database_driver: sqlite
data_dir: data
master_key_file: master.key
admin_jwt_key_file: admin.jwt.key
admin_encryption_key_file: admin.encryption.key
admin_bootstrap_token_file: admin.setup
listen: 127.0.0.1:8080
public_url: http://127.0.0.1:8080
web_enabled: true
web_listen: 127.0.0.1:8081
web_public_url: http://127.0.0.1:8081
web_base_path: ""
web_tls_enabled: false
tool_timeout: 5m
shutdown_timeout: 45s
```

配置中的相对路径以配置文件所在目录为准。数据目录会自动创建为 `0700`；密钥和配置不能是符号链接。主密钥、管理登录密钥和密码加密密钥必须放在 `data_dir` 外。

## 启动和初始化管理员

```sh
xops-mcp migrate --config server.yaml
xops-mcp serve --config server.yaml
```

保持该终端运行。打开 `http://127.0.0.1:8081/`，使用 `admin.setup` 文件中的初始化码创建管理员。完成后可删除初始化码文件，保留其他密钥。

新服务没有节点。按[控制台指南](console.md)添加并启用节点，并在 [MCP Token](tokens.md) 页面创建访问凭据，再连接 MCP 客户端。MCP 地址为 `http://127.0.0.1:8080/mcp`。

按 `Ctrl+C` 停止服务。停止后可以检查数据：

```sh
xops-mcp status --config server.yaml
```

需要局域网访问、HTTPS 或 PostgreSQL 时，分别阅读[配置说明](configuration.md)和[数据库指南](postgresql.md)。

## 作为系统服务运行

可使用随软件提供的 [systemd 配置](../../examples/deployment/systemd.yaml)和[服务文件](../../examples/deployment/xops-mcp.service)。安装程序到 `/usr/local/bin/xops-mcp`，创建名为 `xops-mcp` 的系统账户，将配置和密钥放在 `/etc/xops-mcp/`，并让该账户拥有密钥文件且权限为 `0600`。

示例通过 Nginx 提供两个域名，必须将 `mcp.example.com`、`admin.example.com` 改为实际地址并准备证书。如果直接使用 HTTP，请将两个公开地址改为实际 HTTP 地址。systemd 会创建 `/var/lib/xops-mcp` 作为数据目录。

将服务文件安装到 `/etc/systemd/system/xops-mcp.service` 后执行：

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now xops-mcp
sudo systemctl status xops-mcp
```

服务日志可用 `sudo journalctl -u xops-mcp -n 100` 查看。运行离线命令前先停止服务，并使用 `sudo -u xops-mcp xops-mcp ...`，以免产生服务账户无法读取的文件。
