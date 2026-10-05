# XOps MCP

[简体中文](README.md)

A self-hosted MCP operations server with SQLite or PostgreSQL inventory and encrypted credentials, sharing SSH/SFTP/MCP capabilities with [xops-cli](https://github.com/wentf9/xops-cli).

**Status: M4 PostgreSQL backend.** The embedded single-administrator console provides live inventory/credential management, host trust, policy and audit alongside SQLite, HTTP MCP and file transfers. The target is a single Linux instance, with PostgreSQL and encrypted offline database export/import/verification now available.

## Start the server

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

Open `http://127.0.0.1:8081/`, initialize the administrator using `admin.setup`, then manage inventory in the console. MCP clients use the separate `http://127.0.0.1:8080/mcp` listener with the separate `mcp.token`. Existing deployments retain their master key and data, migrate, and configure an administrator. See the [Web console guide](docs/en/web-console.md).

## Repository relationship

```text
xops-mcp: independently released server product
  Web / management API / database / server adapters
                    │ pinned Go module version
                    ▼
xops-cli: CLI product and current shared-core source owner
  core/mcp/runtime / core/ssh / core/sftp
                    ▲
                    │ local package imports
  CLI / TUI / xops mcp
```

- `xops-mcp` depends on public Go packages from `xops-cli`; there is no reverse dependency.
- MCP tools, guardrails, transfer state machines, and SSH/SFTP fixes have one shared implementation.
- Web assets, management APIs, database models, migrations, and server credential adapters belong to `xops-mcp`.
- A third shared-core repository is deferred until a concrete need justifies it.
- Shared implementations now live in an independently extractable upstream `core/` subtree, with direct CLI consumption and application-owned host adapters. This repository tests core directly using a fixed remote commit without local replacements.
- Initial deployment is single-instance, with SQLite by default and PostgreSQL as an optional external database.

## Design and delivery

- [Web console, API and deployment](docs/en/web-console.md)

- [Server deployment and inventory import](docs/en/server.md)
- [Administrator JWTs, HTTPS and password encryption](docs/en/admin-auth.md)
- [PostgreSQL and offline database migration](docs/en/postgresql.md)
- [Architecture and dependency decisions](docs/en/architecture.md)
- [Shared interfaces and server integration](docs/en/interface-decoupling.md)
- [Roadmap and acceptance criteria](docs/en/roadmap.md)
- [Dependency baseline and verification scope](docs/reuse-baseline.md)
- [Contributor instructions](AGENTS.md)

## Development checks

Go 1.26+ and golangci-lint v2 are required. Tests use temporary local HTTP and SSH/SFTP fixtures with synthetic inventory and credentials. They neither load personal XOps/OpenSSH configuration nor connect to deployed hosts.

```sh
GOWORK=off go build ./...
GOWORK=off go test ./...
golangci-lint config verify
golangci-lint run ./...
go test -race -timeout=120s ./...
```

Tests cover SQLite/PostgreSQL migrations/transactions, cross-backend archive equivalence, database ownership and pool cleanup, encrypted credentials and binding checks, atomic publication and uncertain-commit recovery, HTTP authentication, real local SSH/SFTP, encrypted private keys and ProxyJump, journal restart/unknown locks and shutdown. `internal/dependencycheck` checks every production/test graph for Linux/Windows/macOS; graph checks are not native Windows/macOS execution evidence.

Validate the fixed version independently with `python3 scripts/check_core_consumer.py --version v0.13.1-0.20261003125647-0d4bd2fb866c`. Local development can still use `--upstream /path/to/xops-cli`; replacements exist only in a disposable module. See [integration status](docs/en/interface-decoupling.md).

## License

[MIT](LICENSE), matching the upstream project.
