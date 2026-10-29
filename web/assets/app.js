import { t as tr, locale, saveLocale } from "./i18n.js";
import { encryptRequest } from "./auth.js";
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
const tokenStorageKey = "xops.admin.token:" + apiRoot.href;
let accessToken = "", authGeneration = 0, sessionRequest = 0;
try { accessToken = sessionStorage.getItem(tokenStorageKey) || ""; } catch { /* private browsing may disable storage */ }
function setAccessToken(token) {
  // Token changes invalidate every authentication result from the old state,
  // including transitions where both the old and new token are empty.
  authGeneration++;
  accessToken = token;
  S.tokens = null;
  tokenRequest++;
  try { if (token) sessionStorage.setItem(tokenStorageKey, token); else sessionStorage.removeItem(tokenStorageKey); } catch { /* memory-only login still works */ }
}
const S = {
  session: null,
  tokens: null,
  tokenError: "",
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
  nodes: [tr("节点总览"), tr("集中管理连接目标，让每次操作都指向正确的主机。"), "▦"],
  hosts: [tr("主机与信任"), tr("维护连接地址，并独立核对 SSH 主机公钥。"), "▤"],
  identities: [tr("登录身份"), tr("将远端账号与凭据关联，在节点间安全复用。"), "♙"],
  credentials: [tr("凭据"), tr("密码与私钥加密保存，仅显示配置元数据。"), "◇"],
  tags: [tr("标签"), tr("按环境或用途组织节点。"), "#"],
  policy: [tr("操作策略"), tr("管理风险确认、命令拦截与受保护路径。"), "◎"],
  operations: [tr("运行中"), tr("查看当前准入操作及其执行阶段。"), "↗"],
  audit: [tr("审计记录"), tr("查看授权、配置变更与执行结果。"), "≡"],
  tokens: ["MCP Token", tr("为客户端分别创建访问凭据，独立设置节点范围、有效期和启停状态。"), "⚿"],
  account: [tr("账户"), tr("管理管理员密码和当前会话。"), "⚙"],
};
const dialog = $("#dialog"),
  find = (k, id) => S.v[k].find((x) => x.id === id),
  name = (k, id) =>
    S.v?.[k]?.find((x) => x.id === id)?.name ||
    (id ? id.slice(0, 12) : tr("未关联"));
dialog.addEventListener("close", () => {
  // A queued close event may belong to a form replaced in the same turn.
  if (!dialog.open) dialog.replaceChildren();
});
function ownsDialog(form) {
  return dialog.open && dialog.firstElementChild === form;
}
function languageControl() {
  return `<label class="language-control"><span>${tr("语言")}</span><select data-locale aria-label="${tr("控制台语言")}"><option value="zh-CN" ${locale === "zh-CN" ? "selected" : ""}>中文</option><option value="en" ${locale === "en" ? "selected" : ""}>English</option></select></label>`;
}
const drafts = new WeakMap();
function draftState(form) {
  return JSON.stringify([...form.elements].filter(field => field.name).map(field => [field.name, field.value, field.checked]));
}
function rememberDrafts() {
  document.querySelectorAll("#auth-form, #policy-form, #password-form").forEach(form => {
    if (!drafts.has(form)) drafts.set(form, draftState(form));
  });
}
document.addEventListener("change", event => {
  if (!event.target.matches("[data-locale]")) return;
  const dirty = [...document.querySelectorAll("#auth-form, #policy-form, #password-form")].some(form =>
    drafts.get(form) !== draftState(form) || form.querySelector('button.primary:disabled'));
  if (dialog.open || dirty) {
    event.target.value = locale;
    toast(tr("请先保存或放弃当前编辑，再切换语言。"), true);
    return;
  }
  const url = new URL(location.href);
  url.searchParams.set("lang", saveLocale(event.target.value));
  location.assign(url.href);
});
const date = (v) => new Date(v).toLocaleString(locale, { hour12: false });
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
  tr("密码需为 12–72 个 UTF-8 字节，不能包含换行或空字符。");
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
  auditRequest = 0,
  tokenRequest = 0;
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
function formatDiagnosticSummary(diagnostics) {
  if (!Array.isArray(diagnostics) || diagnostics.length === 0) return "";
  const last = diagnostics[diagnostics.length - 1];
  const parts = [];
  if (last.operation) parts.push(last.operation);
  if (last.outcome) parts.push(last.outcome);
  if (last.serverVersion) parts.push(last.serverVersion);
  if (last.keyExchange) parts.push(last.keyExchange);
  return parts.join(" · ");
}
async function api(path, { method = "GET", body, etag, signal } = {}) {
  const token = accessToken, generation = authGeneration;
  if (body !== undefined && ["/auth/login", "/auth/setup", "/auth/password"].includes(path))
    body = await encryptRequest(apiRoot, path.split("/").pop(), body, token);
  const headers = { Accept: "application/json", "Accept-Language": locale };
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (!["GET", "HEAD"].includes(method) && !path.startsWith("/auth/"))
    headers["If-Match"] = etag ?? S.etag;
  if (token) headers.Authorization = "Bearer " + token;
  const response = await fetch(new URL(path.slice(1), apiRoot), {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    signal,
    credentials: "omit",
    cache: "no-store",
  });
  const data =
    response.status === 204
      ? {}
      : await response.json().catch(() => ({ message: tr("服务响应不正确") }));
  if (!response.ok) {
    if (
      response.status === 401 && generation === authGeneration && token === accessToken &&
      !["/auth/login", "/auth/setup", "/auth/session"].includes(path)
    ) {
      setAccessToken("");
      S.session = null;
      S.v = null;
      if (dialog.open) dialog.close();
      authPage({});
    }
    const e = new Error(data.message || tr("请求失败"));
    e.status = response.status;
    e.diagnostics = data.diagnostics;
    throw e;
  }
  return { data, etag: response.headers.get("ETag") };
}
async function load() {
  const id = ++inventoryRequest, generation = authGeneration;
  const [v, ops] = await Promise.all([api("/inventory"), api("/operations")]);
  if (id !== inventoryRequest || generation !== authGeneration) return;
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
      toast(tr("变更已完成，但页面刷新失败，请刷新查看：") + error.message, true);
  }
}
function updateStatus() {
  if ($("#publication-status"))
    $("#publication-status").innerHTML = S.v.pending
      ? `<div class="banner warning"><div><strong>${tr("配置已保存，仍待应用。")}</strong><p>${tr("受影响的新操作暂时暂停。重试只应用已保存的配置。")}</p></div><button data-action="reconcile">${tr("重试应用")}</button></div>`
      : "";
  if ($("#config-revision"))
    $("#config-revision").textContent = tr("配置版本 ") + S.v.revision;
}
async function boot() {
  const generation = authGeneration, request = ++sessionRequest;
  const current = () => generation === authGeneration && request === sessionRequest;
  try {
    const { data } = await api("/auth/session");
    if (!current()) return;
    if (!data.authenticated) {
      setAccessToken("");
      S.session = null;
      authPage(data);
      return;
    }
    S.session = data;
    await load();
    if (!current()) return;
    render();
  } catch (e) {
    if (!current()) return;
    $("#app").innerHTML =
      `<main class="unavailable"><div class="brand-mark">X</div><h1>${tr("暂时无法连接")}</h1><p>${esc(e.message)}</p><button id="retry" class="primary">${tr("重新连接")}</button></main>`;
    $("#retry").onclick = boot;
  }
}
function authPage(info) {
  resetAuditResults();
  const setup = info.setupRequired;
  $("#app").innerHTML =
    `<main class="auth-layout"><section class="auth-story"><a class="brand"><span class="brand-mark">X</span>XOps <small>CONTROL PLANE</small></a><div><span class="eyebrow">YOUR INFRASTRUCTURE. ONE PLACE.</span><h1>${tr("掌握每一个")}<br>${tr("连接与操作。")}</h1><p>${tr("节点、信任与访问凭据统一管理。")}<br>${tr("从可控的连接开始，让运维井然有序。")}</p><div class="auth-lines">━━ ━ ━</div></div><small>${tr("自托管 · 单实例 · SSH / SFTP / MCP")}</small></section><section class="auth-panel"><div class="auth-box">${languageControl()}<div class="eyebrow">XOPS CONSOLE</div><h2>${setup ? tr("初始化管理员") : tr("欢迎回来")}</h2><p class="muted">${setup ? tr("为这台服务创建唯一的管理员账户。") : tr("登录以管理节点与访问策略。")}</p>${setup && !info.setupEnabled ? `<div class="banner">${tr("请在服务器使用 admin-init 命令初始化管理员，或配置专用 Web 初始化凭据后重启。")}</div><button id="retry-setup">${tr("重新检查")}</button>` : `<form id="auth-form">${field(tr("用户名"), "username", "admin", 'required autocomplete="username" maxlength="64"')}${field(tr("密码"), "password", "", 'type="password" required ' + (setup ? 'data-admin-password aria-describedby="admin-password-help" autocomplete="new-password"' : 'maxlength="72" autocomplete="current-password"'))}${setup ? field(tr("确认密码"), "confirm", "", 'type="password" required data-admin-password aria-describedby="admin-password-help" autocomplete="new-password"') + `<p id="admin-password-help" class="hint">${adminPasswordHint}</p>` + field(tr("初始化凭据"), "token", "", 'type="password" required autocomplete="off" minlength="32"') : ""}<p class="form-error" role="alert"></p><button class="primary wide">${setup ? tr("创建管理员") : tr("登录控制台")} <span aria-hidden="true">→</span></button></form>`}<small class="auth-footer">${tr("管理员会话与 MCP 访问凭据相互独立。")}</small></div></section></main>`;
  $("#retry-setup")?.addEventListener("click", boot);
  const form = $("#auth-form");
  if (!form) return;
  bindAdminPasswords(form);
  rememberDrafts();
  form.onsubmit = async (e) => {
    e.preventDefault();
    if (!checkAdminPasswords(form)) return;
    const x = Object.fromEntries(new FormData(form)),
      button = $("button", form);
    $(".form-error", form).textContent = "";
    if (setup && x.password !== x.confirm) {
      $(".form-error", form).textContent = tr("两次输入的密码不一致");
      return;
    }
    button.disabled = true;
    // A new login attempt supersedes a pending session check immediately,
    // before its encrypted request or response has completed.
    let generation = ++authGeneration;
    try {
      if (setup) {
        await api("/auth/setup", {
          method: "POST",
          body: { username: x.username, password: x.password, token: x.token },
        });
        if (generation !== authGeneration) return;
        toast(tr("管理员已创建"));
      }
      const { data } = await api("/auth/login", {
        method: "POST",
        body: { username: x.username, password: x.password },
      });
      if (generation !== authGeneration) return;
      setAccessToken(data.accessToken);
      generation = authGeneration;
      S.session = data;
      form.reset();
      await load();
      if (generation !== authGeneration) return;
      render();
    } catch (error) {
      if (generation !== authGeneration || !form.isConnected) return;
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
  return `<div class="row-actions">${extra}<button data-action="edit" data-kind="${k}" data-id="${id}">${tr("编辑")}</button><button class="danger-text" data-action="delete" data-kind="${k}" data-id="${id}">${tr("删除")}</button></div>`;
}
function render() {
  if (!S.session || !S.v) return;
  S.page = pages[location.hash.slice(1)] ? location.hash.slice(1) : "nodes";
  const [title, description] = pages[S.page];
  document.title = title + " · XOps";
  $("#app").innerHTML =
    `<div class="shell"><aside class="sidebar"><a class="brand" href="#nodes"><span class="brand-mark">X</span>XOps</a><div class="workspace"><i class="dot"></i><div>${tr("运维工作空间")}<small>${tr("自托管服务")}</small></div></div><div class="nav-label">${tr("资源管理")}</div><nav>${Object.entries(
      pages,
    )
      .map(
        ([p, [label, , icon]]) =>
          `${p === "operations" ? `<div class="nav-label">${tr("可观测性")}</div>` : p === "tokens" ? `<div class="nav-label">${tr("设置")}</div>` : ""}<a href="#${p}" class="nav-item ${S.page === p ? "active" : ""}"><span>${icon}</span>${label}</a>`,
      )
      .join(
        "",
      )}</nav><div class="sidebar-bottom"><div class="avatar">A</div><div>${esc(S.session.username)}<small>${tr("管理员")}</small></div><button data-action="logout" aria-label="${tr("退出登录")}" title="${tr("退出登录")}">↪</button></div></aside><main class="main"><header class="topbar"><div>${tr("控制台")} <span class="slash">/</span> ${title}</div><div class="topbar-right">${languageControl()}<span class="online"><i class="dot"></i>${tr("服务在线")}</span><button class="quiet" data-action="refresh">${tr("↻ 刷新")}</button><button class="mobile-logout quiet" data-action="logout" aria-label="${tr("退出登录")}">↪</button></div></header><div class="content"><div class="page-heading"><div><div class="eyebrow">${S.page === "nodes" ? "INFRASTRUCTURE" : "WORKSPACE"}</div><h1>${title}</h1><p>${description}</p></div>${["nodes", "hosts", "identities", "credentials", "tags", "tokens"].includes(S.page) ? `<button class="primary" data-action="create" data-kind="${S.page}">${tr("＋ 新建")}${{ nodes: tr("节点"), hosts: tr("主机"), identities: tr("身份"), credentials: tr("凭据"), tags: tr("标签"), tokens: "Token" }[S.page]}</button>` : ""}</div><div id="publication-status"></div><section id="page">${content()}</section><footer><span>${tr("XOps MCP · 本地掌控，安全连接")}</span><span id="config-revision"></span></footer></div></main></div>`;
  bind();
  updateStatus();
  if (S.page === "audit") loadAudit();
  if (S.page === "tokens") loadTokens();
}
function content() {
  const v = S.v;
  if (S.page === "tokens") return tokenTable();
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
          ready: [tr("可用"), "good"],
          disabled: [tr("未启用"), "neutral"],
          blocked: [tr("跳板不可用"), "warning"],
        }[n.status];
      return `<tr><td><button class="name-button" data-action="edit" data-kind="nodes" data-id="${n.id}"><span class="node-icon">⌘</span><span>${esc(n.name)}<small>${esc((n.aliases || []).join(" · ") || tr("无别名"))}</small></span></button></td><td><span class="mono">${esc(h?.address)}:${h?.port}</span><small>${esc(i?.user)} · ${esc(h?.name)}</small></td><td>${chip(...status)}</td><td>${(n.tagIDs || []).map((id) => chip(name("tags", id))).join(" ") || "—"}</td><td>${actions("nodes", n.id, `<button data-action="test" data-id="${n.id}" ${n.disabled ? "disabled" : ""}>${tr("测试连接")}</button><button data-action="test-privilege" data-id="${n.id}" ${n.disabled ? "disabled" : ""}>${tr("测试提权")}</button><button data-action="toggle" data-id="${n.id}">${n.disabled ? tr("启用") : tr("停用")}</button>`)}</td></tr>`;
    });
    return `<div class="metrics"><div class="metric"><span>${tr("全部节点")}</span><strong>${v.nodes.length}</strong><small>${tr("统一管理的连接目标")}</small></div><div class="metric"><span>${tr("可用节点")}</span><strong class="accent">${v.nodes.filter((n) => n.status === "ready").length}</strong><small><i class="dot"></i>${tr("凭据与信任已配置")}</small></div><div class="metric"><span>${tr("主机")}</span><strong>${v.hosts.length}</strong><small>${tr("已固定公钥")} ${v.hosts.filter((h) => h.hostKey).length} ${tr("台")}</small></div><div class="metric"><span>${tr("运行中")}</span><strong>${S.ops.length}</strong><small>${tr("当前准入操作")}</small></div></div><div class="panel"><div class="toolbar"><div class="search"><span>⌕</span><input id="search" aria-label="${tr("搜索节点")}" placeholder="${tr("搜索名称、地址或标签…")}" value="${esc(S.search)}"></div><select id="tag-filter" aria-label="${tr("按标签筛选")}"><option value="">${tr("全部标签")}</option>${v.tags.map((t) => `<option value="${esc(t.id)}" ${S.tag === t.id ? "selected" : ""}>${esc(t.name)}</option>`).join("")}</select><span class="count">${nodes.length} ${tr("个节点")}</span></div>${rows.length ? table([tr("节点"), tr("连接目标"), tr("状态"), tr("标签"), tr("操作")], rows) : empty(tr("还没有匹配的节点"), v.hosts.length && v.identities.length ? tr("点击“新建节点”开始配置。") : tr("先添加主机和登录身份，再创建节点。"))}</div>`;
  }
  if (S.page === "hosts")
    return `<div class="panel">${
      v.hosts.length
        ? table(
            [tr("主机"), tr("地址"), tr("主机信任"), tr("关联节点"), tr("操作")],
            v.hosts.map(
              (h) =>
                `<tr><td><strong>${esc(h.name)}</strong></td><td class="mono">${esc(h.address)}:${h.port}</td><td>${chip(h.hostKey ? tr("已固定") : tr("待确认"), h.hostKey ? "good" : "warning")}<small class="fingerprint" title="${esc(h.fingerprint)}">${esc(h.fingerprint || tr("尚未设置主机公钥"))}</small></td><td>${v.nodes.filter((n) => n.hostID === h.id).length}</td><td>${actions("hosts", h.id, `<button data-action="probe" data-id="${h.id}">${tr("配置公钥")}</button>${h.hostKey ? `<button class="danger-text" data-action="revoke" data-id="${h.id}">${tr("撤销信任")}</button>` : ""}`)}</td></tr>`,
            ),
          )
        : empty(tr("添加第一台主机"), tr("填写 SSH 地址，然后配置公钥并核对指纹。"))
    }</div>`;
  if (S.page === "identities")
    return `<div class="panel">${
      v.identities.length
        ? table(
            [tr("身份"), tr("SSH 用户"), tr("关联凭据"), tr("关联节点"), tr("操作")],
            v.identities.map(
              (i) =>
                `<tr><td><strong>${esc(i.name)}</strong></td><td class="mono">${esc(i.user)}</td><td>${esc(name("credentials", i.credentialID))}</td><td>${v.nodes.filter((n) => n.identityID === i.id).length}</td><td>${actions("identities", i.id)}</td></tr>`,
            ),
          )
        : empty(tr("添加登录身份"), tr("将远端账号与凭据关联，供节点使用。"))
    }</div>`;
  if (S.page === "credentials")
    return `<div class="banner"><span>◇</span><div><strong>${tr("凭据仅写入，不回显。")}</strong><p>${tr("更新凭据会撤销相关的可取消操作，后续连接使用新版本。")}</p></div></div><div class="panel">${
      v.credentials.length
        ? table(
            [tr("凭据名称"), tr("类型"), tr("关联身份"), tr("操作")],
            v.credentials.map(
              (c) =>
                `<tr><td><strong>${esc(c.name)}</strong></td><td>${chip(c.kind === "key" ? tr("SSH 私钥") : tr("密码"))}</td><td>${v.identities.filter((i) => i.credentialID === c.id).length}</td><td>${actions("credentials", c.id)}</td></tr>`,
            ),
          )
        : empty(tr("添加访问凭据"), tr("保存密码或 SSH 私钥，再将其关联到身份。"))
    }</div>`;
  if (S.page === "tags")
    return `<p class="muted">${tr("独立创建和管理标签，在节点编辑中选择关联。")}</p><div class="tag-grid">${v.tags.length ? v.tags.map((t) => `<article class="tag-card"><span>#</span><h3>${esc(t.name)}</h3><p>${t.count} ${tr("个节点")}</p><button data-action="rename-tag" data-id="${t.id}" data-name="${esc(t.name)}">${tr("重命名")}</button><button class="danger-text" data-action="delete-tag" data-id="${t.id}" data-name="${esc(t.name)}">${tr("删除标签")}</button></article>`).join("") : empty(tr("尚无标签"), tr("点击“新建标签”即可创建，标签可以暂不关联节点。"))}</div>`;
  if (S.page === "policy") return policyForm();
  if (S.page === "operations")
    return `<div class="banner"><p>${tr("这里显示当前准入操作。停用节点会撤销相关的可取消工作；已准入的提交可能继续完成，取消不代表远端已回滚。传输最终结果以任务状态与审计为准。")}</p></div><div class="panel" id="ops">${opsTable()}</div>`;
  if (S.page === "audit")
    return `<div class="panel"><div class="toolbar"><select id="outcome" aria-label="${tr("按结果筛选")}">${[
      ["", tr("全部结果")],
      ["executed", tr("操作完成")],
      ["error", tr("操作失败")],
      ["completed", tr("传输完成")],
      ["failed", tr("传输失败")],
      ["denied", tr("拒绝")],
      ["unknown", tr("结果未知")],
    ]
      .map(
        ([value, label]) =>
          `<option value="${value}" ${S.outcome === value ? "selected" : ""}>${label}</option>`,
      )
      .join(
        "",
      )}</select><select id="audit-node" aria-label="${tr("按节点筛选")}"><option value="">${tr("全部节点")}</option>${v.nodes.map((n) => `<option value="${n.id}" ${S.auditNode === n.id ? "selected" : ""}>${esc(n.name)}</option>`).join("")}</select><span class="count">${tr("不记录原命令、路径与自由格式错误")}</span></div><div id="audit-list">${auditTable()}</div><div class="pagination"><button id="more" ${S.next ? "" : "disabled"}>${tr("加载更早记录")}</button></div></div>`;
  return `<section class="panel form-panel"><h2>${tr("修改管理员密码")}</h2><p class="muted">${tr("更新后当前页面需重新登录，其他登录会在原有效期结束后失效。MCP 访问凭据不受影响。")}</p><form id="password-form">${field(tr("当前密码"), "current", "", 'type="password" required autocomplete="current-password"')}<div class="form-grid">${field(tr("新密码"), "next", "", 'type="password" required data-admin-password aria-describedby="admin-password-help" autocomplete="new-password"')}${field(tr("确认新密码"), "confirm", "", 'type="password" required data-admin-password aria-describedby="admin-password-help" autocomplete="new-password"')}</div><p id="admin-password-help" class="hint">${adminPasswordHint}</p><p class="form-error" role="alert"></p><button class="primary">${tr("更新密码")}</button></form></section>`;
}
function opsTable() {
  return S.ops.length
    ? table(
        [tr("工具"), tr("节点"), tr("阶段"), tr("状态"), tr("开始时间")],
        S.ops.map(
          (o) =>
            `<tr><td class="mono">${esc(o.tool)}</td><td>${esc((o.nodeIDs ?? []).map((id) => name("nodes", id)).join(", ") || "—")}</td><td>${esc({ inspect: tr("校验连接"), execute: tr("执行"), transfer_start: tr("传输"), commit: tr("提交"), recovery: tr("恢复") }[o.phase] || o.phase)}</td><td>${chip(o.state === "ending" ? tr("正在结束") : tr("运行中"), o.phase === "commit" ? "warning" : "good")}</td><td>${date(o.startedAt)}</td></tr>`,
        ),
      )
    : empty(
        tr("当前没有运行中的操作"),
        tr("新的 SSH、文件操作和连接测试将在这里显示。"),
      );
}
function auditTable() {
  if (S.auditLoading && !S.auditFilter)
    return empty(tr("正在加载审计记录"), tr("正在获取当前筛选条件下的记录。"));
  if (S.auditError && !S.auditFilter)
    return (
      empty(tr("审计记录加载失败"), S.auditError) +
      `<div class="pagination"><button id="audit-retry">${tr("重试加载")}</button></div>`
    );
  const labels = {
    intent: tr("已授权"),
    executed: tr("操作完成"),
    error: tr("操作失败"),
    denied: tr("拒绝"),
    unknown: tr("结果未知"),
    completed: tr("传输完成"),
    failed: tr("传输失败"),
    streamed: tr("已传输"),
    cancel_requested: tr("请求取消"),
  };
  return S.audit.length
    ? table(
        [tr("时间"), tr("操作"), tr("节点"), tr("结果"), tr("操作标识")],
        S.audit.map(
          ({ event: e }) =>
            `<tr><td class="nowrap">${date(e.ts)}</td><td class="mono">${esc(e.tool)}</td><td>${esc(e.node ? name("nodes", e.node) : (e.nodes || []).map((id) => name("nodes", id)).join(", ") || "—")}</td><td>${chip(labels[e.outcome] || e.outcome, ["error", "failed", "denied", "unknown"].includes(e.outcome) ? "warning" : ["executed", "completed"].includes(e.outcome) ? "good" : "neutral")}</td><td class="mono muted" title="${esc(e.op_id)}">${esc(e.op_id?.slice(0, 12))}</td></tr>`,
        ),
      )
    : empty(tr("暂无审计记录"), tr("配置变更和 MCP 操作会在这里留下记录。"));
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
function tokenScopeSummary(item) {
  if (item.nodeScope !== "selected") return tr("全部节点");
  const ids = item.nodeIDs || [];
  if (!ids.length) return tr("无节点访问权限");
  const deleted = ids.filter((id) => !S.v.nodes.some((node) => node.id === id)).length;
  return tr("指定 {0} 个节点{1}", {"0": ids.length, "1": deleted ? tr("（{0} 个已删除）", {"0": deleted}) : ""});
}
function tokenScopeFields(item) {
  const selected = new Set(item.nodeIDs || []);
  const nodes = S.v.nodes.map((node) => ({ ...node, deleted: false }));
  for (const id of selected) {
    if (!nodes.some((node) => node.id === id)) nodes.push({ id, name: tr("已删除节点"), deleted: true });
  }
  return `<label>${tr("允许访问的节点")}<select name="nodeScope" aria-label="${tr("允许访问的节点")}" aria-describedby="token-scope-help"><option value="all" ${item.nodeScope !== "selected" ? "selected" : ""}>${tr("全部节点")}</option><option value="selected" ${item.nodeScope === "selected" ? "selected" : ""}>${tr("指定节点")}</option></select></label><p id="token-scope-help" class="hint">${tr("全部节点包含以后新增的节点；指定节点仅允许访问勾选的节点，未勾选任何节点表示无节点访问权限。节点改名不影响绑定。")}</p><fieldset id="token-node-selection" class="token-node-selection"><legend>${tr("绑定节点")}</legend>${field(tr("搜索节点"), "nodeSearch", "", `type="search" placeholder="${tr("名称、别名或节点 ID")}" autocomplete="off"`)}<div class="token-node-options">${nodes.map((node) => `<label class="check token-node-option" data-search="${esc([node.name, node.id, ...(node.aliases || [])].join(" ").toLocaleLowerCase())}"><input type="checkbox" name="nodeIDs" value="${esc(node.id)}" ${selected.has(node.id) ? "checked" : ""}><span>${esc(node.name)}${node.disabled ? tr("（已停用）") : ""}<small>${esc(node.id)}</small></span></label>`).join("")}</div><p id="token-no-matches" class="hint" hidden>${tr("没有匹配的节点。")}</p>${nodes.length ? "" : `<p class="hint">${tr("暂无节点，可保存为空范围，之后再编辑绑定。")}</p>`}${nodes.some((node) => node.deleted) ? `<p class="hint">${tr("已删除节点无法访问，可取消勾选移除绑定。新建同名节点不会自动获得权限。")}</p>` : ""}<p id="token-node-count" class="hint" role="status"></p></fieldset>`;
}
function bindTokenScope(form) {
  const select = form.elements.nodeScope;
  const selection = $("#token-node-selection", form);
  const update = () => {
    selection.hidden = select.value !== "selected";
    selection.disabled = select.value !== "selected";
    const count = selection.querySelectorAll('[name="nodeIDs"]:checked').length;
    $("#token-node-count", form).textContent = count ? tr("已勾选 {0} 个节点", {"0": count}) : tr("未勾选节点，此 Token 无节点访问权限。");
  };
  select.addEventListener("change", update);
  selection.addEventListener("change", update);
  form.elements.nodeSearch.addEventListener("input", (event) => {
    const search = event.target.value.trim().toLocaleLowerCase();
    const options = [...selection.querySelectorAll(".token-node-option")];
    for (const option of options) option.hidden = !option.dataset.search.includes(search);
    $("#token-no-matches", form).hidden = !options.length || options.some((option) => !option.hidden);
  });
  update();
}
function tokenTable() {
  if (S.tokens === null) return S.tokenError
    ? empty(tr("Token 加载失败"), esc(S.tokenError)) + `<button id="token-retry">${tr("重试加载")}</button>`
    : empty(tr("正在加载 Token"), "");
  if (!S.tokens.length) return empty(tr("尚未创建 Token"), tr("为每个 MCP 客户端创建独立的访问凭据。"));
  return `<div class="panel">${table([tr("名称 / 凭据前缀"), tr("节点范围"), tr("状态"), tr("到期时间"), tr("创建时间"), tr("操作")], S.tokens.map((t) => {
    const revoked = t.revokedAt > 0, expired = t.expiresAt && t.expiresAt * 1000 <= Date.now();
    const status = revoked ? tr("已吊销") : expired ? tr("已过期") : t.enabled ? tr("已启用") : tr("已停用");
    return `<tr><td><strong>${esc(t.name)}</strong><small class="token-prefix">${esc(t.prefix)}…</small></td><td>${tokenScopeSummary(t)}</td><td>${chip(status, revoked || expired || !t.enabled ? "neutral" : "good")}</td><td>${t.expiresAt ? date(t.expiresAt * 1000) : tr("永不过期")}</td><td>${date(t.createdAt * 1000)}</td><td>${revoked ? "—" : `<div class="row-actions"><button data-action="edit" data-kind="tokens" data-id="${esc(t.id)}">${tr("编辑")}</button><button class="danger" data-action="token-revoke" data-id="${esc(t.id)}">${tr("吊销")}</button></div>`}</td></tr>`;
  }))}</div>`;
}
async function loadTokens() {
  const request = ++tokenRequest, generation = authGeneration;
  try {
    const { data } = await api("/mcp-tokens");
    if (request !== tokenRequest || generation !== authGeneration) return;
    S.tokens = data.tokens;
    S.tokenError = "";
  } catch (error) {
    if (request !== tokenRequest || generation !== authGeneration) return;
    S.tokens = null;
    S.tokenError = error.message;
  }
  if (S.page === "tokens" && $("#page") && S.session) {
    $("#page").innerHTML = tokenTable();
    bind();
  }
}
function localDateTime(seconds) {
  if (!seconds) return "";
  const value = new Date(seconds * 1000);
  return new Date(value.getTime() - value.getTimezoneOffset() * 60000).toISOString().slice(0, 19);
}
function showCreatedToken(secret) {
  if (dialog.open) dialog.close();
  const form = modal(tr("Token 已创建"), `<p>${tr("完整 Token 仅显示这一次，请复制到客户端配置中并妥善保存。")}</p><label>Token<textarea id="created-token" aria-label="Token" readonly spellcheck="false" rows="3">${esc(secret)}</textarea></label><button type="button" id="copy-token">${tr("复制 Token")}</button>`, async () => {}, tr("我已保存"));
  $("#copy-token", form).onclick = async () => {
    const field = $("#created-token", form);
    field.focus(); field.select();
    if (!navigator.clipboard) { toast(tr("已选中 Token，请使用复制快捷键")); return; }
    try { await navigator.clipboard.writeText(field.value); toast(tr("Token 已复制")); }
    catch { toast(tr("请使用复制快捷键复制选中的 Token")); }
  };
}
function editToken(id) {
  const item = id ? S.tokens.find((t) => t.id === id) : { name: "", enabled: true, expiresAt: 0 };
  const generation = authGeneration;
  const form = modal(id ? tr("编辑 Token") : tr("新建 Token"), nameField(tr("名称"), item.name) +
    tokenScopeFields(item) +
    field(tr("到期时间（留空表示永不过期）"), "expiresAt", localDateTime(item.expiresAt), 'type="datetime-local" step="1" max="9999-12-31T23:59:59"') +
    `<label class="check"><input type="checkbox" name="enabled" ${item.enabled ? "checked" : ""}>${tr("启用 Token")}</label>`, async (data, submitted) => {
      const expires = data.get("expiresAt");
      const nodeScope = data.get("nodeScope");
      const body = { name: data.get("name"), enabled: data.has("enabled"), expiresAt: expires ? Math.floor(new Date(expires).getTime() / 1000) : 0, nodeScope, nodeIDs: nodeScope === "selected" ? data.getAll("nodeIDs") : [] };
      submitted.dataset.busy = "true";
      submitted.querySelectorAll("[data-close]").forEach((b) => b.disabled = true);
      try {
        const { data: result } = await api("/mcp-tokens" + (id ? "/" + id : ""), { method: id ? "PUT" : "POST", body, etag: id ? `"${item.version}"` : "" });
        if (generation !== authGeneration) return;
        if (!id) showCreatedToken(result.token);
        else toast(tr("Token 已更新"));
        await loadTokens();
      } finally {
        delete submitted.dataset.busy;
        submitted.querySelectorAll("[data-close]").forEach((b) => b.disabled = false);
      }
    }, id ? tr("保存") : tr("创建 Token"));
  bindTokenScope(form);
  // Do not discard a one-time secret while creation is in flight.
  form.addEventListener("keydown", (event) => { if (event.key === "Escape" && form.dataset.busy) event.preventDefault(); });
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
  return `<label>${label}<select aria-label="${esc(label)}" name="${key}" ${required ? "required" : ""}><option value="">${required ? tr("请选择") : tr("不关联")}</option>${options.map((x) => `<option value="${esc(x.id)}" ${value === x.id ? "selected" : ""}>${esc(x.name)}</option>`).join("")}</select></label>`;
}
function modal(title, html, submit, label = tr("保存")) {
  dialog.innerHTML = `<form id="modal-form"><div class="dialog-head"><h2>${title}</h2><button type="button" data-close aria-label="${tr("关闭")}">×</button></div><div class="dialog-body">${html}<p class="form-error" role="alert"></p></div><div class="dialog-footer"><button type="button" data-close>${tr("取消")}</button><button class="primary" type="submit">${label}</button></div></form>`;
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
    if (form.dataset.submitting) return;
    namingFields(form).forEach(validateNamingField);
    if (!form.reportValidity()) return;
    const button = $("[type=submit]", form);
    const data = new FormData(form);
    const locked = form.dataset.lockOnSubmit
      ? [...form.querySelectorAll("input, select, textarea, button:not([data-close])")].map(control => [control, control.disabled])
      : [];
    form.dataset.submitting = "true";
    for (const [control] of locked) control.disabled = true;
    button.disabled = true;
    $(".form-error", form).textContent = "";
    try {
      await submit(data, form);
      close();
    } catch (error) {
      if (ownsDialog(form)) {
        $(".form-error", form).textContent =
          error.message +
          (error.status === 412 ? tr(" 请关闭窗口，刷新后重新编辑。") : "");
        for (const [control, disabled] of locked) control.disabled = disabled;
        button.disabled = false;
      } else toast(error.message, true);
    } finally {
      delete form.dataset.submitting;
    }
  };
  return form;
}
function confirm(title, text, submit) {
  modal(title, `<p class="confirm-copy">${esc(text)}</p>`, submit, tr("确认"));
}
function executionFields(execution) {
  const select = (label, name, values, current) => `<label>${label}<select name="${name}">${values.map(([value, text]) => `<option value="${value}" ${value === (current || "") ? "selected" : ""}>${text}</option>`).join("")}</select></label>`;
  return `<fieldset><legend>${tr("命令执行环境")}</legend><button type="button" id="probe-execution">${tr("自动探测")}</button><p id="execution-probe-result" class="hint" role="status" aria-live="polite"></p>` +
    select(tr("解释器"), "interpreter", [["", tr("未指定")], ...["server", "bash", "sh", "zsh", "fish", "powershell", "pwsh", "cmd"].map(x => [x, x])], execution?.interpreter) +
    select(tr("启动 Shell"), "launchDialect", [["", tr("默认")], ...["posix", "fish", "powershell", "cmd", "unknown"].map(x => [x, x])], execution?.launchDialect) +
    select(tr("登录模式"), "login", [["", tr("默认")], ["true", tr("登录 Shell")], ["false", tr("非登录 Shell")]], execution?.login == null ? "" : String(execution.login)) +
    `<p class="hint">${tr("点击自动探测，获取远端支持的 Shell 并选中推荐配置；点击保存后生效。server 保留服务端原命令。")}</p></fieldset>`;
}
function updateExecutionFields(form) {
  const interpreter = form.elements.interpreter.value;
  const dialects = {
    bash: ["posix", "fish"], sh: ["posix", "fish"], zsh: ["posix", "fish"], fish: ["posix", "fish"],
    powershell: ["cmd", "powershell"], pwsh: ["cmd", "powershell", "posix", "fish"], cmd: ["cmd"],
  }[interpreter];
  const launch = form.elements.launchDialect, login = form.elements.login;
  for (const option of launch.options) option.disabled = !!(option.value && dialects && !dialects.includes(option.value));
  if (launch.selectedOptions[0]?.disabled) launch.value = "";
  const inheritedLogin = ["", "server", "powershell", "pwsh", "cmd"].includes(interpreter);
  if (inheritedLogin || (interpreter === "sh" && login.value === "true")) login.value = "";
  login.disabled = inheritedLogin;
  for (const option of login.options) option.disabled = !!option.value && (inheritedLogin || (interpreter === "sh" && option.value === "true"));
}
function bindExecutionProbe(form, id, base) {
  form.dataset.lockOnSubmit = "true";
  updateExecutionFields(form);
  const button = $("#probe-execution", form), status = $("#execution-probe-result", form);
  const save = $('[type="submit"]', form), interpreter = form.elements.interpreter;
  const allOptions = interpreter.innerHTML;
  let controller, sequence = 0, applied = false;
  const connection = () => ({ nodeID: id || "", hostID: form.elements.hostID.value,
    identityID: form.elements.identityID.value,
    jumpIDs: [...form.querySelectorAll('[name="jumpID"]')].map(x => x.value).filter(Boolean) });
  const cancel = () => {
    sequence++;
    controller?.abort();
    controller = null;
    button.disabled = !!form.dataset.submitting;
    button.textContent = tr("自动探测");
    save.disabled = !!form.dataset.submitting;
  };
  dialog.addEventListener("close", cancel, { once: true });
  form.addEventListener("submit", event => {
    if (controller) {
      event.preventDefault();
      event.stopImmediatePropagation();
      status.textContent = tr("正在探测，请等待结果后保存。");
    }
  }, true);
  form.addEventListener("change", event => {
    if (event.target.name === "interpreter") updateExecutionFields(form);
    if (["hostID", "identityID", "jumpID"].includes(event.target.name)) {
      cancel();
      if (applied) {
        interpreter.innerHTML = allOptions;
        for (const key of ["interpreter", "launchDialect", "login"]) form.elements[key].value = "";
        applied = false;
        updateExecutionFields(form);
      }
      status.textContent = tr("连接配置已变化，请重新探测。");
    } else if (["interpreter", "launchDialect", "login"].includes(event.target.name) && controller) {
      cancel();
      status.textContent = tr("已取消探测，保留手动选择。");
    }
  });
  $("#jumps", form).addEventListener("click", event => {
    if (event.target.matches("[data-remove-jump]")) form.elements.hostID.dispatchEvent(new Event("change", { bubbles: true }));
  });
  button.onclick = async () => {
    if (form.dataset.submitting) return;
    const body = connection(), captured = JSON.stringify(body), generation = authGeneration;
    if (!body.hostID || !body.identityID) { status.textContent = tr("请先选择主机和登录身份，并配置登录凭据及主机公钥。"); return; }
    cancel();
    const request = sequence;
    controller = new AbortController();
    button.disabled = true;
    button.textContent = tr("正在探测…");
    save.disabled = true;
    status.textContent = tr("正在连接远端并探测支持的 Shell…");
    try {
      const { data } = await api("/nodes/execution-probe", { method: "POST", body, etag: base, signal: controller.signal });
      if (!ownsDialog(form) || request !== sequence || generation !== authGeneration || captured !== JSON.stringify(connection())) return;
      const suggested = data.suggestedConfig;
      if (!suggested?.interpreter || !suggested.launchDialect || !Array.isArray(data.availableInterpreters)) throw new Error(tr("探测结果不完整，请重试"));
      const previousExecution = JSON.stringify(executionInput(new FormData(form)));
      const supported = [...new Set(["server", ...data.availableInterpreters, suggested.interpreter])];
      interpreter.innerHTML = supported.map(value => `<option value="${esc(value)}">${esc(value)}</option>`).join("");
      interpreter.value = suggested.interpreter;
      form.elements.launchDialect.value = suggested.launchDialect;
      form.elements.login.value = suggested.login == null ? "" : String(suggested.login);
      updateExecutionFields(form);
      applied = true;
      if (previousExecution !== JSON.stringify(executionInput(new FormData(form))))
        form.dispatchEvent(new Event("executionchange"));
      status.textContent = tr("支持的 Shell：{0}。已选中推荐配置 {1} / {2}，点击保存后生效。", {"0": data.availableInterpreters.join(locale === "en" ? ", " : "、") || "server", "1": suggested.interpreter, "2": suggested.launchDialect});
    } catch (error) {
      if (ownsDialog(form) && request === sequence && error.name !== "AbortError") {
        const diag = formatDiagnosticSummary(error.diagnostics);
        status.textContent = error.message + (diag ? ` (${diag})` : "") + (error.status === 412 ? tr(" 请关闭窗口，刷新后重新编辑。") : "");
      }
    } finally {
      if (ownsDialog(form) && request === sequence) {
        controller = null;
        button.disabled = !!form.dataset.submitting;
        button.textContent = tr("自动探测");
        save.disabled = !!form.dataset.submitting;
      }
    }
  };
}
function bindPrivilegeTest(form, id, base) {
  const button = $("#modal-test-privilege", form);
  const status = $("#modal-privilege-status", form);
  if (!button || !status) return;
  let controller, sequence = 0, requested = false;
  const input = () => ({
    nodeID: id || "",
    hostID: form.elements.hostID.value,
    identityID: form.elements.identityID.value,
    jumpIDs: [...form.querySelectorAll('select[name="jumpID"]')].map(s => s.value).filter(Boolean),
    sudoMode: form.elements.sudoMode.value,
    privilegeCredentialID: form.elements.privilegeCredentialID.value,
    passwordPromptPattern: (form.elements.passwordPromptPattern?.value || "").trim(),
    execution: executionInput(new FormData(form)),
  });
  const cancel = () => {
    sequence++;
    controller?.abort();
    controller = null;
    button.disabled = !!form.dataset.submitting;
    button.textContent = tr("测试提权能力");
    status.textContent = "";
    requested = false;
  };
  dialog.addEventListener("close", cancel, { once: true });
  form.addEventListener("submit", cancel, true);
  form.addEventListener("input", (e) => {
    if (e.target.name === "passwordPromptPattern") cancel();
  });
  form.addEventListener("change", (e) => {
    if (["sudoMode", "privilegeCredentialID", "passwordPromptPattern", "hostID", "identityID", "jumpID", "interpreter", "launchDialect", "login"].includes(e.target.name)) cancel();
  });
  form.addEventListener("executionchange", () => {
    // A prior test authorizes rechecking the updated Shell settings. Manual
    // connection or privilege edits clear that intent through cancel().
    if (requested) void test(true);
    else cancel();
  });
  const test = async (automatic = false) => {
    if (form.dataset.submitting || !ownsDialog(form)) return;
    const body = input(), captured = JSON.stringify(body), generation = authGeneration;
    cancel();
    if (!body.hostID || !body.identityID) {
      status.textContent = tr("请先选择主机和登录身份，并配置登录凭据及主机公钥。");
      return;
    }
    requested = true;
    const request = sequence;
    controller = new AbortController();
    const current = () => ownsDialog(form) && request === sequence && generation === authGeneration && captured === JSON.stringify(input());
    button.disabled = true;
    button.textContent = tr("正在测试…");
    status.textContent = automatic ? tr("Shell 配置已更新，正在自动重新验证提权能力…") : tr("正在测试节点提权能力…");
    try {
      const { data } = await api("/nodes/privilege-probe", {
        method: "POST",
        body,
        etag: base,
        signal: controller.signal,
      });
      if (!current()) return;
      const modeText = data.sudoMode ? ` (${data.sudoMode})` : "";
      const userText = data.user ? `${data.user}${modeText}` : "";
      const detail = data.detail ? ` · ${data.detail}` : "";
      status.textContent = tr("提权测试成功：{0}{1} · {2} ms", {"0": userText, "1": detail, "2": data.elapsedMS});
      toast(tr("提权测试成功 · {0}{1} · {2} ms", {"0": data.user, "1": modeText, "2": data.elapsedMS}));
    } catch (error) {
      if (current() && error.name !== "AbortError") {
        const diag = formatDiagnosticSummary(error.diagnostics);
        const msg = error.message + (diag ? ` (${diag})` : "");
        status.textContent = msg;
        toast(msg, true);
      }
    } finally {
      if (ownsDialog(form) && request === sequence) {
        controller = null;
        button.disabled = !!form.dataset.submitting;
        button.textContent = tr("测试提权能力");
      }
    }
  };
  button.onclick = () => test();
}
function privateKeyInput(value) {
  return String(value ?? "").trim();
}
function bindCredentialKey(form) {
  const uploadBtn = $("#upload-key-btn", form),
    fileInput = $("#private-key-file", form),
    fileName = $("#upload-key-filename", form),
    textarea = form.elements.privateKey,
    passphraseInput = form.elements.passphrase,
    validateBtn = $("#btn-validate-key", form),
    status = $("#key-validate-status", form);
  if (!uploadBtn || !fileInput || !textarea) return;

  let controller, reader, sequence = 0;
  const lifecycle = new AbortController();
  const ownsFields = () => ownsDialog(form) && form.elements.privateKey === textarea && form.elements.passphrase === passphraseInput;
  let observedKey = privateKeyInput(textarea.value), observedPass = passphraseInput.value;
  const cancel = () => {
    observedKey = privateKeyInput(textarea.value);
    observedPass = passphraseInput.value;
    sequence++;
    controller?.abort();
    controller = null;
    if (reader?.readyState === FileReader.LOADING) reader.abort();
    reader = null;
    validateBtn.disabled = !!form.dataset.submitting;
    validateBtn.textContent = tr("验证私钥与口令");
    status.textContent = "";
  };
  const dispose = () => { cancel(); lifecycle.abort(); };
  dialog.addEventListener("close", () => { if (!ownsDialog(form)) dispose(); }, { signal: lifecycle.signal });
  form.addEventListener("submit", cancel, { capture: true, signal: lifecycle.signal });
  const invalidateChangedInput = () => {
    if (privateKeyInput(textarea.value) !== observedKey || passphraseInput.value !== observedPass) cancel();
  };
  for (const input of [textarea, passphraseInput]) {
    input.addEventListener("input", invalidateChangedInput, { signal: lifecycle.signal });
    input.addEventListener("change", invalidateChangedInput, { signal: lifecycle.signal });
  }
  const validate = async () => {
    if (form.dataset.submitting || !ownsFields()) return;
    cancel();
    const key = privateKeyInput(textarea.value), pass = passphraseInput.value;
    if (!key) {
      status.textContent = tr("请先粘贴或上传私钥内容。");
      return;
    }
    const request = sequence, generation = authGeneration;
    controller = new AbortController();
    const current = () => ownsFields() && request === sequence && generation === authGeneration && key === privateKeyInput(textarea.value) && pass === passphraseInput.value;
    validateBtn.disabled = true;
    validateBtn.textContent = tr("正在验证…");
    status.textContent = tr("正在验证私钥与口令…");
    try {
      const { data } = await api("/credentials/validate-key", {
        method: "POST",
        body: { privateKey: key, passphrase: pass },
        signal: controller.signal,
      });
      if (!current()) return;
      if (data.valid) {
        const fp = data.fingerprint ? ` · ${data.fingerprint}` : "";
        const type = data.keyType ? ` (${data.keyType})` : "";
        const protText = data.protected ? tr("受保护私钥口令正确") : tr("私钥有效（未加密）");
        status.textContent = `✓ ${protText}${type}${fp}`;
        toast(tr("私钥验证成功{0}", {"0": type}));
      } else {
        status.textContent = `✗ ${data.message || tr("私钥验证失败")}`;
        if (data.protected && !pass) passphraseInput.focus();
      }
    } catch (error) {
      if (current() && error.name !== "AbortError") status.textContent = `✗ ${error.message}`;
    } finally {
      if (ownsFields() && request === sequence) {
        controller = null;
        validateBtn.disabled = !!form.dataset.submitting;
        validateBtn.textContent = tr("验证私钥与口令");
      }
    }
  };

  uploadBtn.onclick = () => { if (!form.dataset.submitting) fileInput.click(); };
  const handleFile = (file) => {
    if (!file || form.dataset.submitting || !ownsFields()) return;
    cancel();
    if (file.size > 128 * 1024) {
      status.textContent = tr("私钥文件大小不能超过 128 KiB。");
      toast(tr("私钥文件大小不能超过 128 KiB"), true);
      return;
    }
    const pending = new FileReader(), generation = authGeneration;
    reader = pending;
    const current = () => ownsFields() && reader === pending && generation === authGeneration;
    pending.onload = () => {
      if (!current()) return;
      reader = null;
      textarea.value = privateKeyInput(pending.result);
      fileName.textContent = file.name;
      if (form.elements.name && !form.elements.name.value) {
        form.elements.name.value = file.name.replace(/\.(pem|key|pk8|txt|id_rsa|id_ed25519|id_ecdsa)$/i, "");
        namingFields(form).forEach(validateNamingField);
      }
      validate();
    };
    pending.onerror = () => {
      if (!current()) return;
      reader = null;
      status.textContent = tr("无法读取私钥文件，请重试。");
    };
    pending.readAsText(file);
  };
  fileInput.onchange = () => handleFile(fileInput.files?.[0]);
  textarea.ondragover = e => e.preventDefault();
  textarea.ondrop = e => { e.preventDefault(); handleFile(e.dataTransfer?.files?.[0]); };
  validateBtn.onclick = validate;
  passphraseInput.addEventListener("keydown", (e) => {
    if (e.key === "Enter") { e.preventDefault(); validate(); }
  }, { signal: lifecycle.signal });
  return dispose;
}

function executionInput(data) {
  const execution = {};
  for (const key of ["interpreter", "launchDialect"]) if (data.get(key)) execution[key] = data.get(key);
  if (data.get("login")) execution.login = data.get("login") === "true";
  return Object.keys(execution).length ? execution : null;
}
function edit(kind, id) {
  const item = id ? find(kind, id) : {},
    base = S.etag,
    label = {
      hosts: tr("主机"),
      identities: tr("身份"),
      nodes: tr("节点"),
      credentials: tr("凭据"),
      tags: tr("标签"),
    }[kind];
  let html = nameField(tr("名称"), item.name);
  if (kind === "hosts")
    html += `<div class="form-grid">${field(tr("SSH 地址"), "address", item.address, `required placeholder="${tr("192.0.2.10 或主机名")}"`)}${field(tr("端口"), "port", item.port || 22, 'type="number" min="1" max="65535" required')}</div><p class="hint">${tr("保存后通过“配置公钥”核对信任。修改地址会保留已固定公钥。")}</p>`;
  if (kind === "identities")
    html +=
      field(tr("SSH 用户名"), "user", item.user, "required") +
      selectField(
        tr("登录凭据"),
        "credentialID",
        S.v.credentials,
        item.credentialID,
      );
  const jumpRow = (value) =>
    `<div class="jump-row">${selectField(
      tr("跳板"),
      "jumpID",
      S.v.nodes.filter((n) => n.id !== id),
      value,
    )}<button type="button" data-remove-jump aria-label="${tr("移除跳板")}">×</button></div>`;
  if (kind === "nodes")
    html += `<div class="form-grid">${selectField(tr("主机"), "hostID", S.v.hosts, item.hostID, true)}${selectField(tr("登录身份"), "identityID", S.v.identities, item.identityID, true)}</div><label>${tr("别名")}<textarea name="aliases" rows="3" data-name-rule="aliases" aria-label="${tr("别名")}" aria-describedby="aliases-help" spellcheck="false" autocomplete="off" autocapitalize="off" placeholder="${tr("每行一个别名")}">${esc((item.aliases || []).join("\n"))}</textarea></label><p id="aliases-help" class="hint">${tr("每行一个别名，空行忽略。")}${nameHint}</p><fieldset class="tag-options"><legend>${tr("标签")}</legend>${S.v.tags.map((tag) => `<label class="check"><input type="checkbox" name="tagIDs" value="${esc(tag.id)}" ${(item.tagIDs || []).includes(tag.id) ? "checked" : ""}>${esc(tag.name)}</label>`).join("") || `<p class="hint">${tr("暂无标签，请先在“标签”页面创建。")}</p>`}</fieldset><div id="jumps">${(item.jumpIDs || []).map(jumpRow).join("")}</div><button id="add-jump" type="button">${tr("＋ 添加一跳")}</button><p class="hint">${tr("按从本服务到目标的连接顺序添加；空白表示直接连接。")}</p><div class="form-grid"><label>${tr("提权方式")}<select aria-label="${tr("提权方式")}" name="sudoMode">${["none", "sudo", "sudoer", "su", "root"].map((x) => `<option ${(item.sudoMode || "none") === x ? "selected" : ""}>${x}</option>`).join("")}</select></label>${selectField(
      tr("提权凭据"),
      "privilegeCredentialID",
      S.v.credentials.filter((c) => c.kind === "password"),
      item.privilegeCredentialID,
    )}</div><label>${tr("提权提示词正则")}<input name="passwordPromptPattern" value="${esc(item.passwordPromptPattern || "")}" maxlength="256" placeholder="${tr("可选，如 [sudo] password for 或 .*password:.*")}"></label><p class="hint">${tr("可选：当远端 PAM 忽略 -p 选项或使用 su 时，用于匹配密码提示词并自动应答。")}</p><div class="privilege-test-bar"><button type="button" id="modal-test-privilege">${tr("测试提权能力")}</button><p id="modal-privilege-status" class="hint" role="status" aria-live="polite"></p></div><label class="check"><input type="checkbox" name="enabled" ${id && !item.disabled ? "checked" : ""}>${tr("启用节点")}</label><p class="hint">${tr("启用前需配置凭据和主机公钥。未启用的跳板会阻止下游节点操作。")}</p>`;
  if (kind === "nodes") html += executionFields(item.execution);
  if (kind === "credentials")
    html += `<label>${tr("类型")}<select aria-label="${tr("类型")}" name="kind"><option value="password" ${item.kind !== "key" ? "selected" : ""}>${tr("密码")}</option><option value="key" ${item.kind === "key" ? "selected" : ""}>${tr("SSH 私钥")}</option></select></label><div id="secret-fields"></div><p class="hint">${id ? tr("留空保留已有材料；填写后会创建新版本。") : tr("凭据加密保存，创建后不会回显。")}</p>`;
  const form = modal(tr(id ? "编辑{0}" : "新建{0}", { 0: label }), html, async (data) => {
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
        passwordPromptPattern: (x.passwordPromptPattern || "").trim(),
        execution: executionInput(data),
      };
    if (kind === "credentials") {
      body = { name: x.name, kind: x.kind };
      const privateKey = privateKeyInput(x.privateKey);
      if (x.password || privateKey || x.passphrase || !id)
        body.secret = {
          password: x.password || "",
          privateKey,
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
      tr("{0}已保存", {"0": label}),
    );
  });
  if (kind === "credentials") {
    let clearKey;
    const fields = () => {
      clearKey?.();
      const isKey = form.elements.kind.value === "key";
      $("#secret-fields", form).innerHTML = isKey
        ? `<label>${tr("私钥内容")}<div class="key-upload-bar"><button type="button" id="upload-key-btn">${tr("上传私钥文件")}</button><input type="file" id="private-key-file" style="display:none" accept=".pem,.key,.pk8,.id_rsa,.id_ed25519,.id_ecdsa,text/plain,*"><span id="upload-key-filename" class="muted"></span></div><textarea name="privateKey" rows="7" autocomplete="off" spellcheck="false" placeholder="${tr("粘贴 PEM / OpenSSH 私钥，或点击上方按钮上传私钥文件")}"></textarea></label><div class="key-validate-bar"><button type="button" id="btn-validate-key">${tr("验证私钥与口令")}</button><p id="key-validate-status" class="hint" role="status" aria-live="polite"></p></div>` +
          field(
            tr("私钥口令（受保护私钥必填）"),
            "passphrase",
            "",
            'type="password" autocomplete="new-password"',
          )
        : field(
            tr("密码"),
            "password",
            "",
            'type="password" autocomplete="new-password"',
          );
      clearKey = isKey ? bindCredentialKey(form) : null;
    };
    fields();
    form.elements.kind.onchange = fields;
  }
  if (kind === "nodes") {
    bindExecutionProbe(form, id, base);
    bindPrivilegeTest(form, id, base);
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
    tr("确认主机公钥"),
    `<p class="muted">${esc(host.name)} · ${esc(host.address)}:${host.port}</p><label>${tr("公钥来源")}<select name="keySource"><option value="network">${tr("直接连接主机获取")}</option><option value="manual">${tr("输入已独立核验的公钥")}</option></select></label><p class="hint">${tr("仅经跳板可达的主机，请输入从可信渠道获取的公钥。")}</p><div id="network-key"><label>${tr("算法")}<select aria-label="${tr("算法")}" name="algorithm"><option value="">${tr("自动选择安全算法")}</option><option value="ssh-ed25519">Ed25519</option><option value="ecdsa-sha2-nistp256">ECDSA P-256</option><option value="rsa-sha2-512">RSA SHA-512</option><option value="rsa-sha2-256">RSA SHA-256</option></select></label></div><div id="manual-key" hidden><label>${tr("主机公钥")}<textarea name="hostKey" rows="3" spellcheck="false" placeholder="ssh-ed25519 AAAA…" aria-describedby="manual-key-help"></textarea></label><p id="manual-key-help" class="hint">${tr("粘贴单个 SSH 公钥（.pub 文件内容）。请通过主机控制台或可信管理员核对指纹。")}</p></div><button type="button" id="fetch-key">${tr("连接并获取公钥")}</button><div id="key-result"></div>`,
    async () => {
      if (!observed || !$("#verified", form).checked)
        throw new Error(tr("请先预览公钥并独立核对指纹"));
      await mutate(
        "/hosts/" + id + "/trust",
        {
          method: "POST",
          etag: base,
          body: { probeID: observed.probeID },
        },
        tr("主机公钥已固定"),
      );
    },
    tr("确认并固定"),
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
      ? tr("预览公钥指纹")
      : tr("连接并获取公钥");
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
        `<div class="key-preview"><small>${tr("待核对指纹 ·")} ${esc(data.algorithm)}</small><strong class="mono">${esc(data.fingerprint)}</strong><textarea readonly rows="3" aria-label="${tr("待确认的主机公钥")}">${esc(data.hostKey)}</textarea></div>${host.fingerprint && host.fingerprint !== data.fingerprint ? `<p class="form-error">${tr("公钥与当前固定公钥不同，请核实轮换原因。")}</p>` : ""}<label class="check"><input id="verified" type="checkbox">${tr("已通过独立渠道核对指纹")}</label><p class="hint">${tr("两分钟内有效。预览公钥本身不会建立信任。")}</p>`;
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
      ["safe", tr("所有操作")],
      ["moderate", tr("中等风险及以上")],
      ["dangerous", tr("高风险操作")],
    ],
    options = (values, value) =>
      values
        .map(
          ([v, label]) =>
            `<option value="${v}" ${v === value ? "selected" : ""}>${label}</option>`,
        )
        .join("");
  return `<form id="policy-form" class="panel form-panel"><h2>${tr("风险确认")}</h2><label class="check"><input name="enabled" type="checkbox" ${p.enabled ? "checked" : ""}>${tr("启用操作护栏")}</label><div class="form-grid"><label>${tr("开始确认的风险级别")}<select name="approvalThreshold">${options(thresholds, p.approvalThreshold || "dangerous")}</select></label><label>${tr("客户端无法确认时")}<select name="noElicitFallback">${options(
    [
      ["deny", tr("拒绝需要确认的操作")],
      ["downgrade", tr("仅允许中等风险操作")],
      ["allow", tr("允许执行")],
    ],
    p.noElicitFallback || "deny",
  )}</select></label></div><div class="form-grid"><label>${tr("额外拦截的命令模式")}<textarea name="blockedPatterns" rows="5" aria-label="${tr("额外拦截的命令模式")}" aria-describedby="blocked-patterns-help" placeholder="${tr("每行一个 glob 模式，如 hostname 或 hostname*")}">${esc((p.blockedPatterns || []).join("\n"))}</textarea><small id="blocked-patterns-help">${tr("使用 glob 通配符匹配完整命令，支持 *、?、[abc]；* 和 ? 不跨越路径分隔符。hostname 精确匹配，hostname* 匹配前缀；不使用 ^...$ 正则语法。")}</small></label><label>${tr("受保护路径")}<textarea name="protectedPaths" rows="5" placeholder="${tr("每行一个路径")}">${esc((p.protectedPaths || []).join("\n"))}</textarea></label></div><h2>${tr("节点确认级别")}</h2><div class="override-list">${S.v.nodes.map((n) => `<label>${esc(n.name)}<select name="override:${n.id}"><option value="">${tr("使用全局规则")}</option>${options(thresholds, p.nodes[n.id])}</select></label>`).join("") || `<p class="muted">${tr("添加节点后可设置单独规则。")}</p>`}</div><p class="form-error" role="alert"></p><div class="form-bottom"><p class="hint">${tr("策略变更会撤销相关可取消操作，已准入提交仍按原期限结算。")}</p><button class="primary">${tr("保存策略")}</button></div></form>`;
}
function bind() {
  rememberDrafts();
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
  $("#token-retry")?.addEventListener("click", () => loadTokens());
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
          tr("策略已更新"),
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
        $(".form-error", password).textContent = tr("两次输入的新密码不一致");
        return;
      }
      const button = $("button", password);
      button.disabled = true;
      const generation = authGeneration;
      try {
        await api("/auth/password", {
          method: "PUT",
          body: { current: x.current, next: x.next },
        });
        if (generation !== authGeneration) return;
        setAccessToken("");
        S.session = null;
        S.v = null;
        toast(tr("密码已更新，请重新登录"));
        await boot();
      } catch (error) {
        if (generation !== authGeneration || !password.isConnected) return;
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
      if (kind === "tokens") editToken(id);
      else edit(kind, id);
      return;
    }
    if (action === "token-revoke") {
      const item = S.tokens.find((t) => t.id === id);
      confirm(tr("吊销 Token"), tr("确认永久吊销“{0}”？该凭据将不能再发起 MCP 请求。已开始的操作不保证中止。", {"0": item.name}), async () => {
        await api("/mcp-tokens/" + id, { method: "DELETE", etag: `"${item.version}"` });
        await loadTokens();
        toast(tr("Token 已吊销"));
      });
      return;
    }
    if (action === "probe") {
      probe(id);
      return;
    }
    if (action === "delete") {
      const base = S.etag;
      confirm(
        tr("删除记录"),
        tr("确认删除“{0}”？仍被其他记录引用时会拒绝删除。", {"0": name(kind, id)}),
        async () => {
          await mutate(
            "/" + kind + "/" + id,
            { method: "DELETE", etag: base },
            tr("记录已删除"),
          );
        },
      );
      return;
    }
    if (action === "revoke") {
      const base = S.etag;
      confirm(
        tr("撤销主机信任"),
        tr("关联节点将被停用，相关可取消操作会被撤销；远端已经开始的工作不保证回滚。"),
        async () => {
          await mutate(
            "/hosts/" + id + "/trust",
            {
              method: "DELETE",
              etag: base,
            },
            tr("主机信任已撤销"),
          );
        },
      );
      return;
    }
    if (action === "rename-tag") {
      const base = S.etag;
      modal(tr("重命名标签"), nameField(tr("新名称"), tag), async (data) => {
        await mutate(
          "/tags/" + encodeURIComponent(id),
          {
            method: "PUT",
            etag: base,
            body: { name: data.get("name") },
          },
          tr("标签已更新"),
        );
      });
      return;
    }
    if (action === "delete-tag") {
      const base = S.etag;
      confirm(
        tr("删除标签"),
        tr("删除“{0}”并解除所有节点关联，节点本身不会被删除。", {"0": tag}),
        async () => {
          await mutate(
            "/tags/" + encodeURIComponent(id),
            {
              method: "DELETE",
              etag: base,
            },
            tr("标签已删除"),
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
      setAccessToken("");
      S.session = null;
      S.v = null;
      S.audit = [];
      inventoryRequest++;
      if (dialog.open) dialog.close();
      authPage({});
      await boot();
      return;
    }
    if (action === "reconcile") {
      await mutate(
        "/reconcile",
        { method: "POST", body: {} },
        tr("已保存配置已应用"),
      );
    }
    if (action === "test") {
      const { data } = await api("/nodes/" + id + "/test", {
        method: "POST",
        body: {},
      });
      toast(tr("SSH 连接成功 · {0} ms", {"0": data.elapsedMS}));
    }
    if (action === "test-privilege") {
      const { data } = await api("/nodes/" + id + "/test-privilege", {
        method: "POST",
        body: {},
      });
      const modeText = data.sudoMode ? ` (${data.sudoMode})` : "";
      const userText = data.user ? ` · ${data.user}${modeText}` : "";
      toast(tr("提权测试成功{0} · {1} ms", {"0": userText, "1": data.elapsedMS}));
    }
    if (action === "toggle") {
      const { id: ignored, status, ...body } = find("nodes", id);
      body.disabled = !body.disabled;
      await mutate(
        "/nodes/" + id,
        { method: "PUT", body },
        body.disabled ? tr("节点已停用") : tr("节点已启用"),
      );
    }
  } catch (error) {
    const diag = formatDiagnosticSummary(error.diagnostics);
    toast(error.message + (diag ? ` (${diag})` : ""), true);
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
  const generation = authGeneration;
  try {
    const { data } = await api("/operations");
    if (generation !== authGeneration) return;
    S.ops = data.operations;
    if (S.page === "operations" && $("#ops")) $("#ops").innerHTML = opsTable();
  } catch (e) {
    if (generation === authGeneration && e.status !== 401) toast(tr("运行状态暂时无法刷新"), true);
  }
}, 5000);
boot();
