# Milestones

## v1.7 Audio Engine & Hardening (Shipped: 2026-07-18)

**Phases completed:** 5 phases, 18 plans, 44 tasks

**Key accomplishments:**

- Flipped all three KEDA ScaledObjects to fail-safe null handling and retry-inclusive PromQL, replaced a would-be recursive Prometheus checksum with a shared named-template, and closed the falsy-0 stabilization bug with a paired hasKey/not-nil guard — all verified offline via helm lint/template, no cluster involved.
- Wired the missing `OPERATOR_CLIENT_IDS` compose passthrough and proved the operator system-presets path end-to-end against the live compose stack: operator CRUD on `/v1/system/presets`, a byte-identical non-operator no-leak 404, and cross-client system-preset job usability (61/61 acceptance assertions passing).
- Closed keda-load-proof.sh's five remaining gate-tooling warnings (stale-pod race, false-PASS download check, orphaned watcher, unpinned interpreter, CWD-relative fixture), added keda-gate.sh's presigned direct-dial step (HARD-04/D-07), and live-verified the whole gate end-to-end (21/21 PASS) after fixing a Rule-1 bug the WR-01 fallback change exposed in a fresh-install run.
- Local whisper-cli v1.9.1 toolchain provisioned and live-verified, plus a bespoke ID3v2-aware MP3 magic-bytes detector and an ffprobe-based declared-duration guard, both fail-closed and unit-tested in `internal/convert`.
- EngineAudio const + AudioOpts{Language, Translate} validated-opts layer with a closed 6-entry language allowlist and an injection test proving client bytes never reach whisper-cli argv
- AudioConverter's two-stage ffmpeg-normalize -> whisper-cli-transcribe pipeline, live-verified against the pinned whisper-cli v1.9.1 binary to emit segment- and token-level JSON timestamps, deliberately left unregistered pending Phase 31's API/queue wiring
- Postgres CHECK-constraint migration, AudioConverter registration, and full asynq queue/task-type/UniqueTTL/client layer that unblocks every downstream audio-engine plan in Wave 2
- Stage-aware terminal/transient classifier (Key Decision 1) wired into a new HandleAudioConvert handler, with the previously-dormant duration guard actually spliced into the pipeline and ffprobe/ffmpeg path args hardened with the `file:` protocol prefix.
- Audio content-detection, opts validation, and enqueue routing wired live into both the API request path and the reconciler's stranded-job recovery path, closing two confirmed integration bugs (12-byte upload truncation and opts mis-routing) before they could ship.
- cmd/audio-worker built, verified, and live-proven end-to-end: an uploaded jfk.wav job flowed queued → active → done in ~2.8s against real compose infra (Postgres/Redis/MinIO) with local ffmpeg + whisper-cli, producing the correct transcript ("...ask not what your country can do for you..."); .env.example documents all 5 audio env levers plus both Phase-31-deferred tradeoffs.
- Three-stage `Dockerfile.audio-worker` (Go build, whisper.cpp v1.9.1 source-built with `-DGGML_NATIVE=OFF`, slim ffmpeg runtime) built and verified locally on arm64 (682MB), with the mandatory live cgroup v2 `cpu.max` spot-check confirming `200000 100000` under `--cpus=2.0` (Assumption A2 resolved).
- whisper-cli's `--threads` is now sized to the container's real cgroup v2 CPU quota (floor of quota/period, never host core count), resolved once at `cmd/audio-worker` startup via an `AUDIO_THREADS` env override → `CgroupCPULimit()` → `runtime.NumCPU()` precedence chain, and always injected as an explicit `-t <n>` argv pair on every whisper-cli invocation.
- `scripts/audio-rtf-measure.sh` measured p95 RTF=0.2059 (N=10, arm64, base model, 2-cpu container) and the NO-GO lever fired: AUDIO_MAX_DURATION_SECONDS is lowered from the placeholder 14400s (4h) to 1800s (30min), yielding a derived AUDIO_ENGINE_TIMEOUT=742s (12.4min, 17.6% margin under the asserted 900s/15m CAP) and a measured AUDIO_WORKER_CONCURRENCY=1.
- Added the audio-worker compose service with RTF-measured values (742s timeout, concurrency=1, cpus=2.0/memory=1g), propagated AUDIO_ENGINE_TIMEOUT/AUDIO_MAX_RETRY identically across all 7 queue.NewClient()-constructing services (IN-02), corrected the stale Phase-16 5m reconciler-CAP override on both webhook-workers to 15m (IN-16), added audio-worker to the CI bake matrix, and replaced .env.example's [ASSUMED] placeholder with the measured values.
- TestAudioConversionE2E passes live against the containerized audio-worker (docker compose), closing AUD-06's live-acceptance gate with an 8.10s observed job wall-clock — far inside the 5-minute cold-start bound and negligible against the CI e2e suite's 30-minute timeout.
- 1. [Rule 1 - Bug] Reworded two inline comments to avoid literal grep-gate string collisions
- New self-contained scripts/keda-audio-loadproof.sh: structural clone of scripts/keda-load-proof.sh, narrower (no trial-run mode) and with new scope (timestamped kubectl pod-event-timeline capture for the audio class's scale-from-zero proof), statically verified — the two frozen sibling scripts remain byte-unchanged.
- Live-ran both KEDA load-proof gates on OrbStack: keda-audio-loadproof.sh cleanly proved audio 0->1 scale-from-zero with the whisper model baked in (SC3, AUD-08 complete); the unmodified keda-load-proof.sh re-run newly confirmed SC1/SC2 pass under the WR-01-hardened chart but also empirically surfaced a real, previously-only-theoretical WR-05 jsonpath defect in SC3's BUSY_POD selection (fails loud, as 29-REVIEW.md predicted) -- Phase 29's deferred item is re-verified, not cleanly closed.

---

## v1.6 Kubernetes & KEDA (Shipped: 2026-07-17)

**Known deferred items at close:** 3 (stale v1.3-era quick-task record + 2 dormant seeds, SEED-004 substantively delivered — see STATE.md Deferred Items)

**Phases completed:** 5 phases, 14 plans, 33 tasks

**Key accomplishments:**

- Single flat Helm chart (`deploy/chart/octoconv`) with a complete interface-first `values.yaml`, dev-cred `values-local.yaml`, shared config/secret templates carrying the FQDN `S3_ENDPOINT`/`0.0.0.0` `METRICS_ADDR` landmine fixes, and hand-rolled Postgres/Redis/MinIO Deployments — lint-clean and accepted by a live OrbStack `kubectl apply --dry-run=server`.
- Five app Deployments (api + 4 engine-class workers) with dependency-aware probes and per-class grace periods, the atomic metrics-bind NetworkPolicy closure, chromium's /dev/shm memory mount, fixed 2-replica webhook-worker, and an idempotent createbucket post-install hook Job — zero source-tree changes, no migrate hook (api self-migrates per D-05 refinement).
- 1. [Rule 3 - Blocking] `--wait` + post-install hook chicken-egg
- Streamable-HTTP MCP endpoint with per-request caller-key pass-through, presigned-only remote results, and an unconditional session-key-binding hijack guard — after a live spike proved go-sdk v1.6.1 stateless mode delivers in-flight progress notifications.
- Dockerfile.mcp-http + gated single-replica Deployment/ClusterIP Service/NetworkPolicy for cmd/mcp-http, wired with key-free env (OCTOCONV_BASE_URL + MCP_HTTP_ADDR only) and verified entirely offline (helm lint, template gating, server dry-run).
- D-08 live hard gate PASSED: a real streamable-HTTP MCP session against a chart-deployed mcp-http pod completed initialize, tools/list=5, a real png→jpg conversion with a per-request minted caller key returning a presigned-only result (no local_path), a 401-without-key rejection, and a bonus session-hijack 403 — plus Phase 24's deferred SC3 presigned-from-host recheck, which again required the `--connect-to`-equivalent fallback path (OrbStack proxy wedge, same as 24-03).
- Operator-only REST CRUD for system-scope presets under `/v1/system/presets`, gated by an `OPERATOR_CLIENT_IDS` env allowlist that fails closed when empty and fails loud when malformed, reusing the existing scope-agnostic `PresetAdmin` interface unchanged.
- Fixed `presets.Repo.Create` to compute the next version via `COALESCE(MAX(version),0)+1` across active AND inactive rows, closing the "deactivate a preset then it's permanently unusable" 500 bug for both system and user scope, with a `pgconn` 23505 backstop mapping any residual race to `ErrAlreadyExists` (409).
- Moved `octoconv_queue_depth` from the four worker binaries to the always-on api process (single `prometheus.MustRegister` call for all four queues) and set per-class `asynq.Config.ShutdownTimeout` on all four workers, both live-verified against the full compose stack.
- Three per-class KEDA ScaledObjects (image/document/html) scaling from zero on an in-chart Prometheus's `octoconv_queue_depth` signal, with both landmine gaps from RESEARCH.md (NetworkPolicy monitoring-namespace mismatch, missing api :9090 Service port) closed in the same plan.
- Authored and executed `scripts/keda-gate.sh` on OrbStack k8s: KEDA v2.20.1 installed live, the full D-12 proof passed 18/18 assertions (metric resolves at genuinely 0 replicas, all three classes scale 0→1 from one real job each, image cycles back to 0, webhook-worker fixed at 2 throughout), and teardown left the cluster completely clean — human-verify checkpoint approved.
- Field-level `spec.replicas` omission on the three KEDA-scaled Deployments plus a values-gated document-class HPA scaleDown stabilization override, both production-inert by default and both machine-verified via `helm template` — the chart substrate the live load-proof gate (28-02/28-03) depends on.
- Three new tools -- a calibratable python-docx heavy-fixture generator, a headless matplotlib CSV-to-PNG dual-axis renderer, and a self-contained 854-line `keda-load-proof.sh` gate implementing the SC1/SC2 image-burst 0->N->0 scenario and the SC3 document-class downscale-soak with deterministic pod-deletion-cost victim selection and the D-09 triple-check -- all statically verified (bash -n, uv smoke runs) and ready for the live in-cluster run in plan 28-03.
- A single ALL-27-ASSERTIONS-PASSED live gate run against the real OrbStack cluster proving the full 0→4→0 image-class burst cycle (first replica +6s, peak 4 = maxReplicaCount at +11s, drain +76s, scale-to-zero +136s) and a 178s document conversion gracefully surviving a genuine KEDA/HPA 2→1 downscale (SIGTERM 142.8s before job completion, Completed/exit-0 with 188s of the 330s grace window unused) — all captured as committed, credential-free, timestamped evidence.

---

## v1.5 MCP Access & Document Fidelity (Shipped: 2026-07-13)

**Phases completed:** 4 phases, 10 plans, 22 tasks

**Key accomplishments:**

- Authenticated REST self-service for client-scope presets (create/list/show/update/deactivate) plus a registry-derived GET /v1/formats capability endpoint, both mounted inside the existing /v1 auth+rate-limit chain.
- curl-driven live gate (scripts/presets-rest-acceptance.sh) proving all five /v1/presets verbs, mass-assignment resistance, byte-identical no-leak 404s, and registry-derived GET /v1/formats against the real compose stack — 42/42 assertions passed on first run
- Hand-rolled, zero-internal-import HTTP client of the OctoConv public API implementing the blocking convert workflow (multipart upload -> poll -> presigned download), with API-key redaction, output-path containment, and an optional Host-preserving dial-redirect knob for presigned downloads -- all proven by offline httptest unit tests.
- Five agent-facing MCP tools (convert_file/get_job_status/download_result/list_supported_formats/list_presets) registered on the pinned go-sdk v1.6.1, with per-tick NotifyProgress during blocking conversion and upstream API failures surfaced as isError tool results — all proven against a real in-memory MCP session, not just faked handler calls.
- cmd/mcp-server ships as a thin, fail-fast, stderr-only-logging stdio binary; README documents client wiring; and the D-13 live stdio JSON-RPC hard gate PASSED against the real compose stack -- five tools, a real png→jpg conversion (presigned_url + local JPEG-magic file via the child binary's OCTOCONV_S3_DIAL_ADDR dial-redirect), list round-trips, bad-input isError, and full stdout purity.
- Hand-rolled, bounded, fuzz-hardened CFB directory-entry-name parser (`ClassifyCFB`) distinguishing encrypted vs. legacy-binary Office uploads, zero new dependencies, proven crash-free over a 3.5M-execution 30s native fuzz run.
- handleCreateJob now returns a distinct "remove the password" 422 for encrypted OOXML and a distinct "legacy binary ... convert to docx/xlsx/pptx" 422 for legacy .doc/.xls/.ppt, via `convert.ClassifyCFB`, proven live end-to-end against the real fixtures through the rebuilt compose stack.
- veraPDF CLI bundled into Dockerfile.document-worker via a Debian-JRE fallback path (jlink JRE fails the glibc boundary live); measured JVM cold-start p95 = 4650ms over 10 real PDF/A-2b validations -- GO, 2.15x margin under the 10s D-01 budget.
- ValidatePDFA wired into validateDocumentOutput's wantPDFA branch (after the /GTS_PDFA pre-filter) via the existing hardened runCommand, extended to capture stdout since veraPDF's `--format xml` report is stdout-only; fail-closed terminal classification (terminalVeraPDFSignatures) lands in the same commit, and VERAPDF_TIMEOUT is injected from cmd/document-worker/main.go via SetVeraPDFTimeout.
- Live, unconditional proof against the rebuilt document-worker image (real veraPDF JVM in the path): a genuine PDF/A-2b export still reaches done (no regression), and a marker-bearing-but-corrupted PDF/A export injected via an e2e-only soffice shim fails terminally with the exact "pdf/a non-compliant" veraPDF reason recorded in job_events -- all three live tests (TestPDFAExportE2E, TestPDFANonCompliantE2E, TestDocumentConversionE2E) passed in a single 43.16s run; document-worker image build-time delta measured at +12.13s (+8%) via two controlled --no-cache local builds.

---

## v1.4 CI, Presets & Debt Cleanup (Shipped: 2026-07-12)

**Phases completed:** 3 phases, 8 plans, 15 tasks

**Key accomplishments:**

- Removed dead webhook.NewRepo/NewDeliverer/WEBHOOK_SIGNING_SECRET wiring from cmd/document-worker and cmd/chromium-worker (mirroring cmd/worker's nil-safe pattern), and made fakeEnqueuer's call counters mutex-guarded so `go test ./internal/reconciler/... -race` runs the soak test clean against a live Postgres.
- Added TestImageConversionE2E (png->jpg via libvips) to internal/e2e, closing the last gap in the E2E format matrix — live-verified PASS against a real docker-compose stack with HMAC webhook confirmation.
- New `internal/presets` package with SQL-only scope-precedence preset resolution (shadowing + no-leak), bump-on-update versioning, and jobs.preset_name/preset_version provenance wiring — all backed by 11 DB-gated tests against live Postgres.
- Operator CLI (`cmd/manage-presets`) for create/update/list/show/deactivate of system- and client-scoped presets, backed by a new `internal/presets.ValidateOptsJSON` helper that fail-early-rejects opts matching neither the document nor HTML print allowlist schema (D-11).
- POST /v1/jobs now accepts `preset=<name>` which resolves through a narrow PresetRepo interface to target_format+opts, enforcing mutual exclusivity with explicit target/opts, a single non-leaking 422 for any resolution miss, full re-validation of stored opts through the existing engine-keyed parsers, and a pre-insert TOCTOU re-check that rejects a preset deactivated in the resolve-to-create window.
- scripts/presets-acceptance.sh — a 33-assertion live hard gate proving all five manage-presets CLI verbs plus preset resolution/provenance/shadowing/no-leak/re-validation against the real compose stack, passing twice in a row (fresh run + idempotent re-run) with a rebuilt api image.
- Authored `.github/workflows/ci.yml` — OctoConv's first-ever CI workflow, a 4-tier needs-chained pipeline (gate → race → docker-build → e2e) covering CI-01..CI-04, with every tier's exact commands locally hard-gated green before any push.
- Executed inline by the orchestrator

---

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
