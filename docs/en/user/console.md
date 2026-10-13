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
| Access credential | Complete token copied when creating it on the [MCP Token](tokens.md) page |

Clients requiring an explicit header use:

```text
Authorization: Bearer <token copied at creation>
```

Use a separate token for each client. Do not use the administrator password, setup code or management-signing key, and do not connect to the management page's port/path.

First list nodes, then choose a node for commands. Available operations include command execution, text files, directory/file management, uploads and downloads. Use the tools shown by the client.

Local transfer files belong to the client device. Archive directories before transferring them. The single-file limit is 10 GiB. After downloading, confirm that the client saved the file. If a task reports an unknown result after interruption or restart, follow [maintenance](maintenance.md#unknown-file-task-results) instead of blindly repeating an overwrite.

## Command execution environment

In the node editor, click **Auto probe** to get recommended settings, or set the interpreter, launch shell, and login mode manually. Supported interpreters are `server`, `bash`, `sh`, `zsh`, `fish`, `powershell`, `pwsh`, and `cmd`. Automatic probing runs fixed, bounded diagnostic commands when a command needs the environment; it does not save settings. Connection tests still check SSH login only. Explicit `server` passes the original command to the server. Privilege execution requires a supported Bash, sh, zsh, or fish environment.

MCP `xops_ssh_run` accepts an optional `execution` override using `interpreter`, `launchDialect`, and `login`. Responses include `outcome` (`not_started`, `completed`, or `unknown`), optional `exitCode`/`signal`, `planDigest`, and `truncated`. A nonzero exit status is a failed command; do not automatically replay an `unknown` result. File copy and removal use SFTP, including on Windows targets, without requiring a POSIX shell.

When upgrading, stop the service, back up the deployment and run `migrate` with the new executable before restarting. Existing nodes start with automatic probing. If an SSH endpoint restricts diagnostic commands, set its execution interpreter to `server` to preserve passthrough.

### Probe and save Shell settings

In the node editor, select a host, login identity and any jumps, then click **Auto probe**. The host must already have a pinned public key and the identity must have a login credential. Probing works for new and disabled target nodes; transit nodes must be enabled. The result lists installed Shells and selects the recommended interpreter, launch shell and login mode. Review or adjust the choices and click **Save** to persist them. Closing the editor discards the recommendation. Probe errors preserve your current choices; changing connection settings cancels the old result and requires another probe.

While a node is being saved, its fields and probe button are locked to prevent duplicate submissions; failed saves restore editing. Changing the interpreter clears incompatible launch/login values. Interpreters that require inherited login mode disable the login selector.
