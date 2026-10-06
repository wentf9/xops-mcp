# 容器部署

[English](../en/user/container.md) · [使用指南](README.md)

需要 Linux、以系统服务运行的 Docker Engine、Docker Compose，以及能够使用 `sudo` 的账户。以下命令在 XOps MCP 目录执行，该目录应包含 `Dockerfile` 和 `examples/deployment/`。

## 1. 准备运行镜像

```sh
docker compose -f examples/deployment/compose.yaml build --pull
```

后续所有 XOps 命令均使用这份镜像执行。

## 2. 首次创建密钥

镜像中的程序以 UID/GID `65532:65532` 运行。先准备它可访问的目录：

```sh
sudo install -d -m 0700 -o 65532 -g 65532 examples/deployment/.secrets
```

分别生成数据主密钥、管理登录签名密钥、密码加密密钥和管理员初始化码：

```sh
docker run --rm --network none --mount "type=bind,src=$PWD/examples/deployment/.secrets,dst=/secrets" xops-mcp:local keygen --out /secrets/master.key
docker run --rm --network none --mount "type=bind,src=$PWD/examples/deployment/.secrets,dst=/secrets" xops-mcp:local keygen --out /secrets/admin.jwt.key
docker run --rm --network none --mount "type=bind,src=$PWD/examples/deployment/.secrets,dst=/secrets" xops-mcp:local keygen --type rsa --out /secrets/admin.encryption.key
docker run --rm --network none --mount "type=bind,src=$PWD/examples/deployment/.secrets,dst=/secrets" xops-mcp:local keygen --out /secrets/admin.setup
```

生成文件的权限为 `0600`，不会覆盖已有文件。已有密钥报 `file exists` 时应保留原文件；不要为了重跑命令而删除主密钥。

## 3. 设置访问地址

编辑 `examples/deployment/container.yaml`。如果直接在宿主机通过 HTTP 访问，可使用：

```yaml
data_dir: /var/lib/xops-mcp/data
master_key_file: /run/secrets/xops/master.key
admin_jwt_key_file: /run/secrets/xops/admin.jwt.key
admin_encryption_key_file: /run/secrets/xops/admin.encryption.key
admin_bootstrap_token_file: /run/secrets/xops/admin.setup
web_enabled: true
web_listen: 0.0.0.0:8081
web_public_url: http://127.0.0.1:8081
web_base_path: /console
web_tls_enabled: false
listen: 0.0.0.0:8080
public_url: http://127.0.0.1:8080
tool_timeout: 5m
shutdown_timeout: 45s
```

```sh
chmod 0644 examples/deployment/container.yaml
docker compose -f examples/deployment/compose.yaml config --quiet
```

文件中只填写密钥路径，不填写密码。容器中的 `/run/secrets/xops` 对应宿主机的 `.secrets` 目录。

关闭原生 HTTPS 时，直接 HTTP 访问还需要将 `web_public_url` 改成实际 HTTP 地址。若外部代理提供 HTTPS，则保留代理的 HTTPS 地址，参见[地址与 HTTPS](configuration.md)。

## 4. 初始化并启动

```sh
docker compose -f examples/deployment/compose.yaml run --rm --no-deps xops-mcp migrate --config /etc/xops-mcp/server.yaml
docker compose -f examples/deployment/compose.yaml up -d --no-build
docker compose -f examples/deployment/compose.yaml logs --tail=50 xops-mcp
```

打开 `http://127.0.0.1:8081/console/`。查看初始化码：

```sh
sudo cat examples/deployment/.secrets/admin.setup
```

在页面创建管理员账户，再打开 [MCP Token](tokens.md) 页面创建客户端访问凭据。MCP 地址为 `http://127.0.0.1:8080/mcp`，详见[客户端连接](console.md)。

示例将端口发布到宿主机回环地址。供局域网使用时，同时修改 Compose 的 `ports` 和配置中的两个公开地址，不能只修改容器内的 `web_listen`。例如把端口映射改成 `8080:8080`、`8081:8081`，并将公开地址改成服务器的实际地址。

## 停止和日常维护

```sh
docker compose -f examples/deployment/compose.yaml stop
```

离线命令必须在停止服务后执行：

```sh
docker compose -f examples/deployment/compose.yaml run --rm --no-deps xops-mcp status --config /etc/xops-mcp/server.yaml
docker compose -f examples/deployment/compose.yaml up -d --no-build
```

数据保存在 Compose 的命名卷中，密钥保存在 `.secrets`。备份时两者都要保留；不要用 `docker compose down -v` 作为普通停止命令。使用原来的 Compose 项目名和文件路径，避免意外创建另一份空数据卷。
