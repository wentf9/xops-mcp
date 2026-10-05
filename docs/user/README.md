# 使用指南

[English](../en/user/README.md)

XOps MCP 运行在能够访问目标 SSH 主机的 Linux 服务器上。浏览器用于管理节点和凭据，MCP 客户端用于发起运维操作。服务目前只支持单实例；使用 PostgreSQL 也不能同时启动多个实例。

## 首次使用

1. 选择[容器部署](container.md)，或用已有程序[直接运行](install.md)。
2. 打开管理页面，使用初始化码创建管理员账户。
3. 在控制台添加主机、核对公钥、填写凭据，并启用节点。
4. 按[控制台与客户端指南](console.md)连接 MCP 客户端。

管理页面通常使用端口 `8081`，MCP 使用端口 `8080`。管理员密码和 MCP 访问凭据各自独立。

## 常用操作

| 操作 | 文档 |
| --- | --- |
| 更改监听地址、路径或 HTTPS | [配置说明](configuration.md) |
| 管理节点、跳板、标签和策略 | [控制台与客户端](console.md) |
| 批量导入或从 xops-cli 导入 | [配置导入](import.md) |
| 使用 PostgreSQL 或迁移数据库 | [数据库指南](postgresql.md) |
| 重置密码、备份、处理失败任务 | [维护与排错](maintenance.md) |

密钥与数据应一并妥善保管，备份和恢复步骤见[维护与排错](maintenance.md)。
