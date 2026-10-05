# Web console and MCP clients

[简体中文](../../user/console.md) · [User guide](README.md)

## Administrator login

At first use, enter an administrator username, password and setup code. Passwords require 12–72 UTF-8 bytes, without line breaks or NUL. Common Chinese characters usually use 3 bytes each. Confirmation must match exactly.

Login lasts 15 minutes and survives reloads in the same tab. New tabs require login. Logout clears the current tab. After changing a password, the old password cannot log in, but other already-authenticated pages remain valid until their original expiry. Restart does not shorten valid logins.

Change your password on the account page. For a forgotten password, use [offline reset](maintenance.md).

## Add usable nodes

1. **Hosts and trust**: add the SSH address and port, then open the host-key dialog. Fetch the key from the host or enter one obtained through a trusted channel such as its console.
2. **Verify the key**: independently check its fingerprint before confirming. Unconfirmed keys are not automatically trusted. Each preview lasts two minutes; repeat it after changing the address or key.
3. **Credentials**: add the remote password or SSH private key, including its passphrase when needed. Secrets are not shown again. Leaving secret fields empty while editing keeps existing contents.
4. **Login identities**: specify the SSH username and associated credential.
5. **Nodes**: choose host, identity and tags, configure jumps or privilege elevation if needed, and enable the node. The connection test verifies SSH login without executing a command.

For hosts reachable only through a jump, enter an independently verified host key before choosing ordered jumps. Jumps are ordered from this service to the target. Disabling a jump also prevents connections to dependent nodes.

Resource names and node aliases accept Unicode letters/writing, decimal digits, underscores and hyphens, up to 256 UTF-8 bytes. Spaces, other punctuation, emoji and invisible characters are rejected. Names must be unique within their resource type. Enter one node alias per line.

## Tags, policies and audit

Tags can be created, renamed and deleted independently. Deleting a tag removes associations, not nodes. Referenced nodes, hosts, identities and credentials must be detached before deletion.

Operation policies control which risks require confirmation, blocked command patterns and protected paths. Operations requiring confirmation are denied by default when the client cannot complete it.

The active-operations page shows current work. Filter audit records by node and outcome. Audit does not retain raw commands, file paths or passwords. If another page saves first, refresh after the conflict notice and edit again.

Address edits do not redirect work already in progress. Disabling nodes, updating credentials or revoking host keys may interrupt related work; verify any remote file write already underway. If a saved configuration is waiting to take effect, use the retry action instead of submitting the same edit again.

## Connect an MCP client

Configure a client supporting Streamable HTTP:

| Item | Value |
| --- | --- |
| Transport | Streamable HTTP |
| Service URL | For example, `http://127.0.0.1:8080/mcp` |
| Access credential | Complete contents of `mcp.token`, without the final newline |

Clients requiring an explicit header use:

```text
Authorization: Bearer <contents of mcp.token>
```

For containers, read it with `sudo cat examples/deployment/.secrets/mcp.token`. Do not use the administrator password, setup code or management-signing key, and do not connect to the management page's port/path.

First list nodes, then choose a node for commands. Available operations include command execution, text files, directory/file management, uploads and downloads. Use the tools shown by the client.

Local transfer files belong to the client device. Archive directories before transferring them. The single-file limit is 10 GiB. After downloading, confirm that the client saved the file. If a task reports an unknown result after interruption or restart, follow [maintenance](maintenance.md#unknown-file-task-results) instead of blindly repeating an overwrite.
