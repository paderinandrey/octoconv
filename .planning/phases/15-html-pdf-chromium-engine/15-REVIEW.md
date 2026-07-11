---
phase: 15-html-pdf-chromium-engine
reviewed: 2026-07-11T00:00:00Z
depth: standard
files_reviewed: 16
files_reviewed_list:
  - internal/convert/chromium.go
  - internal/convert/htmlopts.go
  - internal/convert/htmlsniff.go
  - internal/convert/convert.go
  - internal/convert/converters.go
  - internal/convert/sniff.go
  - internal/worker/worker.go
  - internal/api/handlers.go
  - internal/api/api.go
  - internal/queue/queue.go
  - internal/queue/client.go
  - internal/reconciler/reconciler.go
  - cmd/chromium-worker/main.go
  - Dockerfile.chromium-worker
  - internal/db/migrations/0005_html_engine.sql
  - internal/e2e/e2e_test.go
findings:
  critical: 1
  warning: 2
  info: 3
  total: 6
status: resolved
fix_commits:
  CR-01: b-fixed  # CSP injected as first head child (injectCSPFirst) + 2 regression tests
  CR-02: b-fixed  # chromium PDF-validation errors wrapped in chromium: context
  CR-03: deferred # forced margin:0 default — needs opts-presence design; changing it regresses approved live-verified SC3 output
---

# Phase 15: Code Review Report

> **Fix outcomes (2026-07-11):** CR-01 (BLOCKER) and CR-02 (WARNING) fixed in
> commit `dd`-range this phase — `injectCSPFirst` places the CSP as the first
> child of `<head>` so it precedes any in-head `<script>` (D-05 restored),
> with two regression tests; chromium PDF-validation failures now wrap in a
> `chromium:` context. CR-03 (WARNING, forced `margin:0 !important` default)
> is DEFERRED: distinguishing "margin unset" from "margin=0" needs an
> HTMLOpts presence-flag design change, and altering the injected margin CSS
> now would regress the print-opts output already live-verified and
> user-approved at the 15-05 acceptance checkpoint — tracked as a fast-follow.
> Info findings (IN-01..IN-03) left as-is (out of Critical+Warning fix scope).

**Reviewed:** 2026-07-11
**Depth:** standard
**Files Reviewed:** 16
**Status:** issues_found

## Summary

Reviewed the third engine class (chromium HTML→PDF): the converter and its
CSS/CSP injection, HTML opts parsing, fail-closed content sniffing, the worker
handler + terminal classification, queue wiring, reconciler routing, the new
command entrypoint, the Dockerfile, the engine-allow-list migration, and the
E2E suite.

The high-risk security surface holds up well on almost every axis the phase
flagged: all three network-block flags plus `--no-pdf-header-footer` are
present and unconditional (chromium.go:141-144); no client-supplied value
reaches the argv or the injected CSS/CSP (`buildPrintCSS` selects only from a
validated closed enum / range, and `MarginMM` is an already-range-checked
`int`); the `file://` input path is entirely worker-generated with no
client-derived component; `LooksLikeHTML` is genuinely fail-closed (rejects
NUL/non-UTF-8/marker-miss before any storage write); and the worker preserves
the Phase-14 discipline of `MarkFailed`-before-`SkipRetry` on corrupt persisted
opts, with `terminalChromiumSignatures` wired into the shared classifier.

The one exception is significant: the CSP `<meta>` that is designated the
load-bearing JS-disable is injected at a document position where it cannot
govern scripts that appear earlier in `<head>` — so the "JS disabled
unconditionally" invariant (D-05) is bypassable by attacker HTML that places an
inline script before the injection point. Blast radius is limited by the
process-level network isolation (which is the real exfiltration containment),
but the control itself does not do what it claims. Two lesser correctness/
diagnostic issues and three info items round out the review.

No `<structural_findings>` block was provided with this review, so there is no
structural substrate to reconcile against.

## Critical Issues

### CR-01: CSP `<meta>` injected before `</head>` does not disable head-level scripts (JS-disable bypass)

**File:** `internal/convert/chromium.go:63-75,116`
**Issue:**
`cspNoScriptMeta` (`script-src 'none'`) is the phase's stated load-bearing
JS-disable mechanism (D-05, replacing the non-working `--blink-settings` flag).
It is concatenated with the print CSS and handed to `injectPrintCSS`, which
inserts the whole block **immediately before the first `</head>`**
(chromium.go:66). A CSP delivered via `<meta>` only governs content that
appears *after* it in source order — scripts the parser has already encountered
have already executed under the prior (empty) policy. Because the meta is
placed at the *end* of `<head>`, any inline script that the attacker-controlled
HTML puts earlier in `<head>` runs before the policy is installed:

```html
<html><head>
  <script>/* executes: CSP not yet in effect */</script>
  <!-- injectPrintCSS inserts the CSP meta HERE, too late -->
</head>...
```

The live verification cited in the doc comment (`document.write` absent from
`--dump-dom`) does not exercise this case — a script in `<body>` (or after the
injection point) *is* correctly blocked, so the verification has a blind spot
for head-preceding scripts. The stated invariant "JS disabled unconditionally"
therefore does not hold. Exfiltration is still contained by the process-level
network-block flags (proxy + host-resolver), which is why this is not a
full data-exfil hole, but the JS-disable control itself is defective and
must not be relied on as written.

**Fix:** Inject the CSP meta as the **first child of `<head>`** (immediately
after the `<head ...>` open tag), before any other head content, so it precedes
all scripts. The print `<style>` can stay at end-of-head for cascade priority —
the two injections have different placement requirements and should not share a
single insertion point. Sketch:

```go
// Insert CSP right after the <head> open tag (or at position 0 / after <html>
// as fallbacks), so it precedes any inline <head> script.
rendered := injectCSPFirst(input, cspNoScriptMeta)      // first-in-head
rendered = injectPrintCSS(rendered, buildPrintCSS(o))   // end-of-head for cascade
```

Add a regression fixture with `<head><script>document.write(...)</script></head>`
and assert the script did not execute.

## Warnings

### WR-01: chromium PDF-validation failures are mislabeled "libreoffice:" and lose the "chromium:" context

**File:** `internal/convert/chromium.go:156`
**Issue:**
`Convert` returns `validatePDF(outPath)` unwrapped. `validatePDF` lives in
`libreoffice.go` and prefixes every error with `libreoffice:` (confirmed:
`libreoffice: stat output`, `libreoffice: output is empty`,
`libreoffice: output missing %PDF- magic bytes` at libreoffice.go:181-202).
So a failed chromium render surfaces in `job_events.detail` /
`engine_stderr` as a *LibreOffice* error, and — unlike every other error path
in this converter — carries no `chromium:` prefix. Terminal classification
still works (the substrings are in `terminalChromiumSignatures`), so this is a
diagnostics/observability defect, not a control-flow bug, but it will actively
mislead anyone triaging a chromium failure.

**Fix:** Wrap the validation result like the rest of the function:

```go
if err := validatePDF(outPath); err != nil {
    return fmt.Errorf("chromium: %w", err)
}
return nil
```

(Keep the `terminalChromiumSignatures` substrings unchanged — they match the
inner `validatePDF` text regardless of the outer prefix.)

### WR-02: `buildPrintCSS` forces `margin: 0 !important` on every render, including no-opts jobs

**File:** `internal/convert/htmlopts.go:123-131`, `internal/convert/chromium.go:116`
**Issue:**
`buildPrintCSS` is injected unconditionally (chromium.go:116), even when the
client sent no opts. For a no-opts job `HTMLOpts` is the zero value, so
`o.MarginMM == 0`, and the emitted rule is
`@page { size: A4 !important; margin: 0mm !important; }`. The `!important`
overrides any `@page` margin in the client's own HTML, so **every** HTML→PDF
render is forced to zero page margins (edge-to-edge content) by default — which
differs from the browser print default (~10mm) and cannot be overridden to a
browser default through the API (0 is indistinguishable from "unset"). Same
applies to page size being forced to A4. If edge-to-edge-by-default is the
intended product behavior this is fine; if not, no-opts jobs silently produce
unexpectedly-cropped-looking output.

**Fix:** Decide and document the intended default. If a non-zero default margin
is wanted, introduce a named default (e.g. `htmlDefaultMarginMM`) applied when
no `margin_mm` was requested, mirroring the existing `size = a4` default
fallback at htmlopts.go:126; distinguish "unset" from an explicit `0` (e.g. a
`*int` or a presence flag) so a client can still request true zero margins.

## Info

### IN-01: `isHTMLTerminal` and `isDocumentTerminal` are byte-identical

**File:** `internal/worker/worker.go:165-194`
**Issue:** Both functions are exactly `nil→false; DeadlineExceeded→true; else
isTerminal(err)`. The duplication is intentional per the doc comments
(engine-scoped classifiers), but the two bodies could share a single
`timeoutIsTerminal(err)` helper to prevent them drifting apart on a future edit.
**Fix:** Optional — extract a shared helper both engine classifiers delegate to,
keeping the two named entrypoints for readability.

### IN-02: chromium runs without an explicit `--user-data-dir` under `USER nobody`

**File:** `internal/convert/chromium.go:136-147`, `Dockerfile.chromium-worker:26`
**Issue:** The argv sets no `--user-data-dir`/`--crash-dumps-dir`, so
chromium-headless-shell falls back to a default profile/crash-dump location
relative to `$HOME`, which for `nobody` is typically non-writable
(`/nonexistent`). The doc comment says the flag set was live-verified, so this
apparently works today, but a pinned per-job `--user-data-dir` inside the
already-cleaned `workDir` would be more robust and guarantee no cross-job state
or stray writes outside the temp dir.
**Fix:** Optional hardening — add `--user-data-dir=<workDir>/chrome-profile` (or
similar) so all chromium scratch state lands inside the `os.RemoveAll(workDir)`
cleanup boundary.

### IN-03: migration `DROP CONSTRAINT` lacks `IF EXISTS` and relies on the inferred constraint name

**File:** `internal/db/migrations/0005_html_engine.sql:10`
**Issue:** `ALTER TABLE jobs DROP CONSTRAINT jobs_engine_check;` will error out
the migration if Postgres's actual auto-generated name differs from the assumed
`jobs_engine_check`. The comment acknowledges this ("if the live name differs,
this migration's DROP is corrected then"), so it is a known/accepted risk gated
on Plan 05 live acceptance, not an oversight.
**Fix:** Optional — verify the name via `\d jobs` before shipping (the comment
already plans this); `DROP CONSTRAINT IF EXISTS` would only mask a genuine
name mismatch, so an explicit verified name is preferable.

---

_Reviewed: 2026-07-11_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
