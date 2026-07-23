# 🐙 OctoConv

**An async, production-hardened file conversion service written in Go.**

A client uploads a file through a REST API; OctoConv stores it in S3-compatible object
storage, queues a conversion job, and a worker runs an external conversion engine — images
via [libvips](https://www.libvips.org/), office documents via headless
[LibreOffice](https://www.libreoffice.org/), HTML via headless Chromium, audio transcription
via whisper.cpp, or video via ffmpeg — and writes the result back to storage. PostgreSQL is
the system of record for job state; Redis ([asynq](https://github.com/hibiken/asynq)) is the
queue broker.

Built to be the kind of internal conversion service you can actually depend on: mandatory
API-key auth, rate limiting, signed webhook delivery with retries, automatic recovery of
stranded jobs, content validation that doesn't trust file extensions, and Prometheus
metrics — not just a vertical slice that works on the happy path.

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go)](go.mod)

---

## Why OctoConv

Most "convert my file" services are either a thin wrapper around a single CLI tool with no
safety net, or a heavyweight SaaS you don't control. OctoConv sits in between: a small,
auditable Go service you can self-host, designed from the start around the failure modes
that actually bite production systems —

- **A worker crashes mid-conversion.** A reconciler sweeps Postgres for stranded jobs and
  recovers them idempotently, with a bounded retry cap so a permanently broken job doesn't
  loop forever.
- **A webhook delivery silently drops** (Redis blip, process restart between `MarkDone` and
  enqueue). The reconciler catches that gap too, and delivers exactly once.
- **A client lies about file type.** Every upload is sniffed by magic bytes / container
  structure — never trusted by extension — before it's written to storage or handed to an
  engine.
- **A "PNG" is actually a 65535×65535 decompression bomb.** Declared dimensions (or, for
  office documents, declared uncompressed ZIP size) are checked against a configurable
  ceiling before any decoding happens.
- **An external engine hangs or gets killed mid-run.** Every engine invocation goes through
  a hardened process wrapper that puts the child in its own process group and SIGKILLs the
  whole group on timeout — verified against a real forking process tree (LibreOffice's
  `oosplash` → `soffice.bin`), not assumed.
- **You need to know when something's wrong**, not just discover it from an angry Slack
  message. Structured job events, Prometheus metrics, and a `/healthz` that actually pings
  its dependencies.

## Features

**Core pipeline**
- Multipart file upload → S3/MinIO storage → async conversion via a queue → presigned
  download URL, or push delivery via signed webhook
- Engine-class queue routing: each conversion engine (image, document, html, audio, av) gets
  its own asynq queue so worker pools scale independently
- Postgres-backed job lifecycle (`queued → active → done/failed`) with an append-only event
  log for every state transition
- Named conversion presets (create/list/show/update/deactivate) plus a registry-derived
  `GET /v1/formats` capability-discovery endpoint
- Kubernetes deployment via a Helm chart with KEDA queue-depth autoscaling, scaling each
  engine-class worker independently

**Security & reliability**
- Mandatory API-key authentication (salted SHA-256 hashes, zero-downtime key rotation via
  dual active key slots), enforced on every `/v1/*` route
- Two-tier rate limiting: coarse pre-auth IP flood guard + per-client fair-use limit
- HMAC-SHA256 signed webhook payloads, bounded exponential-backoff retry with jitter, and
  dead-lettering after retries are exhausted
- SSRF-hardened `callback_url` validation (loopback / RFC1918 / link-local / cloud metadata
  endpoints blocked by default; opt-in RFC1918 allowance for internal-network deployments —
  loopback and link-local always stay blocked, no override)
- A reconciler that recovers both stranded conversion jobs *and* silently-dropped webhook
  deliveries, with a proven idempotent enqueue-first + duplicate-guard pattern (verified
  under real wall-clock conditions, not a mocked clock)
- Content-format validation independent of file extension: magic-byte sniffing for images,
  structural ZIP/OOXML/ODF container inspection for office documents, plus zip-bomb and
  embedded-macro rejection
- Decompression-bomb protection via declared-dimension / declared-uncompressed-size limits,
  enforced before any decoding
- OLE-CFB classification distinguishing encrypted vs. legacy-binary Office uploads before
  conversion, and PDF/A (ISO 19005) export validated by a bundled veraPDF check

**Observability**
- Prometheus metrics (job outcomes, duration, webhook delivery, reconciler actions, queue
  depth) on a dedicated localhost-only port
- Real `/healthz` that pings Postgres, Redis, and S3 — not a static 200
- [asynqmon](https://github.com/hibiken/asynqmon) dashboard for live queue inspection
- Automatic TTL-based cleanup of uploaded/converted files (MinIO ILM lifecycle rules)

**Conversion engines**
- ✅ **Images** (`png`, `jpg`, `webp`, `heic`, `tiff`, any-to-any) via libvips
- ✅ **Office documents** (`docx`, `xlsx`, `pptx`, `odt`, `ods`, `odp` → `pdf`, plus
  `docx`↔`odt`, `xlsx`↔`ods`, `pptx`↔`odp` cross-format pairs) via LibreOffice headless
- ✅ **HTML** (`html` → `pdf`) via headless Chromium
- ✅ **Audio transcription** (`mp3`, `wav`, `m4a`, `ogg`, and video containers `mp4`, `mov`,
  `avi`, `mkv`, `webm` → `txt`, `srt`, `vtt`, `json`) via whisper.cpp
- ✅ **Video** (`mov`/`avi`/`mkv`/`webm` → `mp4`; `mp4` → `webm`; audio-extract to
  `mp3`/`wav`/`m4a`; thumbnail to `jpg`/`png`/`webp`) via ffmpeg

## Architecture

```
                 multipart upload                 asynq (Redis)
Client ────────────────────────▶ API ───────────────────────────▶ Worker
                                  │                                  │
                                  ▼                                  ▼
                            PostgreSQL                    external engine (libvips /
                        (jobs, job_events,                 LibreOffice / Chromium /
                         webhook_deliveries)                whisper.cpp / ffmpeg) via a
                                  │                          hardened process wrapper
                                  │                                  │
                                  │                                  ▼
                                  │                          S3 / MinIO storage
                                  ▼
                          presigned download URL
                          or signed webhook push
```

```
cmd/
  api/                — HTTP server: upload, status, presigned downloads
  worker/             — asynq consumer: image conversion (libvips)
  document-worker/    — asynq consumer: office document conversion (LibreOffice)
  chromium-worker/    — asynq consumer: HTML → PDF conversion (headless Chromium)
  audio-worker/       — asynq consumer: audio transcription (whisper.cpp)
  av-worker/          — asynq consumer: video transcode/extract/thumbnail (ffmpeg)
  webhook-worker/     — asynq consumer: signed webhook delivery + reconciler sweep
  mcp-server/         — stdio MCP server exposing OctoConv to agents
  mcp-http/           — streamable-HTTP MCP server for in-cluster deployment
  manage-clients/     — operator CLI: issue/rotate/revoke API keys
  manage-presets/     — operator CLI: create/update/list/show/deactivate presets
  migrate/            — apply embedded SQL migrations
internal/
  api/                — routes, handlers, auth middleware, rate limiting, SSRF guard
  convert/            — Converter interface + registry, hardened exec, engine implementations
  jobs/               — Postgres-backed job repository with guarded state transitions
  queue/              — asynq task/queue definitions, retry schedules
  reconciler/         — stranded-job and webhook-gap recovery sweep
  webhook/            — HMAC signing, delivery, dead-letter tracking
  storage/            — S3/MinIO client, deterministic object-key layout
  metrics/            — Prometheus metric definitions
  presets/            — named-preset resolution (scope precedence, versioning)
  mcpserver/          — MCP tool implementations (convert_file, get_job_status, …)
  auth/, clients/     — API-key hashing and client repository
  db/                 — connection pool + embedded migration runner
```

## Quick Start

**Requirements:** Go 1.26+, Docker + Docker Compose.

### 1. Start the infrastructure

```bash
cp .env.example .env        # adjust ports/credentials if needed
docker compose up -d
```

| Service  | Image             | Host port                          | Notes                                        |
|----------|-------------------|-------------------------------------|-----------------------------------------------|
| postgres | `postgres:18`     | `5434`                              | `octo / octo-pass / octo_db`                  |
| redis    | `redis:8`         | `6379`                              | asynq broker                                  |
| minio    | `minio/minio`     | `9100` (API), `9101` (console)     | `minioadmin / minioadmin`, bucket auto-created |
| api      | `Dockerfile.api`  | `8090`                              | HTTP API                                      |
| worker   | `Dockerfile.worker`| —                                   | image engine worker, runs as `nobody`, CPU/RAM limited |
| document-worker, chromium-worker, audio-worker, av-worker | per-engine `Dockerfile.*` | — | one worker per remaining engine class (document/html/audio/video), each resource-limited |
| webhook-worker-1, webhook-worker-2 | `Dockerfile.webhook-worker` | —             | two replicas — sole webhook-delivery consumer + reconciler sweeper |
| asynqmon | `hibiken/asynqmon:0.7.2` | `127.0.0.1:8980`               | read-only asynq queue-inspection dashboard    |

> Each conversion engine class runs as its own dedicated worker service, so worker pools
> scale independently per class.

> **Presigned URLs and Docker networking:** the containerized `api` presigns URLs against
> the internal `minio:9000` endpoint, which isn't reachable from your host. To download
> results from your host machine, run `api` locally (step 3 below) so links point at
> `localhost:9100` instead.

### 2. Run migrations

```bash
set -a && . ./.env && set +a
go run ./cmd/migrate
```

### 3. Start the services

```bash
docker compose up -d --build worker     # containerized worker (needs libvips)
set -a && . ./.env && set +a
go run ./cmd/api                        # HTTP API on :8090
```

Or fully containerized (subject to the presigned-URL caveat above):

```bash
docker compose up -d --build
```

### 4. Issue an API key

```bash
go run ./cmd/manage-clients create "my-service"
# client id: <uuid>
# api key (save now, shown once): <raw-key>
```

Keys are printed exactly once and never stored or logged in plaintext (only a salted
SHA-256 hash lives in Postgres). Rotate without downtime via a second active key slot:

```bash
go run ./cmd/manage-clients add-key <client-id>      # second active key
go run ./cmd/manage-clients revoke <client-id> <primary|secondary>
```

### 5. Convert something

```bash
curl -H "Authorization: ApiKey <raw-key>" \
  -F file=@photo.png -F target=webp http://localhost:8090/v1/jobs
# {"job_id":"...","status":"queued"}

curl -H "Authorization: ApiKey <raw-key>" http://localhost:8090/v1/jobs/<job_id>
# {"job_id":"...","status":"done","download_url":"..."}
```

Optionally, skip polling and get a signed webhook push on completion by adding
`-F callback_url=https://your-service/hooks/octoconv` to the create request.

## API Semantics

- No key, or an invalid/revoked key → `401`
- A job that belongs to a different client, or doesn't exist → `404` (never `403` — the API
  doesn't confirm existence of jobs you don't own)
- Over the rate limit → `429` with a `Retry-After` header
- Unsupported format pair, unrecognized/mismatched content, oversized declared dimensions,
  embedded macros, or a zip-bomb-shaped upload → `422`, rejected before anything touches
  storage

## Configuration

All configuration is environment-variable based — see [`.env.example`](.env.example) for
the full, documented list. Highlights:

| Variable | Purpose |
|----------|---------|
| `DATABASE_URL`, `REDIS_ADDR`, `S3_*` | infrastructure connection strings |
| `API_KEY_SALT` | server-side pepper for API-key hashing (required) |
| `MAX_UPLOAD_BYTES` | upload size ceiling |
| `MAX_IMAGE_PIXELS` | decompression-bomb guard for images (default 100 MP) |
| `MAX_DOCUMENT_UNCOMPRESSED_BYTES` | zip-bomb guard for office documents (default 500 MiB) |
| `WORKER_CONCURRENCY` / `ENGINE_TIMEOUT` / `IMAGE_MAX_RETRY` | image engine concurrency, timeout, and retry budget |
| `DOCUMENT_WORKER_CONCURRENCY` / `DOCUMENT_ENGINE_TIMEOUT` / `DOCUMENT_MAX_RETRY` / `VERAPDF_TIMEOUT` | document (LibreOffice) engine concurrency, timeout, retry budget, and veraPDF validation timeout |
| `HTML_WORKER_CONCURRENCY` / `HTML_ENGINE_TIMEOUT` / `HTML_MAX_RETRY` | HTML (Chromium) engine concurrency, timeout, and retry budget |
| `AUDIO_ENGINE_TIMEOUT` / `AUDIO_MAX_RETRY` / `AUDIO_WORKER_CONCURRENCY` / `AUDIO_MAX_DURATION_SECONDS` / `AUDIO_MODEL_PATH` / `AUDIO_THREADS` | audio (whisper.cpp) engine timeout, retry budget, concurrency, max input duration, model path, and thread count |
| `AV_ENGINE_TIMEOUT` / `AV_MAX_RETRY` / `AV_WORKER_CONCURRENCY` / `AV_MAX_DURATION_SECONDS` / `AV_DISK_SAFETY_FACTOR` | video (ffmpeg) engine timeout, retry budget, concurrency, max input duration, and disk-space safety multiplier |
| `OPERATOR_CLIENT_IDS` | comma-separated client UUIDs authorized for `/v1/system/presets` (empty = no operators, fail-closed) |
| `RATE_LIMIT_IP_RPM` / `RATE_LIMIT_CLIENT_RPM` | pre-auth and per-client rate limits |
| `WEBHOOK_SIGNING_SECRET` | HMAC-SHA256 secret for signed webhook callbacks (required) |
| `WEBHOOK_ALLOW_PRIVATE_IPS` | opt-in: allow `callback_url` to target RFC1918 addresses (loopback/link-local always blocked) |
| `WEBHOOK_WORKER_CONCURRENCY` / `WEBHOOK_PRESIGN_TTL` / `WEBHOOK_ALLOW_INSECURE_HTTP` | webhook-worker concurrency, presigned-URL TTL per delivery attempt, and opt-in non-https callback support |
| `RECONCILER_*` | staleness thresholds, sweep interval, recovery cap |
| `METRICS_ADDR` | localhost-only Prometheus `/metrics` listener |

## Frontend

A minimal reference UI lives in [`frontend/`](frontend/) (React + Vite + TypeScript): enter
an API key, pick a file and target format, submit, and poll until the download link is
ready.

```bash
cd frontend
npm install
npm run dev   # http://localhost:5173, proxies /v1 and /healthz to :8090
```

## MCP server

`cmd/mcp-server` exposes OctoConv to agents (Claude Code, Claude Desktop, or any MCP
client) as a stdio [Model Context Protocol](https://modelcontextprotocol.io) server. It is a
zero-privilege pure HTTP client of the public API — it holds only a client API key and
filesystem access to its own output directory, never a database, S3, or Redis credential.

It exposes five tools:

| Tool | Purpose |
|------|---------|
| `convert_file` | Upload a local file, block until the conversion finishes, download the result |
| `get_job_status` | Check a job's status without blocking |
| `download_result` | Download a `done` job's result on demand |
| `list_supported_formats` | List the (source, target) format pairs each engine supports |
| `list_presets` | List named conversion presets available to the client |

Build it like any other binary:

```bash
go build -o octoconv-mcp ./cmd/mcp-server
```

Configuration is environment-variable based:

| Variable | Purpose |
|----------|---------|
| `OCTOCONV_BASE_URL` | base URL of a running OctoConv API (required) |
| `OCTOCONV_API_KEY` | client API key, as issued by `manage-clients` (required) |
| `OCTOCONV_OUTPUT_DIR` | where downloaded results are written (default `$TMPDIR/octoconv-mcp`) |
| `OCTOCONV_CONVERT_TIMEOUT` | `convert_file`'s overall blocking deadline (default `10m`) |
| `OCTOCONV_POLL_INTERVAL` | how often `convert_file` polls job status (default `1s`) |
| `OCTOCONV_S3_DIAL_ADDR` | operator-only: redial a different host:port for presigned downloads (e.g. when running the binary on the Docker host against a compose-internal MinIO); normally left empty |

Point a client at the built binary. For Claude Code / Claude Desktop, add an entry to the
MCP servers config:

```json
{
  "mcpServers": {
    "octoconv": {
      "command": "/path/to/octoconv-mcp",
      "args": [],
      "env": {
        "OCTOCONV_BASE_URL": "http://localhost:8090",
        "OCTOCONV_API_KEY": "<raw-key-from-manage-clients>"
      }
    }
  }
}
```

## MCP over HTTP (in-cluster)

`cmd/mcp-http` exposes the same five tools as `cmd/mcp-server`, but over
streamable HTTP instead of stdio, for deployment as a cluster service
(`deploy/chart/octoconv/templates/deployment-mcp-http.yaml`, gated by
`mcpHttp.enabled`). Key differences from the stdio binary:

- **Transport**: [streamable HTTP](https://modelcontextprotocol.io) on `MCP_HTTP_ADDR`
  (default `:8070`), via the go-sdk's `mcp.NewStreamableHTTPHandler`.
- **Auth is per-request, not per-process**: the pod holds no API key of its own
  (zero-privilege, D-06). Every request must carry
  `Authorization: ApiKey <key>`; the handler parses it once, rejects
  missing/malformed headers with `401` before any MCP JSON-RPC code runs, and
  builds an isolated per-request client bound to that caller's own key. A
  session-key binding rejects (`403`) any request whose `Mcp-Session-Id`
  doesn't match the key that created it.
- **Results are presigned-only**: HTTP mode never writes to the pod's
  filesystem, so `convert_file`/`download_result` omit `local_path` and
  return `presigned_url` (+ expiry) instead (D-04).
- **Config is `OCTOCONV_BASE_URL` + `MCP_HTTP_ADDR` only** — no
  `OCTOCONV_API_KEY`, no `OCTOCONV_OUTPUT_DIR`. `OCTOCONV_BASE_URL` points at
  the in-cluster `api` Service (`http://api.<namespace>.svc.cluster.local:8090`).
- **Network exposure**: the `mcp-http` Service is `ClusterIP` only (no
  ingress/LoadBalancer), fronted by a dedicated NetworkPolicy that
  default-denies ingress except `:8070` from within the release namespace.
  Reach it from outside the cluster with:

  ```bash
  kubectl -n octoconv port-forward svc/mcp-http 8070:8070
  ```

## Roadmap

OctoConv is built and shipped in small, audited milestones — each one closes with a live
end-to-end verification pass, not just green unit tests.

### ✅ Shipped

- **v1.0 — Hardening MVP**: mandatory auth + rate limiting, signed webhook delivery with
  retry/dead-letter, transient/terminal retry classification, stranded-job reconciler,
  magic-byte content validation, storage TTL cleanup, Prometheus + health observability
- **v1.1 — Tech Debt Cleanup**: opt-in RFC1918 webhook delivery for internal networks,
  reconciler coverage for silently-dropped webhook deliveries, a real wall-clock soak test
  proving staleness recovery, decompression-bomb protection for images
- **v1.2 — Document Engine Class**: `docx`/`xlsx`/`pptx`/`odt`/`ods`/`odp` → `pdf` via
  LibreOffice headless, structural zip-bomb/macro validation, dedicated `document-worker`
- **v1.3 — Document Class v2**: cross-format document conversion (`docx`↔`odt`, `xlsx`↔`ods`,
  `pptx`↔`odp`), OLE-CFB legacy-binary rejection, HTML/Chromium engine class, dedicated
  `webhook-worker` decoupled from the engine workers
- **v1.4 — CI, Presets & Debt Cleanup**: first CI workflow, named conversion presets, and a
  registry-derived `GET /v1/formats` capability endpoint
- **v1.5 — MCP Access & Document Fidelity**: stdio MCP server (`cmd/mcp-server`), veraPDF
  ISO-19005 PDF/A validation, OLE-CFB encrypted-vs-legacy classification
- **v1.6 — Kubernetes & KEDA**: Helm chart deployment, KEDA queue-depth autoscaling,
  MCP-over-HTTP (`cmd/mcp-http`), operator-gated system-scope presets
- **v1.7 — Audio Engine & Hardening**: audio transcription via whisper.cpp
  (`cmd/audio-worker`)
- **v1.8 — AV Engine**: video conversion via ffmpeg (`cmd/av-worker`) — transcode, audio
  extraction, thumbnail generation

### 🔭 Planned

- Kubernetes validation in CI (kind/k3d)
- `is_operator` DB column vs. env-allowlist for operators
- Trim/crop as validated closed-opts for video (start/end timecodes)
- Dependency-advisory tracking for pinned engine binaries (ffmpeg, whisper.cpp, LibreOffice,
  veraPDF)

## Contributing

Issues and pull requests are welcome. OctoConv has shipped eight audited milestones
(v1.0–v1.8), each closed with a live end-to-end verification pass, not just green unit tests.

## License

[MIT](LICENSE)
