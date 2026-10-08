# 存储与运行契约

[English](../en/development/storage.md) · [开发指南](README.md)

用户操作见[数据库](../user/postgresql.md)及[维护](../user/maintenance.md)。业务存储接口与具体适配器属于本仓库，SSH/SFTP/MCP 执行保持上游公共 core 依赖。

## 数据与更新

SQLite 当前 schema v8；PostgreSQL 当前 schema v5，两个后端迁移历史独立。当前实体包括 hosts、identities、nodes、tags、credentials、credential_versions、policies、sources、tombstones、admin、mcp_tokens 和 audit_events。node_jumps 保留有序跳板，node_tags 通过稳定 tag ID 关联。旧管理员会话表已移除。

业务写入使用 expected revision。service 先建立协调器准入屏障，数据库提交后再发布快照；提交结果不确定或发布失败时，通过 Reconcile 重读已提交状态，不能盲目重放写入。普通编辑保留已准入操作的原目标；禁用、策略或凭据撤销遵循共享 gate 契约。

凭据为版本化 AES-GCM 密文，认证上下文绑定部署、ID、kind 和版本。历史凭据和来源支持原目标核验，删除节点保留永久墓碑。秘密不得进入 Web DTO 或审计自由文本。

## 后端差异

SQLite 使用外键、WAL、FULL 同步、2 秒 busy 等待和一个数据库连接。数据目录与文件权限检查、部署文件锁同时保护本地数据库和 journal。

PostgreSQL 使用独立 SQL、JSONB/BYTEA 和 identity 序列。读事务使用 repeatable read；写事务以 deployment 行的 revision 条件串行化。所有数据库操作复用一个固定物理会话，在该会话持有 advisory lock，避免连接池替换导致所有权静默丢失。statement timeout 为 5 秒，lock timeout 为 2 秒；事务回滚使用单独的有界清理上下文。

当前库存重写采用分块多行 INSERT，按行数、参数数量和负载大小限制批次。墓碑和不可变凭据版本采用集合查询，不对每个节点发起独立网络请求。恢复的历史凭据和审计同样批量写入。延迟回归验证 3 ms 响应延迟下 400/4096 节点的保存、编辑及恢复，保持原五秒操作预算。

所有权连接丢失后停止准入并退出监听，不自动重连到旧内存快照。Close 取消并 join 心跳、关闭固定会话与池，最后释放本地锁。单实例限制不因使用外部数据库而解除。

## 离线归档

归档仅支持 format 3，文件头为 `XOPSDB\x03`，加密认证上下文为 `xops-mcp database backup v3`，内部 `Format` 必须为 3。v1、v2 和其他版本直接拒绝，不提供兼容读取或格式转换。整体使用部署主密钥认证加密，大小上限 256 MiB。包含库存、密钥校验、全部历史凭据/来源、管理员密码哈希、MCP Token 记录及节点范围和审计，不包含配置、外部密钥或 journal。

Restore 只允许初始化后的空目标，在一笔事务内保留原 domain/revision/ID，恢复数据库记录及审计序列。逐个验证历史密文可解密，并验证当前 source 与可执行视图一致。归档验证将缺失、`null` 和空的 `MCPTokens` 统一为空数组，确保导入后的内容摘要一致。归档校验不能代替 journal 或远端文件核验。JWT 不写入数据库，其有效性由密钥、domain、prefix 和过期时间决定。

## 验证范围

共同契约覆盖双后端事务、冲突、凭据不可变性、永久墓碑、管理员密码版本和四种归档方向。产品集成包含真实本地 HTTP/SSH/SFTP、加密私钥、ProxyJump、Web 更新对 MCP 的可见性和 unknown 恢复。各后端另有迁移失败、锁、错误主密钥、取消及资源回收检查。

文件传输仍使用上游独立 journal，客户端本地路径不进入服务端路径解析。当前服务没有在线备份、主密钥自动轮换、分布式任务调度或多实例 journal 所有权。具体命令见[构建与验证](build-testing.md)。
