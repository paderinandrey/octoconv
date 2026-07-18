---
phase: 32-containerization-local-e2e-rtf-gate
verified: 2026-07-18T17:30:52Z
status: passed
score: 6/6 must-haves verified
overrides_applied: 0
---

# Phase 32: Containerization & Local E2E + RTF Gate Verification Report

**Phase Goal:** A running audio-worker container in docker-compose passes a full live E2E, with `AUDIO_ENGINE_TIMEOUT` sized from a measured realtime-factor go/no-go gate rather than a copied constant.
**Verified:** 2026-07-18T17:30:52Z
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | `Dockerfile.audio-worker` builds whisper.cpp v1.9.1 from source multi-stage with `-DGGML_NATIVE=OFF`, bakes `base` model SHA-256-pinned, installs apt ffmpeg, image is in CI bake matrix with `AUDIO_ENGINE_TIMEOUT`/`AUDIO_WORKER_CONCURRENCY`/ShutdownTimeout env wired | VERIFIED | `Dockerfile.audio-worker` (3 stages: Go build, whisper-build w/ `-DGGML_NATIVE=OFF` + commit-pinned checkout + `sha256sum -c -` on the model, slim ffmpeg runtime, `USER nobody`, plain `ENTRYPOINT`, no tini, no platform pin — all confirmed by direct file read). CI bake matrix: `audio-worker.cache-to`/`cache-from` present in `.github/workflows/ci.yml:75-76,110`. Env wiring confirmed live in `cmd/audio-worker/main.go:61,104,113` (`AUDIO_ENGINE_TIMEOUT`, `AUDIO_WORKER_CONCURRENCY`, `ShutdownTimeout = AUDIO_ENGINE_TIMEOUT+10s`). |
| 2 | An `audio-worker` compose service transcribes an uploaded file end-to-end through the live compose stack (upload → poll → presigned transcript download) with a signed webhook confirmed | VERIFIED | `docker-compose.yml` has an `audio-worker` service (`dockerfile: Dockerfile.audio-worker`, `cpus=2.0`/`memory=1g`, `stop_grace_period: 762s`). `internal/e2e/e2e_test.go:1161-1188` `TestAudioConversionE2E` implements upload→poll(5m)→presigned download→`assertDownloadIsNonEmptyTranscript`→`assertSignedWebhook`, env-gated via `e2eSetup` (self-skips when `E2E_BASE_URL` unset). Live-run evidence trail in `32-05-SUMMARY.md`: job `3bbd9502-d81d-44f3-9d1c-bd05c56bb0c9` (engine=audio, wav→txt, status=done, 8.10s), container-creation-before-job-creation timing, signed-webhook confirmation (test would `t.Fatalf` otherwise). Per explicit task instruction, the live stack was NOT re-run (currently down); evidence trail + code reviewed instead. |
| 3 | RTF measured on the real resource-limited container drives a documented go/no-go decision sizing `AUDIO_ENGINE_TIMEOUT` — hard input to Phase 33 KEDA tuning | VERIFIED | `scripts/audio-rtf-measure.sh` exists, `bash -n` clean, builds the real image (`docker build -f Dockerfile.audio-worker`), reads live `cpu.max`, generates a deterministic ffmpeg fixture, times 10 full-pipeline runs. `32-03-SUMMARY.md` documents the full raw evidence trail: p95 RTF=0.205853 (N=10, nearest-rank), explicit formula, NO-GO lever fired and applied (`AUDIO_MAX_DURATION_SECONDS` 14400s→1800s), derived `AUDIO_ENGINE_TIMEOUT=742s` (17.6% margin under the CAP), CAP read live from deployed compose. `docker-compose.yml`/`.env.example` ship `742s` (7/7 identical occurrences), no `[ASSUMED]` placeholder remains. |
| 4 | whisper.cpp `--threads` pinned to the container's cgroup CPU limit (not host cores); `AUDIO_WORKER_CONCURRENCY` set from measured per-job CPU/RSS, verified against container cpus/memory ceiling | VERIFIED | `internal/convert/cgroup.go` `CgroupCPULimit()`/`parseCPUMax` read `/sys/fs/cgroup/cpu.max`, floor quota/period, fail-open. `internal/convert/whisper.go` `SetAudioThreads`/`audioThreadCount`/`whisperArgs` always inject `-t <n>` (`strconv.Itoa(threads)`). `cmd/audio-worker/main.go` `resolveAudioThreads()` implements env→cgroup→NumCPU precedence, wired before `srv.Start`. `docker-compose.yml` `AUDIO_WORKER_CONCURRENCY: "1"` with a derivation comment tying it to measured peak RSS (~728.4 MiB of 1024 MiB) and cpu-quota fit checks (32-03-SUMMARY.md). |
| 5 | Repeatable `TestAudioConversionE2E` in `internal/e2e/e2e_test.go` (env-gated, non-empty transcript, signed webhook) — USER-AGREED must-have | VERIFIED | Function exists (`internal/e2e/e2e_test.go:1161`), env-gated via `e2eSetup` (`t.Skip` when `E2E_BASE_URL` unset), `assertDownloadIsNonEmptyTranscript` asserts non-emptiness only (Pitfall 9, no exact-string match), `assertSignedWebhook` called. Fixture `internal/e2e/testdata/jfk.wav` committed (352,078 bytes, real file). Live PASS evidence recorded in `32-05-SUMMARY.md` (8.10s). |
| 6 | Code-review fixes (WR-01..05) actually present and tested, not just claimed fixed | VERIFIED | WR-01: `internal/convert/cgroup.go:39-46` uses `strconv.ParseInt` + `<= 0` rejection (Inf/NaN/negative/scientific all fall back); `TestCgroupCPULimit` extended with Inf/NaN/1e300/negative-quota/negative-period/zero-period cases — all PASS. WR-02: `cmd/audio-worker/main.go` `envDurationSeconds` requires `d >= 0` on the `ParseDuration` branch; `TestEnvDurationSeconds` extended with `-5s`/`-30m` cases — PASS. WR-03: `Dockerfile.audio-worker:29-33` pins `WHISPER_COMMIT=f049fff9...` + `git checkout --detach` + `rev-parse HEAD` equality guard. WR-04: `scripts/audio-rtf-measure.sh:91` floor log redirects to `"$WORKDIR/floor_whisper.log"` (trap-cleaned, no host `/tmp` leak). WR-05: `docker-compose.yml:373` `stop_grace_period: 762s` present on `audio-worker`. |

**Score:** 6/6 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `Dockerfile.audio-worker` | Multi-stage build producing audio-worker image, `GGML_NATIVE=OFF` | ✓ VERIFIED | 3 stages present; `GGML_NATIVE=OFF`, `sha256sum -c`, `USER nobody`, `ENTRYPOINT ["/usr/local/bin/audio-worker"]` all present; no `tini`; no `platform:` pin |
| `internal/convert/cgroup.go` | `CgroupCPULimit()` reading `/sys/fs/cgroup/cpu.max`, fail-open | ✓ VERIFIED | `func CgroupCPULimit` present; `parseCPUMax` hardened per WR-01; table-tested (13 cases) |
| `internal/convert/whisper.go` | `SetAudioThreads` setter + `-t` injection in `whisperArgs` | ✓ VERIFIED | `func SetAudioThreads`, `audioThreadCount`, `-t` always appended in `whisperArgs` |
| `scripts/audio-rtf-measure.sh` | Reusable RTF go/no-go measurement gate | ✓ VERIFIED | Contains `cpu.max` read, builds real image, `bash -n` clean, WR-04 fixed |
| `docker-compose.yml` | `audio-worker` service + IN-02 cross-process env consistency | ✓ VERIFIED | `audio-worker` service present; `AUDIO_ENGINE_TIMEOUT: "742s"` × 7; `RECONCILER_ACTIVE_STALE_AFTER: "15m"` × 2, no live `"5m"` config value remaining; `stop_grace_period: 762s` present |
| `internal/e2e/e2e_test.go` | `TestAudioConversionE2E` + `assertDownloadIsNonEmptyTranscript` | ✓ VERIFIED | Both functions present, correctly wired, correct assertions |
| `internal/e2e/testdata/jfk.wav` | Committed audio E2E fixture | ✓ VERIFIED | 352,078 bytes, present, matches `internal/convert/testdata/audio/jfk.wav` |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|----|--------|---------|
| `Dockerfile.audio-worker` runtime stage | `/models/ggml-base.bin` | `COPY --from=whisper-build` | ✓ WIRED | `COPY --from=whisper-build /models/ggml-base.bin /models/ggml-base.bin` present, matches `defaultAudioModelPath` |
| `cmd/audio-worker/main.go` | `convert.SetAudioThreads` | `resolveAudioThreads()` result, before `srv.Start` | ✓ WIRED | Confirmed in source: `convert.SetAudioThreads(threads)` called before `srv := asynq.NewServer(...)` |
| `whisperArgs` | `whisper-cli -t` | `strconv.Itoa(threads)` appended to argv | ✓ WIRED | `args = append(args, "-t", strconv.Itoa(threads))` |
| `scripts/audio-rtf-measure.sh` | `octoconv-audio-worker` image | `docker build -f Dockerfile.audio-worker` | ✓ WIRED | Line 64: `docker build -f Dockerfile.audio-worker -t "$IMAGE_TAG" .` |
| `docker-compose.yml audio-worker` | `Dockerfile.audio-worker` | `build.dockerfile` | ✓ WIRED | `dockerfile: Dockerfile.audio-worker` |
| `.github/workflows/ci.yml` bake targets | `audio-worker` | cache-to/cache-from scope lines | ✓ WIRED | `audio-worker.cache-to`/`cache-from` present in `docker-build`; `cache-from` present in `e2e` job |
| `TestAudioConversionE2E` | compose `audio-worker` service | postJob → audio queue → containerized whisper-cli → presigned download | ✓ WIRED (evidence trail) | Code structurally correct; live pass recorded in 32-05-SUMMARY.md (DB row + container-timing proof it ran through the container, not `go run`) |
| `TestAudioConversionE2E` | `assertSignedWebhook` | webhook-worker signed callback confirmation | ✓ WIRED | Call present at line 1187 |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Go build succeeds | `go build ./...` | exit 0 | ✓ PASS |
| Go vet clean | `go vet ./...` | exit 0 | ✓ PASS |
| gofmt clean | `gofmt -l .` | no output | ✓ PASS |
| Full test suite | `go test ./... -count=1` | all packages `ok` | ✓ PASS |
| RTF script syntax | `bash -n scripts/audio-rtf-measure.sh` | exit 0 | ✓ PASS |
| Compose config valid | `docker compose config` | exit 0, renders `audio-worker` | ✓ PASS |
| `AUDIO_ENGINE_TIMEOUT: "742s"` count | `grep -c 'AUDIO_ENGINE_TIMEOUT: "742s"' docker-compose.yml` | 7 | ✓ PASS |
| `RECONCILER_ACTIVE_STALE_AFTER: "15m"` count | `grep -n RECONCILER_ACTIVE_STALE_AFTER docker-compose.yml` | 2 occurrences, both `"15m"` (residual `"5m"` matches are only in explanatory comments, not config values) | ✓ PASS |
| `stop_grace_period` present | `grep -n stop_grace_period docker-compose.yml` | `stop_grace_period: 762s` on `audio-worker` | ✓ PASS |
| WR-01/WR-02 regression tests | `go test ./internal/convert/... -run TestCgroupCPULimit\|TestWhisperArgs -v` + `go test ./cmd/audio-worker/... -run TestEnvDurationSeconds\|TestResolveAudioThreads -v` | all subtests PASS, including Inf/NaN/negative cases | ✓ PASS |

**Note on live E2E / RTF re-execution:** Per explicit task instruction, the compose stack is currently down and was NOT brought back up to re-run `TestAudioConversionE2E` or `scripts/audio-rtf-measure.sh` live. Verification instead confirmed (a) the code implementing both flows is correct and unchanged since the passing live runs recorded in `32-03-SUMMARY.md`/`32-05-SUMMARY.md`, (b) the evidence trails in those SUMMARYs contain concrete, falsifiable artifacts (DB row IDs, timestamps, raw per-run RTF numbers, container-creation-vs-job-creation ordering) rather than bare narrative claims, and (c) `git log`/`git status` show no uncommitted or reverted changes to the files those runs exercised.

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| AUD-06 | 32-01, 32-02, 32-04, 32-05 | Отдельный `cmd/audio-worker` + `Dockerfile.audio-worker` (whisper.cpp v1.9.1 из исходников multi-stage, `-DGGML_NATIVE=OFF`, модель `base` запечена с пиненным SHA-256, ffmpeg из apt) + `AUDIO_ENGINE_TIMEOUT`/`AUDIO_WORKER_CONCURRENCY`/ShutdownTimeout env + compose-сервис + CI bake matrix | ✓ SATISFIED | `Dockerfile.audio-worker`, `docker-compose.yml` audio-worker service, CI bake matrix, env wiring all confirmed; live E2E evidence trail in 32-05-SUMMARY.md. Note: `REQUIREMENTS.md` checkbox still shows `[ ]`/"Pending" — this is expected pre-verification state; the phase-completion doc commit (which flips it to `[x]`/"Complete") runs after this verification, per the project's established pattern (observed identically for Phases 29-31 in git log). |
| AUD-07 | 32-02, 32-03, 32-04 | RTF (realtime factor) измерен на реальном resource-limited контейнере (measured go/no-go по прецеденту veraPDF Phase 23) до финализации `AUDIO_ENGINE_TIMEOUT` и KEDA cooldown/stabilization | ✓ SATISFIED | `scripts/audio-rtf-measure.sh` + full raw evidence trail in `32-03-SUMMARY.md` (p95=0.205853, N=10, explicit formula, NO-GO lever applied, GO decision at 742s with 17.6% margin); same REQUIREMENTS.md staleness note as above applies. |

No orphaned requirements — REQUIREMENTS.md maps only AUD-06/AUD-07 to Phase 32, both claimed by plans and both traced to evidence above.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| — | — | No `TBD`/`FIXME`/`XXX`/`TODO`/`HACK`/`PLACEHOLDER` markers found in any of the 12 files reviewed by 32-REVIEW.md | — | None |
| `cmd/audio-worker/main.go:61` | 61 | Comment still reads "[ASSUMED] placeholder, Phase 32 re-derives from RTF measurement" even though Phase 32 has completed the re-derivation (742s) | ℹ️ Info | Cosmetic only — this is the code-level env-unset fallback (600s), always overridden by compose/`.env.example`'s `742s`; matches 32-REVIEW.md IN-03 (explicitly not required to fix, Info severity) |
| `.github/workflows/ci.yml:44` | 44 | Comment says "build all 6 compose bake targets" — now 7 with `audio-worker` added | ℹ️ Info | Cosmetic only, does not affect the actual bake matrix (auto-derived from compose `build:` blocks) |
| `cmd/audio-worker/main_test.go:73-78` | ~73 | `TestResolveAudioThreads`'s "unset" subtest doesn't explicitly clear `AUDIO_THREADS` via `t.Setenv("AUDIO_THREADS", "")` | ℹ️ Info | Matches 32-REVIEW.md IN-04 (explicitly not required to fix, Info severity) — theoretical local-dev flakiness only if the developer's shell has `AUDIO_THREADS` set; CI is unaffected |

All four Warning-level findings from 32-REVIEW.md (WR-01 through WR-05) were confirmed FIXED and regression-tested above. All four Info-level findings (IN-01 through IN-04) remain open by design (REVIEW.md explicitly marks only Warnings as requiring fixes) and are non-blocking cosmetic/edge-case items.

### Human Verification Required

None. All must-haves are verifiable from the codebase plus the concrete, falsifiable evidence trails already captured in 32-03-SUMMARY.md and 32-05-SUMMARY.md (DB row IDs, timestamps, raw measurement numbers). The task instructions explicitly directed against re-running the live stack for this verification pass.

### Gaps Summary

No gaps. All 4 ROADMAP success criteria, the USER-AGREED `TestAudioConversionE2E` must-have, and all 5 plans' individual must-haves (truths/artifacts/key-links) are verified present, substantive, and wired. All 5 code-review Warning findings (WR-01..05) are confirmed fixed in the actual source and covered by passing regression tests. `go build`, `go vet`, `gofmt -l`, and the full `go test ./... -count=1` suite all pass clean. The RTF go/no-go decision (742s) and the cgroup-based thread-sizing are backed by an explicit, auditable derivation with raw numbers, not an assumed constant — directly satisfying the phase's core goal statement.

---

*Verified: 2026-07-18T17:30:52Z*
*Verifier: Claude (gsd-verifier)*
