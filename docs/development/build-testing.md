# 构建与验证

[English](../en/development/build-testing.md) · [开发指南](README.md)

命令均从仓库根目录执行。需要 Go 1.26+、golangci-lint v2；浏览器验证另需 Node.js 22 和 Playwright。发布检查使用 `GOWORK=off`，不依赖相邻 checkout 或本地 replace。

## Makefile 入口

安装 GNU Make 后，可使用以下目标；单独执行 `make` 或 `make help` 显示全部目标。

| 命令 | 用途 |
| --- | --- |
| `make build` | 构建实际程序到 `bin/xops-mcp` |
| `make run CONFIG=.local/server.yaml` | 重新构建并启动已初始化的本地部署 |
| `make check` | 检查所有 Go 包构建、Go 测试、lint、模块整洁性和依赖完整性 |
| `make test-race` | 对当前测试后端运行 race 测试 |
| `make test-sqlite` / `make test-postgres` | 分别使用 SQLite / PostgreSQL 运行同一组业务测试和 race 检查 |
| `make fmt` | 格式化 Go 源文件 |
| `make web-install` | 按锁文件安装 Web 测试依赖 |
| `make web-check` / `make test-browser` | 检查 Web 资源 / 检查资源并运行浏览器验收 |
| `make docker-build` | 构建 `xops-mcp:local` 容器镜像 |
| `make clean` | 删除 `BINARY` 指定的程序和 `coverage.out`，保留数据、配置与密钥 |

可覆盖 `BINARY`、`CONFIG`、`IMAGE` 和 `TEST_FLAGS`，例如 `make build BINARY=dist/xops-mcp`。测试默认使用 `-count=1 -timeout=120s`。工具路径可通过 `GO`、`GOLANGCI_LINT`、`NPM` 和 `DOCKER` 指定。

Makefile 默认设置 `GOWORK=off`，包括覆盖继承的环境变量；跨仓库联调须显式传入 `make GOWORK=/path/to/go.work build`。发布检查仍使用默认设置。`make check` 不包含双后端 race 和浏览器验收；按下文准备 PostgreSQL 与浏览器后单独执行相应目标。`make test-postgres` 要求设置 `XOPS_TEST_POSTGRES_DSN`，缺少时直接失败。`make run` 执行 `serve`，请先按下文准备配置与密钥。

## 构建实际程序

```sh
mkdir -p bin
GOWORK=off go build -o bin/xops-mcp ./cmd/xops-mcp
```

`-o bin/xops-mcp` 指定可执行文件的输出路径；`go build ./...` 用于检查所有包能否构建。

本地运行时可准备独立目录：

```sh
mkdir -m 0700 -p .local
cp examples/server.yaml .local/server.yaml
bin/xops-mcp keygen --out .local/master.key
bin/xops-mcp keygen --out .local/admin.jwt.key
bin/xops-mcp keygen --type rsa --out .local/admin.encryption.key
bin/xops-mcp keygen --out .local/admin.setup
bin/xops-mcp migrate --config .local/server.yaml
bin/xops-mcp serve --config .local/server.yaml
```

已有目录不得盲目重复生成密钥。需要重新开始时使用全新的目录，不删除用户已有数据。

构建 Docker 镜像：

```sh
docker build -t xops-mcp:local .
```

## Go 与 PostgreSQL

```sh
GOWORK=off go build ./...
GOWORK=off go test ./...
GOWORK=off golangci-lint run ./...
GOWORK=off go mod tidy -diff
GOWORK=off go mod verify
git diff --check
```

使用专门的可丢弃 PostgreSQL 服务，测试账号需要 CREATEDB。测试自行创建随机数据库并清理，不得设置为生产 DSN：

```sh
docker run --detach --rm --name xops-mcp-tests -e POSTGRES_PASSWORD=synthetic-test-password -p 127.0.0.1:15432:5432 postgres:18
docker exec xops-mcp-tests pg_isready -U postgres
```

等待 `pg_isready` 报告可连接后运行：

```sh
export GOWORK=off
export XOPS_TEST_POSTGRES_DSN='postgres://postgres:synthetic-test-password@127.0.0.1:15432/postgres?sslmode=disable'
XOPS_TEST_BACKEND=sqlite go test -race -count=1 -timeout=120s ./...
XOPS_TEST_BACKEND=postgres go test -race -count=1 -timeout=120s ./...
docker stop xops-mcp-tests
```

没有 DSN 时 PostgreSQL 专项会跳过；显式选择 `XOPS_TEST_BACKEND=postgres` 却缺少 DSN 时失败。只有确认 PostgreSQL 实际参与的结果才算双后端验收。

## 浏览器

```sh
cd web
npm ci --ignore-scripts
npx playwright install chromium
npm run check
npm run test:browser
```

`npm run check` 检查 JavaScript 语法和嵌入的 asmcrypto.js 与锁定 npm 包的一致性。浏览器验收运行 UI 回归，以及 HTTPS/Web Crypto、普通 HTTP/嵌入加密两种真实协议流程。夹具使用临时证书、数据库与 SSH 服务，不访问个人凭据或现网主机。使用已安装浏览器时设置 `XOPS_TEST_CHROME=/path/to/chrome`。输出截图位于忽略的 `web/test-results/`。

更新 asmcrypto.js 版本后执行 `npm run vendor:crypto`，一并提交锁文件、带许可证的资源与检查结果。

## 文档

- 用户指南放在 `docs/user/`，英文放在 `docs/en/user/`；开发文档放在对应的 `development/` 目录。
- 检查 Markdown 相对链接、锚点及双语页面对应关系。移动页面后同步 README、AGENTS 和命令帮助中的引用。
- 使用部署说明中指定的程序或镜像验证命令。
- 在独立临时目录重放密钥生成、配置初始化、导入、备份与恢复命令。用 `openssl pkey -check -noout` 验证 RSA 文件，不打印私钥。
- 容器流程应使用隔离镜像标签、项目名、端口和卷，验证 UID 65532 的权限；不要重建或停止用户正在使用的容器。
- 修改文档不意味着自动提交或推送。仓库有用户修改时，先确认边界并保留其内容。

跨仓库联调时可以使用仓库外的 `go.work`，显式设置 `GOWORK=/path/to/go.work`；浏览器测试通过 `XOPS_TEST_GOWORK=/path/to/go.work` 将同一工作区传给构建夹具。共享内核发布并更新 `go.mod` 固定版本后，须重新通过 `GOWORK=off` 的独立构建和验证。
