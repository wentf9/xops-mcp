# XOps MCP

[简体中文](README.md)

A planned self-hosted MCP operations server with a web console, SQLite or PostgreSQL storage, and SSH/SFTP/MCP capabilities shared with [xops-cli](https://github.com/wentf9/xops-cli).

**Status: repository bootstrap and architecture planning.** This repository contains a pinned dependency, external-consumer compatibility tests, and CI. A runnable server, web console, and database implementation are not available yet.

## Repository relationship

```text
xops-mcp: independently released server product
  Web / management API / database / server adapters
                    │ pinned Go module version
                    ▼
xops-cli: CLI product and current shared-core source owner
  pkg/mcpserver / pkg/ssh / pkg/sftp
                    ▲
                    │ local package imports
  CLI / TUI / xops mcp
```

- `xops-mcp` depends on public Go packages from `xops-cli`; there is no reverse dependency.
- MCP tools, guardrails, transfer state machines, and SSH/SFTP fixes have one shared implementation.
- Web assets, management APIs, database models, migrations, and server credential adapters belong to `xops-mcp`.
- A third shared-core repository is deferred until a concrete need justifies it.
- Shared code is planned to move into an independently extractable upstream `core/` subtree, retaining old package paths as compatibility facades. Local upstream migration and independent extraction pass; the published pin has not yet been upgraded.
- Initial deployment is single-instance, with SQLite by default and PostgreSQL planned as an external database option.

## Design and delivery

- [Architecture and dependency decisions](docs/en/architecture.md)
- [Shared interfaces and server integration](docs/en/interface-decoupling.md)
- [Roadmap and acceptance criteria](docs/en/roadmap.md)
- [Dependency baseline and verification scope](docs/reuse-baseline.md)
- [Contributor instructions](AGENTS.md)

## Validate the bootstrap

Go 1.26+ and golangci-lint v2 are required. Tests use a temporary local HTTP server and synthetic inventory, without connecting to real SSH hosts or loading personal XOps/OpenSSH configuration.

```sh
go build ./...
go test ./...
golangci-lint config verify
golangci-lint run ./...
go test -race -timeout=120s ./...
```

The build currently checks compatibility packages and produces no server executable. Protocol tests cover external-module composition, HTTP authentication, MCP initialization, tool discovery, inventory lookup, and cleanup. They do not establish database support, live inventory updates, or real SSH/SFTP transfer behavior.

## License

[MIT](LICENSE), matching the upstream project.


New core interfaces have a separate isolated consumer probe: `python3 scripts/check_core_consumer.py --upstream /path/to/xops-cli`. Local preview covers real SSH/SFTP, dynamic admission and cleanup in a temporary module. Published acceptance uses `--version EXACT_VERSION`. See [integration status](docs/en/interface-decoupling.md).
