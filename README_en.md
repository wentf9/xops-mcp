# XOps MCP

[简体中文](README.md)

A self-hosted MCP operations server with SQLite inventory and encrypted credentials, sharing SSH/SFTP/MCP capabilities with [xops-cli](https://github.com/wentf9/xops-cli).

**Status: M2 standalone HTTP MCP server.** SQLite migrations, inventory import previews and revision checks, encrypted credentials, pinned host trust, file transfers and offline recovery are available. The initial target is a single Linux instance; Web management and PostgreSQL remain later milestones.

## Start the server

```sh
GOWORK=off go build -o bin/xops-mcp ./cmd/xops-mcp
mkdir -m 700 -p .local
cp examples/server.yaml .local/server.yaml
bin/xops-mcp keygen --out .local/master.key
bin/xops-mcp keygen --out .local/mcp.token
bin/xops-mcp migrate --config .local/server.yaml
bin/xops-mcp serve --config .local/server.yaml
```

The default endpoint is `http://127.0.0.1:8080/mcp`. Clients use the contents of `mcp.token` as their Bearer token. An empty database has no executable nodes; stop the server and follow the [deployment and import guide](docs/en/server.md) to add inventory, credentials and verified host keys.

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
- Initial deployment is single-instance, with SQLite by default and PostgreSQL planned as an external database option.

## Design and delivery

- [SQLite deployment and inventory import](docs/en/server.md)
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

Tests cover SQLite migrations/transactions, encrypted credentials and binding checks, atomic publication and uncertain-commit recovery, HTTP authentication, real local SSH/SFTP, encrypted private keys and ProxyJump, journal restart/unknown locks and shutdown. `internal/dependencycheck` checks every production/test graph for Linux/Windows/macOS; graph checks are not native Windows/macOS execution evidence.

Validate the fixed version independently with `python3 scripts/check_core_consumer.py --version v0.13.1-0.20261003125647-0d4bd2fb866c`. Local development can still use `--upstream /path/to/xops-cli`; replacements exist only in a disposable module. See [integration status](docs/en/interface-decoupling.md).

## License

[MIT](LICENSE), matching the upstream project.
