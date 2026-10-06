# MCP Token management

[简体中文](../../user/tokens.md) · [User guide](README.md)

Open **MCP Token** after signing in. Multiple tokens can work at the same time. Create a separate token for each client and name it after its device or purpose.

## Create and connect

1. Choose **New Token**, enter a name, enable it and optionally set an expiry. Leaving expiry empty means it does not expire.
2. Copy the complete token and save it in that MCP client's credential configuration.
3. Set the server address using the [client connection guide](console.md#connect-an-mcp-client).

The complete token is shown only once after creation. The list shows its name, prefix, status and dates; it cannot reveal the original secret. If lost, create a new token and revoke the unused one. On ordinary HTTP pages where automatic copying is unavailable, select the text and use your copy shortcut.

## Disable, enable and revoke

**Edit** changes the name, expiry and enabled state. A disabled token can be enabled again. **Revoke** permanently retires the credential. Expired tokens cannot authenticate; change their expiry or create a new token.

Changes apply to subsequent MCP requests immediately without a restart. Already-started operations are not guaranteed to stop. Previously issued short-lived file-transfer credentials retain their original validity and authorization. Different clients cannot share MCP sessions or inspect or cancel each other's file tasks.

All tokens currently use the same nodes and operation policies; assigning separate node permissions to each client is not supported. Tokens do not grant console access. Creation, edits and revocations are audited.

## Provision an initial token

To connect MCP before opening the console, generate a file with `keygen --out mcp.token` and configure `mcp_token_file: mcp.token`. Use mode `0600`; relative paths resolve from the configuration file's directory.

When the service first reads that credential, it registers it as `initial-token`. Manage it in the console like any other token. Restarting never enables a disabled or revoked credential. After registration, you may remove both the configuration entry and file; clients continue using the original token. Omit the configuration entry when initial provisioning is unnecessary.

Database backups retain token expiry, status and verification records. Each client must keep its complete token safely.
