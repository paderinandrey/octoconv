---
phase: 32-containerization-local-e2e-rtf-gate
plan: 05
subsystem: testing
tags: [e2e, audio, whisper.cpp, docker-compose, asynq, go-test]

# Dependency graph
requires:
  - phase: 32-containerization-local-e2e-rtf-gate (32-04)
    provides: audio-worker compose service with RTF-measured envs (AUDIO_ENGINE_TIMEOUT=742s*margin/1800, AUDIO_WORKER_CONCURRENCY=1), Dockerfile.audio-worker image octoconv-audio-worker:dev
provides:
  - TestAudioConversionE2E (env-gated live E2E test, self-skips offline) proving the containerized audio-worker end-to-end
  - assertDownloadIsNonEmptyTranscript helper (structural non-emptiness assertion, no exact-transcript match)
  - Committed jfk.wav fixture at internal/e2e/testdata/jfk.wav
  - Live pass evidence: job 3bbd9502-d81d-44f3-9d1c-bd05c56bb0c9 (engine=audio, wav->txt) reached status=done via the containerized audio-worker, 8.10s observed wall-clock
affects: [33-keda-audio-scaling]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "E2E test mirrors TestImageConversionE2E's single-fixture-plus-webhook shape for the 4th (final v1.7) engine class"

key-files:
  created:
    - internal/e2e/testdata/jfk.wav
  modified:
    - internal/e2e/e2e_test.go

key-decisions:
  - "Non-emptiness-only transcript assertion (Pitfall 9): ASR output is the project's first non-deterministic engine output; content/substring checks are the unit suite's job, not E2E's"
  - "5-minute pollUntilDone bound chosen to match document/html cold-start allowance, not image's tighter 2-minute bound — turned out far more generous than needed (observed 8.10s)"

patterns-established:
  - "assertDownloadIsNonEmptyTranscript: structural-only assertion pattern for non-deterministic engine output, distinct from assertDownloadIsImage's exact-format sniff"

requirements-completed: [AUD-06]

# Metrics
duration: 8min
completed: 2026-07-18
---

# Phase 32 Plan 05: Live Audio E2E Through Containerized audio-worker Summary

**TestAudioConversionE2E passes live against the containerized audio-worker (docker compose), closing AUD-06's live-acceptance gate with an 8.10s observed job wall-clock — far inside the 5-minute cold-start bound and negligible against the CI e2e suite's 30-minute timeout.**

## Performance

- **Duration:** ~8 min
- **Started:** 2026-07-18T17:05:00Z (approx, session start)
- **Completed:** 2026-07-18T17:12:46Z
- **Tasks:** 2 completed
- **Files modified:** 2 (internal/e2e/e2e_test.go, internal/e2e/testdata/jfk.wav)

## Accomplishments

- Added `TestAudioConversionE2E` + `assertDownloadIsNonEmptyTranscript` to `internal/e2e/e2e_test.go`, mirroring `TestImageConversionE2E`'s shape (upload → poll → presigned download → assertion → signed webhook)
- Copied `jfk.wav` (352,078 bytes, real file, not symlink) from `internal/convert/testdata/audio/` into `internal/e2e/testdata/`
- Ran the LIVE E2E through the actual containerized `audio-worker` compose service (not `go run`) and confirmed a PASS with database-level proof (job `3bbd9502-d81d-44f3-9d1c-bd05c56bb0c9`, engine=audio, wav→txt, status=done) landing inside the audio-worker container's uptime window
- Confirmed signed webhook delivery via `assertSignedWebhook` (HMAC-SHA256, reused unchanged)
- Recorded observed wall-clock (8.10s) and confirmed the 30-minute CI e2e suite timeout has ample headroom for this addition

## Task Commits

Each task was committed atomically:

1. **Task 1: Copy jfk.wav fixture + add TestAudioConversionE2E and assertDownloadIsNonEmptyTranscript** - `580adcf` (test)
2. **Task 2: Live E2E run through the containerized audio-worker with signed-webhook confirmation** - no code commit (verification-only task; live-run evidence recorded below, stack torn down cleanly, working tree stayed clean throughout)

**Plan metadata:** (this SUMMARY commit, see below)

## Files Created/Modified

- `internal/e2e/testdata/jfk.wav` - Real committed audio fixture (11s, WAV PCM 16-bit mono 16kHz), copied from `internal/convert/testdata/audio/jfk.wav`
- `internal/e2e/e2e_test.go` - Added `TestAudioConversionE2E` (upload → poll 5min bound → presigned download → non-empty transcript → signed webhook) and `assertDownloadIsNonEmptyTranscript` (structural non-emptiness helper)

## Decisions Made

- Non-emptiness-only transcript assertion, per Pitfall 9 (ASR output non-determinism) — no exact-string match, matching the plan's explicit must-have
- Reused the 5-minute `pollUntilDone` bound (document/html cold-start allowance) rather than the image engine's 2-minute bound, since audio's per-job cost profile (whisper.cpp model load + real transcription) is closer to document/html than to libvips' near-instant resize — this proved conservative: the observed job took 8.10s total from job creation (17:11:29 UTC) to test completion

## Deviations from Plan

None - plan executed exactly as written. `bytes` was already imported in `internal/e2e/e2e_test.go` (confirmed before editing, no import change needed).

## Issues Encountered

None. The compose stack, images (`octoconv-audio-worker:dev`), and API were already up-to-date and healthy from prior Phase 32 wave work — no rebuild was required (`docker compose up -d --build` was a no-op build/cache-hit), and `/healthz` responded on the first poll. k8s was confirmed down (`kubectl get nodes` connection refused) before bringing up compose, per the OrbStack mutual-exclusion discipline.

## Live E2E Evidence (AUD-06 acceptance gate)

**Command sequence run:**
```
docker compose -f docker-compose.yml -f docker-compose.e2e.yml up -d --build
# healthz responded immediately (stack was already warm)
go run ./cmd/migrate   # "migrations applied"
E2E_BASE_URL=http://localhost:8090 \
DATABASE_URL=postgres://octo:octo-pass@localhost:5434/octo_db \
API_KEY_SALT=dev-only-change-me-in-real-deploys \
WEBHOOK_SIGNING_SECRET=dev-only-change-me-in-real-deploys \
E2E_S3_DIAL_ADDR=127.0.0.1:9100 \
go test ./internal/e2e/... -run TestAudioConversionE2E -count=1 -v -timeout 15m
```

**Result:**
```
=== RUN   TestAudioConversionE2E
--- PASS: TestAudioConversionE2E (8.10s)
PASS
ok  	github.com/apaderin/octoconv/internal/e2e	8.601s
```

**Database confirmation the job ran through the CONTAINERIZED worker** (not `go run`):
```
                  id                  | engine | status | source_format | target_format |          created_at
--------------------------------------+--------+--------+---------------+---------------+-------------------------------
 3bbd9502-d81d-44f3-9d1c-bd05c56bb0c9 | audio  | done   | wav           | txt           | 2026-07-18 17:11:29.060639+00
```
`docker ps` confirmed `octoconv-audio-worker` container was created at `2026-07-18 20:10:58 +0300` (17:10:58 UTC) — 31 seconds before the job's `created_at` timestamp — and its logs show `asynq: ... INFO: Starting processing` at `17:11:04 UTC`, confirming the running containerized worker (not a `go run` process, none of which were started) consumed and completed the job. Signed webhook was confirmed via `assertSignedWebhook` (test would have failed otherwise — `t.Fatalf` on missing/unsigned webhook).

**Wall-clock and CI-timeout headroom assessment:**
- Observed job wall-clock: 8.10s (well inside the 5-minute `pollUntilDone` bound used in the test)
- CI's `internal/e2e` suite runs with `-timeout 30m` (`.github/workflows/ci.yml:123`); adding this ~8-10s test is negligible against the existing budget (dominated by the 6 document format-pair tests at up to 5 min each) — **no `-timeout` bump needed**
- No flake observed on the 5-minute `pollUntilDone` bound; the bound has substantial headroom relative to observed reality for the short `jfk.wav` fixture (11s audio, `base` model, 2 threads per the 32-04 RTF-measured sizing)

**Teardown:** `docker compose -f docker-compose.yml -f docker-compose.e2e.yml down -v` ran cleanly — all octoconv-* containers, volumes, and the compose network removed; unrelated `gsh-service-*` containers (different project) were untouched throughout (confirmed via `docker ps` before and after).

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- AUD-06's live-E2E acceptance criterion is closed: the containerized audio-worker is proven end-to-end via a repeatable, env-gated test that self-skips offline
- Phase 33 (KEDA audio scaling) can proceed on the confirmed compose-level pipeline; the 8.10s observed wall-clock for a short fixture is not itself an RTF signal (that measurement is 32-04's separate `AUDIO_ENGINE_TIMEOUT` derivation, already recorded in 32-04-SUMMARY.md) but does confirm no functional regression from containerization
- No blockers or concerns carried forward from this plan

---
*Phase: 32-containerization-local-e2e-rtf-gate*
*Completed: 2026-07-18*

## Self-Check: PASSED

- FOUND: internal/e2e/testdata/jfk.wav
- FOUND: func TestAudioConversionE2E in internal/e2e/e2e_test.go
- FOUND: func assertDownloadIsNonEmptyTranscript in internal/e2e/e2e_test.go
- FOUND: commit 580adcf (Task 1)
