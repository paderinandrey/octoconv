---
phase: 15-html-pdf-chromium-engine
plan: 02
subsystem: api

# Dependency graph
requires:
  - phase: 15-html-pdf-chromium-engine plan 01
    provides: "convert.EngineHTML const + htm->html NormalizeFormat alias, queue/producer/reconciler plumbing for the html engine class"
provides:
  - "convert.LooksLikeHTML(r io.ReaderAt, size int64) bool -- fail-closed HTML content sniff (D-07), for the API's sniff-chain branch (Plan 03)"
  - "convert.HTMLOpts + ParseHTMLOpts + HTMLOptsFromMap + ValidateHTMLApplicability -- closed, strictly-validated print-opts struct (D-06/D-10)"
  - "convert.buildPrintCSS(HTMLOpts) string -- server-constant CSS @page/print-color-adjust block, selected only by validated enum values"
  - "convert.ChromiumConverter{Pairs,Convert,Engine} -- registered in convert.Default, EngineFor(\"html\",\"pdf\") now resolves to EngineHTML"
affects: ["15-html-pdf-chromium-engine plan 03 (API sniff-chain branch + opts dispatch + worker wiring)", "plan 04 (live chromium smoke checklist against the exact argv/CSS mechanism built here)", "plan 05 (container/compose + e2e)"]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Print options as CSS injection, not CLI flags (RESEARCH Pattern 1 correction of D-06): chromium-headless-shell's one-shot --print-to-pdf mode has no page-geometry CLI switches, so the validated-opts -> server-constant mechanism from Phase 14 (PDFAFilterOptions) is reused for CSS text instead of an argv suffix"
    - "Worker-built rendered copy, original never mutated: chromium.go writes workDir/rendered.html with the injected <style> block, mirroring LibreOfficeConverter's never-touch-inPath discipline"
    - "Layered network-block argv (dead proxy + loopback-bypass removal + NXDOMAIN resolver) assembled as a compile-time string slice, verify-live-confirmed in Plan 04"

key-files:
  created:
    - internal/convert/htmlsniff.go
    - internal/convert/htmlsniff_test.go
    - internal/convert/htmlopts.go
    - internal/convert/htmlopts_test.go
    - internal/convert/chromium.go
    - internal/convert/chromium_test.go
  modified:
    - internal/convert/converters.go

key-decisions:
  - "buildPrintCSS defaults to the 'a4' CSS constant when HTMLOpts.PageSize is empty (client did not request a page_size), rather than emitting an invalid '@page { size: ; ... }' rule -- Convert() always injects the CSS block regardless of whether opts were supplied, so a valid default was required (Rule 1 auto-fix; RESEARCH's example body did not spell out this empty-input case)."
  - "ValidateHTMLApplicability is its own function scoped to EngineHTML, not merged into opts.go's shared ValidateApplicability, per RESEARCH.md's explicit instruction (HTMLOpts is a structurally different closed type from DocOpts)."
  - "injectPrintCSS is factored out as a standalone, chromium-free function so the CSS-injection placement logic is unit-testable without launching a live chromium binary (per the plan's own acceptance criteria)."

requirements-completed: [HTML-02, HTML-03]

# Metrics
duration: 32min
completed: 2026-07-11
---

# Phase 15 Plan 02: ChromiumConverter (CSS Injection + Network-Block Argv) Summary

**Registered `{html→pdf}` ChromiumConverter with server-constant CSS `@page` print-option injection (never CLI flags) and layered network-block chromium argv, backed by a fail-closed HTML content sniff and a closed-strict print-opts struct mirroring Phase 14's PDFAFilterOptions invariant.**

## Performance

- **Duration:** 32 min
- **Started:** 2026-07-11T13:40:00Z
- **Completed:** 2026-07-11T14:12:00Z
- **Tasks:** 3
- **Files modified:** 7 (6 created, 1 modified)

## Accomplishments
- `LooksLikeHTML` (D-07): a bounded (1 MiB), fail-closed UTF-8/NUL/marker content check distinguishing genuine HTML text from binary or unrelated content masquerading as `.html` -- no full HTML-parser dependency, mirroring `SniffContainer`'s bounded-inspection discipline.
- `HTMLOpts`/`ParseHTMLOpts`/`HTMLOptsFromMap`/`ValidateHTMLApplicability` (D-06/D-10): a closed, strictly-parsed print-options struct (`page_size`, `margin_mm`, `landscape`, `print_background`) reusing `opts.go`'s shared `checkStrictObject` verbatim, with the same `DisallowUnknownFields` + allow-list/range strictness as `DocOpts`.
- `buildPrintCSS` (RESEARCH.md Pattern 1 -- the phase's critical D-06 mechanism correction): a server-constant `<style>@page{...}</style>` block selected only from already-validated `HTMLOpts` fields via a fixed `htmlPageSizeCSS` map -- no chromium CLI flag carries any print option, since none exist for page geometry in one-shot `--print-to-pdf` mode. An injection unit test proves attacker-controlled `page_size` text can never reach the returned CSS.
- `ChromiumConverter{Pairs,Convert,Engine}`: registered in `convert.Default`; `Convert` builds a worker-only `rendered.html` copy (CSS spliced before `</head>`, or after the opening `<html>` tag as fallback, or prepended as a last resort) and invokes `chromium-headless-shell` with the exact research-verified argv (JS disabled + the three layered network-block flags: dead proxy, loopback-bypass removal, NXDOMAIN resolver), reusing `validatePDF` verbatim from `libreoffice.go`.

## Task Commits

Each task was committed atomically:

1. **Task 1: htmlsniff.go -- LooksLikeHTML fail-closed content check** - `5044a9c` (feat)
2. **Task 2: htmlopts.go -- closed validated print-opts + server-constant CSS builder** - `9dfe575` (feat)
3. **Task 3: chromium.go -- ChromiumConverter (CSS injection + network-block argv) + registration** - `ade3134` (feat)

**Plan metadata:** (this SUMMARY.md commit, made by the caller)

## Files Created/Modified
- `internal/convert/htmlsniff.go` - `LooksLikeHTML(r io.ReaderAt, size int64) bool`, bounded UTF-8/NUL/marker fail-closed HTML detection
- `internal/convert/htmlsniff_test.go` - table-driven coverage of every `<behavior>` case plus short-read/oversized-input safety
- `internal/convert/htmlopts.go` - `HTMLOpts`, `ParseHTMLOpts`, `HTMLOptsFromMap`, `ValidateHTMLApplicability`, `htmlPageSizeCSS`, `buildPrintCSS`
- `internal/convert/htmlopts_test.go` - strictness, applicability, and CSS-injection-resistance coverage
- `internal/convert/chromium.go` - `ChromiumConverter`, `injectPrintCSS`/`spliceAt` (testable injection helper), `Convert`/`Pairs`/`Engine`
- `internal/convert/chromium_test.go` - Pairs/Engine/registration assertions, injection-placement tests (all 4 marker scenarios), argv-flag presence, no-`validatePDF`-reimplementation guard
- `internal/convert/converters.go` - `Default.Register(ChromiumConverter{})` added to `init()`

## Decisions Made
- `buildPrintCSS` falls back to the `a4` CSS constant when `PageSize` is empty, since `Convert()` unconditionally injects the CSS block regardless of whether the client requested any print options -- an empty `size: ;` rule would be invalid CSS. This preserves the "server-constant only" invariant (the fallback is still a fixed compile-time constant, never client bytes).
- `ValidateHTMLApplicability` kept as its own function, not merged into `opts.go`'s shared `ValidateApplicability`, per RESEARCH.md's explicit guidance.
- `injectPrintCSS` factored out of `Convert` as a standalone function so CSS-placement logic (all 4 marker scenarios: `</head>` present/absent, case sensitivity, no-marker fallback) is unit-testable without a live chromium binary, satisfying the plan's own acceptance criterion.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] buildPrintCSS default page size when page_size is unset**
- **Found during:** Task 2 (htmlopts.go)
- **Issue:** RESEARCH.md's example `buildPrintCSS` body does `size := htmlPageSizeCSS[o.PageSize]` with no fallback; when `o.PageSize` is empty (client did not request `page_size`), the map lookup yields `""`, producing the invalid CSS rule `@page { size: ; margin: 0mm !important; }`. Since `Convert()` always injects this CSS block regardless of whether the client supplied any opts, this would silently break every default (no-opts) html→pdf conversion's page geometry.
- **Fix:** Added a one-line fallback (`if size == "" { size = htmlPageSizeCSS["a4"] }`) so the emitted CSS is always syntactically valid, while preserving the "compile-time constant selected only by validated enum" invariant (the fallback is itself a fixed constant, never client-controlled).
- **Files modified:** `internal/convert/htmlopts.go`
- **Verification:** `TestBuildPrintCSS` subtests pass; no invalid-CSS regression possible since every test path now asserts a mapped constant is present.
- **Committed in:** `9dfe575` (Task 2 commit)

---

**Total deviations:** 1 auto-fixed (1 bug fix, Rule 1)
**Impact on plan:** Necessary correctness fix for the default (no-opts) conversion path; no scope creep, no new mechanism introduced beyond what RESEARCH.md's Pattern 1 already specified.

## Issues Encountered
- Initial `htmlsniff.go` doc comment literally mentioned `golang.org/x/net/html` as the rejected-parser reference, which broke the acceptance criterion's `grep -L "x/net/html"` absence check (the substring appeared in the comment even though no import exists). Reworded the comment to avoid the literal string while preserving the same "why no full parser" explanation -- not a deviation from the plan's intent, just a wording fix to satisfy the grep-based acceptance check literally.
- One hand-written test (`buildPrintCSS` injection-resistance case) initially asserted `!strings.Contains(css, "</style>")`, which always failed because the function's own legitimate wrapper markup contains that exact closing tag. Corrected the assertion to check for the actual injected-payload substrings (`<script>`, `alert(1)`) instead of the CSS wrapper's own markup.

## User Setup Required
None - no external service configuration required. This plan is pure Go code in `internal/convert`; no new env vars, no new dependencies (`go.mod`/`go.sum` unchanged).

## Next Phase Readiness
- All symbols Plan 03 needs are compiled and tested: `convert.LooksLikeHTML` (for the API's sniff-chain branch), `convert.ParseHTMLOpts`/`HTMLOptsFromMap`/`ValidateHTMLApplicability` (for the API's opts-dispatch branch and the worker's D-10 re-parse), and `ChromiumConverter` registered in `convert.Default` (so `EngineFor("html","pdf")` resolves and `internal/worker/worker.go`'s engine-agnostic `process()` needs zero changes to route html jobs once Plan 03 wires `HandleHTMLConvert`).
- No API routing, worker handler, container, or e2e test exists yet -- an `engine="html"` job still cannot be created end-to-end until Plan 03 lands the API sniff/opts/routing wiring and `HandleHTMLConvert`.
- Exact chromium flag behavior (`scriptEnabled` honoring, `@page` CSS honoring, `--proxy-bypass-list` necessity, tini necessity) remains explicitly a Plan 04 live-verification item, per the plan's own `<verification>` scope boundary -- nothing in this plan asserts live chromium behavior, only the Go-level argv/CSS assembly and injection-placement logic.

---
*Phase: 15-html-pdf-chromium-engine*
*Completed: 2026-07-11*

## Self-Check: PASSED

All 7 created/modified files confirmed present on disk; all three task commit hashes (5044a9c, 9dfe575, ade3134) confirmed present in git log.
