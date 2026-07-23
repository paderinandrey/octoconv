---
phase: 34-av-engine-foundation
plan: 01
subsystem: video-sniff
tags: [go, magic-bytes, ebml, mkv, webm, isobmff, riff, content-validation]

# Dependency graph
requires: []
provides:
  - "matchMP4/matchMOV/matchAVI fixed-offset ISOBMFF/RIFF matchers wired into sniff.go's signatures table"
  - "mp4VideoBrands closed allowlist, disjoint-tested against m4aBrands/heicBrands"
  - "matchEBML/vintLen/readSizeVint/readIDVint bounded-peek EBML DocType walker distinguishing mkv from webm"
  - "SniffVideo(r io.Reader) mirroring SniffAudio's peek+re-stitch shape for mkv/webm"
  - "MIMEType extended with video/mp4, video/quicktime, video/x-msvideo, video/x-matroska, video/webm"
affects: [35-av-registration, 36-av-containerize]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Fixed-offset ftyp+brand / RIFF+fourCC magic-bytes matching (mp4/mov/avi), copied verbatim from matchHEIC/matchWAV's shape"
    - "Bounded-peek, fail-closed declared-length TLV walk (EBML/DocType) for formats that cannot fit a fixed 12-byte window"

key-files:
  created: [internal/convert/avsniff.go, internal/convert/avsniff_test.go]
  modified: [internal/convert/sniff.go, internal/convert/sniff_test.go]

key-decisions:
  - "mp4VideoBrands excludes 'qt  ' (routed to matchMOV) and every m4aBrands/heicBrands entry, enforced by TestVideoBrandDisjointness as a permanent cross-file invariant"
  - "EBML DocType parser fails closed on truncation, unknown DocType, and any declared size/offset exceeding the 4KiB avPeekLen window -- never guesses mkv vs webm"

patterns-established:
  - "Video container detection is split: mp4/mov/avi reuse sniff.go's existing fixed-window Sniff()/signatures table; mkv/webm use a new, narrower SniffVideo() peek function dispatching only on matchEBML"

requirements-completed: [AVE-01]

# Metrics
duration: ~25min
completed: 2026-07-19
---

# Phase 34 Plan 01: Video Container Magic-Bytes Detection Summary

**Fail-closed magic-bytes detection for mp4/mov/avi (fixed-offset ftyp/RIFF matchers) and mkv/webm (bounded-peek EBML/DocType walker) added to `internal/convert`, unit-tested against live-verified byte layouts, with a permanent brand-disjointness invariant against the existing image/audio sniffers.**

## Performance

- **Duration:** ~25 min
- **Started:** 2026-07-19T17:00:00Z (approx, session start)
- **Completed:** 2026-07-19T17:17:00Z
- **Tasks:** 2 completed
- **Files modified:** 4 (2 created, 2 modified)

## Accomplishments
- `matchMP4`/`matchMOV`/`matchAVI` fixed-offset matchers added to `internal/convert/avsniff.go` and wired into `sniff.go`'s `signatures` table -- `Sniff()` now classifies real mp4/mov/avi content by magic bytes alone
- `mp4VideoBrands` closed allowlist proven disjoint from `m4aBrands` (audiosniff.go) and `heicBrands` (sniff.go) via `TestVideoBrandDisjointness`, a permanent cross-file invariant
- New bounded-peek EBML/DocType parser (`vintLen`/`readSizeVint`/`readIDVint`/`matchEBML`) distinguishes mkv from webm despite both sharing an identical 4-byte EBML magic -- the single highest-uncertainty item in Phase 34, built byte-exact against the live-verified reference algorithm in `34-RESEARCH.md`
- `SniffVideo()` added, mirroring `SniffAudio`'s peek-and-re-stitch shape, dispatching only on `matchEBML`
- `MIMEType` extended with all five video/* cases (mp4/mov/avi/mkv/webm)

## Task Commits

Each task was committed atomically:

1. **Task 1: Fixed-offset video container matchers (mp4/mov/avi) wired into sniff.go** - `978ca88` (feat)
2. **Task 2: Bounded-peek EBML/DocType parser (mkv vs webm) and SniffVideo** - `41f320c` (feat)

**Plan metadata:** (this commit) - docs: complete plan

## Files Created/Modified
- `internal/convert/avsniff.go` - matchMP4/matchMOV/matchAVI, mp4VideoBrands; vintLen/readSizeVint/readIDVint/matchEBML EBML walker; SniffVideo
- `internal/convert/avsniff_test.go` - table-driven matcher tests, disjointness test, EBML fixture tests (mkv/webm/truncated/unknown-DocType/oversized-size), vintLen boundary table, SniffVideo tests
- `internal/convert/sniff.go` - signatures table += mp4/mov/avi; MIMEType += 5 video/* cases
- `internal/convert/sniff_test.go` - narrowed `TestSniffHEIC_ForeignBrandNotDetected`'s assertion to match the intentionally-added mp4 classification (see Deviations)

## Decisions Made
- Followed RESEARCH.md Pattern 3/Pattern 4's byte-exact reference implementations verbatim for both the fixed-offset matchers and the EBML walker rather than re-deriving from the RFC description, per the plan's explicit guidance.
- No `converters.go`/`convert.go` edits -- registration into `convert.Default` remains out of scope for this plan (mirrors Phase 30's audio-engine fence), as required by the plan's scope fence.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Updated a pre-existing test whose assumption was invalidated by this plan's own signatures-table change**
- **Found during:** Task 2 (running the full `internal/convert` package test suite per the plan's overall `<verification>` block)
- **Issue:** `TestSniffHEIC_ForeignBrandNotDetected` (`sniff_test.go`, pre-existing) asserted `Sniff()` returns `""` for an `mp42`-branded `ftyp` box. That assumption predates Task 1's addition of `mp4`/`mov`/`avi` to `sniff.go`'s `signatures` table -- `mp42` is a registered `mp4VideoBrands` entry, so `Sniff()` now correctly classifies it as `mp4` (the exact intended behavior called out in Task 1's `<behavior>` block: "Sniff() (existing) now detects a real mp4/mov/avi fixture").
- **Fix:** Narrowed the assertion to what the test has always actually been proving -- that the buffer is never misdetected as `heic` -- while also pinning the new correct `mp4` classification, so the test still guards against a heic/mp4 collision.
- **Files modified:** `internal/convert/sniff_test.go`
- **Verification:** `go test ./internal/convert/` passes (full package, no other regressions)
- **Committed in:** `41f320c` (Task 2 commit)

---

**Total deviations:** 1 auto-fixed (Rule 1 - bug/stale test assumption)
**Impact on plan:** Necessary to keep the full package test suite green after the intentional, plan-specified behavior change. No scope creep -- the fix only touches the one assertion the new feature directly invalidated.

## Issues Encountered
None beyond the deviation above.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- `matchMP4`/`matchMOV`/`matchAVI`/`matchEBML`/`SniffVideo` exist, are unit-tested, and are ready for `AVConverter`/API-layer wiring in a later plan/phase -- this plan intentionally does not wire them into any request path.
- `EngineAV` constant, `converters.go` registration, and queue/worker wiring remain untouched, as scoped.
- `go build ./...`, `go vet ./...`, `gofmt -l .`, and `go test ./internal/convert/` all pass cleanly at HEAD.

---
*Phase: 34-av-engine-foundation*
*Completed: 2026-07-19*

## Self-Check: PASSED

All created/modified files confirmed present on disk; both task commits (`978ca88`, `41f320c`) confirmed in `git log`.
