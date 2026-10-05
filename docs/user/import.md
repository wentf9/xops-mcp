# 导入主机和凭据

[English](../en/user/import.md) · [使用指南](README.md)

导入前停止服务。以下命令在[直接运行](install.md)的 `xops-instance` 目录执行，`inventory.yaml` 为准备好的导入文件。

## 先预览，再应用

```sh
xops-mcp import --config server.yaml --file inventory.yaml --dry-run
```

预览不会修改数据。检查冲突、警告和 `expected_revision`。下面的 `0` 仅适用于预览显示该值的情况，实际使用时替换为本次预览的版本号：

```sh
xops-mcp import --config server.yaml --file inventory.yaml --apply --expected-revision 0
```

默认按名称合并，未写入文件的记录保留。使用 `--replace` 会删除文件中缺席的主机、身份和节点；凭据和标签仍按名称合并。更改名称会被当作另一条记录。

## 文件格式

这是一个可直接预览和导入的未启用节点：

```yaml
version: 1
tags: [office]
hosts:
  example:
    address: 192.0.2.10
    port: 22
    host_key: ""
identities:
  operator:
    user: operator
    credential: ""
nodes:
  example:
    host: example
    identity: operator
    aliases: [demo]
    tags: [office]
    disabled: true
    sudo_mode: none
```

导入后在控制台补齐凭据并核对主机公钥，再启用节点。缺少凭据或公钥时，导入会保持节点停用并提示原因。

| 项目 | 可用内容 |
| --- | --- |
| `hosts.<名称>` | `address`、`port`（默认 22）、`host_key` |
| `identities.<名称>` | `user`、`credential`（凭据名称） |
| `nodes.<名称>` | `host`、`identity`、`aliases`、`tags`、`proxy_jump`、`disabled`、`sudo_mode`、`privilege_credential` |
| `tags` | 标签名称列表，可以没有关联节点 |
| `credentials.<名称>` | `kind: password` 和 `password`；或 `kind: key`、`private_key`、可选 `passphrase` |
| `policy` | 操作策略设置 |

名称规则见[控制台指南](console.md)。`proxy_jump` 为节点名称或按顺序排列的逗号分隔名称，例如 `jump1,jump2`。提权方式支持 `none`、`root`、`sudo`、`sudoer`、`su`；`su` 需提供提权密码。

包含密码或私钥时，文件权限必须为 `0600`，预览和应用均需增加 `--include-secrets`。私钥填写内容，不填写文件路径。文件上限为 4 MiB；不要把真实凭据保存到公开目录。

## 容器中的导入

容器不会自动看到宿主机任意路径。假设 `inventory.yaml` 位于当前目录，停止服务后用标准输入导入：

```sh
docker compose -f examples/deployment/compose.yaml stop
docker compose -f examples/deployment/compose.yaml run --rm --no-deps -T xops-mcp import --config /etc/xops-mcp/server.yaml --file - --dry-run < inventory.yaml
```

确认预览后，使用对应版本号：

```sh
docker compose -f examples/deployment/compose.yaml run --rm --no-deps -T xops-mcp import --config /etc/xops-mcp/server.yaml --file - --apply --expected-revision 0 < inventory.yaml
docker compose -f examples/deployment/compose.yaml up -d --no-build
```

包含凭据时，两次导入都要加 `--include-secrets`。

## 从 xops-cli 导入

```sh
xops-mcp import --config server.yaml --format xops-cli --file exported-config.yaml --dry-run
```

支持主机、身份、节点、别名、标签、跳板和操作策略。源文件不会修改，导入节点保持停用。原来的密码库、私钥路径和 SSH agent 不会自动复制；在控制台重新填写凭据并核对公钥后再启用。

旧配置中直接填写的密码，只有添加 `--include-secrets` 才会导入。原配置中的自动提权方式会转为不提权并给出提示。检查预览后，添加 `--apply --expected-revision` 应用；不要仅凭预览成功就启动远程操作。
