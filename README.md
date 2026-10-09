# pikpak-bridge

A lightweight bridge that uses a pool of PikPak accounts for offline downloads and routes completed files to explicit aria2 download targets.

## Documentation

- [Code review and prioritized improvement backlog (2026-10-08, 中文)](docs/code-review-2026-10-08.md)
- [EhAria2 + PikPak Bridge 用户脚本（安装、配置及授权）](userscripts/README.md)

## Workflow

~~~text
URL / magnet / ED2K
        ↓
choose a Download Target
        ↓
automatically select an eligible PikPak account
        ↓
PikPak offline download
        ↓
resolve completed remote files
        ↓
send files to the Target's aria2 instance + destination directory
        ↓
verify completion and file sizes
        ↓
permanently delete only the PikPak files created by this task
~~~

The two scheduling decisions are intentionally different:

- **PikPak account:** selected automatically from the account pool.
- **Download Target:** selected by the user and never silently changed.

## Core model

### PikPak account pool

All enabled PikPak accounts form one implicit pool. Each account has a stable ID and its own concurrency limit.

Eligible accounts are filtered by health, remaining offline quota, cooldown, active jobs and free storage. They are then ordered by:

1. lower effective load ratio (`active_jobs / max_jobs`)
2. more remaining offline quota
3. least recently used
4. more free storage
5. stable account ID

The worker reserves account capacity before submission so a burst of queued tasks does not all select the same stale account snapshot.

Submission recovery deliberately prioritizes ownership safety. The bridge never adopts an arbitrary existing PikPak offline task merely because its source URL matches. If the process dies in the narrow window after PikPak accepts a submission but before the returned task ID is persisted, the retry may create an extra PikPak task instead of taking ownership of an unrelated one. Persisted PikPak task IDs resume normally.

### aria2 instances

An aria2 instance represents only an RPC endpoint. aria2 itself owns active/waiting queue and concurrency control:

~~~yaml
aria2:
  instances:
    - id: unraid
      name: Unraid
      url: http://192.168.1.10:6800/jsonrpc
      secret: ...
~~~

aria2 instances are not automatically selected by a scheduler.

### Download Targets

A Download Target represents the user's final destination:

~~~yaml
targets:
  - id: movies
    name: 电影
    aria2_instance: unraid
    dir: /downloads/movies
    default: true

  - id: tv
    name: 电视剧
    aria2_instance: unraid
    dir: /downloads/tv
~~~

Multiple targets may reuse the same aria2 instance.

When a task is created, the target ID, target name, aria2 instance ID and destination directory are snapshotted onto the task. Later configuration changes therefore cannot silently move an existing task.

If the selected aria2 instance is offline, the task stays in `WAITING_ARIA2`. Once it is healthy, the files are submitted and aria2 handles its own active/waiting queue. The bridge does **not** fail over to another target.

## Current status

The core pipeline includes:

- multi-account PikPak pool scheduling
- per-account concurrency limits and cooldowns
- durable offline-task state and restart recovery
- asynchronous task cancellation and durable remote cleanup retry
- explicit Download Targets
- multi-instance aria2 registry
- persistent per-file aria2 progress and deterministic GID recovery
- final size verification and precise PikPak cleanup
- embedded Web UI
- runtime configuration management with hot reload
- Docker/Compose deployment and multi-architecture GHCR images

## Development status

The project is still under active development. Configuration and SQLite schemas may change without migration support.

When testing a breaking development build, remove the old configuration and database before starting the new build:

~~~bash
rm -f ./data/config.yaml ./data/pikpak-bridge.db ./data/pikpak-bridge.db-shm ./data/pikpak-bridge.db-wal
~~~

PikPak session files under `./data/sessions` are separate.

## Docker image

~~~text
ghcr.io/nukewarrior/pikpak-bridge:latest
~~~

Supported platforms:

- linux/amd64
- linux/arm64

### First run

~~~bash
docker compose pull
docker compose up -d
~~~

Open:

~~~text
http://<host>:8080/
~~~

The first-run Web UI configures:

- one or more PikPak accounts
- one or more aria2 JSON-RPC instances
- one or more Download Targets

The configuration is saved to:

~~~text
/data/config.yaml
~~~

### Runtime settings

After setup, use the **Settings** button in the Web UI to add, edit, disable or remove:

- PikPak accounts
- aria2 instances
- Download Targets

Saving settings atomically rewrites `/data/config.yaml` and hot-reloads the runtime. The container does not need to restart.

Stored PikPak passwords and aria2 RPC secrets are never returned by the configuration API. Existing credentials remain unchanged when their password/secret fields are left blank while editing.

A PikPak account or aria2 instance that is still referenced by an active task cannot be removed or disabled. Existing tasks keep their snapshotted Target route, while connection details for a stable aria2 instance ID can be updated in place.

## Test a development image

The Docker workflow supports isolated manual tags for development builds without updating `latest`.

In GitHub Actions, open **Docker → Run workflow**, select the development branch, and set an explicit tag such as:

~~~text
pr-12
~~~

Then deploy that image with the same Compose file. Image, host port and data directory are overrideable, so a development build can run beside the stable instance without touching its database or configuration:

~~~bash
export PIKPAK_BRIDGE_IMAGE=ghcr.io/nukewarrior/pikpak-bridge:pr-12
export PIKPAK_BRIDGE_PORT=8081
export PIKPAK_BRIDGE_DATA=./data-pr12

docker compose -p pikpak-bridge-pr12 pull
docker compose -p pikpak-bridge-pr12 up -d
~~~

Open `http://<host>:8081/` and initialize the development instance independently.

Only pushes to `master` publish `latest`. Manual builds can still publish an explicit tag plus the commit `sha-xxxxxxx` tag. The fixed Compose `container_name` is intentionally omitted so multiple project names can coexist.

## API

Create a task for a target:

~~~bash
curl -X POST http://localhost:8080/api/v1/tasks \
  -H 'Content-Type: application/json' \
  -d '{"url":"magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567","target":"movies"}'
~~~

If a default target exists, `target` may be omitted.

Runtime configuration:

~~~text
GET /api/v1/config
PUT /api/v1/config
~~~

`GET /api/v1/config` returns editable resource metadata without password/secret values. `PUT /api/v1/config` validates the complete Accounts / Instances / Targets set before applying it atomically.

List targets:

~~~text
GET /api/v1/targets
~~~

Query tasks:

~~~text
GET /api/v1/tasks
GET /api/v1/tasks/{id}
GET /api/v1/tasks/{id}/downloads
GET /healthz
~~~

A normalized source is globally unique while its task exists. This preserves the invariant that one bridge task owns one PikPak offline task/root and is the only task allowed to clean it up.

## Cancellation recovery

Cancel requests enter `CANCELLING` and return HTTP 202. The cancellation worker stops aria2 downloads, cancels the exact recorded PikPak offline task, and deletes the recorded root. Only after successful cleanup does the task become `CANCELLED`. Failures remain in `CANCELLING` and automatically retry with capped exponential backoff across restarts.

An in-flight PikPak submission is recorded before ordinary state transitions resume. If PikPak accepted a submission but its task ID was lost during a crash or uncertain network failure, cancellation remains blocked with an explicit warning rather than incorrectly reporting successful cleanup; manual reconciliation may be needed.

## Safety

Automatic cleanup only deletes the exact PikPak root/file IDs recorded for a bridge task.

PikPak deletion runs only after aria2 reports completion and configured verification succeeds.


## 历史任务、云端缓存保留与重新下载

- 任务经 aria2 和尺寸校验成功后，PikPak 云端根文件暂时保留，不再按固定延迟自动删除。独立的 `pikpak_cache_entries` 数据表保存源链接、账号和根文件引用，因此即使历史记录被删除，仍可以安全回收。
- 当某 PikPak 账号剩余空间低于 `pikpak.min_free_space`（默认 2GB），或 PikPak 明确报空间不足时，单独的回收器仅删除**该账号**最早完成并校验成功、且没有运行中任务引用的根文件；每次删除后重新查询空间，达到阈值即停止。离线下载次数耗尽不会触发空间清理。
- `cleanup.enabled: false` 可禁用自动空间回收；`cleanup.permanent: true` 代表空间回收会永久删除确切属于 Bridge 的根文件。旧版的 `cleanup.delay` 保留以兼容已有配置，但不再决定下载完成后立即清理的时间。
- 历史记录可删除、失败任务可重试，完成/取消任务可重新下载。删除历史仅移除 Bridge 记录，不删除 NAS 文件；仍有未清理远端资源的失败任务先转为可恢复清理流程，清理成功后自动删除历史。
- 相同磁力的正常提交仍返回 HTTP 409，并告知旧任务状态；只有明确携带 `force: true` 的请求才能在没有同源活动任务时创建新的下载尝试。新尝试优先复用同源已保留的 PikPak 云端缓存，缓存失效时重新走离线下载。
- **安全覆盖重下需要 NAS 挂载。** 下载目标的 `local_dir` 必须是 Bridge 容器中可访问的路径，与该目标的 aria2 `dir` 指向同一 NAS 文件目录。示例：aria2 的 `dir=/downloads/movies`，而 Bridge 的 `local_dir=/nas/downloads/movies`，容器需挂载 `/mnt/user/downloads/movies:/nas/downloads/movies`。下载首先写入 `dir/.pikpak-bridge-staging/{taskID}`；校验所有暂存文件后，逐个备份旧文件并重命名替换。多文件不是一个跨文件原子操作，但会用持久化恢复日志在中断时恢复未提交的替换，直到确认完成后才清理备份。
- 未设置本地挂载时，正常首次下载不受影响，但**明确重下会被拒绝**，避免不经校验就覆盖 NAS 旧文件。
- E-Hentai 用户脚本的 PikPak 按钮会在重复已完成种子时提示，确认后创建新的下载任务。原始直接 aria2 操作仍使用其原有流程，**不具备 Bridge 提供的暂存后替换保证**。对 NAS 文件安全有要求时，重复下载请选择 PikPak Bridge。

请先备份 `/data/pikpak-bridge.db` 与 `/data/config.yaml` 再升级。历史记录里原本已永久清理的云端文件无法恢复，也不会被误记为留存缓存。
