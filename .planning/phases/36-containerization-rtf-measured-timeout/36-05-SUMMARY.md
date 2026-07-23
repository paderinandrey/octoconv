---
phase: 36-containerization-rtf-measured-timeout
plan: 05
gap_closure: true
subsystem: infra
tags: [ffmpeg, oom-dos, security-fix, av-engine, code-review-followup]

# Dependency graph
requires:
  - phase: 36-04
    provides: "AV_ENGINE_TIMEOUT=753s RTF-derived finalization + the (incomplete) enforceNoScalePassthroughBound OOM-DoS guard"
provides:
  - "Generalized fail-closed AV re-encode source-resolution bound (enforceReencodeSourceBound) applied to EVERY re-encode path (no-scale AND explicit resolution_height resize alike), bounding BOTH source Height (>1080) and Width (>1920)"
  - "ErrAVReencodeResolutionExceeded sentinel, classified terminal (non-retryable) in isAVTerminal"
  - "Corrected .env.example/docker-compose.yml/36-04-SUMMARY.md documentation: no longer claims a no-scale-only, height-only bound closes the OOM DoS vector; names the ≤1080p re-encode envelope as a deliberate capability limitation"
affects: ["any future av-engine RTF re-measurement (decode-then-downscale envelope)", "code review CR-01/HI-01 closure"]

tech-stack:
  added: []
  patterns:
    - "enforceReencodeSourceBound: unconditional (not gated on resolution_height==0) fail-closed guard on the re-encode branch, checking both Width and Height against the measured envelope -- generalizes Plan 04's enforceNoScalePassthroughBound"

key-files:
  created: []
  modified:
    - internal/convert/av.go
    - internal/convert/av_test.go
    - internal/worker/worker.go
    - internal/worker/worker_test.go
    - .env.example
    - docker-compose.yml
    - .planning/phases/36-containerization-rtf-measured-timeout/36-04-SUMMARY.md
    - cmd/av-worker/main.go
    - .github/workflows/ci.yml

key-decisions:
  - "Renamed ErrAVNoScalePassthroughExceeded -> ErrAVReencodeResolutionExceeded (and avNoScalePassthroughMaxHeight -> avMaxReencodeSourceHeight + new avMaxReencodeSourceWidth) rather than adding a second parallel sentinel, since the generalized guard IS the same guard, now firing unconditionally and on both axes"
  - "Classified ErrAVReencodeResolutionExceeded terminal in isAVTerminal (internal/worker/worker.go) -- discovered during this plan that the predecessor sentinel was never added to isAVTerminal's list, which would have left this fail-closed rejection retried instead of immediately terminal (Rule 2: auto-added missing critical functionality, not in the plan's declared files_modified list)"
  - "Corrected 36-04-SUMMARY.md via an added 'Correction' section rather than rewriting its historical narrative in place, preserving the audit trail of what Plan 04 actually built/claimed while superseding the inaccurate framing"

requirements-completed: [AVE-04]

duration: 20min
completed: 2026-07-23
---

# Phase 36 Plan 05: AV Re-encode Source-Resolution Bound Gap-Closure Summary

**Generalized the Phase 36 headline OOM-DoS fix from a no-scale-only, height-only guard to a fail-closed bound on every re-encode path (explicit resize included) checking both source Height (>1080) and Width (>1920), closing code-review findings CR-01 (CRITICAL) and HI-01 (HIGH) and correcting the docs' false "OOM DoS closed" claim.**

## Performance

- **Duration:** ~20 min
- **Tasks:** 2/2 completed
- **Files modified:** 9

## Accomplishments

- Replaced `enforceNoScalePassthroughBound` (fired only when `resolution_height==0`, checked only Height) with `enforceReencodeSourceBound`, applied unconditionally to every `convertTranscode` re-encode branch, checking source Width and Height against the measured envelope (≤1920x1080). Stream-copy remux stays exempt; the 4320p decode-bomb outer ceiling is untouched.
- Renamed the sentinel `ErrAVNoScalePassthroughExceeded` → `ErrAVReencodeResolutionExceeded` and classified it terminal in `isAVTerminal` (a gap this plan discovered: the predecessor sentinel was never in that list, so the guard's own fail-closed rejection would have been silently retried rather than immediately terminal).
- Flipped the CR-01 bypass test (`an explicit resolution_height request bypasses the no-scale bound`) to assert rejection; added a Width-bound subtest proving the HI-01 fix (3840x1080 source rejected); kept the stream-copy-exempt and AVE-02 flag re-assertions passing.
- Corrected `.env.example`, `docker-compose.yml`, and `36-04-SUMMARY.md` so they no longer claim a no-scale-only bound closes the OOM DoS vector, and explicitly name the capability limitation (re-encoding/downscaling from sources >1080p is rejected, pending a future measured decode-then-downscale RTF envelope).
- Swept two stale LOW-severity comments flagged by the review: the "provisional timeout" wording in `cmd/av-worker/main.go` (the 753s value was already finalized) and the "6 compose bake targets" count in `.github/workflows/ci.yml` (now 8).

## Task Commits

Each task was committed atomically:

1. **Task 1: Generalize the source-resolution bound to every re-encode path, both axes** - `121e773` (fix)
2. **Task 2: Correct the docs so the OOM-DoS claim is accurate + sweep trivial stale comments** - `1a2595f` (docs)

_Note: this plan was `type="execute"`/`autonomous="true"` with no TDD gate; both commits are single-shot task commits, not RED/GREEN/REFACTOR sequences._

## Files Created/Modified

- `internal/convert/av.go` - `enforceReencodeSourceBound(width, height int)` replaces `enforceNoScalePassthroughBound(height int)`; new consts `avMaxReencodeSourceHeight=1080`/`avMaxReencodeSourceWidth=1920`; new sentinel `ErrAVReencodeResolutionExceeded`; `convertTranscode`'s guard call site is now unconditional (was gated on `o.ResolutionHeight == 0`)
- `internal/convert/av_test.go` - `TestConvertTranscode_NoScalePassthroughBound` renamed `TestConvertTranscode_ReencodeSourceResolutionBound`; the CR-01 bypass subtest flipped to assert rejection; a new HI-01 width-bound subtest added; stream-copy-exempt and boundary subtests updated to the new sentinel/name
- `internal/worker/worker.go` - `isAVTerminal` now includes `errors.Is(err, convert.ErrAVReencodeResolutionExceeded)` in its always-terminal deterministic-rejection list
- `internal/worker/worker_test.go` - added `ErrAVReencodeResolutionExceeded` to the deterministic-terminal test cases
- `.env.example` - `AV_ENGINE_TIMEOUT`/`AV_MAX_DURATION_SECONDS` comments corrected to describe the generalized ≤1920x1080 bound and name the capability limitation
- `docker-compose.yml` - `AV_MAX_DURATION_SECONDS` and `memory: 1g` comments corrected identically
- `.planning/phases/36-containerization-rtf-measured-timeout/36-04-SUMMARY.md` - added a "Correction (Phase 36 Plan 05 gap-closure)" section superseding the original "closes the OOM DoS vector" framing
- `cmd/av-worker/main.go` - fixed the stale `[ASSUMED] provisional` `AV_ENGINE_TIMEOUT` comment
- `.github/workflows/ci.yml` - fixed the stale "6 compose bake targets" comment (now "8")

## Decisions Made

- Renamed rather than duplicated the sentinel/consts (`ErrAVNoScalePassthroughExceeded` → `ErrAVReencodeResolutionExceeded`, etc.) since Plan 04's guard and this plan's guard are the same conceptual check, now firing unconditionally and on both axes -- a second parallel sentinel would have fragmented the terminal-classification contract.
- Corrected `36-04-SUMMARY.md` via an additive "Correction" section rather than editing its historical prose in place, so the audit trail of what Plan 04 actually shipped and claimed remains intact, with the inaccurate parts explicitly marked superseded.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing critical functionality] `ErrAVReencodeResolutionExceeded` (and its Plan-04 predecessor) was never classified in `isAVTerminal`**

- **Found during:** Task 1, while verifying the generalized sentinel's classification per the plan's must_have ("sentinel classified terminal like ErrAVResolutionExceeded")
- **Issue:** `internal/worker/worker.go`'s `isAVTerminal` deterministic-rejection list included `ErrAVResolutionExceeded`, `ErrAVOutputMissingOrEmpty`, `ErrAVTimecodeOutOfRange`, `ErrAudioDurationExceeded`, and `ErrAVNoVideoStream` — but never the Plan 04 passthrough-bound sentinel. Without this, a fail-closed guard rejection would fall through to the shared `isTerminal` fallthrough (default `false`), meaning the worker would treat this deterministic, non-retryable client-input rejection as transient and retry it up to `AV_MAX_RETRY` times before finally dropping it — silently defeating the fail-closed-before-expensive-work intent of the guard (though never invoking ffmpeg on retry, since the guard fires again identically each time).
- **Fix:** Added `errors.Is(err, convert.ErrAVReencodeResolutionExceeded)` to `isAVTerminal`'s deterministic list; added a corresponding test case in `worker_test.go`.
- **Files modified:** `internal/worker/worker.go`, `internal/worker/worker_test.go` (not in the plan's declared `files_modified` list, but required to satisfy the plan's own must_have)
- **Verification:** `go test ./internal/worker/...` green; new deterministic-case assertion passes
- **Committed in:** `121e773` (part of Task 1's commit)

No other deviations — both tasks otherwise executed exactly as specified.

## Verification

- `go build ./...`, `go vet ./...`, `go test ./internal/convert/... ./internal/worker/...`, and the full `go test ./...` all green.
- AVE-02 grep-count in `internal/convert/av.go` unchanged: `protocol_whitelist` appears exactly 3 times outside comments.
- `docker compose config` validates cleanly.
- `AV_ENGINE_TIMEOUT` parity: 8/8 occurrences in `docker-compose.yml`, single distinct value (`753s`).
- `.github/workflows/ci.yml`'s bake-target comment (8) matches the actual count of `build:`-declaring compose services (verified: 8 `build:` blocks in `docker-compose.yml`).

## Self-Check

- `internal/convert/av.go` — FOUND (modified, contains `enforceReencodeSourceBound`)
- `internal/convert/av_test.go` — FOUND (modified, contains `TestConvertTranscode_ReencodeSourceResolutionBound`)
- `internal/worker/worker.go` — FOUND (modified, `isAVTerminal` includes `ErrAVReencodeResolutionExceeded`)
- Commit `121e773` — FOUND in `git log --oneline`
- Commit `1a2595f` — FOUND in `git log --oneline`

## Self-Check: PASSED
