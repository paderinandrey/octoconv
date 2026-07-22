---
phase: 35
slug: 35-queue-worker-routing-integration
status: verified
threats_open: 0
asvs_level: 1
created: 2026-07-22
---

# Phase 35 — Security

> Per-phase security contract: threat register, accepted risks, and audit trail.
> Audit method: every `mitigate` disposition verified by direct read/grep of the
> implementation at HEAD plus execution of its pinning tests — not from SUMMARY/plan
> prose. Register authored at plan time (7 plans, T-35-01..30 + T-35-SC deduplicated).
> Phase 34 baseline (34-SECURITY.md, 17/17 closed, ASVS 2) checked for non-regression.

---

## Trust Boundaries

| Boundary | Description | Data Crossing |
|----------|-------------|---------------|
| uploaded file bytes → SniffVideo EBML parser | Previously dead-code bounded parser goes live on the upload path (D-08) | raw attacker-controlled bytes |
| uploaded file bytes → av/audio ffmpeg subprocess | AVConverter registration makes client containers reach ffmpeg/ffprobe through a newly reachable entry point; audio's normalize stage now also accepts video containers (D-04) | raw attacker-controlled bytes |
| client JSON opts → persisted opts → av converter | AVOpts validated at create, strictly re-parsed on the worker read path before ffmpeg argv | client-supplied JSON |
| job payload → av queue → av-worker | Redis carries only a job ID; all detail re-read from Postgres | job UUID |
| reconciler sweep → engine→queue routing | `jobs.engine` (server-set) drives which queue a recovered job lands on | server-set engine class |
| upload size → two-tier ceiling | Global 2 GiB `http.MaxBytesReader` pre-parse + per-engine post-detection ceiling before any S3 write | attacker-controlled byte volume |
| operator env → av resource envelope | `AV_ENGINE_TIMEOUT` / `AV_MAX_RETRY` / `AV_WORKER_CONCURRENCY` size the engine-class ceiling | operator config |

---

## Threat Register

| Threat ID | Category | Component | Disposition | Mitigation | Status |
|-----------|----------|-----------|-------------|------------|--------|
| T-35-01 | Elevation of Privilege | `ffmpegNormalizeArgs` (whisper.go) | mitigate | AVE-02 pair intact verbatim: `-y -nostdin -protocol_whitelist file,crypto -i file:<in> ... file:<norm>` (`whisper.go:252-255`, Phase 34 fix `64386de` still present). `TestFfmpegNormalizeArgs_FilePrefix` + `_MapAdjacency` assert whitelist/nostdin survive the `-map` insertion (`whisper_test.go:398-404`); pass at HEAD | closed |
| T-35-02 | Tampering | `-map 0:a:0` insertion | mitigate | Compile-time argv literal (`whisper.go:254`), never client-influenced; `TestFfmpegNormalizeArgs_MapAdjacency` pins exact position (immediately after `-i file:<in>`, before `-ar`) and value `0:a:0` (`whisper_test.go:371-395`); passes | closed |
| T-35-03 | Information Disclosure | new av sentinel error strings | accept | Verified sentinels carry only stage names, no paths/client bytes: `"av: transcode failed"`, `"av: audio-extract failed"`, `"av: thumbnail failed"`, `"av: output missing or empty"`, `"av: timecode exceeds source duration"` (`av.go:267-302`), `"ffprobe: no video stream found"` (`avduration.go:27`). See AR-35-01 | closed |
| T-35-04 | Denial of Service | video sources on the audio queue (D-04) | mitigate | `enforceAudioGuardBeforeConvert` runs for every `EngineAudio` job regardless of container, strictly before `Convert` (`worker.go:1024-1037`, called `worker.go:1086`); `ErrAudioDurationExceeded` classified terminal (`worker.go:296-300`). D-05 raises only the pre-stage budget floor (`minFfmpegBudgetVideo=90s`, `whisper.go:132,140-145`), not the attempt timeout | closed |
| T-35-05 | Denial of Service | `AVUniqueTTL` under-derivation | mitigate | Shared formula `(maxRetry+1)*engineTimeout + avBackoffSum + uniqueTTLSafetyMargin` (`queue.go:591-593`), reusing the shared margin const; wired at `client.go:127` from `AV_MAX_RETRY` (default 2, `client.go:113`) + `AV_ENGINE_TIMEOUT`. `TestAVUniqueTTL` (`queue_test.go:515`) asserts the strict lower bound; passes | closed |
| T-35-06 | Denial of Service | `RetryDelayFunc` default fallthrough for av | mitigate | `case TypeAVConvert: return AVRetryDelay(...)` present (`queue.go:393-394`); `TestRetryDelayFuncRoutesAVConvert` (`queue_test.go:492`) passes | closed |
| T-35-07 | Repudiation | missing `av` queue-depth series | mitigate | `AllConvertQueues()` includes `QueueAV` derived from engine constants (`queue.go:606-608`); `TestAllConvertQueuesCoversEveryEngine` (`queue_test.go:555`) passes | closed |
| T-35-08 | Denial of Service | `AV_ENGINE_TIMEOUT` vs 900s `RECONCILER_ACTIVE_STALE_AFTER` | accept | 600s [ASSUMED] provisional default leaves ~300s headroom; coupling documented at the env-read site (`cmd/av-worker/main.go:60`) as required. Correctness held by `AVUniqueTTL` + `asynq.ErrDuplicateTask`, not this threshold. See AR-35-02 | closed |
| T-35-09 | Tampering | tampered `job.Opts` row reaching ffmpeg | mitigate | `convert.AVOptsFromMap` strict re-parse in `HandleAVConvert` (`worker.go:854`) runs BEFORE `MarkActive` (`worker.go:872`); failure is terminal `invalid_options` + `SkipRetry` (`worker.go:860,869`), mirroring document/audio handlers | closed |
| T-35-10 | Denial of Service | infinite retry of a hopeless av job | mitigate | `isAVTerminal` returns true for every deterministic sentinel (`worker.go:354-387`: output-missing, timecode, resolution, duration, no-video-stream always terminal; non-timeout transcode failures terminal; extract/thumbnail failures always terminal); terminal path wraps `asynq.SkipRetry` (`worker.go:897`); transcode-timeout retries bounded by `AV_MAX_RETRY=2` (`client.go:113`) | closed |
| T-35-11 | Information Disclosure | `engine_stderr` leaking server paths | accept | Client-facing `error_message` is the sanitized `avFailureCode` mapping only (`worker.go:884-885`); raw stderr goes to `job_events.detail` (internal diagnostics) — identical exposure the four existing engine classes accept; clients are internal services. See AR-35-03 | closed |
| T-35-12 | Repudiation | double-counted outcome metrics on retry | mitigate | Transient path records no outcome and calls no `MarkFailed` (`worker.go:899-903`); `RecordJobOutcome` only on terminal/done paths (`worker.go:886,905`) | closed |
| T-35-13 | Denial of Service | global `MAX_UPLOAD_BYTES` raised to 2 GiB | mitigate | Two-tier D-07: `NewServer` defaults hold image/document/html/audio at 100 MiB, av alone at 2 GiB (`api.go:167-173`); global default `2<<30` (`cmd/api/main.go:139`). `TestCreateJob_EngineSizeLimit_DefaultsMatchD07` + `TestCreateJob_EngineSizeLimitRejectsOversizedImage` (413) + `TestCreateJob_EngineSizeLimitAcceptsLargeAVUpload` pass. Note: the 413 test uses scaled-down ceilings rather than a literal 200 MiB payload — behavior-equivalent, since the gate is a pure size comparison | closed |
| T-35-14 | Denial of Service | oversized upload reaching S3 before rejection | mitigate | Per-engine check at `handlers.go:360-365` runs after `EngineFor` (`handlers.go:343`) and strictly before `s.storage.Upload` (`handlers.go:556`); test asserts `store.uploaded == false` and `repo.created == nil` on the 413 path (`handlers_test.go:2543-2548`) | closed |
| T-35-15 | Denial of Service | memory/disk pressure from 2 GiB in-flight body | accept | `http.MaxBytesReader` bounds pre-parse (`handlers.go:93`); `ParseMultipartForm` spools oversize parts to a temp file rather than RAM. Container memory/disk sizing for the av class is Phase 36 scope (AVE-04). See AR-35-04 | closed |
| T-35-16 | Repudiation | silent omission of `av` queue-depth series | mitigate | Collector list spread from `queue.AllConvertQueues()` (`cmd/api/main.go:98`); the hand-written variadic list is gone; completeness guarded by `TestAllConvertQueuesCoversEveryEngine` | closed |
| T-35-17 | Denial of Service | av jobs stranded in `active` forever | mitigate | `case convert.EngineAV: EnqueueAVConvert` in the sweep routing switch (`reconciler.go:294-295`); `TestSweepRoutesAVJobsToAVQueue` (`reconciler_test.go:330`) passes | closed |
| T-35-18 | Tampering | job routed to the wrong engine's queue | mitigate | Routing keys on exact `convert.Engine*` constants (`reconciler.go:285-295`); `TestSweepRoutesEveryEngineConstant` (`reconciler_test.go:376`) asserts one-and-only-one enqueue per engine; passes | closed |
| T-35-19 | Denial of Service | duplicate concurrent processing from reconciler re-enqueue | mitigate | Held by `AVUniqueTTL` (T-35-05) + enqueue-first recovery treating `asynq.ErrDuplicateTask` as safe no-op (`reconciler.go:276-278,310-317`) — pattern unchanged from prior phases | closed |
| T-35-20 | Repudiation | unknown engine silently dropped | accept | `default:` arm preserved fail-closed with `unroutable_engine` metric, no enqueue, no RequeueStale (`reconciler.go:296-307`) — existing accepted design. See AR-35-05 | closed |
| T-35-21 | Elevation of Privilege | `AVConverter.Convert` network-reachable for the first time | mitigate | No new call site inside the audited unit; AVE-02 non-regression verified at HEAD: all 7 subprocess invocations in `internal/convert` (`av.go:335,506,553,591`; `avduration.go:84`; `audioduration.go:81`; `whisper.go:369`) route through hardened argv builders carrying `-protocol_whitelist file,crypto` + `file:`-prefixed paths (`av.go:99-101,212-214,328`; `avduration.go:75`; `audioduration.go:77`; `whisper.go:252-255`); the eighth (`whisper-cli`, `whisper.go:379`) consumes only the server-produced WAV. `TestAVBuildersHardenEveryInvocation` + `TestFfprobeStreamArgs_Hardening` + `TestFfprobeDurationArgs` pass; Phase 34's canary retained | closed |
| T-35-22 | Denial of Service | `SniffVideo` EBML parser on hostile input | mitigate | Bounded-peek by construction: `avPeekLen = 4 KiB` (`avsniff.go:62`), uint64-space vint checks unchanged from Phase 34 (T-34-03). Call site placed after the global `MaxBytesReader` (`handlers.go:93`) and before `SniffAudio`'s 512 KiB peek (`handlers.go:296` vs `:316`); `rest` unconditionally rebound on success to prevent stream truncation (`handlers.go:296-301`, T-31-02 class) | closed |
| T-35-23 | Tampering | client opts reaching ffmpeg argv | mitigate | `ParseAVOpts` (closed allowlist, `checkStrictObject`) + `ValidateAVApplicability` wired into the create-time opts dispatch (`handlers.go:473-482`); normalized struct persisted, never raw client bytes (`handlers.go:498`); Phase 34's injection audit (T-34-05/06/13) applies unchanged | closed |
| T-35-24 | Tampering | registry pair collision silently misrouting jobs | mitigate | `TestAVAudioPairDisjointness` (`pairs_disjoint_test.go`) exists, is load-bearing (identity loop + union-count assertion), and passes. Disjointness confirmed independently at HEAD: AV targets {mp4,webm,mp3,wav,m4a,jpg,png,webp} (`av.go:20-35,54-73`) vs audio targets {txt,srt,vtt,json} (`whisper.go:85`) — disjoint including after `NormalizeFormat` aliasing (all listed formats already canonical). Residual noted in Observations #1 | closed |
| T-35-25 | Denial of Service | generic-brand ISOBMFF audio-only file misrouted to av (IN-01) | mitigate | `ErrAVNoVideoStream` (`avduration.go:27`) always terminal in `isAVTerminal` (`worker.go:366`) and mapped to distinct `no_video_stream` client code (`worker.go:810-811`) — no retry loop | closed |
| T-35-26 | Denial of Service | asynq's silent 8s `ShutdownTimeout` aborting long transcodes | mitigate | `ShutdownTimeout: AV_ENGINE_TIMEOUT + 10s` in `asynq.Config` (`cmd/av-worker/main.go:87`), mirroring the audio worker's precedent | closed |
| T-35-27 | Denial of Service | av retries falling through to asynq default backoff | mitigate | `RetryDelayFunc: queue.RetryDelayFunc` set explicitly (`cmd/av-worker/main.go:80`); `TypeAVConvert` arm covered by T-35-06's test | closed |
| T-35-28 | Denial of Service | unbounded worker concurrency exhausting CPU | mitigate | `AV_WORKER_CONCURRENCY` default 2, binding only `queue.QueueAV` (`cmd/av-worker/main.go:78-79`); container-level CPU/RAM/disk ceiling is a Phase 36 forward dependency (AVE-04), per plan scope | closed |
| T-35-29 | Elevation of Privilege | av-worker running with excess privilege | transfer | Transferred to Phase 36's `Dockerfile.av-worker` (inherits `USER nobody` precedent from `Dockerfile.worker:16`); no Docker surface for the av worker exists at HEAD, and the host-run binary is explicitly out of scope per 35-07-PLAN. Transfer recorded here + in the plan's threat model; Phase 36's audit must confirm the `USER nobody` directive lands | closed |
| T-35-30 | Tampering | orphaned ffmpeg child process after timeout | mitigate | `runCommand` sets `Setpgid` (`exec.go:30`) and SIGKILLs the whole process group on ctx cancel/timeout (`exec.go:47`); every av invocation goes through it (grep-confirmed, T-35-21 evidence) | closed |
| T-35-SC | Tampering | package-manager supply chain | mitigate | Zero dependencies added in Phase 35: `git log -- go.mod go.sum` shows last touch `3972e26` (2026-07-13, Phase 21); no Phase 35 commit modifies either file | closed |

*Status: open · closed*
*Disposition: mitigate (implementation required) · accept (documented risk) · transfer (third-party)*

---

## Accepted Risks Log

| Risk ID | Threat Ref | Rationale | Accepted By | Date |
|---------|------------|-----------|-------------|------|
| AR-35-01 | T-35-03 | New av sentinel strings carry only stage names ("av: transcode failed" etc., verified `av.go:267-302`, `avduration.go:15,27`); the wrapped `runCommand` stderr is the pre-existing disclosure surface, unchanged this phase | gsd-security-auditor (per 35-01-PLAN disposition) | 2026-07-22 |
| AR-35-02 | T-35-08 | `AV_ENGINE_TIMEOUT`=600s provisional leaves ~300s headroom under the 900s reconciler stale threshold; the coupling is documented at the env-read site (`cmd/av-worker/main.go:60`) so a future raise is a deliberate coupled change. Correctness held by `AVUniqueTTL` + `asynq.ErrDuplicateTask`. Phase 36 re-derives the timeout from RTF measurement | gsd-security-auditor (per 35-02-PLAN disposition) | 2026-07-22 |
| AR-35-03 | T-35-11 | `engine_stderr` (server temp-dir paths possible) recorded in `job_events.detail` for internal diagnostics only; client-facing `error_message` is a fixed sanitized string. Same exposure the four existing engine classes accept; clients are internal services only | gsd-security-auditor (per 35-03-PLAN disposition) | 2026-07-22 |
| AR-35-04 | T-35-15 | 2 GiB in-flight body bounded pre-parse by `http.MaxBytesReader`; multipart parts spool to temp files, not RAM. Av-class container memory/disk sizing (disk-space guard, AVE-04) is Phase 36 scope — forward dependency, not a Phase 35 gap | gsd-security-auditor (per 35-04-PLAN disposition) | 2026-07-22 |
| AR-35-05 | T-35-20 | Reconciler `default:` arm stays fail-closed for unknown engines with the observable `unroutable_engine` counter (`reconciler.go:296-307`) — the existing accepted design, preserved unchanged | gsd-security-auditor (per 35-05-PLAN disposition) | 2026-07-22 |

*Accepted risks do not resurface in future audit runs.*

---

## Unregistered Flags / Observations

SUMMARY `## Threat Flags` sections: 35-01, 35-03, 35-04, 35-06 declare "none — within plan register" (all map to registered threat IDs; informational). 35-02, 35-05, 35-07 carry no Threat Flags section at all — treated as none declared. **No unregistered flags.**

Observations recorded for future phases (informational, not blockers):

1. **Disjointness test compares raw, not normalized, pairs.** `TestAVAudioPairDisjointness` compares `Pairs()` output directly, while `Registry.Register` normalizes via `NormalizeFormat` (`convert.go:78`). A future alias-level overlap (e.g. one converter listing `jpeg` while the other lists `jpg`) would pass the test yet collide in the registry. Harmless at HEAD (all listed formats are canonical), but the test would be strictly stronger if it normalized both sides before comparing. Recommend for a future hardening pass — not a Phase 35 gap.
2. **T-35-13 test fidelity deviation.** The plan promised "a 200 MiB image ⇒ 413"; the shipped test uses scaled-down `MaxEngineBytes` instead of a literal 200 MiB payload. Behavior-equivalent (the gate is a pure `header.Size > limit` comparison, and the zero-storage-calls assertion is present), so accepted as satisfying the mitigation.
3. **Phase 36 forward dependencies (deliberate scope fences, per plan):** av-worker Docker image + `USER nobody` (T-35-29 transfer target), ffmpeg version pinning (CVE-2026-8461), disk-space guard (AVE-04), container CPU/RAM ceilings (T-35-28 residual), `AV_ENGINE_TIMEOUT` re-derivation from the RTF matrix (T-35-08/AR-35-02).
4. **AVE-02 invariant status:** holds repo-wide at HEAD (7/7 hardened ffmpeg/ffprobe invocations in `internal/convert`); Phase 34's whisper.go fix (`64386de`) confirmed present and extended by the `-map 0:a:0` insertion without weakening. Phase 34 canary + deterministic builder pins both retained.

---

## Security Audit Trail

| Audit Date | Threats Total | Closed | Open | Run By |
|------------|---------------|--------|------|--------|
| 2026-07-22 | 31 | 31 | 0 | gsd-security-auditor (Claude) |

Threat count: 30 unique register entries across the seven plans (T-35-01..30) +
T-35-SC (deduplicated; appears in all seven plans with identical disposition and
identical zero-dependency evidence) = 31 rows verified.

Test evidence executed at audit time: `go test ./internal/convert/ ./internal/queue/
./internal/reconciler/ ./internal/api/ ./internal/worker/` — all pass, including
`TestAVAudioPairDisjointness`, `TestFfmpegNormalizeArgs_MapAdjacency`, `TestAVUniqueTTL`,
`TestRetryDelayFuncRoutesAVConvert`, `TestAllConvertQueuesCoversEveryEngine`,
`TestSweepRoutesAVJobsToAVQueue`, `TestSweepRoutesEveryEngineConstant`,
`TestAVBuildersHardenEveryInvocation`.

---

## Sign-Off

- [x] All threats have a disposition (mitigate / accept / transfer)
- [x] Accepted risks documented in Accepted Risks Log
- [x] `threats_open: 0` confirmed
- [x] `status: verified` set in frontmatter

**Approval:** verified 2026-07-22
