# 服务部署与配置导入

[English](en/server.md)

服务面向 Linux 单实例部署，提供 Streamable HTTP MCP 和文件传输。需要 Go 1.26+ 构建。可通过 [Web 控制台](web-console.md)在线管理配置，也保留离线命令；SQLite 默认配置见下文；PostgreSQL 配置和显式离线迁移见 [PostgreSQL 指南](postgresql.md)。

## 启动

```sh
GOWORK=off go build -o bin/xops-mcp ./cmd/xops-mcp
mkdir -m 700 -p .local
cp examples/server.yaml .local/server.yaml
bin/xops-mcp keygen --out .local/master.key
bin/xops-mcp keygen --out .local/mcp.token
bin/xops-mcp keygen --out .local/admin.jwt.key
bin/xops-mcp keygen --type rsa --out .local/admin.encryption.key
bin/xops-mcp keygen --out .local/admin.setup
bin/xops-mcp migrate --config .local/server.yaml
bin/xops-mcp serve --config .local/server.yaml
```

`keygen` 创建 0600 的随机密钥文件，不覆盖已有文件。主密钥是 64 位十六进制文本；MCP Token 是另一份独立的凭据。配置中的相对路径以配置文件所在目录为基准。数据目录必须为私有目录（0700），主密钥须位于数据目录外；部署文件和数据库文件不接受符号链接。

MCP 默认监听 `127.0.0.1:8080`，管理页面独立监听 `127.0.0.1:8081`（见 [Web 配置](web-console.md)），MCP 地址为 `http://127.0.0.1:8080/mcp`。客户端通过 `Authorization: Bearer <mcp.token 内容>` 鉴权。Token、密码和私钥不会由状态或导入命令输出。空数据库没有可执行节点。

LAN 部署需设置 `listen` 和明确的 `public_url`，例如 `0.0.0.0:8080` 与 `http://192.0.2.20:8080`。`public_url` 是客户端实际访问的 HTTP(S) origin，不含 `/mcp`、路径前缀、查询参数或凭据。反向代理应保留 `/mcp` 和 `/v1/transfers/` 路径，转发配置允许的 Host，并通过 HTTPS 提供外部访问；进程自身监听 HTTP。需要额外域名或浏览器 origin 时显式配置 `allowed_hosts`、`allowed_origins`。

`tool_timeout` 默认为 5 分钟，`shutdown_timeout` 默认为 45 秒。文件传输沿用共享 core 的独立限制：单文件 10 GiB、同时 4 个任务、单目标 2 个任务、5 分钟启动窗口、2 小时总期限、30 秒提交期限。服务收到 SIGINT/SIGTERM 后停止接收请求、结算运行时和 journal，最后关闭数据库。

MCP 和管理监听器均在路由与安全校验前设置默认 60 秒响应写期限，覆盖未匹配路径、拒绝请求和服务关闭提示。进入管理处理器后写期限收紧为 20 秒；MCP 与文件传输的流式处理器按共享 core 的空闲期限续期。客户端持续不读取响应时，阻塞写入会超时并释放连接。

## 数据与迁移

`data_dir/xops.db` 保存部署 ID、revision、主机、身份、节点、标签、策略、密文凭据、历史版本、连接来源和审计事件；`data_dir/transfers/` 独立保存传输 journal。SQLite 启用外键、WAL、完整同步和有界 busy 等待。部署文件锁覆盖运行服务及离线命令，不能启动第二个实例或在服务运行时导入。

`migrate` 初始化空库或在事务中升级已有 schema；`serve` 也会检查并应用支持的迁移。schema v3 将单跳引用迁入有序 `node_jumps` 关系表；v4 增加管理员与会话表；v5 为已有标签生成独立主键并迁移节点关联，保留标签名称和未使用记录。v6 移除旧管理员会话表，改用 JWT。迁移均保留部署 ID、节点 ID 和 revision。较新版本的 schema 会被拒绝。`status`、导入预览和 `recover` 不创建或升级数据库，需要先执行 `migrate`。

```sh
bin/xops-mcp status --config .local/server.yaml
```

审计保存操作 ID、工具、节点、绑定摘要、风险、决定和结果。为防止客户端输入泄露秘密，不持久化原命令、路径或自由格式错误/详情。凭据采用带格式版本和部署/凭据/用途/版本绑定的 AES-256-GCM 加密。私钥在内存中解密和解析，不写临时私钥文件。历史密文及其来源引用保留用于已准入操作，当前没有自动清理或主密钥轮换命令。

## 预览与应用配置

停止服务后预览导入：

```sh
bin/xops-mcp import --config .local/server.yaml --file examples/inventory.yaml --dry-run
bin/xops-mcp import --config .local/server.yaml --file examples/inventory.yaml --apply --expected-revision 0
```

导入默认只预览。预览输出 `expected_revision`、原节点名/别名和标签名称到稳定 ID 的映射（`node_ids`、`tag_ids`）、凭据变更名称、移除节点和冲突。应用必须使用预览对应的 revision；并发修改或过期 revision 会失败。同一部署和 revision 的新节点 ID 在预览与应用中一致；已有节点保持 ID，删除后再次导入同名节点会获得新 ID。

默认按名称合并输入，未声明的记录保留。`--replace` 用输入替换主机、身份和节点，删除缺席节点并保留永久 ID 墓碑。凭据和独立标签始终按名称合并，历史密文不因节点删除而清除。节点名称用于后续导入匹配；重命名输入键会创建另一节点。

`examples/inventory.yaml` 是可直接预览的禁用节点示例。启用实际节点前，设置已独立核验的 `host_key`、服务端凭据和 `disabled: false`。未知主机密钥不会自动接受，缺少凭据或主机密钥的节点自动以禁用状态导入，并在报告中说明原因。

服务器提供多种主机密钥时，连接按固定公钥的算法协商，并继续精确校验实际公钥；不会接受同一算法下的其他密钥。RSA 公钥使用 `rsa-sha2-512` / `rsa-sha2-256` 签名。

服务器格式与 xops-cli 格式导入的资源名称、标签和节点别名均须满足 [Web 控制台的字符规则](web-console.md#管理节点)：Unicode 字母或文字、十进制数字、`_`、`-`，以及字母后的组合音标，长度 1–256 UTF-8 字节。空白、其他标点和符号会拒绝导入，不提供旧值兼容或自动改写。

服务端库存格式为 `version: 1`：

| 字段 | 内容 |
| --- | --- |
| `tags` | 可选的独立标签名称列表，允许不关联节点 |
| `hosts.<名称>` | `address`、`port`（省略为 22）、`host_key`（单个 SSH 公钥） |
| `identities.<名称>` | `user`、`credential`（凭据名称） |
| `nodes.<名称>` | `host`、`identity`、`aliases`、`tags`、`proxy_jump`（节点名称）、`disabled`、`sudo_mode`、`privilege_credential` |
| `credentials.<名称>` | `kind: password` 与 `password`，或 `kind: key` 与 `private_key`、可选 `passphrase` |
| `policy` | 共享护栏字段；无审批能力时默认 `no_elicit_fallback: deny` |

节点的 `tags` 仍使用名称列表；导入会创建缺失的独立标签并转换为稳定 ID 关联，已有同名标签保留 ID。`--replace` 保留独立标签记录，解除最后一个节点关联不会自动删除标签。

提权模式必须明确：`none`、`root`、`sudo`、`sudoer` 或 `su`。`su` 需要独立提权密码；`sudo` 未指定提权凭据时可使用该身份的登录密码。`proxy_jump` 可写节点名称或逗号分隔的有序节点链，例如 `jump1,jump2`。单跳会沿用该跳板自己的上游路线；显式链严格按列出的顺序直达首跳、逐跳连接，不改写共享跳板的独立路线。包含目标在内最多 32 跳，拒绝循环或缺失引用。主机证书和自动发现的 SSH agent 不在当前凭据接入范围内。

`policy.nodes` 接受节点名称、别名、稳定 ID 或 glob，在本次导入时展开为稳定 ID；未匹配或互相冲突的规则会失败。新增节点需要同时重新提交相关模式规则。CLI 的 `audit_log` 路径不会被采用。

凭据导入需显式指定 `--include-secrets`；包含秘密的输入文件要求 0600，也可用 `--file -` 从 stdin 读取。库存上限为 4 MiB。不要把秘密放进命令行参数或版本库。

stdin 导入受离线命令的 30 秒期限和 SIGINT/SIGTERM 取消约束；取消后退出读取并释放部署锁，无需等待 EOF。Linux 使用文件描述符就绪轮询，保留借用的 stdin；不支持可取消流读取的平台会要求改用普通文件。

```yaml
version: 1
credentials:
  operator-password:
    kind: password
    password: REPLACE_WITH_DEPLOYMENT_PASSWORD
identities:
  operator:
    user: operator
    credential: operator-password
```

该片段仅更新凭据和身份。轮换时再次导入同一凭据名称的新材料，会产生新版本；所有使用该身份或跳板的相关连接代际同时失效。普通地址编辑保持已准入操作的原目标；凭据/信任撤销或禁用会取消可取消的相关工作，已经准入提交的文件任务遵循共享 core 的提交保护。

## 从 xops-cli 单向迁移

```sh
bin/xops-mcp import --config .local/server.yaml --format xops-cli --file exported-config.yaml --dry-run
```

支持 CLI schema v1/v2 的主机、身份、节点、节点别名、标签、跳板和护栏字段。ProxyJump 的节点别名在导入时解析为规范节点名，逗号分隔的链按顺序保存，无法解析或有歧义的选择器会被拒绝。CLI 主机别名不作为服务端节点选择器导入。旧 selector 映射包含节点名与节点别名。源 YAML 不会被修改。

CLI 凭据存储、`login_password_ref`、`passphrase_ref`、提权引用、私钥路径、SSH agent 和 known_hosts 不会自动读取或复制。CLI 导入节点统一禁用，之后使用服务端格式补齐已核验主机密钥与凭据，再启用。v1 内联密码可在显式 `--include-secrets` 后重新加密；v2 混入明文密码会被拒绝。`auto` 提权转为 `none` 并报告；自定义密码提示正则不支持，会明确拒绝导入。此导入不建立两个产品共同写入的关系。

v1 的内联 `password` 和 `su_pwd` 分别生成 `login-<名称的 SHA256>`、`privilege-<名称的 SHA256>` 凭据名称，并同步设置身份和节点引用。摘要只取源资源名称，不包含密码；生成名称满足字符与长度限制，且在预览、应用和再次导入时保持稳定，登录与提权凭据不会混用。

## 文件传输与恢复

客户端先调用 `xops_prepare_upload` / `xops_prepare_download`，再使用返回 URL 和短期凭据传输二进制数据。MCP Token 和短期数据凭据不能混用。客户端本地路径只在客户端解释；目录先由客户端归档。下载状态 `streamed` 不证明客户端已经保存文件，客户端仍须核对大小与摘要。

停止服务后可检查任务：

```sh
bin/xops-mcp recover --config .local/server.yaml
bin/xops-mcp recover --config .local/server.yaml --id TRANSFER_ID --verify
bin/xops-mcp recover --config .local/server.yaml --id TRANSFER_ID --cleanup
bin/xops-mcp recover --config .local/server.yaml --id TRANSFER_ID --resolve-unknown --reason '已人工核对原目标'
```

列表不连接 SSH，不修改 journal。`--verify` 核验原始目标；`--cleanup` 只清理由原任务拥有的临时文件。`--resolve-unknown` 记录人工处理结果并释放目标保护，不重传文件，也不把 `unknown` 改成成功。节点/凭据/信任绑定已经变化时，远程核验和清理拒绝执行，需人工核对原目标。

重启保留部署 ID、相关版本和未知结果锁。备份前停止服务，完整备份数据目录（数据库及 journal），并单独保护主密钥、MCP Token 和配置。恢复到新目录时使用同一主密钥；丢失主密钥无法解密凭据。当前只支持单实例，没有在线备份或主密钥自动轮换。`db-export`、`db-import`、`db-verify` 提供加密离线数据库归档和跨后端校验，详见 [PostgreSQL 与离线迁移](postgresql.md)；归档不包含 journal 或部署密钥。
