# 公共接口解耦与服务端接入

状态：上游 D1–D5 公共实现和 D6 独立抽取已完成，本仓库已固定到可从远端下载的 core 提交并通过无 replace 验收。core 消费测试已从 testdata 迁入 internal/coreconsumer，常规 CI 同时运行它与独立 legacy 契约。精确版本及 PR 状态入口见[依赖基线](reuse-baseline.md)；数据库、Web 和产品入口仍属后续阶段。

公共源码的主设计位于 [xops-cli](https://github.com/wentf9/xops-cli) 的 `docs/development/shared-core-decoupling.md`；本文件约定新服务如何消费该设计。规划同时覆盖 CLI 公共代码的可抽取性和服务端接口接入，不把数据库支持作为解耦完成的唯一判断。

## 1. 目标依赖

```text
xops-mcp/internal/service、storage
             │ 实现业务接口
             ▼
xops-mcp/internal/adapters/xops
             │ 固定版本导入
             ▼
github.com/wentf9/xops-cli/core/mcp/runtime
github.com/wentf9/xops-cli/core/mcp/sshexec
github.com/wentf9/xops-cli/core/ssh、sftp、auth、log
```

上游已建立 auth、log、concurrent、ssh、sftp，以及 mcp 下的 policy、guardrail、transfer、tunnel、remotefile、ports、sshexec；runtime 装配入口已迁移，消费者通过固定远端版本直接导入 core。上游在原仓库中建立自包含 core 子树，不新增嵌套 module，也不立即创建第三个仓库。旧 `pkg/*` 入口保留为兼容外观，核心只维护一份实现。

新服务最终不直接或间接编译 CLI config/models/adapter、vault backend、i18n、TUI、根 internal/terminal。后续抽出独立公共仓库时，服务业务层保持不变，依赖路径调整集中在 adapter 和装配入口。

## 2. 服务端实现四类依赖

| 公共接口 | 服务端实现 | 关键约束 |
| --- | --- | --- |
| `StateSource.List/Resolve` | 从同一已发布版本取得展示数据、完整目标/跳板计划及策略 | 带 context；不返回数据库实体或明文机密；多节点一次解析 |
| `ExecutionGate.Enter` | 与节点更新发布共享版本协调器 | 最后校验并登记；和 StateSource 使用同一非空 DomainID |
| `NewBackend(ctx)` | 用共享 sshexec 构造独占 runtime handle，注入认证/信任来源 | Runtime 关闭 backend；不关闭宿主 DB |
| `AuditSink.Append(ctx,event)` | 服务端审计适配器 | 前置失败不执行，后置失败不诱导远端重试 |

策略随操作快照返回，不由另一个数据库查询拼接。只读检查、正式执行、传输启动、提交和恢复使用不同用途的 Permit；只读检查不能获取可写 SFTP client。

数据库和管理员会话仍是宿主业务资源。Runtime 不拥有数据库连接池，不认识 ORM，也不会自动读取个人配置、私钥、known_hosts 或 SSH_AUTH_SOCK。

## 3. 认证、信任和环境

- 实现版本绑定的 SecretResolver：按原目标/用途/版本解析，失配明确失败，不转取最新凭据。
- 实现 KeySource：解密并解析数据库私钥，返回 Signer/Close 租约；不把私钥写入个人路径，不将私钥路径暴露给 MCP 列表或 Web DTO。
- 实现 HostKeyVerifier：校验规范 endpoint、信任版本和服务端 public key。Web 确认主机密钥是独立管理操作，不让后台 MCP 弹出终端提示。
- 日志显式注入；公共 Nop 不初始化 CLI 颜色或标准流。MCP 新服务不提供本地交互 InputBridge。
- 旧 CLI 的默认环境发现与 Windows 输入行为由上游兼容适配器保留；服务端无需携带其实现。

## 4. 动态更新流程

```text
Web 校验和版本前置条件
  → 对受影响节点/共享身份/跳板依赖建立准入屏障
  → 有期限数据库事务
  → 发布一致视图、旧连接代际停止复用
  → 恢复准入并返回新 revision
```

事务回滚解除屏障；提交成功但发布失败继续阻止受影响的新操作，重新加载已提交数据，不能盲目重复写入。屏障只在内存中短期登记，网络/数据库 I/O 不在互斥临界区中进行。

审批期间没有数据库事务。审批后、最终准入前修改目标、凭据、完整跳板链或策略，旧绑定必须失败并重新审批；不能在原审批下执行最新配置。无关节点变更不使所有审批失效。

普通编辑不改变已准入任务的目标。删除/禁用/凭据与信任撤销阻止新准入，并取消可取消的受影响任务。已发送的远端命令或进入提交阶段的上传不承诺回滚。

## 5. 四种标识不可混用

| 标识 | 用途 |
| --- | --- |
| NodeID | 稳定、删除后不复用的业务 ID |
| TargetID | 地址、默认端口和账号组成的资源锁；沿用现有未知提交保护语义 |
| ConnectionKey | scope、完整跳板链、认证/提权/信任版本组成的连接代际 |
| Binding | 完整输入、目标集合、相关配置和策略版本的授权摘要 |

凭据轮换会改变 ConnectionKey，但不能改变 TargetID 来绕过 unknown 上传锁。别名和多个节点映射到同一物理目标时继续共用目的地保护。RequestDigest 保持请求幂等语义，不能与随配置变化的 Binding 混为一项。

## 6. 传输、停机与恢复

先沿用公共本地 journal；节点存进数据库不意味着任务日志也已经迁移。prepare、claim、commit、recovery/cleanup 都要校验原绑定。

提交预约与撤销排定顺序；预约和持久化意图均成功后才发送 rename。进入该阶段后不承诺取消，发送后的确认不确定保持 unknown。传输 manager 唯一负责状态，不在业务数据库重复维护另一套任务状态机。

新 journal 增加格式版本并保留旧记录读取。旧的、没有完整绑定的未完成任务不能用当前配置重放；未知提交的目的地锁必须保留。节点删除/换地址后不能连接同名新节点清理旧临时文件。

宿主关闭顺序：停止接收管理写入和新 MCP 操作，关闭各 runtime、等待有界提交及日志结算，最后关闭审计和数据库。启动部分失败也要回收已创建的 backend、journal 锁及监听器。

## 7. 可执行的验收设计

| 测试场景 | 预期 |
| --- | --- |
| 只导入 core runtime 与新适配器 | 编译图不含原 config/models/adapter/vault/终端 UI |
| 两个 runtime 同时运行 | 节点、日志、凭据和关闭相互隔离；无进程环境切换 |
| 审批后修改目标，再释放执行屏障 | 不连接或执行新目标，返回 stale binding |
| 同时更新共享身份和多级跳板 | 所有依赖节点进入新代际；无关连接不受影响 |
| 只修改不参与策略的显示信息 | 不重建所有连接、不使无关审批失效 |
| 数据库提交成功、发布故障 | 新准入被阻止，恢复加载，不重复执行事务 |
| 撤销与上传 commit 交错 | 按预约次序判定；不把可能已提交说成 cancelled |
| 旧 journal / 新未知版本 | 兼容读取旧记录，拒绝未知格式，保留 unknown 锁 |
| 审计、连接、runtime 构造阶段故障 | 无未关闭资源；post-audit 不触发执行重试 |
| 下载校验与客户端保存 | streamed 不被解释为客户端文件已保存 |
| 独立 core 抽取构建 | 不依赖原仓库源码、module 或本地 replace |

竞态测试使用显式同步屏障复现顺序，不依赖 sleep 或仅增加超时。实际 SSH/SFTP、协议发现、交叉编译和原生平台运行分别记录证据。

## 8. 升级顺序

1. 上游 D1/D2：公共叶子层及 SSH/SFTP 边界；新仓库增加 core 接口探针。
2. 上游 D3：公共 MCP runtime 与兼容 facade；比较新旧入口工具 schema、结果和关闭行为。
3. 上游 D4/D5：动态准入/连接代际及异步任务绑定；消费者用内存版本源验证行为，尚不要求数据库已实现。
4. 上游 D6：core 子树独立 module 抽取检查通过，本仓库 core 消费测试切换到公共入口。
5. 固定远端可获取的 module 版本，完成无替换消费者验收并升级 go.mod/go.sum；产品开发随后进入 SQLite 与 Web 阶段。

探针分成 internal/coreconsumer 与 internal/legacycompat，避免旧测试的 config import 导致聚合图误判。internal/dependencycheck 分别验证 core 的三平台图、整体 CLI/TUI 边界与远端版本。未来生产入口仍须单独检查；当前没有产品入口。

详细阶段与产品范围见[路线图](roadmap.md)，跨仓库职责见[架构](architecture.md)。


## 可复现的消费者验证

根 module 通过 `GOWORK=off go build ./...`、`GOWORK=off go test ./...` 和 `golangci-lint run ./...` 验证同一个固定远端 pin 的 core 与旧入口契约。旧入口探针不能代替 core 的独立依赖边界检查。

`python3 scripts/check_core_consumer.py --upstream /path/to/xops-cli` 从 internal/coreconsumer 复制探针到临时 module，再在那里设置临时替换。它检查 Linux/Windows/macOS 导入图、当前平台 race、lint，以及真实 HTTP/SSH 命令、二进制 SFTP 往返和动态禁用。两个仓库都不写入 replace、workspace 或改动后的 pin。常规 Go 包发现和 CI 已包含 core 消费测试。

升级依赖前运行 `python3 scripts/check_core_consumer.py --version EXACT_VERSION`，以可下载 pin 做无 replace 验收，随后更新根 go.mod/go.sum 并运行完整 gates。当前固定版本已通过此验收；版本、提交与校验范围见依赖基线。

TransferSession 新增 ReserveCommit(ctx, permit)，使流式传输和提交保留原 transport、分别使用准入许可。v2 journal 保存无秘密的原始授权和无损编码的凭据版本；claim、重新发放凭证和恢复均校验原绑定。旧 v1 保留状态与 unknown 锁，但不能获得远程权限。数据库适配器必须跨重启持久化域与依赖版本；SQL 存储和 Web 仍属于后续产品阶段。
