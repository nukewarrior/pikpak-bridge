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

let appConfigured = false;
let dashboardStarted = false;
let taskRefreshBusy = false;
let targetsRefreshBusy = false;
let toastTimer;
let accountSeq = 0;
let aria2Seq = 0;
let targetSeq = 0;
let currentConfigView = "accounts";

function esc(value) {
  return String(value ?? "")
    .replaceAll("&","&amp;").replaceAll("<","&lt;").replaceAll(">","&gt;")
    .replaceAll('"',"&quot;").replaceAll("'","&#039;");
}

function truncate(value, length=72) {
  const s = String(value ?? "");
  return s.length > length ? s.slice(0, length - 1) + "…" : s;
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

  resetConfigForm();
  $("homeView").classList.add("hidden");
  $("setupScreen").classList.remove("hidden");

  (data?.pikpak_accounts || []).forEach(item => $("setupAccounts").appendChild(accountEntry(item)));
  (data?.aria2_instances || []).forEach(item => $("setupAria2").appendChild(aria2Entry(item)));
  (data?.targets || []).forEach(item => $("setupTargets").appendChild(targetEntry(item)));
  refreshTargetAria2Options();

  document.querySelectorAll(".resource-page").forEach(section => section.classList.add("hidden"));
  $(meta.section).classList.remove("hidden");
  $("setupActions").classList.toggle("hidden", currentConfigView === "settings");

  $("setupEyebrow").textContent = meta.eyebrow;
  $("setupTitle").textContent = meta.title;
  $("setupDescription").textContent = meta.description;
  setActiveNav(meta.nav);
  window.scrollTo({top:0, behavior:"smooth"});
}

async function openConfigView(view="accounts") {
  try {
    let data = {pikpak_accounts:[], aria2_instances:[], targets:[]};
    if (appConfigured) data = await request("/api/v1/config");
    showConfigView(data, view);
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

function accountEntry(values={}) {
  accountSeq += 1;
  const index = accountSeq;
  const id = values.id || "pp" + String(index).padStart(2,"0");
  const div = document.createElement("div");
  div.className = "setup-entry";
  div.dataset.kind = "account";
  div.innerHTML = `
    <div class="setup-entry-head">
      <strong>PikPak 账号 #${index}</strong>
      <button class="remove-btn" type="button">移除</button>
    </div>
    <div class="setup-grid">
      <div class="field">
        <label>稳定 ID</label>
        <input data-field="id" value="${esc(id)}" required>
      </div>
      <div class="field">
        <label>显示名称</label>
        <input data-field="name" value="${esc(values.name || id)}" required>
      </div>
      <div class="field">
        <label>账号</label>
        <input data-field="username" value="${esc(values.username || "")}" autocomplete="username" placeholder="邮箱或手机号" required>
      </div>
      <div class="field">
        <label>最大并发离线任务</label>
        <input data-field="max_jobs" type="number" min="1" max="20" value="${esc(values.max_jobs || 2)}" required>
      </div>
      <div class="field">
        <label>状态</label>
        <label class="checkbox-row"><input data-field="enabled" type="checkbox" ${values.enabled === false ? "" : "checked"}> 启用此账号</label>
      </div>
      <div class="field wide">
        <label>密码</label>
        <input data-field="password" type="password" autocomplete="new-password" placeholder="${values.password_set ? "已保存，留空保持不变" : "PikPak 登录密码"}" ${values.password_set ? "" : "required"}>
      </div>
    </div>`;
  div.querySelector(".remove-btn").addEventListener("click", () => div.remove());
  return div;
}

function aria2Entry(values={}) {
  aria2Seq += 1;
  const index = aria2Seq;
  const id = values.id || (index === 1 ? "unraid" : "aria2-" + String(index).padStart(2,"0"));
  const div = document.createElement("div");
  div.className = "setup-entry";
  div.dataset.kind = "aria2";
  div.innerHTML = `
    <div class="setup-entry-head">
      <strong>aria2 实例 #${index}</strong>
      <button class="remove-btn" type="button">移除</button>
    </div>
    <div class="setup-grid">
      <div class="field">
        <label>稳定 ID</label>
        <input data-field="id" value="${esc(id)}" required>
      </div>
      <div class="field">
        <label>显示名称</label>
        <input data-field="name" value="${esc(values.name || id)}" required>
      </div>
      <div class="field wide">
        <label>JSON-RPC 地址</label>
        <input data-field="url" value="${esc(values.url || "")}" placeholder="http://192.168.1.10:6800/jsonrpc" required>
      </div>
      <div class="field">
        <label>RPC Secret</label>
        <input data-field="secret" type="password" autocomplete="new-password" value="${esc(values.secret || "")}" placeholder="${values.secret_set ? "已保存，留空保持不变" : "未设置可留空"}">
      </div>
      <div class="field">
        <label>状态</label>
        <label class="checkbox-row"><input data-field="enabled" type="checkbox" ${values.enabled === false ? "" : "checked"}> 启用此实例</label>
      </div>
    </div>`;
  div.querySelector('[data-field="id"]').addEventListener("input", refreshTargetAria2Options);
  div.querySelector('[data-field="name"]').addEventListener("input", refreshTargetAria2Options);
  div.querySelector(".remove-btn").addEventListener("click", () => {
    div.remove();
    refreshTargetAria2Options();
  });
  return div;
}

function targetEntry(values={}) {
  targetSeq += 1;
  const index = targetSeq;
  const id = values.id || (index === 1 ? "default" : "target-" + String(index).padStart(2,"0"));
  const div = document.createElement("div");
  div.className = "setup-entry";
  div.dataset.kind = "target";
  div.innerHTML = `
    <div class="setup-entry-head">
      <strong>Download Target #${index}</strong>
      <button class="remove-btn" type="button">移除</button>
    </div>
    <div class="setup-grid">
      <div class="field">
        <label>稳定 ID</label>
        <input data-field="id" value="${esc(id)}" required>
      </div>
      <div class="field">
        <label>显示名称</label>
        <input data-field="name" value="${esc(values.name || (index === 1 ? "默认下载" : id))}" required>
      </div>
      <div class="field">
        <label>aria2 实例</label>
        <select data-field="aria2_instance" required></select>
      </div>
      <div class="field">
        <label>默认目标</label>
        <label class="checkbox-row"><input data-field="default" type="checkbox" ${values.default === true || (values.default === undefined && index === 1) ? "checked" : ""}> 未指定 Target 时使用</label>
      </div>
      <div class="field">
        <label>状态</label>
        <label class="checkbox-row"><input data-field="enabled" type="checkbox" ${values.enabled === false ? "" : "checked"}> 启用此目标</label>
      </div>
      <div class="field wide">
        <label>下载目录</label>
        <input data-field="dir" value="${esc(values.dir || "/downloads/pikpak")}" placeholder="/downloads/pikpak" required>
        <span class="hint">填写 aria2 所在机器看到的目录。</span>
      </div>
    </div>`;

  div.querySelector('[data-field="default"]').addEventListener("change", (event) => {
    if (!event.target.checked) return;
    $("setupTargets").querySelectorAll('[data-field="default"]').forEach(el => {
      if (el !== event.target) el.checked = false;
    });
  });
  div.querySelector(".remove-btn").addEventListener("click", () => {
    const wasDefault = div.querySelector('[data-field="default"]').checked;
    div.remove();
    if (wasDefault) {
      const first = $("setupTargets").querySelector('[data-field="default"]');
      if (first) first.checked = true;
    }
  });
  requestAnimationFrame(() => {
    refreshTargetAria2Options();
    if (values.aria2_instance) {
      div.querySelector('[data-field="aria2_instance"]').value = values.aria2_instance;
    }
  });
  return div;
}

function resetConfigForm() {
  $("setupAccounts").innerHTML = "";
  $("setupAria2").innerHTML = "";
  $("setupTargets").innerHTML = "";
  accountSeq = 0;
  aria2Seq = 0;
  targetSeq = 0;
}

function configuredAria2Options() {
  return [...$("setupAria2").querySelectorAll(".setup-entry")].map(row => ({
    id: row.querySelector('[data-field="id"]').value.trim(),
    name: row.querySelector('[data-field="name"]').value.trim()
  })).filter(x => x.id);
}

function refreshTargetAria2Options() {
  const instances = configuredAria2Options();
  $("setupTargets").querySelectorAll('[data-field="aria2_instance"]').forEach(select => {
    const current = select.value;
    select.innerHTML = instances.length
      ? instances.map(x => `<option value="${esc(x.id)}">${esc(x.name || x.id)} · ${esc(x.id)}</option>`).join("")
      : '<option value="">请先添加 aria2 实例</option>';
    if (instances.some(x => x.id === current)) select.value = current;
  });
}

function collectSetup() {
  const accounts = [...$("setupAccounts").querySelectorAll(".setup-entry")].map(row => ({
    id: row.querySelector('[data-field="id"]').value.trim(),
    name: row.querySelector('[data-field="name"]').value.trim(),
    username: row.querySelector('[data-field="username"]').value.trim(),
    password: row.querySelector('[data-field="password"]').value,
    max_jobs: Number(row.querySelector('[data-field="max_jobs"]').value),
    enabled: row.querySelector('[data-field="enabled"]').checked
  }));
  const instances = [...$("setupAria2").querySelectorAll(".setup-entry")].map(row => ({
    id: row.querySelector('[data-field="id"]').value.trim(),
    name: row.querySelector('[data-field="name"]').value.trim(),
    url: row.querySelector('[data-field="url"]').value.trim(),
    secret: row.querySelector('[data-field="secret"]').value,
    enabled: row.querySelector('[data-field="enabled"]').checked
  }));
  const targets = [...$("setupTargets").querySelectorAll(".setup-entry")].map(row => ({
    id: row.querySelector('[data-field="id"]').value.trim(),
    name: row.querySelector('[data-field="name"]').value.trim(),
    aria2_instance: row.querySelector('[data-field="aria2_instance"]').value,
    dir: row.querySelector('[data-field="dir"]').value.trim(),
    default: row.querySelector('[data-field="default"]').checked,
    enabled: row.querySelector('[data-field="enabled"]').checked
  }));
  return {pikpak_accounts: accounts, aria2_instances: instances, targets};
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
    if (!targets.length) {
      select.innerHTML = '<option value="">尚未配置下载目标</option>';
      $("composerHint").textContent = "还没有下载目标。可以从左侧“下载目标”添加，主页仍可正常使用。";
      return;
    }
    select.innerHTML = targets.map(t =>
      `<option value="${esc(t.id)}">${esc(t.name)} · ${esc(t.dir)}</option>`
    ).join("");
    const preferred = targets.find(t => t.id === current) || targets.find(t => t.default) || targets[0];
    if (preferred) select.value = preferred.id;
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

async function openTask(id) {
  const dialog = $("taskDialog");
  $("dialogTitle").textContent = id;
  $("dialogBody").innerHTML = '<div class="empty-state"><span>正在加载…</span></div>';
  dialog.showModal();
  try {
    const [task, downloadsData] = await Promise.all([
      request(`/api/v1/tasks/${encodeURIComponent(id)}`),
      request(`/api/v1/tasks/${encodeURIComponent(id)}/downloads`)
    ]);
    const downloads = downloadsData.downloads || [];
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
        <dt>重试次数</dt><dd>${task.retry_count ?? 0}</dd>
        <dt>创建时间</dt><dd>${fmtTime(task.created_at)}</dd>
        <dt>更新时间</dt><dd>${fmtTime(task.updated_at)}</dd>
        <dt>错误</dt><dd>${esc(task.error || "—")}</dd>
      </dl>
      <div class="detail-section">
        <h3>下载文件（${downloads.length}）</h3>
        ${downloads.length ? downloads.map(d => `
          <div class="download-row">
            <div>
              <div class="download-name">${esc(d.relative_path)}</div>
              <div class="download-meta">GID ${esc(d.aria2_gid || "—")} · ${esc(d.aria2_instance_id || "—")}</div>
            </div>
            <div>
              ${statusBadge(d.status)}
              <div class="download-meta">${fmtBytes(d.completed_length)} / ${fmtBytes(d.total_length || d.expected_size)}</div>
            </div>
          </div>
        `).join("") : '<div class="empty-state"><span>尚未创建 aria2 下载记录</span></div>'}
      </div>`;
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
document.querySelectorAll("[data-config-view]").forEach(button => {
  button.addEventListener("click", () => openConfigView(button.dataset.configView || "settings"));
});

$("settingsCancelBtn").addEventListener("click", showHome);

$("addAccountBtn").addEventListener("click", () => {
  $("setupAccounts").appendChild(accountEntry());
});
$("addAria2Btn").addEventListener("click", () => {
  $("setupAria2").appendChild(aria2Entry());
  refreshTargetAria2Options();
});
$("addTargetBtn").addEventListener("click", () => {
  $("setupTargets").appendChild(targetEntry());
  refreshTargetAria2Options();
});

$("setupForm").addEventListener("submit", async (event) => {
  event.preventDefault();
  const button = $("setupSubmitBtn");
  button.disabled = true;
  try {
    const payload = collectSetup();
    await request(appConfigured ? "/api/v1/config" : "/api/v1/setup", {
      method: appConfigured ? "PUT" : "POST",
      headers: {"Content-Type":"application/json"},
      body: JSON.stringify(payload)
    });
    appConfigured = true;
    toast("配置已保存并生效。");
    await Promise.all([loadHealth(), loadTargets(), loadTasks()]);
    await openConfigView(currentConfigView);
  } catch (err) {
    toast("保存失败：" + err.message, true);
  } finally {
    button.disabled = false;
  }
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

$("refreshBtn").addEventListener("click", async () => {
  $("refreshBtn").disabled = true;
  await Promise.all([loadHealth(), loadTargets(), loadTasks()]);
  $("refreshBtn").disabled = false;
});

$("dialogClose").addEventListener("click", () => $("taskDialog").close());
$("taskDialog").addEventListener("click", (event) => {
  if (event.target === $("taskDialog")) $("taskDialog").close();
});

bootstrap();
