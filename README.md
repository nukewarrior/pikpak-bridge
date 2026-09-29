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

This repository is in active development.

The first foundation includes:

- Go service layout
- YAML configuration with environment-variable expansion
- SQLite task persistence
- task submission/query HTTP API
- magnet/BTIH/HTTP/HTTPS/ED2K source normalization and duplicate detection
- PikPak provider interface
- aria2 JSON-RPC client
- pure/testable PikPak account selector
- pure/testable aria2 instance selector
- Docker and CI scaffolding

The PikPak private-API adapter and background state-machine workers are the next implementation stage.

## Web UI

The Web UI is embedded into the Go binary. No Node.js build or separate frontend container is required.

After starting the service, open:

~~~text
http://localhost:8080/
~~~

The dashboard provides:

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
