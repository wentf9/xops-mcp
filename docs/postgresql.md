# PostgreSQL 与离线数据库迁移

M4 提供 PostgreSQL 后端及 `db-export`、`db-import`、`db-verify` 离线命令。SQLite 仍为默认后端。两种后端共用 Web、MCP、业务服务和凭据加密；PostgreSQL 使用独立的 schema 迁移和查询。当前验收环境为 Linux 上的 PostgreSQL 18。服务仍限定单实例，传输 journal 仍在本地 `data_dir/transfers/`。

## 配置和初始化

为服务创建专用数据库及拥有该数据库和 `public` schema 的角色。该角色需要建表、索引、序列、修改 schema 和读写业务表的权限；服务不创建数据库，也不需要超级用户或 CREATEDB。数据库不得与其他应用共用。使用直接连接或保留物理会话的代理；不支持 PgBouncer 的 transaction/statement pooling。

从 [PostgreSQL 示例](../examples/postgres.yaml) 创建部署配置：

```yaml
database_driver: postgres
postgres_dsn_file: postgres.dsn
data_dir: postgres-data
master_key_file: master.key
mcp_token_file: mcp.token
web_enabled: true
web_listen: 127.0.0.1:8081
web_public_url: http://127.0.0.1:8081
listen: 127.0.0.1:8080
public_url: http://127.0.0.1:8080
tool_timeout: 5m
shutdown_timeout: 45s
```

路径相对于配置文件。`postgres.dsn` 必须为权限 0600 的普通文件，包含单个 URL，例如：

```text
postgresql://xops:URL_ENCODED_PASSWORD@db.example.com:5432/xops?sslmode=verify-full&sslrootcert=/etc/xops-mcp/postgres-ca.pem
```

显式提供 host、user、database 和 password；外部认证使用显式空密码 `user:@host`。对 URL 中的密码特殊字符进行百分号编码。TLS 默认为 `verify-full`，仅允许 `sslmode`、`sslrootcert`、`sslcert`、`sslkey`、`sslpassword` 查询参数；TLS 文件使用部署提供的绝对路径。隔离的本机测试可显式用 `sslmode=disable`。不读取个人 `.pgpass` 或服务文件；设置了 `PGSERVICE` 时拒绝启动。连接配置和连接失败错误不输出连接串或密码。

主密钥仍为数据库外的部署文件；MCP Token 与管理员密码各自独立。准备主密钥和 Token 的步骤见[服务指南](server.md)。使用 PostgreSQL 不会消除本地数据目录和 journal 的备份要求。

```sh
bin/xops-mcp migrate --config .local/postgres.yaml
bin/xops-mcp status --config .local/postgres.yaml
bin/xops-mcp serve --config .local/postgres.yaml
```

`migrate` 与 `serve` 在事务中初始化/升级 schema；检查和离线归档命令要求已初始化的数据库和本地数据目录，不自动升级。PostgreSQL 自有 `schema_version`，当前 v2；与 SQLite v5 无数值对应关系。失败迁移整体回滚，修复原因后可重试；较新的 schema 会被拒绝。迁移、检查和服务启动均验证部署主密钥。

本地文件锁保护 journal；数据库级 advisory lock 防止不同本地目录或机器同时使用同一数据库。查询通过持有该锁的固定物理会话串行执行，读快照使用 repeatable read，写入使用 revision 条件和事务。清单、关联、来源、历史凭据和恢复审计使用按行数、参数数量及负载大小分块的批量写入；墓碑和凭据不可变版本检查使用集合查询，避免逐行网络往返。连接池最多一个连接，不自动切换所有权会话。SQL statement timeout 为 5 秒，lock timeout 为 2 秒；上下文取消先发送查询取消请求，使用独立且有界的事务清理上下文完成回滚，保留可用的所有权会话；网络失效仍在一秒宽限后关闭连接。每秒检查连接；连接丢失后停止准入、取消许可、退出监听并释放连接池。导致连接失效的查询取消也需要重启服务。服务重启从已提交数据恢复，不重放结果未知的业务写入或远端文件提交。

## SQLite 与 PostgreSQL 互迁

只改配置或连接串不会搬迁数据。先停止唯一服务实例，在整个导出、导入、校验和 journal 复制期间保持停止。归档是版本化 AES-GCM 加密文件，需要原主密钥，包含部署 ID、revision、策略、节点及关联、未使用标签、删除墓碑、全部历史密文凭据和来源绑定、管理员密码哈希及审计 ID/事件。导出和导入会验证全部历史凭据可解密。会话不迁移，恢复后需重新登录。

1. 对源部署导出并校验数据库；另行完整备份数据目录、主密钥、MCP Token 和配置。

   ```sh
   bin/xops-mcp db-export --config .local/sqlite.yaml --file /secure-backups/database.xops
   bin/xops-mcp db-verify --config .local/sqlite.yaml --file /secure-backups/database.xops
   ```

2. 目标配置使用原主密钥和 MCP Token、独立的私有数据目录，以及一个空数据库。初始化目标，然后预览、导入和校验：

   ```sh
   bin/xops-mcp migrate --config .local/postgres.yaml
   bin/xops-mcp db-import --config .local/postgres.yaml --file /secure-backups/database.xops
   bin/xops-mcp db-import --config .local/postgres.yaml --file /secure-backups/database.xops --apply
   bin/xops-mcp db-verify --config .local/postgres.yaml --file /secure-backups/database.xops
   ```

   未带 `--apply` 时仅校验归档并输出 domain、revision、节点数和逻辑数据 SHA-256，不修改目标。实际导入在单个事务中检查目标为空并写入全部数据，禁止覆盖已使用的目标；导入后自动校验等价性。完整校验覆盖业务数据及历史、密文、管理员和审计，不包含会话或 journal。若提交确认或校验失败，先检查目标状态并执行 `db-verify`，不要盲目重试导入。

3. 将源 `transfers/` 整个目录复制到目标 `data_dir`，保留内容、权限和所有者；不要只复制活动任务。离线核验后只启动目标：

   ```sh
   bin/xops-mcp recover --config .local/postgres.yaml
   bin/xops-mcp serve --config .local/postgres.yaml
   ```

原部署 ID、凭据版本和来源绑定保持不变，因此 journal 的 unknown 状态与目标锁可继续使用原绑定核验。`db-verify` 不验证远端文件或 journal；需要时使用 `recover --id ID --verify`，结果未知的提交不能自动重试。回滚需要先停止目标并评估切换后产生的数据和远端操作，再恢复成套备份，不能同时运行两份部署。

同一套命令支持 PostgreSQL 到 SQLite，以及任一后端的离线备份恢复。归档创建使用 0600、拒绝覆盖已有文件；读取也要求私有普通文件，不接受 stdin/stdout。单份归档上限 256 MiB，整份在内存中处理；大部署使用数据库原生备份。归档不包含主密钥、MCP Token、连接文件或 journal。

## PostgreSQL 原生备份

停止服务后可以使用 `pg_dump --format=custom`，恢复到空数据库用 `pg_restore --no-owner --no-privileges`，以目标角色拥有表和序列。数据库管理员单独处理角色、权限和 TLS 配置；连接凭据使用部署管理的私有客户端配置，不放入命令日志。数据库、完整本地 journal、主密钥、MCP Token 和服务配置必须来自同一次离线备份。恢复后先执行 `migrate` 和离线 `recover`，再启动单个实例。不能仅备份 PostgreSQL 而遗漏 journal 或密钥。

## 开发验证

测试只使用显式配置的可丢弃 PostgreSQL 服务，测试角色需要 CREATEDB；每个 fixture 创建独立随机数据库，并在清理时删除。禁止指向生产数据库。

```sh
export GOWORK=off
export XOPS_TEST_POSTGRES_DSN='postgres://postgres:synthetic-test-password@127.0.0.1:5432/postgres?sslmode=disable'
XOPS_TEST_BACKEND=sqlite go test -race -count=1 -timeout=120s ./...
XOPS_TEST_BACKEND=postgres go test -race -count=1 -timeout=120s ./...
go build ./...
golangci-lint run ./...
```

未设置测试 DSN 时 PostgreSQL 专项测试跳过；显式指定 `XOPS_TEST_BACKEND=postgres` 却缺少 DSN 时失败，设置后连接失败也会失败，CI 的两后端矩阵显式提供 PostgreSQL 18。共同业务验收覆盖服务发布、Web/MCP 可见性、真实本地 SSH/SFTP/ProxyJump、凭据旋转、审计、会话、unknown 重启恢复和 SQLite 到 PostgreSQL 迁移后的原绑定核验；存储契约覆盖事务回滚、乐观并发、四种归档方向及凭据可用性。后端专项验证迁移失败恢复、锁、取消和连接关闭；延迟回归通过真实 PostgreSQL TCP 代理增加 3 ms 响应延迟，验证 400 和 4096 节点的保存、编辑及包含历史凭据/审计的恢复，保持原有五秒操作期限。另覆盖批次后段失败的整体回滚、大凭据负载和清空清单后的永久墓碑。Linux 隔离验收另验证了普通数据库所有者的初始化/导入，以及原生 `pg_dump`/`pg_restore` 后的归档等价性。浏览器自动化继续使用 SQLite；PostgreSQL 的 Web 行为由相同 Go API 集成测试验证。此证据不包含生产主机、PostgreSQL 故障转移或原生 Windows/macOS 运行。
