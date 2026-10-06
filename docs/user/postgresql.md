# PostgreSQL 与数据库迁移

[English](../en/user/postgresql.md) · [使用指南](README.md)

SQLite 无需单独的数据库服务。选择 PostgreSQL 时，先准备专用数据库和拥有该数据库及其中表、序列的账户。不要与其他应用共用数据库。服务不创建数据库，也不需要超级用户权限。PostgreSQL 仍只允许一个 XOps MCP 实例运行。

## 配置 PostgreSQL

保留原有服务配置、密钥和本地数据目录，设置：

```yaml
database_driver: postgres
postgres_dsn_file: postgres.dsn
```

将连接地址保存为权限 `0600` 的 `postgres.dsn`：

```text
postgresql://xops:URL_ENCODED_PASSWORD@db.example.com:5432/xops?sslmode=verify-full&sslrootcert=/etc/xops-mcp/postgres-ca.pem
```

替换主机、用户名、数据库和密码。密码中的特殊字符需要 URL 编码。必须写出密码部分，使用外部认证时可写显式空密码 `user:@host`。证书路径为运行服务所能读取的绝对路径。

连接默认验证数据库证书和主机名。只有明确不需要数据库 TLS 的连接才使用 `sslmode=disable`。可使用 `sslmode`、`sslrootcert`、`sslcert`、`sslkey`、`sslpassword` 参数。不要设置 `PGSERVICE`；应用不会读取个人的 `.pgpass` 或数据库服务配置。

使用直接数据库连接或保留完整连接的代理，不使用 PgBouncer 的事务/语句池模式。数据库在容器外时，连接地址应能从容器访问；容器中的 `127.0.0.1` 指容器自身。

容器部署可以将文件放在 `.secrets/postgres.dsn`，拥有者为 `65532:65532`、权限为 `0600`，配置中填写 `/run/secrets/xops/postgres.dsn`。

准备好空数据库后初始化并启动：

```sh
xops-mcp migrate --config server.yaml
xops-mcp serve --config server.yaml
```

容器用户按[容器指南](container.md)使用对应的镜像命令。改连接地址不会自动迁移 SQLite 数据。

## 在两个数据库之间迁移

整个迁移期间停止源服务和目标服务，并保留源部署的完整备份。准备两个配置文件：`source.yaml` 指向源数据，`target.yaml` 指向空的目标数据库和独立数据目录。目标配置使用源部署的同一份主密钥和管理密钥。MCP Token 记录随数据库归档迁移，客户端继续使用原 Token。

以下命令在这些文件所在目录执行：

```sh
xops-mcp db-export --config source.yaml --file database.xops
xops-mcp db-verify --config source.yaml --file database.xops
xops-mcp migrate --config target.yaml
xops-mcp db-import --config target.yaml --file database.xops
```

最后一条命令只检查文件并显示待恢复数据。确认目标为空后应用并核对：

```sh
xops-mcp db-import --config target.yaml --file database.xops --apply
xops-mcp db-verify --config target.yaml --file database.xops
```

输出应包含 `verified: true`。已有数据的目标会被拒绝，不能覆盖后重试。若应用命令失败，先运行 `db-verify` 确认目标状态。

再将源 `data_dir/transfers/` 完整复制到目标数据目录，保留权限和拥有者，最后只启动目标服务。备份文件不包含传输记录、配置、主密钥或管理密钥，不能只移动数据库文件。原服务必须保持停止。

导出文件为加密文件，读取需要原主密钥。文件权限为 `0600`，不覆盖已有输出；单个文件上限为 256 MiB。也支持 PostgreSQL 到 SQLite，以及同一种数据库的恢复。

容器迁移时，配置、连接文件和归档的命令行路径必须在容器内可见。可将只含密钥路径的两个配置文件和私有归档放入 `.secrets` 挂载中，用 `/run/secrets/xops/source.yaml`、`/run/secrets/xops/target.yaml` 和 `/run/secrets/xops/database.xops`。导出需要额外的可写目录挂载；不要向默认只读的 `.secrets` 挂载写入文件。可先导出到 `/var/lib/xops-mcp/database.xops`，再从停止的容器复制出来。

## 备份 PostgreSQL

停止 XOps MCP 后，用 `pg_dump --format=custom` 备份数据库，同时备份本地 `data_dir`、配置和全部密钥。恢复时以目标数据库拥有者使用 `pg_restore --no-owner --no-privileges`，恢复到空库后执行 `migrate`，再启动一个服务实例。

数据库备份和本地文件必须来自同一次停止期间。缺少主密钥无法恢复远程凭据；缺少传输记录会丢失未完成任务的信息。更大数据库可使用数据库自身的备份工具。
