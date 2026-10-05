# XOps MCP

[简体中文](README.md)

XOps MCP is a self-hosted remote operations service. Manage SSH hosts, credentials and operation policies in the Web console, then use an MCP client to run commands, inspect files and transfer files.

It supports SQLite and PostgreSQL, HTTP and HTTPS, jump hosts, operation auditing, configuration import and backup/restore. Run one service instance with one administrator account.

## User documentation

Start with the [user guide](docs/en/user/README.md). For a new installation, see [container deployment](docs/en/user/container.md). If you already have the executable, see [running directly](docs/en/user/install.md).

- [Configuration, addresses and HTTPS](docs/en/user/configuration.md)
- [Web console and MCP clients](docs/en/user/console.md)
- [Importing hosts and credentials](docs/en/user/import.md)
- [PostgreSQL and database migration](docs/en/user/postgresql.md)
- [Backups and troubleshooting](docs/en/user/maintenance.md)

## Development documentation

The [development guide](docs/en/development/README.md) covers builds, tests, management APIs, architecture, dependencies and the roadmap.

## License

[MIT](LICENSE)
