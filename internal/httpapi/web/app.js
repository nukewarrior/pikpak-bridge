const $ = (id) => document.getElementById(id);

const STATUS = {
  QUEUED: ["排队中", "warn"],
  WAITING_PIKPAK_ACCOUNT: ["等待账号", "warn"],
  PIKPAK_SUBMITTING: ["提交 PikPak", "active"],
  PIKPAK_RUNNING: ["PikPak 离线中", "active"],
  PIKPAK_COMPLETE: ["离线完成", "active"],
  RESOLVING_FILES: ["解析文件", "active"],
  WAITING_ARIA2: ["等待 aria2", "warn"],
  ARIA2_DOWNLOADING: ["aria2 下载中", "active"],
  VERIFYING: ["校验中", "active"],
  READY_TO_CLEANUP: ["等待清理", "warn"],
  PIKPAK_DELETING: ["清理 PikPak", "active"],
  COMPLETED: ["已完成", "success"],
  CANCELLED: ["已取消", ""],
  PIKPAK_FAILED: ["PikPak 失败", "fail"],
  ARIA2_FAILED: ["aria2 失败", "fail"],
  VERIFY_FAILED: ["校验失败", "fail"],
  CLEANUP_FAILED: ["清理失败", "fail"],
  PENDING: ["等待提交", "warn"],
  SUBMITTED: ["已提交", "active"],
  ACTIVE: ["下载中", "active"],
  WAITING: ["aria2 等待", "warn"],
  PAUSED: ["已暂停", "warn"],
  COMPLETE: ["完成", "success"],
  ERROR: ["失败", "fail"]
};

const ACTIVE = new Set([
  "QUEUED","WAITING_PIKPAK_ACCOUNT","PIKPAK_SUBMITTING","PIKPAK_RUNNING",
  "PIKPAK_COMPLETE","RESOLVING_FILES","WAITING_ARIA2","ARIA2_DOWNLOADING",
  "VERIFYING","READY_TO_CLEANUP","PIKPAK_DELETING"
]);

const FAILED_TASKS = new Set([
  "PIKPAK_FAILED","ARIA2_FAILED","VERIFY_FAILED","CLEANUP_FAILED"
]);

const TERMINAL_TASKS = new Set([
  "COMPLETED","CANCELLED","PIKPAK_FAILED","ARIA2_FAILED","VERIFY_FAILED","CLEANUP_FAILED"
]);

const ERROR_EVENT_HINTS = [
  "failed","retry","error","eof","rejected","missing","manual_retry"
];

function isErrorHistoryEvent(event) {
  const type = String(event?.type || "").toLowerCase();
  return ERROR_EVENT_HINTS.some(hint => type.includes(hint));
}

function eventLabel(type) {
  const labels = {
    "task.manual_retry": "手动重试",
    "aria2.eof_retry": "PikPak EOF 重试",
    "aria2.eof_exhausted": "PikPak EOF 重试耗尽",
    "aria2.file_retry": "aria2 文件重试",
    "aria2.poll_retry": "aria2 状态查询重试",
    "aria2.failed": "aria2 失败",
    "pikpak.retry": "PikPak 重试",
    "pikpak.failed": "PikPak 失败",
    "pikpak.account_rejected": "PikPak 账号不可用",
    "verify.failed": "校验失败",
    "cleanup.retry": "清理重试",
    "cleanup.failed": "清理失败"
  };
  return labels[type] || type || "错误";
}

let appConfigured = false;
let dashboardStarted = false;
let taskRefreshBusy = false;
let historyRefreshBusy = false;
let historyFilter = "all";
let targetsRefreshBusy = false;
let toastTimer;
let currentConfigView = "accounts";
let configState = {pikpak_accounts:[], aria2_instances:[], targets:[]};
let resourceStatus = {pikpak_accounts:{}, aria2_instances:{}, updated_at:null};
let resourceStatusBusy = false;
let editingResource = null;

const LAST_TARGET_STORAGE_KEY = "pikpak-bridge:last-target";

function esc(value) {
  return String(value ?? "")
    .replaceAll("&","&amp;").replaceAll("<","&lt;").replaceAll(">","&gt;")
    .replaceAll('"',"&quot;").replaceAll("'","&#039;");
}

function truncate(value, length=72) {
  const s = String(value ?? "");
  return s.length > length ? s.slice(0, length - 1) + "…" : s;
}

function readLastTarget() {
  try {
    return localStorage.getItem(LAST_TARGET_STORAGE_KEY) || "";
  } catch (_) {
    return "";
  }
}

function rememberLastTarget(id) {
  try {
    if (id) localStorage.setItem(LAST_TARGET_STORAGE_KEY, id);
    else localStorage.removeItem(LAST_TARGET_STORAGE_KEY);
  } catch (_) {}
}


function statusBadge(status) {
  const [label, cls] = STATUS[status] || [status || "未知", ""];
  return `<span class="badge ${cls}">${esc(label)}</span>`;
}

function fmtTime(value) {
  if (!value) return "—";
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return "—";
  return new Intl.DateTimeFormat("zh-CN", {
    month:"2-digit", day:"2-digit", hour:"2-digit", minute:"2-digit", second:"2-digit",
    hour12:false
  }).format(d);
}

function fmtBytes(bytes) {
  const n = Number(bytes || 0);
  if (!Number.isFinite(n) || n <= 0) return "0 B";
  const units = ["B","KB","MB","GB","TB"];
  let v = n, i = 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return `${v >= 10 || i === 0 ? v.toFixed(0) : v.toFixed(1)} ${units[i]}`;
}

function toast(message, isError=false) {
  const el = $("toast");
  el.textContent = message;
  el.className = "toast show" + (isError ? " error" : "");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.className = "toast", 3500);
}

async function request(url, options) {
  const res = await fetch(url, options);
  let body = {};
  try { body = await res.json(); } catch (_) {}
  if (!res.ok) {
    const err = new Error(body.error || `HTTP ${res.status}`);
    err.status = res.status;
    err.body = body;
    throw err;
  }
  return body;
}

function setActiveNav(id) {
  document.querySelectorAll(".nav-item").forEach(item => item.classList.toggle("active", item.id === id));
}

function showHome() {
  $("setupScreen").classList.add("hidden");
  $("historyView").classList.add("hidden");
  $("homeView").classList.remove("hidden");
  setActiveNav("homeNav");
}

const CONFIG_VIEWS = {
  accounts: {
    nav: "accountNav",
    section: "accountSection",
    eyebrow: "PIKPAK",
    title: "PikPak 账号",
    description: "管理用于离线下载的 PikPak 账号，系统会自动选择可用账号。"
  },
  aria2: {
    nav: "aria2Nav",
    section: "aria2Section",
    eyebrow: "ARIA2",
    title: "aria2 实例",
    description: "管理用于接收最终文件的 aria2 JSON-RPC 服务。"
  },
  targets: {
    nav: "targetNav",
    section: "targetSection",
    eyebrow: "TARGETS",
    title: "下载目标",
    description: "管理可选下载位置，每个目标绑定一个 aria2 实例和目录。"
  },
  settings: {
    nav: "settingsNav",
    section: "generalSection",
    eyebrow: "SETTINGS",
    title: "设置",
    description: "查看 PikPak Bridge 的全局运行设置。"
  }
};

function showConfigView(data, view="accounts") {
  currentConfigView = CONFIG_VIEWS[view] ? view : "accounts";
  const meta = CONFIG_VIEWS[currentConfigView];

  configState = normalizeConfigData(data);
  $("homeView").classList.add("hidden");
  $("historyView").classList.add("hidden");
  $("setupScreen").classList.remove("hidden");

  document.querySelectorAll(".resource-page").forEach(section => section.classList.add("hidden"));
  $(meta.section).classList.remove("hidden");

  $("setupEyebrow").textContent = meta.eyebrow;
  $("setupTitle").textContent = meta.title;
  $("setupDescription").textContent = meta.description;
  setActiveNav(meta.nav);
  renderResourcePage(currentConfigView);
  window.scrollTo({top:0, behavior:"smooth"});
}

async function openConfigView(view="accounts") {
  try {
    let data = {pikpak_accounts:[], aria2_instances:[], targets:[]};
    if (appConfigured) data = await request("/api/v1/config");
    showConfigView(data, view);
    if (view === "accounts" || view === "aria2") {
      refreshResourceStatus(false);
    }
  } catch (err) {
    toast("配置加载失败：" + err.message, true);
  }
}

function taskTitle(task) {
  const source = String(task.source || "");
  if (source.startsWith("magnet:?")) {
    try {
      const params = new URLSearchParams(source.slice(source.indexOf("?") + 1));
      const dn = params.get("dn");
      if (dn) return dn;
    } catch (_) {}
    return "Magnet 下载任务";
  }
  try {
    const url = new URL(source);
    const last = url.pathname.split("/").filter(Boolean).pop();
    if (last) return decodeURIComponent(last);
    return url.hostname || source;
  } catch (_) {
    return truncate(source, 58) || "下载任务";
  }
}

function sourceIcon() {
  return `<svg viewBox="0 0 24 24"><path d="M7 3h7l5 5v13H7z"/><path d="M14 3v6h5"/></svg>`;
}

function makeInternalId(prefix) {
  const bytes = new Uint8Array(4);
  crypto.getRandomValues(bytes);
  return prefix + "-" + [...bytes].map(x => x.toString(16).padStart(2, "0")).join("");
}

function defaultAccountName(username) {
  const value = String(username || "").trim();
  if (!value) return "";
  const at = value.indexOf("@");
  if (at > 0) return value.slice(0, at);
  return value;
}


function normalizeConfigData(data={}) {
  return {
    pikpak_accounts: (data.pikpak_accounts || []).map(x => ({...x})),
    aria2_instances: (data.aria2_instances || []).map(x => ({...x})),
    targets: (data.targets || []).map(x => ({...x}))
  };
}

function enabledBadge(enabled) {
  return enabled === false
    ? '<span class="badge">已停用</span>'
    : '<span class="badge success">已启用</span>';
}


function resourceStatusBadge(kind, item) {
  if (item.enabled === false) return '<span class="badge">已停用</span>';
  const status = kind === "account"
    ? resourceStatus.pikpak_accounts[item.id]
    : resourceStatus.aria2_instances[item.id];
  if (!status) return '<span class="badge">未检测</span>';
  if (kind === "account") {
    if (status.state === "QUOTA_EXHAUSTED") return '<span class="badge warn">额度用尽</span>';
    if (status.state === "STORAGE_FULL") return '<span class="badge warn">空间已满</span>';
    if (status.state === "CAPTCHA_REQUIRED") return '<span class="badge fail">需要验证</span>';
    if (status.state === "AUTH_FAILED") return '<span class="badge fail">登录失败</span>';
  }
  if (status.healthy) return '<span class="badge success">正常</span>';
  return '<span class="badge fail">异常</span>';
}

function checkedText(status) {
  if (!status?.checked_at) return "尚未检测";
  return "检测于 " + fmtTime(status.checked_at);
}

function accountStatusMeta(item) {
  const status = resourceStatus.pikpak_accounts[item.id];
  if (!status) return '<div class="resource-health-meta muted">尚未检测账号状态</div>';
  if (status.error) {
    return `<div class="resource-health-meta error">${esc(status.error)}</div><div class="resource-health-time">${esc(checkedText(status))}</div>`;
  }
  const quota = status.quota_total > 0
    ? `云下载 ${status.quota_remaining} / ${status.quota_total}`
    : `云下载剩余 ${status.quota_remaining ?? 0}`;
  const storage = status.storage_total > 0
    ? `空间 ${fmtBytes(status.storage_free)} / ${fmtBytes(status.storage_total)}`
    : `剩余空间 ${fmtBytes(status.storage_free)}`;
  const jobs = `并发 ${status.active_jobs || 0} / ${status.max_jobs || item.max_jobs || 0}`;
  return `<div class="resource-health-meta">${esc(quota)} · ${esc(storage)} · ${esc(jobs)}</div><div class="resource-health-time">${esc(checkedText(status))}</div>`;
}

function aria2StatusMeta(item) {
  const status = resourceStatus.aria2_instances[item.id];
  if (!status) return '<div class="resource-health-meta muted">尚未检测 RPC 状态</div>';
  if (status.error) {
    return `<div class="resource-health-meta error">${esc(status.error)}</div><div class="resource-health-time">${esc(checkedText(status))}</div>`;
  }
  return `<div class="resource-health-meta">活动 ${status.active || 0} · 等待 ${status.waiting || 0}</div><div class="resource-health-time">${esc(checkedText(status))}</div>`;
}

async function refreshResourceStatus(showToast=true) {
  if (resourceStatusBusy || !appConfigured) return;
  resourceStatusBusy = true;
  const accountBtn = $("checkAccountsBtn");
  const aria2Btn = $("checkAria2Btn");
  [accountBtn, aria2Btn].forEach(btn => {
    if (!btn) return;
    btn.disabled = true;
    btn.dataset.oldText = btn.textContent;
    btn.textContent = "检测中…";
  });
  try {
    const data = await request("/api/v1/status");
    resourceStatus = {
      pikpak_accounts: Object.fromEntries((data.pikpak_accounts || []).map(x => [x.id, x])),
      aria2_instances: Object.fromEntries((data.aria2_instances || []).map(x => [x.id, x])),
      updated_at: data.updated_at || null
    };
    if (currentConfigView === "accounts" || currentConfigView === "aria2") {
      renderResourcePage(currentConfigView);
    }
    if (showToast) toast("检测完成。");
  } catch (err) {
    if (showToast) toast("检测失败：" + err.message, true);
  } finally {
    resourceStatusBusy = false;
    [accountBtn, aria2Btn].forEach(btn => {
      if (!btn) return;
      btn.disabled = false;
      btn.textContent = btn.dataset.oldText || "立即检测";
    });
  }
}

function resourceActions(kind, id) {
  return `
    <div class="resource-row-actions">
      <button class="resource-action edit" type="button" data-action="edit" data-kind="${kind}" data-id="${esc(id)}">编辑</button>
      <button class="resource-action remove" type="button" data-action="remove" data-kind="${kind}" data-id="${esc(id)}">删除</button>
    </div>`;
}

function emptyResourceList(label) {
  return `
    <div class="resource-empty">
      <div class="empty-icon">+</div>
      <strong>还没有${esc(label)}</strong>
      <span>点击右上角按钮添加。</span>
    </div>`;
}

function renderResourcePage(view=currentConfigView) {
  if (view === "accounts") {
    const items = configState.pikpak_accounts;
    $("accountResourceCount").textContent = `${items.length} 个账号`;
    $("accountResourceList").innerHTML = items.length ? items.map(item => `
      <article class="resource-row">
        <div class="resource-row-icon purple">P</div>
        <div class="resource-row-main">
          <div class="resource-row-title">${esc(item.name || "PikPak 账号")}</div>
          <div class="resource-row-meta">${esc(item.username || "未填写账号")}</div>
          ${accountStatusMeta(item)}
        </div>
        <div class="resource-row-state">${resourceStatusBadge("account", item)}</div>
        ${resourceActions("account", item.id)}
      </article>`).join("") : emptyResourceList("PikPak 账号");
    bindResourceActions($("accountResourceList"));
    return;
  }

  if (view === "aria2") {
    const items = configState.aria2_instances;
    $("aria2ResourceCount").textContent = `${items.length} 个实例`;
    $("aria2ResourceList").innerHTML = items.length ? items.map(item => `
      <article class="resource-row">
        <div class="resource-row-icon green">A</div>
        <div class="resource-row-main">
          <div class="resource-row-title">${esc(item.name || "aria2 实例")}</div>
          <div class="resource-row-meta">${esc(item.url || "未填写 RPC 地址")}</div>
          ${aria2StatusMeta(item)}
        </div>
        <div class="resource-row-state">${resourceStatusBadge("aria2", item)}</div>
        ${resourceActions("aria2", item.id)}
      </article>`).join("") : emptyResourceList("aria2 实例");
    bindResourceActions($("aria2ResourceList"));
    return;
  }

  if (view === "targets") {
    const items = configState.targets;
    const instanceName = id => configState.aria2_instances.find(x => x.id === id)?.name || "未关联实例";
    $("targetResourceCount").textContent = `${items.length} 个目标`;
    $("targetResourceList").innerHTML = items.length ? items.map(item => `
      <article class="resource-row">
        <div class="resource-row-icon orange">T</div>
        <div class="resource-row-main">
          <div class="resource-row-title">${esc(item.name || "下载目标")} ${item.default ? '<span class="mini-tag">默认</span>' : ''}</div>
          <div class="resource-row-meta">${esc(instanceName(item.aria2_instance))} · ${esc(item.dir || "未填写目录")}</div>
        </div>
        <div class="resource-row-state">${enabledBadge(item.enabled)}</div>
        ${resourceActions("target", item.id)}
      </article>`).join("") : emptyResourceList("下载目标");
    bindResourceActions($("targetResourceList"));
  }
}

function bindResourceActions(container) {
  container.querySelectorAll("[data-action]").forEach(button => {
    button.addEventListener("click", async () => {
      const {action, kind, id} = button.dataset;
      if (action === "edit") {
        openResourceDialog(kind, id);
        return;
      }
      if (action === "remove") {
        await removeResource(kind, id);
      }
    });
  });
}

function resourceCollection(kind) {
  if (kind === "account") return configState.pikpak_accounts;
  if (kind === "aria2") return configState.aria2_instances;
  return configState.targets;
}

function openResourceDialog(kind, id="") {
  const collection = resourceCollection(kind);
  const existing = id ? collection.find(x => x.id === id) : null;
  editingResource = {kind, id: existing?.id || ""};

  const dialog = $("resourceDialog");
  const fields = $("resourceFormFields");
  const titlePrefix = existing ? "编辑" : "添加";

  if (kind === "account") {
    $("resourceDialogEyebrow").textContent = "PIKPAK ACCOUNT";
    $("resourceDialogTitle").textContent = titlePrefix + " PikPak 账号";
    const internalId = existing?.id || makeInternalId("pp");
    fields.innerHTML = `
      <input data-field="id" type="hidden" value="${esc(internalId)}">
      <div class="modal-field-grid two">
        <div class="field">
          <label>名称</label>
          <input data-field="name" value="${esc(existing?.name || defaultAccountName(existing?.username))}" placeholder="根据账号自动生成">
          <span class="hint">默认根据账号生成，也可以手动修改。</span>
        </div>
        <div class="field">
          <label>状态</label>
          <label class="switch-row"><input data-field="enabled" type="checkbox" ${existing?.enabled === false ? "" : "checked"}><span>启用此账号</span></label>
        </div>
      </div>
      <div class="form-group">
        <div class="form-group-title">登录凭据</div>
        <div class="modal-field-grid two">
          <div class="field">
            <label>账号</label>
            <input data-field="username" value="${esc(existing?.username || "")}" autocomplete="username" placeholder="邮箱或手机号" required>
          </div>
          <div class="field">
            <label>密码</label>
            <input data-field="password" type="password" autocomplete="new-password" placeholder="${existing?.password_set ? "已保存，留空保持不变" : "PikPak 登录密码"}" ${existing?.password_set ? "" : "required"}>
          </div>
        </div>
      </div>
      <div class="field compact-field">
        <label>最大并发离线任务</label>
        <input data-field="max_jobs" type="number" min="1" max="20" value="${esc(existing?.max_jobs || 2)}" required>
        <span class="hint">此账号同时处理的 PikPak 离线任务上限。</span>
      </div>`;
  } else if (kind === "aria2") {
    $("resourceDialogEyebrow").textContent = "ARIA2 INSTANCE";
    $("resourceDialogTitle").textContent = titlePrefix + " aria2 实例";
    const internalId = existing?.id || makeInternalId("aria2");
    fields.innerHTML = `
      <input data-field="id" type="hidden" value="${esc(internalId)}">
      <div class="modal-field-grid two">
        <div class="field">
          <label>名称</label>
          <input data-field="name" value="${esc(existing?.name || "")}" placeholder="例如：Unraid" required>
        </div>
        <div class="field">
          <label>状态</label>
          <label class="switch-row"><input data-field="enabled" type="checkbox" ${existing?.enabled === false ? "" : "checked"}><span>启用此实例</span></label>
        </div>
      </div>
      <div class="form-group">
        <div class="form-group-title">JSON-RPC 连接</div>
        <div class="field">
          <label>RPC 地址</label>
          <input data-field="url" value="${esc(existing?.url || "")}" placeholder="http://192.168.1.10:6800/jsonrpc" required>
        </div>
        <div class="field">
          <label>RPC Secret</label>
          <input data-field="secret" type="password" autocomplete="new-password" placeholder="${existing?.secret_set ? "已保存，留空保持不变" : "未设置可留空"}">
        </div>
      </div>`;
  } else {
    if (!configState.aria2_instances.length) {
      toast("请先添加 aria2 实例。", true);
      return;
    }
    $("resourceDialogEyebrow").textContent = "DOWNLOAD TARGET";
    $("resourceDialogTitle").textContent = titlePrefix + "下载目标";
    const internalId = existing?.id || makeInternalId("target");
    fields.innerHTML = `
      <input data-field="id" type="hidden" value="${esc(internalId)}">
      <div class="modal-field-grid two">
        <div class="field">
          <label>名称</label>
          <input data-field="name" value="${esc(existing?.name || "")}" placeholder="例如：电影" required>
        </div>
        <div class="field">
          <label>状态</label>
          <label class="switch-row"><input data-field="enabled" type="checkbox" ${existing?.enabled === false ? "" : "checked"}><span>启用此目标</span></label>
        </div>
      </div>
      <div class="form-group">
        <div class="field">
          <label>aria2 实例</label>
          <select data-field="aria2_instance" required>
            ${configState.aria2_instances.map(x => `<option value="${esc(x.id)}" ${x.id === existing?.aria2_instance ? "selected" : ""}>${esc(x.name)}</option>`).join("")}
          </select>
        </div>
        <div class="field">
          <label>下载目录</label>
          <input data-field="dir" value="${esc(existing?.dir || "/downloads/pikpak")}" placeholder="/downloads/pikpak" required>
        </div>
      </div>
      <div class="modal-field-grid two">
        <div class="field">
          <label>默认目标</label>
          <label class="switch-row"><input data-field="default" type="checkbox" ${existing?.default ? "checked" : ""}><span>未指定时使用</span></label>
        </div>
      </div>`;
  }

  if (kind === "account") {
    const usernameInput = fields.querySelector('[data-field="username"]');
    const nameInput = fields.querySelector('[data-field="name"]');
    let nameManuallyEdited = Boolean(existing?.name && existing.name !== defaultAccountName(existing?.username));

    nameInput.addEventListener("input", () => {
      nameManuallyEdited = nameInput.value.trim() !== "" && nameInput.value.trim() !== defaultAccountName(usernameInput.value);
    });
    usernameInput.addEventListener("input", () => {
      if (!nameManuallyEdited || !nameInput.value.trim()) {
        nameInput.value = defaultAccountName(usernameInput.value);
      }
    });
  }

  dialog.showModal();
}

function readResourceForm() {
  const root = $("resourceFormFields");
  const kind = editingResource.kind;
  const value = field => root.querySelector(`[data-field="${field}"]`)?.value ?? "";
  const checked = field => Boolean(root.querySelector(`[data-field="${field}"]`)?.checked);

  if (kind === "account") {
    const username = value("username").trim();
    return {
      id: value("id"),
      name: value("name").trim() || defaultAccountName(username),
      username,
      password: value("password"),
      max_jobs: Number(value("max_jobs") || 2),
      enabled: checked("enabled")
    };
  }
  if (kind === "aria2") {
    return {
      id: value("id"),
      name: value("name").trim(),
      url: value("url").trim(),
      secret: value("secret"),
      enabled: checked("enabled")
    };
  }
  return {
    id: value("id"),
    name: value("name").trim(),
    aria2_instance: value("aria2_instance"),
    dir: value("dir").trim(),
    default: checked("default"),
    enabled: checked("enabled")
  };
}

function payloadFromConfigState() {
  return {
    pikpak_accounts: configState.pikpak_accounts.map(x => ({
      id:x.id, name:x.name, username:x.username, password:x.password || "",
      max_jobs:x.max_jobs || 2, enabled:x.enabled !== false
    })),
    aria2_instances: configState.aria2_instances.map(x => ({
      id:x.id, name:x.name, url:x.url, secret:x.secret || "", enabled:x.enabled !== false
    })),
    targets: configState.targets.map(x => ({
      id:x.id, name:x.name, aria2_instance:x.aria2_instance, dir:x.dir,
      default:Boolean(x.default), enabled:x.enabled !== false
    }))
  };
}

async function persistConfigState() {
  const payload = payloadFromConfigState();
  await request(appConfigured ? "/api/v1/config" : "/api/v1/setup", {
    method: appConfigured ? "PUT" : "POST",
    headers: {"Content-Type":"application/json"},
    body: JSON.stringify(payload)
  });
  appConfigured = true;
  configState = normalizeConfigData(await request("/api/v1/config"));
  renderResourcePage(currentConfigView);
  await Promise.all([loadHealth(), loadTargets(), loadTasks()]);
  if (currentConfigView === "accounts" || currentConfigView === "aria2") {
    await refreshResourceStatus(false);
  }
}

async function saveResource() {
  const next = readResourceForm();
  const collection = resourceCollection(editingResource.kind);
  const existingIndex = collection.findIndex(x => x.id === next.id);

  if (editingResource.kind === "account" && existingIndex >= 0 && !next.password) {
    next.password_set = collection[existingIndex].password_set;
  }
  if (editingResource.kind === "aria2" && existingIndex >= 0 && !next.secret) {
    next.secret_set = collection[existingIndex].secret_set;
  }
  if (editingResource.kind === "target" && next.default) {
    configState.targets.forEach(x => x.default = false);
  }

  if (existingIndex >= 0) collection[existingIndex] = {...collection[existingIndex], ...next};
  else collection.push(next);

  try {
    $("resourceSaveBtn").disabled = true;
    await persistConfigState();
    $("resourceDialog").close();
    toast("配置已保存并生效。");
  } catch (err) {
    toast("保存失败：" + err.message, true);
  } finally {
    $("resourceSaveBtn").disabled = false;
  }
}

async function removeResource(kind, id) {
  const collection = resourceCollection(kind);
  const item = collection.find(x => x.id === id);
  if (!item) return;
  if (!window.confirm(`确定删除“${item.name}”吗？`)) return;

  const index = collection.findIndex(x => x.id === id);
  collection.splice(index, 1);
  try {
    await persistConfigState();
    toast("已删除。");
  } catch (err) {
    collection.splice(index, 0, item);
    renderResourcePage(currentConfigView);
    toast("删除失败：" + err.message, true);
  }
}

async function loadHealth() {
  const badge = $("healthBadge");
  const sidebarDot = $("sidebarStatusDot");
  const sidebarText = $("sidebarStatusText");
  try {
    const health = await request("/healthz");
    appConfigured = Boolean(health.configured);
    badge.querySelector(".status-dot").className = "status-dot ok";
    badge.querySelector("span:last-child").textContent = "服务运行中";
    sidebarDot.className = "status-dot ok";
    sidebarText.textContent = appConfigured ? "服务运行中" : "服务运行中 · 待配置";
  } catch (_) {
    badge.querySelector(".status-dot").className = "status-dot bad";
    badge.querySelector("span:last-child").textContent = "连接失败";
    sidebarDot.className = "status-dot bad";
    sidebarText.textContent = "连接失败";
  }
}

async function loadTargets() {
  if (targetsRefreshBusy) return;
  targetsRefreshBusy = true;
  try {
    const data = await request("/api/v1/targets");
    const targets = data.targets || [];
    const select = $("targetSelect");
    const current = select.value;
    const remembered = readLastTarget();
    if (!targets.length) {
      rememberLastTarget("");
      select.innerHTML = '<option value="">尚未配置下载目标</option>';
      $("composerHint").textContent = "还没有下载目标。可以从左侧“下载目标”添加，主页仍可正常使用。";
      return;
    }
    select.innerHTML = targets.map(t =>
      `<option value="${esc(t.id)}">${esc(t.name)} · ${esc(t.dir)}</option>`
    ).join("");
    const preferred =
      targets.find(t => t.id === current) ||
      targets.find(t => t.id === remembered) ||
      targets.find(t => t.default) ||
      targets[0];
    if (preferred) {
      select.value = preferred.id;
      rememberLastTarget(preferred.id);
    }
    $("composerHint").textContent = "PikPak 账号由系统自动选择，任务会固定到你选择的下载目标。";
  } catch (err) {
    $("targetSelect").innerHTML = '<option value="">下载目标获取失败</option>';
    $("composerHint").textContent = "无法读取下载目标：" + err.message;
  } finally {
    targetsRefreshBusy = false;
  }
}

async function progressForTask(task) {
  if (!["ARIA2_DOWNLOADING","VERIFYING","READY_TO_CLEANUP","PIKPAK_DELETING"].includes(task.status)) {
    return null;
  }
  try {
    const data = await request(`/api/v1/tasks/${encodeURIComponent(task.id)}/downloads`);
    const files = data.downloads || [];
    if (!files.length) return null;
    const total = files.reduce((sum, x) => sum + Number(x.total_length || x.expected_size || 0), 0);
    const done = files.reduce((sum, x) => sum + Number(x.completed_length || 0), 0);
    const completedFiles = files.filter(x => x.status === "COMPLETE").length;
    return {
      total,
      done,
      completedFiles,
      count: files.length,
      pct: total > 0 ? Math.min(100, done / total * 100) : 0
    };
  } catch (_) {
    return null;
  }
}

async function loadTasks() {
  if (taskRefreshBusy) return;
  taskRefreshBusy = true;
  try {
    const data = await request("/api/v1/tasks?limit=100");
    const activeTasks = (data.tasks || []).filter(task => ACTIVE.has(task.status));
    $("activeTaskCount").textContent = activeTasks.length;
    $("taskUpdated").textContent = "更新于 " + new Date().toLocaleTimeString("zh-CN", {hour12:false});

    if (!activeTasks.length) {
      $("activeTaskList").innerHTML = `
        <div class="empty-state">
          <div class="empty-icon">↓</div>
          <strong>暂无进行中的任务</strong>
          <span>在上方输入链接即可开始。</span>
        </div>`;
      return;
    }

    const progressEntries = await Promise.all(activeTasks.map(async task => [task.id, await progressForTask(task)]));
    const progress = Object.fromEntries(progressEntries);

    $("activeTaskList").innerHTML = activeTasks.map(task => {
      const p = progress[task.id];
      const pct = p ? p.pct : 0;
      const progressLabel = p
        ? (p.total > 0 ? `${fmtBytes(p.done)} / ${fmtBytes(p.total)}` : `${p.completedFiles}/${p.count} 文件`)
        : STATUS[task.status]?.[0] || "处理中";
      const target = task.target_name || task.target_id || "—";
      const account = task.pikpak_account_id || "自动选择";
      return `
        <article class="active-task" data-task-id="${esc(task.id)}">
          <div class="task-main">
            <div class="task-icon">${sourceIcon()}</div>
            <div style="min-width:0">
              <div class="task-name" title="${esc(task.source)}">${esc(taskTitle(task))}</div>
              <div class="task-meta">PikPak: ${esc(account)} · 目标: ${esc(target)} · aria2: ${esc(task.aria2_instance_id || "—")}</div>
            </div>
          </div>
          <div class="task-progress-wrap">
            <div class="task-progress-top">
              <div class="progress-track"><div class="progress-bar" style="width:${pct.toFixed(2)}%"></div></div>
              <span class="progress-percent">${p ? pct.toFixed(0) + "%" : "—"}</span>
            </div>
            <div class="progress-meta">${esc(progressLabel)}</div>
          </div>
          <div class="task-status">${statusBadge(task.status)}</div>
          <button class="detail-btn" type="button" aria-label="查看详情">
            <svg viewBox="0 0 24 24"><circle cx="5" cy="12" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/></svg>
          </button>
        </article>`;
    }).join("");

    $("activeTaskList").querySelectorAll("[data-task-id]").forEach(row => {
      row.addEventListener("click", () => openTask(row.dataset.taskId));
    });
  } catch (err) {
    $("activeTaskList").innerHTML = `
      <div class="empty-state">
        <strong>任务加载失败</strong>
        <span>${esc(err.message)}</span>
      </div>`;
  } finally {
    taskRefreshBusy = false;
  }
}

function historyMatches(task) {
  if (!TERMINAL_TASKS.has(task.status)) return false;
  if (historyFilter === "completed") return task.status === "COMPLETED";
  if (historyFilter === "failed") return FAILED_TASKS.has(task.status);
  if (historyFilter === "cancelled") return task.status === "CANCELLED";
  return true;
}

function historyTime(task) {
  return task.completed_at || task.updated_at || task.created_at;
}

function renderHistoryTasks(tasks) {
  const filtered = tasks.filter(historyMatches);
  $("historyTaskCount").textContent = `${filtered.length} 条记录`;
  $("historyUpdated").textContent = "更新于 " + new Date().toLocaleTimeString("zh-CN", {hour12:false});

  if (!filtered.length) {
    $("historyTaskList").innerHTML = `
      <div class="empty-state">
        <div class="empty-icon">↺</div>
        <strong>暂无符合条件的历史任务</strong>
        <span>任务结束后会出现在这里。</span>
      </div>`;
    return;
  }

  $("historyTaskList").innerHTML = filtered.map(task => {
    const target = task.target_name || task.target_id || "—";
    const account = task.pikpak_account_id || "—";
    const error = task.error ? `<div class="history-error">${esc(truncate(task.error, 90))}</div>` : "";
    return `
      <article class="history-task" data-task-id="${esc(task.id)}">
        <div class="task-main">
          <div class="task-icon">${sourceIcon()}</div>
          <div style="min-width:0">
            <div class="task-name" title="${esc(task.source)}">${esc(taskTitle(task))}</div>
            <div class="task-meta">PikPak: ${esc(account)} · 目标: ${esc(target)} · aria2: ${esc(task.aria2_instance_id || "—")}</div>
            ${error}
          </div>
        </div>
        <div class="history-time">
          <strong>${esc(fmtTime(historyTime(task)))}</strong>
          <span>${task.completed_at ? "结束时间" : "最后更新"}</span>
        </div>
        <div class="task-status">${statusBadge(task.status)}</div>
        <button class="detail-btn" type="button" aria-label="查看详情">
          <svg viewBox="0 0 24 24"><circle cx="5" cy="12" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/></svg>
        </button>
      </article>`;
  }).join("");

  $("historyTaskList").querySelectorAll("[data-task-id]").forEach(row => {
    row.addEventListener("click", () => openTask(row.dataset.taskId));
  });
}

async function loadHistoryTasks() {
  if (historyRefreshBusy) return;
  historyRefreshBusy = true;
  try {
    const data = await request("/api/v1/tasks?limit=200");
    renderHistoryTasks(data.tasks || []);
  } catch (err) {
    $("historyTaskList").innerHTML = `
      <div class="empty-state">
        <strong>历史任务加载失败</strong>
        <span>${esc(err.message)}</span>
      </div>`;
  } finally {
    historyRefreshBusy = false;
  }
}

function showHistory() {
  $("homeView").classList.add("hidden");
  $("setupScreen").classList.add("hidden");
  $("historyView").classList.remove("hidden");
  setActiveNav("historyNav");
  loadHistoryTasks();
}

async function openTask(id) {
  const dialog = $("taskDialog");
  $("dialogTitle").textContent = id;
  $("dialogBody").innerHTML = '<div class="empty-state"><span>正在加载…</span></div>';
  dialog.showModal();
  try {
    const [task, downloadsData, eventsData] = await Promise.all([
      request(`/api/v1/tasks/${encodeURIComponent(id)}`),
      request(`/api/v1/tasks/${encodeURIComponent(id)}/downloads`),
      request(`/api/v1/tasks/${encodeURIComponent(id)}/events?limit=200`)
    ]);
    const downloads = downloadsData.downloads || [];
    const events = (eventsData.events || []).filter(isErrorHistoryEvent);
    const canRetry = FAILED_TASKS.has(task.status);
    $("dialogBody").innerHTML = `
      <dl class="detail-grid">
        <dt>状态</dt><dd>${statusBadge(task.status)}</dd>
        <dt>来源</dt><dd>${esc(task.source)}</dd>
        <dt>Download Target</dt><dd>${esc(task.target_name || "—")} (${esc(task.target_id || "—")})</dd>
        <dt>aria2 实例</dt><dd>${esc(task.aria2_instance_id || "—")}</dd>
        <dt>下载目录</dt><dd>${esc(task.download_dir || "—")}</dd>
        <dt>PikPak 账号</dt><dd>${esc(task.pikpak_account_id || "—")}</dd>
        <dt>PikPak Task ID</dt><dd>${esc(task.pikpak_task_id || "—")}</dd>
        <dt>PikPak Root ID</dt><dd>${esc(task.pikpak_root_file_id || "—")}</dd>
        <dt>当前重试次数</dt><dd>${task.retry_count ?? 0}</dd>
        <dt>手动重试次数</dt><dd>${task.manual_retry_count ?? 0}</dd>
        <dt>创建时间</dt><dd>${fmtTime(task.created_at)}</dd>
        <dt>更新时间</dt><dd>${fmtTime(task.updated_at)}</dd>
        <dt>当前错误</dt><dd>${esc(task.error || "—")}</dd>
      </dl>
      ${canRetry ? `
        <div class="task-retry-panel">
          <div>
            <strong>重新尝试这个失败任务</strong>
            <span>保留错误历史，并重新获得完整 10 次自动重试机会。</span>
          </div>
          <button id="retryTaskBtn" class="primary-btn compact" type="button">重试任务</button>
        </div>` : ""}
      <div class="detail-section">
        <h3>错误历史（${events.length}）</h3>
        <div class="error-history">
          ${events.length ? events.map(event => `
            <div class="error-history-row">
              <div class="error-history-head">
                <strong>${esc(eventLabel(event.type))}</strong>
                <span>${esc(fmtTime(event.created_at))}</span>
              </div>
              <div class="error-history-message">${esc(event.message || "—")}</div>
              <div class="error-history-type">${esc(event.type || "")}</div>
            </div>
          `).join("") : '<div class="error-history-empty">暂无错误或重试记录</div>'}
        </div>
      </div>
      <div class="detail-section">
        <h3>下载文件（${downloads.length}）</h3>
        ${downloads.length ? downloads.map(d => `
          <div class="download-row">
            <div>
              <div class="download-name">${esc(d.relative_path)}</div>
              <div class="download-meta">GID ${esc(d.aria2_gid || "—")} · ${esc(d.aria2_instance_id || "—")}</div>
              <div class="download-meta">普通重试 ${d.retry_count ?? 0}/10 · EOF 重试 ${d.eof_retry_count ?? 0}/10</div>
              ${d.last_error ? `<div class="download-error">${esc(d.last_error)}</div>` : ""}
            </div>
            <div>
              ${statusBadge(d.status)}
              <div class="download-meta">${fmtBytes(d.completed_length)} / ${fmtBytes(d.total_length || d.expected_size)}</div>
            </div>
          </div>
        `).join("") : '<div class="empty-state"><span>尚未创建 aria2 下载记录</span></div>'}
      </div>`;

    const retryButton = $("retryTaskBtn");
    if (retryButton) {
      retryButton.addEventListener("click", async () => {
        if (!window.confirm("确定重试这个失败任务吗？它会重新获得完整 10 次自动重试机会。")) return;
        retryButton.disabled = true;
        try {
          await request(`/api/v1/tasks/${encodeURIComponent(id)}/retry`, {method:"POST"});
          toast("任务已重新开始，并重新获得 10 次重试机会。");
          await Promise.all([loadTasks(), loadHistoryTasks()]);
          await openTask(id);
        } catch (err) {
          toast("重试失败：" + err.message, true);
          retryButton.disabled = false;
        }
      });
    }
  } catch (err) {
    $("dialogBody").innerHTML = `<div class="empty-state"><strong>加载失败</strong><span>${esc(err.message)}</span></div>`;
  }
}

function startDashboard() {
  if (dashboardStarted) return;
  dashboardStarted = true;
  loadHealth();
  loadTargets();
  loadTasks();
  setInterval(loadHealth, 15000);
  setInterval(loadTargets, 60000);
  setInterval(loadTasks, 3000);
}

async function bootstrap() {
  try {
    const state = await request("/api/v1/setup");
    appConfigured = Boolean(state.configured);
  } catch (_) {
    appConfigured = false;
  }
  showHome();
  startDashboard();
}

$("homeNav").addEventListener("click", showHome);
$("historyNav").addEventListener("click", showHistory);
document.querySelectorAll("[data-config-view]").forEach(button => {
  button.addEventListener("click", () => openConfigView(button.dataset.configView || "settings"));
});

document.querySelectorAll("[data-history-filter]").forEach(button => {
  button.addEventListener("click", async () => {
    historyFilter = button.dataset.historyFilter || "all";
    document.querySelectorAll("[data-history-filter]").forEach(item => {
      item.classList.toggle("active", item === button);
    });
    await loadHistoryTasks();
  });
});

$("settingsCancelBtn").addEventListener("click", showHome);

$("addAccountBtn").addEventListener("click", () => openResourceDialog("account"));
$("addAria2Btn").addEventListener("click", () => openResourceDialog("aria2"));
$("addTargetBtn").addEventListener("click", () => openResourceDialog("target"));
$("checkAccountsBtn").addEventListener("click", () => refreshResourceStatus(true));
$("checkAria2Btn").addEventListener("click", () => refreshResourceStatus(true));

$("resourceDialogClose").addEventListener("click", () => $("resourceDialog").close());
$("resourceCancelBtn").addEventListener("click", () => $("resourceDialog").close());
$("resourceDialog").addEventListener("click", event => {
  if (event.target === $("resourceDialog")) $("resourceDialog").close();
});
$("resourceForm").addEventListener("submit", async event => {
  event.preventDefault();
  await saveResource();
});

$("taskForm").addEventListener("submit", async (event) => {
  event.preventDefault();
  const source = $("sourceInput").value.trim();
  const target = $("targetSelect").value;
  if (!source) {
    toast("请先输入下载链接或磁链。", true);
    $("sourceInput").focus();
    return;
  }
  if (!target) {
    toast("请先添加一个下载目标。", true);
    openConfigView("targets");
    return;
  }

  const button = $("submitBtn");
  button.disabled = true;
  try {
    const task = await request("/api/v1/tasks", {
      method: "POST",
      headers: {"Content-Type":"application/json"},
      body: JSON.stringify({url:source, target})
    });
    $("sourceInput").value = "";
    toast("任务已创建：" + task.id);
    await loadTasks();
  } catch (err) {
    if (err.status === 409 && err.body?.existing_task_id) {
      toast("该资源已有任务：" + err.body.existing_task_id, true);
    } else if (err.status === 503) {
      toast("请先完成所需资源配置。", true);
      openConfigView("accounts");
    } else {
      toast("创建失败：" + err.message, true);
    }
  } finally {
    button.disabled = false;
  }
});

$("targetSelect").addEventListener("change", event => {
  rememberLastTarget(event.target.value);
});

$("refreshBtn").addEventListener("click", async () => {
  $("refreshBtn").disabled = true;
  const jobs = [loadHealth(), loadTargets(), loadTasks()];
  if (!$("historyView").classList.contains("hidden")) jobs.push(loadHistoryTasks());
  await Promise.all(jobs);
  $("refreshBtn").disabled = false;
});

$("dialogClose").addEventListener("click", () => $("taskDialog").close());
$("taskDialog").addEventListener("click", (event) => {
  if (event.target === $("taskDialog")) $("taskDialog").close();
});

bootstrap();
