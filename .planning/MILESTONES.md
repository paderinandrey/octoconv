# Milestones

## v1.3 Document Class v2 (Shipped: 2026-07-12)

**Phases completed:** 5 phases, 17 plans, 44 tasks

**Key accomplishments:**

- Closed all 5 inherited advisory tech-debt items (v1.0 docker-compose audit + v1.2 WR-02/WR-03/WR-04 + gofmt nit) with zero new features, before v1.3 engine work begins.
- LibreOfficeConverter now handles docx<->odt, xlsx<->ods, pptx<->odp via an explicit (source,target) filter table, with non-PDF output validated by the same convert.SniffContainer that guards upload input, coupled same-commit with the worker's terminal-error classifier.
- Uploads beginning with the OLE-CFB magic byte header (legacy binary .doc/.xls/.ppt or password-protected OOXML) now get an immediate 422 before touching S3/Postgres, via a standalone `convert.IsOLECFB` detector wired as a third fail-closed branch in `handleCreateJob`.
- Extended `internal/e2e` with a 6-pair cross-format table (SniffContainer-verified) and a 2-fixture OLE-CFB-rejection table, then ran the full suite live against a freshly built docker-compose stack -- everything passed on the first run, including the existing 6 `->pdf` pairs and the webhook path, confirming CONV-01, CONV-02, and SAFE-01 end to end.
- Wired the already-existing, previously-inert `jobs.options jsonb` column into a live round-trip via `Job.Opts` / `CreateParams.Opts` (`map[string]any`), with no schema migration.
- Closed DocOpts contract with strict allow-list parsing, a PDF/A-2b filter-options builder assembled entirely from server constants, and a worker-side OutputIntent check that fails a mis-tagged PDF/A export terminally — the injection unit test proves adversarial client bytes never reach the soffice argv.
- API now parses/validates/persists/echoes the opts form field end to end (fail-closed 422 before storage), and a live docker-compose run confirmed a real PDF/A-2b export carrying `/Type/OutputIntent/S/GTS_PDFA1` on LibreOffice 7.4 -- no code corrections needed. Checkpoint APPROVED by the user (2026-07-11); plan complete.
- Third (html) engine class fully declared end-to-end — CHECK migration, EngineHTML const, htm-alias, dedicated asynq queue/retry/TTL, producer method, and reconciler routing — mirroring the document engine 1:1, with no converter or worker wired yet.
- Registered `{html→pdf}` ChromiumConverter with server-constant CSS `@page` print-option injection (never CLI flags) and layered network-block chromium argv, backed by a fail-closed HTML content sniff and a closed-strict print-opts struct mirroring Phase 14's PDFAFilterOptions invariant.
- HandleHTMLConvert + isHTMLTerminal wired into the worker (terminal-classified HTML_ENGINE_TIMEOUT, mirroring HandleDocumentConvert exactly), and handleCreateJob now fail-closed-detects HTML content, validates+persists print opts via an engine-keyed dispatch, and routes html jobs to their own asynq queue -- the entire HTML-01/02/03 Go code path is now present, pending only the chromium-worker container (Plan 04).
- Third engine-class container (chromium-worker) built and live-tested against the real chromium-headless-shell 150.0.7871.100 binary; two load-bearing RESEARCH.md assumptions (JS-disable via launch flag, print_background via CSS hint) were found broken and corrected in place with live-verified working mechanisms — checkpoint approved by user 2026-07-11.
- All four Phase 15 success criteria (html→pdf end-to-end, network-block canary with zero hits across external/loopback/compose-host/file:// vectors, page_size/print_background opts round-trip, and HTML_ENGINE_TIMEOUT terminal classification) live-verified against a freshly built docker-compose stack — awaiting human checkpoint sign-off, not self-approved.
- Postgres session-level advisory-lock (`pg_try_advisory_lock` on a dedicated `pgxpool.Conn`) added to `internal/reconciler` so exactly one webhook-worker replica sweeps at a time, fail-safe-closed on any lock-check error.
- New `cmd/webhook-worker` binary (trimmed from `cmd/worker`, wired with storage for PresignGet) consumes `TypeWebhookDeliver` and runs the reconciler sweeper under the Plan 16-01 Postgres advisory lock; `cmd/worker` is demoted to image-only with the webhook role and sweeper fully removed.
- Two named webhook-worker services (webhook-worker-1/-2, Dockerfile.webhook-worker) wired into docker-compose.yml with full storage+webhook+reconciler env, image/document/chromium workers stripped of webhook env, E2E host-gateway reachability moved to the new services, and .env.example documenting the webhook-worker-only config surface.
- All three Phase 16 success criteria live-verified against a freshly built two-webhook-worker stack and human-approved: webhook delivery survives image-worker absence (SC1), survives a mid-delivery consumer kill with zero lost/zero duplicated delivered webhooks (SC2), and exactly one fleet-wide advisory-lock sweeper with ~11s auto-failover (SC3). WEBH-01 satisfied.
- Verified CR-01/WR-01 pool-slot-leak and shutdown-blocking fixes already landed by a parallel quick-task, and closed the one remaining test gap with a new -race regression test for the TryAcquire/Close mutex interleaving.

---

## v1.2 Document Engine Class (Shipped: 2026-07-10)

**Phases completed:** 4 phases (8–11), 12 plans (incl. gap-closure 11-04), 26 tasks
**Audit:** passed — 10/10 requirements, 10/10 integration links, 1/1 E2E flow (`.planning/milestones/v1.2-MILESTONE-AUDIT.md`)
**Known deferred items at close:** 12 (6 newly acknowledged at v1.2 close: WR-02/03/04 advisory review findings, gofmt nit, 2 dormant seeds — see STATE.md Deferred Items)

**Key accomplishments:**

- Stdlib-only `SniffContainer` disambiguates all 6 ZIP-based office formats via root-part-name/mimetype structural checks, sums declared uncompressed size for a zip-bomb guard, and flags macro-carrying/duplicate-named parts in one `archive/zip` central-directory pass — plus a `HasDimensionLimit` predicate fixing the confirmed image-only dimension-check regression.
- `handleCreateJob` now structurally detects the six ZIP-based office formats via `convert.SniffContainer`, rejects zip-bomb-shaped and macro-carrying uploads with 422 before any storage write, and the confirmed image-only dimension-check regression is fixed with a `HasDimensionLimit` guard — all threaded through a new configurable `MAX_DOCUMENT_UNCOMPRESSED_BYTES` limit (500 MiB default).
- LibreOfficeConverter shells out to soffice headless for docx/xlsx/pptx/odt/ods/odp to PDF, self-isolates its LibreOffice profile per job, and validates output (size + %PDF- magic bytes) before returning success; registered in convert.Default and covered by unit tests plus a soffice-gated live process-kill proof test.
- Provisioned LibreOffice + fonts into Dockerfile.worker, built a repeatable Dockerfile.worker-test harness, and proved DOC-06's zero-survivors process-group-kill guarantee via a live-executed (not skipped) integration test — discovering and fixing a real zombie-process reaping gap (tini added as PID 1) along the way.
- Document conversion tasks now route to a dedicated `document` asynq queue with a genuinely-derived (not hardcoded) per-job unique-lock TTL and a no-jitter 5s/15s/30s retry schedule, mirroring the existing image-queue pattern exactly.
- Reconciler sweep now dispatches stranded-job recovery by `jobs.engine` (image -> image queue, document -> document queue) with a fail-closed, metric-visible skip for any other engine value, closing the launch-blocking image-only hardcode (DOC-09).
- HandleDocumentConvert task handler plus a standalone cmd/document-worker binary that consumes only the document asynq queue, classifying a DOCUMENT_ENGINE_TIMEOUT expiry as terminal via an engine-scoped isDocumentTerminal while leaving the image engine's timeout-as-transient isTerminal untouched.
- Reverted Dockerfile.worker to libvips-only and created Dockerfile.document-worker carrying LibreOffice + tini-as-PID-1, correcting Phase 9's pragmatic same-image compromise; added a document-worker compose service with its own concurrency/timeout and identical D-02 resource limits, and documented all three new env vars in .env.example.
- handleCreateJob now derives the job's engine class from content-detected format via a new Converter.Engine()/Registry.EngineFor contract, routing document uploads to the document queue (Engine="document") and image uploads to the image queue, replacing the last hardcoded image-only assumption in the request path.
- Env-gated `internal/e2e` suite drives all 6 document pairs (docx/xlsx/pptx/odt/ods/odp -> pdf) over real HTTP against a live compose stack — upload, poll, presigned `%PDF-` download check, plus a fully HMAC-verified webhook assertion on the docx pair — backed by soffice-verified fixtures and an E2E-only compose override.
- All 6 document format pairs (docx/xlsx/pptx/odt/ods/odp to pdf) converted successfully through a freshly built, live docker-compose stack (api + worker + document-worker + postgres/redis/minio), including a fully HMAC-verified signed webhook delivery for the docx pair — `go test ./internal/e2e/ -run E2E -v` ran (not skipped) and passed in 15.3s.
- Extended `convert.MIMEType` with the six document formats and `pdf`, closing the last gap in DOC-10 so document job uploads/downloads are served with the correct MIME type instead of `application/octet-stream`, exactly like image jobs.

---

## v1.1 Tech Debt Cleanup (Shipped: 2026-07-08)

**Phases completed:** 3 phases, 7 plans, 13 tasks

**Key accomplishments:**

- Added `WEBHOOK_ALLOW_PRIVATE_IPS` operator opt-in that narrowly relaxes only the RFC1918 check inside `isBlockedIP`, with a startup warning and both-sides test coverage, while loopback/link-local/unspecified stay hard-blocked.
- Derived, jitter-corrected `WebhookUniqueTTL` (2477.5s for MaxRetry=6/10s) wired into `asynq.Unique` on the webhook delivery task, closing the duplicate-enqueue race RECON-04's gap sweep depends on
- `FindWebhookGaps` NOT EXISTS anti-join detects done/failed jobs with a silently-dropped webhook enqueue, with `RecordWebhookGapRecovered` logging recovery without a fake status transition
- `Sweeper.sweep()` gains a second enqueue-first scan over `FindWebhookGaps`, combining Plan 01's `asynq.Unique` lock and Plan 02's gap-finder into the working RECON-04 behavior
- Two real-wall-clock integration tests (`TestSoakRecoversStrandedQueuedJob`, `TestSoakExhaustsAtCap`) prove Phase 3's staleness recovery and cap-exhaustion behavior under genuine elapsed time, using a live Postgres `jobs.Repo` paired with the existing in-memory `fakeEnqueuer`, completing in under 4 seconds combined
- `convert.Dimensions()` — hand-written binary parsers for PNG/JPEG/WebP/TIFF/HEIC extracting declared pixel width/height from a bounded 64 KiB non-seekable stream prefix, with zero new dependencies and full byte-fixture test coverage.
- MAX_IMAGE_PIXELS config/env wiring plus a handleCreateJob gate that calls convert.Dimensions between the format pair-check and callback_url validation, rejecting 422 before any storage write when declared pixel dimensions are unknown or exceed the configurable 100-megapixel default.

---

## v1.0 Hardening MVP (Shipped: 2026-07-08)

**Phases completed:** 4 phases, 15 plans, 36 tasks

**Key accomplishments:**

- Salted-SHA-256 client API key issuance: `0002` migration with dual-slot key columns, `internal/auth` hash helpers, `internal/clients` repository, and a `manage-clients` operator CLI supporting create/add-key/revoke.
- chi middleware turning issued API keys into hard-cutover 401 enforcement on `/v1/*`, with `client_id` threaded through job creation and a 404-only (never 403) cross-client ownership guard on job reads.
- In-process `go-chi/httprate` middleware (`internal/ratelimit`) with a coarse pre-auth IP flood guard and a per-client fair-use limiter keyed on the authenticated `client_id`, wired into `/v1` as `ByIP -> auth -> PerClient` with env-configurable 60/120 rpm defaults.
- Fixed two verified-but-unfixed gaps from `01-VERIFICATION.md`: jobs integration tests violating the new `jobs_client_id_fkey` when run against a live Postgres, and the pre-auth `ratelimit.ByIP` guard being fully bypassable via spoofed `X-Forwarded-For` because of chi's deprecated `middleware.RealIP`.
- POST /v1/jobs now accepts a per-job `callback_url`, rejecting SSRF targets (loopback/RFC1918/link-local/metadata) and non-https schemes with a fixed 400 before any storage write, and persists/reads it through Postgres via the existing nullable-column idiom.
- HMAC-SHA256 payload signing, a Postgres delivery-attempt repository with dead-lettering, and a single-attempt HTTPS deliverer (2xx-only, 10s timeout), each independently unit/integration tested.
- Completing jobs with a callback_url now trigger a signed, retried, tracked webhook end-to-end: `webhook:deliver` enqueued after MarkDone/MarkFailed, delivered with a freshly-presigned URL per attempt, retried by asynq with bounded exponential backoff + jitter, and dead-lettered after 6 exhausted retries.
- Image conversion tasks now retry on their own fast 2s/5s/15s schedule with a bounded MaxRetry (default 4) via a queue-aware RetryDelayFunc dispatcher, and carry a per-job asynq.Unique lock whose TTL is derived from IMAGE_MAX_RETRY + ENGINE_TIMEOUT so duplicate enqueues collide safely instead of double-processing.
- Worker now distinguishes transient from terminal image-conversion failures via a pure `isTerminal(err)` classifier, `MarkActive` is idempotent for asynq's same-task retries, raw vips stderr no longer reaches `error_message`, and a single whole-attempt timeout bounds download+convert+upload+record so no attempt can outlive the asynq unique-lock TTL.
- A ticker-driven reconciler now sweeps Postgres every minute for jobs stranded in `queued`/`active` past a staleness threshold, requeues genuinely-stranded ones through an enqueue-first, `asynq.ErrDuplicateTask`-guarded recovery path (never duplicating a still-live task or falsely inflating a backlogged job's recovery count), and terminally fails jobs that exceed a bounded recovery cap with a webhook fired on exhaustion.
- Magic-byte content sniffing (hardcoded 5-format signature table) gates `handleCreateJob` before any pair-check or S3 write, rejecting declared/detected mismatches and unrecognized content with a detailed 422 and a client-scoped log line.
- MinIO ILM lifecycle rule (7-day default TTL on uploads/ and results/) applied declaratively via minio-go's SetBucketLifecycle at API startup, plus a read-only storage.Ping probe for the future health endpoint.
- Defined four Prometheus metric families (job outcomes, job duration, webhook deliveries, reconciler actions) plus a pull-based queue-depth collector in a new `internal/metrics` package, and instrumented the existing worker/reconciler terminal exit points to call them — closing the instrumentation half of OBS-01.
- GET /healthz now pings Postgres, Redis, and S3/MinIO under a shared 3s timeout, returning 200/ok when all reachable and 503/degraded with per-dependency detail otherwise.
- Second localhost-only `/metrics` HTTP listener mounted in both `cmd/api/main.go` and `cmd/worker/main.go` (promhttp.Handler(), METRICS_ADDR default 127.0.0.1:9090), queue-depth collector registered in the worker, and a pinned `hibiken/asynqmon:0.7.2` dashboard service bound to 127.0.0.1:8980 — all three verified live end-to-end (real conversion job, real metrics scrape, real dashboard query).

---
