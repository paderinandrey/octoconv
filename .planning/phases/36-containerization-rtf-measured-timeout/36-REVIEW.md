---
phase: 36-containerization-rtf-measured-timeout
reviewed: 2026-07-23T00:00:00Z
depth: deep
scope: git diff 53d4f41~1..HEAD -- ':(exclude).planning'
files_reviewed: 12
files_reviewed_list:
  - internal/convert/avdiskguard.go
  - internal/convert/avdiskguard_test.go
  - internal/convert/av.go
  - internal/convert/av_test.go
  - internal/convert/converters.go
  - cmd/av-worker/main.go
  - Dockerfile.av-worker
  - scripts/av-rtf-measure.sh
  - docker-compose.yml
  - .github/workflows/ci.yml
  - .env.example
  - go.mod
findings:
  critical: 1
  high: 1
  medium: 2
  low: 3
  total: 7
status: issues_found
advisory: true
---

# Phase 36: Code Review Report (containerization-rtf-measured-timeout)

**Reviewed:** 2026-07-23
**Depth:** deep (cross-file, argv/guard call-chain traced end to end)
**Files Reviewed:** 12
**Status:** issues_found (advisory — non-blocking per task instructions)

## Summary

This phase containerizes the `av-worker` (from-source ffmpeg n8.1.2), adds a
fail-closed disk-space guard, threads `AV_MAX_DURATION_SECONDS`/
`AV_DISK_SAFETY_FACTOR` into `AVConverter`, and finalizes `AV_ENGINE_TIMEOUT`
from a supervised RTF matrix. The env-parity, timeout-cap, and
`stop_grace_period` arithmetic all check out (753s parity across 8 services,
753 < 900s reconciler cap, 773s stop_grace_period = 763s asynq
`ShutdownTimeout` + 10s margin). The `-nostdin`/`-protocol_whitelist
file,crypto`/`file:`-prefix hardening (AVE-02) is untouched and well
re-asserted by tests.

The one finding that matters most: the phase's headline security fix —
`enforceNoScalePassthroughBound`, which is documented and tested as closing a
live "hevc@2160p OOM-KILL" DoS vector discovered by the RTF measurement — only
guards the `resolution_height==0` (no-scale) request shape. An **explicit**
`resolution_height` request (still fully legal: `{480,720,1080}` enum, `hevc`
codec) against a source up to the pre-existing 4320p decode-bomb ceiling
bypasses the new guard entirely and was never exercised by the RTF matrix.
Tracing the actual ffmpeg invocation shape for that path shows it very
plausibly reproduces the same OOM class the guard was built to close. See
CR-01 below for the full chain of evidence.

## Critical Issues

### CR-01: Passthrough-bound fix does not close the OOM/DoS vector for explicit-resize requests against large sources

**File:** `internal/convert/av.go:611-635` (guard placement), `internal/convert/av.go:292-300` (`enforceNoScalePassthroughBound`), `internal/convert/av_test.go:869-881` (test explicitly asserting the bypass)

**Issue:**

The phase's stated goal (`.env.example:94`, `docker-compose.yml:510-521`,
`36-04-SUMMARY.md`) is: *"closing the hevc@2160p OOM-KILL (exit 137) DoS
vector this measurement discovered"*. The implemented fix only fires when
`o.ResolutionHeight == 0`:

```go
// internal/convert/av.go:622-626
if o.ResolutionHeight == 0 {
    if err := enforceNoScalePassthroughBound(src.primary.Height); err != nil {
        return fmt.Errorf("av: %w", err)
    }
}
```

But an **explicit** `resolution_height` request (e.g. `{"resolution_height":
1080, "codec": "hevc"}`) is a fully legal `AVOpts` value (validated against
the closed `{480,720,1080}` enum in `avopts.go:112-113`) and is never routed
through this guard at all — confirmed by the phase's own test
(`TestConvertTranscode_NoScalePassthroughBound/an_explicit_resolution_height_request_bypasses_the_no-scale_bound`,
`av_test.go:869-881`), which asserts the opposite of a rejection: that the
request proceeds straight to a real ffmpeg invocation
(`errors.Is(err, ErrAVTranscodeFailed)`).

The pre-existing, independent resolution ceiling
(`avMaxSourceResolutionHeight = 4320`, `av.go:259`, enforced in `Convert` at
`av.go:528` against `avMaxVideoHeight` — the max height across ALL probed
streams) still admits sources up to 4320p (8K) regardless of what
`resolution_height` is requested. `cmd/av-worker/main.go:69` re-registers
`AVConverter{MaxSourceResolutionHeight: 4320}` explicitly — i.e. production
runs with exactly this ceiling, not a lower one.

So the fully legal request `{"resolution_height": 1080, "codec": "hevc"}`
against a 4320p (or 2160p) source:
1. Passes the front-of-`Convert` duration/resolution guard (height ≤ 4320).
2. Is not stream-copy eligible (a resize was requested — `avStreamCopyEligible`, `av.go:657`).
3. Skips `enforceNoScalePassthroughBound` entirely (`o.ResolutionHeight != 0`).
4. Reaches `transcodeToMP4Args(..., codec="hevc", height=1080, ...)`, which emits `-vf scale=-2:1080` (`av.go:629`).

ffmpeg must still **decode the full 4320p/2160p source** before the scale
filter runs — decoder frame-buffer allocation is sized to the *source*
resolution, not the post-filter output resolution. This is precisely the
mechanism that produced the measured `exit 137` OOM at the 2160p passthrough
cell (`36-04-SUMMARY.md`'s own RTF table: `hevc | passthrough@2160 |
OOM-KILLED at 1g`). Crucially, **no cell in `scripts/av-rtf-measure.sh` ever
measures a decode-then-downscale shape**: every "bounded" matrix cell
generates its lavfi fixture at *exactly* the cell's target resolution and
applies no `-vf scale` at all (`av-rtf-measure.sh:174-186` — `ENC_ARGS` never
includes `-vf scale`), so `hevc@1080`'s measured 4.179s p95 RTF is a
same-resolution-in/out number, not a "4320p in, 1080p out" number. The
"explicit resize from an oversized source" shape was never in the matrix,
never GO/NO-GO'd, and is not guarded — yet it is presented in `.env.example`
and `docker-compose.yml` as a closed vector ("*closing the hevc@2160p
OOM-KILL ... DoS vector*").

`36-RESEARCH.md:257` ("Open Q3" framing) only ever discusses the
`resolution_height==0` passthrough case; the explicit-resize-from-a-large-source
case is not raised anywhere in the phase's own research/plan/summary
artifacts, so this looks like a genuine blind spot introduced by treating the
`{480,720,1080}` enum as bounding *decode* cost, when it only bounds *encode
target* resolution (`36-RESEARCH.md:257` itself makes exactly this
enum-vs-decode distinction for the *input* axis, but the fix doesn't apply the
same reasoning to the explicit-resize path).

**Fix:** Apply `enforceNoScalePassthroughBound` (or an equivalent height
ceiling) to `src.primary.Height` unconditionally in the re-encode branch, not
only when `o.ResolutionHeight == 0` — i.e. reject (or require a live RTF
measurement to justify a higher ceiling for) *any* re-encode whose *source*
height exceeds the measured-safe envelope, independent of what output height
was requested:

```go
// convertTranscode, re-encode branch — check unconditionally, not gated on
// o.ResolutionHeight == 0:
if err := enforceNoScalePassthroughBound(src.primary.Height); err != nil {
    return fmt.Errorf("av: %w", err)
}
```

If a deliberately higher decode ceiling for explicit-resize requests is
wanted (e.g. because downscaling is expected to reduce peak RSS after the
first few frames), that needs its own RTF/OOM measurement cell
(`scripts/av-rtf-measure.sh` extended with a real "decode NxM, scale to
480/720/1080" cell) before being encoded as a distinct, higher ceiling — not
silently left unbounded.

## High

### HI-01: Source resolution is bounded on height only — width is never checked, on either guard

**File:** `internal/convert/avduration.go:139-152` (`avMaxVideoHeight`), `internal/convert/av.go:295-299` (`enforceNoScalePassthroughBound`)

**Issue:** Both the pre-existing decode-bomb ceiling (`avMaxSourceResolutionHeight`/`enforceMaxResolutionOf`) and this phase's new passthrough-bound guard check only `Height` (`avMaxVideoHeight` iterates `s.Height`; `enforceNoScalePassthroughBound(height int)` takes only a height). `Width` is probed (`avduration.go:36,51`) and validated as `>0` but is never compared against any ceiling anywhere in the package (verified: `Width` appears in exactly one comparison in the whole package — the primary-stream-selection area comparison `s.Width*s.Height > best.Width*best.Height`, which picks the *largest* stream, not a bound). A crafted source declaring e.g. `7680x1080` (an ultra-wide, short frame — legal per both guards, since height ≤ 1080/4320) carries a similar or larger total-pixel decode/encode cost to the already-OOM'd 2160p case, and is unbounded by every check in this file. This directly compounds CR-01 (the new guard inherits the same height-only blind spot as the guard it mirrors) and is worth closing in the same pass, since the RTF matrix and disposition (b) are both framed around "closing an OOM/DoS vector."

**Fix:** Bound total declared pixel count (`Width*Height`) rather than height alone, in both `enforceMaxResolutionOf` and `enforceNoScalePassthroughBound`, or explicitly document why an aspect-ratio-driven width bomb is accepted residual risk (mirroring this project's own "explicitly name and accept residual risk" convention, `36-RESEARCH.md:404`) rather than leaving it unaddressed and unmentioned.

## Medium

### MD-01: cmd/av-worker's newly duplicated env-parsing helpers ship with zero unit tests

**File:** `cmd/av-worker/main.go:161-249` (`envInt`, `envDuration`, `envDurationSeconds`, `envFloat`, `resolveAVThreads`, `firstField`)

**Issue:** These functions are explicitly documented as "cloned verbatim from `cmd/audio-worker/main.go`" (`main.go:181`) and carry security-relevant fail-closed behavior (negative-value rejection per WR-02, bare-integer-seconds parsing per WR-05) — but `cmd/audio-worker` ships `main_test.go` with a 12-case table test for `envDurationSeconds` plus a `resolveAudioThreads` precedence test (`cmd/audio-worker/main_test.go:16-103`), while `cmd/av-worker` has **no test file at all**. The negative/garbage/zero/inline-comment edge cases that the audio precedent explicitly regression-tests (e.g. `"-5s"` silently becoming a job-rejecting-everything ceiling, WR-02) are unverified for the av-worker copy — a future edit to this duplicated logic (or a future divergence between the two copies) has no test to catch a regression. Traced to plan scope: `36-01-PLAN.md` only requested tests in `internal/convert/av_test.go`, never `cmd/av-worker/main_test.go`, so this is a plan-level gap the implementation faithfully inherited rather than an execution-time oversight — but it's still a real coverage hole worth closing.

**Fix:** Add `cmd/av-worker/main_test.go` mirroring `cmd/audio-worker/main_test.go`'s `TestEnvDurationSeconds`/`TestResolveAudioThreads` table tests (rename cases for `AV_MAX_DURATION_SECONDS`/`AV_THREADS`→N/A since av has no thread override, but the `envDurationSeconds`/`envFloat` parsing logic is identical enough to warrant the same negative/garbage/inline-comment cases).

### MD-02: Dockerfile.av-worker enables the `wav` demuxer with no documented justification and no use in the AV pipeline

**File:** `Dockerfile.av-worker:123`

**Issue:** `--enable-demuxer=mov,matroska,avi,wav` includes `wav`, but `avExtractSources`/`avTranscodeToMP4Sources` (`av.go:20,27`) never list `wav` as a legal AV-engine *source* format — `wav` only ever appears as an audio-extract *target* (`avAudioExtractTargets`, `av.go:31`), which needs the `wav` **muxer** (already present, `--enable-muxer=...,wav,...`), not the demuxer. Every other non-obvious flag in this Dockerfile (`lavfi`, `testsrc`/`sine`, `wrapped_avframe`, `format`/`aformat`/`aresample`, `zlib`, the `webp` muxer) carries a detailed "DEVIATION"/"verified against n8.1.2's ... .c" justification in the surrounding comment block — the `wav` demuxer is the one addition with no such justification, and it directly contradicts this file's own stated security rationale ("*a SECOND, structural layer of defense ... network protocols ... are not compiled into the binary AT ALL*" — the same minimal-surface argument applies to demuxers). This widens the file-format-confusion attack surface (a polyglot upload sniffed by the API layer as one container format could still be demuxed by ffmpeg's own probe as WAV) without an established need.

**Fix:** Remove `wav` from `--enable-demuxer` unless a concrete need surfaces (re-run the full `av_test.go` live-binary suite to confirm nothing regresses), or add the same class of justification comment the other additions received if it is in fact load-bearing for something not obvious from `av.go` alone.

## Low

### LO-01: Stale "provisional timeout" comment in cmd/av-worker/main.go

**File:** `cmd/av-worker/main.go:87`

**Issue:** The inline comment on the `envDuration("AV_ENGINE_TIMEOUT", 600*time.Second)` call still reads `"[ASSUMED] provisional ... Phase 36 re-derives the real value from an RTF matrix"` — but this *is* Phase 36, and the RTF-derived value (753s) was already finalized in this same phase's `docker-compose.yml`/`.env.example` (commit `b208634`). The 600s fallback default itself is harmless (only used if the env var is ever unset), but the surrounding prose reads as still-pending work that is, in fact, already done in sibling files.

**Fix:** Update the comment to note the default is now purely a `docker-compose.yml`-unset fallback, with the finalized value (753s) documented in `.env.example`/`docker-compose.yml`, mirroring how `AUDIO_ENGINE_TIMEOUT`'s equivalent comment was worded post-finalization.

### LO-02: `.github/workflows/ci.yml` "6 compose bake targets" comment is stale (now 8), and this phase widens the drift

**File:** `.github/workflows/ci.yml:44`

**Issue:** The Tier-3 job comment says `"build all 6 compose bake targets"`. As of this diff there are 8 distinct `build:`-declaring compose services (`api`, `worker`, `document-worker`, `chromium-worker`, `webhook-worker-1`, `webhook-worker-2`, `audio-worker`, `av-worker`) — the count was already stale before this phase (Phase 32's `audio-worker` addition made it 7) and this phase's `av-worker` addition (the two new `set:` lines at `ci.yml:77-78,113`) makes it 8 without correcting the comment.

**Fix:** Update to `"build all 8 compose bake targets"` (or drop the specific count and just say "every compose bake target" to avoid this recurring drift).

### LO-03: `AV_DISK_SAFETY_FACTOR` is not pinned in `docker-compose.yml`, unlike its sibling RTF-derived knobs

**File:** `docker-compose.yml` (av-worker service block, ~line 482-534)

**Issue:** `AV_MAX_DURATION_SECONDS`, `AV_WORKER_CONCURRENCY`, and `AV_ENGINE_TIMEOUT` are all explicitly set in the `av-worker` service even though `AV_DISK_SAFETY_FACTOR` shares the same "operator-tunable, RTF/measurement-relevant" character (`.env.example:95` documents it as a `[ASSUMED]` D-06 knob) — but it is left entirely unset in compose, silently relying on the `cmd/av-worker/main.go:76` code default (3.0). Functionally harmless today (the default matches the documented intended value), but inconsistent with this file's own convention of pinning every operator-relevant knob explicitly for auditability (every other AV_* var in this same block is pinned even when equal to its code default).

**Fix:** Add `AV_DISK_SAFETY_FACTOR: "3.0"` to the `av-worker` service block for consistency and auditability, or explicitly comment why it's the one knob intentionally left to the code default.

---

## Verified Correct (no findings)

- `AV_ENGINE_TIMEOUT=753s` parity: exactly 8/8 `queue.NewClient()`-constructing services, single value (`docker-compose.yml:117,167,224,271,321,370,442,508`).
- `AV_MAX_RETRY="2"` parity: same 8/8 count.
- `753s < 900s` (`RECONCILER_ACTIVE_STALE_AFTER=15m`) reconciler cap — 16.3% margin as documented.
- `stop_grace_period: 773s` = asynq `ShutdownTimeout` (753s + 10s = 763s) + 10s Docker SIGKILL margin (`docker-compose.yml:474`, `cmd/av-worker/main.go:114`).
- `envDurationSeconds`/`envFloat` (`cmd/av-worker/main.go:190-228`) correctly reject negative/non-positive values with a logged warning rather than silently miscomputing a fail-closed ceiling (WR-02 precedent correctly carried over, even though untested — see MD-01).
- AVE-02 hardening (`-nostdin`, `-protocol_whitelist file,crypto`, `file:`-prefixed paths) is present on every argv builder touched or added this phase, confirmed both by direct reading and by the phase's own `TestAVBuildersHardenEveryInvocation`/`TestConvertTranscode_NoScaleBound_PreservesAVE02Flags`/`TestProtocolWhitelist_BlocksHTTP_Canary` tests.
- `enforceNoScalePassthroughBound`'s own boundary condition is correct: `height > 1080` rejects, `height == 1080` passes (confirmed by both code and `TestConvertTranscode_NoScalePassthroughBound`'s exact-boundary subtest).
- `EnforceMinFreeDisk` (`avdiskguard.go`) correctly uses `Bavail` (not `Bfree`), correctly treats `free == needed` as passing (strict `<` comparison), and correctly distinguishes a `statfs` error from the insufficient-space sentinel via `errors.Is` — all backed by direct tests against real filesystem state, no host-specific assumptions.
- Stream-copy (remux) exemption from `enforceNoScalePassthroughBound` is correctly scoped (verified via `TestConvertTranscode_NoScalePassthroughBound/stream-copy_remux_is_exempt...`) — no decode/encode occurs on that path, so the exemption is sound.
- `EnforceMinFreeDisk`'s directory argument (`filepath.Dir(inPath)`) is the same directory `outPath` is written to (both under the same `os.MkdirTemp` `workDir`, `internal/worker/worker.go:1064-1072`), so the guard correctly accounts for the filesystem the eventual output will also consume.
- ffmpeg build pin: commit-hash pin (not the mutable `n8.1.2` tag) plus a `git rev-parse` fail-loud guard plus a runtime `ffmpeg -version` string assertion — both layers present and correctly ordered (`Dockerfile.av-worker:40-44,134-135`).
- `--enable-protocol=file,crypto` (no `http`/`https`/`rtmp`/`concat`) is a real structural second layer beneath the `-protocol_whitelist` argv flag, closing what would otherwise be defense-in-depth-only.

---

*Reviewed: 2026-07-23*
*Reviewer: Claude (gsd-code-reviewer)*
*Depth: deep*
*Advisory: findings above do not block merge per task instructions; CR-01/HI-01 are recommended for prompt follow-up given their direct connection to this phase's stated DoS-closure goal.*

## Resolution (Phase 36 Plan 05)

- **CR-01 (CRITICAL):** RESOLVED — `enforceReencodeSourceBound` generalizes the bound to every re-encode path (commit `121e773`).
- **HI-01 (HIGH):** RESOLVED — both Height and Width now bounded (commit `121e773`).
- **MEDIUM (env-parser unit tests, wav-demuxer justification):** DEFERRED — tracked follow-ups, out of scope for the gap-closure.
- **LOW (stale provisional-timeout comment, 6→8 bake-targets comment):** RESOLVED (commit `1a2595f`); `AV_DISK_SAFETY_FACTOR` pin left as noted.
