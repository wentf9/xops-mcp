# MCP Token management

[简体中文](../../user/tokens.md) · [User guide](README.md)

Open **MCP Token** after signing in. Multiple tokens can work at the same time. Create a separate token for each client and name it after its device or purpose.

## Create and connect

1. Choose **New Token**, enter a name, select its allowed nodes, enable it and optionally set an expiry. Leaving expiry empty means it does not expire.
2. Copy the complete token and save it in that MCP client's credential configuration.
3. Set the server address using the [client connection guide](console.md#connect-an-mcp-client).

The complete token is shown only once after creation. The list shows its name, prefix, node scope, status and dates; it cannot reveal the original secret. If lost, create a new token and revoke the unused one. On ordinary HTTP pages where automatic copying is unavailable, select the text and use your copy shortcut.

## Bind allowed nodes

**Allowed nodes** offers two scopes:

- **All nodes** allows every current node and automatically includes future nodes. Tokens created before upgrading retain this scope.
- **Selected nodes** allows only checked nodes. Search by name, alias or node ID to select multiple nodes. Selecting none grants no node access.

Use **Edit** to change a token's scope at any time. Renaming a node or changing its aliases preserves the binding. Deleting and recreating a node with the same name requires a new selection. The editor retains deleted bindings with a label; uncheck them to remove them. Disabled nodes remain subject to their own disablement rules.

Clients see only allowed nodes in their node list. Targets selected by name, alias or ID must also fall within that token's scope, and every target in a batch needs authorization. Access to a node permits transit through its configured jump hosts without granting permission to operate directly on those jump nodes. All tokens remain subject to the shared operation policy.

## Disable, enable and revoke

**Edit** changes the name, node scope, expiry and enabled state. A disabled token can be enabled again. **Revoke** permanently retires the credential. Expired tokens cannot authenticate; change their expiry or create a new token.

Changes apply to subsequent MCP requests immediately without a restart. Transfers recheck token state and node scope when transfer content starts and when an upload commits, even if a short-lived transfer credential was already issued. Already-admitted operations are not guaranteed to stop. A still-valid token may inspect or cancel its earlier file tasks after a target leaves its scope; looking up an existing result does not repeat remote work. Different clients cannot share MCP sessions or inspect or cancel each other's file tasks.

Tokens do not grant console access. Creation, edits and revocations are audited.

## Provision an initial token

To connect MCP before opening the console, generate a file with `keygen --out mcp.token` and configure `mcp_token_file: mcp.token`. Use mode `0600`; relative paths resolve from the configuration file's directory.

When the service first reads that credential, it registers it as `initial-token`. Manage it in the console like any other token. Restarting never enables a disabled or revoked credential. After registration, you may remove both the configuration entry and file; clients continue using the original token. Omit the configuration entry when initial provisioning is unnecessary.

Database backups retain token node scopes, expiry, status and verification records. Each client must keep its complete token safely.
