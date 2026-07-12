---
phase: 15-html-pdf-chromium-engine
verified: 2026-07-11T20:15:00Z
status: passed
score: 4/4 roadmap success criteria verified (18/18 merged must-haves verified)
overrides_applied: 0
---

# Phase 15: HTML→PDF Chromium Engine Verification Report

**Phase Goal:** HTML-файлы конвертируются в PDF через новый, полностью изолированный от сети (офлайн-рендеринг) третий engine-class, следующий паттерну engine-class из v1.2.
**Verified:** 2026-07-11
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths (ROADMAP Success Criteria — the contract)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | `jobs.engine` CHECK accepts `html` (migration applied); HTML upload routes to its own queue/worker/container distinct from image+document — live e2e verified end-to-end | ✓ VERIFIED | `internal/db/migrations/0005_html_engine.sql` adds `'html'` to the CHECK list; `internal/convert/convert.go:EngineHTML`, `internal/queue/queue.go:TypeHTMLConvert/QueueHTML`, `internal/queue/client.go:EnqueueHTMLConvert`, `internal/api/handlers.go:368-369` (`case convert.EngineHTML: s.queue.EnqueueHTMLConvert`), `internal/reconciler/reconciler.go:139-140`, `cmd/chromium-worker/main.go` (dedicated binary, `mux.HandleFunc(queue.TypeHTMLConvert, h.HandleHTMLConvert)`), `Dockerfile.chromium-worker` + `docker-compose.yml:201-216` (dedicated `chromium-worker` service). Live e2e: 15-05-SUMMARY.md `TestHTMLConversionE2E/HappyPath` PASS + Postgres `engine='html'` confirmed for 18 done jobs + `.htm` alias confirmed end-to-end; checkpoint APPROVED by user 2026-07-11. |
| 2 | HTML referencing an external network resource results in NO network fetch during conversion — proven by a live canary test | ✓ VERIFIED | `internal/convert/chromium.go:165-176` argv unconditionally carries `--proxy-server=127.0.0.1:9`, `--proxy-bypass-list=<-loopback>`, `--host-resolver-rules=MAP * ~NOTFOUND`, `--no-pdf-header-footer` — asserted by regression test `TestChromiumArgvContainsRequiredFlags` (chromium_test.go:155). Live proof: 15-05-SUMMARY.md `TestHTMLNetworkBlockE2E` — zero canary hits across external IP / loopback / compose-hostname / `file://` vectors while the job reached `done`; checkpoint APPROVED by user 2026-07-11. |
| 3 | Client sets page size/margins/printBackground via the Phase 14 validated-opts mechanism; resulting PDF reflects the options | ✓ VERIFIED | `internal/convert/htmlopts.go` — `HTMLOpts`/`ParseHTMLOpts` (closed enum + range, `DisallowUnknownFields`, strict-object check reused from Phase 14's `checkStrictObject`) / `HTMLOptsFromMap` / `ValidateHTMLApplicability` / `buildPrintCSS` (server-constant CSS only, never client bytes); wired at `internal/api/handlers.go:276-291` (API write path) and `internal/convert/chromium.go:127,145` (worker read path, same strictness both sides — D-10 parity). Live proof: 15-05-SUMMARY.md `TestHTMLConversionE2E/PrintOptsPageSize` — `/MediaBox` widths differ a4=594.96pt vs a5=420pt (real chromium renderer); `PrintOptsBackground` — differing byte-count signal between true/false variants; checkpoint APPROVED. |
| 4 | HTML→PDF bounded by its own engine timeout, classified terminal on expiry, runs in its own dedicated worker binary/container | ✓ VERIFIED | `internal/worker/worker.go:186` `isHTMLTerminal` (DeadlineExceeded → terminal, else `isTerminal(err)` incl. `terminalChromiumSignatures`); `HandleHTMLConvert` (worker.go:397) calls it at line 441; `HTML_ENGINE_TIMEOUT` env read in both `internal/queue/client.go:72` and `cmd/chromium-worker/main.go:69`; dedicated binary/container confirmed under Truth 1. Live proof: 15-05-SUMMARY.md — `HTML_ENGINE_TIMEOUT=50ms` override, job failed ~1s, exactly one `active→failed` transition (no retry storm), `engine_stderr: "chromium-headless-shell killed: context deadline exceeded"`; checkpoint APPROVED by user 2026-07-11. |

**Score:** 4/4 roadmap success criteria verified.

### Merged Plan-Level Must-Haves (18 total, all VERIFIED)

| # | Truth (from PLAN frontmatter) | Status | Evidence |
|---|---|---|---|
| 1 | `jobs.engine` CHECK accepts `html` | ✓ VERIFIED | `0005_html_engine.sql:11-12` |
| 2 | `EngineHTML` is single source of truth, no raw literal elsewhere | ✓ VERIFIED | `convert.go:20 EngineHTML = "html"`; all call sites use the const |
| 3 | `.htm` → `html` alias | ✓ VERIFIED | `convert.go:53-54 case "htm": return "html"`; live-confirmed `report.htm` routing (15-05-SUMMARY) |
| 4 | API/queue/reconciler can enqueue html job onto its own queue | ✓ VERIFIED | handlers.go:369, client.go:127-133, queue.go:103-121 |
| 5 | Binary/non-UTF8 `.html` upload detected NOT-html (fail-closed) | ✓ VERIFIED | `htmlsniff.go:43-86` `LooksLikeHTML`; `TestLooksLikeHTML` + 3 edge-case tests pass |
| 6 | Print opts validated against closed allow-list/range | ✓ VERIFIED | `htmlopts.go:53-72 ParseHTMLOpts`; `TestParseHTMLOpts` passes |
| 7 | Print opts become server-constant CSS, no client bytes, no CLI flag | ✓ VERIFIED | `htmlopts.go:123-149 buildPrintCSS` selects only from validated enum/range |
| 8 | ChromiumConverter invokes chromium-headless-shell one-shot with layered network-block flags + `file://` on rendered copy, reuses `validatePDF` | ✓ VERIFIED | `chromium.go:120-193` |
| 9 | Network isolation via CLI flags only, no CDP/chromedp, no new Go dep | ✓ VERIFIED | `go.mod` unchanged for this phase (no chromedp dep); `runCommand` (exec.go) reused |
| 10 | `HandleHTMLConvert` orchestrates job lifecycle mirroring `HandleDocumentConvert` | ✓ VERIFIED | `worker.go:397-466` |
| 11 | `HTML_ENGINE_TIMEOUT` expiry classified terminal | ✓ VERIFIED | `worker.go:186 isHTMLTerminal`; live-verified (Truth 4 above) |
| 12 | `handleCreateJob` detects HTML fail-closed before S3 write, dispatches opts by engine, routes to html queue | ✓ VERIFIED | `handlers.go:164-174` (sniff before storage), `:276-291` (opts dispatch), `:368-369` (routing) |
| 13 | Non-HTML file uploaded as `.html` rejected 422 pre-storage | ✓ VERIFIED | `LooksLikeHTML` gate at handlers.go:164; live: `TestHTMLContentRejectionE2E` PASS (15-05-SUMMARY) |
| 14 | html→pdf download served with correct MIME | ✓ VERIFIED | `sniff.go:129 return "text/html"`; pdf MIME already covered pre-existing |
| 15 | Dedicated chromium-worker binary/container, distinct from image/document | ✓ VERIFIED | `cmd/chromium-worker/main.go`, `Dockerfile.chromium-worker`, `docker-compose.yml:201-216` |
| 16 | Container: chromium-headless-shell + tini + Latin fonts, `USER nobody` | ✓ VERIFIED | `Dockerfile.chromium-worker:12-26` |
| 17 | 8-item Verify-Live Smoke Checklist run against real binary | ✓ VERIFIED | 15-04-SUMMARY.md — full checklist results recorded; checkpoint APPROVED by user 2026-07-11 |
| 18 | Live E2E acceptance: end-to-end + network-block canary + print-opts round-trip + timeout-terminal + non-HTML 422 | ✓ VERIFIED | 15-05-SUMMARY.md — all 5 items PASS; checkpoint APPROVED by user 2026-07-11 |

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/db/migrations/0005_html_engine.sql` | CHECK migration adding `'html'` | ✓ VERIFIED | Present, contains `'html'` in the allow-list |
| `internal/convert/convert.go` | `EngineHTML` const + `.htm` alias | ✓ VERIFIED | `EngineHTML = "html"` (line 20), alias (line 53-54) |
| `internal/convert/chromium.go` | `ChromiumConverter{Pairs,Convert,Engine}` + CSS/CSP injection + network-block argv | ✓ VERIFIED | Full implementation, all flags present, `injectCSPFirst` fixes CR-01 |
| `internal/convert/htmlopts.go` | `HTMLOpts`/`ParseHTMLOpts`/`HTMLOptsFromMap`/`ValidateHTMLApplicability`/`buildPrintCSS` | ✓ VERIFIED | All present and substantive |
| `internal/convert/htmlsniff.go` | `LooksLikeHTML(r io.ReaderAt, size int64) bool` | ✓ VERIFIED | Fail-closed, bounded read, BOM/whitespace handling |
| `internal/convert/converters.go` | `ChromiumConverter` registered | ✓ VERIFIED | `Default.Register(ChromiumConverter{})` line 8 |
| `internal/queue/queue.go` | `TypeHTMLConvert`, `QueueHTML`, `NewHTMLConvertTask`, retry/TTL funcs | ✓ VERIFIED | All present |
| `internal/queue/client.go` | `EnqueueHTMLConvert` producer | ✓ VERIFIED | Present, wired |
| `internal/reconciler/reconciler.go` | engine switch case `convert.EngineHTML` | ✓ VERIFIED | Line 139-140 |
| `internal/worker/worker.go` | `HandleHTMLConvert` + `isHTMLTerminal` + `terminalChromiumSignatures` | ✓ VERIFIED | All present, wired into shared `isTerminal` |
| `internal/api/handlers.go` | HTML sniff branch + opts dispatch + routing case | ✓ VERIFIED | All present |
| `internal/convert/sniff.go` | `MIMEType` html arm | ✓ VERIFIED | `text/html` at line 129 |
| `cmd/chromium-worker/main.go` | Dedicated entry point binding `TypeHTMLConvert` on `QueueHTML` | ✓ VERIFIED | Present, no reconciler sweeper (avoids double-sweep), own metrics listener |
| `Dockerfile.chromium-worker` | chromium-headless-shell + tini runtime, `USER nobody` | ✓ VERIFIED | Present |
| `docker-compose.yml` | `chromium-worker` service | ✓ VERIFIED | Lines 201-216 |
| `docker-compose.e2e.yml` | `chromium-worker` extra_hosts for canary reachability | ✓ VERIFIED | Line 40-44 |
| `internal/e2e/e2e_test.go` | `TestHTMLConversionE2E`, `TestHTMLNetworkBlockE2E`, `TestHTMLContentRejectionE2E`, `startCanaryReceiver` | ✓ VERIFIED | All present, builds cleanly |
| `internal/e2e/testdata/{sample,nothtml,canary}.html` | E2E fixtures | ✓ VERIFIED | All present on disk |

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| `internal/convert/chromium.go Convert` | `buildPrintCSS` + `rendered.html` | injection into worker-built copy | ✓ WIRED | `chromium.go:144-149` |
| `internal/api/handlers.go` opts dispatch | `ParseHTMLOpts` / `ValidateHTMLApplicability` | `case convert.EngineHTML` | ✓ WIRED | `handlers.go:276-291` |
| `internal/api/handlers.go` engine switch | `EnqueueHTMLConvert` | `case convert.EngineHTML` | ✓ WIRED | `handlers.go:368-369` |
| `cmd/chromium-worker/main.go` | `queue.TypeHTMLConvert` / `queue.QueueHTML` | `mux.HandleFunc` + `asynq.Config.Queues` | ✓ WIRED | `main.go:83,87` |
| `TestHTMLNetworkBlockE2E` | `startCanaryReceiver` | zero-hits assertion + job completes | ✓ WIRED (live) | 15-05-SUMMARY.md — PASS, checkpoint approved |
| `TestHTMLConversionE2E` | `assertDownloadIsPDF` / `mediaBoxWidth` | magic-bytes + structural PDF check | ✓ WIRED (live) | 15-05-SUMMARY.md — PASS, checkpoint approved |

### Data-Flow Trace (Level 4)

Not applicable in the UI-rendering sense (this is a backend conversion pipeline, not a data-rendering component). The equivalent trace — client opts → validated `HTMLOpts` → server-constant CSS/CSP → chromium argv → PDF output — was followed end-to-end above (Truths 2-3, 7-9) and confirmed both statically (code) and live (structural `/MediaBox` diff, byte-count diff, zero canary hits).

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| `go build ./...` | full module build | success, no errors | ✓ PASS |
| `go vet ./...` | static analysis | clean, exit 0 | ✓ PASS |
| `gofmt -l` on all phase-15 Go files | formatting check | no output (all formatted) | ✓ PASS |
| `go test ./internal/convert/... ./internal/queue/... ./internal/worker/... ./internal/api/...` | unit test suite | all packages `ok` | ✓ PASS |
| `go build ./internal/e2e/...` | E2E test package compiles | success | ✓ PASS |
| `TestInjectCSPFirstPrecedesInHeadScript` (CR-01 regression) | `go test -run TestInjectCSPFirst ./internal/convert/` | PASS | ✓ PASS |
| `TestChromiumArgvContainsRequiredFlags` (network-block flags) | `go test -run TestChromiumArgvContainsRequiredFlags ./internal/convert/` | PASS | ✓ PASS |

Full live docker-compose e2e run was NOT re-executed by this verifier per task instructions — the recorded, human-approved evidence in 15-04-SUMMARY.md and 15-05-SUMMARY.md is treated as the live-verification source of truth.

### Probe Execution

No `scripts/*/tests/probe-*.sh` convention used by this project/phase. SKIPPED — no probe scripts declared in PLAN/SUMMARY files or found under `scripts/`.

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|---|---|---|---|---|
| HTML-01 | 15-01, 15-03, 15-04, 15-05 | Отдельный chromium-based движок: своя очередь, свой воркер-бинарник/контейнер, свой таймаут с terminal-классификацией; требует миграции CHECK-констрейнта | ✓ SATISFIED | Migration + `EngineHTML`/queue/worker/container all present and wired; live e2e PASS + checkpoint approved (SC1) |
| HTML-02 | 15-02, 15-03, 15-04, 15-05 | Офлайн-рендеринг: движок не фетчит внешние сетевые ресурсы; нет режима URL-fetch на входе | ✓ SATISFIED | Network-block argv unconditional + regression test; no URL-fetch input path found anywhere in `handlers.go`/`chromium.go`/`htmlopts.go` (grep confirms no `source_url`/`fetch_url`/`url_fetch` construct exists); live canary test zero-hits + checkpoint approved (SC2) |
| HTML-03 | 15-02, 15-03, 15-05 | Print-опции (размер, поля, printBackground) через тот же validated-opts механизм, что OPTS-01 | ✓ SATISFIED | `HTMLOpts`/`ParseHTMLOpts` mirrors Phase 14's `DocOpts`/`checkStrictObject` discipline; live PDF-geometry diff + checkpoint approved (SC3) |

**Note:** `.planning/REQUIREMENTS.md` still shows HTML-01/02/03 as unchecked `[ ]` / "Pending" in its coverage table — this is expected bookkeeping (orchestrator updates REQUIREMENTS.md/ROADMAP.md/STATE.md after verification passes, per 15-05-SUMMARY.md's own "Next Phase Readiness" note), not a gap in the implementation.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| — | — | No `TBD`/`FIXME`/`XXX`/`TODO`/`HACK`/`PLACEHOLDER` markers found in any phase-15 file | — | none |
| `internal/convert/htmlopts.go` | 123-147 | `buildPrintCSS` forces `margin: 0mm !important` and `size: A4 !important` on every render including no-opts jobs (review WR-02, deferred as CR-03) | ℹ️ Info (advisory, non-blocking per task scope) | No-opts HTML→PDF jobs render edge-to-edge by default rather than a browser-like ~10mm margin; documented as an intentional fast-follow in `15-REVIEW.md` (CR-03) because reworking it now would regress the already live-verified, user-approved SC3 output. Not re-litigated here per verification task instructions. |
| `15-REVIEW.md` | frontmatter vs body | Frontmatter labels the `validatePDF` wrap fix and the forced-margin finding `CR-02`/`CR-03`; the review body labels the same two findings `WR-01`/`WR-02`. Cosmetic label mismatch between frontmatter and prose in the review document itself. | ℹ️ Info | Documentation-only inconsistency inside `15-REVIEW.md`; does not affect the code fix (confirmed present in `chromium.go:190-192`) or the finding's substance. |

CR-01 (BLOCKER, JS-disable CSP injected too late to precede head-level scripts) was found by code review and is CONFIRMED FIXED: `injectCSPFirst` (`chromium.go:88-102`) now places the CSP as the first child of `<head>`, called before `injectPrintCSS` in `Convert` (`chromium.go:144-145`), with two regression tests (`TestInjectCSPFirstPrecedesInHeadScript`, `TestInjectCSPFirstNoHeadFallsBackToHTMLOpen`) — both passing.

### Human Verification Required

None outstanding. Both `checkpoint:human-verify` gates for this phase (Plan 04's live smoke checklist, Plan 05's live e2e acceptance) were already run and APPROVED by the user on 2026-07-11, per the recorded evidence in `15-04-SUMMARY.md` and `15-05-SUMMARY.md`. No new behavior requiring human judgment was introduced after those approvals (CR-01/CR-02 fixes are deterministic code changes covered by unit-level regression tests, not new live-verified behavior).

### Gaps Summary

None. All four ROADMAP success criteria and all 18 merged plan-level must-haves are VERIFIED against the actual codebase, backed by passing unit/regression tests and recorded, human-approved live e2e evidence. The one BLOCKER identified by code review (CR-01, JS-disable CSP bypass) has been fixed in code with regression-test coverage. The one deferred WARNING (WR-02/CR-03, forced margin:0 default) is explicitly out of scope for this verification per task instructions and is tracked as a documented fast-follow in `15-REVIEW.md`.

---

_Verified: 2026-07-11_
_Verifier: Claude (gsd-verifier)_
