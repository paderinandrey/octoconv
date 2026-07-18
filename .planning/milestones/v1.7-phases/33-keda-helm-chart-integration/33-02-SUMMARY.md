---
phase: 33-keda-helm-chart-integration
plan: 02
subsystem: infra
tags: [keda, kubernetes, helm, bash, audio, whisper.cpp, orbstack]

# Dependency graph
requires:
  - phase: 29-v1-6-hardening-tail
    provides: process-group + pkill watcher-kill discipline (WR-01/WR-02/WR-03), reused verbatim in the new teardown trap
  - phase: 32-audio-worker-containerization-rtf-gate
    provides: AUDIO_ENGINE_TIMEOUT=742s (measured RTF gate), so this script needs no calibration/trial-run mode
provides:
  - scripts/keda-audio-loadproof.sh — self-contained, single-purpose live-cluster gate proving audio scale-from-zero with a timestamped kubectl pod-event-timeline (Scheduled->Pulling->Pulled->Created->Started)
affects: [33-03-live-execution, phase-33-audit]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "kubectl get events --field-selector reason=<Reason> -o jsonpath='{.items[0].firstTimestamp}' for absolute (not relative-age) pod lifecycle timestamps"
    - "watch-based (kubectl get pod -w --output-watch-events) capture instead of periodic polling, to avoid missing short-lived pull/create transitions"

key-files:
  created: [scripts/keda-audio-loadproof.sh]
  modified: []

key-decisions:
  - "No trial-run/pre-measurement mode: AUDIO_ENGINE_TIMEOUT=742s already comes from Phase 32's RTF gate, so unlike scripts/keda-load-proof.sh this script has exactly one run mode"
  - "Trigger fixture is internal/e2e/testdata/jfk.wav (11s, already committed) rather than a synthesized heavy fixture — the measurement here is pod-lifecycle timing, not engine throughput"
  - "scripts/keda-load-proof.sh and scripts/keda-gate.sh left byte-unchanged (git diff --quiet verified) — audio proof logic lives exclusively in the new file, per the Phase-29-gap-closure validity requirement"

patterns-established:
  - "Pattern: kubectl-describe-pod event-timeline evidence extraction (podEventTimestamp helper + portable BSD/GNU date epoch conversion) for separating image-pull time from orchestration time on a scale-from-zero event"

requirements-completed: [AUD-08]

# Metrics
duration: ~20min
completed: 2026-07-18
---

# Phase 33 Plan 02: Author keda-audio-loadproof.sh Summary

**New self-contained scripts/keda-audio-loadproof.sh: structural clone of scripts/keda-load-proof.sh, narrower (no trial-run mode) and with new scope (timestamped kubectl pod-event-timeline capture for the audio class's scale-from-zero proof), statically verified — the two frozen sibling scripts remain byte-unchanged.**

## Performance

- **Duration:** ~20 min
- **Completed:** 2026-07-18T21:00:37Z
- **Tasks:** 2
- **Files modified:** 1 (created)

## Accomplishments
- Authored `scripts/keda-audio-loadproof.sh` (566 lines): self-contained KEDA install, `values-local.yaml` overlay, EXIT-trap teardown, refuses to run if the docker-compose stack is up
- Triggers the audio class with the committed `internal/e2e/testdata/jfk.wav` (11s) fixture and captures a Phase-28-style timestamped `kubectl describe pod` + `kubectl get events` event timeline (Scheduled -> Pulling -> Pulled -> Created -> Started), including pull-duration and orchestration-duration deltas
- Seeds `SADD asynq:queues image document html audio webhook` before expecting a genuine zero (Pitfall 7 — audio included in the seed set)
- Watcher teardown discipline: `kill -- -"$SNAPSHOT_PID"` (process-group) + `pkill -f "kubectl get pod ... -w"` belt-and-suspenders, both in the inline stop point and the EXIT trap (Phase-29 WR-01/WR-02/WR-03 pattern)
- Confirmed `scripts/keda-load-proof.sh` and `scripts/keda-gate.sh` remain byte-unchanged (`git diff --quiet HEAD` passed) — the new audio proof logic lives exclusively in the new file

## Task Commits

Each task was committed atomically:

1. **Task 1: Author scripts/keda-audio-loadproof.sh** - `84bb3da` (feat)
2. **Task 2: Assert the two frozen scripts remain byte-unchanged** - verification-only, no file changes to commit (confirmed via `git diff --quiet HEAD -- scripts/keda-load-proof.sh scripts/keda-gate.sh`, exit 0)

**Plan metadata:** committed alongside this SUMMARY (see final commit)

## Files Created/Modified
- `scripts/keda-audio-loadproof.sh` - New self-contained live-cluster gate: installs KEDA + octoconv via `values-local.yaml`, seeds the asynq queue registry, submits `jfk.wav`, waits for `audio-worker` 0->1, captures a timestamped pod-event timeline into `.planning/phases/33-keda-helm-chart-integration/evidence/sc3-audio-scale-from-zero-<ts>.txt`, tears down unconditionally via an EXIT trap.

## Decisions Made
- Skipped the optional CSV/PNG burst-sampler evidence artifact (`scripts/fixtures/render_evidence.py`) — this gate has no burst/replica-count time series to render; only the single-pod event-timeline is in scope for AUD-08. Evidence output is the gate transcript log plus the `sc3-audio-scale-from-zero-*.txt` timeline file, matching the Phase-28 naming convention for the artifact types this script actually produces.
- Used distinct local ports (`API_LOCAL_PORT=18092`, `DB_LOCAL_PORT=15436`) from both `scripts/keda-gate.sh` and `scripts/keda-load-proof.sh` so this gate can never collide with a concurrently-running sibling script during Plan 03's live execution.
- `AUDIO_POD` identification uses `kubectl get pod -l app.kubernetes.io/component=audio-worker --field-selector=status.phase!=Failed -o jsonpath='{.items[0]...}'` rather than the document-class's earliest-creationTimestamp disambiguation logic — a true scale-from-zero trigger produces exactly one candidate pod, so the document-worker script's multi-pod tie-breaking logic (needed there because of a lingering old-generation pod) is unnecessary here.

## Deviations from Plan

None - plan executed exactly as written. The verify gate's `! grep -qi 'CALIBRATE'` constraint was honored by phrasing the "no trial-run mode" explanation without ever using the literal substring "calibrate"/"CALIBRATE" (used "trial run" / "pre-measurement" / "measurement gate" instead) — this is a wording choice within the plan's own explicit instruction, not a deviation from it.

## Issues Encountered
None. `shellcheck` is not installed in this environment, so only `bash -n` static syntax verification was run (as permitted by the environment notes: "shellcheck if available"). All required grep-based verification gates from the plan's `<verify>` blocks passed.

## User Setup Required
None - no external service configuration required. This script requires a live OrbStack Kubernetes cluster to execute (deferred to Plan 03); no setup is needed to merely author and statically verify it.

## Next Phase Readiness
- `scripts/keda-audio-loadproof.sh` is ready for Plan 03's live execution once the chart templates from Plan 33-01 (`deployment-audio-worker.yaml`, `scaledobject-audio.yaml`, ConfigMap/values additions) land — the script references the `audio-worker` Deployment and its label selector, which do not yet exist in the chart as of this plan's completion (Plan 33-01 runs concurrently in a separate worktree).
- No blockers for Plan 03. The script is self-verifying only at the syntax/static level here; actual SC3 proof (the live scale-from-zero event timeline) is produced when Plan 03 runs it against a real cluster.

## Self-Check: PASSED

- FOUND: scripts/keda-audio-loadproof.sh
- FOUND commit: 84bb3da
- FOUND: .planning/phases/33-keda-helm-chart-integration/33-02-SUMMARY.md

---
*Phase: 33-keda-helm-chart-integration*
*Completed: 2026-07-18*
