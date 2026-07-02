# Codebase Concerns

**Analysis Date:** 2026-07-02

## Tech Debt

**No reconciler for enqueue failures:**
- Issue: `handleCreateJob` writes the job row (status `queued`) in Postgres, then calls `s.queue.EnqueueImageConvert`. If enqueue fails, the handler returns a 500 but the job row is left in `queued` forever — the code comment even says "a reconciler (next steps) will recover it," but no such reconciler exists anywhere in the codebase.
- Files: `internal/api/handlers.go:105-109`
- Impact: A transient Redis blip during job creation silently strands jobs; clients polling `GET /v1/jobs/{id}` see `status: queued` indefinitely with no way to know the job will never run.
- Fix approach: Add a periodic sweep (cron job or asynq periodic task) that re-enqueues jobs stuck in `queued` past a threshold, or make job creation transactionally consistent with enqueue (e.g. transactional outbox).

**Single-attempt processing despite retry-capable queue:**
- Issue: `HandleImageConvert` calls `MarkActive` (guarded to only allow `queued -> active`) before running the engine. If the engine step fails for *any* reason — including transient ones like a momentary MinIO/Postgres network blip — `MarkFailed` is called, which is a terminal state. asynq's built-in retry mechanism (default up to 25 retries with backoff) will re-invoke the handler, but `MarkActive` will now fail because the job is `failed`, and that error is wrapped in `asynq.SkipRetry`. This means every job effectively gets exactly one real attempt regardless of asynq's retry configuration.
- Files: `internal/worker/worker.go:40-63`, `internal/jobs/repo.go:81-106`
- Impact: Transient infrastructure hiccups (S3 timeout, DB connection drop) permanently fail user jobs instead of being retried; the retry infrastructure that asynq provides is effectively unused.
- Fix approach: Distinguish transient vs. terminal errors (e.g. via a sentinel error type or by inspecting error class); only call `MarkFailed` for terminal errors and let asynq retry (returning a plain error without moving the job out of `active`) for transient ones. Alternatively, allow `MarkActive` to also accept `active` as a valid "from" state to support re-entry.

**No object lifecycle / cleanup for uploaded and converted files:**
- Issue: `job_outputs.expires_at` exists in the schema but nothing ever sets or enforces it. Every upload under `uploads/{job_id}/...` and result under `results/{job_id}/...` lives in MinIO/S3 forever.
- Files: `internal/db/migrations/0001_init.sql:89-102`, `internal/storage/storage.go`
- Impact: Storage grows unbounded with every job processed; no cost/capacity ceiling in production.
- Fix approach: Add a TTL sweep (bucket lifecycle rule in S3/MinIO, or an application-level cleanup job keyed off `job_outputs.expires_at`).

**Generic `vips copy` with no format-specific options:**
- Issue: `LibvipsConverter.Convert` always runs `vips copy <in> <out>` and relies purely on file extension to select the codec; there are no quality/compression parameters and no explicit alpha-channel handling.
- Files: `internal/convert/libvips.go:28-35`
- Impact: PNG (with alpha) → JPG conversions may fail or produce unexpected results since JPG has no alpha channel and `vips copy` does no flattening. Output quality/compression cannot be tuned per target format.
- Fix approach: Add per-target-format save options (e.g. use `vips jpegsave`/`vips webpsave` with explicit quality, or flatten alpha before JPG export) instead of the generic `copy` verb.

**Unused schema surface (presets, callback_url, webhooks, clients):**
- Issue: The DB schema (`clients`, `presets`, `jobs.client_id`, `jobs.callback_url`, `jobs.preset_name/version`, `webhook_deliveries`) is far larger than what the current vertical slice uses. None of these columns/tables are read or written by any Go code outside the migration.
- Files: `internal/db/migrations/0001_init.sql:10-39,114-127`
- Impact: Schema drift risk — future features may be planned against a schema whose shape was guessed rather than validated by working code; the unused columns make it hard to tell what's actually load-bearing.
- Fix approach: Either wire up the vertical slice for these fields as features land, or keep a comment/doc noting which parts of the schema are aspirational vs. active.

## Known Bugs

None identified. The codebase is small and the implemented vertical slice (image conversion) has been exercised by unit and (optional) integration tests; no reproducible defects were found during review.

## Security Considerations

**No authentication or authorization on any API endpoint:**
- Risk: `POST /v1/jobs` and `GET /v1/jobs/{id}` are fully public. Anyone who can reach the API can submit conversion jobs (consuming compute/storage) or, if job IDs are guessable/leaked, read another client's job status and download URL.
- Files: `internal/api/routes.go:16-20`, `internal/api/handlers.go`
- Current mitigation: Job IDs are random UUIDv4s (not sequential), which provides some obscurity, but there is no `clients` scoping enforced anywhere despite the `clients` table existing in the schema.
- Recommendations: Add an API key or token-based auth middleware and tie jobs to a `client_id` (the column already exists but is unused); return 403/404 for jobs not owned by the caller.

**Uploaded content-type and format are trusted from client-supplied data:**
- Risk: The source format is derived only from the filename extension (`path.Ext`) and the stored `Content-Type` is taken verbatim from the multipart header — neither is validated against the actual file bytes. A file with a `.png` extension containing arbitrary content is uploaded to storage and handed to `vips` unmodified.
- Files: `internal/api/handlers.go:60-76`
- Current mitigation: `vips copy` will typically fail loudly on genuinely malformed input, and the process runs in its own process group with a timeout (`internal/convert/exec.go`).
- Recommendations: Sniff actual file type (e.g. magic bytes) before trusting the extension; reject mismatches early rather than relying on the engine to fail safely.

**No image decompression-bomb / resource-exhaustion guard:**
- Risk: `MAX_UPLOAD_BYTES` (default 100 MiB) bounds the *compressed* upload size only. libvips can decode a small, highly compressed image into a very large in-memory/on-disk representation (classic "decompression bomb"), and there are no dimension/pixel-count limits configured before invoking `vips copy`.
- Files: `internal/convert/libvips.go`, `cmd/api/main.go` (`MAX_UPLOAD_BYTES` only)
- Current mitigation: The worker container has CPU/memory limits in `docker-compose.yml:113-117` (`cpus: 2.0`, `memory: 1g`), which bounds the blast radius but does not prevent individual job failures or noisy-neighbor effects.
- Recommendations: Pass `vips` explicit limits (e.g. `--vips-disc-threshold`, or check image header dimensions via `vipsheader`/`libvips` API before full decode) and reject oversized pixel counts before conversion.

**HEIC support depends on unverified libvips build flags:**
- Risk: `imageFormats` advertises `heic` as a supported source/target format, but `Dockerfile.worker` only installs `libvips-tools` via `apt-get` with no explicit confirmation that the Debian bookworm build includes HEIC/HEIF support (which itself depends on libheif and its codec backends, sometimes patent-encumbered and excluded from distro packages).
- Files: `Dockerfile.worker:11-13`, `internal/convert/libvips.go:9`
- Current mitigation: None — no test exercises an actual HEIC conversion (see Test Coverage Gaps).
- Recommendations: Verify HEIC support in the built worker image (`vips --vips-config` or a smoke conversion) as part of the Docker build or CI; if unsupported, drop `heic` from `imageFormats` or install `libheif` explicitly.

## Performance Bottlenecks

**No timeout around storage/DB calls outside the engine step:**
- Problem: `h.downloadTo` and `h.uploadFrom` in the worker, plus all `jobs.Repo` calls, use whatever context asynq hands the handler with no explicit deadline of their own. Only `conv.Convert` is bounded by `ENGINE_TIMEOUT` (`context.WithTimeout` wrapping just the engine call).
- Files: `internal/worker/worker.go:65-115,128-162`
- Cause: If MinIO or Postgres becomes slow/unresponsive, download/upload/DB calls can block a worker slot indefinitely, reducing effective concurrency below `WORKER_CONCURRENCY`.
- Improvement path: Wrap the whole `process()` call (or at least storage I/O) in a bounded context derived from a configurable job-level timeout.

## Fragile Areas

**Dev-mode presigned URL / Docker networking mismatch (self-documented):**
- Files: `README.md:70-72`
- Why fragile: When both API and worker run inside Docker Compose, the API presigns download URLs against the internal `minio:9000` hostname, which is unreachable from the host machine — the README explicitly calls this out and recommends running the API on the host instead. This is a footgun for anyone deploying the full compose stack behind a reverse proxy without adjusting the presign endpoint.
- Safe modification: Any change to how `storage.New` picks the S3 endpoint (e.g. introducing a separate "public" endpoint for presigning vs. an internal one for the SDK) should account for this split.
- Test coverage: None — this is purely an operational/deployment concern, not exercised by tests.

**`/healthz` does not check dependencies:**
- Files: `internal/api/handlers.go:25-27`
- Why fragile: `handleHealth` unconditionally returns `{"status":"ok"}` without pinging Postgres, Redis, or S3. A container orchestrator relying on this endpoint for readiness/liveness will report the API as healthy even when its dependencies are down.
- Safe modification: Extend the handler to (optionally, with a fast timeout) verify pool/queue/storage reachability, distinguishing liveness from readiness.
- Test coverage: None.

## Scaling Limits

**Single asynq queue / fixed worker concurrency:**
- Current capacity: The image engine class runs on one queue (`queue.QueueImage`) with `WORKER_CONCURRENCY` (default 4) in-process goroutines per worker instance; `docker-compose.yml` runs exactly one `worker` container.
- Limit: All image jobs share the same priority queue with no priority tiers, per-client fairness, or backpressure signaling to the API (the API always accepts and enqueues regardless of queue depth).
- Scaling path: Horizontal scale-out is possible (multiple worker containers/replicas pointed at the same Redis), since asynq supports multiple consumers per queue, but this isn't wired up in `docker-compose.yml` (single replica, no `deploy.replicas`).

## Dependencies at Risk

None identified as currently at risk — dependency set is small (`chi`, `uuid`, `asynq`, `pgx/v5`, `minio-go/v7`) and versions in `go.mod` are recent as of this analysis.

## Missing Critical Features

**No CI pipeline:**
- Problem: No `.github/workflows/` (or any other CI config) exists in the repository. `go test ./...` is not run automatically on push/PR.
- Blocks: Regressions in the unit-testable layers (`internal/api`, `internal/convert` registry logic) can be merged without automated verification; integration tests (DB/Redis/S3) are entirely opt-in via env vars and are never exercised unless a developer runs them locally against live infra.

**No webhook delivery despite schema support:**
- Problem: `jobs.callback_url` and the `webhook_deliveries` table exist in the schema, but nothing in the Go code sends, records, or retries a webhook callback on job completion.
- Blocks: Clients must poll `GET /v1/jobs/{id}` for status; no push-based completion notification is available even though the data model anticipates it.

## Test Coverage Gaps

**No test exercises a real libvips conversion:**
- What's not tested: `internal/convert/convert_test.go` only validates format normalization and the registry's pair lookup — it never invokes `LibvipsConverter.Convert` against a real image file (no `vips` binary is shelled out to in tests).
- Files: `internal/convert/convert_test.go`, `internal/convert/libvips.go`
- Risk: The actual value proposition of the service — successfully converting png/jpg/webp/heic/tiff via `vips copy` — has zero automated coverage. Format-specific breakage (e.g. the alpha-channel/JPG issue noted under Tech Debt, or HEIC support) would not be caught by `go test ./...`.
- Priority: High.

**Integration tests require live infrastructure and are skipped by default:**
- What's not tested: `internal/jobs/repo_test.go` (Postgres), `internal/queue/queue_test.go` (Redis `TestEnqueueImageConvert`), and `internal/storage/storage_test.go` (S3/MinIO) all `t.Skip` when their respective env vars (`DATABASE_URL`, `REDIS_ADDR`, `S3_ENDPOINT`) are unset — which is the default for `go test ./...` run without `docker compose up`.
- Files: `internal/jobs/repo_test.go:12-16`, `internal/queue/queue_test.go:32-35`, `internal/storage/storage_test.go:17-20`
- Risk: A plain `go test ./...` run (e.g. in a future CI setup that doesn't stand up Postgres/Redis/MinIO first) silently skips all repository, queue, and storage round-trip coverage, giving false confidence.
- Priority: Medium — acceptable for local dev, but any CI setup (see Missing Critical Features) must explicitly provision these services and set the env vars, or these packages are effectively untested in CI.

**No end-to-end test of the full job lifecycle (API -> queue -> worker -> storage -> status):**
- What's not tested: Each layer (API handlers, jobs repo, queue, storage, worker) has isolated tests/fakes, but nothing drives a request through `POST /v1/jobs` and asserts the job eventually reaches `done` with a downloadable output via the real worker handler.
- Files: n/a (gap spans `internal/api`, `internal/worker`, `internal/queue`, `internal/storage`, `internal/jobs`)
- Risk: Wiring bugs between layers (e.g. a payload field renamed in `queue.ConvertPayload` without updating the worker, or a storage key format mismatch between `storage.InputKey`/`OutputKey` and what the worker expects) would only surface in manual/production testing.
- Priority: Medium.

---

*Concerns audit: 2026-07-02*
