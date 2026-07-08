---
phase: 04-content-validation-storage-lifecycle-observability
plan: 01
subsystem: api
tags: [go, magic-bytes, content-validation, http, security]

# Dependency graph
requires:
  - phase: 01-auth-hardening
    provides: "auth.ClientFromContext resolved client identity used for D-08 rejection logging"
provides:
  - "internal/convert/sniff.go: hardcoded magic-byte signature table + Sniff(peek+re-stitch) + shared MIMEType"
  - "handleCreateJob reordered to detect-then-validate: content sniffed before the pair-check and before any S3 write"
  - "S3-stored Content-Type is always the detected-format canonical MIME, never the client-supplied header"
affects: [04-02, 04-03, 04-04, 04-05]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Peek-and-restitch content detection: io.ReadFull(sniffLen) + io.MultiReader(bytes.NewReader(buf), r) so the full stream survives detection without buffering"
    - "Format->MIME mapping centralized in internal/convert (MIMEType), shared by API upload path and worker output path"

key-files:
  created:
    - internal/convert/sniff.go
    - internal/convert/sniff_test.go
  modified:
    - internal/api/handlers.go
    - internal/api/handlers_test.go
    - internal/worker/worker.go

key-decisions:
  - "D-01/D-02: declared-vs-detected mismatch and unrecognized content both reject with 422 before any storage write"
  - "D-03: hardcoded 5-format signature table (png/jpg/webp/heic/tiff) scoped exactly to convert.Default's registered formats, no external detection library"
  - "D-04: 422 body names both declared and detected formats (scoped exception to the generic-error-message convention)"
  - "D-05: convert.Sniff runs before convert.Default.Supports; the detected format (not the extension) feeds the pair-check"
  - "D-06: stored Content-Type is convert.MIMEType(detected), never the client multipart header"
  - "D-07: sniffLen=12, peek-and-restitch via io.MultiReader, never fully buffers the upload"
  - "D-08: every mismatch/unrecognized rejection is log.Printf'd with client_id (scoped exception to internal/* never logging)"

patterns-established:
  - "Content detection precedes trust decisions: any handler accepting user file uploads should sniff before branching on format"

requirements-completed: [VALID-01, VALID-02]

# Metrics
duration: 35min
completed: 2026-07-07
---

# Phase 4 Plan 01: Content Validation Summary

**Magic-byte content sniffing (hardcoded 5-format signature table) gates `handleCreateJob` before any pair-check or S3 write, rejecting declared/detected mismatches and unrecognized content with a detailed 422 and a client-scoped log line.**

## Performance

- **Duration:** 35 min
- **Started:** 2026-07-07T13:00:00Z
- **Completed:** 2026-07-07T13:35:00Z
- **Tasks:** 2
- **Files modified:** 5 (2 created, 3 modified)

## Accomplishments
- `internal/convert/sniff.go`: hardcoded magic-byte table for png/jpg/webp/heic/tiff (the exact set `convert.Default` registers), `Sniff(io.Reader) (detected string, rest io.Reader, err error)` using a 12-byte peek + `io.MultiReader` re-stitch (never buffers the full upload), and `MIMEType(format) string` promoted from the worker's former private `contentTypeFor`
- HEIC detection correctly distinguishes HEIF brands (`heic`/`heix`/`hevc`/`hevx`/`mif1`/`msf1`) from other ISOBMFF containers (e.g. `mp42`) sharing the same `ftyp` box structure
- `handleCreateJob` reordered: client resolved first (needed for D-08 logging) → content sniffed → mismatch/unrecognized rejected 422 with client-tagged log → detected format (not extension) drives the pair-check → stored Content-Type is the canonical detected-format MIME
- `internal/worker/worker.go` now calls the shared `convert.MIMEType` at both output-content-type call sites; the private duplicate `contentTypeFor` is removed

## Task Commits

Each task was committed atomically (TDD RED/GREEN pairs):

1. **Task 1: Magic-byte signature table, Sniff, and shared MIMEType** - `9454496` (test, RED) → `ac9b38e` (feat, GREEN)
2. **Task 2: Reorder handleCreateJob to detect-then-validate** - `59a28f6` (test, RED) → `f5d6011` (feat, GREEN)

## Files Created/Modified
- `internal/convert/sniff.go` - hardcoded signature table, `Sniff`, `MIMEType`
- `internal/convert/sniff_test.go` - per-format detection, foreign-brand HEIC rejection, unrecognized-content, short-input, full-stream-preservation, and MIMEType tests
- `internal/api/handlers.go` - `handleCreateJob` reordered (detect-then-validate), `log` import added, `contentType` sourced from `convert.MIMEType(detected)`
- `internal/api/handlers_test.go` - real magic-byte fixtures for existing OK/UnsupportedPair tests, new mismatch/unrecognized-content tests, `fakeStorage` now captures the `contentType` it received
- `internal/worker/worker.go` - both `contentTypeFor` call sites replaced with `convert.MIMEType`; private helper removed

## Decisions Made
No new decisions beyond CONTEXT.md's D-01 through D-08, all implemented as specified. Notable implementation choices within Claude's Discretion:
- HEIC matcher checks bytes 4-7 for `"ftyp"` and bytes 8-11 against a 6-brand allow-list (not a single fixed magic string), per D-03's hardcoded-table requirement and the plan's explicit foreign-brand-rejection test case
- `Sniff` tolerates both `io.ErrUnexpectedEOF` and `io.EOF` from `io.ReadFull` as "short input, not an error" (a completely empty upload also must not panic)

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered
None.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- `convert.Sniff`/`convert.MIMEType` are available for any future plan in this phase needing content-format detection or MIME mapping (no other Wave 1/2/3 plan in this phase currently depends on them per the dependency graph, but they are additive and safe to build on)
- `internal/api/handlers.go` and `internal/worker/worker.go` changes are isolated to the upload-intake and output-content-type paths; no interface signatures changed, so sibling plan 04-02 (different files, confirmed non-overlapping) is unaffected
- No blockers for subsequent phase-4 plans (storage lifecycle, observability)

## Self-Check: PASSED

- FOUND: internal/convert/sniff.go
- FOUND: internal/convert/sniff_test.go
- FOUND: internal/api/handlers.go (modified)
- FOUND: internal/api/handlers_test.go (modified)
- FOUND: internal/worker/worker.go (modified)
- FOUND commit: 9454496
- FOUND commit: ac9b38e
- FOUND commit: 59a28f6
- FOUND commit: f5d6011

---
*Phase: 04-content-validation-storage-lifecycle-observability*
*Completed: 2026-07-07*
