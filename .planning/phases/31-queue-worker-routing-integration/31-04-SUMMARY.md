---
phase: 31-queue-worker-routing-integration
plan: 04
subsystem: infra
tags: [asynq, cmd-entrypoint, go, docker-compose, whisper-cli, env-config]

# Dependency graph
requires:
  - phase: 31-01
    provides: audio queue/task types (TypeAudioConvert, QueueAudio, EnqueueAudioConvert, AudioUniqueTTL), migration 0006
  - phase: 31-02
    provides: worker.NewHandler's 10th audioMaxDuration param, HandleAudioConvert, stage-aware isAudioTerminal, RECONCILER_ACTIVE_STALE_AFTER raised to 15m
  - phase: 31-03
    provides: AudioConverter registered in convert.Default, API SniffAudio/opts/enqueue routing, reconciler audio routing
provides:
  - cmd/audio-worker/main.go — runnable audio-queue consumer entry point
  - .env.example audio operator documentation (5 AUDIO_* vars + 2 deferred-decision tradeoffs)
affects: [32-audio-engine-timing-and-sizing, 33-audio-keda-and-chart]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "cmd/<class>-worker/main.go verbatim template (5th application: image/document/html/webhook precedent, audio is the 4th engine-class repetition)"
    - "env-only-in-main setter injection (convert.SetAudioModelPath mirrors SetVeraPDFTimeout) — internal/convert never calls os.Getenv directly"

key-files:
  created: [cmd/audio-worker/main.go]
  modified: [.env.example]

key-decisions:
  - "AUDIO_ENGINE_TIMEOUT=600s shipped as an explicit [ASSUMED] placeholder in both code default and .env.example — Phase 32 re-derives from measured RTF, do not treat as tuned"
  - "AUDIO_MAX_DURATION_SECONDS default 4h (14400s) — generous ceiling on declared audio duration via EnforceMaxDuration, independent lever from AUDIO_ENGINE_TIMEOUT"
  - "RECONCILER_ACTIVE_STALE_AFTER global-raise (5m -> 15m) documented as a deliberate global tradeoff affecting image/document/html staleness-detection latency too, per Pitfall 2 option 1 — real safety mechanism is asynq.Unique/ErrDuplicateTask, not this threshold"
  - "MAX_UPLOAD_BYTES kept at global 100 MiB default — long-audio per-format ceiling explicitly deferred to Phase 32, documented inline so it is not silently dropped (Pitfall 3)"

requirements-completed: []  # AUD-05 NOT yet complete — Task 2 (live E2E) is the proof step and is blocked at an operator-safety checkpoint; see below.

# Metrics
duration: (partial — Task 1 only; Task 2 pending operator input)
completed: 2026-07-18
---

# Phase 31 Plan 04: cmd/audio-worker entry point + .env.example documentation Summary

**cmd/audio-worker built and verified against the compiled codebase (go build/vet/gofmt clean, wired to queue.TypeAudioConvert/QueueAudio, SetAudioModelPath called before srv.Start); .env.example documents all 5 audio env levers plus both Phase-31-deferred tradeoffs. Live E2E proof (Task 2) is BLOCKED at an operator-safety checkpoint, not yet executed.**

## Performance

- **Started:** 2026-07-18T~14:55Z (approx, first tool call this session)
- **Task 1 completed:** 2026-07-18T15:00Z
- **Tasks:** 1 of 2 completed (Task 2 is a `checkpoint:human-verify` gate, returned unexecuted per this plan's own explicit two-phase design)
- **Files modified:** 2 (1 created, 1 modified)

## Accomplishments

- `cmd/audio-worker/main.go` created — verbatim structural copy of `cmd/document-worker/main.go` with every Document/document identifier swapped for Audio/audio: asynq server bound solely to `queue.QueueAudio`, `mux.HandleFunc(queue.TypeAudioConvert, h.HandleAudioConvert)`, 10-param `worker.NewHandler` call passing `envDuration("AUDIO_MAX_DURATION_SECONDS", 4*time.Hour)` as the new `audioMaxDuration` argument and `nil`/`nil`/`nil`/`0` for the webhook-only fields, `convert.SetAudioModelPath(os.Getenv("AUDIO_MODEL_PATH"))` called before `srv.Start` (env-only-in-main, mirrors `SetVeraPDFTimeout`), no sweeper constructed, own localhost `/metrics` listener + graceful shutdown identical to the document-worker template.
- `.env.example` updated: new `# Audio worker` block (`AUDIO_ENGINE_TIMEOUT`, `AUDIO_MAX_RETRY`, `AUDIO_WORKER_CONCURRENCY`, `AUDIO_MAX_DURATION_SECONDS`, `AUDIO_MODEL_PATH`) plus updated `RECONCILER_ACTIVE_STALE_AFTER` (5m -> 15m, matching the value already live in `cmd/webhook-worker/main.go` since Plan 02) with an inline comment explaining the global-raise tradeoff, and an inline comment on `MAX_UPLOAD_BYTES` documenting the deliberate long-audio deferred decision.
- Verified: `gofmt -l cmd/audio-worker` clean, `go vet ./cmd/audio-worker/...` clean, `go build ./cmd/audio-worker/` succeeds, `go build ./...` and `go vet ./...` clean across the whole repo (no regressions to sibling packages).
- Confirmed `cmd/api/main.go`'s `NewQueueDepthCollector` was NOT touched (scope fence respected — that wiring is AUD-08/Phase 33).
- Confirmed all local E2E dependencies present and ready for Task 2: `/opt/homebrew/bin/ffmpeg`, `/opt/homebrew/bin/ffprobe`, `/opt/homebrew/bin/whisper-cli`, `~/.cache/whisper/ggml-base.bin` (147,951,465 bytes), and audio fixtures at `internal/convert/testdata/audio/` (jfk.wav, sample.wav, sample.m4a, sample-id3.mp3).

## Task Commits

1. **Task 1: cmd/audio-worker/main.go + .env.example audio block** - `d61ca82` (feat)
2. **Task 2: Live end-to-end audio conversion (queued → active → done)** - NOT EXECUTED (checkpoint, see below)

**Plan metadata:** (this commit, docs)

## Files Created/Modified
- `cmd/audio-worker/main.go` - audio-queue consumer process entry point (asynq server, metrics listener, graceful shutdown)
- `.env.example` - documents AUDIO_ENGINE_TIMEOUT/AUDIO_MAX_RETRY/AUDIO_WORKER_CONCURRENCY/AUDIO_MAX_DURATION_SECONDS/AUDIO_MODEL_PATH, RECONCILER_ACTIVE_STALE_AFTER tradeoff, MAX_UPLOAD_BYTES long-audio decision

## Decisions Made

See `key-decisions` in frontmatter. None of these were new architectural decisions — all four were pre-flagged as open items in `31-RESEARCH.md` (Pitfalls 1-3) and `31-02-SUMMARY.md` (RECONCILER_ACTIVE_STALE_AFTER already raised in code); this plan's job was to make them operator-visible in `.env.example`, which is now done.

## Deviations from Plan

None - Task 1 executed exactly as written (verbatim template copy per 31-PATTERNS.md, byte-for-byte matching the plan's own code block for `cmd/audio-worker/main.go`).

One incidental cleanup during verification: `go build ./cmd/audio-worker/` (run from repo root, no `-o` flag) wrote a stray `audio-worker` binary to the worktree root as a side effect of the verify command syntax specified in the plan (`go build ./cmd/audio-worker/`, no output path). This is standard `go build` behavior for a `main` package built without `-o`, not a bug in the plan or the code — the binary was untracked and removed (`rm -f audio-worker`) before staging, so nothing generated was committed.

## Issues Encountered

**Task 2 is blocked at its own designed checkpoint, not an error.** Environment check at execution time:

- `docker compose ps` (repo root) — empty, compose infra is DOWN.
- `kubectl get nodes` — `orbstack` cluster reachable, `Ready`, meaning the **k8s stack is currently UP**.

Per `STATE.md`'s own operational discipline ("never run compose and k8s stacks hot simultaneously — four confirmed daemon wedges on record") and this plan's Task 2 `<action>` text verbatim ("ask the operator to confirm the k8s stack is DOWN, then (only on approval, per the 'never run both hot' discipline...) `docker compose up -d postgres redis minio createbucket`"), bringing up compose right now without an explicit operator go-ahead to first tear down or accept the risk on the k8s stack would violate a documented safety precedent. `workflow.auto_advance` and `workflow._auto_chain_active` are both `false` in `.planning/config.json` (auto-mode is NOT active), so per the standard (non-auto) checkpoint protocol this plan must STOP and return the checkpoint for explicit operator input rather than auto-approving.

All automatable prep that does not require the infra decision was completed and verified (see Accomplishments): code builds/vets/formats clean, all local ffmpeg/whisper-cli/model/fixture dependencies confirmed present, `.env` template copy step identified as needed (`cp /Users/apaderin/dev/octoconv/.env ./.env` — not yet run, held pending the compose-up decision since it's meaningless without live infra).

## User Setup Required

None - no external service configuration required. Operator action needed is purely the infra-safety decision described above (see CHECKPOINT REACHED at the end of this turn).

## Next Phase Readiness

- Task 1 (the code + docs half of this plan) is fully complete, committed, and verified — `cmd/audio-worker` is a real, buildable, correctly-wired binary today.
- Task 2 (the live-proof half, and AUD-05's actual completion criterion) requires a fresh continuation once the operator confirms the k8s-down/compose-up precondition. The continuation agent should: confirm precondition → `cp /Users/apaderin/dev/octoconv/.env ./.env` → `docker compose up -d postgres redis minio createbucket` → wait for healthchecks → `set -a && . ./.env && set +a` with `AUDIO_MODEL_PATH=$HOME/.cache/whisper/ggml-base.bin` and a generous `AUDIO_MAX_DURATION_SECONDS` exported → start `go run ./cmd/api` and `go run ./cmd/audio-worker` in the background → confirm migration 0006 applied via `\d jobs` → create a test client via `go run ./cmd/manage-clients` if none exists → POST `internal/convert/testdata/audio/jfk.wav` to `/v1/jobs` with `target=txt` → poll to `done` → download and report the transcript.
- `requirements-completed` is deliberately left empty in this summary's frontmatter — AUD-05 should only be marked complete once Task 2's live proof lands (do not mark it complete from Task 1 alone).

---
*Phase: 31-queue-worker-routing-integration*
*Completed: 2026-07-18 (Task 1 only; Task 2 pending)*

## Self-Check: PASSED

- FOUND: cmd/audio-worker/main.go
- FOUND: commit d61ca82 (git log --oneline --all)
- FOUND: AUDIO_MODEL_PATH documented in .env.example
