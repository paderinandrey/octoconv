---
phase: 14-validated-conversion-options-pdf-a-export
reviewed: 2026-07-11T10:30:00Z
depth: standard
files_reviewed: 10
files_reviewed_list:
  - internal/jobs/jobs.go
  - internal/jobs/repo.go
  - internal/jobs/repo_test.go
  - internal/convert/opts.go
  - internal/convert/libreoffice.go
  - internal/convert/libreoffice_test.go
  - internal/worker/worker.go
  - internal/api/handlers.go
  - internal/api/handlers_test.go
  - internal/e2e/e2e_test.go
findings:
  critical: 0
  warning: 5
  info: 3
  total: 8
status: issues_found
---

# Phase 14: Code Review Report

**Reviewed:** 2026-07-11T10:30:00Z
**Depth:** standard
**Files Reviewed:** 10
**Status:** issues_found

## Summary

Reviewed the validated-conversion-options / PDF/A-export slice at standard depth, with extra scrutiny on the milestone's stated highest-severity attack surface: client-supplied `opts` reaching LibreOffice's argv/filter-JSON.

**The core security invariant holds.** I traced every path from raw client bytes to the `soffice` invocation:

- API write path: `rawOpts` → `ParseDocOpts` (closed struct, `DisallowUnknownFields`, exact-string allow-list) → `ValidateApplicability` → re-marshal of the *struct* (never the raw bytes) into `normalizedOpts` (`internal/api/handlers.go:257-279`). Raw client bytes are discarded at the parse boundary.
- Worker read path: `job.Opts` → `DocOptsFromMap` (same strictness) → `PDFAFilterOptions`, which returns only the compile-time constant `pdfaFilterOptionsSuffix` selected by an exact enum comparison (`internal/convert/opts.go:92-97`). No client-controlled content can appear in `convertTo` (`internal/convert/libreoffice.go:80-86`), and `runCommand` takes an argv array with no shell (`internal/convert/exec.go:20`).
- Verified empirically (throwaway probe test, removed): the injection vectors in `TestDocOptsInjectionResistance` are all rejected or normalized to the constant suffix.

Also verified: fail-closed 422 ordering is correct — the opts size cap, syntax parse, and applicability check all run before `s.storage.Upload` and `repo.Create` (`internal/api/handlers.go:249-288`); and the OutputIntent terminal-error string stays in lowercase-substring sync with `terminalLibreOfficeSignatures` (`libreoffice: output missing PDF/A OutputIntent marker` at `internal/convert/libreoffice.go:227` lowercases to exactly `output missing pdf/a outputintent marker` at `internal/worker/worker.go:60`; same for `produced no output file`, `output is empty`, `no export filter for`, and the container-mismatch string).

That said, the strict-parse contract is weaker than documented, two worker failure paths degrade badly, and one invariant is enforced only at a distance from where the argv is built. Details below.

## Warnings

### WR-01: `ParseDocOpts` is not actually strict — accepts trailing data, `null`, and duplicate keys

**File:** `internal/convert/opts.go:39-50`
**Issue:** The doc comment says "strict-decodes raw JSON" and this function is the documented first firewall against smuggled options, but `json.Decoder.Decode` reads only the *first* JSON value and ignores everything after it. Verified empirically:

- `{"pdf_profile":"pdf/a-2b"}{"EncryptFile":true}` → accepted (second object silently dropped)
- `{"pdf_profile":"pdf/a-2b"} garbage` → accepted
- `null` → accepted as zero `DocOpts` (not an object at all)
- `{"pdf_profile":"evil","pdf_profile":"pdf/a-2b"}` → accepted (duplicate key, last wins; the reverse ordering is rejected only because the last value fails the allow-list)

Nothing leaks downstream today because handlers.go re-marshals the struct (D-08) and `PDFAFilterOptions` is constant-only — but this layer's contract is violated, and any future caller that trusts "ParseDocOpts rejected it or it was clean" inherits these holes. The same laxity applies on the worker read path via `DocOptsFromMap`.
**Fix:**
```go
dec := json.NewDecoder(bytes.NewReader(raw))
dec.DisallowUnknownFields()
if err := dec.Decode(&o); err != nil {
    return DocOpts{}, fmt.Errorf("parse opts: %w", err)
}
// Reject trailing content after the first JSON value.
if dec.More() {
    return DocOpts{}, fmt.Errorf("parse opts: trailing data after JSON value")
}
```
Optionally also reject non-object top-level values (`null`, which currently yields a valid zero DocOpts) by peeking the first token and requiring `json.Delim('{')`.

### WR-02: Corrupt-opts terminal path strands the job in `queued` with no failure record

**File:** `internal/worker/worker.go:262-264`
**Issue:** `HandleDocumentConvert`'s strict re-parse of persisted opts returns `asynq.SkipRetry` **without calling `MarkFailed`** — and it runs *before* `MarkActive`, so the job row stays `queued` forever from the client's perspective: polling shows `queued`, no `error_code`, no webhook. The reconciler then repeatedly finds it stale-queued, re-enqueues it, and each re-delivery hits the same SkipRetry — burning the entire `RECONCILER_MAX_RECOVERIES` budget over multiple staleness intervals before the reconciler finally fails it with the misleading code `reconciler_exhausted` (`internal/reconciler/reconciler.go:116`) instead of the actual cause. Every other terminal classification in this handler (`isDocumentTerminal` branch, line 273-285) correctly commits `MarkFailed` first.
**Fix:**
```go
if _, err := convert.DocOptsFromMap(job.Opts); err != nil {
    _ = h.repo.MarkFailed(ctx, jobID, "invalid_opts", "stored conversion options are invalid",
        map[string]any{"opts_error": err.Error()})
    if job.CallbackURL != "" {
        _ = h.enqueuer.EnqueueWebhookDeliver(ctx, jobID)
    }
    return fmt.Errorf("%w: opts: %v", asynq.SkipRetry, err)
}
```
(`MarkFailed` allows `queued -> failed`, `internal/jobs/repo.go:162`, so this transition is legal.)

### WR-03: PDF/A filter suffix is appended for any target format, and `wantPDFA` is silently ignored for non-pdf targets

**File:** `internal/convert/libreoffice.go:80-86` (and `:213-249`)
**Issue:** `Convert` appends the PDF/A suffix to `convertTo` whenever the persisted opts carry `pdf_profile`, without checking that `targetFormat == "pdf"`. The API's `ValidateApplicability` guarantees this at write time, but the worker deliberately skips the applicability re-check (worker.go:257-261 comment), so a `jobs.options` row carrying `pdf_profile` on a cross-format job (DB corruption, manual insert, or a future write path) produces an argv like `odt:writer8:{"SelectPdfVersion":...}` — a PDF-export filter-JSON handed to a non-PDF filter. Worse, `validateDocumentOutput`'s non-pdf branch ignores `wantPDFA` entirely, so if soffice tolerates the bogus options string the job is reported `done` with the requested archival profile silently unhonored. This is the one place where the "suffix only ever rides on a pdf export filter" invariant is *not* enforced at the point the argv is built.
**Fix:** Enforce locally in `Convert`, where the argv is assembled:
```go
suffix, isPDFA := PDFAFilterOptions(docOpts)
if isPDFA && targetFormat != "pdf" {
    return fmt.Errorf("libreoffice: pdf_profile requested for non-pdf target %q", targetFormat)
}
```
(Note: `targetFormat` is computed at line 74, so move this check after it. The error should also be added to `terminalLibreOfficeSignatures` or phrased to match an existing terminal signature, since a retry cannot fix it.)

### WR-04: Swallowed `MarkFailed` error can emit a webhook reporting non-terminal status `"active"`

**File:** `internal/worker/worker.go:278-285` (document path; same pattern at `:205-212` image path)
**Issue:** On a terminal conversion error, `_ = h.repo.MarkFailed(...)` discards the error, then the handler unconditionally enqueues a webhook when `job.CallbackURL != ""` and returns SkipRetry. If `MarkFailed` fails (Postgres blip mid-transaction), the comment's premise — "the failed status is already committed above" — is false: the job is still `active`. `HandleWebhookDeliver` then re-reads the job (`worker.go:316`) and delivers a payload with `"status": "active"` — a non-terminal status the webhook contract never defines (clients expect `done`/`failed`; the e2e assertion at `internal/e2e/e2e_test.go:670-672` treats anything else as a failure). Meanwhile the job stays `active` until the reconciler requeues it and the entire conversion re-runs.
**Fix:** Gate the webhook enqueue on `MarkFailed` success:
```go
if ferr := h.repo.MarkFailed(ctx, jobID, "engine_error", "unsupported or corrupted input format",
    map[string]any{"engine_stderr": err.Error()}); ferr == nil && job.CallbackURL != "" {
    _ = h.enqueuer.EnqueueWebhookDeliver(ctx, jobID)
}
```
Apply to both `HandleImageConvert` and `HandleDocumentConvert`.

### WR-05: Oversize-opts test cannot detect removal of the size cap

**File:** `internal/api/handlers_test.go:1023-1045`
**Issue:** `TestCreateJob_OptsOversizeRejected` uses `{"pdf_profile":"aaaa...5000"}` — a value that *also* fails the `ParseDocOpts` allow-list. If the `len(rawOpts) > maxOptsBytes` guard (`internal/api/handlers.go:252`) were deleted outright, this test would still pass 422 via the enum rejection, silently masking the regression of the T-14-02 DoS bound this test exists to prove.
**Fix:** Pad with JSON whitespace so the payload is oversized but otherwise fully valid — only the size cap can reject it:
```go
oversized := `{` + strings.Repeat(" ", 5000) + `"pdf_profile":"pdf/a-2b"}`
```

## Info

### IN-01: Misspelled struct field `engineTimout`

**File:** `internal/worker/worker.go:143` (used at `:164`, `:399`)
**Issue:** `engineTimout` — missing the "e". Pre-existing, but this phase touched the file; harmless yet it propagates into every new read of the field.
**Fix:** Rename to `engineTimeout`.

### IN-02: Client-controlled filename echoed into error bodies with HTML escaping disabled

**File:** `internal/api/handlers.go:182, 190, 421`
**Issue:** The unrecognized-content and mismatch 422 messages interpolate the client-supplied `filename`/`source` into the response, and `writeJSON` sets `SetEscapeHTML(false)`. The `application/json` Content-Type keeps this from being an XSS today, but there is no `X-Content-Type-Options: nosniff` defense in depth.
**Fix:** Add `w.Header().Set("X-Content-Type-Options", "nosniff")` in `writeJSON` (or as middleware in routes.go).

### IN-03: `AddOutput` can insert duplicate ordinal-0 rows on retry after a transient `MarkDone` failure

**File:** `internal/worker/worker.go:436-447`, `internal/jobs/repo.go:293-302`, `internal/db/migrations/0001_init.sql:89-102`
**Issue:** `job_outputs` has only a surrogate `id` PK — no unique constraint on `(job_id, ordinal)`. If `process()` succeeds through `AddOutput` but `MarkDone` fails transiently, asynq's retry re-runs the whole pipeline and inserts a second `(job_id, 0)` row. Benign today (both rows carry the identical object key, and `Outputs()[0]` resolves the same object), but the schema's implied one-row-per-ordinal invariant is unenforced.
**Fix:** Add `CREATE UNIQUE INDEX job_outputs_job_ordinal_uq ON job_outputs (job_id, ordinal)` in a follow-up migration and make `AddOutput` upsert (`ON CONFLICT (job_id, ordinal) DO UPDATE`) so retries stay idempotent.

---

_Reviewed: 2026-07-11T10:30:00Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
