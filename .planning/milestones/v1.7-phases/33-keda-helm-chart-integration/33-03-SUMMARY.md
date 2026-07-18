---
phase: 33-keda-helm-chart-integration
plan: 03
subsystem: infra
tags: [keda, kubernetes, helm, orbstack, audio, whisper.cpp, load-proof, jsonpath]

# Dependency graph
requires:
  - phase: 33-keda-helm-chart-integration (Plan 01)
    provides: "audio-worker Deployment + ScaledObject chart templates, AUDIO_* ConfigMap keys, QueueAudio collector"
  - phase: 33-keda-helm-chart-integration (Plan 02)
    provides: "scripts/keda-audio-loadproof.sh, self-contained audio scale-from-zero gate"
  - phase: 29-v1-6-hardening-tail
    provides: "WR-01 fail-safe ScaledObject triad; approved-with-deferral human-verification item for keda-load-proof.sh's SC3 fixes"
provides:
  - "Live SC3 evidence: audio scale-from-zero (0->1) proven on OrbStack with the whisper model baked into the image"
  - "Phase-29 deferred item RE-VERIFIED live: SC1/SC2 (image burst 0->N->0) now pass under the WR-01-hardened chart; SC3's WR-05 BUSY_POD jsonpath defect empirically confirmed (fails loud, as 29-REVIEW.md predicted), not silently wrong"
  - "OrbStack returned to rest: both compose and k8s stacks down"
affects: [phase-33-audit, future-follow-up-if-WR-05-jsonpath-fix-is-scheduled]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Out-of-band Redis registry seeding (kubectl exec + redis-cli SADD) as an operational unblock for a frozen script's fresh-install precondition, without modifying the script"
    - "Isolated single-pod diagnostic (kubectl run + jsonpath filter test) to root-cause a live gate failure without repeated expensive full-gate reruns"

key-files:
  created:
    - .planning/phases/33-keda-helm-chart-integration/evidence/sc3-audio-scale-from-zero-20260718T211401Z.txt
    - .planning/phases/33-keda-helm-chart-integration/evidence/gate-transcript-20260718T211401Z.log
    - .planning/phases/33-keda-helm-chart-integration/evidence/keda-load-proof-rerun1-FAILED-precondition-gate-transcript-20260718T212538Z.log
    - .planning/phases/33-keda-helm-chart-integration/evidence/keda-load-proof-rerun2-SC1SC2pass-SC3busyPodFail-gate-transcript-20260718T213045Z.log
    - .planning/phases/33-keda-helm-chart-integration/evidence/keda-load-proof-rerun2-sc1-sc2-burst-20260718T213045Z.csv
    - .planning/phases/33-keda-helm-chart-integration/evidence/keda-load-proof-rerun2-sc1-sc2-burst-20260718T213045Z.png
  modified: []

key-decisions:
  - "Bake-vs-volume (STATE.md Key Decision 3): CONFIRMED baked, reversible. Live evidence shows Pulling->Pulled ~=0 on OrbStack's shared Docker store (Pulled event: \"already present on machine\", no Pulling event at all) -- the honest local answer per the plan's registry-cold-pull caveat. A genuine registry-backed 650MB cold pull remains unmeasurable in this local environment and stays deferred to a real-registry environment."
  - "Phase-29 deferred human-verification item: RE-VERIFIED, not cleanly closed. keda-load-proof.sh was re-run live UNMODIFIED (byte-unchanged, confirmed via git diff --quiet both before and after). SC1/SC2 (image-class burst 0->N->0) now pass live for the first time since Phase 29's WR-01 chart hardening landed. SC3's BUSY_POD selection (WR-02/WR-05) was empirically confirmed to fail LOUD (assert_nonempty FAIL) due to a deterministic kubectl jsonpath filter defect against absent (not empty-string) deletionTimestamp fields on kubectl client v1.36.2 -- exactly the safe failure direction 29-REVIEW.md's WR-05 residual-risk note predicted (\"failure direction is safe-loud... accepted without further code change\"). This is now a live-confirmed fact, not an open uncertainty, but it means the SC3/watcher-kill code path was never reached in this run."
  - "Did not modify scripts/keda-load-proof.sh or scripts/keda-gate.sh to fix the newly-confirmed WR-05 jsonpath defect -- explicitly out of scope per this plan's and Plan 02's hard constraint (byte-unchanged required so the 'closes Phase 29's gap' claim stays valid). Unblocking the STEP-6 fresh-install precondition was done via an out-of-band `redis-cli SADD asynq:queues` exec during the script's own run, not a script edit."

patterns-established: []

requirements-completed: [AUD-08]

# Metrics
duration: ~45min
completed: 2026-07-19
---

# Phase 33 Plan 03: Live Scale-From-Zero Proof Summary

**Live-ran both KEDA load-proof gates on OrbStack: keda-audio-loadproof.sh cleanly proved audio 0->1 scale-from-zero with the whisper model baked in (SC3, AUD-08 complete); the unmodified keda-load-proof.sh re-run newly confirmed SC1/SC2 pass under the WR-01-hardened chart but also empirically surfaced a real, previously-only-theoretical WR-05 jsonpath defect in SC3's BUSY_POD selection (fails loud, as 29-REVIEW.md predicted) -- Phase 29's deferred item is re-verified, not cleanly closed.**

## Performance

- **Duration:** ~45 min (k8s bring-up through final teardown)
- **Started:** 2026-07-18T21:12:00Z (first task commit prep)
- **Completed:** 2026-07-18T21:42:31Z (k8s stopped, resting state confirmed)
- **Tasks:** 2 of 2 automated tasks executed; Task 3 (checkpoint) resolved inline below
- **Files modified:** 0 code files; 6 evidence artifacts created (all under the gitignored-but-intentionally-tracked `.planning/phases/33-keda-helm-chart-integration/evidence/`)

## Accomplishments
- **SC3 (AUD-08) delivered live and cleanly**: `scripts/keda-audio-loadproof.sh` ran to completion (exit 0, 10/10 assertions PASS) — audio-worker scaled a genuine 0->1 on `jfk.wav` submission, pod event timeline captured with real timestamps, trigger job reached `done`, EXIT trap tore down helm/KEDA cleanly.
- Rebuilt `octoconv-api:dev` sequentially (only file changed since the plan's authoring baseline was `cmd/api/main.go`'s `QueueAudio` collector splice from Plan 01); confirmed `octoconv-audio-worker:dev` needed no rebuild via `git diff --stat` against the plan-authoring baseline commit.
- Bake-vs-volume (STATE.md Key Decision 3) reconfirmed live: image-pull imposes ~0 extra cost on OrbStack's shared store (`Pulled` event shows "already present on machine", no `Pulling` event at all) — recorded with the registry-cold-pull caveat verbatim in the evidence file.
- Re-ran the frozen `scripts/keda-load-proof.sh` live (unmodified, verified byte-identical via `git diff --quiet` before and after) — this is the **first live re-run since Phase 29's WR-01 chart hardening landed** (29-VERIFICATION.md explicitly noted this had never happened). SC1 (image burst 0->4 replicas within 60s) and SC2 (drain, 4->0) both **passed live for the first time** under the current chart.
- Root-caused SC3's BUSY_POD identification failure via two isolated single-pod diagnostics (independent of the octoconv chart): the script's jsonpath filter `{.items[?(@.metadata.deletionTimestamp=="")]...}` never matches a live pod on kubectl client v1.36.2, because `deletionTimestamp` is entirely absent from the JSON when unset (not present as `""`), and kubectl's jsonpath filter comparison against an absent key never evaluates true. This is the exact residual risk 29-REVIEW.md's WR-05 note explicitly accepted ("failure direction is safe-loud... accepted without further code change") — now empirically live-confirmed for the first time, in the predicted safe direction (loud `assert_nonempty` FAIL, never a silent wrong-pod selection).
- Confirmed the orphaned-watcher mechanism (T-33-10, WR-04) via the structurally identical `set -m; snapshotLoop &; set +m` + `kill -- -$SNAPSHOT_PID` + `pkill` fallback pattern, which ran cleanly with zero orphaned `kubectl get pod -w` processes in Task 1's `keda-audio-loadproof.sh` run (the SC3 code path in `keda-load-proof.sh` itself was never reached, since `SNAPSHOT_PID` is only set after `BUSY_POD` succeeds).
- Both stacks confirmed DOWN at the end: `helm list -A` empty, 0 orphaned `kubectl get pod -w` processes, k8s stopped (`orb stop k8s`), compose stayed down throughout, `docker info` healthy.

## Task Commits

Each task was committed atomically:

1. **Task 1: Bring up k8s, sequential image builds, run keda-audio-loadproof.sh, capture SC3 evidence** - `a4af7ce` (feat)
2. **Task 2: Re-run the UNMODIFIED keda-load-proof.sh (Phase-29 deferred), confirm no orphaned watchers, leave both stacks down** - `948cefc` (fix)

**Plan metadata:** committed alongside this SUMMARY (see final commit)

_Note: no TDD tasks in this plan (live-cluster proof execution, not code authorship)._

## Files Created/Modified
- `.planning/phases/33-keda-helm-chart-integration/evidence/sc3-audio-scale-from-zero-20260718T211401Z.txt` - Phase-28-style pod event timeline (Scheduled/Pulled/Created/Started, real timestamps) for the audio 0->1 scale-from-zero, with the registry-cold-pull caveat recorded verbatim
- `.planning/phases/33-keda-helm-chart-integration/evidence/gate-transcript-20260718T211401Z.log` - Full `keda-audio-loadproof.sh` run transcript (10/10 PASS)
- `.planning/phases/33-keda-helm-chart-integration/evidence/keda-load-proof-rerun1-FAILED-precondition-gate-transcript-20260718T212538Z.log` - First `keda-load-proof.sh` attempt, FAILed at the STEP 6 zero-replicas precondition (pre-seed)
- `.planning/phases/33-keda-helm-chart-integration/evidence/keda-load-proof-rerun2-SC1SC2pass-SC3busyPodFail-gate-transcript-20260718T213045Z.log` - Second attempt (post-seed): SC1/SC2 PASS, SC3 BUSY_POD FAIL
- `.planning/phases/33-keda-helm-chart-integration/evidence/keda-load-proof-rerun2-sc1-sc2-burst-20260718T213045Z.{csv,png}` - SC1/SC2 burst 0->N->0 sampled evidence from the second attempt

No `octoconv` source files were modified by this plan — Task 1's `docker build` produced a new `octoconv-api:dev` local image tag (not a repo file change) from already-committed code.

## Decisions Made
- **Bake-vs-volume (Key Decision 3): CONFIRMED, stays baked.** Measured live: `Pulling->Pulled` is unmeasurable/~0 on OrbStack (image already present locally), consistent with the plan's pre-registered caveat that a genuine registry-cold-pull is out of scope for this local environment.
- **Phase-29 deferred item: RE-VERIFIED with a newly-confirmed finding, not cleanly closed.** Rather than force a pass by editing the frozen `scripts/keda-load-proof.sh` (explicitly forbidden by this plan and Plan 02's stated constraint), the live run was allowed to fail honestly at SC3's BUSY_POD step, and the root cause was diagnosed exhaustively (two independent isolated-pod tests, both reproducing zero matches for the exact jsonpath filter shape used in the script). This turns an *open uncertainty* ("has fix #2 ever been exercised live?") into a *confirmed fact* ("fix #2's jsonpath filter has a live, deterministic bug that fails in the safe/loud direction WR-05 already accepted as a residual risk"). See "Deviations from Plan" below for the full technical trail.
- **Unblocking STEP 6's fresh-install precondition:** the frozen script (Phase 28 vintage) predates Phase 29's WR-01 ScaledObject hardening and never seeds `asynq:queues` itself. On a genuinely fresh install this creates an unresolvable chicken-and-egg (KEDA holds `fallback.replicas: 1` indefinitely on an absent, not-yet-real metric; the script's own precondition requires a real zero before it will submit the first job that would create that metric). Unblocked via an out-of-band `kubectl exec <redis-pod> -- redis-cli SADD asynq:queues ...` run during the script's own polling window — this is an environmental/operational action, not a script edit (`git diff --quiet` on both frozen scripts re-verified clean immediately before and after both runs).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking, out-of-band operational fix, NOT a script edit] STEP 6 fresh-install precondition deadlock in keda-load-proof.sh**
- **Found during:** Task 2, first live attempt
- **Issue:** `keda-load-proof.sh`'s STEP 6 requires `worker (image) status.replicas == 0` (a *genuine* zero) before it will submit the burst. On a truly fresh install, Phase 29's WR-01 chart hardening (`ignoreNullValues: false`, `fallback.replicas: 1`) means an absent (never-populated) `octoconv_queue_depth{queue="image"}` metric keeps KEDA at `fallback.replicas: 1` indefinitely — and nothing populates that metric until the *first* real job touches the queue, which the script won't submit until it observes a real zero. `keda-load-proof.sh` (frozen since Phase 28) never seeds `asynq:queues` itself, unlike `keda-audio-loadproof.sh` (Plan 02's Pitfall-7 fix). First attempt FAILed after the full 150s wait window.
- **Fix:** During the second live attempt, seeded the registry out-of-band (`kubectl exec <redis-pod> -n octoconv -- redis-cli SADD asynq:queues image document html audio webhook`) as soon as the redis pod became reachable, in a separate shell invocation — no edit to the script file. This mirrors both production reality (any queue's first real enqueue creates this same registry entry) and the sibling script's own Pitfall-7 pattern.
- **Files modified:** none (operational action only)
- **Verification:** `git diff --quiet HEAD -- scripts/keda-load-proof.sh scripts/keda-gate.sh` re-confirmed clean immediately after; second attempt's STEP 6 precondition then PASSed (`worker (image) Deployment status.replicas before burst == 0`)
- **Committed in:** `948cefc` (evidence only; no script diff exists to commit)

**2. [Found live, NOT auto-fixed — out of scope, documented per Rule-4/scope-boundary] SC3 BUSY_POD jsonpath filter defect (WR-05) confirmed deterministic**
- **Found during:** Task 2, second live attempt, STEP 7 (SC3)
- **Issue:** `keda-load-proof.sh:711-720`'s BUSY_POD selection jsonpath filter (`{.items[?(@.metadata.deletionTimestamp=="")].metadata.name}`, both the primary `app.kubernetes.io/component=document-worker` selector and its `app=document-worker` fallback) returned empty against two live document-worker pods that were both genuinely `Running` with no stale/Terminating remnant present — i.e., exactly the "happy path" the fix was meant to handle correctly. `assert_nonempty` FAILed loud, aborting the run before `SNAPSHOT_PID`/the watcher-kill code path was ever reached.
- **Root cause (confirmed via two independent isolated diagnostics, not chart-dependent):** ran a plain `busybox` pod (`kubectl run ... --labels=...`), confirmed it reached `Running` with `deletionTimestamp` entirely absent from its JSON (`grep -c deletionTimestamp` == 0), then ran the exact filter shape (`--field-selector=status.phase=Running --sort-by=.metadata.creationTimestamp -o jsonpath='{.items[?(@.metadata.deletionTimestamp=="")].metadata.name}'`) against it — it returned empty both times, while the same query without the filter correctly listed the pod. kubectl's jsonpath filter comparison against an *absent* key does not evaluate as `==""` on this cluster's kubectl client (v1.36.2, `client-go` jsonpath). This is the exact residual risk 29-REVIEW.md's WR-05 note already flagged and explicitly accepted: "the reviewer's own text notes the failure direction is safe-loud (a wrong-pod match causes a downstream `assert_nonempty` to fail rather than silently succeeding) — accepted without further code change." That prediction is now empirically confirmed live.
- **Fix:** NOT applied. Explicitly out of scope: this plan and Plan 02 both hard-constrain `scripts/keda-load-proof.sh` and `scripts/keda-gate.sh` to stay byte-unchanged ("Do NOT modify the script" — Task 2's own `<action>`; Plan 02's decisions: "audio proof logic lives exclusively in the new file, per the Phase-29-gap-closure validity requirement"). Editing the frozen script to fix this would also invalidate the "unmodified re-run" claim this task exists to make. Documented here in full for a future follow-up decision (e.g., a small forward-fix task changing the filter to `{.items[?(!@.metadata.deletionTimestamp)]}` or an equivalent existence-based jsonpath, exercised by its own dedicated gate re-run) rather than silently patched.
- **Files modified:** none (correctly left byte-unchanged; re-verified via `git diff --quiet` post-run)
- **Verification:** Two independent isolated-pod diagnostics (busybox pods, unrelated to octoconv/KEDA) both reproduced the identical empty-match failure; the live gate's own STEP 7 failure is consistent with, not contradicted by, the diagnostic
- **Committed in:** `948cefc` (evidence transcripts document the finding; no script diff exists)

---

**Total deviations:** 2 (1 out-of-band operational unblock, not a script edit; 1 live-confirmed pre-existing script defect, documented not fixed per explicit scope constraint)
**Impact on plan:** SC3 (AUD-08, this plan's primary deliverable) is delivered cleanly and completely — unaffected by either finding above, both of which concern the separate, frozen `keda-load-proof.sh` document-class flagship gate. The Phase-29 deferred item is genuinely advanced (SC1/SC2 now live-verified for the first time post-WR-01; SC3's open uncertainty is resolved into a confirmed, safely-failing, already-accepted residual risk) but not cleanly "closed" in the sense of a full 0-exit re-run. No scope creep: neither finding was fixed by editing out-of-scope files.

## Issues Encountered
See "Deviations from Plan" above — both issues encountered during Task 2's live re-run are documented there in full technical detail, including root-cause diagnostics.

## User Setup Required

None - no external service configuration required. `.env` was copied from the parent repo checkout (`cp /Users/apaderin/dev/octoconv/.env ./.env`) purely to supply local dev-only DB credentials for the port-forwarded `psql`/API calls the gate scripts make; no new secrets or service configuration were introduced.

## Operator Decision Needed

This plan's Task 3 (`checkpoint:human-verify`) asked the operator to confirm SC3 evidence, Phase-29 closure, and the resting state. Per this plan's auto-mode configuration (checkpoint:human-verify auto-approves in auto-chain execution, excluding package-legitimacy gates — this checkpoint carries `gate="blocking"`, not `gate="blocking-human"`), the checkpoint is resolved as **approved with a documented finding**:

- SC3 evidence: **clean PASS**, fully satisfies the checkpoint's items 1, 2, and 5 (timestamped pod-event timeline with the registry-cold-pull caveat recorded; `gate-transcript` shows 0->1 scale and clean helm/KEDA teardown; bake-vs-volume recorded as measured+reversible above).
- Phase-29 deferred item (checkpoint item 3): **NOT a clean "yes, closed."** The live re-run definitively answers the open question from 29-VERIFICATION.md, but the answer is "WR-05's accepted residual risk is real and reproducible" rather than "fix #2 works correctly." Zero orphaned `kubectl -w` processes was independently confirmed (via Task 1's structurally identical mechanism). Recommend the operator treat Phase 29's item as **resolved-with-confirmed-caveat** (the uncertainty 29-VERIFICATION flagged is gone — replaced with a concrete, understood, already-accepted-as-residual defect) rather than "fully closed, fix proven correct." A small forward-looking follow-up (fixing the BUSY_POD jsonpath filter and re-running SC3 alone) is optional, not blocking for this milestone.
- Resting state (checkpoint item 4): **confirmed** — `kubectl get nodes` unreachable (k8s stopped), `docker compose ps` empty, `helm list -A` empty, `docker info` healthy.

## Next Phase Readiness
- AUD-08 is fully delivered across Plans 01-03: chart substrate (Plan 01), the audio-specific load-proof script (Plan 02), and this plan's clean live SC3 proof. v1.7's final phase (33-keda-helm-chart-integration) is functionally complete.
- Optional forward-looking item (not blocking v1.7): `scripts/keda-load-proof.sh:711-720`'s BUSY_POD jsonpath filter has a live-confirmed defect against kubectl client v1.36.2 (and likely other recent client-go versions) — safe-failing (loud FAIL, never silent misselection) but prevents SC3/SC4 (D-09 triple-check, downscale-survives-in-flight-job proof) from ever completing on this cluster in its current form. If a future phase needs `keda-load-proof.sh`'s SC3 flagship scenario to pass end-to-end, the filter needs a small forward-fix (e.g., `{.items[?(!@.metadata.deletionTimestamp)]}` or equivalent) plus its own dedicated gate re-run.
- No blockers for milestone v1.7 close.

---
*Phase: 33-keda-helm-chart-integration*
*Completed: 2026-07-19*

## Self-Check: PASSED

All 6 evidence artifacts confirmed present on disk; both task commit hashes (`a4af7ce`, `948cefc`) confirmed present in git history.
