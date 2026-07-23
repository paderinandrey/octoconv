---
id: SEED-008
status: dormant
planted: 2026-07-23
planted_during: "after v1.8 AV Engine close"
trigger_when: "when improving job observability / UX for long-running conversions (esp. video transcode), or when scoping a progress/status-reporting feature"
scope: medium
---

# SEED-008: Conversion progress reporting (percent complete)

Expose live conversion progress — "how many percent are left" — for long-running jobs, primarily video transcode. Today a job is opaque `queued → active → done`; a client polling an in-flight 90s transcode sees only `active` with no sense of how far along it is.

## Why This Matters

Video transcode is the longest-running operation in the system (RTF-measured up to hundreds of seconds), and the current status contract gives the caller/UI nothing to show but a spinner. A percent-complete (or ETA) turns a multi-minute opaque wait into a real progress bar — directly the UX the user asked for. It also makes the poll loop and any frontend materially more usable for the heavy engine classes (av, audio, large documents).

## When to Surface

**Trigger:** when improving observability/UX for long-running conversions, or when a frontend/consumer needs a progress bar. Naturally pairs with any av-engine follow-up milestone.

## Scope Estimate

**Medium** — a phase or two. The mechanics per engine differ:

- **ffmpeg (av/audio-extract)**: emits machine-readable progress via `-progress pipe:` / `-progress -` (`out_time_ms`, `frame`, `speed`); percent = `out_time_ms / (source_duration × 1000)`. Source duration is already probed (ffprobe) for the duration guard, so the denominator is in hand.
- **whisper.cpp (audio transcript)**: coarser — progress by processed-audio-position or segment count.
- **libvips/LibreOffice/Chromium**: mostly not incrementally reportable → report indeterminate / stage-only.

The cross-cutting design decisions: (1) a `progress` field (0–100 or null-when-indeterminate) on the job, surfaced in `GET /v1/jobs/{id}`; (2) how the worker streams progress from the child process without breaking the hardened `runCommand` exec contract (Setpgid + process-group kill) — likely a progress pipe/stderr parser; (3) write cadence (throttle DB writes — don't update Postgres on every ffmpeg tick; e.g. every N seconds or every ≥1% change); (4) optionally a `progress` webhook event or SSE, but poll-response percent is the minimal version.

## Breadcrumbs

- `internal/convert/exec.go` — the hardened `runCommand` process wrapper (Setpgid + SIGKILL-on-timeout) is where a progress-pipe reader would hook in without weakening the process-group-kill guarantee.
- `internal/convert/av.go` — ffmpeg invocation; already probes source duration (the percent denominator) for the duration guard.
- `internal/api/handlers.go` `handleGetJob` + the `jobs` table — where a `progress` column/field would surface in the status response.
- `job_events` append-only log (`0001_init.sql`) — an alternative/companion place to record progress milestones.
- Throttling matters: the guarded `Repo.transition` pattern shows the project already cares about not hammering Postgres; progress writes must be rate-limited similarly.

## Notes

Start with ffmpeg (av) since it has the best native progress signal and is the longest job; generalize the contract (percent-or-indeterminate) so other engines can opt in later.
