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

requirements-completed: [AUD-05]

# Metrics
duration: ~85min (including operator checkpoint wait for k8s teardown)
completed: 2026-07-18
---

# Phase 31 Plan 04: cmd/audio-worker entry point + .env.example documentation Summary

**cmd/audio-worker built, verified, and live-proven end-to-end: an uploaded jfk.wav job flowed queued → active → done in ~2.8s against real compose infra (Postgres/Redis/MinIO) with local ffmpeg + whisper-cli, producing the correct transcript ("...ask not what your country can do for you..."); .env.example documents all 5 audio env levers plus both Phase-31-deferred tradeoffs.**

## Performance

- **Started:** 2026-07-18T~14:55Z (approx, first tool call this session)
- **Task 1 completed:** 2026-07-18T15:00Z
- **Checkpoint (operator k8s teardown + confirmation):** ~14:55-15:03Z (external wait, not agent compute time)
- **Task 2 (live E2E) completed:** 2026-07-18T15:06:54Z (compose torn down, cleanup done)
- **Tasks:** 2 of 2 completed
- **Files modified:** 3 (2 created, 1 modified) — `cmd/audio-worker/main.go`, `.planning/phases/31-queue-worker-routing-integration/31-04-SUMMARY.md`, `.env.example`

## Accomplishments

- `cmd/audio-worker/main.go` created — verbatim structural copy of `cmd/document-worker/main.go` with every Document/document identifier swapped for Audio/audio: asynq server bound solely to `queue.QueueAudio`, `mux.HandleFunc(queue.TypeAudioConvert, h.HandleAudioConvert)`, 10-param `worker.NewHandler` call passing `envDuration("AUDIO_MAX_DURATION_SECONDS", 4*time.Hour)` as the new `audioMaxDuration` argument and `nil`/`nil`/`nil`/`0` for the webhook-only fields, `convert.SetAudioModelPath(os.Getenv("AUDIO_MODEL_PATH"))` called before `srv.Start` (env-only-in-main, mirrors `SetVeraPDFTimeout`), no sweeper constructed, own localhost `/metrics` listener + graceful shutdown identical to the document-worker template.
- `.env.example` updated: new `# Audio worker` block (`AUDIO_ENGINE_TIMEOUT`, `AUDIO_MAX_RETRY`, `AUDIO_WORKER_CONCURRENCY`, `AUDIO_MAX_DURATION_SECONDS`, `AUDIO_MODEL_PATH`) plus updated `RECONCILER_ACTIVE_STALE_AFTER` (5m -> 15m, matching the value already live in `cmd/webhook-worker/main.go` since Plan 02) with an inline comment explaining the global-raise tradeoff, and an inline comment on `MAX_UPLOAD_BYTES` documenting the deliberate long-audio deferred decision.
- Verified: `gofmt -l cmd/audio-worker` clean, `go vet ./cmd/audio-worker/...` clean, `go build ./cmd/audio-worker/` succeeds, `go build ./...` and `go vet ./...` clean across the whole repo (no regressions to sibling packages).
- Confirmed `cmd/api/main.go`'s `NewQueueDepthCollector` was NOT touched (scope fence respected — that wiring is AUD-08/Phase 33).
- Confirmed all local E2E dependencies present and ready for Task 2: `/opt/homebrew/bin/ffmpeg`, `/opt/homebrew/bin/ffprobe`, `/opt/homebrew/bin/whisper-cli`, `~/.cache/whisper/ggml-base.bin` (147,951,465 bytes), and audio fixtures at `internal/convert/testdata/audio/` (jfk.wav, sample.wav, sample.m4a, sample-id3.mp3).
- **Task 2 (live E2E) executed successfully after operator cleared the checkpoint** (orbstack k8s cluster verified empty and stopped via `orb stop k8s`). Full evidence in the "Live E2E Evidence" section below: migration 0006 confirmed live via `\d jobs`, job create returned `202` (not `500`), status transitioned `queued -> active -> done` in ~2.8s with Postgres `job_events` timestamps, and the downloaded transcript matched the well-known JFK inaugural-address fixture content exactly.

## Task Commits

1. **Task 1: cmd/audio-worker/main.go + .env.example audio block** - `d61ca82` (feat)
2. **Task 2: Live end-to-end audio conversion (queued → active → done)** - verified live against real infra; no code changes (verification-only task per plan spec), evidence captured below and in this summary's commit

**Plan metadata:** (this SUMMARY.md update commit, docs)

## Files Created/Modified
- `cmd/audio-worker/main.go` - audio-queue consumer process entry point (asynq server, metrics listener, graceful shutdown)
- `.env.example` - documents AUDIO_ENGINE_TIMEOUT/AUDIO_MAX_RETRY/AUDIO_WORKER_CONCURRENCY/AUDIO_MAX_DURATION_SECONDS/AUDIO_MODEL_PATH, RECONCILER_ACTIVE_STALE_AFTER tradeoff, MAX_UPLOAD_BYTES long-audio decision

## Decisions Made

See `key-decisions` in frontmatter. None of these were new architectural decisions — all four were pre-flagged as open items in `31-RESEARCH.md` (Pitfalls 1-3) and `31-02-SUMMARY.md` (RECONCILER_ACTIVE_STALE_AFTER already raised in code); this plan's job was to make them operator-visible in `.env.example`, which is now done.

## Deviations from Plan

None - Task 1 executed exactly as written (verbatim template copy per 31-PATTERNS.md, byte-for-byte matching the plan's own code block for `cmd/audio-worker/main.go`).

One incidental cleanup during verification: `go build ./cmd/audio-worker/` (run from repo root, no `-o` flag) wrote a stray `audio-worker` binary to the worktree root as a side effect of the verify command syntax specified in the plan (`go build ./cmd/audio-worker/`, no output path). This is standard `go build` behavior for a `main` package built without `-o`, not a bug in the plan or the code — the binary was untracked and removed (`rm -f audio-worker`) before staging, so nothing generated was committed.

## Live E2E Evidence (Task 2)

**Checkpoint resolution:** orchestrator verified the orbstack k8s cluster was empty (zero helm releases, only kube-system coredns/local-path-provisioner) and stopped it via `orb stop k8s` (the project's own documented command). Confirmed independently at execution time: `kubectl get nodes` refused the connection (`127.0.0.1:26443` connection refused — cluster down), `docker info` responded normally (daemon healthy, v29.4.0). The compose↔k8s mutual-exclusion discipline was satisfied before proceeding.

**Compose infra brought up (octoconv-scoped only, non-conflicting ports):**
- `cp /Users/apaderin/dev/octoconv/.env ./.env` (gitignored copy into the worktree)
- `docker compose up -d postgres redis minio createbucket` — `octoconv-db` (5434), `octoconv-redis` (6379), `octoconv-minio` (9100/9101) all reported `healthy` on first poll
- Confirmed no port/name collision with the unrelated `gsh-service-*` project's own compose stack running concurrently (`gsh-service-postgres-1` on 5432, `gsh-service-redis-1` on 16379 — disjoint from octoconv's 5434/6379) — that stack was left untouched throughout, per instruction.

**Migration verification:**
```
$ go run ./cmd/migrate
2026/07/18 18:04:45 migrations applied

$ docker exec octoconv-db psql -U octo -d octo_db -c "\d jobs"
"jobs_engine_check" CHECK (engine = ANY (ARRAY['image'::text, 'document'::text, 'av'::text, 'cad'::text, 'archive'::text, 'probe'::text, 'html'::text, 'audio'::text]))
```
Migration 0006 confirmed live — `jobs_engine_check` accepts `'audio'`.

**Processes started (background, `go run`, logs to `/tmp/octoconv-e2e/`):**
- `go run ./cmd/api` — `2026/07/18 18:05:01 🚀 API listening on :8090`; `/healthz` returned `{"postgres":"ok","redis":"ok","s3":"ok","status":"ok"}`
- `go run ./cmd/audio-worker` with `AUDIO_MODEL_PATH=$HOME/.cache/whisper/ggml-base.bin`, `AUDIO_MAX_DURATION_SECONDS=14400s`, `AUDIO_ENGINE_TIMEOUT=300s`, `METRICS_ADDR=127.0.0.1:9091` (distinct from the API's own `:9090` to avoid a port clash between the two locally co-located processes) — `2026/07/18 18:05:15 🐙 audio-worker starting (queue=audio)`, asynq `Starting processing`

**Test client:** created via `go run ./cmd/manage-clients create audio-e2e-test-client` (no pre-existing clients in the fresh DB) — `client id: c7cc83bf-b82c-49cc-931c-16edb6902571`.

**Job create (auth scheme is `Authorization: ApiKey <key>`, per `internal/auth/middleware.go`, not `Bearer`):**
```
$ curl -X POST http://localhost:8090/v1/jobs \
    -H "Authorization: ApiKey ${API_KEY}" \
    -F "file=@internal/convert/testdata/audio/jfk.wav" \
    -F "target=txt"
HTTP 202
{"job_id":"993b5efe-f44e-4bce-a6fb-04f3a8c02746","status":"queued"}
```
**202 Accepted, not 500** — proves the 0006 constraint + engine routing accept an audio job end-to-end from the API's perspective.

**Status transitions (Postgres `job_events`, authoritative timestamps):**
| from_status | to_status | created_at (UTC) |
|---|---|---|
| — | queued | 2026-07-18 15:05:52.281066+00 |
| queued | active | 2026-07-18 15:05:53.413200+00 |
| active | done | 2026-07-18 15:05:55.103601+00 |

Total wall-clock queued→done: **~2.8 seconds** (jfk.wav is an 11-second clip; whisper-cli `base` model transcribes well under real-time on this hardware).

**Job row confirms audio engine routing:** `engine=audio, status=done, source_format=wav, target_format=txt`.

**Transcript (downloaded via the presigned `download_url` returned by `GET /v1/jobs/{id}`):**
```
 And so my fellow Americans ask not what your country can do for you, ask what you can do for your country.
```
Plausible, correct, and exactly matches the well-known JFK inaugural-address excerpt the `jfk.wav` fixture is known to contain — not empty, not garbage, not a hallucination.

**Cleanup performed:**
- `kill` sent to both `go run` parent PIDs; audio-worker logged a clean graceful shutdown (`🛑 shutting down audio-worker...` → asynq `Starting graceful shutdown` → `Waiting for all workers to finish` → `All workers have finished` → `Exiting` → `bye 👋`); `pkill` used to also stop the underlying compiled `go-build` child binaries left running by `go run`'s exec wrapper
- `docker compose down` removed `octoconv-db`, `octoconv-redis`, `octoconv-minio`, and the one-shot `createbucket` container plus the worktree-scoped compose network/volumes — `gsh-service-*` containers confirmed untouched (still running, unrelated project)
- `.env` copy remains in the worktree but is git-ignored (`git check-ignore -v .env` confirms `.gitignore:1:/.env`); `git status --short` showed a clean tree before staging this SUMMARY update

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- Both tasks of this plan are complete: `cmd/audio-worker` is a real, buildable, correctly-wired binary, and its live E2E behavior against real infra is now proven with timestamped evidence.
- AUD-05 is satisfied: an uploaded audio job flows `queued -> active -> done` via `go run ./cmd/audio-worker` against compose infra with local ffmpeg/whisper-cli, producing a valid transcript, with job create returning `202` (never `500`).
- Phase 32 (RTF/timing measurement) can now build directly on a proven-working `cmd/audio-worker` — the `AUDIO_ENGINE_TIMEOUT=600s` and `AUDIO_MAX_DURATION_SECONDS=4h` defaults shipped here remain explicit placeholders pending that measurement.
- No blockers carried forward from this plan.

---
*Phase: 31-queue-worker-routing-integration*
*Completed: 2026-07-18*

## Self-Check: PASSED

- FOUND: cmd/audio-worker/main.go
- FOUND: commit d61ca82 (git log --oneline --all)
- FOUND: AUDIO_MODEL_PATH documented in .env.example
- FOUND: live job 993b5efe-f44e-4bce-a6fb-04f3a8c02746 reached status=done in Postgres job_events (verified via docker exec psql during execution, infra since torn down)
