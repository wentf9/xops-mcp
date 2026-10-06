# 开发指南

[English](../en/development/README.md)

开发内容集中在本目录。安装、配置和日常操作见[用户使用文档](../user/README.md)。修改代码前先阅读根目录 [AGENTS.md](../../AGENTS.md)。

- [构建与验证](build-testing.md)：本地程序、Docker 镜像、双数据库测试、浏览器与文档检查。
- [架构与依赖归属](architecture.md)：服务端与共享 core 的职责。
- [接口解耦](interface-decoupling.md)：状态、准入、凭据和传输接入契约。
- [管理 API](api.md)：路由、错误及并发编辑。
- [认证契约](admin-auth.md)：JWT、JWE、TLS 配置和前端认证状态。
- [MCP 客户端认证](mcp-auth.md)：多 Token、客户端身份、生命周期与会话/任务隔离。
- [数据库与运行约束](storage.md)：迁移、事务、归档和资源所有权。
- [依赖基线](reuse-baseline.md)：当前 pin 与验证范围。
- [路线图](roadmap.md)：里程碑、已实现能力及后续边界。

用户页描述可操作的步骤和限制，不包含源码归属、里程碑、验收记录、测试夹具、协议内部字段或实现过程。
