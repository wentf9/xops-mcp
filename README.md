# XOps MCP

[English](README_en.md)

XOps MCP 是可自行部署的远程运维服务。通过 Web 控制台管理 SSH 主机、登录凭据和操作策略，再使用支持 MCP 的客户端执行命令、查看文件和传输文件。

支持 SQLite 和 PostgreSQL、HTTP 和 HTTPS、跳板连接、操作审计、配置导入及备份恢复。目前使用一个管理员账户，并以单实例运行。

## 用户使用文档

从[使用指南](docs/user/README.md)开始。首次安装推荐阅读[容器部署](docs/user/container.md)；已有可执行程序可阅读[直接运行](docs/user/install.md)。

- [配置、访问地址与 HTTPS](docs/user/configuration.md)
- [控制台与 MCP 客户端](docs/user/console.md)
- [MCP Token 管理](docs/user/tokens.md)
- [导入主机和凭据](docs/user/import.md)
- [PostgreSQL 与数据库迁移](docs/user/postgresql.md)
- [备份与故障处理](docs/user/maintenance.md)

## 开发文档

[开发指南](docs/development/README.md)包含构建、测试、管理接口、架构、依赖和路线图。

## 许可证

[MIT](LICENSE)
