---
phase: 35-queue-worker-routing-integration
plan: 06
subsystem: api
tags: [ffmpeg, av-engine, content-detection, ebml, opts-validation, routing, go]

# Dependency graph
requires:
  - phase: 35-queue-worker-routing-integration
    plan: 01
    provides: "ErrAVTranscodeFailed/ErrAVAudioExtractFailed/ErrAVThumbnailFailed sentinels, AudioConverter.Pairs() extended to video sources (D-04), TestAVAudioPairDisjointness pair-disjointness guard"
  - phase: 35-queue-worker-routing-integration
    plan: 04
    provides: "Enqueuer.EnqueueAVConvert, the EngineAV enqueue-switch case, D-07 two-tier upload ceiling (Server.maxEngineBytes), the test-only fakeAVConverter synthetic pair"
provides:
  - "AVConverter registered into convert.Default (init(), converters.go) -- the first plan in this phase that makes video jobs reachable from outside"
  - "SniffVideo wired into handleCreateJob's detection chain (D-08), closing the mkv/webm detection gap that has had zero non-test callers since Phase 34 (WR-02)"
  - "EngineAV case in the opts-dispatch switch: ParseAVOpts + ValidateAVApplicability registered into the API create-job path (AVE-03)"
  - "TestCreateJobRoutesEveryEngineToItsQueue: the third and final D-06 completeness guard, covering the API enqueue switch"
  - "mp4->srt (audio/transcript) vs mp4->webm (av/transcode) routing split proven end-to-end (AVT-01, ROADMAP success criterion 2)"
affects: [35-07]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Reader-rebinding discipline for a NON-terminal sniffer in a chained detection pipeline: unlike SniffAudio (the chain's last detector, safe to only rebind `rest` on a match), SniffVideo sits mid-chain and must rebind `rest` on every call regardless of match outcome, because its io.ReadFull always drains the underlying stream"
    - "Registration-order pair-disjointness discipline for a bare-map-assignment registry: Default.Register(AVConverter{}) documented inline with the exact collision hazard and the single test that guards it"

key-files:
  created: []
  modified:
    - internal/convert/converters.go
    - internal/api/handlers.go
    - internal/api/handlers_test.go

key-decisions:
  - "Deviation (Rule 1, auto-fixed): the plan's literal instruction to mirror SniffAudio's rebind-only-on-match shape would have silently truncated every non-video upload's stream by up to avPeekLen (4 KiB) once SniffVideo was inserted mid-chain, because SniffAudio's own safety property (it's the LAST detector, so a stale `rest` is never read again) does not hold for SniffVideo (SniffAudio still runs after it). Fixed by rebinding `rest` on every verr == nil call, keeping only `detected` conditional on a match. Caught immediately by the existing TestCreateJob_AudioDetectedAndAccepted/TestCreateJob_AudioOptsAccepted regression tests before commit -- go test failing loudly, not a silent corruption discovered later."
  - "mkv/webm test fixtures built as committed byte-slice literals (buildEBMLFixture helper, duplicated from internal/convert's own unexported buildEBMLHeader test helper) rather than binary test files or a live-binary skip-gate, per the plan's stated preference for no binary dependency."
  - "AV opts-dispatch case adds zero clamping/defaulting/coercion of Timecode -- an out-of-range value is deliberately left to surface later as ErrAVTimecodeOutOfRange, mapped to a distinguishable failure code by HandleAVConvert (plan 03), per D-09's binding contract."

requirements-completed: [AVE-03, AVT-01]

# Metrics
duration: ~50min
completed: 2026-07-21
---

# Phase 35 Plan 06: AVConverter Registration, SniffVideo Wiring, AV Opts Dispatch, D-06 API Completeness Summary

**Registers AVConverter into convert.Default and wires SniffVideo into the upload detection chain in the same change (D-08) -- making video jobs reachable from outside for the first time -- plus the EngineAV opts-dispatch case and the third and final D-06 routing-completeness test.**

## Performance

- **Duration:** ~50 min
- **Started:** 2026-07-21 (worktree spawn)
- **Completed:** 2026-07-21
- **Tasks:** 3/3 completed
- **Files modified:** 3 (`internal/convert/converters.go`, `internal/api/handlers.go`, `internal/api/handlers_test.go`)

## Accomplishments

- `Default.Register(AVConverter{})` added to `internal/convert/converters.go`'s `init()`, after `AudioConverter` -- with an inline comment recording the `Registry.Register` bare-map-assignment collision hazard and pointing at `TestAVAudioPairDisjointness` (plan 01) as the sole guard. Re-ran that test after registration: still passes.
- `convert.SniffVideo` wired into `handleCreateJob`'s detection chain (`internal/api/handlers.go`), placed after the OLE-CFB `ClassifyCFB` check (line 249) and before `SniffAudio` (line 316) -- `SniffVideo` itself lands at line 296, satisfying the plan's ordering acceptance criterion. mp4/mov/avi remain detected earlier by `Sniff()`'s fixed-12-byte-window signatures table and never reach `SniffVideo` (proven by `TestCreateJob_MP4StillDetectedBySniffPrefixTable`).
- **Deviation caught and auto-fixed (Rule 1) during Task 1's own verification**, not left for later discovery: mirroring `SniffAudio`'s exact "only rebind `rest` on a non-empty match" shape broke existing mp3 detection tests (`TestCreateJob_AudioDetectedAndAccepted`, `TestCreateJob_AudioOptsAccepted`) because `SniffVideo` is not the chain's last detector -- its `io.ReadFull` always drains bytes from the underlying `rest` reader even when it finds no match, and `SniffAudio` runs immediately after it. Fixed by always rebinding `rest` to `SniffVideo`'s returned reader on `verr == nil`, leaving only `detected`'s reassignment conditional on a match. See "Deviations from Plan" below for full detail.
- `case convert.EngineAV` added to the opts-dispatch switch, mirroring the `EngineAudio` branch: `ParseAVOpts` -> 422 `invalid_opts` on parse failure, `ValidateAVApplicability` -> 422 `opts_not_applicable` on a mismatched pair, then `json.Marshal` to persist only the normalized struct. Zero clamping/defaulting of `Timecode` -- confirmed by reading the new case (no `math.Min`/clamp call anywhere in it).
- `TestCreateJobRoutesEveryEngineToItsQueue` added: table-driven over all 5 `convert.Engine*` constants (7 subtests total: 5 engines + 2 dedicated video-split rows), asserting exactly one matching `Enqueue*` call and zero on every other accessor. Confirmed load-bearing by temporarily removing the `case convert.EngineAV` enqueue-switch arm: both the `av` subtest and the `mp4_to_webm` split subtest failed with a 500 (the fail-closed `default:`), then restored before commit.
- AVE-02 non-regression verified: `protocol_whitelist` occurrence counts in `av.go` (5), `avduration.go` (2), `whisper.go` (2) are byte-identical to the Phase 34/35-01 baseline recorded in `35-01-SUMMARY.md`. `git diff --stat go.mod go.sum` shows no changes -- zero new dependencies.

## Task Commits

Each task was committed atomically:

1. **Task 1: Register AVConverter and wire SniffVideo into the detection chain (D-08, same change)** - `aa10348` (feat)
2. **Task 2: Add the EngineAV opts-dispatch case** - `0f0248a` (feat)
3. **Task 3: Engine-routing completeness test for the API enqueue switch (D-06)** - `6e706bf` (test)

_No TDD-tagged task required separate RED/GREEN commits -- production code and its tests landed in the same task commit, per this phase's established task-level granularity (see 35-01-SUMMARY.md, 35-04-SUMMARY.md)._

## Files Created/Modified

- `internal/convert/converters.go` - `Default.Register(AVConverter{})` added after `AudioConverter`, with the collision-hazard/disjointness-test comment
- `internal/api/handlers.go` - `SniffVideo` gate inserted into the detection chain (after OLE-CFB, before `SniffAudio`); `case convert.EngineAV` added to the opts-dispatch switch
- `internal/api/handlers_test.go` - `buildEBMLFixture`/`mkvBytesFixture`/`webmBytesFixture` helpers; `TestCreateJob_MKVDetectedAndAccepted`, `TestCreateJob_WebMDetectedAndAccepted`, `TestCreateJob_MP4StillDetectedBySniffPrefixTable`; `TestCreateJob_AVOptsAccepted`, `TestCreateJob_AVOptsAbsentAccepted`, `TestCreateJob_AVOptsRejectedForMalformedJSON`, `TestCreateJob_AVOptsRejectedForInapplicablePair`; `TestCreateJobRoutesEveryEngineToItsQueue` (D-06 completeness guard); two stale doc-comment references to "the real AVConverter is not yet registered" corrected to reflect this plan's registration

## Decisions Made

- Rebind `rest` on every `SniffVideo` call (verr == nil), not only on a match -- see Deviations below for the full rationale; this is a correctness fix required by SniffVideo's position mid-chain, not an optional style choice.
- Test fixtures for mkv/webm built via a committed byte-slice EBML-header builder (`buildEBMLFixture`, duplicated from `internal/convert`'s own unexported `buildEBMLHeader` test helper since it isn't exported across packages) rather than binary fixture files or a live-binary skip-gate -- the plan's own stated preference, and it keeps the new tests dependency-free and fast.
- `EngineAV` opts-dispatch case placed directly after `EngineAudio` in the switch (mirroring the plan's own ordering example), ahead of `EngineHTML` and the doc-default branch.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] SniffVideo's rest-reassignment shape, mirrored literally from SniffAudio, silently truncated non-video uploads**

- **Found during:** Task 1, immediately after wiring `SniffVideo` and running `go test ./internal/api/`
- **Issue:** The plan instructed mirroring `SniffAudio`'s exact gate shape: `if videoDetected, videoRest, verr := convert.SniffVideo(rest); verr == nil && videoDetected != "" { detected = videoDetected; rest = videoRest }`. This is safe for `SniffAudio` only because it is the chain's LAST detector -- on a miss, `detected` stays `""` and the handler returns the unrecognized-content 422 immediately after, so a `rest` left pointing at a partially-drained reader is never read again. `SniffVideo` is NOT last (`SniffAudio` still runs after it): its `io.ReadFull(rest, buf)` call unconditionally consumes up to `avPeekLen` (4 KiB) bytes from the underlying stream `rest` refers to, whether or not `matchEBML` finds a DocType match. Because the literal-mirror shape only reassigned `rest` on a match, an mp3 upload (or any non-video, non-match upload) hit `SniffVideo`, had its first ~4 KiB silently drained by the `ReadFull` call, then fell through to `SniffAudio(rest)` reading from that now-truncated reader -- `SniffAudio` could no longer find its magic bytes and the upload was rejected as unrecognized content. `TestCreateJob_AudioDetectedAndAccepted` and `TestCreateJob_AudioOptsAccepted` failed immediately (422 instead of 202) as soon as this shape was wired in.
- **Fix:** Rebind `rest = videoRest` unconditionally whenever `verr == nil` (a genuine read error is the only case that legitimately leaves `rest` untouched, matching `videoRest`'s own nil-on-error contract); only `detected`'s reassignment stays conditional on `videoDetected != ""`.
- **Files modified:** `internal/api/handlers.go`
- **Verification:** `go test ./internal/api/ -count=1` (full package) passes; `TestCreateJob_AudioDetectedAndAccepted`'s explicit byte-identity assertion (`store.uploadedBytes == data`) continues to hold, proving the fix restores full-stream integrity, not just a passing status code.
- **Committed in:** `aa10348` (Task 1 commit) -- caught and fixed before the commit landed, not a follow-up patch.

---

**Total deviations:** 1 auto-fixed (Rule 1 -- a correctness bug in the plan's own literal instruction, caught by the existing regression suite before commit)
**Impact on plan:** The fix is strictly more correct than the plan's literal text and does not change any of the plan's stated behavior bullets or acceptance criteria -- mkv/webm detection, mp4/mov/avi non-regression, and existing audio/document/html/OLE-CFB detection all hold, now including the byte-integrity property the plan's own CRITICAL constraint called out ("a mis-threaded rest silently corrupts the subsequent S3 upload body"). No scope creep.

## Issues Encountered

None beyond the deviation above, which was caught and resolved within Task 1 before any commit.

## Non-Regression Verification (AVE-02 / T-34-10, carried per this plan's threat register)

| File | Baseline (35-01-SUMMARY.md) | Current | Note |
|---|---|---|---|
| `internal/convert/av.go` | 5 | 5 | unchanged -- this plan touches `internal/api` and `internal/convert/converters.go` only, no argv-building code |
| `internal/convert/avduration.go` | 2 | 2 | unchanged |
| `internal/convert/whisper.go` | 2 | 2 | unchanged |

`git diff --stat go.mod go.sum` shows no changes (zero new dependencies).

## Acceptance-Criteria Induced-Failure Verifications

Per plan instructions, the following were verified by temporary edit, test run, and restore (all restored and confirmed byte-identical to the pre-edit state before commit):

- **Task 1:** Commenting out the `SniffVideo` gate makes `TestCreateJob_MKVDetectedAndAccepted` fail with a 422 (`"unrecognized file content for in.mkv"`) instead of 202.
- **Task 3:** Removing `case convert.EngineAV` from the enqueue switch makes both `TestCreateJobRoutesEveryEngineToItsQueue/av` and `TestCreateJobRoutesEveryEngineToItsQueue/mp4_to_webm_routes_av_not_audio` fail with a 500 (`"failed to enqueue job"`, the fail-closed `default:` branch) instead of 202.

## Known Stubs

None.

## Threat Flags

None -- this plan's changes stay entirely inside the threat register `35-06-PLAN.md` already declared (T-35-21 through T-35-25, T-35-SC), all dispositioned `mitigate`/`accept`/N-A in the plan itself:

- T-35-21 (AVConverter reachable from the network for the first time): no new call site created inside `AVConverter.Convert` -- it is the exact, already-audited unit from `34-SECURITY.md`. Verified via the AVE-02 grep counts above.
- T-35-22 (SniffVideo's EBML parser on hostile input): the bounded-peek parser (`avPeekLen = 4 KiB`) is unchanged from Phase 34; this plan only adds the API-layer call site, after the global `MaxBytesReader` bound and before `SniffAudio`'s larger peek.
- T-35-23 (client opts reaching ffmpeg argv): `ParseAVOpts`'s closed allowlist and `ValidateAVApplicability` are registered as-is (Phase 34 already ASVS-audited them); no new coercion/clamping added.
- T-35-24 (registry pair collision): `TestAVAudioPairDisjointness` re-run and still passes after registration.
- T-35-25 (generic-brand audio-only ISOBMFF misrouted to av): `ErrAVNoVideoStream` (plan 01) + its terminal classification (plan 03) already close this; unaffected by this plan's changes.

## User Setup Required

None -- no external service configuration required.

## Next Phase Readiness

- Video jobs are now reachable end-to-end through the API for the first time this milestone: an mkv/webm/mp4/mov/avi upload targeting a supported pair is detected, routed to the real `av` engine, opts-validated, and enqueued via `EnqueueAVConvert` -- all three of Plan 06's stated deliverables (AVConverter registration + SniffVideo wiring in the same change, opts dispatch, D-06 API completeness) are complete and independently test-verified.
- All three D-06 seams (queue-depth collector list: plan 04; reconciler routing switch: plan 05; API enqueue switch: this plan) now have a dedicated completeness test -- the "mirror by hand" decision (D-06, declined `queueForEngine` refactor) is fully defensible going into plan 07.
- No blockers for the final plan in this phase (35-07). The `cmd/av-worker` binary, the RTF-measured `AV_ENGINE_TIMEOUT`, and Docker/KEDA integration remain out of this phase's scope (Phase 36/37) per PROJECT.md's stated boundary.

---
*Phase: 35-queue-worker-routing-integration*
*Completed: 2026-07-21*

## Self-Check: PASSED

- FOUND: internal/convert/converters.go
- FOUND: internal/api/handlers.go
- FOUND: internal/api/handlers_test.go
- FOUND commit: aa10348 (Task 1)
- FOUND commit: 0f0248a (Task 2)
- FOUND commit: 6e706bf (Task 3)
