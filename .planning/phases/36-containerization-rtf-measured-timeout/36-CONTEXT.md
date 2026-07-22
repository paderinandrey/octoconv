# Phase 36: Containerization & RTF-Measured Timeout - Context

**Gathered:** 2026-07-22
**Status:** Ready for planning

<domain>
## Phase Boundary

Ship a running `av-worker` container in docker-compose that passes a full live E2E, and replace the provisional `AV_ENGINE_TIMEOUT=600s [ASSUMED]` with a value derived from a measured RTF matrix across the closed `AVOpts` opts space. Plus the two resource axes containerization forces: a new disk-space/ephemeral-storage guard, and cgroup-derived thread/RAM sizing. Requirement: AVE-04.

**Not in this phase:** Helm chart and KEDA ScaledObject (Phase 37). The av-worker Go binary already exists (Phase 35).
</domain>

<decisions>
## Implementation Decisions

### FFmpeg version strategy

- **D-01: Build the latest stable ffmpeg 8.x from pinned source in a dedicated Docker build stage — NOT `apt-get install ffmpeg` (Debian bookworm's 5.1.x).** Operator directive 2026-07-22: security is a primary emphasis, use the latest versions wherever possible. This resolves the standing conflict between ROADMAP §36 SC1 (which said "Debian apt 5.1.x with CVE backports") and the newer STATE.md Key Decision ("pin ffmpeg ≥8.1.2"). The STATE.md decision + the operator directive win; the ROADMAP SC1 text is superseded on the version-source point (its *intent* — a version-pinned, CVE-clean ffmpeg, verified fail-loud — is preserved, just satisfied by source-build instead of apt).
  - **This is the established project convention, not a novel risk.** `Dockerfile.audio-worker` already builds its engine (whisper.cpp) from a **commit-hash-pinned** source in a throwaway build stage, with `-DGGML_NATIVE=OFF` for cross-arch portability and a `rev-parse` guard that fails the build if the pinned ref ever moves. Mirror that shape exactly for ffmpeg.
  - **Pin discipline:** resolve the exact latest-stable 8.x release tag via `git ls-remote` at Dockerfile-authoring time, pin by the resolved commit/tag, and add a `rev-parse`/checksum guard so the build FAILS if the tag is later force-moved (same rationale as whisper's WR-03 commit-pin and the SHA-256 model pin). Record the resolution date inline, exactly as the whisper `WHISPER_COMMIT` comment does.
  - **RTF validity depends on this.** The entire 34/35 codebase was developed and tested against host ffmpeg 8.1.2; an RTF matrix measured on a 5.1.x container would not validate the shipped code's real behavior. Same-major-version (8.x) keeps the go/no-go measurement meaningful.

- **D-02: CVE-2026-8461 (PixelSmash decoder RCE) posture — closed by building patched upstream, no longer an accepted risk.** STATE.md previously flagged this as accepted-risk/tech-debt pending a version pin. Building the latest stable 8.x from source inherently includes the upstream fix, so this phase *retires* the accepted risk rather than deferring it. The plan must still verify (fail-loud) that the pinned release is at/after the fix, and note that ongoing advisory tracking (this project has no dependency-advisory process) remains an open operational follow-up — the pin is a point-in-time fix, not a durable process.

- **D-03: Keep the runtime base image `debian:bookworm-slim`, consistent with the whole worker fleet.** STACK.md records that every runtime container is bookworm-slim running as `USER nobody`; the av-worker follows suit. The ffmpeg CVE is addressed by the from-source engine build (D-01), independent of the base image, so a base bump is not required for this phase's security goal. A fleet-wide base bump (bookworm → trixie) for general security hygiene is a **separate, cross-cutting task** — flagged in Deferred Ideas, not smuggled into this phase where it would touch all nine Dockerfiles.

### RTF measurement (the go/no-go gate)

- **D-04: Clone `scripts/audio-rtf-measure.sh` → `scripts/av-rtf-measure.sh`.** That script is Phase 32's proven RTF gate (itself a clone of Phase 23's `verapdf-measure.sh`). Adapt for video:
  - The matrix spans the codec × resolution × preset combinations the closed `AVOpts` allowlist actually exposes (H.264/HEVC × 480/720/1080 × the transcode presets) — **not a single fixture**, because unlike whisper's roughly content-invariant RTF, video RTF varies across this space (STATE.md Key Decision, the reason opts-allowlist closing was sequenced before this measurement).
  - Fixtures are synthesized **inside the container** via ffmpeg `lavfi` (`testsrc` + `sine`, and/or looping a small committed source) to the target duration — never a committed large binary, matching the audio script and the av_test.go fixture convention.
  - The script gates **measurement integrity only** (build/container/fixture/pipeline must succeed); the GO/NO-GO decision on the *derived* `AV_ENGINE_TIMEOUT` is made separately from the printed p95 RTF, exactly as Phase 32 did.
  - **NO-GO lever, pre-agreed:** if the derived timeout would breach the 900s `RECONCILER_ACTIVE_STALE_AFTER` cap, apply Phase 32's documented lever — lower `AV_MAX_DURATION_SECONDS` (the duration ceiling) rather than inflate the timeout past the reconciler cap or raise the global cap. Do NOT silently raise the timeout toward 900s.

- **D-05 (SUPERVISED): the RTF measurement run and the go/no-go acceptance of the derived timeout require the operator at the Docker daemon.** Two reasons: (1) OrbStack has wedged its daemon four times on record in this project (v1.6/v1.7) when compose and k8s ran hot together or images built carelessly — the plan must pre-build sequentially with non-`latest` tags and never run compose + k8s simultaneously; (2) the go/no-go on a production timeout is a safety judgment on measured numbers, not an autonomous default. Everything up to and including a *built* image is autonomous; pressing "run the measurement" and accepting its number is operator-gated.

### New resource axes

- **D-06: Disk-space/ephemeral-storage guard — genuinely new, no codebase precedent.** Confirmed by grep: no `Statfs`/`Bavail`/ephemeral check exists anywhere in `internal/`. Video decode/transcode writes large intermediate + output files; the guard must fail-closed BEFORE transcode when free ephemeral space is below a sized threshold, mirroring the fail-closed-before-expensive-work discipline of the existing duration/resolution guards. Size the threshold explicitly from the container's ephemeral storage and the 2 GiB upload ceiling, not by analogy to the CPU/RAM guards (which don't cover disk).

- **D-07: cgroup-derived thread/RAM sizing reuses `CgroupCPULimit()`.** `internal/convert/cgroup.go`'s `CgroupCPULimit()` (cgroup v2 `cpu.max`, floored quota/period, fails open on v1/host) already exists and `av.go:343-348` already references it for ffmpeg `-threads`. This phase wires the container's real ceiling through and adds RAM/concurrency sizing, mirroring `AUDIO_THREADS`/`AUDIO_WORKER_CONCURRENCY`'s "measured, not assumed" provenance (peak RSS from cgroup v2 `memory.peak`, companion observation from the RTF script).

### Compose service, CI bake, env parity

- **D-08: The `av-worker` compose service + CI bake matrix entry land here, and `AV_ENGINE_TIMEOUT`/`AV_MAX_RETRY` propagate identically across every `queue.NewClient()`-constructing service (IN-02).** This is the compose-parity item explicitly deferred from Phase 35 (docker-compose was consistent-by-default there because no service set `AV_*`; the moment the measured timeout is set on one service it must be set on all, or `AVUniqueTTL` diverges between enqueuer and consumer → silent duplicate processing). ShutdownTimeout = measured `AV_ENGINE_TIMEOUT + 10s` on the av-worker service.

### Claude's Discretion

- Exact ffmpeg configure flags / codec libraries to enable (must cover the AVOpts allowlist: libx264, libx265, libvpx-vp9, libopus, aac/faststart; disable what's not needed to shrink the image and attack surface).
- The disk-guard threshold formula and env var name.
- The RTF matrix's exact fixture durations and run count (mirror the audio script's `RUNS`/`CPUS` knobs).

### Open questions for research

- **Latest stable ffmpeg 8.x release + its exact commit/tag** to pin (resolve via `git ls-remote` at authoring time; verify it's at/after the CVE-2026-8461 fix). Host has 8.1.2 — confirm whether a newer 8.x stable exists to target.
- **Build weight vs. image size:** a full-codec ffmpeg source build is heavy. Research whether a `--disable-everything` + selective `--enable-*` (only the allowlist codecs) build is materially smaller/faster and reduces attack surface, and whether any codec lib itself needs a version pin for CVE reasons.
- **Cross-arch:** whisper used `-DGGML_NATIVE=OFF` to stay portable across the OrbStack arm64 build host and amd64 runtime. Determine the ffmpeg equivalent (avoid `--enable-native`/`-march=native`; ffmpeg has runtime CPU dispatch by default — confirm no build flag defeats it).

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Direct precedents to clone
- `Dockerfile.audio-worker` — the from-pinned-source engine build pattern (commit pin, `-DGGML_NATIVE=OFF` portability, `rev-parse` fail-loud guard, throwaway build stage COPYing one artifact into the slim runtime). D-01 mirrors this for ffmpeg.
- `scripts/audio-rtf-measure.sh` — the RTF go/no-go measurement gate to clone (D-04); its header documents the exact methodology (RTF metric, in-container synthesized fixture, cgroup-derived `-t`, measurement-integrity-only exit code, separate go/no-go).
- `internal/convert/cgroup.go` (`CgroupCPULimit`) — cgroup v2 sizing precedent (D-07); already referenced by `av.go:343-348`.

### Prior-phase artifacts binding on this phase
- `.planning/phases/34-av-engine-foundation/34-SECURITY.md` — AVE-02 invariant (every ffmpeg/ffprobe invocation protocol-whitelisted) must not regress when containerized.
- `.planning/phases/35-queue-worker-routing-integration/35-CONTEXT.md` §D-06/D-07 — the AllConvertQueues collector and two-tier upload ceiling this phase's compose env must stay consistent with.
- `.planning/STATE.md` §Decisions/Deferred — the disk-guard, ffmpeg-pin, and RTF-sequencing Key Decisions; AVE-04 requirement text in `.planning/REQUIREMENTS.md`.
- `.planning/ROADMAP.md` §Phase 36 — the four success criteria (SC1 apt-5.1.x wording is superseded by D-01; the rest stand).

### Audio precedent this phase parallels end to end
- Phase 32 (`.planning/phases/*32*`) — the audio engine's containerize + RTF-measured-timeout phase; `AUDIO_ENGINE_TIMEOUT` went 600s placeholder → 742s measured, with `AUDIO_MAX_DURATION_SECONDS` lowered as the NO-GO lever to stay under the 900s reconciler cap. This phase is its video analog.

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `CgroupCPULimit()` (`internal/convert/cgroup.go`) — reuse verbatim for `-threads` sizing; already wired into `av.go`.
- `scripts/audio-rtf-measure.sh` + `scripts/verapdf-measure.sh` — two generations of the measure-script pattern to clone.
- The whisper-from-source build stage in `Dockerfile.audio-worker` — structural template for the ffmpeg-from-source stage.
- `AVUniqueTTL` / `AV_ENGINE_TIMEOUT` env plumbing (Phase 35, `internal/queue/client.go`, `cmd/av-worker/main.go`) — the consumers of the measured value; env parity (D-08) must cover every `queue.NewClient()` caller.

### Established Patterns
- Runtime containers: `debian:bookworm-slim`, `USER nobody`, `CGO_ENABLED=0` static Go binary + a COPYed engine artifact.
- "Measured, not assumed" resource sizing (Phase 32): timeout, concurrency, and threads all derived from a live in-container measurement, never a guessed constant.
- NO-GO lever discipline: when a derived timeout breaches the 900s reconciler cap, lower the duration ceiling, never the cap.

### Integration Points
- `docker-compose.yml` — new `av-worker` service; `AV_ENGINE_TIMEOUT`/`AV_MAX_RETRY` added to EVERY queue-client service (IN-02), not just av-worker.
- CI bake matrix — new av-worker image entry.
- No disk guard exists anywhere — D-06 is net-new surface in `internal/convert` (and/or the worker guard stage).

</code_context>

<deferred>
## Deferred Ideas

- **Fleet-wide base-image bump (debian bookworm → trixie) for general security hygiene** — considered under the "latest versions" directive but scoped OUT of Phase 36: it touches all nine Dockerfiles and is orthogonal to the av-worker's CVE goal (which D-01's from-source ffmpeg already addresses). Worth a dedicated cross-cutting security-hygiene task.
- **A dependency-advisory-tracking process** — the ffmpeg pin (D-02) is a point-in-time CVE fix, not a durable process; the project has no advisory-monitoring today. Flagged as an operational follow-up, not this phase.

</deferred>

---

*Phase: 36-Containerization & RTF-Measured Timeout*
*Context gathered: 2026-07-22 (operator directive: security-first, latest versions)*
