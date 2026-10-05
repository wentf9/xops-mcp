# 备份与故障处理

[English](../en/user/maintenance.md) · [使用指南](README.md)

下文的 `xops-mcp` 命令用于直接运行的程序。容器部署使用同一镜像执行命令，配置路径为 `/etc/xops-mcp/server.yaml`；示例见[容器指南](container.md)。除 `keygen` 外，离线管理命令都应在服务停止后执行。

## 创建或重置管理员

如果未配置 Web 初始化码，将密码单独保存到服务账户可读取的 `0600` 普通文件，再执行：

```sh
xops-mcp admin-init --config server.yaml --username admin --password-file /secure/admin-password.txt
```

忘记密码时执行：

```sh
xops-mcp admin-reset --config server.yaml --password-file /secure/new-admin-password.txt
```

也可用 `--password-stdin` 从标准输入读取。容器示例中，密码文件位于宿主机，不需要放入容器：

```sh
docker compose -f examples/deployment/compose.yaml stop
docker compose -f examples/deployment/compose.yaml run --rm --no-deps -T xops-mcp admin-reset --config /etc/xops-mcp/server.yaml --password-stdin < /secure/new-admin-password.txt
docker compose -f examples/deployment/compose.yaml up -d --no-build
```

不要把密码直接写在命令参数里。已有登录会在原有效期结束后失效；如需立即使所有管理登录失效，可停止服务，生成一份新的管理登录签名密钥，更新 `admin_jwt_key_file` 后重启。不要为此更换数据主密钥。

## 备份与恢复

SQLite 部署停止后，备份整个 `data_dir`（包括传输记录），并单独备份配置、主密钥、MCP 访问凭据、管理密钥和使用中的证书。不要只复制 `xops.db`。

直接运行示例的所有文件都在 `xops-instance` 下时，可在其父目录执行：

```sh
umask 077
tar -czf "xops-instance-$(date +%Y%m%d-%H%M%S).tar.gz" xops-instance
```

恢复到新目录，保持原拥有者和权限，使用原密钥执行 `migrate` 后再启动。不能同时运行原部署和恢复副本。[PostgreSQL](postgresql.md)还需要单独备份数据库。

容器备份需要同时保存命名卷和 `.secrets`。下例使用已停止的服务容器找到正确的数据卷；不要先删除容器：

```sh
docker compose -f examples/deployment/compose.yaml stop
mkdir -m 0700 backup
docker run --rm --network none --volumes-from "$(docker compose -f examples/deployment/compose.yaml ps -aq xops-mcp):ro" --mount "type=bind,src=$PWD/backup,dst=/backup" busybox:1.37 sh -c 'umask 077; tar -czf /backup/data.tgz -C /var/lib/xops-mcp . && tar -czf /backup/secrets.tgz -C /run/secrets/xops . && cp /etc/xops-mcp/server.yaml /backup/server.yaml'
```

每次备份使用新的空目录，或先妥善移走旧备份。输出由容器管理员账户拥有，可能需要 `sudo` 才能读取。恢复时将 `secrets.tgz` 放回 `.secrets`、恢复配置，将 `data.tgz` 解压到服务数据卷的 `/var/lib/xops-mcp`，保留拥有者和权限，再执行离线 `migrate`。只恢复到空的目标目录/卷；不要覆盖正在使用的数据。

## 文件任务结果未知

在停止的部署中查看任务：

```sh
xops-mcp recover --config server.yaml
```

将下面的 `TRANSFER_ID` 换成待处理任务编号：

```sh
xops-mcp recover --config server.yaml --id TRANSFER_ID --verify
```

核验结果不确定时，先在目标主机检查文件，不要直接重复上传。只需清理该任务的临时文件时用 `--cleanup`。完成人工处理后，才可记录确认并允许该目标后续写入：

```sh
xops-mcp recover --config server.yaml --id TRANSFER_ID --resolve-unknown --reason '已人工核对目标文件'
```

这个命令不会重传文件，也不会把未知结果改成成功。节点地址、凭据或公钥已经变化时，自动核验可能被拒绝，应人工检查原来的目标。

## 常见问题

| 提示或现象 | 处理 |
| --- | --- |
| `no such file or directory` | 先创建输出目录，确认当前目录和容器内路径；命令不会自动创建密钥的父目录 |
| `permission denied` | 检查运行账户、目录拥有者和权限；镜像使用 UID/GID 65532，私有目录为 0700、密钥文件为 0600 |
| `file exists` | 密钥生成和数据库导出不覆盖文件；保留已有密钥，或为新导出选择新文件名 |
| `origin_denied` 或管理页面无法保存 | 浏览器使用的协议、主机名和端口须与 `web_public_url` 一致；直接 HTTP 访问不能仍配置为 HTTPS 地址 |
| HTTPS 握手失败 | 检查开关、证书、私钥、证书域名和浏览器信任；关闭 HTTPS 时改用 HTTP 地址 |
| `deployment is already in use` | 先停止运行中的服务或其他离线命令；不要尝试启动第二个实例 |
| 主密钥不匹配 | 恢复该部署原来的主密钥，不要重新生成替代 |
| 节点不能启用或连接失败 | 补齐身份和凭据、核对主机公钥及跳板，并检查网络连通性 |

容器日志使用 `docker compose -f examples/deployment/compose.yaml logs --tail=100 xops-mcp` 查看。不要把密钥、访问凭据或密码粘贴到公开问题报告。
