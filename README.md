# pikpak-bridge

A lightweight scheduler that bridges PikPak offline downloads to one or more aria2 instances.

## Goal

The target workflow is:

~~~text
URL / magnet / ED2K
        ↓
choose an eligible PikPak account
        ↓
PikPak offline download
        ↓
resolve completed remote files
        ↓
choose an aria2 instance
        ↓
aria2 downloads the files
        ↓
verify completion and file sizes
        ↓
permanently delete only the PikPak files created by this task
~~~

The design is intentionally built around multi-account PikPak quota scheduling, multi-instance aria2 scheduling, durable SQLite task state, restart recovery, and conservative cleanup.

## Current status

The core v1 pipeline is implemented:

- multi-account PikPak scheduling with quota/storage checks
- durable offline-task state machine and restart recovery
- multi-instance aria2 scheduling with task affinity
- persistent per-file aria2 progress and deterministic GID recovery
- final size verification and precise PikPak cleanup
- embedded Web UI
- Docker/Compose deployment and multi-architecture GHCR images

## Docker image

Prebuilt multi-architecture images are published to GitHub Container Registry:

~~~text
ghcr.io/nukewarrior/pikpak-bridge:latest
~~~

Supported platforms:

- linux/amd64
- linux/arm64

### First run

No config file or environment variables are required for the initial setup.

~~~bash
docker compose pull
docker compose up -d
~~~

Then open:

~~~text
http://<host>:8080/
~~~

The first-run Web UI asks for:

- one or more PikPak accounts
- one or more aria2 JSON-RPC endpoints
- each aria2 download directory, concurrency and scheduler weight

After saving, PikPak Bridge writes the complete configuration to:

~~~text
/data/config.yaml
~~~

The file is created with mode `0600` inside the persistent `./data:/data` volume, and workers start immediately without restarting the container. Subsequent restarts load this saved configuration and go directly to the dashboard.

To reset the first-run setup, stop the container and remove `./data/config.yaml`. The SQLite database and sessions are separate files under `./data`; removing only `config.yaml` does not erase task history.

Each master build publishes both `latest` and a commit-pinned `sha-xxxxxxx` image tag. Git tags such as `v0.1.0` are also published with the same image tag.

## Web UI

The Web UI is embedded into the Go binary. No Node.js build or separate frontend container is required.

The dashboard provides:

- first-run setup for PikPak accounts and aria2 instances
- task submission for Magnet / HTTP(S) / ED2K / BTIH
- live task states and aria2 download progress
- task details including PikPak IDs and aria2 GIDs
- PikPak account health, remaining offline quota, active jobs, and free storage
- aria2 instance health, active/waiting jobs, capacity, and scheduler weight

The task list refreshes automatically. Runtime account/node status refreshes less frequently to avoid unnecessary remote API traffic.

## API

Create a task:

~~~bash
curl -X POST http://localhost:8080/api/v1/tasks   -H 'Content-Type: application/json'   -d '{"url":"magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567"}'
~~~

Plain-text submission is also supported:

~~~bash
curl -X POST --data 'magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567'   http://localhost:8080/api/v1/tasks/text
~~~

Query tasks:

~~~text
GET /api/v1/tasks
GET /api/v1/tasks/{id}
GET /healthz
~~~

Duplicate BTIH values return HTTP 409 with the existing task ID.

## Configuration

~~~bash
cp config.example.yaml config.yaml
~~~

Secrets can be injected with environment variables rather than committed to YAML.

See `config.example.yaml`.

## Scheduling rules

PikPak accounts are filtered by enabled/healthy state, remaining offline quota, cooldown, per-account active-job limit, and known free space. Eligible accounts are ordered by:

1. more remaining quota
2. fewer active offline jobs
3. least recently used
4. stable account name tie-break

aria2 instances are filtered by enabled/healthy state and capacity. The current score is:

~~~text
((active + 0.5 * waiting) / max_active) / weight
~~~

The lowest score wins. Task-level aria2 affinity is the default so one task's files stay on the same destination.

## Safety

Automatic cleanup must only ever delete the exact PikPak root/file IDs recorded for a bridge task. The project will not implement "clean account" or "delete everything" behavior.

PikPak deletion will only run after aria2 reports completion and configured verification succeeds.
