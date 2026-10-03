# XOps MCP

[简体中文](README.md)

A planned self-hosted MCP operations server with a web console, SQLite or PostgreSQL storage, and SSH/SFTP/MCP capabilities shared with [xops-cli](https://github.com/wentf9/xops-cli).

**Status: shared-core integration and consumer validation.** The repository pins a remotely downloadable core version, with core-consumer and legacy-facade contracts in normal CI. A runnable server, web console, and database implementation are not available yet.

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
- Shared implementations now live in an independently extractable upstream `core/` subtree, with old `pkg/*` paths retained as facades. This repository tests core directly using a fixed remote commit without local replacements.
- Initial deployment is single-instance, with SQLite by default and PostgreSQL planned as an external database option.

## Design and delivery

- [Architecture and dependency decisions](docs/en/architecture.md)
- [Shared interfaces and server integration](docs/en/interface-decoupling.md)
- [Roadmap and acceptance criteria](docs/en/roadmap.md)
- [Dependency baseline and verification scope](docs/reuse-baseline.md)
- [Contributor instructions](AGENTS.md)

## Validate the bootstrap

Go 1.26+ and golangci-lint v2 are required. Tests use temporary local HTTP and SSH/SFTP fixtures with synthetic inventory and credentials. They neither load personal XOps/OpenSSH configuration nor connect to deployed hosts.

```sh
GOWORK=off go build ./...
GOWORK=off go test ./...
golangci-lint config verify
golangci-lint run ./...
go test -race -timeout=120s ./...
```

The build checks consumer and compatibility packages and produces no server executable. Tests cover HTTP authentication, MCP initialization and tool sets, SSH commands, binary SFTP upload/download, dynamic disablement through an in-memory coordinator, and cleanup. `internal/dependencycheck` separately checks the core consumer graph for Linux/Windows/macOS; import-graph checks are not native execution evidence. Database and Web features remain unimplemented.

Validate the fixed version independently with `python3 scripts/check_core_consumer.py --version v0.13.1-0.20261003020948-b8ee0ed9f1a0`. Local development can still use `--upstream /path/to/xops-cli`; replacements exist only in a disposable module. See [integration status](docs/en/interface-decoupling.md).

## License

[MIT](LICENSE), matching the upstream project.
