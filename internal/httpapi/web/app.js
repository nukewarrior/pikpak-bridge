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
let dashboardStarted = false;
let toastTimer;
let accountSeq = 0;
let aria2Seq = 0;

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
        <label>名称</label>
        <input data-field="name" value="${esc(values.name || "pp" + String(index).padStart(2,"0"))}" required>
      </div>
      <div class="field">
        <label>账号</label>
        <input data-field="username" value="${esc(values.username || "")}" autocomplete="username" placeholder="邮箱或手机号" required>
      </div>
      <div class="field wide">
        <label>密码</label>
        <input data-field="password" type="password" autocomplete="new-password" placeholder="PikPak 登录密码" required>
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
  const div = document.createElement("div");
  div.className = "setup-entry";
  div.dataset.kind = "aria2";
  div.innerHTML = `
    <div class="setup-entry-head">
      <strong>aria2 节点 #${index}</strong>
      <button class="remove-btn" type="button">移除</button>
    </div>
    <div class="setup-grid">
      <div class="field">
        <label>名称</label>
        <input data-field="name" value="${esc(values.name || "aria2-" + String(index).padStart(2,"0"))}" required>
      </div>
      <div class="field">
        <label>RPC Secret</label>
        <input data-field="secret" type="password" autocomplete="new-password" value="${esc(values.secret || "")}" placeholder="未设置可留空">
      </div>
      <div class="field wide">
        <label>JSON-RPC 地址</label>
        <input data-field="url" value="${esc(values.url || "")}" placeholder="http://192.168.1.10:6800/jsonrpc" required>
      </div>
      <div class="field wide">
        <label>下载目录</label>
        <input data-field="dir" value="${esc(values.dir || "/downloads/pikpak")}" placeholder="/downloads/pikpak" required>
        <span class="hint">填写 aria2 所在机器看到的目录，不是 PikPak Bridge 容器内部目录。</span>
      </div>
      <div class="field">
        <label>最大活动任务</label>
        <input data-field="max_active" type="number" min="1" max="100" value="${esc(values.max_active || 4)}" required>
      </div>
      <div class="field">
        <label>调度权重</label>
        <input data-field="weight" type="number" min="0.1" step="0.1" value="${esc(values.weight || 10)}" required>
      </div>
    </div>`;
  div.querySelector(".remove-btn").addEventListener("click", () => {
    if ($("setupAria2").children.length <= 1) {
      toast("至少保留一个 aria2 节点。", true);
      return;
    }
    div.remove();
  });
  return div;
}

function showSetup() {
  $("dashboard").classList.add("hidden");
  $("setupScreen").classList.remove("hidden");
  if (!$("setupAccounts").children.length) $("setupAccounts").appendChild(accountEntry());
  if (!$("setupAria2").children.length) $("setupAria2").appendChild(aria2Entry({name:"unraid"}));
}

function showDashboard() {
  $("setupScreen").classList.add("hidden");
  $("dashboard").classList.remove("hidden");
}

function collectSetup() {
  const accounts = [...$("setupAccounts").querySelectorAll(".setup-entry")].map(row => ({
    name: row.querySelector('[data-field="name"]').value.trim(),
    username: row.querySelector('[data-field="username"]').value.trim(),
    password: row.querySelector('[data-field="password"]').value
  }));
  const instances = [...$("setupAria2").querySelectorAll(".setup-entry")].map(row => ({
    name: row.querySelector('[data-field="name"]').value.trim(),
    url: row.querySelector('[data-field="url"]').value.trim(),
    secret: row.querySelector('[data-field="secret"]').value,
    dir: row.querySelector('[data-field="dir"]').value.trim(),
    max_active: Number(row.querySelector('[data-field="max_active"]').value),
    weight: Number(row.querySelector('[data-field="weight"]').value)
  }));
  return {pikpak_accounts: accounts, aria2_instances: instances};
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
  loadTasks();
  loadStatus();
  setInterval(loadHealth, 15000);
  setInterval(loadTasks, 3000);
  setInterval(loadStatus, 60000);
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
      return `<tr data-task-id="${esc(task.id)}">
        <td>${statusBadge(task.status)}</td>
        <td class="source-cell"><div class="source-main" title="${esc(task.source)}">${esc(truncate(task.source))}</div><div class="source-sub">${esc(task.source_type || "")} · ${esc(task.id)}</div></td>
        <td>${progressHtml}</td>
        <td>${esc(task.pikpak_account || "—")}</td>
        <td>${esc(task.aria2_instance || "—")}</td>
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
        <div class="resource-top"><span class="resource-name">${esc(a.name)}</span>${resourceBadge(a.enabled && a.healthy, a.state)}</div>
        <div class="resource-meta"><span>离线额度 <b>${a.quota_remaining ?? 0}/${a.quota_total ?? 0}</b></span><span>任务 ${a.active_jobs ?? 0}/${a.max_jobs ?? 0}</span></div>
        <div class="resource-meta"><span>可用空间</span><span>${fmtBytes(a.storage_free)}</span></div>
        ${a.error ? `<div class="resource-error">${esc(a.error)}</div>` : ""}
      </div>
    `).join("") : '<div class="empty-block">未配置 PikPak 账号</div>';

    $("aria2List").innerHTML = instances.length ? instances.map(a => `
      <div class="resource">
        <div class="resource-top"><span class="resource-name">${esc(a.name)}</span>${resourceBadge(a.enabled && a.healthy, a.enabled ? "OFFLINE" : "DISABLED")}</div>
        <div class="resource-meta"><span>Active <b>${a.active ?? 0}/${a.max_active ?? 0}</b></span><span>Waiting ${a.waiting ?? 0}</span></div>
        <div class="resource-meta"><span>调度权重</span><span>${a.weight ?? 1}</span></div>
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
        <dt>PikPak 账号</dt><dd>${esc(task.pikpak_account || "—")}</dd>
        <dt>PikPak Task ID</dt><dd>${esc(task.pikpak_task_id || "—")}</dd>
        <dt>PikPak Root ID</dt><dd>${esc(task.pikpak_root_file_id || "—")}</dd>
        <dt>aria2 实例</dt><dd>${esc(task.aria2_instance || "—")}</dd>
        <dt>重试次数</dt><dd>${task.retry_count ?? 0}</dd>
        <dt>创建时间</dt><dd>${fmtTime(task.created_at)}</dd>
        <dt>更新时间</dt><dd>${fmtTime(task.updated_at)}</dd>
        <dt>错误</dt><dd>${esc(task.error || "—")}</dd>
      </dl>
      <div class="detail-section"><h3>下载文件（${downloads.length}）</h3>
        ${downloads.length ? downloads.map(d => `
          <div class="download-row">
            <div><div class="download-name">${esc(d.relative_path)}</div><div class="download-meta">GID ${esc(d.aria2_gid || "—")} · ${esc(d.aria2_instance || "—")}</div></div>
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

$("addAccountBtn").addEventListener("click", () => $("setupAccounts").appendChild(accountEntry()));
$("addAria2Btn").addEventListener("click", () => $("setupAria2").appendChild(aria2Entry()));

$("setupForm").addEventListener("submit", async (event) => {
  event.preventDefault();
  const button = $("setupSubmitBtn");
  button.disabled = true;
  try {
    const payload = collectSetup();
    await request("/api/v1/setup", {
      method:"POST",
      headers:{"Content-Type":"application/json"},
      body:JSON.stringify(payload)
    });
    toast("初始化完成，PikPak Bridge 已开始工作。");
    showDashboard();
    startDashboard();
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
  if (!source) { toast("请先输入下载链接或磁链。", true); return; }
  const button = $("submitBtn");
  button.disabled = true;
  try {
    const task = await request("/api/v1/tasks", {
      method:"POST",
      headers:{"Content-Type":"application/json"},
      body:JSON.stringify({url:source})
    });
    input.value = "";
    toast("任务已创建：" + task.id);
    await loadTasks();
  } catch (err) {
    if (err.status === 409 && err.body?.existing_task_id) {
      toast("任务已存在：" + err.body.existing_task_id, true);
    } else {
      toast("创建失败：" + err.message, true);
    }
  } finally {
    button.disabled = false;
  }
});

$("refreshBtn").addEventListener("click", async () => {
  $("refreshBtn").textContent = "…";
  await Promise.all([loadHealth(), loadTasks(), loadStatus()]);
  $("refreshBtn").textContent = "↻";
});

$("dialogClose").addEventListener("click", () => $("taskDialog").close());
$("taskDialog").addEventListener("click", (event) => {
  if (event.target === $("taskDialog")) $("taskDialog").close();
});

bootstrap();
