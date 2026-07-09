---
phase: 11-api-routing-end-to-end-document-conversion
verified: 2026-07-10T02:05:00Z
status: passed
score: 4/4 must-haves verified
overrides_applied: 0
re_verification:
  previous_status: gaps_found
  previous_score: 3.5/4
  gaps_closed:
    - "GET /v1/jobs/{id} returns a working presigned download URL for a completed document job, identically to image jobs (Content-Type parity)"
  gaps_remaining: []
  regressions: []
---

# Phase 11: API Routing & End-to-End Document Conversion Verification Report

**Phase Goal:** A client can submit an office document and get a converted PDF back through the exact same API, webhook, and download flow already used for images — no separate integration path.
**Verified (initial):** 2026-07-10
**Verified (re-verification after gap closure):** 2026-07-10
**Status:** passed
**Re-verification:** Yes — after gap closure (plan 11-04)

## Re-verification After Gap Closure (2026-07-10)

**Prior status:** `gaps_found`, score 3.5/4. The single gap: `convert.MIMEType()` (`internal/convert/sniff.go`) had switch cases only for the five image formats, so document uploads and worker-produced PDF outputs were served with `Content-Type: application/octet-stream` instead of the correct MIME type — breaking SC#2's "identically to image jobs" claim.

**Gap-closure plan 11-04** (commits `2b34b50` fix, `19e1c45` test, `fd28f67` docs) claimed to close this by extending `MIMEType()`.

### What was independently re-verified

1. **Read `internal/convert/sniff.go` directly** (not trusting the SUMMARY). Confirmed `MIMEType()` (lines 102-131) now has explicit `case` branches for `pdf` (`application/pdf`), `docx` (`application/vnd.openxmlformats-officedocument.wordprocessingml.document`), `xlsx`, `pptx` (OOXML MIME types), and `odt`, `ods`, `odp` (OASIS ODF MIME types) — all seven formats that were previously falling through to the `default: "application/octet-stream"` case. The five pre-existing image cases (png/jpg/webp/heic/tiff) are untouched.
2. **Confirmed both call sites still route through this single function** (no parallel/divergent MIME table was introduced): `internal/api/handlers.go:231` (`contentType := convert.MIMEType(detected)`, stored-upload Content-Type) and `internal/worker/worker.go:409,420` (`convert.MIMEType(job.TargetFormat)`, worker's PDF-output Content-Type). Both are unchanged from the prior verification pass except for now receiving correct values from the fixed function.
3. **Read `internal/convert/sniff_test.go`** — `TestMIMEType` (lines 141-163) now table-drives all 13 cases (6 image + pdf + 6 document formats + an `"unknown"` fallback case), asserting exact MIME strings.
4. **Read `internal/api/handlers_test.go`** — confirmed `contentType` field exists on the `fakeStorage` (line 58, populated at line 65 in `Upload`), and the two document-routing tests now assert it: `TestCreateJob_DocumentDetectedAndAccepted` (line 534, asserts docx canonical MIME) and `TestCreateJob_ODFDetectedAndAccepted` (line 576, asserts odt canonical MIME) — mirroring the pre-existing PNG assertion pattern (line 336) exactly as the gap-closure plan intended.
5. **Ran the tests myself** rather than trusting the SUMMARY's claimed pass:
   - `go build ./... && go vet ./...` — clean.
   - `go test ./internal/convert/ ./internal/api/ -count=1 -v` — all tests pass, including `TestMIMEType`, `TestCreateJob_DocumentDetectedAndAccepted`, `TestCreateJob_ODFDetectedAndAccepted`.
   - `go test ./... -count=1` — all 14 packages `ok` (api, auth, clients, convert, e2e, jobs, metrics, queue, ratelimit, reconciler, storage, webhook, worker; db/cmd have no test files).
6. **Confirmed the referenced commits are real** (`git log --oneline`, `git show --stat`): `2b34b50` (fix: extend MIMEType), `19e1c45` (test: assert Content-Type parity), `fd28f67` (docs), all present in history with the diffs matching the SUMMARY's description.
7. **No regressions to the other 3 success criteria**: the fix touched only `internal/convert/sniff.go` (added switch cases, no removed/changed logic) plus two test files. Engine-aware routing (`handlers.go`'s `EngineFor`/dimension-skip logic), the webhook delivery pipeline, and the E2E suite/fixtures are all untouched by this gap-closure plan (`git show --stat` confirms only the 3 files listed in 11-04-SUMMARY.md's key-files were modified). The live E2E run captured in `11-03-SUMMARY.md` (human-approved checkpoint, all 6 format pairs PASS) remains valid evidence and was not required to be re-run per task instructions — its underlying code paths (routing, webhook, download URL generation) are unchanged.

### Updated Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | `POST /v1/jobs` routes an accepted office document to the document engine/queue (not image queue) and skips the image-only dimension check | ✓ VERIFIED | Unchanged since initial verification — no code in this path was touched by the gap-closure plan. `go test ./internal/api/ -run TestCreateJob -v` re-confirmed all routing/dimension-skip tests pass. |
| 2 | `GET /v1/jobs/{id}` returns status and a working presigned download URL for a completed document job, identically to image jobs | ✓ VERIFIED (was ⚠️ PARTIAL) | Content-Type parity gap closed: `convert.MIMEType()` now returns the correct canonical MIME type for `pdf` and all 6 document formats instead of `application/octet-stream`. Verified directly by reading `internal/convert/sniff.go:102-131` (not just trusting the SUMMARY) and by running `TestMIMEType` plus the two handler-level Content-Type assertions myself — all pass. The download mechanism itself was already generic/shared (confirmed in initial verification); now the served Content-Type is also identical in kind to image jobs (correct-format MIME instead of octet-stream). |
| 3 | Webhook delivery fires for completed/failed document jobs using the existing signed-delivery pipeline, no document-specific changes | ✓ VERIFIED | Unchanged — `internal/webhook/` was not touched by 11-04. Full `internal/webhook` test package still green (`go test ./internal/webhook/ -count=1` — ok). |
| 4 | A live end-to-end test converts all 6 supported format pairs (docx, xlsx, pptx, odt, ods, odp → pdf) through the full upload → convert → download pipeline | ✓ VERIFIED | Unchanged evidence trail from `11-03-SUMMARY.md` (human-approved live run, all 6 pairs PASS) — not re-run per task instructions since no code in the E2E path was modified by the gap-closure plan. `internal/e2e` package still compiles/self-skips offline (`go test ./internal/e2e/ -count=1` — ok). |

**Score:** 4/4 truths fully verified.

### Anti-Patterns Re-Check

No new `TBD`/`FIXME`/`XXX`/`TODO`/`HACK`/`PLACEHOLDER` markers introduced by the 3 files this gap-closure plan touched (`internal/convert/sniff.go`, `internal/convert/sniff_test.go`, `internal/api/handlers_test.go`) — confirmed by direct read of all three files. The `MIMEType` function's `default: return "application/octet-stream"` fallback is intentional (documented behavior for genuinely unrecognized formats, not a stub), and is itself asserted by the new `"unknown"` test case.

Pre-existing warnings from the initial verification (WR-02 E2E `extra_hosts` portability, WR-03 engine-class string literal duplication, WR-04 E2E HTTP client timeouts) are unchanged by this gap-closure plan — they were explicitly out of scope for 11-04 (confirmed in 11-04-SUMMARY.md's decisions) and do not block phase goal achievement; they remain informational/warning-level code-review debt, not blockers.

### Requirements Coverage (Updated)

| Requirement | Source Plan | Description | Status | Evidence |
|--------------|------------|-------------|--------|----------|
| DOC-10 | 11-01, 11-02, 11-03, 11-04 | `GET /v1/jobs/{id}`, webhook delivery, and presigned download URL work for document jobs identically to image jobs, without further doc-specific code | ✓ SATISFIED | Routing/webhook/download-mechanism reuse existing infrastructure with zero document-specific branching, AND Content-Type parity is now closed — document uploads and PDF outputs are served with the correct canonical MIME type, matching image job behavior exactly. |

**Documentation staleness note (unchanged, not a code gap):** `.planning/REQUIREMENTS.md` may still show DOC-10 as unchecked; this is a process/documentation matter outside this verifier's mandate (instructed not to modify REQUIREMENTS.md/ROADMAP.md/STATE.md) and does not affect the functional `passed` determination here.

### Gaps Summary

**No gaps remain.** The single gap identified in the initial verification (Content-Type parity for document uploads/PDF outputs, SC#2) has been closed by gap-closure plan 11-04. This was independently re-verified by reading the actual source diff in `internal/convert/sniff.go` (not trusting the SUMMARY's narrative), confirming both dependent call sites (`internal/api/handlers.go`, `internal/worker/worker.go`) inherit the fix without modification, running the new and pre-existing tests directly (`go test ./... -count=1` — all 14 packages green), and confirming the referenced commits (`2b34b50`, `19e1c45`, `fd28f67`) are real and match the claimed diffs. The other 3 success criteria were re-checked for regressions and remain intact — no code outside the 3 files listed in 11-04-SUMMARY.md was touched.

Phase 11's goal — a client submitting an office document gets a converted PDF back through the exact same API, webhook, and download flow already used for images — is now fully and observably achieved in the codebase.

---

## Initial Verification Report (2026-07-10, superseded by re-verification above)

**Phase Goal:** A client can submit an office document and get a converted PDF back through the exact same API, webhook, and download flow already used for images — no separate integration path.
**Verified:** 2026-07-10
**Status (at time of initial verification):** gaps_found
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | `POST /v1/jobs` routes an accepted office document to the document engine/queue (not image queue) and skips the image-only dimension check | ✓ VERIFIED | `internal/convert/convert.go` adds `Converter.Engine()` + `Registry.EngineFor`; `LibvipsConverter.Engine()` returns `"image"`, `LibreOfficeConverter.Engine()` returns `"document"` (both read directly, code present exactly as planned). `internal/api/handlers.go:183` calls `convert.Default.EngineFor(detected, target)`; `:266-278` switches on the returned engine to `EnqueueImageConvert`/`EnqueueDocumentConvert` with a fail-closed `default` (HTTP 500). `internal/api/handlers.go:198` gates `HasDimensionLimit(detected)` before the pixel check — documents (not in the image-format set) skip it entirely. Unit tests `TestCreateJob_OK` (image, asserts `enqueuedDocument==uuid.Nil`), `TestCreateJob_DocumentDetectedAndAccepted`/`_ODFDetectedAndAccepted` (asserts `enqueuedDocument==created, Engine=="document"`), and `TestCreateJob_DocumentSkipsDimensionCheck` (202 under `MaxImagePixels=1`) all present and pass (`go test ./internal/api/ ./internal/convert/ -count=1` green). |
| 2 | `GET /v1/jobs/{id}` returns status and a working presigned download URL for a completed document job, identically to image jobs | ⚠️ PARTIAL | The download mechanism is generic/shared code (`handleGetJob` in handlers.go has zero document-specific branching) and the live E2E run confirms all 6 pairs produced a working presigned URL returning genuine `%PDF-` bytes. BUT the served Content-Type is not identical to image jobs — see gap below. The URL "works" (returns correct bytes) but is not identical in header behavior. |
| 3 | Webhook delivery fires for completed/failed document jobs using the existing signed-delivery pipeline, no document-specific changes | ✓ VERIFIED | No document-specific code exists anywhere in `internal/webhook/`; the worker's document path reuses the exact same `MarkDone`/webhook-enqueue path as images (confirmed by reading `internal/worker/worker.go`'s single non-engine-specific completion path). Live E2E (`11-03-SUMMARY.md`) captured a real signed webhook: non-empty `X-OctoConv-Signature`/`X-OctoConv-Timestamp`, matching `job_id`, terminal status, and full HMAC verification via `webhook.SignPayload` against the known `WEBHOOK_SIGNING_SECRET` — all assertions passed with no `t.Error`/`t.Fatal`. |
| 4 | A live end-to-end test converts all 6 supported format pairs (docx, xlsx, pptx, odt, ods, odp → pdf) through the full upload → convert → download pipeline | ✓ VERIFIED | `internal/e2e/e2e_test.go`'s `TestDocumentConversionE2E` table-drives all 6 fixtures; `11-03-SUMMARY.md` captured a real run (`go test ./internal/e2e/ -run E2E -count=1 -v -timeout 20m`) against a freshly built docker-compose stack: `--- PASS: TestDocumentConversionE2E (14.41s)`, per-pair matrix shows PASS for all 6 pairs (docx 4.14s incl. webhook, xlsx/pptx/odt/ods/odp ~2.0s each), overall `ok ... 15.337s`. Human-verify checkpoint (Task 2 of 11-03-PLAN.md) was approved by the operator on 2026-07-10 per the SUMMARY's "Self-Check: PASSED" and "human-verify checkpoint... approved" notes. Six genuinely soffice-renderable fixtures verified present and structurally correct (`file` reports Microsoft Word/Excel/PowerPoint 2007+ for OOXML, OpenDocument Text/Spreadsheet/Presentation for ODF). |

**Score (initial):** 3/4 truths fully verified, 1/4 partially verified (functions but not "identical" as literally required) — reported here as `gaps_found` per the adversarial verification standard rather than rounding up to `passed`.

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/convert/convert.go` | `Engine()` on `Converter` interface + `Registry.EngineFor` | ✓ VERIFIED | Both present exactly as specified; `EngineFor` mirrors `Supports`, no parallel engine-class map introduced |
| `internal/convert/libvips.go` | `LibvipsConverter.Engine() string { return "image" }` | ✓ VERIFIED | Present, line 38 |
| `internal/convert/libreoffice.go` | `LibreOfficeConverter.Engine() string { return "document" }` | ✓ VERIFIED | Present, line 71 |
| `internal/api/api.go` | `Enqueuer` widened with `EnqueueDocumentConvert` | ✓ VERIFIED | Interface has both methods; doc comment updated to "dispatches conversion work to the appropriate engine-class queue" |
| `internal/api/handlers.go` | engine-aware routing in `handleCreateJob` | ✓ VERIFIED | `EngineFor` call, `engineDocument` const, fail-closed switch, `Engine: engine` (not hardcoded) all present |
| `internal/api/handlers_test.go` | split fakeQueue + document-routing + dimension-skip test | ✓ VERIFIED | `enqueuedImage`/`enqueuedDocument` fields present; `TestCreateJob_DocumentSkipsDimensionCheck` present; no stale "transitional"/"Phase 10/11" comments remain (`grep -ni` returns nothing) |
| `internal/e2e/e2e_test.go` | env-gated live E2E suite | ✓ VERIFIED | `E2E_BASE_URL` gate, `provisionClient`, `postJob`, `pollUntilDone`, `startWebhookReceiver` (binds `0.0.0.0`, not loopback), `TestDocumentConversionE2E` covering all 6 pairs + webhook on docx, `%PDF-` byte assertion, self-skips offline (`go test ./internal/e2e/ -count=1` → `ok` with no E2E_BASE_URL, confirmed by direct run) |
| `internal/e2e/testdata/sample.{docx,xlsx,pptx,odt,ods,odp}` | genuinely-openable fixtures | ✓ VERIFIED | All 6 present, non-empty (4-24 KB), `file` confirms correct format per fixture |
| `docker-compose.e2e.yml` | E2E-only override, prod compose untouched | ✓ VERIFIED | Present with explicit E2E-only header comment; `WEBHOOK_ALLOW_PRIVATE_IPS`/`WEBHOOK_ALLOW_INSECURE_HTTP` on `api`, `host.docker.internal:host-gateway` `extra_hosts` on `worker`/`document-worker`; `git diff --name-only docker-compose.yml` confirms zero prod-compose changes |
| `.planning/phases/.../11-03-SUMMARY.md` | captured E2E run output + pass/fail per pair | ✓ VERIFIED | Full per-pair matrix, webhook result, run configuration, and human-approval note present |

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| `internal/api/handlers.go` | `internal/convert/convert.go EngineFor` | `convert.Default.EngineFor(detected, target)` | ✓ WIRED | Called at handlers.go:183, result used to set `Engine:` and drive the switch |
| `internal/api/handlers.go` | `queue.EnqueueDocumentConvert`/`EnqueueImageConvert` | `switch engine` | ✓ WIRED | Both branches present; `queue.Client` (internal/queue/client.go:75,100) implements both methods |
| `internal/e2e/e2e_test.go` | live API | `http POST/GET against E2E_BASE_URL` | ✓ WIRED | Confirmed by the 11-03 live run (not skipped, ran and passed) |
| `internal/e2e/e2e_test.go` webhook receiver | document-worker → webhook consumer | `callback_url` on host.docker.internal | ✓ WIRED | Confirmed live: signed webhook received and HMAC-verified during the 11-03 run |
| `internal/reconciler/reconciler.go` | engine-aware recovery | `case "image"`/`case "document"` switch | ✓ VERIFIED (pre-existing from Phase 10, cross-checked here since 11-01-SUMMARY.md explicitly claims it as the pattern mirrored) | Present at reconciler.go:133,135 |
| `internal/db/migrations/0001_init.sql` | `jobs.engine` CHECK constraint | includes `'document'` | ✓ VERIFIED | Line 48: `CHECK (engine IN ('image', 'document', 'av', 'cad', 'archive', 'probe'))` |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Full unit test tree green | `go build ./... && go vet ./... && go test ./... -count=1` | All packages `ok`, including `internal/e2e` (self-skips offline) | ✓ PASS |
| Document fixtures are structurally correct | `file internal/e2e/testdata/sample.*` | Reports Microsoft Word/Excel/PowerPoint 2007+ and OpenDocument Text/Spreadsheet/Presentation, matching each claimed extension | ✓ PASS |
| No production compose drift | `git diff --name-only docker-compose.yml` | Empty | ✓ PASS |
| Live E2E run (already captured, re-verified from SUMMARY per task instructions rather than re-run) | `go test ./internal/e2e/ -run E2E -count=1 -v -timeout 20m` (per 11-03-SUMMARY.md) | `PASS` all 6 subtests, `ok ... 15.337s` | ✓ PASS (evidence trail, not re-executed per task instructions) |

### Requirements Coverage (Initial)

| Requirement | Source Plan | Description | Status | Evidence |
|--------------|------------|-------------|--------|----------|
| DOC-10 | 11-01, 11-02, 11-03 | `GET /v1/jobs/{id}`, webhook delivery, and presigned download URL work for document jobs identically to image jobs, without further doc-specific code | ⚠️ PARTIAL | Routing/webhook/download-mechanism all reuse the existing infrastructure with zero document-specific branching (satisfying the "no doc-specific changes" clause). However "identically to image jobs" is not fully true: document Content-Type is `application/octet-stream` instead of the correct MIME type, unlike images which get correct per-format Content-Type. Requirement is functionally mostly satisfied but not literally 100% met. |

**Documentation staleness note (not a code gap):** `.planning/REQUIREMENTS.md` still shows DOC-10 as an unchecked `[ ]` item with Traceability status "Pending" (line 30, line 72), even though ROADMAP.md and all three SUMMARY.md files mark Phase 11 complete with `requirements-completed: [DOC-10]`. REQUIREMENTS.md was not updated to reflect completion. This is an informational/process gap, not a functional one — recommend updating REQUIREMENTS.md's checkbox and Traceability row to "Done"/checked as part of gap closure or milestone wrap-up.

### Anti-Patterns Found (Initial)

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `internal/convert/sniff.go` | 99-114 | Incomplete MIME-type table (missing pdf + 6 document formats, silent `application/octet-stream` fallback) | ⚠️ Warning | Document uploads and PDF outputs served with wrong Content-Type on the presigned download URL — see gap above. Already flagged by the phase's own code review (`11-REVIEW.md` WR-01) and left unfixed. |
| `docker-compose.e2e.yml` | api service block | Missing `extra_hosts: host.docker.internal:host-gateway` on the `api` service (only added to `worker`/`document-worker`) | ⚠️ Warning (code-review WR-02, not independently re-verified here since it did not block the already-captured macOS/Docker-Desktop live run, but would break the harness on plain Linux docker engines) | Does not affect functional truth of DOC-10 on the platform where 11-03 was actually run, but is a portability defect in the reusable harness artifact |
| `internal/api/handlers.go` + `internal/convert/{libvips,libreoffice}.go` + `internal/reconciler/reconciler.go` + `0001_init.sql` | multiple | Engine-class name (`"image"`/`"document"`) hand-duplicated as literals in 4+ places despite `Engine()`'s doc comment claiming single-source-of-truth | ℹ️ Info (code-review WR-03) | Drift risk for a future third engine class; fail-closed behavior mitigates actual breakage |
| `internal/e2e/e2e_test.go` | 143, 181, 315, 391-404 | HTTP clients have no request timeout | ℹ️ Info (code-review WR-04) | Suite diagnosability risk on a hung live stack, not a functional gap in the shipped feature |

No `TBD`/`FIXME`/`XXX` unreferenced debt markers found in any of the 9 files this phase touched (`internal/convert/convert.go`, `libvips.go`, `libreoffice.go`, `convert_test.go`, `internal/api/api.go`, `handlers.go`, `handlers_test.go`, `internal/e2e/e2e_test.go`, `docker-compose.e2e.yml`).

### Human Verification Required (Initial)

None outstanding — the phase's own human-verify checkpoint (11-03-PLAN.md Task 2) was already executed and approved by the operator on 2026-07-10, with the per-pair matrix and webhook result captured in `11-03-SUMMARY.md`. Per the task instructions, this live run is treated as valid evidence and was not re-executed by this verifier.

### Gaps Summary (Initial)

The phase substantially achieves its goal: engine-aware routing is real, tested, content-derived code (not a hardcoded/stubbed assumption); the document queue, worker, and reconciler are correctly wired; webhook delivery is proven live with full HMAC verification; and a genuine, human-approved live E2E run passed all 6 document format pairs.

The one concrete gap is narrower than the phase goal itself: the presigned download URL for a completed document job is NOT served with a correct Content-Type header (`application/octet-stream` instead of `application/pdf`), because `convert.MIMEType()` was never extended for document formats when Phases 8-10 added them, and this phase (11) — the first to actually exercise that code path for documents in a live API request — surfaced it without fixing it. This exact defect was already independently caught by this phase's own code review (`11-REVIEW.md`, WR-01) on the same day the phase completed, and no follow-up commit addressed it. The download itself works (correct bytes, `%PDF-` verified), so this does not block the core value proposition, but it does mean SC#2's literal claim of "identically to image jobs" is not fully true today.

This looks like an unintentional oversight (confirmed unfixed, self-flagged defect) rather than an accepted deviation, so no override is suggested. Recommend either: (a) a small gap-closure plan extending `convert.MIMEType` per the fix already specified in 11-REVIEW.md WR-01, or (b) an explicit, developer-signed override if the team decides Content-Type parity is out of scope for v1.2 given internal-service-only clients (per CLAUDE.md's "only internal services" constraint, which may reduce the practical severity of a wrong browser MIME type).

---

_Initial verification: 2026-07-10 — Claude (gsd-verifier)_
_Re-verification after gap closure: 2026-07-10 — Claude (gsd-verifier)_
