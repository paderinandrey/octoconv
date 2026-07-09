# 🐙 OctoConv

**An async, production-hardened file conversion service written in Go.**

A client uploads a file through a REST API; OctoConv stores it in S3-compatible object
storage, queues a conversion job, and a worker runs an external conversion engine
(currently [libvips](https://www.libvips.org/) for images, with
[LibreOffice](https://www.libreoffice.org/) for office documents landing next) and writes
the result back to storage. PostgreSQL is the system of record for job state; Redis
([asynq](https://github.com/hibiken/asynq)) is the queue broker.

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
- Engine-class queue routing: each conversion engine (image, document, …) gets its own
  asynq queue so worker pools scale independently
- Postgres-backed job lifecycle (`queued → active → done/failed`) with an append-only event
  log for every state transition

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

**Observability**
- Prometheus metrics (job outcomes, duration, webhook delivery, reconciler actions, queue
  depth) on a dedicated localhost-only port
- Real `/healthz` that pings Postgres, Redis, and S3 — not a static 200
- [asynqmon](https://github.com/hibiken/asynqmon) dashboard for live queue inspection
- Automatic TTL-based cleanup of uploaded/converted files (MinIO ILM lifecycle rules)

**Conversion engines**
- ✅ **Images** (`png`, `jpg`, `webp`, `heic`, `tiff`) via libvips — the original,
  fully-hardened vertical slice
- 🚧 **Office documents** (`docx`, `xlsx`, `pptx`, `odt`, `ods`, `odp` → `pdf`) via
  LibreOffice headless — content-safety validation and the conversion engine itself are
  built and live-tested (including a real process-group-kill proof against LibreOffice's
  `oosplash`/`soffice.bin` fork chain); wiring the dedicated worker, queue, and API routing
  is in progress (see [Roadmap](#roadmap))

## Architecture

```
                 multipart upload                 asynq (Redis)
Client ────────────────────────▶ API ───────────────────────────▶ Worker
                                  │                                  │
                                  ▼                                  ▼
                            PostgreSQL                    external engine (libvips,
                        (jobs, job_events,                 LibreOffice, …) via a
                         webhook_deliveries)                hardened process wrapper
                                  │                                  │
                                  │                                  ▼
                                  │                          S3 / MinIO storage
                                  ▼
                          presigned download URL
                          or signed webhook push
```

```
cmd/
  api/              — HTTP server: upload, status, presigned downloads
  worker/            — asynq consumer: image conversion, webhook delivery, reconciler
  manage-clients/     — operator CLI: issue/rotate/revoke API keys
  migrate/            — apply embedded SQL migrations
internal/
  api/                — routes, handlers, auth middleware, rate limiting, SSRF guard
  convert/            — Converter interface + registry, hardened exec, engine implementations
  jobs/               — Postgres-backed job repository with guarded state transitions
  queue/               — asynq task/queue definitions, retry schedules
  reconciler/           — stranded-job and webhook-gap recovery sweep
  webhook/             — HMAC signing, delivery, dead-letter tracking
  storage/             — S3/MinIO client, deterministic object-key layout
  metrics/             — Prometheus metric definitions
  auth/, clients/      — API-key hashing and client repository
  db/                  — connection pool + embedded migration runner
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
| `RATE_LIMIT_IP_RPM` / `RATE_LIMIT_CLIENT_RPM` | pre-auth and per-client rate limits |
| `WEBHOOK_SIGNING_SECRET` | HMAC-SHA256 secret for signed webhook callbacks (required) |
| `WEBHOOK_ALLOW_PRIVATE_IPS` | opt-in: allow `callback_url` to target RFC1918 addresses (loopback/link-local always blocked) |
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

### 🚧 In progress — v1.2: Document Engine Class

Adding a second conversion engine class (office documents → PDF via LibreOffice) on top of
the existing hardened infrastructure:

- ✅ Structural content validation for `docx`/`xlsx`/`pptx`/`odt`/`ods`/`odp` (zip-bomb
  guard, embedded-macro rejection, format spoofing detection)
- ✅ `LibreOfficeConverter` engine: per-job profile isolation, output validation, a live
  process-group-kill proof against LibreOffice's real process tree
- ⬜ Dedicated `cmd/document-worker` binary, resource-isolated from the image worker, with
  its own timeout budget
- ⬜ Engine-aware reconciler recovery and end-to-end API routing for document jobs

### 🔭 Planned

- Additional engine classes (audio/video via ffmpeg, archive handling)
- Cross-format conversion within the document class (`docx ↔ odt`, etc.)
- Kubernetes + KEDA autoscaling for production deployment
- Audio/video transcription-with-summary engine — early idea

## Contributing

Issues and pull requests are welcome. This is a young project — expect the architecture to
still be settling in places outside the hardened v1.0/v1.1 core.

## License

[MIT](LICENSE)
