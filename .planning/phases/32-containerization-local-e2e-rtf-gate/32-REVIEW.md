---
phase: 32-containerization-local-e2e-rtf-gate
reviewed: 2026-07-18T17:19:05Z
depth: standard
files_reviewed: 12
files_reviewed_list:
  - Dockerfile.audio-worker
  - internal/convert/cgroup.go
  - internal/convert/cgroup_test.go
  - internal/convert/whisper.go
  - internal/convert/whisper_test.go
  - cmd/audio-worker/main.go
  - cmd/audio-worker/main_test.go
  - scripts/audio-rtf-measure.sh
  - docker-compose.yml
  - .github/workflows/ci.yml
  - .env.example
  - internal/e2e/e2e_test.go
findings:
  critical: 0
  warning: 5
  info: 4
  total: 9
status: issues_found
---

# Phase 32: Code Review Report

**Reviewed:** 2026-07-18T17:19:05Z
**Depth:** standard
**Files Reviewed:** 12
**Status:** issues_found

## Summary

Reviewed the Phase 32 deliverables: the 3-stage `Dockerfile.audio-worker`, cgroup v2 CPU-limit detection (`internal/convert/cgroup.go`), the `-t` thread injection into `whisperArgs`, the RTF measurement script, compose/CI/env wiring, and `TestAudioConversionE2E`.

**Invariant cross-check (requested):** verified. `AUDIO_ENGINE_TIMEOUT: "742s"` appears exactly 7 times in `docker-compose.yml` (api, worker, webhook-worker-1, webhook-worker-2, document-worker, chromium-worker, audio-worker) and matches `.env.example:51`. `RECONCILER_ACTIVE_STALE_AFTER` is `15m` in both webhook-worker services and `.env.example:82`; no residual `5m` anywhere (also confirmed absent from `docker-compose.e2e.yml`). 742s < 900s (15m) holds. `AUDIO_MAX_RETRY: "3"` is present in all 7 services. `AUDIO_MAX_DURATION_SECONDS=1800` matches between compose (audio-worker) and `.env.example`.

Also verified clean: the Dockerfile's model checksum chain (`curl && sha256sum -c -` in one RUN — a mismatch fails the layer), `USER nobody` before ENTRYPOINT, no secrets, apt-list cleanup in every stage; `whisperArgs`' `-t` injection is argv-correct and pinned by `TestWhisperArgs`; the nearest-rank p95 awk math in the RTF script is correct for all N (verified the `n*0.95` float-rounding direction cannot produce an off-by-one, and the `print (n<1) ? 1 : n` awk ternary parses correctly — tested live); the CI bake matrix entry for `audio-worker` matches the sibling scopes in both tiers; `TestAudioConversionE2E` correctly env-gates, uses the committed `internal/e2e/testdata/jfk.wav` (present, 352 KB), a 5-minute bound, and no exact-transcript assertion; `cmd/audio-worker/main.go` structurally mirrors `cmd/document-worker/main.go` including the `SetAudioModelPath`/`SetAudioThreads` happens-before-`srv.Start` ordering, and the `worker.NewHandler` 10-argument wiring is positionally correct.

No Critical findings. Five Warnings (parser robustness, a silent negative-ceiling config path, a supply-chain pin gap, a script cleanup defect, and an unreachable graceful-shutdown window) and four Info items follow.

## Warnings

### WR-01: parseCPUMax accepts Inf/NaN/negative/scientific inputs; "Inf" yields (MaxInt64, true)

**File:** `internal/convert/cgroup.go:35-47`
**Issue:** cgroup v2 `cpu.max` fields are integers, but `parseCPUMax` parses both fields with `strconv.ParseFloat`, which additionally accepts `"Inf"`, `"NaN"`, negatives, and scientific notation. Verified live with the exact function body:

```
parseCPUMax("Inf 100000")     => (9223372036854775807, true)
parseCPUMax("1e300 100000")   => (9223372036854775807, true)
parseCPUMax("NaN 100000")     => (1, true)
parseCPUMax("-200000 100000") => (1, true)
parseCPUMax("200000 -100000") => (1, true)
```

`int(+Inf)` is an implementation-dependent conversion in Go (MaxInt64 on darwin/arm64 and linux/amd64), so an `Inf`-shaped quota would be handed to `whisper-cli -t 9223372036854775807`. The doc comment's contract ("Any other unparseable shape also falls back") is violated for these shapes. Exploitability is effectively nil today — `/sys/fs/cgroup/cpu.max` is kernel-controlled — but the parser is exported package machinery and its contract is wrong for a whole input class. The test table (`cgroup_test.go`) also omits the zero-period and negative-period cases the phase's focus called out (zero period IS handled correctly by the `period == 0` check, but is untested).
**Fix:**
```go
quota, err := strconv.ParseInt(fields[0], 10, 64)
if err != nil || quota <= 0 {
    return 0, false
}
period, err := strconv.ParseInt(fields[1], 10, 64)
if err != nil || period <= 0 {
    return 0, false
}
n := int(quota / period) // floor
```
And add table cases `{"zero period falls back", "200000 0", 0, false}`, `{"negative period falls back", "200000 -100000", 0, false}`, `{"Inf quota falls back", "Inf 100000", 0, false}` to `TestCgroupCPULimit`.

### WR-02: envDurationSeconds silently accepts a negative Go-duration, producing a ceiling that terminally rejects every audio job

**File:** `cmd/audio-worker/main.go:208-222`
**Issue:** The bare-integer branch correctly rejects negatives (`sec >= 0`, pinned by the `"negative bare seconds falls back"` test case), but the `time.ParseDuration` branch runs first and happily returns negative values: `AUDIO_MAX_DURATION_SECONDS="-5s"` or `"-30m"` parses and is returned as a negative duration with **no warning** — the exact silent-fallback failure mode this function's own doc comment says is "unacceptable for a fail-closed resource guard." Downstream, `enforceAudioGuardBeforeConvert` → `EnforceMaxDuration` (`internal/convert/audioduration.go:102`) does `if d > max` with no non-positive-max guard, so any probed duration `d >= 0` exceeds a negative max: **every audio job is terminally rejected** with `ErrAudioDurationExceeded` (classified terminal, never retried). Fail-closed, so no data corruption — but a one-character typo (`-30m` vs `30m`) becomes a silent, total audio-engine outage with a misleading per-job error instead of the designed startup warning.
**Fix:**
```go
if d, err := time.ParseDuration(f); err == nil && d >= 0 {
    return d
}
```
(letting negative durations fall through to the existing logged-warning fallback), plus a `{"negative duration falls back", "-5s", true, def}` case in `TestEnvDurationSeconds`.

### WR-03: whisper.cpp source pinned by mutable git tag, not commit hash — the compiled binary has no integrity anchor

**File:** `Dockerfile.audio-worker:21-22`
**Issue:** `git clone --depth 1 --branch v1.9.1 https://github.com/ggml-org/whisper.cpp.git` pins a **tag**, and git tags are mutable — a force-pushed `v1.9.1` silently changes every byte of the `whisper-cli` binary and shared libs baked into the runtime image. The Dockerfile's own comment invokes "pinned-tag discipline (verapdf/chromium/MinIO)" — but those are pre-built registry images where the tag is the only available pin; this is the repo's **only from-source build**, where a stronger, free pin exists. Notably the same RUN block treats HuggingFace's mutable `main` pointer as untrustworthy and SHA-256-pins the model — the source code compiled into the executable deserves at least the same treatment.
**Fix:**
```dockerfile
ARG WHISPER_COMMIT=<full 40-char sha of the v1.9.1 tag's commit>
RUN git clone https://github.com/ggml-org/whisper.cpp.git /whisper \
 && git -C /whisper checkout --detach "${WHISPER_COMMIT}"
```
(or `git fetch --depth 1 origin "${WHISPER_COMMIT}"` to keep the shallow clone).

### WR-04: RTF script's WORKDIR is dead — the cleanup trap removes an empty dir while the floor log leaks into host /tmp

**File:** `scripts/audio-rtf-measure.sh:47-48,88`
**Issue:** `WORKDIR=$(mktemp -d)` is created at line 47 and removed by the EXIT trap, but **never referenced anywhere else in the script** — all real work happens in `/tmp/work` inside the container. Meanwhile line 88 redirects the floor run's whisper output to `>/tmp/floor_whisper.log` on the **host**, which the trap does not clean: the artifact that was evidently meant to live in `$WORKDIR` leaks into host `/tmp` on every run, and the cleanup trap's `rm -rf "$WORKDIR"` deletes an always-empty directory. The trap's container-removal half (`docker rm -f`) is correct.
**Fix:** `docker exec "$CONTAINER" whisper-cli ... >"$WORKDIR/floor_whisper.log" 2>&1 || true` — or drop `WORKDIR` entirely and write the log inside the container (`/tmp/work/floor_whisper.log`), where it dies with the container.

### WR-05: compose audio-worker has no stop_grace_period — the 752s asynq ShutdownTimeout is unreachable (SIGKILL at docker's 10s default)

**File:** `docker-compose.yml:360-412`, `cmd/audio-worker/main.go:103-114`
**Issue:** `cmd/audio-worker` deliberately sets `asynq.Config.ShutdownTimeout = AUDIO_ENGINE_TIMEOUT + 10s` (752s) so "a genuinely long in-flight whisper-cli transcription survives SIGTERM instead of being aborted+requeued" — but the compose service defines no `stop_grace_period`, so `docker compose stop/down/restart` sends SIGKILL **10 seconds** after SIGTERM (docker's default). The 752s graceful window is dead configuration under the only deployment this phase ships: any transcription longer than ~10s at shutdown is killed mid-flight, the job stays `active`, and recovery waits on the 15m reconciler sweep. The sibling workers share this gap (pre-existing), but audio has by far the longest engine budget, making the mismatch between the code's documented intent and the actual kill window largest here — and this phase authored both sides.
**Fix:** add to the `audio-worker` service:
```yaml
    stop_grace_period: 755s
```
(≥ ShutdownTimeout + margin), or explicitly document in the compose comment that compose-local shutdown is best-effort-10s and the 752s window targets the future k8s `terminationGracePeriodSeconds` only.

## Info

### IN-01: Floor-fixture RTF divides by the speech fixture's probed duration, not the floor fixture's own

**File:** `scripts/audio-rtf-measure.sh:86-91`
**Issue:** `FLOOR_RTF` divides the sine-tone run's wall clock by `FIXTURE_ACTUAL_DURATION_S` — the ffprobe-measured duration of `fixture.wav` (looped speech), not of `floor.wav`. Both are requested at the same `FIXTURE_DURATION_S`, so the error is negligible today, but the denominator is semantically wrong and would diverge if either generation command changes.
**Fix:** probe `/tmp/work/floor.wav` into its own `FLOOR_ACTUAL_DURATION_S` and use that.

### IN-02: Script's "max" quota branch derives -t 1; the Go code it claims to match falls back to NumCPU

**File:** `scripts/audio-rtf-measure.sh:73`
**Issue:** The awk derivation prints `1` when `cpu.max` reads `max` (unlimited), while `CgroupCPULimit`/`resolveAudioThreads` — which the comment says this "matches ... exactly" — fall back to `runtime.NumCPU()` in that case. Unreachable today (the container is always started with `--cpus`), but if the limit flags were ever dropped, the script would measure RTF at `-t 1` while production ran at `-t NumCPU`, silently invalidating the derived timeout.
**Fix:** fail loudly instead: `{ if ($1 == "max") { print "ERROR" } ... }` and abort when the container unexpectedly has no CPU quota.

### IN-03: Stale "[ASSUMED] placeholder" comment on the 600s default — Phase 32 has since derived 742s

**File:** `cmd/audio-worker/main.go:61`
**Issue:** `envDuration("AUDIO_ENGINE_TIMEOUT", 600*time.Second)` still carries the Phase-31 comment "[ASSUMED] placeholder, Phase 32 re-derives from RTF measurement" — that re-derivation happened (742s, `32-03-SUMMARY.md`), so the comment now describes completed work as future. The env-unset code default (600s, also used at line 113 for ShutdownTimeout) is 19% tighter than the measured-safe 742s; compose/.env.example always override it, but a bare `go run ./cmd/audio-worker` gets a budget below the derived floor with no hint.
**Fix:** update the comment to reference the RTF-derived 742s (and consider aligning the code default), e.g. `// default deliberately below the RTF-derived 742s (32-03-SUMMARY.md); compose/.env.example set the derived value`.

### IN-04: TestResolveAudioThreads' "unset" subtests don't clear ambient AUDIO_THREADS

**File:** `cmd/audio-worker/main_test.go:68-76`
**Issue:** The "unset falls through past env override" subtest asserts fall-through without calling `t.Setenv("AUDIO_THREADS", "")` — if the developer's shell (or a sourced `.env`, per the README's `set -a && . ./.env` flow, where `.env.example` ships an `AUDIO_THREADS=` line an operator may fill in) has `AUDIO_THREADS` set to a positive value, this subtest fails spuriously despite correct code.
**Fix:** add `t.Setenv("AUDIO_THREADS", "")` as the first line of the "unset" subtest to force the empty-env branch deterministically.

---

_Reviewed: 2026-07-18T17:19:05Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
