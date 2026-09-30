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
const FAILED = new Set(["PIKPAK_FAILED","ARIA2_FAILED","VERIFY_FAILED","CLEANUP_FAILED"]);

let taskRefreshBusy = false;
let statusRefreshBusy = false;
let targetsRefreshBusy = false;
let dashboardStarted = false;
let toastTimer;
let accountSeq = 0;
let aria2Seq = 0;
let targetSeq = 0;
let configMode = "setup";

function esc(value) {
  return String(value ?? "")
    .replaceAll("&","&amp;").replaceAll("<","&lt;").replaceAll(">","&gt;")
    .replaceAll('"',"&quot;").replaceAll("'","&#039;");
}

function truncate(value, length=70) {
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
  div.querySelector(".remove-btn").addEventListener("click", () => {
    if ($("setupAccounts").children.length <= 1) {
      toast("至少保留一个 PikPak 账号。", true);
      return;
    }
    div.remove();
  });
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
    if ($("setupAria2").children.length <= 1) {
      toast("至少保留一个 aria2 实例。", true);
      return;
    }
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
    if ($("setupTargets").children.length <= 1) {
      toast("至少保留一个 Download Target。", true);
      return;
    }
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

function resetConfigForm() {
  $("setupAccounts").innerHTML = "";
  $("setupAria2").innerHTML = "";
  $("setupTargets").innerHTML = "";
  accountSeq = 0;
  aria2Seq = 0;
  targetSeq = 0;
}

function showSetup(mode="setup", data=null) {
  configMode = mode;
  resetConfigForm();
  $("dashboard").classList.add("hidden");
  $("setupScreen").classList.remove("hidden");

  const editing = mode === "settings";
  $("setupEyebrow").textContent = editing ? "SETTINGS" : "FIRST RUN";
  $("setupTitle").textContent = editing ? "配置 PikPak Bridge" : "设置 PikPak Bridge";
  $("setupDescription").textContent = editing
    ? "修改账号池、aria2 实例和 Download Target；保存后服务会热重载。"
    : "账号池负责自动选择离线账号，Download Target 决定最终下载位置。";
  $("setupSubmitLabel").textContent = editing ? "保存并热重载" : "保存并开始使用";
  $("settingsCancelBtn").classList.toggle("hidden", !editing);

  if (editing && data) {
    (data.pikpak_accounts || []).forEach(item => $("setupAccounts").appendChild(accountEntry(item)));
    (data.aria2_instances || []).forEach(item => $("setupAria2").appendChild(aria2Entry(item)));
    (data.targets || []).forEach(item => $("setupTargets").appendChild(targetEntry(item)));
  } else {
    $("setupAccounts").appendChild(accountEntry());
    $("setupAria2").appendChild(aria2Entry({id:"unraid", name:"Unraid"}));
    $("setupTargets").appendChild(targetEntry({
      id:"default", name:"默认下载", aria2_instance:"unraid", dir:"/downloads/pikpak", default:true
    }));
  }
  refreshTargetAria2Options();
}

function showDashboard() {
  $("setupScreen").classList.add("hidden");
  $("dashboard").classList.remove("hidden");
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

async function bootstrap() {
  try {
    const state = await request("/api/v1/setup");
    if (!state.configured) {
      showSetup();
      return;
    }
    showDashboard();
    startDashboard();
  } catch (err) {
    showSetup();
    toast("无法读取初始化状态：" + err.message, true);
  }
}

function startDashboard() {
  if (dashboardStarted) return;
  dashboardStarted = true;
  loadHealth();
  loadTargets();
  loadTasks();
  loadStatus();
  setInterval(loadHealth, 15000);
  setInterval(loadTasks, 3000);
  setInterval(loadStatus, 60000);
  setInterval(loadTargets, 60000);
}

async function loadHealth() {
  const badge = $("healthBadge");
  try {
    const health = await request("/healthz");
    badge.className = health.configured ? "health-badge ok" : "health-badge";
    badge.innerHTML = '<span class="dot"></span><span>' + (health.configured ? "服务在线" : "等待配置") + '</span>';
  } catch (_) {
    badge.className = "health-badge bad";
    badge.innerHTML = '<span class="dot"></span><span>连接失败</span>';
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
    select.innerHTML = targets.map(t =>
      `<option value="${esc(t.id)}">${esc(t.name)} · ${esc(t.aria2_instance)} · ${esc(t.dir)}</option>`
    ).join("");
    const preferred = targets.find(t => t.id === current) || targets.find(t => t.default) || targets[0];
    if (preferred) select.value = preferred.id;
    $("submitBtn").disabled = targets.length === 0;

    $("targetCount").textContent = targets.length;
    $("targetList").innerHTML = targets.length ? targets.map(t => `
      <div class="resource">
        <div class="resource-top"><span class="resource-name">${esc(t.name)}</span>${t.default ? '<span class="badge success">默认</span>' : ''}</div>
        <div class="resource-meta"><span>${esc(t.id)}</span><span>${esc(t.aria2_instance)}</span></div>
        <div class="resource-meta"><span>目录</span><span title="${esc(t.dir)}">${esc(truncate(t.dir, 32))}</span></div>
      </div>
    `).join("") : '<div class="empty-block">未配置 Download Target</div>';
  } catch (err) {
    $("targetList").innerHTML = `<div class="empty-block">目标获取失败：${esc(err.message)}</div>`;
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
    return { total, done, completedFiles, count:files.length, pct: total > 0 ? Math.min(100, done / total * 100) : 0 };
  } catch (_) { return null; }
}

async function loadTasks() {
  if (taskRefreshBusy) return;
  taskRefreshBusy = true;
  try {
    const data = await request("/api/v1/tasks?limit=100");
    const tasks = data.tasks || [];
    const progressEntries = await Promise.all(tasks.map(async t => [t.id, await progressForTask(t)]));
    const progress = Object.fromEntries(progressEntries);

    $("statTotal").textContent = tasks.length;
    $("statActive").textContent = tasks.filter(t => ACTIVE.has(t.status)).length;
    $("statCompleted").textContent = tasks.filter(t => t.status === "COMPLETED").length;
    $("statFailed").textContent = tasks.filter(t => FAILED.has(t.status)).length;
    $("taskUpdated").textContent = "更新于 " + new Date().toLocaleTimeString("zh-CN", {hour12:false});

    const rows = $("taskRows");
    if (!tasks.length) {
      rows.innerHTML = '<tr><td colspan="6" class="empty">还没有任务，先在上方添加一个链接。</td></tr>';
      return;
    }
    rows.innerHTML = tasks.map(task => {
      const p = progress[task.id];
      let progressHtml = '<span class="muted">—</span>';
      if (p) {
        const label = p.total > 0 ? `${p.pct.toFixed(1)}% · ${fmtBytes(p.done)} / ${fmtBytes(p.total)}` : `${p.completedFiles}/${p.count} 文件`;
        progressHtml = `<div class="progress"><div class="progress-track"><div class="progress-bar" style="width:${p.pct.toFixed(2)}%"></div></div><div class="progress-label">${esc(label)}</div></div>`;
      } else if (task.status === "COMPLETED") {
        progressHtml = '<span class="muted">100%</span>';
      }
      const targetLabel = `${task.target_name || task.target_id || "—"} · ${task.aria2_instance_id || "—"}`;
      return `<tr data-task-id="${esc(task.id)}">
        <td>${statusBadge(task.status)}</td>
        <td class="source-cell"><div class="source-main" title="${esc(task.source)}">${esc(truncate(task.source))}</div><div class="source-sub">${esc(task.source_type || "")} · ${esc(task.id)}</div></td>
        <td>${progressHtml}</td>
        <td>${esc(task.pikpak_account_id || "—")}</td>
        <td title="${esc(task.download_dir || "")}">${esc(targetLabel)}</td>
        <td class="muted">${fmtTime(task.updated_at)}</td>
      </tr>`;
    }).join("");

    rows.querySelectorAll("tr[data-task-id]").forEach(row => {
      row.addEventListener("click", () => openTask(row.dataset.taskId));
    });
  } catch (err) {
    $("taskRows").innerHTML = `<tr><td colspan="6" class="empty">任务加载失败：${esc(err.message)}</td></tr>`;
  } finally {
    taskRefreshBusy = false;
  }
}

function resourceBadge(ok, state) {
  if (ok) return '<span class="badge success">正常</span>';
  if (state === "DISABLED") return '<span class="badge">禁用</span>';
  return `<span class="badge fail">${esc(state || "异常")}</span>`;
}

async function loadStatus() {
  if (statusRefreshBusy) return;
  statusRefreshBusy = true;
  try {
    const data = await request("/api/v1/status");
    const accounts = data.pikpak_accounts || [];
    const instances = data.aria2_instances || [];
    $("accountCount").textContent = accounts.length;
    $("aria2Count").textContent = instances.length;

    $("accountList").innerHTML = accounts.length ? accounts.map(a => `
      <div class="resource">
        <div class="resource-top"><span class="resource-name">${esc(a.name || a.id)}</span>${resourceBadge(a.enabled && a.healthy, a.state)}</div>
        <div class="resource-meta"><span>${esc(a.id)}</span><span>离线额度 <b>${a.quota_remaining ?? 0}/${a.quota_total ?? 0}</b></span></div>
        <div class="resource-meta"><span>任务 ${a.active_jobs ?? 0}/${a.max_jobs ?? 0}</span><span>${fmtBytes(a.storage_free)}</span></div>
        ${a.error ? `<div class="resource-error">${esc(a.error)}</div>` : ""}
      </div>
    `).join("") : '<div class="empty-block">未配置 PikPak 账号</div>';

    $("aria2List").innerHTML = instances.length ? instances.map(a => `
      <div class="resource">
        <div class="resource-top"><span class="resource-name">${esc(a.name || a.id)}</span>${resourceBadge(a.enabled && a.healthy, a.enabled ? "OFFLINE" : "DISABLED")}</div>
        <div class="resource-meta"><span>${esc(a.id)}</span><span>Active <b>${a.active ?? 0}</b></span></div>
        <div class="resource-meta"><span>Waiting</span><span>${a.waiting ?? 0}</span></div>
        ${a.error ? `<div class="resource-error">${esc(a.error)}</div>` : ""}
      </div>
    `).join("") : '<div class="empty-block">未配置 aria2 实例</div>';
  } catch (err) {
    $("accountList").innerHTML = `<div class="empty-block">状态获取失败：${esc(err.message)}</div>`;
    $("aria2List").innerHTML = '<div class="empty-block">状态获取失败</div>';
  } finally {
    statusRefreshBusy = false;
  }
}

async function openTask(id) {
  const dialog = $("taskDialog");
  $("dialogTitle").textContent = id;
  $("dialogBody").innerHTML = '<div class="empty-block">正在加载…</div>';
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
      <div class="detail-section"><h3>下载文件（${downloads.length}）</h3>
        ${downloads.length ? downloads.map(d => `
          <div class="download-row">
            <div><div class="download-name">${esc(d.relative_path)}</div><div class="download-meta">GID ${esc(d.aria2_gid || "—")} · ${esc(d.aria2_instance_id || "—")}</div></div>
            <div>${statusBadge(d.status)}</div>
            <div class="download-meta">${fmtBytes(d.completed_length)} / ${fmtBytes(d.total_length || d.expected_size)}</div>
            <div class="download-meta">${d.last_error ? esc(d.last_error) : ""}</div>
          </div>
        `).join("") : '<div class="empty-block">尚未创建 aria2 下载记录</div>'}
      </div>`;
  } catch (err) {
    $("dialogBody").innerHTML = `<div class="empty-block">加载失败：${esc(err.message)}</div>`;
  }
}

$("settingsBtn").addEventListener("click", async () => {
  $("settingsBtn").disabled = true;
  try {
    const data = await request("/api/v1/config");
    showSetup("settings", data);
  } catch (err) {
    toast("配置加载失败：" + err.message, true);
  } finally {
    $("settingsBtn").disabled = false;
  }
});

$("settingsCancelBtn").addEventListener("click", () => showDashboard());

$("addAccountBtn").addEventListener("click", () => $("setupAccounts").appendChild(accountEntry()));
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
    const editing = configMode === "settings";
    await request(editing ? "/api/v1/config" : "/api/v1/setup", {
      method: editing ? "PUT" : "POST",
      headers:{"Content-Type":"application/json"},
      body:JSON.stringify(payload)
    });
    toast(editing ? "配置已保存并热重载。" : "初始化完成，PikPak Bridge 已开始工作。");
    showDashboard();
    startDashboard();
    await Promise.all([loadHealth(), loadTargets(), loadStatus()]);
  } catch (err) {
    toast("保存失败：" + err.message, true);
  } finally {
    button.disabled = false;
  }
});

$("taskForm").addEventListener("submit", async (event) => {
  event.preventDefault();
  const input = $("sourceInput");
  const source = input.value.trim();
  const target = $("targetSelect").value;
  if (!source) { toast("请先输入下载链接或磁链。", true); return; }
  if (!target) { toast("请选择 Download Target。", true); return; }
  const button = $("submitBtn");
  button.disabled = true;
  try {
    const task = await request("/api/v1/tasks", {
      method:"POST",
      headers:{"Content-Type":"application/json"},
      body:JSON.stringify({url:source, target})
    });
    input.value = "";
    toast("任务已创建：" + task.id);
    await loadTasks();
  } catch (err) {
    if (err.status === 409 && err.body?.existing_task_id) {
      toast("该资源在此 Target 已存在：" + err.body.existing_task_id, true);
    } else {
      toast("创建失败：" + err.message, true);
    }
  } finally {
    button.disabled = false;
  }
});

$("refreshBtn").addEventListener("click", async () => {
  $("refreshBtn").textContent = "…";
  await Promise.all([loadHealth(), loadTasks(), loadStatus(), loadTargets()]);
  $("refreshBtn").textContent = "↻";
});

$("dialogClose").addEventListener("click", () => $("taskDialog").close());
$("taskDialog").addEventListener("click", (event) => {
  if (event.target === $("taskDialog")) $("taskDialog").close();
});

bootstrap();
