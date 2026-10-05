# Web 控制台与部署

[English](en/web-console.md)

控制台使用独立的管理 HTTP 监听器，默认 `127.0.0.1:8081`；MCP 默认监听 `127.0.0.1:8080`。管理端口仅提供页面和管理 API，MCP 端口仅提供 `/mcp` 与 `/v1/transfers/`。Go 程序已嵌入全部 HTML、CSS 和 JavaScript，部署时不需要 Node.js、前端服务器或 CDN。首版面向 Linux 单实例；PostgreSQL、多管理员和 OAuth 不在当前范围内。

## 管理监听器与路径前缀

| 配置 | 默认值与作用 |
| --- | --- |
| `web_enabled` | `true`；设为 `false` 时不启动管理监听器 |
| `web_listen` | `127.0.0.1:8081`，与 MCP 的 `listen` 分离 |
| `web_public_url` | 默认按管理监听地址生成；为管理页面的外部 HTTP(S) origin，不含路径。监听通配地址时必须显式配置 |
| `web_allowed_hosts` | 额外允许访问管理端口的 Host，不继承 MCP 的 `allowed_hosts` |
| `web_base_path` | 默认空，页面位于 `/`；例如 `/console` 或 `/platform/ops`，覆盖页面、资源和管理 API |

反向代理部署示例：

```yaml
listen: 127.0.0.1:8080
public_url: https://mcp.example.com
web_listen: 127.0.0.1:8081
web_public_url: https://admin.example.com
web_base_path: /console
```

页面地址为 `https://admin.example.com/console/`，管理 API 为 `/console/api/v1/`；`/console` 自动跳转到带尾斜杠的地址。代理应保留前缀和 Host，使用不带尾斜杠的 `proxy_pass http://127.0.0.1:8081`。前缀由配置固定，不读取 `X-Forwarded-Prefix`，不作用于 MCP 与传输路径。前缀仅允许由字母、数字、`-._~` 构成的路径段；相对段、重复斜杠及编码分隔符会被拒绝。两个监听器共用有界关闭流程；任一端口启动失败都会释放另一监听器和部署锁。

两个公开 origin 也可使用同一域名；此时将示例中两组 `location` 合并到同一个反代虚拟主机，仍分别转发到管理端口和 MCP 端口。

## 初始化与登录

使用 [server.yaml](../examples/server.yaml) 时，分别创建三个文件：

```sh
bin/xops-mcp keygen --out .local/master.key
bin/xops-mcp keygen --out .local/mcp.token
bin/xops-mcp keygen --out .local/admin.setup
bin/xops-mcp migrate --config .local/server.yaml
bin/xops-mcp serve --config .local/server.yaml
```

打开 `http://127.0.0.1:8081/`，填写管理员用户名、密码和 `admin.setup` 中的初始化凭据。初始化凭据必须与 MCP Token 不同；只允许创建一个管理员，后续初始化请求不会覆盖已有账户。管理员密码为 12–72 字节，以 bcrypt 哈希保存。

初始化和修改密码均按 UTF-8 字节数校验，不按字符数计数。例如 `管理员新密码` 为 18 字节，符合要求；24 个常见中文字符通常为 72 字节。前端和后端都会拒绝不足 12 字节、超过 72 字节或包含 NUL、CR、LF 的新密码，确认密码需完全一致。

初始化完成后可删除 `admin.setup`，已初始化的服务不再读取它。未配置 `admin_bootstrap_token_file` 时，Web 初始化关闭，仍可使用离线命令：

```sh
bin/xops-mcp admin-init --config .local/server.yaml --username admin --password-file /secure/admin-password.txt
bin/xops-mcp admin-reset --config .local/server.yaml --password-file /secure/new-admin-password.txt
```

密码文件必须为 0600；也可使用 `--password-stdin`。命令不接受明文密码参数，且需要停止服务以取得部署锁。`admin-init` 不覆盖账户；`admin-reset` 为持有部署主密钥的运维人员重置密码，并使全部会话失效。

管理员 Cookie 为 HttpOnly、SameSite=Strict，仅作用于 `<web_base_path>/api/v1`，有效期 12 小时。管理入口 `web_public_url` 使用 HTTPS 时 Cookie 带 Secure。退出登录使当前会话失效；修改密码和重启服务使所有管理员会话失效。MCP Token 与短期传输凭据不能登录控制台，管理员 Cookie 也不能访问 MCP 工具。

支持从其他网站的链接打开控制台首页。跨站放行仅限首页的 `GET`/`HEAD` 顶层文档导航；仍校验 Host 和请求携带的 Origin，跨站 iframe、资源抓取及管理 API 请求保持拒绝。

## 管理节点

推荐顺序：

1. 在“主机与信任”创建 SSH 地址和端口。
2. 选择“配置公钥”，通过直连获取或输入已独立核验的公钥，预览指纹后确认并固定。直连获取不发送 SSH 认证凭据，输入预览不连接主机；两种方式都不会自动建立信任。确认记录绑定当前会话、主机和配置版本，两分钟内有效且只能使用一次。
3. 在“凭据”写入密码或私钥；私钥可附带口令。列表和编辑界面只显示元数据，留空可保留已有材料。
4. 创建“登录身份”，关联 SSH 用户名和凭据。
5. 在“标签”页面创建所需标签，再创建节点，选择主机、身份、已有标签和有序跳板链，然后启用。连接测试验证 SSH 握手与认证，不执行远端命令，需要节点及依赖跳板已启用。

主机、登录身份、凭据、节点、标签的名称和节点别名使用同一字符规则：允许 Unicode 字母或文字、十进制数字、ASCII 下划线 `_` 和连字符 `-`。支持跟随字母的组合音标和元音标记，适用于重音字母、印地语等文字。长度为 1–256 个 UTF-8 字节；拒绝所有空白、其他标点、符号、emoji、不可见字符及独立组合标记。例如 `运维_生产-01`、`école`、`हिन्दी` 合法，`ops prod`、`ops,prod`、`node.example` 不合法。同类名称仍须唯一。

前端在输入和提交时校验，后端对 API 写入、导入及库存加载统一校验，不保留旧非法名称或别名的兼容例外，也不自动裁剪、拆分或改写非法值。开发数据也需满足规则。节点别名编辑每行一个值，空行忽略；留空表示不配置额外别名。

仅经 ProxyJump 可达的主机，首次配置或轮换公钥时选择“输入已独立核验的公钥”。通过主机控制台或已验证的管理连接读取主机 `.pub` 文件（例如 `/etc/ssh/ssh_host_ed25519_key.pub`），粘贴单个 SSH 公钥，并通过可信渠道核对预览的 SHA256 指纹后勾选确认。输入不接受私钥、带选项的公钥或 SSH 证书。更改来源或公钥内容会清除旧预览及勾选状态，需重新预览确认；跳板链在节点配置中选择。

修改立即通过发布协调器对后续 MCP 请求生效，不重建整个 runtime 或断开全部会话。编辑窗口保存打开时的版本条件；若其他会话已修改配置，返回冲突并保留表单内容，需要关闭窗口、刷新并重新编辑。

普通地址编辑不会重定向已准入操作。凭据轮换、撤销主机信任、停用节点或修改策略，会撤销受影响的可取消操作；已准入的上传提交仍按原提交期限结算，取消不意味着远端回滚。撤销主机信任同时停用直接引用该主机的节点，下游跳板依赖随之不可用。

仍被引用的主机、身份、凭据和跳板不能删除。节点删除后 ID 不复用。标签在“标签”页面独立创建、重命名和删除，可以不关联任何节点。节点编辑通过多选关联已有标签。标签拥有稳定的不透明主键，名称唯一；重命名保留 ID 和节点关联，删除标签会解除关联而不删除节点。SQLite v5 迁移为现有标签生成 ID 并保留全部关联和未使用记录；MCP 仍显示标签名称。

## 策略、审计和运行状态

“操作策略”提供全局风险确认级别、客户端无法确认时的处理方式、额外命令拦截模式、受保护路径和节点级别覆盖。规则与 MCP 共用 core 实现。

导入策略未设置全局 `approval_threshold` 或值为空时，沿用 core 的 `dangerous` 默认值，页面显示“高风险操作”。保存其他策略字段时保持该有效阈值；已有的显式阈值按原值显示和保存。

额外命令拦截模式使用 core 的 `filepath.Match` glob 语法，每行一条，匹配完整命令字符串。`hostname` 只匹配该命令，`hostname*` 也匹配 `hostname -f`；支持 `*`、`?` 和 `[abc]`，其中 `*`、`?` 不跨越服务运行平台的路径分隔符。它不是正则表达式，`^hostname$` 中的 `^`、`$` 是普通字符，不会拦截 `hostname`。非法 glob（如未闭合的 `[`）会拒绝保存。

“审计记录”按节点、结果筛选，并按 ID 游标分页。审计只保存工具、节点、授权决定、结果和绑定信息；不保存原命令、路径或自由格式诊断。“运行中”显示活跃的准入操作、阶段、节点和开始时间；它不是另一套传输状态机，也不列出未开始的 ready 任务。传输最终结果仍由 MCP 任务状态和离线 journal 恢复确认，`unknown` 不会被自动解释为成功。

结果筛选按存储状态精确匹配：“操作完成/操作失败”对应 `executed`/`error`，“传输完成/传输失败”对应 `completed`/`failed`，并可与节点筛选和分页组合使用。下载的 `streamed` 仍显示为“已传输”，不表示客户端已保存成功；“全部结果”可查看所有状态。

切换审计筛选条件会立即清空旧记录和分页游标。首页或下一页加载期间禁用“加载更早记录”；旧筛选的延迟响应不会覆盖或追加到当前结果。刷新库存时若所选节点已删除，节点筛选恢复为“全部节点”，清空旧结果和游标，并保留结果类型筛选。新首页加载失败时保留当前筛选条件，选择“重试加载”从最新记录重新查询。

库存查询等不绑定具体节点的操作使用空节点列表，控制台显示“—”。关闭正在保存的编辑窗口仅关闭表单，不保证撤销已发送的请求；该请求完成后不会关闭后来打开的编辑窗口，也不会清除后来编辑的策略、密码等页面表单。策略保存期间继续输入的内容同样保留，原草稿的版本条件不随后台刷新改变；发生版本冲突时保留输入并提示重新加载。主动点击“刷新”会重新载入表单，但刷新请求发出后新增的输入仍会保留。

数据库提交后若发布失败，受影响的新操作保持暂停。控制台显示“仍待应用”；“重试应用”只重新读取和发布已提交状态，不重复业务写入。所有配置变更统一显示服务端返回的后置审计告警，代替普通成功提示，明确配置已应用但审计写入失败。此时不回滚或自动重放变更；后续页面刷新失败也只提示重新查询，不将已完成的写入当作失败重试。

## HTTP API

管理 API 位于管理端口的 `<web_base_path>/api/v1/`，下表路径均相对此目录。写请求必须为同源请求，使用 JSON、有效会话的 `X-CSRF-Token`，并在配置操作中携带 `If-Match`。`GET /inventory` 返回强 ETag，例如 `"7"`；缺少条件返回 428，版本冲突返回 412。凭据输入仅用于写入，返回数据不含密码、私钥、口令、密文或密码哈希。

| 接口 | 用途 |
| --- | --- |
| `GET /auth/session` | 登录/初始化状态、当前会话 CSRF Token |
| `POST /auth/setup`、`POST /auth/login` | 初始化或登录；JSON 字段为 `username`、`password`，初始化另有 `token` |
| `POST /auth/logout`、`PUT /auth/password` | 退出或修改密码；密码更新字段为 `current`、`next` |
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

## 原生与容器部署

[systemd 配置](../examples/deployment/systemd.yaml)、[服务单元](../examples/deployment/xops-mcp.service)和 [Nginx 配置](../examples/deployment/nginx.conf)提供原生部署示例。创建专用 `xops-mcp` 系统用户，将二进制安装到 `/usr/local/bin/xops-mcp`，配置及 0600 密钥文件放到 `/etc/xops-mcp/` 并由该用户持有。systemd 使用 0700 的 `/var/lib/xops-mcp` 持久化状态。

修改示例域名、证书路径及 `public_url`、`web_public_url` 后，安装服务单元并执行 `systemctl daemon-reload`、`systemctl enable --now xops-mcp`。两个原生监听器保持回环地址。Nginx 将 `/mcp`、`/v1/transfers/` 转发到 8080，将管理域名的 `/console/` 转发到 8081；均保留路径和 Host，MCP 关闭流式缓冲。管理前缀必须与 `web_base_path` 一致。

[Dockerfile](../Dockerfile) 构建包含静态资源的单二进制镜像，以 UID/GID 65532 运行；[Compose 示例](../examples/deployment/compose.yaml)使用命名数据卷、只读根文件系统和独立只读密钥目录。

```sh
mkdir -m 700 -p examples/deployment/.secrets
bin/xops-mcp keygen --out examples/deployment/.secrets/master.key
bin/xops-mcp keygen --out examples/deployment/.secrets/mcp.token
bin/xops-mcp keygen --out examples/deployment/.secrets/admin.setup
sudo chown -R 65532:65532 examples/deployment/.secrets
docker compose -f examples/deployment/compose.yaml up -d --build
```

密钥保存在点号开头的 `.secrets` 目录，Go 的 `./...` 包扫描会跳过它，仓库中的构建和测试可由普通用户直接执行。Compose 要求该目录已存在，不会自动创建空的密钥目录。秘密目录保持 0700、密钥文件保持 0600，并归 UID/GID 65532 所有。[.dockerignore](../.dockerignore) 排除整个 `examples/deployment` 部署示例目录，使普通 Docker 用户构建时不会遍历其中的私有 `.secrets` 目录或将其内容发送到构建上下文。Compose 仍使用宿主机上的配置文件，配置与密钥仅在运行时只读挂载。

已有旧 `examples/deployment/secrets` 实体目录时，应将原目录重命名为 `.secrets`，保留所有文件、所有者和权限，无需重新生成密钥。若已有容器仍引用旧路径，可保留 `secrets -> .secrets` 符号链接；Go 不遍历该链接，现有挂载也可继续使用。更新后的 Compose 直接挂载 `.secrets`，后续重建容器后可移除旧链接。

Compose 将两个端口分别映射到宿主机回环地址；控制台示例地址为 `http://127.0.0.1:8081/console/`。

修改 [container.yaml](../examples/deployment/container.yaml) 中的公开地址后再用于反向代理部署。命名卷根目录由 UID/GID 65532 持有，程序在卷内创建 0700 的 `data/` 子目录作为数据目录；已有数据目录同样需要正确的所有者和私有权限。不要把主密钥仅存放在数据卷内。

## 备份与恢复

停止唯一运行实例后，完整备份数据目录（SQLite、可能存在的 WAL/SHM 和整个 transfers journal），并分别保管配置、主密钥和 MCP Token。原生部署可使用：

```sh
sudo systemctl stop xops-mcp
sudo tar --numeric-owner -czf /secure-backups/xops-data.tgz -C /var/lib/xops-mcp .
# 将 /etc/xops-mcp 中的配置、主密钥和 MCP Token 另存于受保护的备份位置
sudo systemctl start xops-mcp
```

容器部署先 `docker compose stop`，再备份命名卷和密钥挂载。恢复时保持文件权限和所有者，将完整数据放回私有目录，配置同一主密钥与 MCP Token；先执行 `migrate`，再启动一个实例。缺少主密钥无法解密 SSH 凭据。重启会清除旧管理员会话，需要重新登录；节点、策略、管理员密码、稳定 ID 和 unknown 目标锁保留。不要同时运行原实例与恢复副本。

## 开发验证

Go 构建直接嵌入 `web/assets`。只有浏览器验收需要 Node.js 与 Playwright：

```sh
npm --prefix web ci --ignore-scripts
cd web
npx playwright install chromium
npm run check
npm run test:browser
```

测试自动启动隔离的临时部署和 SSH fixture，验证外部链接导航、初始化/登录、直连和独立输入公钥确认、CRUD、真实连接测试、编辑冲突、XSS 转义、策略、审计、移动布局与退出。Go 集成测试另覆盖不可直连地址的公钥配置与轮换、真实跳板连接、错误公钥拒绝，以及预览的会话/主机/版本绑定和单次确认。可用 `XOPS_TEST_CHROME=/path/to/chrome` 选择已有浏览器。截图写入忽略的 `web/test-results/`，不使用个人凭据或生产节点。
