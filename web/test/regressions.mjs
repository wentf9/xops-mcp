import { chromium } from "playwright";
import assert from "node:assert/strict";
import { generateKeyPairSync, privateDecrypt, createDecipheriv, constants } from "node:crypto";
const encryptionPair = generateKeyPairSync("rsa", { modulusLength: 2048 });
const encryptionJWK = { ...encryptionPair.publicKey.export({ format: "jwk" }), alg: "RSA-OAEP-256", use: "enc", kid: "ui-fixture" };
function encryptedBody(route) {
  const body = route.request().postDataJSON();
  assert.deepEqual(Object.keys(body), ["ciphertext"]);
  const [header, wrapped, iv, ciphertext, tag] = body.ciphertext.split(".");
  const key = privateDecrypt({ key: encryptionPair.privateKey, padding: constants.RSA_PKCS1_OAEP_PADDING, oaepHash: "sha256" }, Buffer.from(wrapped, "base64url"));
  const decipher = createDecipheriv("aes-256-gcm", key, Buffer.from(iv, "base64url"));
  decipher.setAAD(Buffer.from(header));
  decipher.setAuthTag(Buffer.from(tag, "base64url"));
  const plaintext = Buffer.concat([decipher.update(Buffer.from(ciphertext, "base64url")), decipher.final()]);
  try { return JSON.parse(plaintext.toString()).data; }
  finally { key.fill(0); plaintext.fill(0); }
}
import { readFile, mkdir } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const web = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const origin = "https://xops.test";
const nameCases = JSON.parse(
  await readFile(
    resolve(web, "../internal/naming/testdata/cases.json"),
    "utf8",
  ),
);
const passwordCases = JSON.parse(
  await readFile(
    resolve(web, "../internal/adminauth/testdata/passwords.json"),
    "utf8",
  ),
);
const artifacts = resolve(web, "test-results");
await mkdir(artifacts, { recursive: true });
const browser = await chromium.launch({
  headless: true,
  ...(process.env.XOPS_TEST_CHROME
    ? { executablePath: process.env.XOPS_TEST_CHROME }
    : {}),
  args: ["--no-sandbox"],
});

function barrier() {
  let release;
  const ready = new Promise((resolve) => {
    release = resolve;
  });
  return { ready, release };
}
async function bounded(promise) {
  let timer;
  try {
    return await Promise.race([
      promise,
      new Promise((_, reject) => {
        timer = setTimeout(
          () => reject(new Error("browser synchronization deadline exceeded")),
          5000,
        );
      }),
    ]);
  } finally {
    clearTimeout(timer);
  }
}
async function fixture() {
  const context = await browser.newContext();
  context.setDefaultTimeout(5000);
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  const state = {
    inventory: {
      revision: 0,
      pending: false,
      nodes: [],
      hosts: [],
      identities: [],
      credentials: [],
      tags: [],
      policy: {
        enabled: true,
        approvalThreshold: "dangerous",
        noElicitFallback: "deny",
        blockedPatterns: [],
        protectedPaths: [],
        nodes: {},
      },
    },
    operations: [],
    save: null,
    routes: new Map(),
  };
  // Only HTTP responses are controlled. The tests execute the shipped HTML,
  // JavaScript and CSS, and exercise real dialog events in the browser.
  await page.route(origin + "/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (state.routes.has(path)) return state.routes.get(path)(route);
    const json = (data, headers = {}) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        headers,
        body: JSON.stringify(data),
      });
    if (path === "/api/v1/auth/challenge")
      return json({ publicKey: encryptionJWK, challenge: "synthetic-ui-challenge" });
    if (path === "/api/v1/auth/session")
      return json({
        authenticated: true,
        username: "fixture-admin",
        expiresAt: new Date(Date.now() + 900000).toISOString(),
      });
    if (path === "/api/v1/inventory")
      return json(state.inventory, { ETag: `"${state.inventory.revision}"` });
    if (path === "/api/v1/operations")
      return json({ operations: state.operations });
    if (
      path === "/api/v1/hosts" &&
      route.request().method() === "POST" &&
      state.save
    )
      return state.save(route);
    const file = {
      "/": ["index.html", "text/html"],
      "/assets/app.js": ["app.js", "text/javascript"],
      "/assets/auth.js": ["auth.js", "text/javascript"],
      "/assets/naming.js": ["naming.js", "text/javascript"],
      "/assets/style.css": ["style.css", "text/css"],
    }[path];
    if (file)
      return route.fulfill({
        status: 200,
        contentType: file[1],
        body: await readFile(resolve(web, "assets", file[0])),
      });
    return route.fulfill({ status: 404, body: "unexpected fixture request" });
  });
  return { context, page, state, errors };
}
async function openHostDraft(page, name) {
  await page.getByRole("button", { name: "新建主机", exact: false }).click();
  const form = page.locator("dialog[open]");
  await form.getByLabel("名称", { exact: true }).fill(name);
  await form.getByLabel("SSH 地址", { exact: true }).fill("192.0.2.10");
  return form;
}
function json(route, data, status = 200) {
  return route.fulfill({
    status,
    contentType: "application/json",
    body: JSON.stringify(data),
  });
}
async function navigate(page, name) {
  await page.locator(".sidebar").getByRole("link", { name }).click();
  await page.getByRole("heading", { name, exact: true }).waitFor();
}
function populate(state) {
  state.inventory.hosts = [
    {
      id: "host",
      name: "fixture-host",
      address: "192.0.2.10",
      port: 22,
      hostKey: "synthetic-public-key",
      fingerprint: "SHA256:fixture",
    },
  ];
  state.inventory.nodes = ["a", "b"].map((id) => ({
    id,
    name: "node-" + id,
    hostID: "host",
    identityID: "identity",
    tagIDs: ["tag-staging"],
    aliases: [],
    jumpIDs: [],
    disabled: false,
    status: "ready",
    sudoMode: "none",
    privilegeCredentialID: "",
  }));
  state.inventory.identities = [
    {
      id: "identity",
      name: "fixture-identity",
      user: "fixture",
      credentialID: "",
    },
  ];
  state.inventory.tags = [{ id: "tag-staging", name: "staging", count: 2 }];
}
const formValues = (page) =>
  page
    .locator("#policy-form")
    .evaluate((form) => Object.fromEntries(new FormData(form)));
function auditEntry(id, node, outcome) {
  return {
    id,
    event: {
      ts: "2026-10-04T00:00:00Z",
      tool: "fixture.audit." + id,
      node,
      outcome,
      op_id: "operation-" + id,
    },
  };
}
async function checkRemovedAuditNode({ page, state }, external) {
  populate(state);
  const queries = [];
  const auditAfter = async (action) => {
    const [request] = await Promise.all([
      page.waitForRequest((r) => new URL(r.url()).pathname === "/api/v1/audit"),
      action(),
    ]);
    await request.response();
    await page.waitForFunction(
      () => document.querySelector("#more")?.disabled === false,
    );
  };
  state.routes.set("/api/v1/audit", (route) => {
    const query = Object.fromEntries(
      new URL(route.request().url()).searchParams,
    );
    queries.push(query);
    return json(route, {
      entries: query.nodeID
        ? [auditEntry(80, query.nodeID, "error")]
        : [auditEntry(100, "b", "error"), auditEntry(80, "a", "error")],
      next: query.nodeID ? 70 : 60,
    });
  });
  const removeNode = () => {
    state.inventory.nodes = state.inventory.nodes.filter(
      (node) => node.id !== "a",
    );
    state.inventory.revision++;
  };
  state.routes.set("/api/v1/nodes/a", (route) => {
    assert.equal(route.request().method(), "DELETE");
    removeNode();
    return json(route, { id: "a", revision: state.inventory.revision });
  });
  await page.goto(origin + "/#audit");
  await page.getByText("fixture.audit.100", { exact: true }).waitFor();
  await auditAfter(() => page.getByLabel("按结果筛选").selectOption("error"));
  await auditAfter(() => page.getByLabel("按节点筛选").selectOption("a"));
  await page.getByText("fixture.audit.80", { exact: true }).waitFor();
  if (external) {
    removeNode();
    await auditAfter(() =>
      page.getByRole("button", { name: "刷新", exact: false }).click(),
    );
  } else {
    await navigate(page, "节点总览");
    await page
      .locator('[data-action="delete"][data-kind="nodes"][data-id="a"]')
      .click();
    await page
      .locator("dialog[open]")
      .getByRole("button", { name: "确认", exact: true })
      .click();
    await page.locator("dialog[open]").waitFor({ state: "hidden" });
    await auditAfter(() => navigate(page, "审计记录"));
  }
  await page.waitForFunction(
    () => document.querySelector("#more")?.disabled === false,
  );
  assert.equal(await page.getByLabel("按节点筛选").inputValue(), "");
  assert.equal(queries.at(-1).nodeID, "");
  assert.equal(queries.at(-1).before, "0");
  assert.equal(queries.at(-1).outcome, "error");
  await page.getByText("fixture.audit.100", { exact: true }).waitFor();
  await auditAfter(() =>
    page.getByRole("button", { name: "刷新", exact: false }).click(),
  );
  assert.equal(queries.at(-1).nodeID, "");
  assert.equal(await page.getByLabel("按结果筛选").inputValue(), "error");
  await page.getByText("fixture.audit.100", { exact: true }).waitFor();
}
async function checkPasswordByteLimits({ page, state }, setup) {
  if (setup)
    state.routes.set("/api/v1/auth/session", (route) =>
      json(route, {
        authenticated: false,
        setupRequired: true,
        setupEnabled: true,
      }),
    );
  const writes = [];
  page.on("request", (request) => {
    if (["POST", "PUT"].includes(request.method())) writes.push(request.url());
  });
  await page.goto(origin + (setup ? "/" : "/#account"));
  if (setup)
    await page
      .getByLabel("初始化凭据", { exact: true })
      .fill("synthetic-setup-token-01234567890123456789");
  else
    await page
      .getByLabel("当前密码", { exact: true })
      .fill("synthetic-current-password");
  const form = page.locator(setup ? "#auth-form" : "#password-form");
  const password = form.getByLabel(setup ? "密码" : "新密码", { exact: true });
  const confirmation = form.getByLabel(setup ? "确认密码" : "确认新密码", {
    exact: true,
  });
  for (const test of passwordCases) {
    await password.fill(test.value);
    await confirmation.fill(test.value);
    assert.equal(
      await password.inputValue(),
      test.value,
      "password was truncated",
    );
    assert.equal(
      await password.evaluate((input) => input.checkValidity()),
      test.valid,
      test.label,
    );
    assert.equal(
      await confirmation.evaluate((input) => input.checkValidity()),
      test.valid,
      test.label,
    );
    if (!test.valid) await form.dispatchEvent("submit");
  }
  assert.deepEqual(writes, []);
}
async function checkLogoutSessionRace({ page, state }, lateStatus, duringLogin = false) {
  await page.setViewportSize({ width: 1440, height: 1100 });
  const previous = "previous.jwt.token", current = "current.jwt.token";
  const storageKey = "xops.admin.token:" + origin + "/api/v1/";
  const pending = barrier(), release = barrier(), loginPending = barrier(), finishLogin = barrier();
  await page.addInitScript(({ storageKey, previous }) => {
    sessionStorage.setItem(storageKey, previous);
    const readJSON = Response.prototype.json;
    Response.prototype.json = async function (...args) {
      const data = await readJSON.apply(this, args);
      if (this.headers.get("X-Fixture-Late-Session") === "1") {
        // Observe the next task, after api()/boot() consume the JSON and their
        // promise continuations. No timing sleep guesses when the race ran.
        setTimeout(() => { window.lateSessionProcessed = true; }, 0);
      }
      return data;
    };
  }, { storageKey, previous });
  state.routes.set("/api/v1/auth/session", async route => {
    const auth = route.request().headers().authorization;
    if (auth === "Bearer " + previous)
      return json(route, { authenticated: true, username: "previous-admin" });
    assert.equal(auth, undefined);
    pending.release();
    await release.ready;
    return route.fulfill({
      status: lateStatus,
      contentType: "application/json",
      headers: { "X-Fixture-Late-Session": "1" },
      body: JSON.stringify(lateStatus === 200
        ? { authenticated: false, setupRequired: false }
        : { message: "delayed session failure" }),
    });
  });
  state.routes.set("/api/v1/auth/login", async route => {
    const body = encryptedBody(route);
    assert.equal(body.username, "current-admin");
    assert.equal(body.password, "synthetic-current-password");
    loginPending.release();
    if (duringLogin) await finishLogin.ready;
    return json(route, { accessToken: current, username: "current-admin", expiresAt: new Date(Date.now() + 900000).toISOString() });
  });
  try {
    await page.goto(origin + "/");
    await page.getByRole("heading", { name: "节点总览", exact: true }).waitFor();
    await page.locator('.sidebar [data-action="logout"]').click();
    await bounded(pending.ready);
    await page.getByLabel("用户名", { exact: true }).fill("current-admin");
    await page.getByLabel("密码", { exact: true }).fill("synthetic-current-password");
    await page.getByRole("button", { name: "登录控制台", exact: false }).click();
    if (duringLogin) {
      await bounded(loginPending.ready);
      release.release();
      await page.waitForFunction(() => window.lateSessionProcessed);
      assert.equal(await page.getByLabel("用户名", { exact: true }).inputValue(), "current-admin");
      assert.equal(await page.getByRole("button", { name: "登录控制台", exact: false }).isDisabled(), true);
      finishLogin.release();
    }
    await page.getByRole("heading", { name: "节点总览", exact: true }).waitFor();
    assert.equal(await page.evaluate(key => sessionStorage.getItem(key), storageKey), current);
    release.release();
    await page.waitForFunction(() => window.lateSessionProcessed);
    assert.equal(await page.evaluate(key => sessionStorage.getItem(key), storageKey), current);
    await page.getByRole("heading", { name: "节点总览", exact: true }).waitFor();
    assert.match(await page.locator(".sidebar-bottom").innerText(), /current-admin/);
    const nextRequest = page.waitForRequest(request => new URL(request.url()).pathname === "/api/v1/inventory");
    await page.getByRole("button", { name: "↻ 刷新", exact: true }).click();
    assert.equal((await nextRequest).headers().authorization, "Bearer " + current);
  } finally { release.release(); finishLogin.release(); }
}

const cases = {
  async tokenNodeScopeCreate({ page, state }) {
    populate(state);
    const literal = '<img src=x onerror="window.xopsInjected=true">';
    state.inventory.nodes[1].name = literal;
    let saved;
    state.routes.set("/api/v1/mcp-tokens", (route) => {
      if (route.request().method() === "GET") return json(route, { tokens: saved ? [saved] : [] });
      saved = { ...route.request().postDataJSON(), id: "scoped", version: 1, prefix: "xmcp_test", createdAt: 1 };
      return json(route, { item: saved, token: "synthetic-one-time-token" }, 201);
    });
    await page.goto(origin + "/#tokens");
    await page.getByRole("button", { name: "新建Token", exact: false }).click();
    const form = page.locator("dialog[open]");
    await form.getByLabel("名称", { exact: true }).fill("scoped-client");
    assert.equal(await form.getByLabel("允许访问的节点", { exact: true }).inputValue(), "all");
    assert.equal(await form.getByRole("group", { name: "绑定节点" }).isVisible(), false);
    await form.getByLabel("允许访问的节点", { exact: true }).selectOption("selected");
    await form.locator('[name="nodeIDs"][value="a"]').check();
    await form.getByLabel("搜索节点", { exact: true }).fill("img");
    assert.equal(await form.locator('[name="nodeIDs"][value="a"]').isVisible(), false);
    await form.getByLabel(literal, { exact: false }).check();
    assert.equal(await form.locator("img").count(), 0);
    assert.equal(await page.evaluate(() => window.xopsInjected), undefined);
    // Filtering and toggling the scope do not discard the current selections.
    await form.getByLabel("允许访问的节点", { exact: true }).selectOption("all");
    await form.getByLabel("允许访问的节点", { exact: true }).selectOption("selected");
    await form.getByRole("button", { name: "创建 Token", exact: true }).click();
    await form.getByRole("button", { name: "我已保存" }).click();
    await page.getByRole("row").filter({ hasText: "scoped-client" }).getByText("指定 2 个节点", { exact: true }).waitFor();
    assert.equal(saved.nodeScope, "selected");
    assert.deepEqual(saved.nodeIDs, ["a", "b"]);
  },
  async tokenNodeScopeEmpty({ page, state }) {
    let saved;
    state.routes.set("/api/v1/mcp-tokens", (route) => {
      if (route.request().method() === "GET") return json(route, { tokens: saved ? [saved] : [] });
      saved = { ...route.request().postDataJSON(), id: "empty", version: 1, createdAt: 1 };
      return json(route, { item: saved, token: "synthetic-empty-scope-token" }, 201);
    });
    await page.goto(origin + "/#tokens");
    await page.getByRole("button", { name: "新建Token", exact: false }).click();
    const form = page.locator("dialog[open]");
    await form.getByLabel("名称", { exact: true }).fill("no-nodes");
    await form.getByLabel("允许访问的节点", { exact: true }).selectOption("selected");
    await form.getByText("暂无节点，可保存为空范围，之后再编辑绑定。", { exact: true }).waitFor();
    await form.getByRole("button", { name: "创建 Token", exact: true }).click();
    await form.getByRole("button", { name: "我已保存" }).click();
    await page.getByRole("row").filter({ hasText: "no-nodes" }).getByText("无节点访问权限", { exact: true }).waitFor();
    assert.equal(saved.nodeScope, "selected");
    assert.deepEqual(saved.nodeIDs, []);
  },
  async tokenNodeScopeEdit({ page, state }) {
    populate(state);
    let item = { id: "scoped", name: "scoped-client", version: 7, enabled: true, expiresAt: 0, createdAt: 1, nodeScope: "selected", nodeIDs: ["a", "deleted-id"] };
    const submissions = [];
    state.routes.set("/api/v1/mcp-tokens", (route) => json(route, { tokens: [item] }));
    state.routes.set("/api/v1/mcp-tokens/scoped", (route) => {
      submissions.push({ body: route.request().postDataJSON(), etag: route.request().headers()["if-match"] });
      item = { ...item, ...submissions.at(-1).body, version: item.version + 1 };
      return json(route, { item });
    });
    await page.goto(origin + "/#tokens");
    const row = page.getByRole("row").filter({ hasText: "scoped-client" });
    await row.getByText("指定 2 个节点（1 个已删除）", { exact: true }).waitFor();
    await row.getByRole("button", { name: "编辑", exact: true }).click();
    const form = page.locator("dialog[open]");
    assert.equal(await form.getByLabel("允许访问的节点", { exact: true }).inputValue(), "selected");
    assert.equal(await form.locator('[name="nodeIDs"][value="deleted-id"]').isChecked(), true);
    await form.getByRole("button", { name: "保存", exact: true }).click();
    await form.waitFor({ state: "hidden" });
    assert.deepEqual(submissions[0].body.nodeIDs, ["a", "deleted-id"]);
    assert.equal(submissions[0].etag, '"7"');
    await page.reload();
    await row.getByRole("button", { name: "编辑", exact: true }).click();
    await form.locator('[name="nodeIDs"][value="deleted-id"]').uncheck();
    await form.locator('[name="nodeIDs"][value="b"]').check();
    await form.getByRole("button", { name: "保存", exact: true }).click();
    await row.getByText("指定 2 个节点", { exact: true }).waitFor();
    assert.deepEqual(submissions[1].body.nodeIDs, ["a", "b"]);
    await row.getByRole("button", { name: "编辑", exact: true }).click();
    await form.getByLabel("允许访问的节点", { exact: true }).selectOption("all");
    await form.getByRole("button", { name: "保存", exact: true }).click();
    await row.getByText("全部节点", { exact: true }).waitFor();
    assert.equal(submissions[2].body.nodeScope, "all");
    assert.deepEqual(submissions[2].body.nodeIDs, []);
  },
  async tokenLegacyScope({ page, state }) {
    state.routes.set("/api/v1/mcp-tokens", (route) => json(route, { tokens: [{ id: "legacy", name: "legacy-client", version: 1, enabled: true, createdAt: 1 }] }));
    await page.goto(origin + "/#tokens");
    const row = page.getByRole("row").filter({ hasText: "legacy-client" });
    await row.getByText("全部节点", { exact: true }).waitFor();
    await row.getByRole("button", { name: "编辑", exact: true }).click();
    assert.equal(await page.locator("dialog[open]").getByLabel("允许访问的节点", { exact: true }).inputValue(), "all");
  },
  async logoutSessionResponseAfterLogin(fixture) { await checkLogoutSessionRace(fixture, 200); },
  async logoutSessionErrorAfterLogin(fixture) { await checkLogoutSessionRace(fixture, 503); },
  async logoutSessionResponseDuringLogin(fixture) { await checkLogoutSessionRace(fixture, 200, true); },
  async auditTransferOutcomes({ page, state }) {
    populate(state);
    const records = [
      auditEntry(106, "a", "executed"),
      auditEntry(105, "a", "completed"),
      auditEntry(104, "a", "error"),
      auditEntry(103, "a", "failed"),
      auditEntry(102, "a", "completed"),
      auditEntry(101, "a", "failed"),
      auditEntry(100, "b", "completed"),
      auditEntry(99, "b", "failed"),
    ];
    const queries = [];
    state.routes.set("/api/v1/audit", (route) => {
      const query = Object.fromEntries(
        new URL(route.request().url()).searchParams,
      );
      queries.push(query);
      const matching = records.filter(
        ({ id, event }) =>
          (!Number(query.before) || id < Number(query.before)) &&
          (!query.outcome || event.outcome === query.outcome) &&
          (!query.nodeID || event.node === query.nodeID),
      );
      const entries = matching.slice(0, 1);
      return json(route, {
        entries,
        next: matching.length > 1 ? entries[0].id : 0,
      });
    });
    const rowNames = () =>
      page.locator("#audit-list tbody tr td:nth-child(2)").allTextContents();
    await page.goto(origin + "/#audit");
    await page.getByText("fixture.audit.106", { exact: true }).waitFor();
    await page.getByLabel("按节点筛选").selectOption("a");
    await page.getByText("fixture.audit.106", { exact: true }).waitFor();
    for (const [label, outcome, newest, earlier] of [
      ["传输完成", "completed", 105, 102],
      ["传输失败", "failed", 103, 101],
    ]) {
      await page.getByLabel("按结果筛选").selectOption({ label });
      await page
        .getByText(`fixture.audit.${newest}`, { exact: true })
        .waitFor();
      assert.deepEqual(await rowNames(), [`fixture.audit.${newest}`]);
      assert.equal(queries.at(-1).outcome, outcome);
      assert.equal(queries.at(-1).nodeID, "a");
      assert.equal(queries.at(-1).before, "0");
      await page.getByRole("button", { name: "加载更早记录" }).click();
      await page
        .getByText(`fixture.audit.${earlier}`, { exact: true })
        .waitFor();
      assert.deepEqual(await rowNames(), [
        `fixture.audit.${newest}`,
        `fixture.audit.${earlier}`,
      ]);
      assert.equal(queries.at(-1).outcome, outcome);
      assert.equal(queries.at(-1).before, String(newest));
      assert.deepEqual(
        await page
          .locator("#audit-list tbody tr td:nth-child(4)")
          .allTextContents(),
        [label, label],
      );
      assert.equal(
        await page.getByRole("button", { name: "加载更早记录" }).isDisabled(),
        true,
      );
    }
    for (const [label, id] of [
      ["操作完成", 106],
      ["操作失败", 104],
    ]) {
      await page.getByLabel("按结果筛选").selectOption({ label });
      await page.getByText(`fixture.audit.${id}`, { exact: true }).waitFor();
      assert.deepEqual(await rowNames(), [`fixture.audit.${id}`]);
    }
  },
  async setupPasswordByteLimits(fixture) {
    await checkPasswordByteLimits(fixture, true);
  },
  async changePasswordByteLimits(fixture) {
    await checkPasswordByteLimits(fixture, false);
  },
  async unicodePasswordChange({ page, state }) {
    const submitted = barrier();
    state.routes.set("/api/v1/auth/password", (route) => {
      const body = encryptedBody(route);
      assert.equal(body.next, "管理员新密码");
      submitted.release();
      state.routes.set("/api/v1/auth/session", (route) =>
        json(route, { authenticated: false }),
      );
      return route.fulfill({ status: 204 });
    });
    await page.goto(origin + "/#account");
    await page
      .getByLabel("当前密码", { exact: true })
      .fill("synthetic-current-password");
    await page.getByLabel("新密码", { exact: true }).fill("管理员新密码");
    await page.getByLabel("确认新密码", { exact: true }).fill("管理员新密码");
    assert.equal(
      await page
        .locator("#password-form")
        .evaluate((form) => form.checkValidity()),
      true,
    );
    await page.getByRole("button", { name: "更新密码", exact: true }).click();
    await bounded(submitted.ready);
    await page.getByRole("heading", { name: "欢迎回来" }).waitFor();
  },
  async deletedAuditNodeFilter(fixture) {
    await checkRemovedAuditNode(fixture, false);
  },
  async removedAuditNodeAfterRefresh(fixture) {
    await checkRemovedAuditNode(fixture, true);
  },
  async namingContract({ page }) {
    await page.goto(origin + "/#hosts");
    const results = await page.evaluate(async (cases) => {
      const { validName } = await import("/assets/naming.js");
      return cases.map((item) => ({
        label: item.label,
        valid: validName(item.value),
      }));
    }, nameCases);
    assert.deepEqual(
      results,
      nameCases.map(({ label, valid }) => ({ label, valid })),
    );
  },
  async nameForms({ page, state }) {
    populate(state);
    state.inventory.credentials = [
      { id: "credential", name: "fixture-credential", kind: "password" },
    ];
    const writes = [];
    page.on("request", (request) => {
      if (["POST", "PUT", "DELETE"].includes(request.method()))
        writes.push(request.url());
    });
    await page.goto(origin + "/#hosts");
    for (const [kind, title, id] of [
      ["hosts", "主机与信任", "host"],
      ["identities", "登录身份", "identity"],
      ["credentials", "凭据", "credential"],
      ["nodes", "节点总览", "a"],
      ["tags", "标签", "tag-staging"],
    ]) {
      await navigate(page, title);
      for (const editing of [false, true]) {
        if (!editing)
          await page
            .locator(`[data-action="create"][data-kind="${kind}"]`)
            .click();
        else if (kind === "tags")
          await page
            .locator(`[data-action="rename-tag"][data-id="${id}"]`)
            .click();
        else
          await page
            .locator(
              `[data-action="edit"][data-kind="${kind}"][data-id="${id}"]`,
            )
            .filter({ hasText: "编辑" })
            .click();
        const form = page.locator("dialog[open]");
        const input = form.getByLabel(
          kind === "tags" && editing ? "新名称" : "名称",
          { exact: true },
        );
        for (const invalid of [
          "bad name",
          " leading",
          "trailing ",
          "ops,prod",
          "a/b",
          "x.y",
          "🙂",
          "a\u200b",
          "<script>",
          "中".repeat(86),
        ]) {
          await input.fill(invalid);
          assert.equal(
            await input.evaluate((field) => field.validity.valid),
            false,
            `${kind}: ${invalid}`,
          );
          assert.ok(await input.evaluate((field) => field.validationMessage));
          await form.dispatchEvent("submit");
        }
        await input.fill("节点_école-хост-हिन्दी");
        assert.equal(
          await input.evaluate((field) => field.validity.valid),
          true,
        );
        await form.getByRole("button", { name: "取消", exact: true }).click();
      }
    }
    assert.deepEqual(writes, []);
  },
  async aliasValidation({ page, state }) {
    populate(state);
    const aliases = ["ops-prod", "运维_生产", "e\u0301cole", "हिन्दी"];
    state.inventory.nodes[0].aliases = [...aliases];
    const submissions = [];
    state.routes.set("/api/v1/nodes/a", (route) => {
      const body = route.request().postDataJSON();
      submissions.push(body);
      state.inventory.nodes[0] = { ...state.inventory.nodes[0], ...body };
      state.inventory.revision++;
      return json(route, { id: "a", revision: state.inventory.revision });
    });
    const open = async () => {
      await page
        .locator('[data-action="edit"][data-kind="nodes"][data-id="a"]')
        .filter({ hasText: "编辑" })
        .click();
      return page.locator("dialog[open]");
    };
    const save = async (form) => {
      await form.getByRole("button", { name: "保存", exact: true }).click();
      await form.waitFor({ state: "hidden" });
    };
    await page.goto(origin + "/#nodes");
    let form = await open();
    await form.getByLabel("名称", { exact: true }).fill("重新命名_01");
    const input = form.getByLabel("别名", { exact: true });
    for (const invalid of [
      "ops,prod",
      " bad",
      "bad ",
      "two words",
      "a.b",
      "a\tb",
      "🙂",
      "a\u200b",
      "\u0301a",
    ]) {
      await input.fill("valid-alias\n" + invalid);
      assert.equal(
        await input.evaluate((field) => field.validity.valid),
        false,
      );
      await form.dispatchEvent("submit");
    }
    assert.deepEqual(submissions, []);
    await input.fill(aliases.join("\n"));
    assert.equal(await input.evaluate((field) => field.validity.valid), true);
    await save(form);
    assert.deepEqual(submissions[0].aliases, aliases);
    form = await open();
    await form
      .getByLabel("别名", { exact: true })
      .fill("新别名\n\nМосква_02\n");
    await save(form);
    assert.deepEqual(submissions[1].aliases, ["新别名", "Москва_02"]);
    form = await open();
    await form.getByLabel("别名", { exact: true }).fill("");
    await save(form);
    assert.deepEqual(submissions[2].aliases, []);
  },
  async escapedInvalidInventory({ page, state }) {
    populate(state);
    const literal = '<img src=x onerror="window.nameInjected=true">';
    state.inventory.nodes[0].name = literal;
    state.inventory.nodes[0].aliases = ["</textarea>" + literal];
    await page.goto(origin + "/#nodes");
    await page
      .locator('[data-action="edit"][data-kind="nodes"][data-id="a"]')
      .filter({ hasText: "编辑" })
      .click();
    const form = page.locator("dialog[open]");
    assert.equal(
      await form.getByLabel("名称", { exact: true }).inputValue(),
      literal,
    );
    assert.equal(
      await form
        .getByLabel("名称", { exact: true })
        .evaluate((input) => input.validity.valid),
      false,
    );
    assert.equal(
      await form
        .getByLabel("别名", { exact: true })
        .evaluate((input) => input.validity.valid),
      false,
    );
    assert.equal(await page.evaluate(() => window.nameInjected), undefined);
    assert.equal(await page.locator("#app img, dialog img").count(), 0);
  },
  async deletedTagFilter({ page, state }) {
    populate(state);
    state.routes.set("/api/v1/tags/tag-staging", (route) => {
      assert.equal(route.request().method(), "DELETE");
      state.inventory.tags = [];
      for (const node of state.inventory.nodes) node.tagIDs = [];
      state.inventory.revision = 1;
      return json(route, { revision: 1 });
    });
    await page.goto(origin + "/#nodes");
    await page.getByLabel("按标签筛选").selectOption("tag-staging");
    await navigate(page, "标签");
    await page.getByRole("button", { name: "删除标签", exact: true }).click();
    await page
      .locator("dialog[open]")
      .getByRole("button", { name: "确认", exact: true })
      .click();
    await page.locator("dialog[open]").waitFor({ state: "hidden" });
    await navigate(page, "节点总览");
    assert.equal(await page.getByLabel("按标签筛选").inputValue(), "");
    assert.equal(await page.locator("#page tbody tr").count(), 2);
  },
  async policy({ page }) {
    await page.goto(origin + "/#policy");
    await page
      .getByRole("heading", { name: "操作策略", exact: true })
      .waitFor();
    const input = page.getByLabel("额外拦截的命令模式", { exact: true });
    assert.match(await input.getAttribute("placeholder"), /glob/i);
    assert.doesNotMatch(await input.getAttribute("placeholder"), /正则/);
    const helpID = await input.getAttribute("aria-describedby");
    assert.ok(helpID, "the glob syntax must have an accessible explanation");
    const help = await page.locator(`#${helpID}`).innerText();
    assert.match(help, /hostname\*/);
    assert.match(help, /完整命令/);
  },
  async policyApprovalThreshold({ page, state }) {
    state.routes.set("/api/v1/policy", (route) => {
      state.inventory.policy = route.request().postDataJSON();
      state.inventory.revision++;
      return json(route, { revision: state.inventory.revision });
    });
    for (const threshold of [undefined, "", "safe", "moderate", "dangerous"]) {
      state.inventory.policy.approvalThreshold = threshold;
      if (page.url() === origin + "/#policy") await page.reload();
      else await page.goto(origin + "/#policy");
      const expected = threshold || "dangerous";
      assert.equal(
        await page.getByLabel("开始确认的风险级别").inputValue(),
        expected,
        `policy threshold ${JSON.stringify(threshold)}`,
      );
      await page
        .getByLabel("额外拦截的命令模式", { exact: true })
        .fill("fixture-blocked*");
      await page.getByLabel("受保护路径").fill("/fixture-protected");
      const revision = state.inventory.revision + 1;
      const [request] = await Promise.all([
        page.waitForRequest(
          (r) =>
            r.method() === "PUT" &&
            new URL(r.url()).pathname === "/api/v1/policy",
        ),
        page.getByRole("button", { name: "保存策略" }).click(),
      ]);
      assert.deepEqual(request.postDataJSON(), {
        enabled: true,
        approvalThreshold: expected,
        noElicitFallback: "deny",
        blockedPatterns: ["fixture-blocked*"],
        protectedPaths: ["/fixture-protected"],
        nodes: {},
      });
      await page.waitForFunction(
        (revision) =>
          document.querySelector("#config-revision")?.textContent ===
          `配置版本 ${revision}`,
        revision,
      );
      assert.equal(
        await page.getByLabel("开始确认的风险级别").inputValue(),
        expected,
      );
    }
  },
  async targetless({ page, state, errors }) {
    state.operations = [[], null].map((nodeIDs, i) => ({
      operationID: "inventory-" + i,
      tool: "xops_list_nodes",
      nodeIDs,
      phase: "execute",
      state: "active",
      startedAt: new Date().toISOString(),
    }));
    await page.goto(origin + "/#operations");
    await page.getByRole("heading", { name: "运行中", exact: true }).waitFor();
    assert.equal(await page.locator("#ops tbody tr").count(), 2);
    assert.deepEqual(
      await page.locator("#ops tbody tr td:nth-child(2)").allTextContents(),
      ["—", "—"],
    );
    assert.deepEqual(errors, []);
  },
  async submission({ page, state }) {
    const received = barrier(),
      release = barrier();
    state.save = async (route) => {
      const input = route.request().postDataJSON();
      state.inventory.revision = 1;
      state.inventory.hosts = [
        { ...input, id: "saved-host", hostKey: "", fingerprint: "" },
      ];
      received.release();
      await release.ready;
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ id: "saved-host", revision: 1 }),
      });
    };
    try {
      await page.goto(origin + "/#hosts");
      const first = await openHostDraft(page, "submitted-host");
      await first.getByRole("button", { name: "保存", exact: true }).click();
      await bounded(received.ready);
      await first.getByRole("button", { name: "取消", exact: true }).click();
      const second = await openHostDraft(page, "unsaved-host");
      await second.getByLabel("SSH 地址", { exact: true }).fill("192.0.2.20");
      release.release();
      await page.waitForFunction(() =>
        document.querySelector("footer")?.textContent.includes("配置版本 1"),
      );
      const draft = await page.evaluate(() => ({
        open: document.querySelector("dialog").open,
        name: document.querySelector('#modal-form [name="name"]')?.value,
        address: document.querySelector('#modal-form [name="address"]')?.value,
      }));
      assert.deepEqual(draft, {
        open: true,
        name: "unsaved-host",
        address: "192.0.2.20",
      });
    } finally {
      release.release();
    }
  },
  async queuedClose({ page }) {
    await page.goto(origin + "/#hosts");
    await openHostDraft(page, "old-draft");
    const draft = await page.evaluate(async () => {
      const dialog = document.querySelector("dialog");
      const closed = new Promise((resolve) =>
        dialog.addEventListener("close", resolve, { once: true }),
      );
      dialog.querySelector("[data-close]").click();
      document
        .querySelector('[data-action="create"][data-kind="hosts"]')
        .click();
      document.querySelector('#modal-form [name="name"]').value =
        "reopened-draft";
      await closed;
      return {
        open: dialog.open,
        name: dialog.querySelector('[name="name"]')?.value,
      };
    });
    assert.deepEqual(draft, { open: true, name: "reopened-draft" });
  },
  async newerPolicyDraft({ page, state }) {
    const received = barrier(),
      release = barrier();
    let submitted;
    state.save = async (route) => {
      received.release();
      await release.ready;
      state.inventory.revision = 1;
      return json(route, { id: "saved-host", revision: 1 });
    };
    state.routes.set("/api/v1/policy", (route) => {
      submitted = {
        body: route.request().postDataJSON(),
        etag: route.request().headers()["if-match"],
      };
      return json(
        route,
        { message: "配置已被其他会话修改，请刷新后重试" },
        412,
      );
    });
    try {
      await page.goto(origin + "/#hosts");
      const host = await openHostDraft(page, "submitted-host");
      await host.getByRole("button", { name: "保存", exact: true }).click();
      await bounded(received.ready);
      await host.getByRole("button", { name: "取消", exact: true }).click();
      await navigate(page, "操作策略");
      await page
        .getByLabel("额外拦截的命令模式", { exact: true })
        .fill("hostname*\nwhoami");
      await page
        .getByLabel("受保护路径", { exact: true })
        .fill("/private-draft");
      await page.getByLabel("开始确认的风险级别").selectOption("moderate");
      const draft = await formValues(page);
      release.release();
      await page.waitForFunction(() =>
        document.querySelector("footer")?.textContent.includes("配置版本 1"),
      );
      assert.deepEqual(await formValues(page), draft);
      await page.getByRole("button", { name: "保存策略" }).click();
      await page
        .getByRole("alert")
        .getByText("配置已被其他会话修改", { exact: false })
        .waitFor();
      assert.equal(submitted.etag, '"0"');
      assert.deepEqual(submitted.body.blockedPatterns, ["hostname*", "whoami"]);
      assert.deepEqual(await formValues(page), draft);
    } finally {
      release.release();
    }
  },
  async editDuringPolicySave({ page, state, reopen = false }) {
    const received = barrier(),
      release = barrier();
    const submissions = [];
    state.routes.set("/api/v1/policy", async (route) => {
      submissions.push({
        body: route.request().postDataJSON(),
        etag: route.request().headers()["if-match"],
      });
      if (submissions.length > 1)
        return json(route, { message: "配置版本冲突" }, 412);
      received.release();
      await release.ready;
      state.inventory.revision = 1;
      state.inventory.policy = submissions[0].body;
      return json(route, { revision: 1 });
    });
    try {
      await page.goto(origin + "/#policy");
      const input = page.getByLabel("额外拦截的命令模式", { exact: true });
      await input.fill("hostname");
      await page.getByRole("button", { name: "保存策略" }).click();
      await bounded(received.ready);
      if (reopen) {
        await navigate(page, "账户");
        await navigate(page, "操作策略");
      }
      await input.fill("newer-draft*");
      release.release();
      await page.waitForFunction(() =>
        document.querySelector("footer")?.textContent.includes("配置版本 1"),
      );
      assert.equal(await input.inputValue(), "newer-draft*");
      await page.getByRole("button", { name: "保存策略" }).click();
      await page.getByRole("alert").getByText("配置版本冲突").waitFor();
      assert.equal(submissions[1].etag, '"0"');
    } finally {
      release.release();
    }
  },
  async reopenedPolicyDraft(fixture) {
    await cases.editDuringPolicySave({ ...fixture, reopen: true });
  },
  async policySavedRevision({ page, state }) {
    const etags = [];
    state.routes.set("/api/v1/policy", (route) => {
      etags.push(route.request().headers()["if-match"]);
      state.inventory.policy = route.request().postDataJSON();
      state.inventory.revision++;
      return json(route, { revision: state.inventory.revision });
    });
    await page.goto(origin + "/#policy");
    for (const [index, pattern] of ["hostname", "whoami"].entries()) {
      await page
        .getByLabel("额外拦截的命令模式", { exact: true })
        .fill(pattern);
      await page.getByRole("button", { name: "保存策略" }).click();
      await page.waitForFunction(
        (revision) =>
          document
            .querySelector("footer")
            ?.textContent.includes("配置版本 " + revision),
        index + 1,
      );
    }
    assert.deepEqual(etags, ['"0"', '"1"']);
  },
  async auditFilterPagination({ page, state }) {
    populate(state);
    const received = barrier(),
      release = barrier();
    const queries = [];
    state.routes.set("/api/v1/audit", async (route) => {
      const query = Object.fromEntries(
        new URL(route.request().url()).searchParams,
      );
      queries.push(query);
      if (!query.outcome)
        return json(route, {
          entries: [auditEntry(100, "a", "executed")],
          next: 80,
        });
      if (query.before === "0") {
        received.release();
        await release.ready;
        return json(route, {
          entries: [auditEntry(110, "b", "error")],
          next: 75,
        });
      }
      return json(route, { entries: [auditEntry(70, "b", "error")], next: 0 });
    });
    try {
      await page.goto(origin + "/#audit");
      await page.getByText("fixture.audit.100", { exact: true }).waitFor();
      await page.getByLabel("按结果筛选").selectOption("error");
      await bounded(received.ready);
      assert.equal(
        await page.getByRole("button", { name: "加载更早记录" }).isDisabled(),
        true,
      );
      assert.equal(
        await page.getByText("fixture.audit.100", { exact: true }).count(),
        0,
      );
      // Even a queued click cannot supersede the filtered first page.
      await page.locator("#more").dispatchEvent("click");
      release.release();
      await page.getByText("fixture.audit.110", { exact: true }).waitFor();
      assert.equal(queries.length, 2);
      await page.getByRole("button", { name: "加载更早记录" }).click();
      await page.getByText("fixture.audit.70", { exact: true }).waitFor();
      assert.deepEqual(
        await page
          .locator("#audit-list tbody tr td:nth-child(2)")
          .allTextContents(),
        ["fixture.audit.110", "fixture.audit.70"],
      );
      assert.equal(queries[2].before, "75");
      assert.equal(queries[2].outcome, "error");
    } finally {
      release.release();
    }
  },
  async auditOldPageAndFailure({ page, state }) {
    populate(state);
    const received = barrier(),
      release = barrier(),
      finished = barrier();
    const queries = [];
    let fail = true;
    state.routes.set("/api/v1/audit", async (route) => {
      const query = Object.fromEntries(
        new URL(route.request().url()).searchParams,
      );
      queries.push(query);
      if (query.nodeID === "b") {
        if (fail)
          return json(route, { message: "synthetic audit load failure" }, 503);
        return json(route, {
          entries: [auditEntry(120, "b", "error")],
          next: 0,
        });
      }
      if (query.before !== "0") {
        received.release();
        await release.ready;
        await json(route, {
          entries: [auditEntry(70, "a", "executed")],
          next: 60,
        });
        finished.release();
        return;
      }
      return json(route, {
        entries: [auditEntry(100, "a", "executed")],
        next: 90,
      });
    });
    try {
      await page.goto(origin + "/#audit");
      await page.getByText("fixture.audit.100", { exact: true }).waitFor();
      await page.getByRole("button", { name: "加载更早记录" }).click();
      await bounded(received.ready);
      await page.locator("#more").dispatchEvent("click");
      await page.getByLabel("按节点筛选").selectOption("b");
      await page
        .locator("#notices")
        .getByText("synthetic audit load failure")
        .waitFor();
      release.release();
      await bounded(finished.ready);
      assert.equal(
        await page.getByRole("button", { name: "加载更早记录" }).isDisabled(),
        true,
      );
      assert.equal(await page.locator("#audit-list tbody tr").count(), 0);
      assert.equal(queries.filter((q) => q.before === "90").length, 1);
      fail = false;
      await page.getByRole("button", { name: "重试加载", exact: true }).click();
      await page.getByText("fixture.audit.120", { exact: true }).waitFor();
      assert.deepEqual(
        await page
          .locator("#audit-list tbody tr td:nth-child(2)")
          .allTextContents(),
        ["fixture.audit.120"],
      );
      assert.equal(queries.at(-1).before, "0");
      assert.equal(queries.at(-1).nodeID, "b");
    } finally {
      release.release();
    }
  },
  async appliedWarningWithRefreshFailure({ page, state }) {
    let mutations = 0;
    state.save = (route) => {
      mutations++;
      state.routes.set("/api/v1/inventory", (route) =>
        json(route, { message: "synthetic refresh failure" }, 503),
      );
      return json(route, { revision: 1, warning }, 202);
    };
    await page.goto(origin + "/#hosts");
    const host = await openHostDraft(page, "applied-host");
    await host.getByRole("button", { name: "保存", exact: true }).click();
    await page
      .locator("#notices")
      .getByText(warning, { exact: true })
      .waitFor();
    await page
      .locator("#notices")
      .getByText("变更已完成，但页面刷新失败", { exact: false })
      .waitFor();
    await page.locator("dialog[open]").waitFor({ state: "hidden" });
    assert.equal(mutations, 1);
  },
};
const warning = "配置已应用，但审计写入失败，请勿重复提交";
for (const kind of [
  "edit",
  "policy",
  "trust",
  "revoke",
  "delete",
  "rename-tag",
  "delete-tag",
  "toggle",
  "reconcile",
]) {
  cases["warning_" + kind] = async ({ page, state }) => {
    populate(state);
    const path = {
      edit: "/hosts/host",
      policy: "/policy",
      trust: "/hosts/host/trust",
      revoke: "/hosts/host/trust",
      delete: "/hosts/host",
      "rename-tag": "/tags/tag-staging",
      "delete-tag": "/tags/tag-staging",
      toggle: "/nodes/a",
      reconcile: "/reconcile",
    }[kind];
    let mutations = 0;
    state.routes.set("/api/v1" + path, (route) => {
      mutations++;
      state.inventory.revision = mutations;
      return json(route, { revision: mutations, id: "host", warning }, 202);
    });
    if (kind === "reconcile") state.inventory.pending = true;
    const section =
      kind === "policy"
        ? "policy"
        : kind.includes("tag")
          ? "tags"
          : ["toggle", "reconcile"].includes(kind)
            ? "nodes"
            : "hosts";
    await page.goto(origin + "/#" + section);
    if (kind === "policy")
      await page.getByRole("button", { name: "保存策略" }).click();
    else if (kind === "toggle")
      await page.locator('[data-action="toggle"][data-id="a"]').click();
    else if (kind === "reconcile")
      await page.getByRole("button", { name: "重试应用" }).click();
    else {
      if (kind === "trust") {
        state.routes.set("/api/v1/hosts/host/probe", (route) =>
          json(route, {
            probeID: "probe",
            hostKey: "synthetic-public-key",
            fingerprint: "SHA256:fixture",
            algorithm: "ssh-ed25519",
          }),
        );
        await page.getByRole("button", { name: "配置公钥" }).click();
        await page.getByRole("button", { name: "连接并获取公钥" }).click();
        await page.getByLabel("已通过独立渠道核对指纹").check();
      } else await page.locator(`[data-action="${kind}"]`).click();
      await page
        .locator("dialog[open]")
        .getByRole("button", { name: /^(保存|确认|确认并固定)$/ })
        .click();
    }
    await page
      .locator("#notices")
      .getByText(warning, { exact: true })
      .waitFor();
    await page.waitForFunction(() =>
      document.querySelector("footer")?.textContent.includes("配置版本 1"),
    );
    assert.equal(mutations, 1);
    assert.equal(await page.locator("#notices .notice").count(), 1);
  };
}
const failures = [];
try {
  const selected = new Set(process.argv.slice(2));
  for (const name of selected) assert(name in cases, "unknown browser regression: " + name);
  for (const [name, check] of Object.entries(cases)) {
    if (selected.size && !selected.has(name)) continue;
    const f = await fixture();
    try {
      await check(f);
      assert.deepEqual(f.errors, []);
      console.log(`PASS browser regression: ${name}`);
    } catch (error) {
      failures.push(`${name}: ${error.message}`);
      await f.page.screenshot({
        path: resolve(artifacts, `regression-${name}.png`),
        fullPage: true,
      });
    } finally {
      await f.context.close();
    }
  }
} finally {
  await browser.close();
}
if (failures.length) {
  console.error(failures.join("\n"));
  process.exitCode = 1;
}
