import { aliasLines, nameHint, validName } from "./naming.js";

const $ = (s, r = document) => r.querySelector(s);
const esc = (v) =>
  String(v ?? "").replace(
    /[&<>"']/g,
    (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        c
      ],
  );
const apiRoot = new URL("api/v1/", document.baseURI);
const S = {
  session: null,
  v: null,
  etag: "",
  page: "nodes",
  search: "",
  tag: "",
  ops: [],
  audit: [],
  next: 0,
  auditFilter: "",
  auditLoading: false,
  auditError: "",
  outcome: "",
  auditNode: "",
};
const pages = {
  nodes: ["节点总览", "集中管理连接目标，让每次操作都指向正确的主机。", "▦"],
  hosts: ["主机与信任", "维护连接地址，并独立核对 SSH 主机公钥。", "▤"],
  identities: ["登录身份", "将远端账号与凭据关联，在节点间安全复用。", "♙"],
  credentials: ["凭据", "密码与私钥加密保存，仅显示配置元数据。", "◇"],
  tags: ["标签", "按环境或用途组织节点。", "#"],
  policy: ["操作策略", "管理风险确认、命令拦截与受保护路径。", "◎"],
  operations: ["运行中", "查看当前准入操作及其执行阶段。", "↗"],
  audit: ["审计记录", "查看授权、配置变更与执行结果。", "≡"],
  account: ["账户", "管理管理员密码和当前会话。", "⚙"],
};
const dialog = $("#dialog"),
  find = (k, id) => S.v[k].find((x) => x.id === id),
  name = (k, id) =>
    S.v?.[k]?.find((x) => x.id === id)?.name ||
    (id ? id.slice(0, 12) : "未关联");
dialog.addEventListener("close", () => {
  // A queued close event may belong to a form replaced in the same turn.
  if (!dialog.open) dialog.replaceChildren();
});
function ownsDialog(form) {
  return dialog.open && dialog.firstElementChild === form;
}
const date = (v) => new Date(v).toLocaleString("zh-CN", { hour12: false });
function validateNamingField(input) {
  const values =
    input.dataset.nameRule === "aliases"
      ? aliasLines(input.value)
      : [input.value];
  input.setCustomValidity(values.every(validName) ? "" : nameHint);
}
function namingFields(form) {
  return form.querySelectorAll("[data-name-rule]");
}
const adminPasswordHint =
  "密码需为 12–72 个 UTF-8 字节，不能包含换行或空字符。";
const passwordEncoder = new TextEncoder();
function validateAdminPassword(input) {
  const value = input.value,
    bytes = passwordEncoder.encode(value).length;
  input.setCustomValidity(
    bytes >= 12 && bytes <= 72 && !/[\x00\r\n]/.test(value)
      ? ""
      : adminPasswordHint,
  );
}
function bindAdminPasswords(form) {
  form.querySelectorAll("[data-admin-password]").forEach((input) => {
    const validate = () => validateAdminPassword(input);
    input.addEventListener("input", validate);
    input.addEventListener("change", validate);
    validate();
  });
}
function checkAdminPasswords(form) {
  form.querySelectorAll("[data-admin-password]").forEach(validateAdminPassword);
  return form.reportValidity();
}
let inventoryRequest = 0,
  auditRequest = 0;
const formVersions = new WeakMap();
function formSnapshot(form = $("#page form")) {
  return { form, version: formVersions.get(form) };
}
function toast(text, error = false) {
  const e = document.createElement("div");
  e.className = "notice" + (error ? " error" : "");
  e.textContent = text;
  $("#notices").append(e);
  while ($("#notices").children.length > 2)
    $("#notices").firstElementChild.remove();
  setTimeout(() => e.remove(), 6000);
}
async function api(path, { method = "GET", body, etag } = {}) {
  const headers = { Accept: "application/json" };
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (!["GET", "HEAD"].includes(method) && !path.startsWith("/auth/"))
    headers["If-Match"] = etag ?? S.etag;
  if (S.session?.csrf) headers["X-CSRF-Token"] = S.session.csrf;
  const response = await fetch(new URL(path.slice(1), apiRoot), {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: "same-origin",
    cache: "no-store",
  });
  const data =
    response.status === 204
      ? {}
      : await response.json().catch(() => ({ message: "服务响应不正确" }));
  if (!response.ok) {
    if (
      response.status === 401 &&
      !["/auth/login", "/auth/setup", "/auth/session"].includes(path)
    ) {
      S.session = null;
      S.v = null;
      if (dialog.open) dialog.close();
      authPage({});
    }
    const e = new Error(data.message || "请求失败");
    e.status = response.status;
    throw e;
  }
  return { data, etag: response.headers.get("ETag") };
}
async function load() {
  const id = ++inventoryRequest;
  const [v, ops] = await Promise.all([api("/inventory"), api("/operations")]);
  if (id !== inventoryRequest) return;
  S.v = v.data;
  S.etag = v.etag;
  S.ops = ops.data.operations;
  if (S.tag && !S.v.tags.some((tag) => tag.id === S.tag)) S.tag = "";
  if (S.auditNode && !S.v.nodes.some((node) => node.id === S.auditNode)) {
    S.auditNode = "";
    resetAuditResults();
  }
}
function resetAuditResults() {
  // Invalidate in-flight requests as well as the cursor for the old filter.
  auditRequest++;
  S.audit = [];
  S.next = 0;
  S.auditFilter = "";
  S.auditLoading = false;
  S.auditError = "";
}
async function refreshInventory(submitted = null) {
  await load();
  const form = $("#page form");
  // Keep the live form and its original ETag closure when another action
  // completes, or when input has changed since this form was submitted.
  if (
    form &&
    (submitted?.form !== form || submitted.version !== formVersions.get(form))
  )
    updateStatus();
  else render();
}
async function mutate(path, options, message, submitted = null) {
  const { data } = await api(path, options);
  toast(data.warning || message, !!data.warning);
  try {
    await refreshInventory(submitted);
  } catch (error) {
    // The write already succeeded. A failed refresh must not invite replay.
    if (error.status !== 401)
      toast("变更已完成，但页面刷新失败，请刷新查看：" + error.message, true);
  }
}
function updateStatus() {
  if ($("#publication-status"))
    $("#publication-status").innerHTML = S.v.pending
      ? '<div class="banner warning"><div><strong>配置已保存，仍待应用。</strong><p>受影响的新操作暂时暂停。重试只应用已保存的配置。</p></div><button data-action="reconcile">重试应用</button></div>'
      : "";
  if ($("#config-revision"))
    $("#config-revision").textContent = "配置版本 " + S.v.revision;
}
async function boot() {
  try {
    const { data } = await api("/auth/session");
    if (!data.authenticated) {
      S.session = null;
      authPage(data);
      return;
    }
    S.session = data;
    await load();
    render();
  } catch (e) {
    $("#app").innerHTML =
      `<main class="unavailable"><div class="brand-mark">X</div><h1>暂时无法连接</h1><p>${esc(e.message)}</p><button id="retry" class="primary">重新连接</button></main>`;
    $("#retry").onclick = boot;
  }
}
function authPage(info) {
  resetAuditResults();
  const setup = info.setupRequired;
  $("#app").innerHTML =
    `<main class="auth-layout"><section class="auth-story"><a class="brand"><span class="brand-mark">X</span>XOps <small>CONTROL PLANE</small></a><div><span class="eyebrow">YOUR INFRASTRUCTURE. ONE PLACE.</span><h1>掌握每一个<br>连接与操作。</h1><p>节点、信任与访问凭据统一管理。<br>从可控的连接开始，让运维井然有序。</p><div class="auth-lines">━━ ━ ━</div></div><small>自托管 · 单实例 · SSH / SFTP / MCP</small></section><section class="auth-panel"><div class="auth-box"><div class="eyebrow">XOPS CONSOLE</div><h2>${setup ? "初始化管理员" : "欢迎回来"}</h2><p class="muted">${setup ? "为这台服务创建唯一的管理员账户。" : "登录以管理节点与访问策略。"}</p>${setup && !info.setupEnabled ? '<div class="banner">请在服务器使用 admin-init 命令初始化管理员，或配置专用 Web 初始化凭据后重启。</div><button id="retry-setup">重新检查</button>' : `<form id="auth-form">${field("用户名", "username", "admin", 'required autocomplete="username" maxlength="64"')}${field("密码", "password", "", 'type="password" required ' + (setup ? 'data-admin-password aria-describedby="admin-password-help" autocomplete="new-password"' : 'maxlength="72" autocomplete="current-password"'))}${setup ? field("确认密码", "confirm", "", 'type="password" required data-admin-password aria-describedby="admin-password-help" autocomplete="new-password"') + `<p id="admin-password-help" class="hint">${adminPasswordHint}</p>` + field("初始化凭据", "token", "", 'type="password" required autocomplete="off" minlength="32"') : ""}<p class="form-error" role="alert"></p><button class="primary wide">${setup ? "创建管理员" : "登录控制台"} <span aria-hidden="true">→</span></button></form>`}<small class="auth-footer">管理员会话与 MCP 访问凭据相互独立。</small></div></section></main>`;
  $("#retry-setup")?.addEventListener("click", boot);
  const form = $("#auth-form");
  if (!form) return;
  bindAdminPasswords(form);
  form.onsubmit = async (e) => {
    e.preventDefault();
    if (!checkAdminPasswords(form)) return;
    const x = Object.fromEntries(new FormData(form)),
      button = $("button", form);
    $(".form-error", form).textContent = "";
    if (setup && x.password !== x.confirm) {
      $(".form-error", form).textContent = "两次输入的密码不一致";
      return;
    }
    button.disabled = true;
    try {
      if (setup) {
        await api("/auth/setup", {
          method: "POST",
          body: { username: x.username, password: x.password, token: x.token },
        });
        toast("管理员已创建");
      }
      const { data } = await api("/auth/login", {
        method: "POST",
        body: { username: x.username, password: x.password },
      });
      S.session = data;
      form.reset();
      await load();
      render();
    } catch (error) {
      $(".form-error", form).textContent = error.message;
      button.disabled = false;
    }
  };
}
function chip(text, kind = "neutral") {
  return `<span class="chip ${kind}">${esc(text)}</span>`;
}
function empty(title, text) {
  return `<div class="empty"><div class="empty-symbol">◇</div><h3>${title}</h3><p>${text}</p></div>`;
}
function table(headers, rows) {
  return `<div class="table-wrap"><table><thead><tr>${headers.map((h) => `<th>${h}</th>`).join("")}</tr></thead><tbody>${rows.join("")}</tbody></table></div>`;
}
function actions(k, id, extra = "") {
  return `<div class="row-actions">${extra}<button data-action="edit" data-kind="${k}" data-id="${id}">编辑</button><button class="danger-text" data-action="delete" data-kind="${k}" data-id="${id}">删除</button></div>`;
}
function render() {
  if (!S.session || !S.v) return;
  S.page = pages[location.hash.slice(1)] ? location.hash.slice(1) : "nodes";
  const [title, description] = pages[S.page];
  document.title = title + " · XOps";
  $("#app").innerHTML =
    `<div class="shell"><aside class="sidebar"><a class="brand" href="#nodes"><span class="brand-mark">X</span>XOps</a><div class="workspace"><i class="dot"></i><div>运维工作空间<small>自托管服务</small></div></div><div class="nav-label">资源管理</div><nav>${Object.entries(
      pages,
    )
      .map(
        ([p, [label, , icon]]) =>
          `${p === "operations" ? '<div class="nav-label">可观测性</div>' : p === "account" ? '<div class="nav-label">设置</div>' : ""}<a href="#${p}" class="nav-item ${S.page === p ? "active" : ""}"><span>${icon}</span>${label}</a>`,
      )
      .join(
        "",
      )}</nav><div class="sidebar-bottom"><div class="avatar">A</div><div>${esc(S.session.username)}<small>管理员</small></div><button data-action="logout" aria-label="退出登录" title="退出登录">↪</button></div></aside><main class="main"><header class="topbar"><div>控制台 <span class="slash">/</span> ${title}</div><div class="topbar-right"><span class="online"><i class="dot"></i>服务在线</span><button class="quiet" data-action="refresh">↻ 刷新</button><button class="mobile-logout quiet" data-action="logout" aria-label="退出登录">↪</button></div></header><div class="content"><div class="page-heading"><div><div class="eyebrow">${S.page === "nodes" ? "INFRASTRUCTURE" : "WORKSPACE"}</div><h1>${title}</h1><p>${description}</p></div>${["nodes", "hosts", "identities", "credentials", "tags"].includes(S.page) ? `<button class="primary" data-action="create" data-kind="${S.page}">＋ 新建${{ nodes: "节点", hosts: "主机", identities: "身份", credentials: "凭据", tags: "标签" }[S.page]}</button>` : ""}</div><div id="publication-status"></div><section id="page">${content()}</section><footer><span>XOps MCP · 本地掌控，安全连接</span><span id="config-revision"></span></footer></div></main></div>`;
  bind();
  updateStatus();
  if (S.page === "audit") loadAudit();
}
function content() {
  const v = S.v;
  if (S.page === "nodes") {
    const nodes = v.nodes.filter(
      (n) =>
        (!S.tag || n.tagIDs?.includes(S.tag)) &&
        [
          n.name,
          ...(n.aliases || []),
          ...(n.tagIDs || []).map((id) => name("tags", id)),
          find("hosts", n.hostID)?.address,
        ]
          .join(" ")
          .toLowerCase()
          .includes(S.search.toLowerCase()),
    );
    const rows = nodes.map((n) => {
      const h = find("hosts", n.hostID),
        i = find("identities", n.identityID),
        status = {
          ready: ["可用", "good"],
          disabled: ["未启用", "neutral"],
          blocked: ["跳板不可用", "warning"],
        }[n.status];
      return `<tr><td><button class="name-button" data-action="edit" data-kind="nodes" data-id="${n.id}"><span class="node-icon">⌘</span><span>${esc(n.name)}<small>${esc((n.aliases || []).join(" · ") || "无别名")}</small></span></button></td><td><span class="mono">${esc(h?.address)}:${h?.port}</span><small>${esc(i?.user)} · ${esc(h?.name)}</small></td><td>${chip(...status)}</td><td>${(n.tagIDs || []).map((id) => chip(name("tags", id))).join(" ") || "—"}</td><td>${actions("nodes", n.id, `<button data-action="test" data-id="${n.id}" ${n.disabled ? "disabled" : ""}>测试连接</button><button data-action="toggle" data-id="${n.id}">${n.disabled ? "启用" : "停用"}</button>`)}</td></tr>`;
    });
    return `<div class="metrics"><div class="metric"><span>全部节点</span><strong>${v.nodes.length}</strong><small>统一管理的连接目标</small></div><div class="metric"><span>可用节点</span><strong class="accent">${v.nodes.filter((n) => n.status === "ready").length}</strong><small><i class="dot"></i>凭据与信任已配置</small></div><div class="metric"><span>主机</span><strong>${v.hosts.length}</strong><small>已固定公钥 ${v.hosts.filter((h) => h.hostKey).length} 台</small></div><div class="metric"><span>运行中</span><strong>${S.ops.length}</strong><small>当前准入操作</small></div></div><div class="panel"><div class="toolbar"><div class="search"><span>⌕</span><input id="search" aria-label="搜索节点" placeholder="搜索名称、地址或标签…" value="${esc(S.search)}"></div><select id="tag-filter" aria-label="按标签筛选"><option value="">全部标签</option>${v.tags.map((t) => `<option value="${esc(t.id)}" ${S.tag === t.id ? "selected" : ""}>${esc(t.name)}</option>`).join("")}</select><span class="count">${nodes.length} 个节点</span></div>${rows.length ? table(["节点", "连接目标", "状态", "标签", "操作"], rows) : empty("还没有匹配的节点", v.hosts.length && v.identities.length ? "点击“新建节点”开始配置。" : "先添加主机和登录身份，再创建节点。")}</div>`;
  }
  if (S.page === "hosts")
    return `<div class="panel">${
      v.hosts.length
        ? table(
            ["主机", "地址", "主机信任", "关联节点", "操作"],
            v.hosts.map(
              (h) =>
                `<tr><td><strong>${esc(h.name)}</strong></td><td class="mono">${esc(h.address)}:${h.port}</td><td>${chip(h.hostKey ? "已固定" : "待确认", h.hostKey ? "good" : "warning")}<small class="fingerprint" title="${esc(h.fingerprint)}">${esc(h.fingerprint || "尚未设置主机公钥")}</small></td><td>${v.nodes.filter((n) => n.hostID === h.id).length}</td><td>${actions("hosts", h.id, `<button data-action="probe" data-id="${h.id}">配置公钥</button>${h.hostKey ? `<button class="danger-text" data-action="revoke" data-id="${h.id}">撤销信任</button>` : ""}`)}</td></tr>`,
            ),
          )
        : empty("添加第一台主机", "填写 SSH 地址，然后配置公钥并核对指纹。")
    }</div>`;
  if (S.page === "identities")
    return `<div class="panel">${
      v.identities.length
        ? table(
            ["身份", "SSH 用户", "关联凭据", "关联节点", "操作"],
            v.identities.map(
              (i) =>
                `<tr><td><strong>${esc(i.name)}</strong></td><td class="mono">${esc(i.user)}</td><td>${esc(name("credentials", i.credentialID))}</td><td>${v.nodes.filter((n) => n.identityID === i.id).length}</td><td>${actions("identities", i.id)}</td></tr>`,
            ),
          )
        : empty("添加登录身份", "将远端账号与凭据关联，供节点使用。")
    }</div>`;
  if (S.page === "credentials")
    return `<div class="banner"><span>◇</span><div><strong>凭据仅写入，不回显。</strong><p>更新凭据会撤销相关的可取消操作，后续连接使用新版本。</p></div></div><div class="panel">${
      v.credentials.length
        ? table(
            ["凭据名称", "类型", "关联身份", "操作"],
            v.credentials.map(
              (c) =>
                `<tr><td><strong>${esc(c.name)}</strong></td><td>${chip(c.kind === "key" ? "SSH 私钥" : "密码")}</td><td>${v.identities.filter((i) => i.credentialID === c.id).length}</td><td>${actions("credentials", c.id)}</td></tr>`,
            ),
          )
        : empty("添加访问凭据", "保存密码或 SSH 私钥，再将其关联到身份。")
    }</div>`;
  if (S.page === "tags")
    return `<p class="muted">独立创建和管理标签，在节点编辑中选择关联。</p><div class="tag-grid">${v.tags.length ? v.tags.map((t) => `<article class="tag-card"><span>#</span><h3>${esc(t.name)}</h3><p>${t.count} 个节点</p><button data-action="rename-tag" data-id="${t.id}" data-name="${esc(t.name)}">重命名</button><button class="danger-text" data-action="delete-tag" data-id="${t.id}" data-name="${esc(t.name)}">删除标签</button></article>`).join("") : empty("尚无标签", "点击“新建标签”即可创建，标签可以暂不关联节点。")}</div>`;
  if (S.page === "policy") return policyForm();
  if (S.page === "operations")
    return `<div class="banner"><p>这里显示当前准入操作。停用节点会撤销相关的可取消工作；已准入的提交可能继续完成，取消不代表远端已回滚。传输最终结果以任务状态与审计为准。</p></div><div class="panel" id="ops">${opsTable()}</div>`;
  if (S.page === "audit")
    return `<div class="panel"><div class="toolbar"><select id="outcome" aria-label="按结果筛选">${[
      ["", "全部结果"],
      ["executed", "操作完成"],
      ["error", "操作失败"],
      ["completed", "传输完成"],
      ["failed", "传输失败"],
      ["denied", "拒绝"],
      ["unknown", "结果未知"],
    ]
      .map(
        ([value, label]) =>
          `<option value="${value}" ${S.outcome === value ? "selected" : ""}>${label}</option>`,
      )
      .join(
        "",
      )}</select><select id="audit-node" aria-label="按节点筛选"><option value="">全部节点</option>${v.nodes.map((n) => `<option value="${n.id}" ${S.auditNode === n.id ? "selected" : ""}>${esc(n.name)}</option>`).join("")}</select><span class="count">不记录原命令、路径与自由格式错误</span></div><div id="audit-list">${auditTable()}</div><div class="pagination"><button id="more" ${S.next ? "" : "disabled"}>加载更早记录</button></div></div>`;
  return `<section class="panel form-panel"><h2>修改管理员密码</h2><p class="muted">更新后所有管理员会话失效，需要重新登录。MCP 访问凭据不受影响。</p><form id="password-form">${field("当前密码", "current", "", 'type="password" required autocomplete="current-password"')}<div class="form-grid">${field("新密码", "next", "", 'type="password" required data-admin-password aria-describedby="admin-password-help" autocomplete="new-password"')}${field("确认新密码", "confirm", "", 'type="password" required data-admin-password aria-describedby="admin-password-help" autocomplete="new-password"')}</div><p id="admin-password-help" class="hint">${adminPasswordHint}</p><p class="form-error" role="alert"></p><button class="primary">更新密码</button></form></section>`;
}
function opsTable() {
  return S.ops.length
    ? table(
        ["工具", "节点", "阶段", "状态", "开始时间"],
        S.ops.map(
          (o) =>
            `<tr><td class="mono">${esc(o.tool)}</td><td>${esc((o.nodeIDs ?? []).map((id) => name("nodes", id)).join(", ") || "—")}</td><td>${esc({ inspect: "校验连接", execute: "执行", transfer_start: "传输", commit: "提交", recovery: "恢复" }[o.phase] || o.phase)}</td><td>${chip(o.state === "ending" ? "正在结束" : "运行中", o.phase === "commit" ? "warning" : "good")}</td><td>${date(o.startedAt)}</td></tr>`,
        ),
      )
    : empty(
        "当前没有运行中的操作",
        "新的 SSH、文件操作和连接测试将在这里显示。",
      );
}
function auditTable() {
  if (S.auditLoading && !S.auditFilter)
    return empty("正在加载审计记录", "正在获取当前筛选条件下的记录。");
  if (S.auditError && !S.auditFilter)
    return (
      empty("审计记录加载失败", S.auditError) +
      '<div class="pagination"><button id="audit-retry">重试加载</button></div>'
    );
  const labels = {
    intent: "已授权",
    executed: "操作完成",
    error: "操作失败",
    denied: "拒绝",
    unknown: "结果未知",
    completed: "传输完成",
    failed: "传输失败",
    streamed: "已传输",
    cancel_requested: "请求取消",
  };
  return S.audit.length
    ? table(
        ["时间", "操作", "节点", "结果", "操作标识"],
        S.audit.map(
          ({ event: e }) =>
            `<tr><td class="nowrap">${date(e.ts)}</td><td class="mono">${esc(e.tool)}</td><td>${esc(e.node ? name("nodes", e.node) : (e.nodes || []).map((id) => name("nodes", id)).join(", ") || "—")}</td><td>${chip(labels[e.outcome] || e.outcome, ["error", "failed", "denied", "unknown"].includes(e.outcome) ? "warning" : ["executed", "completed"].includes(e.outcome) ? "good" : "neutral")}</td><td class="mono muted" title="${esc(e.op_id)}">${esc(e.op_id?.slice(0, 12))}</td></tr>`,
        ),
      )
    : empty("暂无审计记录", "配置变更和 MCP 操作会在这里留下记录。");
}
async function loadAudit(more = false) {
  const filter = JSON.stringify([S.outcome, S.auditNode]);
  if (more && (S.auditLoading || !S.next || S.auditFilter !== filter)) return;
  const id = ++auditRequest;
  if (!more) {
    S.audit = [];
    S.next = 0;
    S.auditFilter = "";
  }
  S.auditLoading = true;
  S.auditError = "";
  renderAudit();
  try {
    const { data } = await api(
      "/audit?" +
        new URLSearchParams({
          limit: "50",
          before: String(more ? S.next : 0),
          outcome: S.outcome,
          nodeID: S.auditNode,
        }),
    );
    if (id !== auditRequest) return;
    S.audit = more ? [...S.audit, ...data.entries] : data.entries;
    S.next = data.next;
    S.auditFilter = filter;
  } catch (e) {
    if (id === auditRequest) {
      S.auditError = e.message;
      toast(e.message, true);
    }
  } finally {
    if (id === auditRequest) {
      S.auditLoading = false;
      renderAudit();
    }
  }
}
function renderAudit() {
  if (S.page !== "audit" || !$("#audit-list")) return;
  $("#audit-list").innerHTML = auditTable();
  $("#more").disabled =
    S.auditLoading ||
    !S.next ||
    S.auditFilter !== JSON.stringify([S.outcome, S.auditNode]);
  $("#audit-retry")?.addEventListener("click", () => loadAudit());
}
function field(label, key, value = "", attrs = "") {
  return `<label>${label}<input name="${key}" value="${esc(value)}" ${attrs}></label>`;
}
function nameField(label, value = "") {
  return (
    field(
      label,
      "name",
      value,
      'required maxlength="256" data-name-rule="single" aria-describedby="name-help"',
    ) + `<p id="name-help" class="hint">${nameHint}</p>`
  );
}
function selectField(label, key, options, value = "", required = false) {
  return `<label>${label}<select aria-label="${esc(label)}" name="${key}" ${required ? "required" : ""}><option value="">${required ? "请选择" : "不关联"}</option>${options.map((x) => `<option value="${esc(x.id)}" ${value === x.id ? "selected" : ""}>${esc(x.name)}</option>`).join("")}</select></label>`;
}
function modal(title, html, submit, label = "保存") {
  dialog.innerHTML = `<form id="modal-form"><div class="dialog-head"><h2>${title}</h2><button type="button" data-close aria-label="关闭">×</button></div><div class="dialog-body">${html}<p class="form-error" role="alert"></p></div><div class="dialog-footer"><button type="button" data-close>取消</button><button class="primary" type="submit">${label}</button></div></form>`;
  dialog.showModal();
  const form = $("#modal-form", dialog);
  namingFields(form).forEach((input) => {
    const validate = () => validateNamingField(input);
    input.addEventListener("input", validate);
    input.addEventListener("change", validate);
    validate();
  });
  const close = () => {
    if (ownsDialog(form)) dialog.close();
  };
  dialog.querySelectorAll("[data-close]").forEach((b) => (b.onclick = close));
  form.onsubmit = async (e) => {
    e.preventDefault();
    namingFields(form).forEach(validateNamingField);
    if (!form.reportValidity()) return;
    const button = $("[type=submit]", form);
    button.disabled = true;
    $(".form-error", form).textContent = "";
    try {
      await submit(new FormData(form), form);
      close();
    } catch (error) {
      if (ownsDialog(form)) {
        $(".form-error", form).textContent =
          error.message +
          (error.status === 412 ? " 请关闭窗口，刷新后重新编辑。" : "");
        button.disabled = false;
      } else toast(error.message, true);
    }
  };
  return form;
}
function confirm(title, text, submit) {
  modal(title, `<p class="confirm-copy">${esc(text)}</p>`, submit, "确认");
}
function edit(kind, id) {
  const item = id ? find(kind, id) : {},
    base = S.etag,
    label = {
      hosts: "主机",
      identities: "身份",
      nodes: "节点",
      credentials: "凭据",
      tags: "标签",
    }[kind];
  let html = nameField("名称", item.name);
  if (kind === "hosts")
    html += `<div class="form-grid">${field("SSH 地址", "address", item.address, 'required placeholder="192.0.2.10 或主机名"')}${field("端口", "port", item.port || 22, 'type="number" min="1" max="65535" required')}</div><p class="hint">保存后通过“配置公钥”核对信任。修改地址会保留已固定公钥。</p>`;
  if (kind === "identities")
    html +=
      field("SSH 用户名", "user", item.user, "required") +
      selectField(
        "登录凭据",
        "credentialID",
        S.v.credentials,
        item.credentialID,
      );
  const jumpRow = (value) =>
    `<div class="jump-row">${selectField(
      "跳板",
      "jumpID",
      S.v.nodes.filter((n) => n.id !== id),
      value,
    )}<button type="button" data-remove-jump aria-label="移除跳板">×</button></div>`;
  if (kind === "nodes")
    html += `<div class="form-grid">${selectField("主机", "hostID", S.v.hosts, item.hostID, true)}${selectField("登录身份", "identityID", S.v.identities, item.identityID, true)}</div><label>别名<textarea name="aliases" rows="3" data-name-rule="aliases" aria-label="别名" aria-describedby="aliases-help" spellcheck="false" autocomplete="off" autocapitalize="off" placeholder="每行一个别名">${esc((item.aliases || []).join("\n"))}</textarea></label><p id="aliases-help" class="hint">每行一个别名，空行忽略。${nameHint}</p><fieldset class="tag-options"><legend>标签</legend>${S.v.tags.map((tag) => `<label class="check"><input type="checkbox" name="tagIDs" value="${esc(tag.id)}" ${(item.tagIDs || []).includes(tag.id) ? "checked" : ""}>${esc(tag.name)}</label>`).join("") || '<p class="hint">暂无标签，请先在“标签”页面创建。</p>'}</fieldset><div id="jumps">${(item.jumpIDs || []).map(jumpRow).join("")}</div><button id="add-jump" type="button">＋ 添加一跳</button><p class="hint">按从本服务到目标的连接顺序添加；空白表示直接连接。</p><div class="form-grid"><label>提权方式<select aria-label="提权方式" name="sudoMode">${["none", "sudo", "sudoer", "su", "root"].map((x) => `<option ${(item.sudoMode || "none") === x ? "selected" : ""}>${x}</option>`).join("")}</select></label>${selectField(
      "提权凭据",
      "privilegeCredentialID",
      S.v.credentials.filter((c) => c.kind === "password"),
      item.privilegeCredentialID,
    )}</div><label class="check"><input type="checkbox" name="enabled" ${id && !item.disabled ? "checked" : ""}>启用节点</label><p class="hint">启用前需配置凭据和主机公钥。未启用的跳板会阻止下游节点操作。</p>`;
  if (kind === "credentials")
    html += `<label>类型<select aria-label="类型" name="kind"><option value="password" ${item.kind !== "key" ? "selected" : ""}>密码</option><option value="key" ${item.kind === "key" ? "selected" : ""}>SSH 私钥</option></select></label><div id="secret-fields"></div><p class="hint">${id ? "留空保留已有材料；填写后会创建新版本。" : "凭据加密保存，创建后不会回显。"}</p>`;
  const form = modal(`${id ? "编辑" : "新建"}${label}`, html, async (data) => {
    const x = Object.fromEntries(data);
    let body;
    if (kind === "tags") body = { name: x.name };
    if (kind === "hosts")
      body = { name: x.name, address: x.address, port: Number(x.port) };
    if (kind === "identities")
      body = { name: x.name, user: x.user, credentialID: x.credentialID };
    if (kind === "nodes")
      body = {
        name: x.name,
        hostID: x.hostID,
        identityID: x.identityID,
        aliases: aliasLines(x.aliases),
        tagIDs: data.getAll("tagIDs"),
        jumpIDs: data.getAll("jumpID").filter(Boolean),
        disabled: !data.has("enabled"),
        sudoMode: x.sudoMode,
        privilegeCredentialID: x.privilegeCredentialID,
      };
    if (kind === "credentials") {
      body = { name: x.name, kind: x.kind };
      if (x.password || x.privateKey || x.passphrase || !id)
        body.secret = {
          password: x.password || "",
          privateKey: x.privateKey || "",
          passphrase: x.passphrase || "",
        };
    }
    await mutate(
      "/" + kind + (id ? "/" + id : ""),
      {
        method: id ? "PUT" : "POST",
        etag: base,
        body,
      },
      `${label}已保存`,
    );
  });
  if (kind === "credentials") {
    const fields = () => {
      $("#secret-fields", form).innerHTML =
        form.elements.kind.value === "key"
          ? '<label>私钥内容<textarea name="privateKey" rows="7" autocomplete="off" spellcheck="false" placeholder="粘贴 PEM / OpenSSH 私钥"></textarea></label>' +
            field(
              "私钥口令（可选）",
              "passphrase",
              "",
              'type="password" autocomplete="new-password"',
            )
          : field(
              "密码",
              "password",
              "",
              'type="password" autocomplete="new-password"',
            );
    };
    fields();
    form.elements.kind.onchange = fields;
  }
  if (kind === "nodes") {
    $("#add-jump", form).onclick = () =>
      $("#jumps", form).insertAdjacentHTML("beforeend", jumpRow(""));
    $("#jumps", form).onclick = (e) => {
      if (e.target.matches("[data-remove-jump]"))
        e.target.closest(".jump-row").remove();
    };
  }
}
function probe(id) {
  const host = find("hosts", id),
    base = S.etag;
  let observed = null,
    previewRequest = 0;
  const form = modal(
    "确认主机公钥",
    `<p class="muted">${esc(host.name)} · ${esc(host.address)}:${host.port}</p><label>公钥来源<select name="keySource"><option value="network">直接连接主机获取</option><option value="manual">输入已独立核验的公钥</option></select></label><p class="hint">仅经跳板可达的主机，请输入从可信渠道获取的公钥。</p><div id="network-key"><label>算法<select aria-label="算法" name="algorithm"><option value="">自动选择安全算法</option><option value="ssh-ed25519">Ed25519</option><option value="ecdsa-sha2-nistp256">ECDSA P-256</option><option value="rsa-sha2-512">RSA SHA-512</option><option value="rsa-sha2-256">RSA SHA-256</option></select></label></div><div id="manual-key" hidden><label>主机公钥<textarea name="hostKey" rows="3" spellcheck="false" placeholder="ssh-ed25519 AAAA…" aria-describedby="manual-key-help"></textarea></label><p id="manual-key-help" class="hint">粘贴单个 SSH 公钥（.pub 文件内容）。请通过主机控制台或可信管理员核对指纹。</p></div><button type="button" id="fetch-key">连接并获取公钥</button><div id="key-result"></div>`,
    async () => {
      if (!observed || !$("#verified", form).checked)
        throw new Error("请先预览公钥并独立核对指纹");
      await mutate(
        "/hosts/" + id + "/trust",
        {
          method: "POST",
          etag: base,
          body: { probeID: observed.probeID },
        },
        "主机公钥已固定",
      );
    },
    "确认并固定",
  );
  $("[type=submit]", form).disabled = true;
  const clearPreview = () => {
    previewRequest++;
    observed = null;
    $("[type=submit]", form).disabled = true;
    $("#key-result", form).replaceChildren();
    $(".form-error", form).textContent = "";
  };
  form.elements.keySource.onchange = () => {
    const manual = form.elements.keySource.value === "manual";
    $("#manual-key", form).hidden = !manual;
    $("#network-key", form).hidden = manual;
    $("#fetch-key", form).textContent = manual
      ? "预览公钥指纹"
      : "连接并获取公钥";
    clearPreview();
  };
  form.elements.hostKey.oninput = clearPreview;
  form.elements.algorithm.onchange = clearPreview;
  $("#fetch-key", form).onclick = async () => {
    clearPreview();
    const button = $("#fetch-key", form),
      request = previewRequest,
      manual = form.elements.keySource.value === "manual";
    button.disabled = true;
    try {
      const { data } = await api(
        "/hosts/" + id + (manual ? "/key-preview" : "/probe"),
        {
          method: "POST",
          etag: base,
          body: manual
            ? { hostKey: form.elements.hostKey.value.trim() }
            : { algorithm: form.elements.algorithm.value },
        },
      );
      if (!ownsDialog(form) || request !== previewRequest) return;
      observed = data;
      $("#key-result", form).innerHTML =
        `<div class="key-preview"><small>待核对指纹 · ${esc(data.algorithm)}</small><strong class="mono">${esc(data.fingerprint)}</strong><textarea readonly rows="3" aria-label="待确认的主机公钥">${esc(data.hostKey)}</textarea></div>${host.fingerprint && host.fingerprint !== data.fingerprint ? '<p class="form-error">公钥与当前固定公钥不同，请核实轮换原因。</p>' : ""}<label class="check"><input id="verified" type="checkbox">已通过独立渠道核对指纹</label><p class="hint">两分钟内有效。预览公钥本身不会建立信任。</p>`;
      $("#verified", form).onchange = (e) =>
        ($("[type=submit]", form).disabled = !e.target.checked);
    } catch (e) {
      if (ownsDialog(form) && request === previewRequest)
        $(".form-error", form).textContent = e.message;
    } finally {
      if (ownsDialog(form)) button.disabled = false;
    }
  };
}
function policyForm() {
  const p = S.v.policy,
    thresholds = [
      ["safe", "所有操作"],
      ["moderate", "中等风险及以上"],
      ["dangerous", "高风险操作"],
    ],
    options = (values, value) =>
      values
        .map(
          ([v, label]) =>
            `<option value="${v}" ${v === value ? "selected" : ""}>${label}</option>`,
        )
        .join("");
  return `<form id="policy-form" class="panel form-panel"><h2>风险确认</h2><label class="check"><input name="enabled" type="checkbox" ${p.enabled ? "checked" : ""}>启用操作护栏</label><div class="form-grid"><label>开始确认的风险级别<select name="approvalThreshold">${options(thresholds, p.approvalThreshold || "dangerous")}</select></label><label>客户端无法确认时<select name="noElicitFallback">${options(
    [
      ["deny", "拒绝需要确认的操作"],
      ["downgrade", "仅允许中等风险操作"],
      ["allow", "允许执行"],
    ],
    p.noElicitFallback || "deny",
  )}</select></label></div><div class="form-grid"><label>额外拦截的命令模式<textarea name="blockedPatterns" rows="5" aria-label="额外拦截的命令模式" aria-describedby="blocked-patterns-help" placeholder="每行一个 glob 模式，如 hostname 或 hostname*">${esc((p.blockedPatterns || []).join("\n"))}</textarea><small id="blocked-patterns-help">使用 glob 通配符匹配完整命令，支持 *、?、[abc]；* 和 ? 不跨越路径分隔符。hostname 精确匹配，hostname* 匹配前缀；不使用 ^...$ 正则语法。</small></label><label>受保护路径<textarea name="protectedPaths" rows="5" placeholder="每行一个路径">${esc((p.protectedPaths || []).join("\n"))}</textarea></label></div><h2>节点确认级别</h2><div class="override-list">${S.v.nodes.map((n) => `<label>${esc(n.name)}<select name="override:${n.id}"><option value="">使用全局规则</option>${options(thresholds, p.nodes[n.id])}</select></label>`).join("") || '<p class="muted">添加节点后可设置单独规则。</p>'}</div><p class="form-error" role="alert"></p><div class="form-bottom"><p class="hint">策略变更会撤销相关可取消操作，已准入提交仍按原期限结算。</p><button class="primary">保存策略</button></div></form>`;
}
function bind() {
  $("#page")
    .querySelectorAll("form")
    .forEach((form) => {
      formVersions.set(form, 0);
      const changed = () => formVersions.set(form, formVersions.get(form) + 1);
      form.addEventListener("input", changed);
      form.addEventListener("change", changed);
    });
  $("#search")?.addEventListener("input", (e) => {
    const position = e.target.selectionStart;
    S.search = e.target.value;
    $("#page").innerHTML = content();
    bind();
    $("#search").focus();
    $("#search").setSelectionRange(position, position);
  });
  $("#tag-filter")?.addEventListener("change", (e) => {
    S.tag = e.target.value;
    $("#page").innerHTML = content();
    bind();
  });
  $("#outcome")?.addEventListener("change", (e) => {
    S.outcome = e.target.value;
    loadAudit();
  });
  $("#audit-node")?.addEventListener("change", (e) => {
    S.auditNode = e.target.value;
    loadAudit();
  });
  $("#more")?.addEventListener("click", () => loadAudit(true));
  const policy = $("#policy-form");
  if (policy) {
    const base = S.etag;
    policy.onsubmit = async (e) => {
      e.preventDefault();
      const x = Object.fromEntries(new FormData(policy)),
        nodes = {};
      for (const [k, v] of Object.entries(x))
        if (k.startsWith("override:") && v) nodes[k.slice(9)] = v;
      const button = $("button.primary", policy);
      const submitted = formSnapshot(policy);
      button.disabled = true;
      $(".form-error", policy).textContent = "";
      try {
        await mutate(
          "/policy",
          {
            method: "PUT",
            etag: base,
            body: {
              enabled: !!x.enabled,
              approvalThreshold: x.approvalThreshold,
              noElicitFallback: x.noElicitFallback,
              blockedPatterns: x.blockedPatterns.split("\n").filter(Boolean),
              protectedPaths: x.protectedPaths.split("\n").filter(Boolean),
              nodes,
            },
          },
          "策略已更新",
          submitted,
        );
      } catch (error) {
        if (policy.isConnected)
          $(".form-error", policy).textContent = error.message;
        else toast(error.message, true);
      } finally {
        if (policy.isConnected) button.disabled = false;
      }
    };
  }
  const password = $("#password-form");
  if (password) {
    bindAdminPasswords(password);
    password.onsubmit = async (e) => {
      e.preventDefault();
      if (!checkAdminPasswords(password)) return;
      const x = Object.fromEntries(new FormData(password));
      if (x.next !== x.confirm) {
        $(".form-error", password).textContent = "两次输入的新密码不一致";
        return;
      }
      const button = $("button", password);
      button.disabled = true;
      try {
        await api("/auth/password", {
          method: "PUT",
          body: { current: x.current, next: x.next },
        });
        S.session = null;
        toast("密码已更新，请重新登录");
        await boot();
      } catch (error) {
        $(".form-error", password).textContent = error.message;
        button.disabled = false;
      }
    };
  }
}
document.addEventListener("click", async (e) => {
  const button = e.target.closest("[data-action]");
  if (!button) return;
  const { action, kind, id, name: tag } = button.dataset;
  try {
    if (["create", "edit"].includes(action)) {
      edit(kind, id);
      return;
    }
    if (action === "probe") {
      probe(id);
      return;
    }
    if (action === "delete") {
      const base = S.etag;
      confirm(
        "删除记录",
        `确认删除“${name(kind, id)}”？仍被其他记录引用时会拒绝删除。`,
        async () => {
          await mutate(
            "/" + kind + "/" + id,
            { method: "DELETE", etag: base },
            "记录已删除",
          );
        },
      );
      return;
    }
    if (action === "revoke") {
      const base = S.etag;
      confirm(
        "撤销主机信任",
        "关联节点将被停用，相关可取消操作会被撤销；远端已经开始的工作不保证回滚。",
        async () => {
          await mutate(
            "/hosts/" + id + "/trust",
            {
              method: "DELETE",
              etag: base,
            },
            "主机信任已撤销",
          );
        },
      );
      return;
    }
    if (action === "rename-tag") {
      const base = S.etag;
      modal("重命名标签", nameField("新名称", tag), async (data) => {
        await mutate(
          "/tags/" + encodeURIComponent(id),
          {
            method: "PUT",
            etag: base,
            body: { name: data.get("name") },
          },
          "标签已更新",
        );
      });
      return;
    }
    if (action === "delete-tag") {
      const base = S.etag;
      confirm(
        "删除标签",
        `删除“${tag}”并解除所有节点关联，节点本身不会被删除。`,
        async () => {
          await mutate(
            "/tags/" + encodeURIComponent(id),
            {
              method: "DELETE",
              etag: base,
            },
            "标签已删除",
          );
        },
      );
      return;
    }
    button.disabled = true;
    if (action === "refresh") {
      await refreshInventory(formSnapshot());
    }
    if (action === "logout") {
      await api("/auth/logout", { method: "POST", body: {} });
      S.session = null;
      S.audit = [];
      await boot();
    }
    if (action === "reconcile") {
      await mutate(
        "/reconcile",
        { method: "POST", body: {} },
        "已保存配置已应用",
      );
    }
    if (action === "test") {
      const { data } = await api("/nodes/" + id + "/test", {
        method: "POST",
        body: {},
      });
      toast(`SSH 连接成功 · ${data.elapsedMS} ms`);
    }
    if (action === "toggle") {
      const { id: ignored, status, ...body } = find("nodes", id);
      body.disabled = !body.disabled;
      await mutate(
        "/nodes/" + id,
        { method: "PUT", body },
        body.disabled ? "节点已停用" : "节点已启用",
      );
    }
  } catch (error) {
    toast(error.message, true);
  } finally {
    button.disabled = false;
  }
});
window.addEventListener("hashchange", () => {
  S.search = "";
  render();
});
setInterval(async () => {
  if (!S.session || document.visibilityState !== "visible") return;
  try {
    const { data } = await api("/operations");
    S.ops = data.operations;
    if (S.page === "operations" && $("#ops")) $("#ops").innerHTML = opsTable();
  } catch (e) {
    if (e.status !== 401) toast("运行状态暂时无法刷新", true);
  }
}, 5000);
boot();
