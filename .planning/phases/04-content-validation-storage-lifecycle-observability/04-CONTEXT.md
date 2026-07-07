# Phase 4: Content Validation, Storage Lifecycle & Observability - Context

**Gathered:** 2026-07-07
**Status:** Ready for planning

<domain>
## Phase Boundary

Uploaded files are verified by their actual content (magic bytes) rather than trusted filename/metadata before anything is written to S3; uploaded inputs and conversion results are automatically purged from S3/MinIO after a retention window instead of growing storage unbounded; and operators get real visibility into system health — Prometheus metrics for queue depth/job outcomes/webhook delivery, a health endpoint that actually checks Postgres/Redis/S3 reachability, and an asynqmon dashboard for the asynq queue. This phase covers: `VALID-01`/`VALID-02` (content validation), `STOR-01` (storage TTL), `OBS-01`/`OBS-02`/`OBS-03` (metrics, health, asynqmon). It does NOT cover: new conversion engines, auth/rate-limiting changes (Phase 1, done), webhook delivery logic changes (Phase 2, done), or retry/reconciler logic changes (Phase 3, done) — this phase only adds observability into what those phases already built (reconciler recovery counter, webhook success/fail metric) without changing their behavior.

</domain>

<decisions>
## Implementation Decisions

### Content validation (VALID-01/02)
- **D-01:** Detected-format vs declared-extension mismatch (both are otherwise-supported formats, e.g. `.jpg` containing PNG bytes) → always reject with 422. No auto-correction to the detected format — the declared extension must be honest.
- **D-02:** Magic bytes matching no known signature at all (unrecognized/corrupt content) → also reject with 422. Never fall through and let the extension win.
- **D-03:** Detection approach — a small hardcoded signature table for exactly the formats currently registered in `convert.Default` (the libvips-backed pairs), not a general-purpose external detection library (e.g. `h2non/filetype`). Keeps zero new dependencies and full control over the supported-format list.
- **D-04:** 422 error responses include a detailed message (e.g. "declared format jpg does not match detected content png") rather than a generic "invalid file content" — clients are trusted internal services, so a fast, actionable error beats generic messaging. This is a deliberate, scoped exception to the general "handlers never leak internal error text" convention (CLAUDE.md) — the *declared vs detected format* is not sensitive internal state, unlike stack traces / raw engine stderr / file paths.
- **D-05:** Check order changes from current behavior: magic-byte detection now runs BEFORE the `convert.Default.Supports(source, target)` pair-check. Detected format becomes the source of truth fed into the pair-check (not the extension-derived format) — the extension is only used for the D-01 honesty comparison.
- **D-06:** S3-stored `Content-Type` metadata is overwritten with the canonical MIME type of the magic-byte-detected format, not the client-supplied multipart `Content-Type` header (which is no longer trusted after this phase).
- **D-07:** Peek buffer size — read only as many bytes as the longest signature in the hardcoded table actually needs (small, e.g. ≤16 bytes for the current jpg/png/webp/gif/tiff/bmp set), not a fixed generic buffer like `net/http.DetectContentType`'s 512 bytes. Must not fully buffer the upload into memory.
- **D-08:** API logs (`log.Printf`, with `client_id`) every magic-byte-mismatch rejection — an explicit, scoped exception to the "only `cmd/*/main.go` logs, `internal/*` never logs" convention (CLAUDE.md), justified because all clients are trusted internal services and a mismatch is a signal of a client-side bug/misconfiguration worth surfacing quickly, not routine request noise.
- **D-09 (deferred, not built this phase):** No declared-image-dimension / decompression-bomb limit in this phase — VALID-01/02 is about content-vs-format matching, not resource-exhaustion limits. See Deferred Ideas.

### Storage lifecycle (STOR-01)
- **D-10:** A single configurable TTL applies to both `uploads/` and `results/` prefixes (one env var, e.g. `STORAGE_TTL`), not two separate TTLs — simpler to operate and reason about.
- **D-11:** Default retention: 7 days.
- **D-12:** Mechanism — the API/worker sets a MinIO ILM (lifecycle) rule on the bucket via the minio-go SDK at startup (declarative, versioned with the code), not a manual `mc` CLI step in `docker-compose.yml` (which wouldn't apply automatically in other environments).

### Observability — metrics (OBS-01)
- **D-13:** Job-outcome metric labels: `engine` + `status` only — no `client_id` (cardinality grows with tenant count) and no `error_code` label (kept as a closed-enough set to revisit later, not blocking this phase).
- **D-14:** Add a job-duration histogram in addition to the ROADMAP-listed minimum (queue depth, job outcomes, webhook success/fail) — standard practice for a worker service, low cost to add now.
- **D-15:** Add a reconciler-recovery counter (recovered / exhausted, from Phase 3's reconciler) even though ROADMAP.md doesn't explicitly name it — currently the reconciler's actions are visible only via `job_events`, with no fast dashboard signal for a spike in recoveries.

### Observability — health endpoint (OBS-02)
- **D-16:** "Real" dependency check = a lightweight ping with a short timeout (~2-3s) per dependency: `pgxpool.Ping`, Redis `PING`, MinIO `BucketExists` — not a full read/write round-trip (no test `INSERT`, no test object PUT/GET). Keeps the healthcheck cheap and non-invasive.
- **D-17:** Degraded response = `503` with a JSON body detailing per-dependency status, e.g. `{"status":"degraded","postgres":"ok","redis":"timeout","s3":"ok"}` — standard pattern for k8s/compose healthchecks and useful for manual diagnosis; `503` correctly triggers restart/alerting semantics downstream.

### Observability — asynqmon dashboard (OBS-03)
- **D-18:** Deploy `hibiken/asynqmon` as a separate service in `docker-compose.yml`. Bind its port to `127.0.0.1` only (not `0.0.0.0`) — no additional auth layer needed since it's not reachable outside the host. Matches the project's "internal services only" trust model without adding new credential/secret management.
- **D-19 (added post-research):** The `/metrics` Prometheus endpoint is served on its own listener (e.g. `METRICS_ADDR=127.0.0.1:9090`), separate from the public `API_ADDR`, and bound to localhost only — same trust-model reasoning as D-18 (asynqmon). Raised by `gsd-phase-researcher` as a gap not covered during discuss-phase: job/queue metrics are operational data that should not be reachable by arbitrary internal API callers just because they can reach `API_ADDR`.

### Claude's Discretion
- Exact Go signature bytes/offsets for each of the currently-registered formats (jpg/png/webp/gif/tiff/bmp or whichever subset `convert.Default` actually has registered by the time this phase is planned) — implementation detail, verify against `internal/convert/converters.go`.
- Exact metric names/types (Counter vs Histogram bucket boundaries) following Prometheus Go client conventions — technical detail.
- Where the magic-byte peek/detect function and signature table live (e.g. `internal/convert` alongside the registry, since it's format-scoped, vs a new small package) — planner's call based on existing package boundaries.
- Exact MinIO ILM rule API call sequence (minio-go's `SetBucketLifecycle` or equivalent in v7.2.1) and whether it needs to be idempotent/safe to re-run on every startup — technical detail, researcher to confirm against the actual minio-go version already in `go.mod`.
- asynqmon version/image tag and exact docker-compose port mapping value — technical detail.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Project & Requirements
- `.planning/PROJECT.md` — Core value, Key Decisions table (hardening milestone status: Phases 1–3 closed, Phase 4 is the closing phase)
- `.planning/REQUIREMENTS.md` — `VALID-01`, `VALID-02`, `STOR-01`, `OBS-01`, `OBS-02`, `OBS-03` (locked v1 scope for this phase)
- `.planning/ROADMAP.md` — Phase 4 goal, success criteria; explicit note that all three sub-areas (validation, storage lifecycle, observability) are independent of each other and of the auth/webhook/reconciler critical path

### Existing Codebase (reference patterns to follow)
- `internal/api/handlers.go` `handleCreateJob` (lines ~34-95) — current format-from-extension logic (`source := convert.NormalizeFormat(strings.TrimPrefix(path.Ext(filename), "."))`) and the pair-check (`convert.Default.Supports(source, target)`) that must be reordered per D-05; also where the multipart `Content-Type` header is currently read and passed to `s3.Upload` (to be replaced with detected MIME per D-06)
- `internal/api/handlers.go` `handleHealth` (line 27) — current static `{"status":"ok"}` stub to replace with the real dependency-check logic (D-16/D-17)
- `internal/convert/convert.go` + `internal/convert/converters.go` — `convert.Default` registry; the exact set of currently-registered `(source, target)` pairs determines which magic-byte signatures need to be in the D-03 hardcoded table
- `internal/queue/queue.go`, `internal/worker/worker.go`, `internal/reconciler/reconciler.go` — Phase 2/3 outputs that OBS-01 metrics wrap (webhook delivery outcomes, job outcomes, reconciler recovery/exhaustion) without modifying their logic
- `internal/storage/storage.go`, `internal/storage/keys.go` — MinIO client wrapper and `InputKey`/`OutputKey` builders; the D-12 ILM lifecycle rule must target the same `uploads/`/`results/` prefixes these already produce
- `docker-compose.yml` — existing `minio`/`createbucket` one-shot service pattern (lines ~32-63) as the model for where a new `asynqmon` service (D-18) would be added; existing `api`/`worker` service env var wiring as the model for new `STORAGE_TTL` env var
- `.env.example` — existing only-env-var configuration convention (`DATABASE_URL`, `REDIS_ADDR`, `S3_*`, etc.) that new config (`STORAGE_TTL`, health-check timeout, asynqmon port) must follow

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `convert.NormalizeFormat` — existing format-string normalization (handles `jpeg`/`jpg`, `tif`/`tiff` aliases); the magic-byte detector's output should feed through this same normalizer so it composes cleanly with `convert.Default.Supports`
- `github.com/minio/minio-go/v7` client already wired in `internal/storage` — the same client instance can issue the ILM lifecycle-rule call at startup, no new S3 client needed
- Existing `.env.example` + `firstField` env-parsing helper pattern (`cmd/api/main.go:89`, `cmd/worker/main.go:77`) — reuse for new `STORAGE_TTL` / health-check-timeout / asynqmon port env vars

### Established Patterns
- Postgres-first, guarded-transition discipline (Phase 1-3) — health-check and metrics code should read dependency state, never write/mutate it, to stay a passive observer
- Engine-class queue routing (`image`/`webhook`) — metric labels should mirror this existing taxonomy (`engine` label) rather than inventing a new dimension
- HTTP handlers never leak internal error text (CLAUDE.md) — D-04's detailed 422 message and D-08's rejection logging are explicit, scoped, documented exceptions to this rule, not a reversal of it

### Integration Points
- `internal/api/handlers.go` `handleCreateJob` — where magic-byte detection + reordered pair-check + Content-Type override land
- `internal/api/handlers.go` `handleHealth` — where real dependency pings land
- New Prometheus metrics registration point — likely `cmd/api/main.go` and `cmd/worker/main.go` (wherever `promhttp` handler gets mounted) plus instrumentation calls added at the existing job-outcome/webhook-outcome/reconciler-action sites in `internal/worker/worker.go`, `internal/webhook/*`, `internal/reconciler/reconciler.go`
- `docker-compose.yml` — new `asynqmon` service block; MinIO ILM rule call needs to run once at API or worker startup (`cmd/api/main.go` or `cmd/worker/main.go`, planner to decide which is more natural given existing storage-client init location)

</code_context>

<specifics>
## Specific Ideas

No UI/UX references — this is a backend-only infrastructure phase, same character as Phase 3. All concrete asks are captured as decisions above: strict reject-on-mismatch content validation with a small hardcoded signature table, a single 7-day TTL applied via MinIO ILM, metrics scoped to `engine`+`status` plus a duration histogram and a reconciler-recovery counter, a lightweight-ping health check returning 503+JSON detail on degradation, and a localhost-only asynqmon container.

</specifics>

<deferred>
## Deferred Ideas

- **Declared-image-dimension / decompression-bomb protection** (D-09) — explicitly out of scope for VALID-01/02 in this phase; revisit as its own hardening item if a real incident or threat-model review flags it.
- **Per-client (`client_id`) or per-error-code labels on job-outcome metrics** — explicitly deferred (D-13) to avoid unbounded cardinality growth; revisit if per-client dashboards become a real operational need.
- **Basic-auth or other access control on asynqmon** — explicitly deferred (D-18) in favor of localhost-only binding; revisit if the deployment model changes (e.g. moving off docker-compose to a shared host where localhost-only isn't a sufficient boundary).

</deferred>

---

*Phase: 4-Content Validation, Storage Lifecycle & Observability*
*Context gathered: 2026-07-07*
