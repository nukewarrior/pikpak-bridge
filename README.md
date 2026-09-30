# pikpak-bridge

A lightweight bridge that uses a pool of PikPak accounts for offline downloads and routes completed files to explicit aria2 download targets.

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
- explicit Download Targets
- multi-instance aria2 registry
- persistent per-file aria2 progress and deterministic GID recovery
- final size verification and precise PikPak cleanup
- embedded Web UI
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

During the current development cycle, pushes to `master` and `refactor/download-targets` publish `latest`, so the branch can be tested with the normal Compose workflow. Manual builds can still publish an explicit tag plus the commit `sha-xxxxxxx` tag. The fixed Compose `container_name` is intentionally omitted so multiple project names can coexist.

## API

Create a task for a target:

~~~bash
curl -X POST http://localhost:8080/api/v1/tasks \
  -H 'Content-Type: application/json' \
  -d '{"url":"magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567","target":"movies"}'
~~~

If a default target exists, `target` may be omitted.

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

## Safety

Automatic cleanup only deletes the exact PikPak root/file IDs recorded for a bridge task.

PikPak deletion runs only after aria2 reports completion and configured verification succeeds.
