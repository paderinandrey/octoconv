# Phase 4: Content Validation, Storage Lifecycle & Observability - Research

**Researched:** 2026-07-07
**Domain:** Go backend hardening — content-type/magic-byte validation, MinIO ILM lifecycle rules, Prometheus instrumentation for an asynq worker, health-check patterns, asynqmon deployment
**Confidence:** HIGH (all four flagged unknowns resolved against pinned dependency versions or official specs; one MEDIUM item flagged below)

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Content validation (VALID-01/02)**
- **D-01:** Detected-format vs declared-extension mismatch (both are otherwise-supported formats, e.g. `.jpg` containing PNG bytes) → always reject with 422. No auto-correction to the detected format — the declared extension must be honest.
- **D-02:** Magic bytes matching no known signature at all (unrecognized/corrupt content) → also reject with 422. Never fall through and let the extension win.
- **D-03:** Detection approach — a small hardcoded signature table for exactly the formats currently registered in `convert.Default` (the libvips-backed pairs), not a general-purpose external detection library (e.g. `h2non/filetype`). Keeps zero new dependencies and full control over the supported-format list.
- **D-04:** 422 error responses include a detailed message (e.g. "declared format jpg does not match detected content png") rather than a generic "invalid file content" — clients are trusted internal services, so a fast, actionable error beats generic messaging. This is a deliberate, scoped exception to the general "handlers never leak internal error text" convention (CLAUDE.md) — the *declared vs detected format* is not sensitive internal state, unlike stack traces / raw engine stderr / file paths.
- **D-05:** Check order changes from current behavior: magic-byte detection now runs BEFORE the `convert.Default.Supports(source, target)` pair-check. Detected format becomes the source of truth fed into the pair-check (not the extension-derived format) — the extension is only used for the D-01 honesty comparison.
- **D-06:** S3-stored `Content-Type` metadata is overwritten with the canonical MIME type of the magic-byte-detected format, not the client-supplied multipart `Content-Type` header (which is no longer trusted after this phase).
- **D-07:** Peek buffer size — read only as many bytes as the longest signature in the hardcoded table actually needs (small, e.g. ≤16 bytes for the current jpg/png/webp/gif/tiff/bmp set), not a fixed generic buffer like `net/http.DetectContentType`'s 512 bytes. Must not fully buffer the upload into memory.
- **D-08:** API logs (`log.Printf`, with `client_id`) every magic-byte-mismatch rejection — an explicit, scoped exception to the "only `cmd/*/main.go` logs, `internal/*` never logs" convention (CLAUDE.md), justified because all clients are trusted internal services and a mismatch is a signal of a client-side bug/misconfiguration worth surfacing quickly, not routine request noise.
- **D-09 (deferred, not built this phase):** No declared-image-dimension / decompression-bomb limit in this phase — VALID-01/02 is about content-vs-format matching, not resource-exhaustion limits. See Deferred Ideas.

**Storage lifecycle (STOR-01)**
- **D-10:** A single configurable TTL applies to both `uploads/` and `results/` prefixes (one env var, e.g. `STORAGE_TTL`), not two separate TTLs — simpler to operate and reason about.
- **D-11:** Default retention: 7 days.
- **D-12:** Mechanism — the API/worker sets a MinIO ILM (lifecycle) rule on the bucket via the minio-go SDK at startup (declarative, versioned with the code), not a manual `mc` CLI step in `docker-compose.yml` (which wouldn't apply automatically in other environments).

**Observability — metrics (OBS-01)**
- **D-13:** Job-outcome metric labels: `engine` + `status` only — no `client_id` (cardinality grows with tenant count) and no `error_code` label (kept as a closed-enough set to revisit later, not blocking this phase).
- **D-14:** Add a job-duration histogram in addition to the ROADMAP-listed minimum (queue depth, job outcomes, webhook success/fail) — standard practice for a worker service, low cost to add now.
- **D-15:** Add a reconciler-recovery counter (recovered / exhausted, from Phase 3's reconciler) even though ROADMAP.md doesn't explicitly name it — currently the reconciler's actions are visible only via `job_events`, with no fast dashboard signal for a spike in recoveries.

**Observability — health endpoint (OBS-02)**
- **D-16:** "Real" dependency check = a lightweight ping with a short timeout (~2-3s) per dependency: `pgxpool.Ping`, Redis `PING`, MinIO `BucketExists` — not a full read/write round-trip (no test `INSERT`, no test object PUT/GET). Keeps the healthcheck cheap and non-invasive.
- **D-17:** Degraded response = `503` with a JSON body detailing per-dependency status, e.g. `{"status":"degraded","postgres":"ok","redis":"timeout","s3":"ok"}` — standard pattern for k8s/compose healthchecks and useful for manual diagnosis; `503` correctly triggers restart/alerting semantics downstream.

**Observability — asynqmon dashboard (OBS-03)**
- **D-18:** Deploy `hibiken/asynqmon` as a separate service in `docker-compose.yml`. Bind its port to `127.0.0.1` only (not `0.0.0.0`) — no additional auth layer needed since it's not reachable outside the host. Matches the project's "internal services only" trust model without adding new credential/secret management.

### Claude's Discretion
- Exact Go signature bytes/offsets for each of the currently-registered formats (jpg/png/webp/gif/tiff/bmp or whichever subset `convert.Default` actually has registered by the time this phase is planned) — implementation detail, verify against `internal/convert/converters.go`.
- Exact metric names/types (Counter vs Histogram bucket boundaries) following Prometheus Go client conventions — technical detail.
- Where the magic-byte peek/detect function and signature table live (e.g. `internal/convert` alongside the registry, since it's format-scoped, vs a new small package) — planner's call based on existing package boundaries.
- Exact MinIO ILM rule API call sequence (minio-go's `SetBucketLifecycle` or equivalent in v7.2.1) and whether it needs to be idempotent/safe to re-run on every startup — technical detail, researcher to confirm against the actual minio-go version already in `go.mod`.
- asynqmon version/image tag and exact docker-compose port mapping value — technical detail.

### Deferred Ideas (OUT OF SCOPE)
- **Declared-image-dimension / decompression-bomb protection** (D-09) — explicitly out of scope for VALID-01/02 in this phase; revisit as its own hardening item if a real incident or threat-model review flags it.
- **Per-client (`client_id`) or per-error-code labels on job-outcome metrics** — explicitly deferred (D-13) to avoid unbounded cardinality growth; revisit if per-client dashboards become a real operational need.
- **Basic-auth or other access control on asynqmon** — explicitly deferred (D-18) in favor of localhost-only binding; revisit if the deployment model changes (e.g. moving off docker-compose to a shared host where localhost-only isn't a sufficient boundary).
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-------------------|
| VALID-01 | API проверяет содержимое загруженного файла по magic bytes перед сохранением/обработкой | Verified 5-format signature table (png/jpg/webp/heic/tiff), 12-byte peek buffer sizing, `io.MultiReader` peek-without-consume pattern — see Standard Stack, Pattern 1, Code Examples. |
| VALID-02 | При несовпадении определённого по содержимому формата с заявленным (расширение/Content-Type) API отклоняет запрос (422) до записи в S3 | Reordered validation flow (detect → compare-to-declared → pair-check → upload) mapped directly onto the existing `handleCreateJob` code path — see Architecture Patterns, Pitfall 5, Anti-Patterns. |
| STOR-01 | Загруженные файлы и результаты в S3/MinIO автоматически удаляются по истечении срока хранения (lifecycle TTL на `uploads/` и `results/`) | `minio-go/v7` v7.2.1 `SetBucketLifecycle`/`lifecycle.Configuration` API verified against the pinned tag; idempotency, day-granularity, and startup-ownership pitfalls documented — see Standard Stack, Pattern 2, Pitfalls 2-4. |
| OBS-01 | Сервис экспортирует Prometheus-метрики (глубина очереди, исходы задач, успешность доставки вебхуков) | `prometheus/client_golang` standard stack pick, `asynq.Inspector.GetQueueInfo` verified against pinned `v0.26.0`, custom-Collector pattern for queue depth, counter/histogram instrumentation points mapped onto existing `worker.go`/`webhook`/`reconciler.go` exit points — see Standard Stack, Pattern 3, Code Examples, Pitfall 6. |
| OBS-02 | Health-эндпоинт реально проверяет доступность Postgres, Redis и S3/MinIO, а не возвращает статичный `{"status":"ok"}` | Lightweight-ping pattern (`pgxpool.Ping`, Redis `PING`, MinIO `BucketExists`) with per-dependency timeout and 503+JSON degraded response mapped onto the existing `handleHealth` stub — see Code Examples, Open Question 2. |
| OBS-03 | Разворачивается asynqmon-дашборд для визуальной инспекции очереди | `hibiken/asynqmon` Docker image confirmed (no Go dependency), env vars (`REDIS_ADDR`, `PORT`) and localhost-only binding pattern confirmed from official README — see Summary, Environment Availability, Security Domain. |
</phase_requirements>

## Summary

This phase touches three independent surfaces of the existing OctoConv codebase, none of which require new architectural layers — only additive code in `internal/api`, `internal/storage`, `internal/worker`, `internal/webhook`, `internal/reconciler`, `cmd/api`, `cmd/worker`, and `docker-compose.yml`.

**Content validation (VALID-01/02):** `internal/convert/converters.go` currently registers exactly five formats — `png, jpg, webp, heic, tiff` (verified by reading `internal/convert/libvips.go:9`, not assumed). A small hardcoded signature table covering these five formats needs at most a **12-byte peek buffer** (WebP's signature is the longest: bytes 0-3 `RIFF`, bytes 8-11 `WEBP`). PNG, JPEG, and WebP signatures are cross-verified from two independent authoritative sources (WHATWG MIME Sniffing spec and Go's own `net/http/internal` sniff table, which implements that same spec). TIFF's signature is confirmed from IETF RFC 2301 / TIFF 6.0 spec citations. HEIC is the one non-trivial case: it is an ISOBMFF (MP4-family) container with a variable-length leading box-size field, so detection is "bytes 4-7 == `ftyp`" + "bytes 8-11 is one of a known brand list" rather than a single fixed byte string — this is flagged MEDIUM confidence (see Pitfall 1 and Assumptions Log).

**Storage lifecycle (STOR-01):** `github.com/minio/minio-go/v7` v7.2.1 (the exact version pinned in `go.mod`) ships `Client.SetBucketLifecycle(ctx, bucket, *lifecycle.Configuration)` and `Client.GetBucketLifecycle(ctx, bucket)` — verified directly against the `v7.2.1` git tag source, not just `master`. `SetBucketLifecycle` performs a full-replace PUT of the bucket's lifecycle XML document; calling it repeatedly with an identical `Configuration` is safe and idempotent (last-write-wins on an unchanged document), so calling it once at every process startup (API and/or worker) is a reasonable, low-risk pattern. One rule with two `Filter.Prefix`-scoped entries (`uploads/`, `results/`) covers both prefixes under the single `STORAGE_TTL` env var (D-10).

**Observability (OBS-01/02/03):** No existing Prometheus/health/dashboard code exists yet (`handleHealth` is a static stub at `internal/api/handlers.go:27`). `github.com/prometheus/client_golang` (latest tagged `v1.23.2`, confirmed via the Go module proxy against its GitHub release tag) is the standard, official Go Prometheus client — `promauto` + `promhttp.Handler()` is the idiomatic pattern for both `cmd/api/main.go` and `cmd/worker/main.go`. Queue depth is read via `asynq.Inspector.GetQueueInfo(queue)` — confirmed against the pinned `v0.26.0` `inspector.go` source — which returns per-queue `Pending/Active/Scheduled/Retry/Archived/Size` fields; implementing this as a custom `prometheus.Collector` (pull-based, no extra goroutine) is the cleanest fit for the existing codebase's "no unnecessary global state" convention. `hibiken/asynqmon` is a **separate Docker Compose service** (not a Go module dependency) — no `go.mod` changes are needed for OBS-03. It reads `REDIS_ADDR` (or `--redis-addr`) and exposes port 8080; binding it to `127.0.0.1:<port>:8080` in `docker-compose.yml` satisfies D-18.

**Primary recommendation:** Implement content validation and MinIO lifecycle as small, targeted additions to existing files (no new packages, only `internal/convert` gets a new file for the signature table); implement Prometheus metrics as a new `internal/metrics` (or `internal/observability`) package registered once in each `cmd/*/main.go`; add `hibiken/asynqmon` purely as a `docker-compose.yml` service block with no application code changes.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Magic-byte content validation (VALID-01/02) | API / Backend (`internal/api/handlers.go`) | Converter package (`internal/convert`, signature table) | Must run before any S3 write, inside the existing `handleCreateJob` request path; the signature table is format-scoped data, natural home next to `convert.Default`. |
| Storage TTL / ILM rule (STOR-01) | Database / Storage (`internal/storage`, MinIO) | API or Worker startup (`cmd/api`/`cmd/worker` main) | The rule itself is server-side S3 state; the *setting* of it is a one-time startup side effect triggered from whichever process owns `storage.New()` first. |
| Prometheus metrics — HTTP/job/webhook/reconciler (OBS-01) | API / Backend + Worker (cross-cutting) | — | Metrics are emitted at the exact point each event already happens (`internal/worker/worker.go`, `internal/webhook/*`, `internal/reconciler/reconciler.go`); `/metrics` is mounted in both `cmd/api/main.go` and `cmd/worker/main.go` since both processes have independent Prometheus-scrapeable state (API: HTTP/validation rejections; Worker: job outcomes, queue depth, webhook, reconciler). |
| Health endpoint (OBS-02) | API / Backend (`internal/api/handlers.go`) | Database / Storage (dependency pings) | `handleHealth` already lives in the API tier; it becomes an active (but read-only) prober of Postgres/Redis/S3, never a writer. |
| asynqmon dashboard (OBS-03) | Infra / Ops (docker-compose service) | — | Purely an operational tool reading Redis directly; no application code integration, no Go dependency. |

## Standard Stack

### Core

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/prometheus/client_golang` | v1.23.2 (latest tagged; any v1.2x is fine) | Prometheus metrics client (`promauto`, `promhttp`) | The official, canonical Go Prometheus client — used by virtually every Go service that exposes Prometheus metrics; documented directly on prometheus.io's own Go instrumentation guide [CITED: prometheus.io/docs/guides/go-application/]. |

### Supporting

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `github.com/minio/minio-go/v7/pkg/lifecycle` | Already vendored (part of `minio-go/v7` v7.2.1, no new `go.mod` entry) | Typed lifecycle-rule builder (`Configuration`, `Rule`, `Expiration`, `Filter`) | STOR-01 — build the `lifecycle.Configuration` passed to `Client.SetBucketLifecycle`. |
| `github.com/hibiken/asynq` (already a dependency, v0.26.0) `Inspector` type | Already vendored, no new `go.mod` entry | `NewInspector(redisOpt)` + `GetQueueInfo(queue)` for queue-depth gauges | OBS-01 — read-only queue introspection, no new dependency. |

**No new dependency for storage lifecycle or queue inspection — both ship inside libraries already in `go.mod`.** The only genuinely new Go module dependency this phase introduces is `github.com/prometheus/client_golang`.

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Hardcoded 5-format signature table (D-03, locked) | `h2non/filetype` or `gabriel-vasile/mimetype` | User explicitly locked out general-purpose detection libraries (D-03) to avoid a new dependency and keep the supported-format list fully controlled; not revisited here. |
| Custom `prometheus.Collector` for queue depth (pull-based) | A background goroutine that polls `Inspector.GetQueueInfo` on a ticker and sets a `GaugeVec` | The Collector pattern avoids a second background goroutine, avoids staleness between ticks, and matches the project's "no unnecessary global state / goroutines" bias (`internal/reconciler` already owns the one periodic-sweep goroutine pattern; adding a second unrelated ticker in `cmd/worker/main.go` would be redundant). A polling goroutine is a reasonable fallback if pull-based collection turns out to add noticeable latency to `/metrics` scrapes (unlikely at this queue depth). |
| Setting the MinIO lifecycle rule at both API and worker startup | Setting it only once, in a one-shot migration-style command (mirroring `cmd/migrate`) | D-12 explicitly wants it "declarative, versioned with the code" at process startup, not a separate manual step; calling it from both processes is safe because `SetBucketLifecycle` is idempotent, but planner should pick exactly ONE of {api, worker} to own the call to avoid two near-simultaneous redundant PUTs on every `docker-compose up`. |

**Installation:**
```bash
go get github.com/prometheus/client_golang@v1.23.2
```

**Version verification:** Confirmed via the Go module proxy against the package's GitHub release tag (an authoritative, VCS-backed source — not just a registry existence check):
```
$ curl -s https://proxy.golang.org/github.com/prometheus/client_golang/@latest
{"Version":"v1.23.2","Time":"2025-09-05T14:03:59Z","Origin":{"VCS":"git","URL":"https://github.com/prometheus/client_golang","Hash":"8179a560819f2c64ef6ade70e6ae4c73aecaca3c","Ref":"refs/tags/v1.23.2"}}
```
`github.com/minio/minio-go/v7` stays pinned at the already-vendored **v7.2.1** (`go.mod:11`) — no version bump needed; the lifecycle API used here was verified directly against that exact tag's source on GitHub.
`github.com/hibiken/asynq` stays pinned at the already-vendored **v0.26.0** (`go.mod:9`) — the `Inspector` API was verified directly against that exact tag's source.

## Package Legitimacy Audit

> slopcheck could not be installed in this research environment (sandbox denied `pip install slopcheck` as an unrelated external package execution, and `pip`/`pip3` were not available as a fallback). Per the graceful-degradation protocol, **all newly-introduced packages below are tagged `[ASSUMED]`** and the planner MUST gate their install behind a `checkpoint:human-verify` task, even though registry/proxy existence was independently confirmed.

| Package | Registry | Age | Downloads | Source Repo | slopcheck | Disposition |
|---------|----------|-----|-----------|-------------|-----------|-------------|
| `github.com/prometheus/client_golang` | Go module proxy (proxy.golang.org) | ~11 years (project started 2015; v1.23.2 tagged 2025-09-05) | Extremely high — de facto standard Go Prometheus client, used by Kubernetes, etcd, and most CNCF Go projects | `github.com/prometheus/client_golang` (official Prometheus org repo) | not run (unavailable) | Approved, `[ASSUMED]` pending human `go get` verification |

**Packages removed due to slopcheck [SLOP] verdict:** none — slopcheck did not run.
**Packages flagged as suspicious [SUS]:** none — slopcheck did not run.

*All packages above are tagged `[ASSUMED]` because slopcheck was unavailable at research time; the planner must gate the `go get github.com/prometheus/client_golang` step behind a `checkpoint:human-verify` task.* Note: `hibiken/asynqmon` and `minio/mc` (if used for manual verification) are **Docker images**, not Go module dependencies — they do not go through `go.mod`/slopcheck at all, but the Docker image tag itself should still be pinned to a specific digest/tag (not `:latest`) per standard supply-chain hygiene, and a human should confirm the image name against Docker Hub before `docker-compose up` in a real environment.

## Architecture Patterns

### System Architecture Diagram

```text
                         ┌─────────────────────────────────────────┐
                         │              docker-compose               │
                         │                                           │
  multipart POST         │   ┌────────────┐        ┌─────────────┐  │
  /v1/jobs  ────────────►│──►│  api (chi) │        │  worker     │  │
                         │   │            │        │  (asynq)    │  │
                         │   │ 1. parse   │        │             │  │
                         │   │ 2. peek≤12B│        │ 3. dequeue  │  │
                         │   │    magic   │        │ 4. download │◄─┼── S3/MinIO (uploads/, results/)
                         │   │    bytes   │        │    input    │  │      ▲ lifecycle rule (TTL, D-12)
                         │   │ 3. compare │        │ 5. vips     │  │      │
                         │   │    detected│        │    convert  │──┼──────┘
                         │   │    vs      │        │ 6. upload   │
                         │   │    declared│        │    result   │
                         │   │ 4a. 422 if │        │ 7. mark     │──┼──► Postgres (jobs, job_events,
                         │   │    mismatch│        │    done/    │  │      webhook_deliveries)
                         │   │ 4b. else   │        │    failed   │  │
                         │   │    upload  │        │ 8. emit     │──┼──► Prometheus metrics
                         │   │    (correct│        │    metrics  │  │      (job outcome, duration,
                         │   │    Content-│        │    (OBS-01) │  │       webhook, reconciler)
                         │   │    Type,   │        └──────┬──────┘  │
                         │   │    D-06)   │               │         │
                         │   │ 5. /healthz│               ▼         │
                         │   │    pings   │        ┌─────────────┐  │
                         │   │    PG/Redis│        │ reconciler  │  │
                         │   │    /S3     │        │ sweeper     │  │
                         │   │ 6. /metrics│        │ (existing)  │  │
                         │   └─────┬──────┘        └─────────────┘  │
                         │         │                                │
                         │         ▼                                │
                         │   Redis (asynq queues: image, webhook)   │
                         │         ▲                                │
                         │         │  read-only                     │
                         │   ┌─────┴──────┐                         │
                         │   │ asynqmon   │ 127.0.0.1:<port>:8080   │
                         │   │ (OBS-03)   │ dashboard, no app code  │
                         │   └────────────┘                         │
                         └─────────────────────────────────────────┘
```

### Recommended Project Structure

```
internal/
├── convert/
│   ├── convert.go          # existing: Converter interface + Registry
│   ├── converters.go       # existing: Default registry init()
│   ├── libvips.go          # existing: imageFormats = {png, jpg, webp, heic, tiff}
│   └── sniff.go            # NEW: magic-byte signature table + Detect(peek []byte) (format string, ok bool)
├── api/
│   └── handlers.go         # MODIFIED: handleCreateJob reordered (D-05), handleHealth real pings (D-16/17)
├── storage/
│   ├── storage.go          # MODIFIED: add SetLifecycle / EnsureLifecycle method
│   └── keys.go             # unchanged — InputKey/OutputKey prefixes match lifecycle rule prefixes
├── metrics/                # NEW package (name at planner's discretion, e.g. internal/metrics or internal/observability)
│   ├── metrics.go          # Counter/Histogram/GaugeVec definitions (promauto)
│   └── queue_collector.go  # custom prometheus.Collector wrapping asynq.Inspector.GetQueueInfo
├── worker/worker.go         # MODIFIED: instrument job-outcome counter + duration histogram at existing exit points
├── webhook/*.go              # MODIFIED: instrument delivery success/fail counter at RecordAttempt call site
└── reconciler/reconciler.go  # MODIFIED: instrument recovered/exhausted counter at existing sweep() branches
cmd/
├── api/main.go              # MODIFIED: mount promhttp.Handler() on /metrics, call storage lifecycle setup (if API owns it)
└── worker/main.go           # MODIFIED: mount promhttp.Handler() on /metrics (needs its own tiny http.Server), wire queue collector
docker-compose.yml            # MODIFIED: new asynqmon service, bound to 127.0.0.1
```

### Pattern 1: Magic-byte peek without full buffering (D-07)

**What:** Read only the first N bytes (N = 12 for this format set) from the multipart file before deciding to reject or continue streaming the rest to S3.
**When to use:** Any point where content must be classified before a large body is fully read/uploaded.
**Example:**
```go
// Source: pattern derived from net/http.DetectContentType's own approach
// (https://pkg.go.dev/net/http#DetectContentType), adapted to a small,
// closed signature set per D-03/D-07 instead of the general 512-byte sniff.
const sniffLen = 12 // longest signature needed: WebP (RIFF....WEBP)

func Sniff(r io.Reader) (detected string, rest io.Reader, err error) {
    buf := make([]byte, sniffLen)
    n, err := io.ReadFull(r, buf)
    if err != nil && err != io.ErrUnexpectedEOF { // short file is fine, just fewer signatures will match
        return "", nil, err
    }
    buf = buf[:n]
    detected = matchSignature(buf) // "" if no known signature matches (D-02)
    // Re-stitch the peeked bytes back onto the stream so the full file
    // (peeked prefix + remainder) is still uploaded intact to S3.
    rest = io.MultiReader(bytes.NewReader(buf), r)
    return detected, rest, nil
}
```
This `io.MultiReader` re-stitch pattern is the standard idiom for "peek without consuming" in Go — it is how `http.DetectContentType` callers typically avoid buffering an entire body, and how `bufio.Reader.Peek` works internally.

### Pattern 2: Idempotent MinIO lifecycle rule at startup (D-12)

**What:** Build a `lifecycle.Configuration` with one rule per prefix and PUT it unconditionally at startup.
**When to use:** STOR-01 — call once during `storage.New()` or immediately after, in whichever of `cmd/api`/`cmd/worker` the planner designates as the owner.
**Example:**
```go
// Source: verified against github.com/minio/minio-go/v7 v7.2.1 tag
// (api-bucket-lifecycle.go, pkg/lifecycle/lifecycle.go)
import "github.com/minio/minio-go/v7/pkg/lifecycle"

func (c *Client) EnsureLifecycle(ctx context.Context, ttl time.Duration) error {
    days := int(ttl.Hours() / 24)
    if days < 1 {
        days = 1 // MinIO ILM Expiration.Days has day granularity; sub-day TTLs are not representable server-side
    }
    cfg := lifecycle.NewConfiguration()
    cfg.Rules = []lifecycle.Rule{
        {
            ID:     "octoconv-uploads-ttl",
            Status: "Enabled",
            RuleFilter: lifecycle.Filter{Prefix: "uploads/"},
            Expiration: lifecycle.Expiration{Days: lifecycle.ExpirationDays(days)},
        },
        {
            ID:     "octoconv-results-ttl",
            Status: "Enabled",
            RuleFilter: lifecycle.Filter{Prefix: "results/"},
            Expiration: lifecycle.Expiration{Days: lifecycle.ExpirationDays(days)},
        },
    }
    if err := c.mc.SetBucketLifecycle(ctx, c.bucket, cfg); err != nil {
        return fmt.Errorf("set bucket lifecycle: %w", err)
    }
    return nil
}
```
**Idempotency note:** `SetBucketLifecycle` is a full-document PUT (MinIO server-side), not a merge/patch — calling it with the same `Configuration` value on every startup produces the same server-side document every time; there is no accumulation, no versioning conflict, and no error on re-application. Safe to call unconditionally on every process start (D-12's explicit ask).

### Pattern 3: Prometheus custom Collector wrapping asynq Inspector (OBS-01 queue depth)

**What:** A `prometheus.Collector` implementation whose `Collect()` method calls `Inspector.GetQueueInfo("image")` and `Inspector.GetQueueInfo("webhook")` synchronously on every scrape, emitting `Pending`/`Active`/`Scheduled`/`Retry`/`Archived` as a `GaugeVec` labeled by `queue` and `state`.
**When to use:** OBS-01 queue-depth metric, mounted alongside the standard `promauto`-registered counters/histograms.
**Example:**
```go
// Source: verified against github.com/hibiken/asynq v0.26.0 inspector.go
// (Inspector.GetQueueInfo, QueueInfo struct fields)
type queueDepthCollector struct {
    inspector *asynq.Inspector
    queues    []string
    desc      *prometheus.Desc
}

func NewQueueDepthCollector(inspector *asynq.Inspector, queues ...string) prometheus.Collector {
    return &queueDepthCollector{
        inspector: inspector,
        queues:    queues,
        desc: prometheus.NewDesc(
            "octoconv_queue_depth",
            "Number of tasks in an asynq queue by state.",
            []string{"queue", "state"}, nil,
        ),
    }
}

func (c *queueDepthCollector) Describe(ch chan<- *prometheus.Desc) { ch <- c.desc }

func (c *queueDepthCollector) Collect(ch chan<- prometheus.Metric) {
    for _, q := range c.queues {
        info, err := c.inspector.GetQueueInfo(q)
        if err != nil {
            continue // best-effort: a Redis blip should not crash a scrape
        }
        states := map[string]int{
            "pending": info.Pending, "active": info.Active,
            "scheduled": info.Scheduled, "retry": info.Retry, "archived": info.Archived,
        }
        for state, n := range states {
            ch <- prometheus.MustNewConstMetric(c.desc, prometheus.GaugeValue, float64(n), q, state)
        }
    }
}
```
Register once via `prometheus.MustRegister(NewQueueDepthCollector(asynq.NewInspector(redisOpt), queue.QueueImage, queue.QueueWebhook))` in `cmd/worker/main.go` (the worker already constructs `redisOpt` for its own `asynq.Server`).

### Anti-Patterns to Avoid

- **Buffering the entire upload to detect content type:** `net/http.DetectContentType` needs up to 512 bytes and is a fine general-purpose sniffer, but the D-03 decision deliberately avoids both external detection libraries AND large fixed buffers — always peek only `sniffLen` bytes (12 for this format set), never read-to-completion before deciding.
- **Trusting the multipart `Content-Type` header anywhere after detection runs:** D-06 requires the S3-stored `Content-Type` to always be the canonical MIME type of the *detected* format, never the client-supplied header — a residual use of `header.Header.Get("Content-Type")` (as `internal/api/handlers.go:89` does today) after this phase would silently reintroduce the trust-the-client bug this phase closes.
- **Calling `SetBucketLifecycle` from a request-handling code path:** it is a startup-time, one-time-per-process operation — not something to call per-job or per-request.
- **A second background polling goroutine for queue depth when a pull-based Collector suffices:** avoid unless the Collect-on-scrape latency proves problematic in practice.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Prometheus metric registration/exposition, HTTP `/metrics` handler, text-format encoding | A custom in-memory counter map + manual Prometheus text-format writer | `github.com/prometheus/client_golang` (`promauto`, `promhttp`) | Correct label cardinality handling, thread-safety, and the exposition format are exactly what this library exists for; hand-rolling risks subtly wrong histogram bucket math or race conditions under concurrent worker goroutines. |
| S3 object expiration / cleanup sweeper | A custom cron job or reconciler-style sweeper that lists and deletes objects older than N days | MinIO's native ILM (`SetBucketLifecycle`) | The storage engine already implements this exactly, atomically, and without needing the application to enumerate objects or run its own scheduler — D-12 explicitly chose this over a manual `mc` step for the same reason. |
| Queue-depth introspection | Manually scanning Redis keys/sorted-sets that asynq uses internally | `asynq.Inspector.GetQueueInfo` | asynq's internal Redis key layout is an implementation detail (subject to change between versions); `Inspector` is the stable, public, documented API for exactly this. |
| Asynq queue visualization/dashboard | A custom admin UI reading job status from Postgres or asynq | `hibiken/asynqmon` | Already exists, actively maintained by the asynq author, zero new application code — exactly the OBS-03 ask. |

**Key insight:** every "don't hand-roll" item in this phase already has an existing, in-repo, or already-in-`go.mod` mechanism — the entire phase is closing observability/lifecycle gaps with tools already present in the dependency graph, not introducing new infrastructure classes.

## Common Pitfalls

### Pitfall 1: HEIC detection is not a fixed-offset magic number
**What goes wrong:** Treating HEIC like PNG/JPEG/WebP (a fixed byte string at a fixed offset) will either false-negative on valid HEIC files (whose leading box-size field varies) or false-positive on other ISOBMFF-family files (`.mov`, `.mp4`, generic HEIF non-image variants).
**Why it happens:** HEIC is layered on the ISO Base Media File Format; the file begins with a 4-byte big-endian box *size* (not a magic constant) before the literal string `ftyp` at offset 4, and the actual format identity lives in a 4-byte "brand" at offset 8-11.
**How to avoid:** Match bytes 4-7 == `ftyp` (ASCII) AND bytes 8-11 is one of the six documented HEIF/HEIC brand codes: `heic`, `heix`, `hevc`, `hevx`, `mif1`, `msf1` [CITED: nokiatech.github.io/heif/technical.html, Table I]. This needs 12 bytes of peek (same as the WebP requirement, so it does not increase `sniffLen`).
**Warning signs:** Real-world `.heic` files (especially from iOS, which commonly write `mif1`/`heic` as compatible-brands rather than major-brand) failing detection — test against actual camera-produced HEIC samples, not only synthetic ones.

### Pitfall 2: MinIO's lifecycle expiration is not real-time
**What goes wrong:** Assuming an object disappears from S3 the instant it crosses the TTL boundary; operators may be confused when an "expired" object is still downloadable for some additional hours.
**Why it happens:** MinIO (like AWS S3) runs its ILM expiration scan as a periodic background process, not an immediate per-object timer; actual deletion happens on the next scan cycle after the object becomes eligible, not the exact moment it becomes eligible.
**How to avoid:** Document the TTL as "no manual cleanup required" (matching the phase's success criterion) rather than "deleted exactly at T+7d." Do not build any code path that assumes deletion is synchronous with expiration eligibility (e.g., do not gate any user-facing message on it).
**Warning signs:** A support ticket asking "why is this object still downloadable a few hours after its stated TTL?" — expected MinIO behavior, not a bug.

### Pitfall 3: `Expiration.Days` has whole-day granularity only
**What goes wrong:** A `STORAGE_TTL` env var expressed as `Nh` or `Nm` (hours/minutes) for fast local testing cannot be honored precisely by MinIO's lifecycle engine — `lifecycle.ExpirationDays` is an `int` number of days; there is no sub-day expiration field in this API.
**Why it happens:** S3-compatible lifecycle expiration (both AWS S3 and MinIO) is fundamentally day-granular by design.
**How to avoid:** Either (a) document that `STORAGE_TTL` must be expressed/rounded to whole days for the lifecycle rule (even if the env var itself accepts a `time.Duration` string like the rest of the codebase's convention), or (b) round up to the nearest full day in `EnsureLifecycle` and document the rounding behavior. For local dev/testing where a sub-day TTL is desired, note that MinIO cannot honor it via ILM — a manual `mc rm` or a test-only cleanup path would be needed, which is explicitly out of scope here.
**Warning signs:** A `STORAGE_TTL=1h` env value silently becoming a 1-day (or 0-day, if not clamped) rule.

### Pitfall 4: Two processes independently calling `SetBucketLifecycle` at startup
**What goes wrong:** If both `cmd/api` and `cmd/worker` call `EnsureLifecycle` at startup (both already call `storage.New()`), two near-simultaneous PUTs happen on every `docker-compose up`. This is safe (idempotent, same document) but redundant and could theoretically interleave with a future config change mid-rollout in a way that's hard to reason about.
**Why it happens:** Both processes construct their own `storage.Client` independently; there's no natural single "owner" without an explicit decision.
**How to avoid:** Planner should pick exactly one process (API is the more natural fit — it already runs `db.Migrate` as a startup-only side effect in `cmd/api/main.go:34`, establishing the "API owns one-time schema/infra setup at boot" convention) to call `EnsureLifecycle`, and leave the worker's `storage.New()` as a plain client construction with no lifecycle call.
**Warning signs:** None functionally (idempotent), but worth a design decision rather than an accident of "whichever file gets the TODO first."

### Pitfall 5: Content-Type override interacting with existing upload code path
**What goes wrong:** `internal/api/handlers.go:89` currently reads `contentType := header.Header.Get("Content-Type")` and passes it straight to `s3.Upload`; if content detection is added but this line is left untouched, D-06 (canonical detected-format Content-Type) silently doesn't happen even though validation itself works correctly.
**Why it happens:** The detection and the upload call are ~5 lines apart in the same function — easy to add the 422 check without touching the `contentType` variable's source.
**How to avoid:** Explicitly replace the `contentType` value with a small `canonicalMIME(detectedFormat)` lookup (mirroring the existing `contentTypeFor` function already in `internal/worker/worker.go:348-363` — consider whether to share/duplicate this function between `internal/api` and `internal/worker`, since both now need the same format→MIME mapping).
**Warning signs:** A code review catching that `handleCreateJob` still references `header.Header.Get("Content-Type")` after this phase lands.

### Pitfall 6: Job-outcome counter double-counting or under-counting across asynq retries
**What goes wrong:** `HandleImageConvert` can be invoked multiple times for the same logical job (each asynq retry re-enters the handler). If a "job outcome" counter is incremented on every handler invocation rather than only on a genuine terminal transition (`done` or `failed`), the counter will over-count retried jobs relative to the number of jobs actually processed.
**Why it happens:** The handler function is naturally where instrumentation gets added, but it runs once per *attempt*, not once per *job*.
**How to avoid:** Increment the outcome counter only at the two genuine terminal exit points already in `HandleImageConvert` — after `h.repo.MarkFailed` (label `status="failed"`) and after the successful `h.process` call that leads to `MarkDone` (label `status="done"`) — never inside the transient-error return path (where the job stays `active` for asynq's own retry).
**Warning signs:** Dashboard counters higher than the actual number of `jobs` rows reaching a terminal Postgres status.

## Code Examples

### Job-outcome counter + duration histogram instrumentation point (D-13, D-14)

```go
// Source: pattern derived from prometheus.io's official Go instrumentation
// guide (promauto.NewCounterVec / NewHistogramVec), applied at the existing
// terminal-transition points in internal/worker/worker.go.
var (
    jobOutcomes = promauto.NewCounterVec(prometheus.CounterOpts{
        Name: "octoconv_job_outcomes_total",
        Help: "Total conversion jobs reaching a terminal state, by engine and status.",
    }, []string{"engine", "status"}) // D-13: engine+status only, no client_id/error_code

    jobDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
        Name:    "octoconv_job_duration_seconds",
        Help:    "Wall-clock duration of a single conversion attempt.",
        Buckets: prometheus.DefBuckets, // 0.005s .. 10s default; revisit if attempts commonly exceed 10s
    }, []string{"engine", "status"})
)

// Inside HandleImageConvert, wrap h.process with a timer and observe once
// per terminal outcome (see Pitfall 6 — never on the transient-retry path):
start := time.Now()
err := h.process(ctx, job)
if err != nil {
    if isTerminal(err) {
        jobOutcomes.WithLabelValues(engineImage, "failed").Inc()
        jobDuration.WithLabelValues(engineImage, "failed").Observe(time.Since(start).Seconds())
        // ... existing MarkFailed / SkipRetry logic unchanged
    }
    // transient: no metric observation — job stays active, will retry
    return err
}
jobOutcomes.WithLabelValues(engineImage, "done").Inc()
jobDuration.WithLabelValues(engineImage, "done").Observe(time.Since(start).Seconds())
```

### Health endpoint with per-dependency timeout (D-16, D-17)

```go
// Source: pattern derived from pgxpool.Pool.Ping, go-redis Client.Ping,
// and minio-go Client.BucketExists — all already used elsewhere in this
// codebase (internal/db/db.go:27, internal/storage/storage.go:43).
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
    ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
    defer cancel()

    result := map[string]string{}
    healthy := true

    if err := s.pool.Ping(ctx); err != nil {
        result["postgres"] = "timeout"
        healthy = false
    } else {
        result["postgres"] = "ok"
    }

    if err := s.redis.Ping(ctx).Err(); err != nil {
        result["redis"] = "timeout"
        healthy = false
    } else {
        result["redis"] = "ok"
    }

    if _, err := s.storage.BucketExistsCheck(ctx); err != nil {
        result["s3"] = "timeout"
        healthy = false
    } else {
        result["s3"] = "ok"
    }

    status := http.StatusOK
    result["status"] = "ok"
    if !healthy {
        status = http.StatusServiceUnavailable
        result["status"] = "degraded"
    }
    writeJSON(w, status, result)
}
```
Note: `Server` currently has no direct `*pgxpool.Pool` or Redis client field (`internal/api/api.go` only holds `Repo`/`Storage`/`Enqueuer` interfaces) — the planner will need to either add narrow health-check-only interfaces (`Pinger` with a single `Ping(ctx) error` method) satisfied by `*pgxpool.Pool` and a small Redis ping wrapper, or thread the raw pool/Redis client into `Server`/`Config`, consistent with the existing interface-segregation pattern in `internal/api/api.go`.

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|---------------|--------|
| Trusting multipart `Content-Type` header / filename extension for format identity | Magic-byte content sniffing before any trust decision | Long-standing best practice (WHATWG MIME Sniffing spec, first published ~2011, still current) | Standard practice for any service accepting untrusted/semi-trusted uploads; not new to 2026. |
| `net/http.DetectContentType`'s 512-byte generic sniff | Purpose-built small signature table for a known-closed format set | N/A — both approaches coexist; the smaller table is a deliberate scope-reduction for this project (D-03/D-07), not an industry-wide shift | Lower memory/IO footprint, avoids buffering, but only works because the target format set is small and fixed. |

**Deprecated/outdated:** None identified — `minio-go/v7`, `hibiken/asynq` v0.26.0, and `prometheus/client_golang` are all current, actively maintained major-version lines; no migration/deprecation warnings apply to the APIs used here.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | HEIC brand list (`heic`, `heix`, `hevc`, `hevx`, `mif1`, `msf1`) is complete enough for real-world HEIC files (notably iOS Camera output) | Pitfall 1, Standard Stack | A valid HEIC upload from an untested producer (e.g., a different camera/encoder) could be wrongly rejected as "unrecognized content" (D-02) if it uses a brand outside this list; low risk since iOS's `mif1`/`heic` combination is the overwhelmingly common real-world case, but should be smoke-tested against actual sample files during implementation, not just synthetic byte arrays. |
| A2 | `github.com/prometheus/client_golang` package existence/currency, verified via Go module proxy + official prometheus.io docs but NOT via slopcheck (unavailable in this environment) | Package Legitimacy Audit, Standard Stack | Extremely low real-world risk (this is the official, first-party Prometheus Go client, referenced directly by prometheus.io's own documentation), but per protocol it is still tagged `[ASSUMED]` and gated behind a `checkpoint:human-verify` `go get` step since the automated slopcheck verification could not run. |
| A3 | Rounding/handling behavior for `STORAGE_TTL` values below 24h (day-granularity mismatch, Pitfall 3) is left as an open implementation decision, not a locked spec | Common Pitfalls, Architecture Patterns | If the planner doesn't explicitly address this, a dev/test `STORAGE_TTL` shorter than 1 day could silently become a 1-day (or worse, a 0-day/invalid) MinIO rule; low risk for production (default is 7 days per D-11) but could confuse local testing. |
| A4 | Exactly one of API or worker should own the `EnsureLifecycle` startup call (Pitfall 4) — recommended API, following the `db.Migrate`-at-boot convention | Pitfall 4 | If both call it, no functional harm (idempotent), but it's a minor design inconsistency the planner should resolve explicitly rather than leave to accident. |

## Open Questions

1. **Where does the shared `format → canonical MIME type` mapping live?**
   - What we know: `internal/worker/worker.go:348-363` already has a `contentTypeFor(format string) string` function for the five registered formats; the API side needs the identical mapping for D-06.
   - What's unclear: Whether to export/share this single function (e.g., promote it to `internal/convert`) or duplicate a second copy in `internal/api`.
   - Recommendation: Promote `contentTypeFor` to `internal/convert` (e.g., `convert.MIMEType(format string) string`) since it is genuinely format-registry-scoped knowledge, and have both `internal/worker` and `internal/api` call the shared function — avoids the two copies silently drifting if a new format is added later.

2. **Which process (API or worker) health-checks Redis, given only the worker currently owns an `asynq.Server`/Redis-native client?**
   - What we know: `cmd/api/main.go` builds a `queue.Client` (wraps `asynq.Client`, which itself wraps a Redis connection) but has no direct `go-redis` client exposed for a raw `PING`; `cmd/worker/main.go` builds `redisOpt` directly.
   - What's unclear: Whether OBS-02's `/healthz` (an API-tier endpoint per D-17's example JSON) needs the API process to open its own lightweight Redis ping client, or whether asynq's `Client`/`Inspector` exposes a usable ping-equivalent.
   - Recommendation: The API process should construct a minimal `*redis.Client` (or reuse `asynq.RedisClientOpt` + a raw ping) purely for the health check — `asynq.Client` itself does not expose a public `Ping()` method, so a small dedicated Redis ping connection is the cleanest path; confirm during planning whether `github.com/redis/go-redis/v9` (already an indirect dependency via asynq, `go.mod:27`) needs to become a direct dependency for this purpose.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Go toolchain | Building all new code | ✓ | go1.26.4 darwin/arm64 (per CLAUDE.md) | — |
| Docker / Docker Compose | Running `postgres`, `redis`, `minio`, new `asynqmon` service | Not probed directly in this research session (sandboxed environment); project's existing `docker-compose.yml` already assumes it | — | Local dev without Docker would need direct `vips`/Postgres/Redis/MinIO binaries per README; unaffected by this phase's additions. |
| `github.com/prometheus/client_golang` | OBS-01 | Confirmed resolvable via Go module proxy (network-reachable in this session) | v1.23.2 latest tag | — |
| `hibiken/asynqmon` Docker image | OBS-03 | Confirmed to exist on Docker Hub (`hibiken/asynqmon`), README-documented `docker run` pattern | Not pinned to a specific tag in research (README examples use bare `hibiken/asynqmon`, i.e. `:latest`) | Planner should pin to a specific tag rather than `:latest` for reproducibility; confirm exact available tags via `docker manifest inspect hibiken/asynqmon:latest` or the Docker Hub tag list at implementation time. |
| slopcheck (package legitimacy tool) | Package Legitimacy Audit | ✗ — `pip install slopcheck` denied by sandbox policy as an unrelated external package execution; `pip`/`pip3` binaries not present as a fallback either | — | Graceful degradation applied: all new packages tagged `[ASSUMED]`, planner gates installs behind `checkpoint:human-verify`. |

**Missing dependencies with no fallback:** none — every gap above has a documented fallback.
**Missing dependencies with fallback:** slopcheck (fallback: `[ASSUMED]` tagging + human verification checkpoint); asynqmon exact tag pin (fallback: verify tag manually at implementation time).

## Security Domain

> `security_enforcement` is not present in `.planning/config.json` — treated as enabled per protocol default.

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-------------------|
| V2 Authentication | No (unchanged this phase) | Existing API-key auth (`internal/auth`) untouched by VALID/STOR/OBS work. |
| V3 Session Management | No | Not applicable — stateless API-key auth, no sessions. |
| V4 Access Control | No (unchanged this phase) | `/healthz` intentionally stays outside `/v1` auth (matches existing `internal/api/routes.go:11-13` convention — health checks are typically unauthenticated for orchestrator probes); `/metrics` and asynqmon are new *unauthenticated-by-design* surfaces — see below. |
| V5 Input Validation | Yes | Magic-byte content validation (VALID-01/02) IS the input-validation control this phase adds — replacing trust-the-declared-format with verify-the-actual-bytes. |
| V6 Cryptography | No | No new cryptographic operations in this phase (HMAC webhook signing is Phase 2/unchanged). |
| V7 Error Handling / Logging | Yes (partial, already decided) | D-04/D-08 are explicit, documented, scoped exceptions to the "no internal error leakage" rule — content-mismatch details are not sensitive, and rejection logging with `client_id` is justified as legitimate operational signal, not sensitive-data leakage. |
| V12 File Handling | Yes | VALID-01/02 (content-vs-declared-format verification) directly addresses ASVS V12's file-upload verification requirement — verifying actual file type rather than trusting extension/MIME-type metadata is the canonical ASVS V12.1-family control. |

### Known Threat Patterns for this stack

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|-----------------------|
| Extension/MIME-type spoofing (e.g. a `.jpg` file containing a different or malicious payload format) | Tampering / Spoofing | Magic-byte verification before storage (this phase, VALID-01/02) — exactly the control being added. |
| Unbounded storage growth from abandoned/never-downloaded uploads and results | Denial of Service (resource exhaustion) | MinIO ILM TTL (STOR-01, this phase). |
| **New surface: `/metrics` and asynqmon exposed without authentication** | Information Disclosure | D-18 accepts this risk explicitly, scoped by binding asynqmon to `127.0.0.1` only (not `0.0.0.0`) — not reachable outside the host, matching the "internal services only" trust model already established for this project. The same reasoning should extend to `/metrics`: if `/metrics` is mounted on the same externally-reachable `API_ADDR`/`:8090` port as the public API (rather than a separate localhost-only port), it becomes reachable by anyone who can reach the API — job counts, queue depth, and webhook success/failure rates would be visible to any caller, not just internal operators. **This is a gap CONTEXT.md does not explicitly address for `/metrics` (only for asynqmon, D-18)** — flagged as an open question for planning: either mount `/metrics` on a separate localhost-only port/listener (mirroring D-18's asynqmon treatment) or explicitly accept it as low-risk internal-network exposure and document why. Given OBS-03's asynqmon precedent (`127.0.0.1`-only), the consistent choice is to also bind `/metrics` to a loopback-only listener or a separate internal port, not the public `API_ADDR`. |
| Decompression bomb / oversized declared-dimension image | Denial of Service | **Explicitly deferred (D-09)** — out of scope this phase, not mitigated. |

## Sources

### Primary (HIGH confidence)
- `github.com/minio/minio-go` v7.2.1 tag source: `api-bucket-lifecycle.go`, `pkg/lifecycle/lifecycle.go` (fetched directly from `raw.githubusercontent.com/minio/minio-go/v7.2.1/...`) — `SetBucketLifecycle`/`GetBucketLifecycle` signatures, `Configuration`/`Rule`/`Expiration`/`Filter` struct fields.
- `github.com/hibiken/asynq` v0.26.0 tag source: `inspector.go` (fetched directly from `raw.githubusercontent.com/hibiken/asynq/v0.26.0/inspector.go`) — `Inspector`, `NewInspector`, `GetQueueInfo`, `QueueInfo` struct fields.
- WHATWG MIME Sniffing Standard (`mimesniff.spec.whatwg.org`) — PNG/JPEG/WebP byte-pattern signatures.
- Go standard library source, `net/http/internal/sniff.go` (`raw.githubusercontent.com/golang/go/master/...`) — cross-verification of PNG/JPEG/WebP/GIF/BMP signatures against the same WHATWG spec, confirming Go's own implementation matches.
- IETF RFC 2301 / TIFF 6.0 spec citations (via `docs.fileformat.com`/`fileformat.info` summarizing the formal spec) — TIFF byte-order marker + magic number 42.
- prometheus.io official Go instrumentation guide (`prometheus.io/docs/guides/go-application/`) — `promauto`/`promhttp` usage pattern.
- Go module proxy (`proxy.golang.org/github.com/prometheus/client_golang/@latest`) — version/currency confirmation tied to the actual GitHub release tag/commit hash.
- Direct repository reads: `internal/convert/libvips.go`, `internal/convert/convert.go`, `internal/api/handlers.go`, `internal/api/api.go`, `internal/api/routes.go`, `internal/storage/storage.go`, `internal/storage/keys.go`, `internal/worker/worker.go`, `internal/queue/queue.go`, `internal/queue/client.go`, `internal/webhook/deliver.go`, `internal/webhook/repo.go`, `internal/reconciler/reconciler.go`, `internal/jobs/repo.go`, `internal/db/db.go`, `cmd/api/main.go`, `cmd/worker/main.go`, `docker-compose.yml`, `go.mod`, `.env.example`, `Dockerfile.api`, `Dockerfile.worker`, `internal/db/migrations/0001_init.sql` — the exact-current-state grounding for every architecture/pitfall claim above.

### Secondary (MEDIUM confidence)
- Nokia HEIF Technical Information page (`nokiatech.github.io/heif/technical.html`) — HEIF/HEIC brand code table (`heic`/`heix`/`hevc`/`hevx`/`mif1`/`msf1`). Official-adjacent (Nokia was a core HEIF standardization contributor) but not the formal ISO/IEC 23008-12 spec text itself.
- `hibiken/asynqmon` GitHub README (`raw.githubusercontent.com/hibiken/asynqmon/master/README.md`) — Docker run pattern, env var table (`PORT`, `REDIS_ADDR`, `REDIS_URL`, `REDIS_DB`, `REDIS_PASSWORD`, `REDIS_CLUSTER_NODES`, `REDIS_TLS`), default port 8080.

### Tertiary (LOW confidence)
- None retained as authoritative claims — all WebSearch-only findings were either cross-verified against a primary source above or explicitly flagged in the Assumptions Log.

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — every library/API call cited above was verified against the exact pinned version's source (minio-go v7.2.1, asynq v0.26.0) or an official spec/docs page, not training-data recall alone.
- Architecture: HIGH — all integration points (`handleCreateJob`, `handleHealth`, `worker.go` exit points, `reconciler.go` sweep branches, `webhook` delivery recording) verified by direct file reads of the current codebase, not assumed.
- Pitfalls: MEDIUM-HIGH — five of six pitfalls are grounded in verified source/spec behavior; Pitfall 1 (HEIC brand completeness) carries the phase's one genuine MEDIUM-confidence residual risk (A1 in Assumptions Log).

**Research date:** 2026-07-07
**Valid until:** 2026-08-06 (30 days — all pinned dependency versions are stable; re-verify if `go.mod` versions for `minio-go`/`asynq` change, or before choosing a specific `asynqmon` image tag at implementation time)
