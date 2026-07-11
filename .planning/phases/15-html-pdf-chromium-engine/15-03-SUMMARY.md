---
phase: 15-html-pdf-chromium-engine
plan: 03
subsystem: api

# Dependency graph
requires:
  - phase: 15-html-pdf-chromium-engine plan 01
    provides: "convert.EngineHTML const + htm->html NormalizeFormat alias, queue/producer/reconciler plumbing for the html engine class"
  - phase: 15-html-pdf-chromium-engine plan 02
    provides: "convert.LooksLikeHTML, convert.ParseHTMLOpts/HTMLOptsFromMap/ValidateHTMLApplicability, ChromiumConverter registered in convert.Default"
provides:
  - "internal/worker/worker.go HandleHTMLConvert -- full html job orchestration (parse -> strict opts re-parse -> MarkActive -> process -> MarkDone/MarkFailed -> best-effort webhook)"
  - "internal/worker/worker.go isHTMLTerminal + terminalChromiumSignatures -- terminal-classified HTML_ENGINE_TIMEOUT, live-capture-ready signature list"
  - "internal/api/handlers.go HTML content-detection branch (fail-closed, pre-storage), engine-keyed opts dispatch, EngineHTML routing case"
  - "internal/convert/sniff.go MIMEType html arm (text/html)"
affects: ["15-html-pdf-chromium-engine plan 04 (container/binary + live chromium smoke checklist, finalizes terminalChromiumSignatures from real stderr)", "plan 05 (container/compose + e2e)"]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "HandleHTMLConvert is a mechanical mirror of HandleDocumentConvert: strict re-parse of persisted opts -> MarkFailed-before-SkipRetry on corruption (D-10/T-14-02b discipline) -> MarkActive -> engine-agnostic process() -> engine-scoped terminal classifier -> MarkDone/MarkFailed with QueueHTML-tagged metrics and best-effort webhook enqueue"
    - "isHTMLTerminal mirrors isDocumentTerminal's DOC-08 timeout-is-terminal divergence: a wrapped context.DeadlineExceeded is terminal, everything else delegates to the shared isTerminal"
    - "API opts dispatch is now engine-keyed (switch on engine) rather than a single unconditional ParseDocOpts call -- HTMLOpts is a structurally different closed type, so EngineHTML gets its own ParseHTMLOpts/ValidateHTMLApplicability branch while every other engine keeps the existing ParseDocOpts/ValidateApplicability path"
    - "HTML content detection has no magic bytes (unlike ZIP/OLE-CFB): the sniff-chain branch is gated on the (still-untrusted) declared source already claiming html POST NormalizeFormat's htm->html alias, PLUS convert.LooksLikeHTML's fail-closed structural content check"

key-files:
  created: []
  modified:
    - internal/worker/worker.go
    - internal/worker/worker_test.go
    - internal/api/handlers.go
    - internal/api/handlers_test.go
    - internal/convert/sniff.go
    - internal/convert/sniff_test.go

key-decisions:
  - "terminalChromiumSignatures is seeded ONLY with the two validatePDF-reused strings that genuinely carry over ('output is empty', 'output missing %pdf- magic bytes') plus an explicit TODO(plan-04) comment -- no chromium-specific stderr text is guessed, per the project's established live-capture-only convention (mirrors terminalVipsSignatures's 'Verified live-tested' discipline) and RESEARCH.md's Open Question 2."
  - "The .html-content-check-failure case does NOT get its own dedicated log reason/422 message; it intentionally falls through to the existing generic 'reason=unrecognized_content' 422, mirroring the established DuplicateRootPart precedent (SniffContainer's own comment: 'leaves detected empty intentionally -- fail closed to the unrecognized-content rejection below') rather than adding a parallel reason=not_html log line."

requirements-completed: [HTML-01, HTML-02, HTML-03]

# Metrics
duration: 28min
completed: 2026-07-11
---

# Phase 15 Plan 03: HTML Engine Go-Layer Wiring (Worker + API) Summary

**HandleHTMLConvert + isHTMLTerminal wired into the worker (terminal-classified HTML_ENGINE_TIMEOUT, mirroring HandleDocumentConvert exactly), and handleCreateJob now fail-closed-detects HTML content, validates+persists print opts via an engine-keyed dispatch, and routes html jobs to their own asynq queue -- the entire HTML-01/02/03 Go code path is now present, pending only the chromium-worker container (Plan 04).**

## Performance

- **Duration:** 28 min
- **Started:** 2026-07-11T14:15:00Z
- **Completed:** 2026-07-11T14:43:00Z
- **Tasks:** 2
- **Files modified:** 6 (0 created, 6 modified)

## Accomplishments
- `HandleHTMLConvert` (`internal/worker/worker.go`) orchestrates a full html conversion job end-to-end: unparseable-payload SkipRetry, strict re-parse of persisted opts via `convert.HTMLOptsFromMap` with MarkFailed-before-SkipRetry on corruption (D-10, T-14-02b discipline), MarkActive, the engine-agnostic `h.process` (byte-for-byte unchanged -- confirmed via `git diff` showing no edits inside `process()`), an `isHTMLTerminal`-gated MarkFailed/metrics/webhook-enqueue branch on failure, and MarkDone/webhook-enqueue on success.
- `isHTMLTerminal` mirrors `isDocumentTerminal`'s DOC-08 divergence exactly: a wrapped `context.DeadlineExceeded` (an `HTML_ENGINE_TIMEOUT` expiry) is classified terminal so a stuck chromium render never retries to exhaustion; every other error delegates to the shared `isTerminal`.
- `terminalChromiumSignatures` seeded with only the two `validatePDF`-reused strings that genuinely carry over today, plus an explicit `TODO(plan-04)` marking the live-capture requirement -- the shared `isTerminal`'s signature loop now also checks this list.
- `handleCreateJob` (`internal/api/handlers.go`) gained a fail-closed HTML content-detection branch (`source == "html" && convert.LooksLikeHTML(...)`) placed before the OLE-CFB branch in the sniff chain, so a `.html`-named file with non-HTML content falls through to the existing generic 422 before any storage write (T-15-09 mitigated).
- The opts-validation block is now engine-keyed: `EngineHTML` routes through `convert.ParseHTMLOpts` + `convert.ValidateHTMLApplicability`, every other engine keeps the existing `ParseDocOpts`/`ValidateApplicability` path -- same size cap and normalize-before-persist round-trip for both branches (T-15-10 mitigated).
- The engine-routing switch gained `case convert.EngineHTML: enqueueErr = s.queue.EnqueueHTMLConvert(...)`; the fail-closed `default:` branch is unchanged.
- `internal/convert/sniff.go`'s `MIMEType` gained a `"html" -> "text/html"` arm so a stored html input carries the correct Content-Type (pdf output Content-Type was already covered).

## Task Commits

Each task was committed atomically:

1. **Task 1: worker.go -- HandleHTMLConvert + isHTMLTerminal + terminalChromiumSignatures** - `14464ed` (feat)
2. **Task 2: handlers.go -- HTML sniff branch + engine-keyed opts dispatch + routing; sniff.go MIMEType** - `60e7052` (feat)

**Plan metadata:** (this SUMMARY.md commit, made by the caller)

## Files Created/Modified
- `internal/worker/worker.go` - `HandleHTMLConvert`, `isHTMLTerminal`, `terminalChromiumSignatures`, extended `isTerminal`'s signature loop
- `internal/worker/worker_test.go` - `TestIsTerminalChromiumSignatures`, `TestIsHTMLTerminal`
- `internal/api/handlers.go` - HTML detection branch in the sniff chain, engine-keyed opts dispatch (`switch engine { case convert.EngineHTML: ... default: ... }`), `case convert.EngineHTML` in the routing switch
- `internal/api/handlers_test.go` - 7 new tests: html detected+accepted+routed, `.htm` alias accepted, non-HTML-under-`.html` rejected pre-storage, valid html opts persisted+echoed, unknown-field opts rejected, out-of-range `margin_mm` rejected, a document-only opt (`pdf_profile`) on an html job rejected
- `internal/convert/sniff.go` - `MIMEType` gains a `"html"` arm
- `internal/convert/sniff_test.go` - `TestMIMEType` gains an `"html": "text/html"` case

## Decisions Made
- `terminalChromiumSignatures` stays minimal (2 carried-over strings) rather than guessing chromium-specific stderr text, per the project's live-capture-only discipline for terminal-signature lists; the exact list finalizes in Plan 04 against real `chromium-headless-shell` stderr output.
- The HTML-content-check-failure branch deliberately does not add its own dedicated log reason -- it falls through to the existing generic `reason=unrecognized_content` 422, following the same precedent `SniffContainer`'s `DuplicateRootPart` case already establishes (leave `detected` empty, let the shared fail-closed branch below handle logging/response).

## Deviations from Plan

None - plan executed exactly as written. Both tasks' `<action>` and `<verify>` blocks were followed literally; the plan's own acceptance criteria (grep checks, `go build`/`go vet`/`go test` passes, `h.process` unchanged) all passed without needing any Rule 1-4 fix.

## Issues Encountered
None.

## User Setup Required
None - no external service configuration required. This plan is pure Go code across `internal/worker`, `internal/api`, `internal/convert`; no new env vars, no new dependencies (`go.mod`/`go.sum` unchanged).

## Next Phase Readiness
- The entire HTML-01/02/03 Go code path is now present and tested: a running `api` process can accept an html upload, fail-closed-detect its content, validate+persist print opts, and route the job to the `html` asynq queue; a (Plan 04) `chromium-worker` process registering `worker.NewHandler(...).HandleHTMLConvert` on `queue.TypeHTMLConvert` would immediately be able to dequeue and process it -- `h.process`'s engine-agnostic `registry.Lookup` already resolves `("html","pdf")` to the `ChromiumConverter` registered in Plan 02.
- No container/binary exists yet (`cmd/chromium-worker`, `Dockerfile.chromium-worker`) -- an `engine="html"` job can be created, validated, and enqueued, but nothing will dequeue/process it until Plan 04 lands the worker entry point and container image.
- `terminalChromiumSignatures` is intentionally minimal; Plan 04's live chromium smoke checklist (RESEARCH.md's 8-item list) is the explicit next step to finalize this list from real stderr output before treating HTML-01's terminal-classification coverage as complete.
- Live confirmation of every RESEARCH.md open question (JS-disabled honoring, CSS `@page`/`print-color-adjust` honoring, network-block flag efficacy, `file://` residual risk, tini necessity) remains explicitly deferred to Plan 04/05 per this plan's own scope boundary (Go-layer wiring only, no container).

---
*Phase: 15-html-pdf-chromium-engine*
*Completed: 2026-07-11*
