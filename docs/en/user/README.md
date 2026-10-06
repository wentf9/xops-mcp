# User guide

[简体中文](../../user/README.md)

Run XOps MCP on a Linux server that can reach your SSH hosts. Use a browser to manage hosts and credentials, and an MCP client to perform operations. Only one service instance is supported, including with PostgreSQL.

## Getting started

1. Choose [container deployment](container.md) or [run an existing executable](install.md).
2. Open the management page and create the administrator using the setup code.
3. Add hosts, verify their public keys, configure credentials and enable nodes.
4. Create a credential on the [MCP Token](tokens.md) page, then connect an MCP client using the [console and client guide](console.md).

Management normally uses port `8081`; MCP uses port `8080`. The administrator password and MCP access credential are separate.

## Common tasks

| Task | Guide |
| --- | --- |
| Create, disable or revoke client tokens | [MCP Token management](tokens.md) |
| Change addresses, paths or HTTPS | [Configuration](configuration.md) |
| Manage nodes, jumps, tags and policies | [Console and clients](console.md) |
| Import inventory or xops-cli settings | [Import](import.md) |
| Use PostgreSQL or move databases | [Databases](postgresql.md) |
| Reset passwords, back up or recover tasks | [Maintenance](maintenance.md) |

Protect keys together with your data. See [maintenance](maintenance.md) for backup and restore procedures.
