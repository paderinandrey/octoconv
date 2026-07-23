# Phase 37: KEDA/Helm Chart Integration - Context

**Gathered:** 2026-07-23
**Status:** Ready for planning

<domain>
## Phase Boundary

Ship the `av` engine class's cluster-autoscaling surface with production parity to the other four engine classes (image/document/html/audio): an `av-worker` Deployment + a KEDA `ScaledObject` in the Helm chart, `QueueAV` resolvable by KEDA at genuinely zero replicas, and a **live** scale-from-zero proof plus a downscale-survival proof. This is the final phase of milestone v1.8. It consumes Phase 36's measured `AV_ENGINE_TIMEOUT=753s` for all timing derivations.

**In scope:** `deployment-av-worker.yaml`, `scaledobject-av.yaml`, `values.yaml` av blocks (keda.av + avWorker), confirming `QueueAV` is collected at zero replicas, IN-02 `AV_*` env parity across all queue-client services in the chart, and the live load-proof evidence (scale-from-zero + N→N-1 downscale survival).
**Out of scope:** re-deriving `AV_ENGINE_TIMEOUT` (locked in Phase 36); any new av-engine capability; changes to the other four engine classes' KEDA config; per-engine reconciler-threshold overrides (deferred debt, see STATE.md AudioUniqueTTL decision).
</domain>

<decisions>
## Implementation Decisions

This phase is a deliberate near-verbatim clone of the audio KEDA precedent (Phase 33, which mirrored Phase 27), with `audio`→`av` substitutions and Phase-36-measured timing. The decisions below lock the few av-specific numbers; everything else is "clone the audio template."

### av capacity (KEDA trigger sizing)
- **D-01:** `keda.av.threshold = "1"` — one queued task per replica, matching `AV_WORKER_CONCURRENCY=1` (an av-worker processes exactly one job at a time; threshold=1 keeps replica count == outstanding-job count). Same as audio.
- **D-02:** `keda.av.maxReplicaCount = 2` — parity with audio, honoring the "production parity to the other four engine classes" goal. Conservative given av pods are heavier per-pod (2 cpu / 1g / concurrency=1); `maxReplicaCount` and `threshold` are values.yaml knobs, overridable in load-proof/prod overlays without a template change. No evidence yet justifies diverging from audio — divergence, if ever, belongs to a later capacity-tuning phase, not this parity phase.
- **D-03:** `keda.av.cooldownPeriod = 180`, `keda.av.pollingInterval = 15` — clone audio verbatim.

### Timing derivation (from Phase 36's measured `AV_ENGINE_TIMEOUT=753s`)
- **D-04:** `avWorker.terminationGracePeriodSeconds = 783` — carries the audio formula exactly: `ENGINE_TIMEOUT + 30s margin` (audio: 742→772; av: 753→783). Grace ≥ engine timeout guarantees a genuine N→N-1 downscale never premature-SIGTERMs a live transcode (SC4).
- **D-05:** `keda.av.scaleDownStabilizationSeconds = 900` — clone audio's non-null value. Rationale identical to audio: the worst-case job duration (753s) exceeds the k8s 300s HPA default, so this knob is load-bearing in production (not just a load-proof overlay). 753s < 900s (~19.5% margin), aligned with the 900s/15m `RECONCILER_ACTIVE_STALE_AFTER` cap — a downscale never races a live job. Gated on BOTH `hasKey` AND `ne … nil` (falsy-0 guard, HARD-03/D-06), same as audio.

### WR-01 fail-safe triad (applied verbatim from the first commit — non-negotiable)
- **D-06:** `ignoreNullValues: "false"` (sustained absent PromQL result reads as scaler ERROR, not queue-empty — D-01 in audio) + `fallback: {failureThreshold: 3, replicas: 1}` (holds one replica on outage instead of false scale-to-zero with a live backlog) + retry-inclusive PromQL `sum(octoconv_queue_depth{queue="av", state=~"pending|active|retry"})` (D-03 — retry-state tasks are imminent work). Co-dependency guard: gate the ScaledObject on BOTH `keda.enabled` AND `prometheus.enabled`, mirroring `scaledobject-audio.yaml`/`scaledobject-image.yaml`.

### Load-proof bar (SC3 + SC4, live/operator-run, Phase-33/28-style)
- **D-07:** Two distinct live proofs, both with timestamped evidence:
  1. **Scale-from-zero (SC3):** enqueue an av backlog while av-worker is at 0 replicas → KEDA resolves the backlog via the always-on api collector → scales 0→1→…→N → drains → back to 0. Capture timestamped kubectl/Prometheus evidence (mirrors Phase 28/33).
  2. **Downscale survival (SC4, load-bearing):** with an in-flight LONG av transcode, trigger a genuine N→N-1 HPA downscale and prove the job completes gracefully (no exit-137/SIGKILL mid-job) — this is the test that validates `terminationGracePeriodSeconds=783s`. Not a synthetic unit test; a real cluster observation.

### Claude's Discretion
- Exact template mechanics (label blocks, `nindent`, ConfigMap/Secret env wiring) follow the existing audio templates verbatim — planner/executor mirror `deployment-audio-worker.yaml` + `scaledobject-audio.yaml` structure.
</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### KEDA/Helm precedent to clone (audio Phase 33)
- `deploy/chart/octoconv/templates/scaledobject-audio.yaml` — the exact ScaledObject to clone (WR-01 triad, retry-inclusive PromQL, falsy-0 guard, co-dependency gate). av version swaps `audio`→`av`.
- `deploy/chart/octoconv/templates/deployment-audio-worker.yaml` — the exact Deployment to clone; note the `terminationGracePeriodSeconds` wiring + the grace-formula header comment (742→772).
- `deploy/chart/octoconv/templates/scaledobject-image.yaml` — the co-dependency-guard header reference cited by the audio template.
- `deploy/chart/octoconv/values.yaml` (audio block ~L188-198) — `keda.audio` + `audioWorker` value shape to mirror as `keda.av` + `avWorker`.

### Collector / queue wiring (SC2 — largely already satisfied)
- `internal/queue/queue.go:607` — `AllConvertQueues()` ALREADY includes `QueueAV`; the api queue-depth collector already sums the av queue. SC2 is mostly done — Phase 37 confirms KEDA resolves it at zero replicas, not re-implements it.
- `internal/metrics/queue_collector.go` — `octoconv_queue_depth` collector (always-on, api process).
- `internal/queue/queue_test.go:555` — `TestAllConvertQueuesCoversEveryEngine` (D-06 completeness guard) already covers av.

### Measured input (Phase 36, locked)
- `.env.example` (AV_ENGINE_TIMEOUT / AV_MAX_DURATION_SECONDS block) — the 753s/90s finalized values driving D-04/D-05.
- `.planning/phases/36-containerization-rtf-measured-timeout/36-04-SUMMARY.md` — the RTF derivation + go/no-go record.

### Prior KEDA CONTEXT (locked decisions to honor)
- `.planning/milestones/v1.6-phases/27-keda-autoscaling/27-CONTEXT.md` — original KEDA autoscaling decisions (WR-01 triad origin).
- `.planning/milestones/v1.6-phases/28-autoscale-load-proof/28-CONTEXT.md` — the live load-proof evidence pattern for SC3/SC4.
</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- The audio KEDA templates (`scaledobject-audio.yaml`, `deployment-audio-worker.yaml`) are direct clone sources — av differs only in name, queue label, and the two measured timing values (783s grace, 900s stabilization — same as audio) plus capacity knobs (max=2, threshold=1 — same as audio).
- `AllConvertQueues()` already emits av → the always-on collector already exposes `octoconv_queue_depth{queue="av"}`; no collector code change expected (verify only).

### Established Patterns
- Per-engine chart templates (one `scaledobject-<engine>.yaml` + one `deployment-<engine>-worker.yaml` per class) — add the av pair.
- Falsy-0 guard (`hasKey` AND `ne … nil`) on the stabilization block; co-dependency gate (`keda.enabled` AND `prometheus.enabled`) — copy verbatim.
- IN-02 env parity: every queue.NewClient()-constructing service must carry identical `AV_*` env — already established in Phase 36's compose; confirm the chart's ConfigMap/Secret wiring matches.

### Integration Points
- `values.yaml` keda.* + <engine>Worker.* blocks; the api Deployment's always-on collector (no change expected); the av-worker Deployment's env from the shared ConfigMap/Secret.
</code_context>

<specifics>
## Specific Ideas

Clone the audio precedent verbatim; the only av-specific numbers are the two Phase-36-measured timing values (grace 783s, stabilization 900s) and the capacity knobs (max 2, threshold 1) — all locked in decisions above. Live load-proof is operator-run (live cluster), mirroring Phase 28/33.
</specifics>

<deferred>
## Deferred Ideas

- **av-specific capacity tuning** (maxReplicaCount / threshold diverging from audio to reflect the heavier 2cpu/1g av pod) — deferred; no evidence yet justifies divergence from parity. Revisit in a future capacity-tuning phase if av backlog behavior warrants it.
- **Per-engine reconciler staleness threshold** (`RECONCILER_ACTIVE_STALE_AFTER` is global 900s) — a Config-shape change, deferred as noted in STATE.md's AudioUniqueTTL decision.
- **MEDIUM code-review debt from Phase 36** (av-worker env-parser unit tests; wav-demuxer justification in Dockerfile.av-worker) — tracked in `36-REVIEW.md`; belongs in a v1.8 tech-debt tail, not this phase.
</deferred>

---

*Phase: 37-keda-helm-chart-integration*
*Context gathered: 2026-07-23*
