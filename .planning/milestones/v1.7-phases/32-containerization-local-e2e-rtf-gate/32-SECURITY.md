---
phase: 32
slug: containerization-local-e2e-rtf-gate
status: verified
threats_open: 0
asvs_level: 1
created: 2026-07-18
---

# Phase 32 — Security

> Per-phase security contract: threat register, accepted risks, and audit trail.

---

## Trust Boundaries

| Boundary | Description | Data Crossing |
|----------|-------------|---------------|
| build host → upstream (HuggingFace, GitHub) | Model + whisper.cpp source pulled over network at build time; untrusted until pinned | Model bytes, source tree |
| runtime container → host | Worker shells out to whisper-cli/ffmpeg on untrusted audio input; runs as `nobody` | Subprocess argv/exec |
| container → shared CPU quota | Over-threading whisper-cli beyond the cgroup quota causes CFS throttling / starves co-scheduled work | cgroup cpu.max reads |
| env/config → engine argv | AUDIO_THREADS/-t is operator-set process env, never client-derived | Process env → argv |
| measurement env → production sizing | An unrepresentative RTF fixture/arch yields a timeout that under/over-sizes the real budget | RTF measurement → AUDIO_ENGINE_TIMEOUT |
| derived timeout → reconciler invariant | A timeout ≥ RECONCILER_ACTIVE_STALE_AFTER reopens spurious recovery of long in-flight jobs | Compose env values |
| enqueuing process ↔ enqueuing process | audioUniqueTTL is derived independently per process from AUDIO_ENGINE_TIMEOUT/AUDIO_MAX_RETRY; drift = double-processing window | Compose env consistency |
| compose config → deployable stack | A missing/mis-typed service block leaves audio jobs with no consumer (IN-04) | docker-compose.yml |
| test client → live API | E2E exercises the real public HTTP surface + presigned S3 download + webhook receiver | HTTP/S3/webhook |
| non-deterministic engine output → test assertion | ASR transcript content varies run-to-run (Pitfall 9) | Transcript bytes |

---

## Threat Register

| Threat ID | Category | Component | Disposition | Mitigation | Status |
|-----------|----------|-----------|-------------|------------|--------|
| T-32-SC | Tampering (supply chain) | HuggingFace `ggml-base.bin` fetch | mitigate | `sha256sum -c -` fail-closed check, `Dockerfile.audio-worker:47-51` | closed |
| T-32-01 | Tampering (supply chain) | whisper.cpp source pin | mitigate (upgraded from accept) | `ARG WHISPER_COMMIT=f049fff...` + `git checkout --detach` + `rev-parse HEAD` equality guard, `Dockerfile.audio-worker:29-33` (WR-03) | closed |
| T-32-02 | Elevation of Privilege | Runtime container executing whisper-cli/ffmpeg on adversarial audio | mitigate | `USER nobody`, `Dockerfile.audio-worker:72` | closed |
| T-32-03 | Denial of Service | `-march=native` SIGILL / mis-threading on runtime host | mitigate | `-DGGML_NATIVE=OFF`, `Dockerfile.audio-worker:40`; explicit `-t` ceiling (see T-32-04) | closed |
| T-32-04 | Denial of Service | whisper-cli defaulting to host core count under a cgroup CPU quota | mitigate | `CgroupCPULimit()`/`parseCPUMax` floor(quota/period), `internal/convert/cgroup.go:31-65`; wired via `whisperArgs` `-t`, `internal/convert/whisper.go:193-206`; `TestCgroupCPULimit`/`TestWhisperArgs` green | closed |
| T-32-05 | Denial of Service | `AUDIO_THREADS` operator override oversubscription | accept | Documented in Accepted Risks Log below; `.env.example:64-76` | closed |
| T-32-06 | Tampering | Client bytes reaching the `-t` argv element | mitigate | `-t` sourced only from `audioThreadCount()` (env/cgroup/NumCPU, never request input), `strconv.Itoa`, `internal/convert/whisper.go:204` | closed |
| T-32-07 | Denial of Service | RTF-derived AUDIO_ENGINE_TIMEOUT operationally absurd against a fixed reconciler CAP | mitigate | Explicit NO-GO lever applied (AUDIO_MAX_DURATION_SECONDS 14400s→1800s), CAP read from deployed compose (15m/900s), derived 742s < CAP with 17.6% margin — `32-03-SUMMARY.md` "AUDIO_ENGINE_TIMEOUT Derivation" | closed |
| T-32-08 | Repudiation | Sizing decision with no auditable basis | mitigate | Raw per-run ms + RTF, sorted, N/rank/p95, peak memory, image size, arch caveat all recorded — `32-03-SUMMARY.md` "Raw Measurement Evidence" (live evidence, not documentation-only) | closed |
| T-32-09 | Denial of Service | Over-permissive concurrency × threads starving the shared container | mitigate | Measured peak-RSS (~728.4 MiB/1g) + cpu-quota fit check both fail for concurrency=2 → AUDIO_WORKER_CONCURRENCY=1 measured not assumed — `32-03-SUMMARY.md` "AUDIO_WORKER_CONCURRENCY Derivation" (live evidence) | closed |
| T-32-10 | Tampering | Divergent AUDIO_ENGINE_TIMEOUT across processes → stale audioUniqueTTL → double-processing | mitigate | `grep -c 'AUDIO_ENGINE_TIMEOUT:' docker-compose.yml` == 7 (verified live), all identical `"742s"` values | closed |
| T-32-11 | Denial of Service | API accepts audio jobs with no compose consumer (IN-04) | mitigate | `audio-worker` service block present, `docker-compose.yml:360-419` | closed |
| T-32-12 | Repudiation | `.env.example` advertising an `[ASSUMED]` placeholder as tuned | mitigate | No `ASSUMED` string in `.env.example` (verified via grep); measured value + derivation comment at `.env.example:51` | closed |
| T-32-16 | Denial of Service | Stale 5m `RECONCILER_ACTIVE_STALE_AFTER` on webhook-worker-1/2 < AUDIO_ENGINE_TIMEOUT | mitigate | `grep -c 'RECONCILER_ACTIVE_STALE_AFTER: "5m"' docker-compose.yml` == 0; `"15m"` count == 2 (verified live); 742s < 900s cross-check recorded in `32-04-SUMMARY.md` | closed |
| T-32-13 | Repudiation | Flaky exact-transcript assertion masking real regressions | mitigate | `assertDownloadIsNonEmptyTranscript` — structural non-emptiness only (`bytes.TrimSpace(body) == 0`), `internal/e2e/e2e_test.go:1196-1214` | closed |
| T-32-14 | Spoofing | Unsigned/forged webhook accepted as delivery confirmation | mitigate | `assertSignedWebhook` HMAC-SHA256 validation reused unchanged, `internal/e2e/e2e_test.go:763-` (called at line 1187 in `TestAudioConversionE2E`) | closed |
| T-32-15 | Tampering | Test passing against a local `go run` worker instead of the containerized service (false green) | mitigate | Live run evidence: `docker ps` shows `octoconv-audio-worker` container created 31s before job `created_at`; container logs show asynq processing the job at the matching timestamp — `32-05-SUMMARY.md` "Database confirmation the job ran through the CONTAINERIZED worker" (live evidence, container-uptime DB proof) | closed |

*Status: open · closed*
*Disposition: mitigate (implementation required) · accept (documented risk) · transfer (third-party)*

---

## Additional Hardening Beyond Plan-Time Register

- **WR-05 (`stop_grace_period: 762s`)**: `docker-compose.yml:373` — not a plan-time threat entry, but closes a real gap discovered during code review: `cmd/audio-worker` sets `asynq.Config.ShutdownTimeout = AUDIO_ENGINE_TIMEOUT + 10s` (752s) so a genuine in-flight transcription survives SIGTERM, but without a matching compose `stop_grace_period`, Docker's default 10s SIGKILL window made that 752s graceful window unreachable — any transcription still running at shutdown was killed mid-flight regardless of the code's intent. Fixed by setting `stop_grace_period: 762s` (752s + 10s margin), verified rendered by `docker compose config` (12m42s). This directly supports the reconciler-recovery-latency assumptions underlying T-32-16's invariant (a killed-mid-flight job now correctly reaches the 15m reconciler sweep rather than being silently truncated well inside that window).

## Unregistered Flags

None — `32-REVIEW.md`'s five Warnings (WR-01 through WR-05) all map to declared threats or close infrastructure gaps directly supporting a declared threat's intent (WR-01→T-32-06/T-32-04 parser robustness, WR-02→T-32-07 timeout-derivation integrity, WR-03→T-32-01, WR-04→T-32-08 evidence-trail integrity, WR-05→ noted above as additional hardening). The four Info items (IN-01 through IN-04) are measurement-script/test-robustness nits with no new attack surface and are not threat-register gaps.

---

## Accepted Risks Log

| Risk ID | Threat Ref | Rationale | Accepted By | Date |
|---------|------------|-----------|-------------|------|
| AR-32-01 | T-32-05 | `AUDIO_THREADS` is an operator-set process env (never client-derived, per T-32-06's mitigation), documented in `.env.example:64-76` as an escape hatch that "should match the thread count used for the phase's RTF measurement — changing it after the fact invalidates that measurement's timeout/concurrency sizing." The safe default (unset → cgroup floor, never oversubscribing) is what ships; an operator who overrides it and mis-sizes it accepts the consequence of self-inflicted CPU throttling, not a security compromise reachable by any external actor. | Phase 32 plan authors (32-02-PLAN.md threat_model) | 2026-07-18 |

*Accepted risks do not resurface in future audit runs.*

---

## Security Audit Trail

| Audit Date | Threats Total | Closed | Open | Run By |
|------------|---------------|--------|------|--------|
| 2026-07-18 | 15 | 15 | 0 | secure-phase agent |

---

## Sign-Off

- [x] All threats have a disposition (mitigate / accept / transfer)
- [x] Accepted risks documented in Accepted Risks Log
- [x] `threats_open: 0` confirmed
- [x] `status: verified` set in frontmatter

**Approval:** verified 2026-07-18
