import { chromium } from "playwright";
import { spawn, execFileSync } from "node:child_process";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import assert from "node:assert/strict";

const useTLS = !process.argv.includes("--http");
const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
const work = await mkdtemp(resolve(tmpdir(), "xops-browser-"));
const artifacts = resolve(root, "web/test-results");
await mkdir(artifacts, { recursive: true });
let fixture, browser, page;
try {
  execFileSync(
    "go",
    [
      "build",
      "-tags=webfixture",
      "-o",
      resolve(work, "fixture"),
      "./scripts/webfixture",
    ],
    {
      cwd: root,
      env: { ...process.env, GOWORK: process.env.XOPS_TEST_GOWORK || "off" },
      stdio: "pipe",
      timeout: 120000,
    },
  );
  fixture = spawn(resolve(work, "fixture"), [], {
    cwd: root,
    env: { ...process.env, XOPS_TEST_WEB_TLS: useTLS ? "1" : "0" },
    stdio: ["ignore", "pipe", "pipe"],
  });
  let diagnostics = "";
  fixture.stderr.on("data", (chunk) => (diagnostics += chunk));
  const info = await new Promise((accept, reject) => {
    let text = "";
    const timer = setTimeout(
      () => reject(new Error("fixture readiness timeout")),
      15000,
    );
    fixture.once("exit", (code) => {
      clearTimeout(timer);
      reject(new Error(`fixture exited ${code}: ${diagnostics}`));
    });
    fixture.stdout.on("data", (chunk) => {
      text += chunk;
      if (text.includes("\n")) {
        clearTimeout(timer);
        accept(JSON.parse(text.split("\n")[0]));
      }
    });
  });
  browser = await chromium.launch({
    headless: true,
    ...(process.env.XOPS_TEST_CHROME
      ? { executablePath: process.env.XOPS_TEST_CHROME }
      : {}),
    args: ["--no-sandbox", "--no-proxy-server", "--host-resolver-rules=MAP xops-http.test 127.0.0.1"],
  });
  const context = await browser.newContext({
    viewport: { width: 1440, height: 1000 },
    // The fixture generates its own isolated certificate, never a deployed key.
    ignoreHTTPSErrors: true,
  });
  const request = (method, raw, options = {}) => {
    const target = new URL(raw), host = target.host;
    if (target.hostname === "xops-http.test") target.hostname = "127.0.0.1";
    return context.request[method](target.href, { ...options, headers: { ...options.headers, Host: host } });
  };
  page = await context.newPage();
  const errors = [];
  const encryptedRequests = [];
  page.on("request", request => {
    if (/\/auth\/(login|setup|password)$/.test(new URL(request.url()).pathname) && ["POST", "PUT"].includes(request.method())) {
      const body = request.postDataJSON();
      assert.deepEqual(Object.keys(body), ["ciphertext"]);
      assert.equal(body.ciphertext.split(".").length, 5);
      encryptedRequests.push(request.url());
    }
  });
  page.on("pageerror", (error) => errors.push(error.message));
  await page.route("https://external.test/", (route) =>
    route.fulfill({
      contentType: "text/html",
      body: `<a href="${info.url}/">Open XOps console</a>`,
    }),
  );
  const openFromExternalLink = async () => {
    await page.goto("https://external.test/");
    const [response] = await Promise.all([
      page.waitForResponse((r) => r.url() === info.url + "/"),
      page.getByRole("link", { name: "Open XOps console" }).click(),
    ]);
    const headers = await response.allHeaders();
    // Fetch Metadata is omitted by browsers for ordinary HTTP origins.
    if (useTLS) {
      assert.equal(headers["x-fixture-fetch-site"], "cross-site");
      assert.equal(headers["x-fixture-fetch-mode"], "navigate");
      assert.equal(headers["x-fixture-fetch-dest"], "document");
    }
    assert.equal(response.status(), 200);
  };
  await openFromExternalLink();
  assert.equal(await page.evaluate(() => isSecureContext), useTLS);
  assert.equal(await page.evaluate(() => !!globalThis.crypto?.subtle), useTLS);
  await page.getByRole("heading", { name: "初始化管理员" }).waitFor();
  await page.screenshot({
    path: resolve(artifacts, "setup.png"),
    fullPage: true,
  });
  await page.getByLabel("用户名", { exact: true }).fill(info.username);
  const initialPassword = "管理员初密码";
  await page.getByLabel("密码", { exact: true }).fill(initialPassword);
  await page.getByLabel("确认密码", { exact: true }).fill(initialPassword);
  await page.getByLabel("初始化凭据", { exact: true }).fill(info.setupToken);
  await page.getByRole("button", { name: "创建管理员", exact: true }).click();
  await page.getByRole("heading", { name: "节点总览", exact: true }).waitFor();
  const token = async () => page.evaluate(() => sessionStorage.getItem("xops.admin.token:" + new URL("api/v1/", document.baseURI).href));
  assert.equal((await token()).split(".").length, 3);
  assert.equal((await context.cookies(info.url)).filter(cookie => cookie.name.startsWith("xops_admin_")).length, 0);
  for (const url of [
    info.mcpURL + "/console/api/v1/inventory",
    info.mcpURL + "/console/",
    info.url + "/mcp",
  ]) {
    const response = await request("get", url);
    assert.equal(response.status(), 404);
    await response.dispose();
  }
  const nav = async (label, p = page) => {
    await p
      .locator(".sidebar")
      .getByRole("link", { name: label, exact: false })
      .click();
    await p.getByRole("heading", { name: label, exact: true }).waitFor();
  };
  const form = () => page.locator("dialog[open]");
  const save = async () => {
    await form().getByRole("button", { name: "保存", exact: true }).click();
    await page.locator("dialog[open]").waitFor({ state: "hidden" });
  };
  await nav("MCP Token");
  await page.getByRole("button", { name: "新建Token", exact: false }).click();
  await form().getByLabel("名称", { exact: true }).fill("browser-client");
  await form().getByLabel("到期时间（留空表示永不过期）").fill("2099-10-05T12:34:56");
  await form().getByRole("button", { name: "创建 Token", exact: true }).click();
  await form().getByRole("heading", { name: "Token 已创建" }).waitFor();
  const mcpSecret = await form().getByLabel("Token", { exact: true }).inputValue();
  assert.match(mcpSecret, /^xmcp_[A-Z2-7]+$/);
  await form().getByRole("button", { name: "我已保存" }).click();
  await page.locator("#created-token").waitFor({ state: "detached" });
  await page.getByRole("row").filter({ hasText: "browser-client" }).waitFor();
  assert.equal(await page.locator("#created-token").count(), 0);
  const metadata = await request("get", info.url + "api/v1/mcp-tokens", { headers: { Authorization: "Bearer " + await token() } });
  const metadataText = await metadata.text();
  assert(!metadataText.includes(mcpSecret));
  assert(!metadataText.includes("Digest"));
  await metadata.dispose();
  const tokenRow = () => page.getByRole("row").filter({ hasText: "browser-client" });
  await page.screenshot({ path: resolve(artifacts, `mcp-tokens-${useTLS ? "https" : "http"}.png`), fullPage: true });
  await tokenRow().getByRole("button", { name: "编辑", exact: true }).click();
  assert.equal(await form().getByLabel("到期时间（留空表示永不过期）").inputValue(), "2099-10-05T12:34:56");
  await form().getByLabel("启用 Token", { exact: true }).uncheck();
  await save();
  await tokenRow().getByText("已停用", { exact: true }).waitFor();
  const rejected = await request("get", info.mcpURL + "/mcp", { headers: { Authorization: "Bearer " + mcpSecret } });
  assert.equal(rejected.status(), 401);
  await rejected.dispose();
  await tokenRow().getByRole("button", { name: "编辑", exact: true }).click();
  await form().getByLabel("启用 Token", { exact: true }).check();
  await save();
  await tokenRow().getByText("已启用", { exact: true }).waitFor();
  await tokenRow().getByRole("button", { name: "吊销", exact: true }).click();
  await form().getByRole("button", { name: "确认", exact: true }).click();
  await tokenRow().getByText("已吊销", { exact: true }).waitFor();
  await page.reload();
  await tokenRow().getByText("已吊销", { exact: true }).waitFor();
  await nav("主机与信任");
  await page.getByRole("button", { name: "新建主机", exact: false }).click();
  await form().getByLabel("名称", { exact: true }).fill("browser-host");
  const [host, port] = info.sshAddress.split(":");
  await form().getByLabel("SSH 地址", { exact: true }).fill(host);
  await form().getByLabel("端口", { exact: true }).fill(port);
  await save();
  await page.getByRole("button", { name: "配置公钥", exact: true }).click();
  await form()
    .getByRole("button", { name: "连接并获取公钥", exact: true })
    .click();
  await form().locator(".key-preview strong").waitFor();
  await form().getByLabel("已通过独立渠道核对指纹").check();
  await form().getByRole("button", { name: "确认并固定", exact: true }).click();
  await page.locator("dialog[open]").waitFor({ state: "hidden" });
  await page.getByText("已固定", { exact: true }).waitFor();
  // Provision a host that cannot be resolved from this deployment using only
  // independently supplied public material. Preview must not create trust.
  await page.getByRole("button", { name: "新建主机", exact: false }).click();
  await form().getByLabel("名称", { exact: true }).fill("jump-only-host");
  await form()
    .getByLabel("SSH 地址", { exact: true })
    .fill("jump-only.invalid");
  await save();
  const offlineRow = page
    .getByRole("row")
    .filter({ hasText: "jump-only-host" });
  await offlineRow
    .getByRole("button", { name: "配置公钥", exact: true })
    .click();
  await form().getByLabel("公钥来源").selectOption("manual");
  await form().getByLabel("主机公钥", { exact: true }).fill("invalid-key");
  await form().getByRole("button", { name: "预览公钥指纹" }).click();
  await form()
    .getByRole("alert")
    .getByText("请提供单个 SSH 主机公钥", { exact: false })
    .waitFor();
  assert.equal(
    await form().getByRole("button", { name: "确认并固定" }).isDisabled(),
    true,
  );
  await form().getByLabel("主机公钥", { exact: true }).fill(info.sshHostKey);
  await form().getByRole("button", { name: "预览公钥指纹" }).click();
  await form().getByText(info.sshFingerprint, { exact: true }).waitFor();
  assert.equal(
    await form().getByRole("button", { name: "确认并固定" }).isDisabled(),
    true,
  );
  await page.screenshot({
    path: resolve(artifacts, "independent-key-preview.png"),
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  assert.equal(
    await form().evaluate(
      (element) => element.scrollWidth > element.clientWidth,
    ),
    false,
  );
  await page.screenshot({
    path: resolve(artifacts, "independent-key-mobile.png"),
    fullPage: true,
  });
  await page.setViewportSize({ width: 1440, height: 1000 });
  // Changing either the source or the pasted key clears earlier approval.
  await form().getByLabel("已通过独立渠道核对指纹").check();
  await form().getByLabel("公钥来源").selectOption("network");
  assert.equal(
    await form().getByRole("button", { name: "确认并固定" }).isDisabled(),
    true,
  );
  await form().getByLabel("公钥来源").selectOption("manual");
  await form().getByRole("button", { name: "预览公钥指纹" }).click();
  await form().getByLabel("已通过独立渠道核对指纹").check();
  await form()
    .getByLabel("主机公钥", { exact: true })
    .fill(info.sshHostKey.trim() + " verified");
  assert.equal(
    await form().getByRole("button", { name: "确认并固定" }).isDisabled(),
    true,
  );
  assert.equal(await form().locator(".key-preview").count(), 0);
  await form().getByRole("button", { name: "预览公钥指纹" }).click();
  await form().getByLabel("已通过独立渠道核对指纹").check();
  await form().getByRole("button", { name: "取消", exact: true }).click();
  await offlineRow.getByText("待确认", { exact: true }).waitFor();
  await offlineRow
    .getByRole("button", { name: "配置公钥", exact: true })
    .click();
  await form().getByLabel("公钥来源").selectOption("manual");
  await form().getByLabel("主机公钥", { exact: true }).fill(info.sshHostKey);
  await form().getByRole("button", { name: "预览公钥指纹" }).click();
  await form().getByLabel("已通过独立渠道核对指纹").check();
  await form().getByRole("button", { name: "确认并固定" }).click();
  await page.locator("dialog[open]").waitFor({ state: "hidden" });
  await offlineRow.getByText("已固定", { exact: true }).waitFor();
  await nav("凭据");
  await page.getByRole("button", { name: "新建凭据", exact: false }).click();
  await form().getByLabel("名称", { exact: true }).fill("browser-password");
  await form().getByLabel("密码", { exact: true }).fill(info.sshPassword);
  await save();
  assert.equal(
    (await page.locator("body").innerText()).includes(info.sshPassword),
    false,
  );
  await nav("登录身份");
  await page.getByRole("button", { name: "新建身份", exact: false }).click();
  await form().getByLabel("名称", { exact: true }).fill("browser-identity");
  await form().getByLabel("SSH 用户名", { exact: true }).fill("fixture");
  await form()
    .getByLabel("登录凭据", { exact: true })
    .selectOption({ label: "browser-password" });
  await save();
  await nav("标签");
  for (const tag of ["production", "service", "unused"]) {
    await page.getByRole("button", { name: "新建标签", exact: false }).click();
    await form().getByLabel("名称", { exact: true }).fill(tag);
    await save();
    await page.getByRole("heading", { name: tag, exact: true }).waitFor();
  }
  assert.equal(await page.locator(".tag-card").count(), 3);
  await nav("节点总览");
  await page.getByRole("button", { name: "新建节点", exact: false }).click();
  await form().getByLabel("名称", { exact: true }).fill("browser-node");
  await form()
    .getByLabel("主机", { exact: true })
    .selectOption({ label: "browser-host" });
  await form()
    .getByLabel("登录身份", { exact: true })
    .selectOption({ label: "browser-identity" });
  const browserAliases = ["运维_生产", "e\u0301cole", "हिन्दी"];
  await form()
    .getByLabel("别名", { exact: true })
    .fill(browserAliases.join("\n"));
  await form().getByLabel("production", { exact: true }).check();
  await form().getByLabel("service", { exact: true }).check();
  await form().getByLabel("启用节点", { exact: true }).check();
  await save();
  await page.getByRole("button", { name: "测试连接", exact: true }).click();
  await page
    .locator("#notices")
    .getByText("SSH 连接成功", { exact: false })
    .waitFor();
  await page.screenshot({
    path: resolve(artifacts, "nodes.png"),
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({
    path: resolve(artifacts, "mobile-populated.png"),
    fullPage: true,
  });
  assert.equal(
    await page.evaluate(
      () => document.documentElement.scrollWidth > innerWidth,
    ),
    false,
  );
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.getByRole("button", { name: "编辑", exact: true }).click();
  await form().getByLabel("名称", { exact: true }).fill("stale-name");
  const other = await context.newPage();
  await other.goto(info.url);
  // A new tab has its own sessionStorage and authenticates explicitly.
  await other.getByLabel("用户名", { exact: true }).fill(info.username);
  await other.getByLabel("密码", { exact: true }).fill(initialPassword);
  await other.getByRole("button", { name: "登录控制台", exact: false }).click();
  await other.getByRole("heading", { name: "节点总览", exact: true }).waitFor();
  await other.getByRole("button", { name: "编辑", exact: true }).click();
  await other
    .locator("dialog[open]")
    .getByLabel("名称", { exact: true })
    .fill("newer-name");
  await other
    .locator("dialog[open]")
    .getByRole("button", { name: "保存", exact: true })
    .click();
  await other.locator("dialog[open]").waitFor({ state: "hidden" });
  await form().getByRole("button", { name: "保存", exact: true }).click();
  await form()
    .locator(".form-error")
    .getByText("配置已被其他会话修改", { exact: false })
    .waitFor();
  await form().getByRole("button", { name: "取消", exact: true }).click();
  await page.getByRole("button", { name: "刷新", exact: false }).click();
  await page
    .getByRole("button", { name: "newer-name", exact: false })
    .waitFor();
  await other.close();
  await page.getByRole("button", { name: "编辑", exact: true }).click();
  const literal = '<img src=x onerror="window.xopsInjected=true">';
  await form().getByLabel("名称", { exact: true }).fill(literal);
  assert.equal(
    await form()
      .getByLabel("名称", { exact: true })
      .evaluate((input) => input.validity.valid),
    false,
  );
  await form().getByRole("button", { name: "保存", exact: true }).click();
  assert.equal(await form().isVisible(), true);
  assert.equal(await page.evaluate(() => window.xopsInjected), undefined);
  const unicodeName = "生产_école-хост-हिन्दी";
  await form().getByLabel("名称", { exact: true }).fill(unicodeName);
  await save();
  assert.ok((await page.locator("body").innerText()).includes(unicodeName));
  const inventoryResponse = await request("get",
    info.url + "/api/v1/inventory",
    { headers: { Authorization: "Bearer " + await token() } },
  );
  assert.equal(inventoryResponse.status(), 200);
  const inventory = await inventoryResponse.json();
  await inventoryResponse.dispose();
  assert.deepEqual(
    inventory.nodes.find((node) => node.name === unicodeName).aliases,
    [...browserAliases].sort(),
  );
  await nav("标签");
  await page
    .locator(".tag-card")
    .filter({
      has: page.getByRole("heading", { name: "production", exact: true }),
    })
    .getByRole("button", { name: "重命名" })
    .click();
  await form().getByLabel("新名称", { exact: true }).fill("prod");
  await save();
  await page.getByRole("heading", { name: "prod", exact: true }).waitFor();
  const unused = page.locator(".tag-card").filter({
    has: page.getByRole("heading", { name: "unused", exact: true }),
  });
  await unused.getByText("0 个节点", { exact: true }).waitFor();
  await unused.getByRole("button", { name: "重命名" }).click();
  await form().getByLabel("新名称", { exact: true }).fill("spare");
  await save();
  await page.getByRole("heading", { name: "spare", exact: true }).waitFor();
  await page
    .locator(".tag-card")
    .filter({
      has: page.getByRole("heading", { name: "service", exact: true }),
    })
    .getByRole("button", { name: "删除标签" })
    .click();
  await form().getByRole("button", { name: "确认", exact: true }).click();
  await form().waitFor({ state: "hidden" });
  await nav("节点总览");
  await page.locator(".chip").getByText("prod", { exact: true }).waitFor();
  assert.equal(
    await page.locator(".chip").getByText("service", { exact: true }).count(),
    0,
  );
  // Reproduce an imported policy whose approval_threshold was omitted.
  const policyInventoryResponse = await request("get",
    info.url + "/api/v1/inventory",
    { headers: { Authorization: "Bearer " + await token() } },
  );
  assert.equal(policyInventoryResponse.status(), 200);
  const policyInventory = await policyInventoryResponse.json();
  const sessionResponse = await request("get",
    info.url + "/api/v1/auth/session",
    { headers: { Authorization: "Bearer " + await token() } },
  );
  assert.equal(sessionResponse.status(), 200);
  assert.equal((await sessionResponse.json()).authenticated, true);
  await sessionResponse.dispose();
  const unsetPolicyResponse = await request("put",
    info.url + "/api/v1/policy",
    {
      headers: {
        Origin: new URL(info.url).origin,
        Authorization: "Bearer " + await token(),
        "If-Match": policyInventoryResponse.headers().etag,
      },
      data: {
        ...policyInventory.policy,
        enabled: true,
        approvalThreshold: "",
        noElicitFallback: "deny",
      },
    },
  );
  assert.equal(unsetPolicyResponse.status(), 200);
  await unsetPolicyResponse.dispose();
  await policyInventoryResponse.dispose();
  await page.reload();
  await nav("操作策略");
  assert.equal(
    await page.getByLabel("开始确认的风险级别").inputValue(),
    "dangerous",
  );
  await page.getByLabel("额外拦截的命令模式").fill("blocked-browser-test*");
  await page.getByRole("button", { name: "保存策略" }).click();
  await page
    .locator("#notices")
    .getByText("策略已更新", { exact: true })
    .waitFor();
  const savedPolicyResponse = await request("get",
    info.url + "/api/v1/inventory",
    { headers: { Authorization: "Bearer " + await token() } },
  );
  assert.equal(savedPolicyResponse.status(), 200);
  const savedPolicy = (await savedPolicyResponse.json()).policy;
  await savedPolicyResponse.dispose();
  assert.equal(savedPolicy.approvalThreshold, "dangerous");
  assert.equal(savedPolicy.noElicitFallback, "deny");
  assert.deepEqual(savedPolicy.blockedPatterns, ["blocked-browser-test*"]);
  await nav("审计记录");
  await page.locator("#audit-list tbody tr").first().waitFor();
  await page.screenshot({
    path: resolve(artifacts, "audit.png"),
    fullPage: true,
  });
  await nav("节点总览");
  await page.getByRole("button", { name: "删除", exact: true }).click();
  await form().getByRole("button", { name: "确认", exact: true }).click();
  await page.locator("dialog[open]").waitFor({ state: "hidden" });
  await page.getByRole("heading", { name: "还没有匹配的节点" }).waitFor();
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({
    path: resolve(artifacts, "mobile.png"),
    fullPage: true,
  });
  assert.equal(
    await page.evaluate(
      () => document.documentElement.scrollWidth > innerWidth,
    ),
    false,
  );
  await nav("账户");
  await page.getByLabel("当前密码", { exact: true }).fill(initialPassword);
  await page.getByLabel("新密码", { exact: true }).fill("管理员新密码");
  await page.getByLabel("确认新密码", { exact: true }).fill("管理员新密码");
  await page.getByRole("button", { name: "更新密码", exact: true }).click();
  await page.getByRole("heading", { name: "欢迎回来" }).waitFor();
  await page.getByLabel("用户名", { exact: true }).fill(info.username);
  await page.getByLabel("密码", { exact: true }).fill("管理员新密码");
  await page.getByRole("button", { name: "登录控制台", exact: false }).click();
  await page.getByRole("heading", { name: "账户", exact: true }).waitFor();
  await page.getByRole("button", { name: "退出登录", exact: true }).click();
  await page.getByRole("heading", { name: "欢迎回来" }).waitFor();
  await openFromExternalLink();
  await page.getByRole("heading", { name: "欢迎回来" }).waitFor();
  assert.deepEqual(errors, []);
  assert(encryptedRequests.some(url => url.endsWith("/login")));
  assert(encryptedRequests.some(url => url.endsWith("/setup")));
  assert(encryptedRequests.some(url => url.endsWith("/password")));
  console.log(
    `Browser acceptance passed (${useTLS ? "HTTPS / Web Crypto" : "HTTP / embedded crypto"}): prefixed console/assets/API/JWT, isolated ports, external links, Unicode password setup/login/change, direct and independent host-key confirmation, credential/identity/node/tag CRUD, one-time MCP tokens, expiry, enable/revoke, real SSH test, revision conflict, XSS escaping, policy, audit, responsive layout and logout`,
  );
} catch (error) {
  if (page) {
    await page.screenshot({
      path: resolve(artifacts, "failure.png"),
      fullPage: true,
    });
    await writeFile(resolve(artifacts, "failure.html"), await page.content());
  }
  throw error;
} finally {
  if (browser) await browser.close();
  if (fixture && fixture.exitCode === null) {
    fixture.kill("SIGTERM");
    await new Promise((accept, reject) => {
      const timer = setTimeout(() => {
        fixture.kill("SIGKILL");
        reject(new Error("fixture shutdown timeout"));
      }, 10000);
      fixture.once("exit", (code) => {
        clearTimeout(timer);
        code === 0 ? accept() : reject(new Error("fixture exit " + code));
      });
    });
  }
  await rm(work, { recursive: true, force: true });
}
