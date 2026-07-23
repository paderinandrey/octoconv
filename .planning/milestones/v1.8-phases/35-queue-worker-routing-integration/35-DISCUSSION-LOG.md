# Phase 35: Queue, Worker & Routing Integration - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-07-21
**Phase:** 35-queue-worker-routing-integration
**Areas discussed:** Retry classification (av), Video-to-transcript coverage, Plumbing seams (refactor vs mirror), Detection chain and upload limits

---

## Area selection

All four offered gray areas were selected for discussion.

| Option | Description | Selected |
|--------|-------------|----------|
| Retry classification (av) | How to distinguish transient/terminal when ffmpeg runs at both stages and no string prefix separates them | ✓ |
| Video-to-transcript coverage | Which containers get transcription; what to do about `AUDIO_ENGINE_TIMEOUT` demux overhead | ✓ |
| 18 plumbing seams: refactor or mirror | Hand-mirror the audio pattern vs centralize `queueForEngine` | ✓ |
| Detection chain and upload limits | `SniffVideo` placement, `HasDimensionLimit`, global `MAX_UPLOAD_BYTES` | ✓ |

---

## Retry classification (av)

### Q1 — What does the classifier key on to distinguish av stages?

| Option | Description | Selected |
|--------|-------------|----------|
| Typed sentinels (recommended) | `av.go` emits `ErrAVTranscodeFailed`/`ErrAVExtractFailed`/`ErrAVThumbnailFailed`; classifier uses `errors.Is`. Compiler helps on refactor, but edits Phase 34 code and its argv tests | ✓ |
| By operation (target format) | Classifier inspects the job's target: mp4/webm → timeout transient; others → terminal. Leaves `av.go` untouched but decides from job data, not failure cause | |
| Hybrid | Sentinels for deterministic failures, operation for timeouts | |
| You decide | Lock only "transcode timeout = transient, string prefix won't work" | |

**User's choice:** Typed sentinels.
**Notes:** Grounded in a live check of the code before asking — `av.go` emits the identical `"av: ffmpeg: %w"` wrapper from three separate call sites (`:481` transcode, `:528` extract, `:566` thumbnail), so the information needed to classify is genuinely absent from the string. Two typed sentinels (`ErrAVOutputMissingOrEmpty`, `ErrAVTimecodeOutOfRange`) already exist as precedent.

### Q2 — Which av failures are terminal?

| Option | Description | Selected |
|--------|-------------|----------|
| Timeout transient only for transcode (roadmap) | Transcode timeout transient; thumbnail/extract timeout terminal; all deterministic failures terminal | ✓ |
| All timeouts transient | Terminal only for deterministic failures. More forgiving of infra hiccups, risks burning CPU three times on a hopeless file | |
| All timeouts terminal | Conservative on CPU, contradicts the roadmap decision and makes the class fragile under load | |

**User's choice:** Timeout transient only for transcode.
**Notes:** Matches the pre-recorded roadmap decision that av's classification must be re-derived rather than copied from audio.

### Q3 — Retry budget

| Option | Description | Selected |
|--------|-------------|----------|
| Fewer attempts, longer pauses (recommended) | `AV_MAX_RETRY=2`, schedule ~30s/2m. Three executions at full timeout; long pause lets load drain | ✓ |
| Same as audio (3, 5s/15s/30s) | Parity with the neighbouring class, fewer concepts — but four full-timeout transcodes inflate both CPU cost and `AVUniqueTTL` | |
| No retries until measured (1 attempt) | Safest on resources, but makes the transient classification decorative and leaves success criterion 3 with nothing live to assert | |

**User's choice:** Fewer attempts, longer pauses.
**Notes:** Surfaced during the question that `AV_ENGINE_TIMEOUT` is only measured in Phase 36, so Phase 35 must use a provisional value and Phase 36 recomputes `AVUniqueTTL`. The retry count was chosen now because it is a multiplier in that formula.

---

## Video-to-transcript coverage

### Q1 — Which containers get transcription?

| Option | Description | Selected |
|--------|-------------|----------|
| All five (recommended) | mp4/mov/avi/mkv/webm × txt/srt/vtt/json — the ffmpeg normalize stage already demuxes anything ffmpeg decodes, so a subset would be arbitrary | ✓ |
| Only mp4/mov | Minimal reading of AVT-01, expand on demand — but an mkv client gets a 422 with no clear reason | |
| All five minus avi | Exclude the oldest, most codec-heterogeneous container from transcription while keeping it in transcode | |

**User's choice:** All five.
**Notes:** AVT-01 deliberately says "mp4/mov and others", leaving coverage open to this discussion.

### Q2 — `AUDIO_ENGINE_TIMEOUT` and demux overhead

| Option | Description | Selected |
|--------|-------------|----------|
| Raise `minFfmpegBudget` for video (recommended) | Leave the class timeout alone, enlarge the guaranteed stage-1 budget when the source is a video container. Targets the real difference without inflating the class-wide budget or `AudioUniqueTTL` | ✓ |
| Raise `AUDIO_ENGINE_TIMEOUT` | Simple, but lengthens pure-audio jobs and recomputes `AudioUniqueTTL` for the whole class | |
| Measure and document only | Run a video fixture through the audio pipeline, record real demux overhead, change constants only if the measurement shows a problem | |

**User's choice:** Raise `minFfmpegBudget` for video.
**Notes:** Grounded in `whisper.go:90` (`minFfmpegBudget = 30s`) and the existing "insufficient attempt budget remaining" transient error that guards stage 1.

---

## Plumbing seams (refactor vs mirror)

### Q1 — Refactor routing or mirror by hand?

| Option | Description | Selected |
|--------|-------------|----------|
| Mirror + completeness test (recommended) | Add av following the audio pattern, but close the risk with a test asserting every engine constant has a case in the API switch, the reconciler switch, and the collector list | ✓ |
| Centralize `queueForEngine` | Single engine→queue/task-type helper, migrate API and reconciler onto it. Removes the error class permanently but touches hot paths of four working classes for the sake of a fifth | |
| Just mirror | Minimal diff, as the previous four classes did. Leaves the invariant hand-maintained — the sixth engine hits the same rake | |

**User's choice:** Mirror + completeness test.
**Notes:** The deciding detail was the queue-depth collector at `cmd/api/main.go:92` — a variadic call where a missing `QueueAV` is not a compile error but a silently missing KEDA scaling metric. The other two switches at least fail closed at runtime.

---

## Detection chain and upload limits

### Q1 — `MAX_UPLOAD_BYTES` vs legitimate video

| Option | Description | Selected |
|--------|-------------|----------|
| Two-tier (recommended) | Raise the global hard cap to video scale (it protects memory/disk), then add a per-engine check after format detection — non-video over its own ceiling gets 413 before the S3 write | ✓ |
| Just raise the global cap | One number for everything. Minimal code, but docx and png become uploadable at video sizes, weakening the DoS posture of four working classes | |
| Defer to Phase 36 | Keep 100 MiB and decide alongside the disk-space guard and RTF measurement — but then Phase 35's E2E can only use tiny fixtures | |

**User's choice:** Two-tier.
**Notes:** Verified before asking that `http.MaxBytesReader` wraps `r.Body` at `handlers.go:93`, before parsing and therefore before the engine class is known — so a naive per-engine limit at that point is impossible, which is what forces the two-tier shape. `HasDimensionLimit` was checked in the same pass and appears to be a non-issue (image-scoped; video skips the block, and video resolution is guarded in the worker) — recorded as a planner confirmation item rather than a decision.

---

## Claude's Discretion

- Exact sentinel error names and their placement in `av.go`
- Numeric values: raised `minFfmpegBudget` for video, raised global `MAX_UPLOAD_BYTES`, per-engine ceilings, provisional `AV_ENGINE_TIMEOUT`
- Shape of the completeness test
- Exact insertion point for `SniffVideo` in the detection chain order

## Open questions raised but not decided

- Video with no audio track submitted for transcription (whisper receives empty input) — needs a defined behavior
- Multiple audio tracks in one container — which track feeds whisper
- `HasDimensionLimit` vs video — planner to confirm the no-op assumption rather than assume it

## Deferred Ideas

- Centralizing engine→queue routing behind a `queueForEngine` helper — considered and declined for this phase; revisit at the sixth engine class or as standalone tech-debt work
