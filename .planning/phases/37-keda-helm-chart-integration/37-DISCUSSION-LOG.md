# Phase 37 Discussion Log

**Date:** 2026-07-23
**Mode:** discuss (default)

## Framing

Phase 37 identified as a near-verbatim clone of the audio KEDA precedent (Phase 33 → Phase 27). Carried forward without re-asking: WR-01 fail-safe triad (verbatim), retry-inclusive PromQL, `QueueAV` already in `AllConvertQueues()` (SC2 largely satisfied since Phase 35), per-engine template clone pattern, and the audio grace formula (`ENGINE_TIMEOUT + 30s`).

## Gray areas presented

1. av capacity — maxReplicas + threshold (clone audio vs tune for heavier av pod)
2. Timing derivation — grace + scaleDownStabilization from measured 753s
3. Load-proof bar — SC3/SC4 evidence rigor
4. (option) "clone audio verbatim"

**User selection:** all four (including "clone verbatim") — interpreted as "make the sensible decisions on all three, default to the audio clone with av substitutions, and proceed." No deep-dive turns run.

## Decisions captured (see 37-CONTEXT.md for full text)

- **D-01/D-02/D-03 (capacity):** threshold=1 (matches AV_WORKER_CONCURRENCY=1), maxReplicaCount=2 (parity with audio), cooldownPeriod=180, pollingInterval=15 — clone audio.
- **D-04 (grace):** terminationGracePeriodSeconds=783 (753 + 30s margin, audio formula).
- **D-05 (stabilization):** scaleDownStabilizationSeconds=900 (clone audio; 753 < 900 cap, load-bearing, falsy-0 guarded).
- **D-06 (WR-01 triad):** ignoreNullValues:"false" + fallback replicas:1 + retry-inclusive PromQL for queue="av", co-dependency gate — verbatim.
- **D-07 (load-proof):** two live operator-run proofs — scale 0→N→0 (SC3) + long-transcode survives N→N-1 downscale without SIGTERM (SC4).

## Deferred

- av-specific capacity divergence from audio parity (future capacity phase).
- Per-engine reconciler staleness threshold (Config-shape change, deferred).
- Phase 36 MEDIUM review debt (av-worker env-parser tests; wav-demuxer justification) — v1.8 tech-debt tail.
