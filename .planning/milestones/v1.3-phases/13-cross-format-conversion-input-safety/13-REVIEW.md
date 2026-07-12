---
phase: 13-cross-format-conversion-input-safety
reviewed: 2026-07-10T00:00:00Z
depth: standard
files_reviewed: 9
files_reviewed_list:
  - internal/convert/libreoffice.go
  - internal/convert/libreoffice_test.go
  - internal/convert/olecfb.go
  - internal/convert/olecfb_test.go
  - internal/api/handlers.go
  - internal/api/handlers_test.go
  - internal/worker/worker.go
  - internal/worker/worker_test.go
  - internal/e2e/e2e_test.go
findings:
  critical: 0
  warning: 2
  info: 4
  total: 6
status: issues_found
---

# Phase 13: Code Review Report

**Reviewed:** 2026-07-10
**Depth:** standard
**Files Reviewed:** 9
**Status:** issues_found

## Summary

Reviewed the phase 13 change set (commits `2ecaf86`..`fcf5192`): LibreOfficeConverter generalization to 6 cross pairs via the two-axis `filterTable`, `validateDocumentOutput` dispatch, `terminalLibreOfficeSignatures` extension, the `IsOLECFB` fail-closed 422 branch, and the E2E cross-format/CFB suites. `go vet` and all unit suites pass.

Verdict on the four focus questions:

- **(a) Validator/terminal-signature coupling:** holds for every string the *new* non-pdf branch can return (`output is empty`, `output does not match expected container format` — both covered; `stat output`/`open output` are genuinely-transient OS errors, correctly left transient). It does **not** hold for two error strings on adjacent paths of the same `Convert()` call: `validatePDF`'s short-read error (WR-01) and the rename-ENOENT error from LibreOffice's exit-0-no-output failure mode (WR-02). Both are deterministic failures classified transient. The commit that renamed `no pdf export filter for` → `no export filter for` updated both sides consistently — no stale string remains anywhere (verified by grep).
- **(b) False `done` on cross-format paths:** none found. `MarkDone` is reachable only through `Convert()` returning nil, which requires `validateDocumentOutput` to pass; for non-pdf targets that requires `SniffContainer(output).Format == target`. Every wrong-family/wrong-container/truncated/empty output shape traced maps to an error, and all of those errors reach `MarkFailed` (terminal) or bounded retry followed by reconciler `reconciler_exhausted` — never `done`.
- **(c) CFB ordering:** airtight. `IsOLECFB` uses `ReadAt(_, 0)` (positional; `multipart.File` guarantees `io.ReaderAt` for both in-memory and temp-file backing, and `Sniff`'s consumed sequential cursor does not affect it). The branch sits before the mismatch check, before `s.storage.Upload`, and before `repo.Create`. A CFB file cannot be Sniff-detected as any image format (disjoint magic), cannot enter the ZIP branch (no `PK\x03\x04` prefix), and a <8-byte or polyglot-tail file falls through to the fail-closed unrecognized-content 422. Confirmed both live fixtures (`legacy.doc`, `encrypted.docx`) begin with `D0 CF 11 E0 A1 B1 1A E1`. Handler unit test asserts `store.uploaded == false` and `repo.created == nil`.
- **(d) Image pipeline regression:** none found. The handler change is a single new branch gated on `detected == ""` (images always detect earlier); the worker change adds two document-specific substrings to `isTerminal` that no realistic vips stderr can contain; `HandleImageConvert` and the dimension-check gating are untouched by this phase.

Pre-existing tracked debt (12-REVIEW WR-02/WR-03) was not re-flagged; this phase does not worsen either item.

## Warnings

### WR-01: `validatePDF` short-output error is not in `terminalLibreOfficeSignatures` — the stated coupling invariant has a hole on the pdf path

**File:** `internal/convert/libreoffice.go:149-152`; `internal/worker/worker.go:51-56`
**Issue:** `terminalLibreOfficeSignatures`'s doc comment claims "No retry can fix any of these — a corrupt or filter-confused document always fails validateDocumentOutput/filterFor again," but a non-empty PDF output of 1–4 bytes makes `io.ReadFull` in `validatePDF` return `libreoffice: read output header: unexpected EOF` — a string in none of the four signatures. `isDocumentTerminal` then classifies this deterministic corrupt-output shape as *transient*: the job burns up to DOCUMENT_MAX_RETRY (default 3) full LibreOffice runs (each up to DOCUMENT_ENGINE_TIMEOUT, 300s default), then sits `active` until the reconciler's staleness sweep cycles it through MaxRecoveries before finally failing with `reconciler_exhausted` instead of `engine_error`. Note the non-pdf branch does not share this hole (a <5-byte file fails `SniffContainer` → the covered mismatch string), so this is pdf-target-only — but pdf is the highest-volume target.
**Fix:** Close the gap inside the validator rather than adding another fragile substring: fail the size check before ever reading the header.
```go
if fi.Size() < int64(len(pdfMagic)) {
    // covers 0 bytes AND the 1-4 byte truncated shape with an
    // already-terminal-classified message
    return fmt.Errorf("libreoffice: output is empty")
}
```
(or return the `output missing %%PDF- magic bytes` error for `0 < size < 5`). After this, `read output header` can only occur on genuine local-IO errors, which are correctly transient.

### WR-02: LibreOffice "exit 0, no output file" surfaces as rename-ENOENT and is classified transient — deterministic failure retried at full engine-timeout cost

**File:** `internal/convert/libreoffice.go:83-86`; `internal/worker/worker.go:51-56`
**Issue:** LibreOffice's best-documented silent-failure mode — exit code 0 with *no output file produced at all* (filter refuses the document, filter-name/content mismatch, import module missing) — makes `os.Rename(producedPath, outPath)` fail with `libreoffice: rename output: ... no such file or directory`. That string matches no terminal signature, so `isDocumentTerminal` treats a deterministic per-input failure as transient: up to DOCUMENT_MAX_RETRY × DOCUMENT_ENGINE_TIMEOUT of wasted engine time, then reconciler recovery cycles, ending in `reconciler_exhausted` rather than a prompt `engine_error`. This path pre-dates phase 13, but the phase doubled its exposure surface: the filter table went from 3 auto-derived pdf filters to 12 hand-entered names, including 6 brand-new export filters (`writer8`, `MS Word 2007 XML`, …) whose refusal behavior is exactly the shape that triggers this mode. The comment on `terminalLibreOfficeSignatures` ("exit 0 but empty/corrupt/wrong-container output") claims this class is covered — the *no-output* sub-case is not.
**Fix:** Detect the missing output explicitly and return an already-terminal message:
```go
if _, statErr := os.Stat(producedPath); statErr != nil {
    // soffice exited 0 but produced nothing — deterministic filter refusal
    return fmt.Errorf("libreoffice: output is empty")
}
if err := os.Rename(producedPath, outPath); err != nil {
    return fmt.Errorf("libreoffice: rename output: %w", err)
}
```
(or add a dedicated `"no output produced"` message + signature-list entry in the same commit, per the phase's own coupling discipline).

## Info

### IN-01: `validateDocumentOutput` masks the `SniffContainer` error and conflates local-IO failure with format mismatch

**File:** `internal/convert/libreoffice.go:186-189`
**Issue:** `if serr != nil || cr.Format != target` collapses two distinct causes into one message and discards `serr` entirely. A transient local-disk read error during `zip.NewReader` is terminally failed as a "container format" mismatch, and `job_events.detail`'s `engine_stderr` loses the underlying cause (`not a valid zip container` vs. a genuine wrong-format container). Terminal-in-the-safe-direction, so no correctness risk — but diagnosability suffers.
**Fix:** Include the cause: `fmt.Errorf("libreoffice: output does not match expected container format %s (sniff: format=%q err=%v)", targetFormat, cr.Format, serr)` — the load-bearing substring stays intact.

### IN-02: Output-side validation is not fully "symmetric to the input-side guarantee" as the comment claims

**File:** `internal/convert/libreoffice.go:159-166`
**Issue:** The input path treats `cr.DuplicateRootPart` as fail-closed-unrecognized and rejects `cr.HasMacro`; `validateDocumentOutput` checks only `cr.Format`. Since the output is produced by our own LibreOffice from already-screened input this is not exploitable, but the doc comment overclaims ("validated by the same sniff … symmetric"). A future reader may rely on that symmetry.
**Fix:** Either add `|| cr.DuplicateRootPart` to the mismatch condition (one token, keeps the invariant literal) or soften the comment to "same format detection".

### IN-03: Cross-package substring coupling between validator and classifier has no compile-time link

**File:** `internal/worker/worker.go:51-56`; `internal/convert/libreoffice.go`
**Issue:** WR-01/WR-02 are both instances of the same structural weakness: `internal/convert` error *strings* are load-bearing API consumed by `internal/worker` via `strings.Contains`, with only a comment ("coupled in the same commit") holding them together. Any future reword in `libreoffice.go` silently reverts terminal classification to transient.
**Fix:** Introduce a typed sentinel in `internal/convert` (e.g. `var ErrTerminalOutput = errors.New("terminal output validation failure")`), wrap all deterministic validator/filter errors with `%w`, and have `isTerminal` check `errors.Is(err, convert.ErrTerminalOutput)` — the substring lists can then shrink to engine-stderr-only signatures.

### IN-04: `engineTimout` field name typo

**File:** `internal/worker/worker.go:137,159,384`
**Issue:** Unexported struct field `engineTimout` (missing "e") — pre-existing, but the file is in this phase's scope and the constructor parameter next to it is spelled correctly (`engineTimeout`), which invites copy-paste confusion.
**Fix:** Rename to `engineTimeout` (mechanical, three sites).

---

_Reviewed: 2026-07-10_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
