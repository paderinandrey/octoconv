---
phase: 11-api-routing-end-to-end-document-conversion
plan: 02
subsystem: testing
tags: [go, e2e, docker-compose, libreoffice, webhook, presigned-url]

# Dependency graph
requires:
  - phase: 11-api-routing-end-to-end-document-conversion
    plan: 01
    provides: engine-aware routing in handleCreateJob (document uploads reach the document queue)
  - phase: 10-document-worker-reconciler-integration
    provides: document queue/worker (cmd/document-worker) the E2E jobs run through
  - phase: 09-libreoffice-converter-engine
    provides: LibreOfficeConverter (soffice) that renders the 6 fixtures to PDF
provides:
  - "env-gated live E2E suite (internal/e2e): 6 document pairs upload->poll->presigned-download->%PDF- check over real HTTP"
  - "signed-webhook delivery assertion folded into the docx pair (in-test 0.0.0.0 receiver, host-gateway reachable)"
  - "6 genuinely soffice-renderable office fixtures in internal/e2e/testdata/"
  - "docker-compose.e2e.yml E2E-only override (SSRF opt-out + host-gateway) leaving prod compose untouched"
affects: [11-03]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "E2E suite self-skips on missing E2E_BASE_URL as the gating helper's first action — same env-gated skip convention as all integration tests, lifted to the HTTP layer"
    - "Presigned-URL fetch via dial-redirect client (E2E_S3_DIAL_ADDR): redirect the TCP dial, never rewrite the URL host, so the V4 signature over the Host header stays valid"
    - "t.Run subtests used for the 6-pair table — deliberate, file-comment-documented deviation from the codebase's no-subtests convention"

key-files:
  created:
    - internal/e2e/e2e_test.go
    - internal/e2e/testdata/sample.docx
    - internal/e2e/testdata/sample.xlsx
    - internal/e2e/testdata/sample.pptx
    - internal/e2e/testdata/sample.odt
    - internal/e2e/testdata/sample.ods
    - internal/e2e/testdata/sample.odp
    - docker-compose.e2e.yml
  modified: []

key-decisions:
  - "Fixtures generated once via soffice --convert-to inside the document-worker image (txt/csv/fodp seeds) and checked in — every one verified renderable to %PDF- output before commit, so E2E failures can never be blamed on a broken fixture"
  - "Presigned download reachability from the host solved with a dial-redirecting http.Client (E2E_S3_DIAL_ADDR), not URL rewriting — MinIO's V4 signature covers the Host header, so rewriting minio:9000 -> localhost:9100 would 403"
  - "Webhook HMAC is fully verified via webhook.SignPayload when WEBHOOK_SIGNING_SECRET is set, with header-presence + body-correctness as the always-on minimum"
  - "docker-compose.e2e.yml is a layered override only (api: WEBHOOK_ALLOW_* opt-outs; worker + document-worker: host-gateway extra_hosts) — prod docker-compose.yml unchanged (T-11-04)"

patterns-established:
  - "First checked-in testdata/ fixtures and first true HTTP-layer E2E suite in the repo (TESTING.md previously: 'E2E Tests: Not used')"

requirements-completed: [DOC-10]

# Metrics
duration: 11min
completed: 2026-07-10
---

# Phase 11 Plan 02: Committed Live E2E Suite for Document Conversion Summary

**Env-gated `internal/e2e` suite drives all 6 document pairs (docx/xlsx/pptx/odt/ods/odp -> pdf) over real HTTP against a live compose stack — upload, poll, presigned `%PDF-` download check, plus a fully HMAC-verified webhook assertion on the docx pair — backed by soffice-verified fixtures and an E2E-only compose override.**

## Performance

- **Duration:** ~11 min (fixture generation through final commit)
- **Started:** 2026-07-10T00:05+03:00 (approx, includes document-worker image build for fixture generation)
- **Completed:** 2026-07-10T00:16:07+03:00
- **Tasks:** 3 completed
- **Files created:** 8

## Accomplishments

- Six genuinely-openable office fixtures generated with `soffice --headless --convert-to` inside the `Dockerfile.document-worker` image (seeds: one-line txt -> docx/odt, tiny csv -> xlsx/ods, minimal flat-ODP xml -> odp/pptx); every fixture was verified renderable to a valid `%PDF-` output by soffice before being committed
- `docker-compose.e2e.yml` layers the SSRF opt-outs (`WEBHOOK_ALLOW_PRIVATE_IPS`, `WEBHOOK_ALLOW_INSECURE_HTTP`) onto the `api` service and `host.docker.internal:host-gateway` onto `worker` (the webhook deliverer) and `document-worker`, with an explicit E2E-only/never-production header; `docker compose -f docker-compose.yml -f docker-compose.e2e.yml config` merges cleanly and prod compose is untouched (T-11-04)
- E2E harness (D-03): `e2eSetup` self-skips without `E2E_BASE_URL` as its first action; `provisionClient` replicates `cmd/manage-clients create` against the live `DATABASE_URL` (documented salt-must-match constraint); `postJob`/`pollUntilDone` drive the API contract over real HTTP; `startWebhookReceiver` binds `0.0.0.0` (not httptest's loopback) and returns a `host.docker.internal`-based callback URL, since loopback stays hard-blocked by the SSRF guard regardless of opt-outs
- 6-pair coverage (D-04): `TestDocumentConversionE2E` table-drives all pairs via `t.Run` (documented convention deviation), asserting terminal `done`, non-empty `download_url`, and `%PDF-` magic bytes on the actual downloaded body
- Webhook assertion (D-05): the docx pair alone registers a `callback_url`, blocks (90s bound) on the receiver channel, and asserts non-empty `X-OctoConv-Signature`/`X-OctoConv-Timestamp`, matching `job_id`, terminal status, `download_url` presence for done, plus full HMAC verification via `webhook.SignPayload` when `WEBHOOK_SIGNING_SECRET` is set
- Presigned-download reachability from the host solved in-test: `downloadClient()` redirects the TCP dial to `E2E_S3_DIAL_ADDR` (e.g. `127.0.0.1:9100`) while preserving the URL and Host header, keeping MinIO's V4 signature valid when `minio:9000` doesn't resolve outside the compose network

## Task Commits

Each task was committed atomically:

1. **Task 1: Real office fixtures + E2E compose override** - `d50b130` (feat)
2. **Task 2: E2E harness — gating, provisioning, HTTP + poll + webhook helpers** - `5cfa714` (feat)
3. **Task 3: 6-pair coverage + webhook + presigned-download assertions** - `9f3bca1` (test)

## Files Created/Modified

- `internal/e2e/e2e_test.go` - env-gated E2E suite: gating/provisioning/HTTP/poll/webhook helpers + `TestDocumentConversionE2E` (6 pairs, 1 webhook pair)
- `internal/e2e/testdata/sample.{docx,xlsx,pptx,odt,ods,odp}` - real, soffice-renderable office fixtures (4–24 KB each)
- `docker-compose.e2e.yml` - E2E-only compose override (SSRF opt-out on api; host-gateway extra_hosts on worker/document-worker)

## Decisions Made

- **Fixture generation strategy:** generated once via soffice in the document-worker image and checked in (rather than generated at test time) — deterministic, no soffice dependency on the machine running the test, and each fixture pre-verified renderable so a live E2E failure always implicates the pipeline, not the fixture
- **pptx/odp seeding:** `soffice --convert-to pptx/odp` cannot start from a `.txt` seed (no Impress import filter for plain text) — used a minimal flat-ODP (`.fodp`) XML seed instead; this deviation from the plan's suggested txt seed is a fixture-generation detail only, output verified identical in kind
- **Dial-redirect over URL rewrite** for presigned downloads (see key-decisions) — added `E2E_S3_DIAL_ADDR` as an optional env knob, defaulting to plain `http.DefaultClient` when unset (covers setups where the S3 endpoint hostname resolves from the host)
- **HMAC verification is conditional** on `WEBHOOK_SIGNING_SECRET` being exported to the test process, with header-presence assertions as the unconditional minimum — the plan explicitly allowed this shape

## Deviations from Plan

- **[Fixture seeding] pptx/odp generated from a flat-ODP XML seed, not a txt seed.** The plan suggested "a minimal source" for pptx/odp; `soffice --convert-to pptx` from `.txt` fails with "no export filter" (Impress has no plain-text import path). Fixed inline during Task 1 by seeding with a hand-written minimal `.fodp` (flat OpenDocument presentation) that Impress imports natively; both outputs verified structurally correct (`file` reports the claimed formats, ODF `mimetype` entries correct) and soffice-renderable to PDF. No scope change.
- **[Rule 2 - Missing critical functionality] Added `E2E_S3_DIAL_ADDR` dial-redirect client.** The plan's download step ("HTTP GET the download_url") would fail on any host where the compose-internal S3 endpoint (`minio:9000`) doesn't resolve — which is the default local setup. Without it, Plan 11-03's live run would dead-end. Added the `downloadClient()` helper (redirects the dial, preserves the signed Host header) plus doc comments. Files: `internal/e2e/e2e_test.go`. Commit: `5cfa714`.

## Issues Encountered

- **Docker disk exhaustion during the document-worker image build** (needed for fixture generation): `no space left on device` mid-build. Resolved with `docker builder prune -f` (freed ~5.7 GB of build cache); build then completed cleanly. No repo impact.

## User Setup Required

None to merge this plan — the suite self-skips offline. For the live run (Plan 11-03): bring the stack up with `docker compose -f docker-compose.yml -f docker-compose.e2e.yml up -d --build`, then run the suite with `E2E_BASE_URL=http://localhost:8090`, `DATABASE_URL=postgres://octo:octo-pass@localhost:5434/octo_db`, `API_KEY_SALT=dev-only-change-me-in-real-deploys`, `E2E_S3_DIAL_ADDR=127.0.0.1:9100`, and optionally `WEBHOOK_SIGNING_SECRET=dev-only-change-me-in-real-deploys` for full HMAC verification.

## Known Stubs

None — no placeholder values or unwired data paths. The suite's env-conditional branches (webhook HMAC verify, dial-redirect) are documented opt-in knobs, not stubs.

## Next Phase Readiness

- Plan 11-03 (the live run) is a one-command invocation: compose override + env vars above; all helpers, fixtures, and assertions are committed and verified compiling/self-skipping
- `go build ./...`, `go vet ./internal/e2e/`, `go test ./... -count=1` all green offline; `gofmt -l internal/e2e` clean; compose merge validated
- The `octoconv-document-worker:e2e-fixtures` image built during fixture generation remains locally available, shortening 11-03's first `--build`

## Self-Check: PASSED

All 8 created files exist on disk; all 4 task/doc commits (`d50b130`, `5cfa714`, `9f3bca1`, `79ca5c7`) verified in git log.

---
*Phase: 11-api-routing-end-to-end-document-conversion*
*Completed: 2026-07-10*
