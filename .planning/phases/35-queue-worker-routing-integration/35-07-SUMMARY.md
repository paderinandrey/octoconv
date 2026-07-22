---
phase: 35-queue-worker-routing-integration
plan: 07
subsystem: infra
tags: [asynq, av-worker, ffmpeg, graceful-shutdown, prometheus, e2e]

requires:
  - phase: 35-02
    provides: TypeAVConvert, QueueAV, AVUniqueTTL, AV_ENGINE_TIMEOUT env plumbing
  - phase: 35-03
    provides: worker.Handler.HandleAVConvert (the mux handler this binary registers)
provides:
  - cmd/av-worker binary bound to the av queue, serving TypeAVConvert
  - live end-to-end proof of the av/audio routing split (Phase 35 SC1 + SC2)
affects: [36-containerization, 37-keda-helm]

tech-stack:
  added: []
  patterns:
    - "engine-class worker binary mirrors cmd/audio-worker/main.go (ShutdownTimeout = ENGINE_TIMEOUT + 10s overriding asynq's silent 8s default)"

key-files:
  created:
    - cmd/av-worker/main.go

status: complete
verification: passed (live E2E, differential routing confirmed)
---

# Plan 35-07 Summary — cmd/av-worker + live E2E

## What was built

**Task 1 — `cmd/av-worker/main.go` (commit `7055b7c`).** The fifth engine class's
worker binary, mirroring `cmd/audio-worker/main.go`:

- Binds to the `av` queue only (`Queues: map[string]int{queue.QueueAV: 1}`) and
  registers `queue.TypeAVConvert` → `worker.Handler.HandleAVConvert` on the mux.
- `ShutdownTimeout: AV_ENGINE_TIMEOUT + 10s` — the deliberate override of asynq's
  silent 8s default, so a long transcode survives SIGTERM instead of being aborted
  mid-flight on every deploy (the failure mode is silent without this).
- `RetryDelayFunc: queue.RetryDelayFunc` — routes av backoff through the shared
  dispatcher, not asynq's default schedule.
- `AV_ENGINE_TIMEOUT` default 600s, marked `[ASSUMED]` pending Phase 36's RTF
  measurement, with the inline comment recording its coupling to the 900s
  `RECONCILER_ACTIVE_STALE_AFTER` global (the tension that nearly broke the audio
  engine once, per docker-compose.yml).
- No model-path/thread setters (AV needs none this phase); `internal/convert`
  configuration still enters via setters called before `srv.Start`.

Task-1 gate: `go build ./...`, `go vet ./...`, `gofmt -l .`, `go test ./...` all
clean; zero `go.mod`/`go.sum` changes; no Dockerfile/compose/chart/CI file created
(scope fence honored, confirmed via `git status --porcelain`).

## Task 2 — live E2E checkpoint (verified, variant A)

Run on the host against docker-compose infra (postgres:5434, redis, minio) with
`go run` processes and host ffmpeg 8.1.2. The **differential routing test** — the
actual proof, per the plan — passed. Both jobs used the *same* mp4 source
(`testsrc` video + real jfk.wav speech), differing only in target format:

| Job | Pair | Engine (DB `jobs.engine`) | Result |
|-----|------|---------------------------|--------|
| A | mp4 → webm | **av** | `done` (~3.4s) |
| B | mp4 → srt  | **audio** | routed to audio, consumed by cmd/audio-worker |

All four checkpoint criteria met:
1. mp4→webm reached `done` on the av worker. ✓
2. mp4→srt routed to the **audio** queue, not av — the SC1/SC2 split, proven at the
   DB level (`engine=audio` vs `engine=av` for identical source bytes). ✓
3. `octoconv_queue_depth{queue="av",...}` series scrapeable on the API metrics
   endpoint (five states present). ✓
4. No orphaned ffmpeg process after workers idle. ✓

**Note (out of scope for this plan):** job B's whisper transcription stage stalled
on the host because `AUDIO_MODEL_PATH` in the local `.env` is unset / points at the
container path `/models/ggml-base.bin`; the real model is at
`~/.cache/whisper/ggml-base.bin`. This is a v1.7 audio-worker host-run config
matter, orthogonal to Phase 35 routing — Phase 35 never touches whisper model
resolution, and the routing decision (B → audio engine) had already been recorded
before the whisper stage ran. It does not affect any 35-07 acceptance criterion.

## Self-Check: PASSED

- All must_have truths verified: binary builds & serves TypeAVConvert; SIGTERM
  survival via the +10s override; Prometheus endpoint present; RetryDelayFunc wired.
- key_links verified: `mux.HandleFunc(queue.TypeAVConvert, ...)` present;
  `ShutdownTimeout` override present.
- No deviations from plan.
