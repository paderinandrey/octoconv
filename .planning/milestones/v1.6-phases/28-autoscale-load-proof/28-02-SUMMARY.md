---
phase: 28-autoscale-load-proof
plan: 02
subsystem: infra
tags: [keda, k8s, load-testing, evidence, uv, python-docx, matplotlib, bash]

# Dependency graph
requires:
  - phase: 28-autoscale-load-proof
    plan: 01
    provides: "values-loadproof.yaml overlay (document scaleDownStabilizationSeconds=15, DOCUMENT_WORKER_CONCURRENCY=1), field-level spec.replicas omission on KEDA-scaled Deployments"
provides:
  - "scripts/fixtures/gen_heavy_docx.py -- calibratable python-docx heavy-fixture generator (--page-units, --out), ephemeral uv run --with python-docx only"
  - "scripts/fixtures/render_evidence.py -- CSV->PNG dual-axis (queue depth + pod count) headless matplotlib renderer, ephemeral uv run --with matplotlib only"
  - "scripts/keda-load-proof.sh -- self-contained Phase 28 live gate: CALIBRATE=1/--calibrate trial-run mode, SC1/SC2 image-class burst-of-20 0->N->0 scenario with CSV+PNG evidence, SC3 document-class downscale-soak with deterministic pod-deletion-cost victim selection and the D-09 triple-check, ALL-PASSED summary"
affects: [28-03]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Ephemeral uv run --with <pkg> for one-off Python evidence/fixture scripts -- python-docx and matplotlib pulled fresh per invocation, never persisted to any dependency manifest"
    - "Separate gate script reusing (not modifying) a prior phase's live-gate helper shapes (assert_eq/assert_nonempty/log, teardown+trap, postJob, waitForReplicasAtLeast/AtMost, live external-metric discovery)"
    - "Redacted assertion helper (assert_nonempty_redacted) for secrets/signed-URLs that must never be echoed into a transcript that gets teed to a committed evidence file"
    - "Continuous background snapshot loop (not a single post-hoc read) for pod state that may be garbage-collected mid-scenario"

key-files:
  created:
    - scripts/fixtures/gen_heavy_docx.py
    - scripts/fixtures/render_evidence.py
    - scripts/keda-load-proof.sh
  modified: []

key-decisions:
  - "keda-load-proof.sh is a wholly separate script from scripts/keda-gate.sh (D-12) -- Phase 27's gate stays byte-unchanged (git diff --quiet verified after every task)"
  - "Burst image fixture: synthesize a ~9MB random-noise PNG via `uv run --with pillow` (fast, no pixel-loop) at gate-run time; falls back to internal/e2e/testdata/sample.png x20 if Pillow-via-uv fails for any reason (D-05 planner discretion)"
  - "SC1/SC2 CSV sampler and SC3 pod-snapshot loop both run as background jobs with explicit PID tracking, killed both explicitly (clean CSV end marker) and via teardown() (D-12: nothing left running after EXIT)"
  - "D-09 SIGTERM timestamp read exclusively from the kubelet's Killing event (never the pod's own deletion-deadline field, which the file contains zero occurrences of, verified via grep -c) per 28-RESEARCH.md Pitfall 2"
  - "assert_nonempty on $CLIENT_KEY and the D-09 presigned $RESULT_URL would echo the raw secret into the committed gate-transcript log (T-28-04) -- fixed by adding assert_nonempty_redacted() which prints only a length marker"

patterns-established:
  - "assert_nonempty_redacted(value, label) -- non-empty check without echoing the value, for any assertion input that could itself be a secret/signed credential"

requirements-completed: [KEDA-03]

# Metrics
duration: ~45min
completed: 2026-07-17
---

# Phase 28 Plan 02: Load-Proof Evidence Tooling Summary

**Three new tools -- a calibratable python-docx heavy-fixture generator, a headless matplotlib CSV-to-PNG dual-axis renderer, and a self-contained 854-line `keda-load-proof.sh` gate implementing the SC1/SC2 image-burst 0->N->0 scenario and the SC3 document-class downscale-soak with deterministic pod-deletion-cost victim selection and the D-09 triple-check -- all statically verified (bash -n, uv smoke runs) and ready for the live in-cluster run in plan 28-03.**

## Performance

- **Duration:** ~45 min
- **Completed:** 2026-07-17
- **Tasks:** 3 completed (Task 1: both Python fixtures; Task 2: gate skeleton + calibration + SC1/SC2; Task 3: SC3 + D-09 triple-check + summary) plus 1 auto-fixed security deviation
- **Files modified:** 3 new files (1064 total lines)

## Accomplishments

- `scripts/fixtures/gen_heavy_docx.py`: python-docx generator with a `--page-units` calibration knob and `--out` path; per unit emits a level-1 heading, a filler paragraph, an 8x6 table, and (every 10th unit) an embedded `internal/e2e/testdata/sample.png`; inserts a TOC field via low-level OXML (`OxmlElement`/`qn`) so LibreOffice does real field layout on conversion. Smoke-verified at `--page-units 3` and `--page-units 10` (heaviness scales with N). Never commits generated `.docx` output -- scratch-path only.
- `scripts/fixtures/render_evidence.py`: headless (`matplotlib.use("Agg")`) CSV->PNG renderer; dual-axis (`twinx()`) plot of `queue_depth` (left) vs. the first `*_replicas` column found (right, defaults to `worker_replicas`), `mdates.DateFormatter` time axis, `dpi=120`. Smoke-verified against a 3-row synthetic CSV, producing a non-empty PNG with no `DISPLAY`.
- `scripts/keda-load-proof.sh` (854 lines): a wholly separate, self-contained gate script (Phase 27's `scripts/keda-gate.sh` stays byte-identical -- verified via `git diff --quiet` after every task). Reuses `keda-gate.sh`'s config/assert/teardown/postJob/`waitForReplicasAtLeast`/`waitForReplicasAtMost`/live-metric-discovery shapes, but installs octoconv layered on `-f values-local.yaml -f values-loadproof.yaml` (28-01's overlay).
  - **CALIBRATE mode** (`CALIBRATE=1` env or `--calibrate` arg, `PAGE_UNITS` env default 300): generates one heavy docx, submits it alone, polls to `done`, prints the observed in-cluster conversion duration via `psql` -- the live trial run D-07 requires (no local `soffice` binary on this host).
  - **SC1/SC2 scenario** (D-01/D-04/D-05/D-06): confirms the image worker at a genuine 0 replicas, live-discovers the external metric name (never hardcoded), starts a ~5s-cadence CSV sampler as a background job (3 samples of steady state before the burst), synthesizes a ~9MB image fixture via `uv run --with pillow` (falls back to `sample.png` x20), fires 20 parallel `postJobPath` submissions, asserts SC1 literally (`waitForReplicasAtLeast worker 2 60`), *records* (not asserts) the observed peak toward `maxReplicaCount=4`, waits for drain + asserts SC2 N->0, stops the sampler cleanly, and renders the D-02 PNG.
  - **SC3 scenario** (D-07/D-08/D-09): confirms document-worker at 0, generates the calibrated heavy docx, submits it FIRST and polls to `status=active`, submits the short `sample.docx` SECOND, waits for document-worker 1->2, identifies pod1 as the earliest-`creationTimestamp` document-worker pod, immediately annotates it `controller.kubernetes.io/pod-deletion-cost=-1000` (before the downscale decision -- the script never issues an imperative pod-deletion command anywhere), runs a continuous background pod1 snapshot loop (Pitfall 3: terminated pods get GC'd -- snapshots are captured throughout the window, not just at the end), waits for the short job to finish and the 2->1 downscale (`waitForReplicasAtMost document-worker 1 120`), then runs the D-09 triple-check: (1) long job reaches `done` and its result downloads; (2) exactly one `queued->active` `job_events` row via `psql`; (3) the kubelet's `Killing` event SIGTERM timestamp (never the pod's own deletion-deadline field -- the file contains zero occurrences of that field name, grep-verified) proven to precede `jobs.finished_at`, with a graceful (`Completed`/exit 0) pod termination. All three checks are persisted to `sc3-timestamps-<ts>.txt` alongside the gate transcript.
  - Sets `GATE_OK=1` and prints an ALL-PASSED summary only after every SC1/SC2/SC3 assertion has passed; teardown (EXIT trap) unconditionally kills the sampler and snapshot-loop PIDs, clears any pod-deletion-cost annotation, and uninstalls both Helm releases (D-12: OrbStack never left hot).

## Task Commits

Each task was committed atomically:

1. **Task 1: python-docx heavy-docx generator + matplotlib CSV->PNG renderer** - `bd14fdc` (feat)
2. **Task 2: keda-load-proof.sh skeleton + calibration mode + SC1/SC2 image-burst sampler scenario** - `36676fd` (feat)
3. **Task 3: keda-load-proof.sh SC3 downscale-soak scenario + D-09 triple-check + ALL-PASSED summary** - `75d3134` (feat)
4. **Deviation fix: redact secrets from the committed gate transcript** - `b3b2219` (fix, Rule 2)

## Files Created/Modified

- `scripts/fixtures/gen_heavy_docx.py` - calibratable heavy-docx generator (python-docx, ephemeral uv)
- `scripts/fixtures/render_evidence.py` - CSV->PNG dual-axis headless renderer (matplotlib, ephemeral uv)
- `scripts/keda-load-proof.sh` - Phase 28 self-contained live gate (SC1/SC2/SC3 + evidence)

## Decisions Made

- `keda-load-proof.sh` is a separate script, not a mode flag on `keda-gate.sh` -- keeps Phase 27's gate untouched and lets the two scenarios (burst vs. downscale-soak) each own their full lifecycle without a shared conditional-mode branch point.
- Burst image fixture generation prefers a fast `uv run --with pillow` random-noise synthesis (`os.urandom` -> `Image.frombytes`, no per-pixel Python loop) over the illustrative-but-slow `putdata` list-comprehension approach initially smoke-tested; falls back to `sample.png` x20 if Pillow can't be pulled.
- `assert_nonempty_redacted()` added as a new helper (not present in `keda-gate.sh`) specifically because this gate's entire run is teed into a *committed* evidence transcript (D-03) -- a risk `keda-gate.sh` never had, since its output was never captured to a repo-committed file.
- SC3's 2->1 downscale wait window set to 120s (`scaleDownStabilizationSeconds=15s` from `values-loadproof.yaml` + generous margin for HPA/KEDA sync propagation), distinct from the calibration target's own `[30s, 250s]` guidance band printed at the end of CALIBRATE mode.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing Critical] Redacted secrets from the committed gate transcript**
- **Found during:** Task 3 (writing the D-09 triple-check, which introduced the first `assert_nonempty`-on-a-URL call and prompted a review of every `assert_nonempty` call site against the plan's own T-28-04 threat mitigation)
- **Issue:** `assert_nonempty()` (copied verbatim from `keda-gate.sh`) echoes its raw value into the "PASS: label == value" line. `keda-load-proof.sh` tees its *entire* run to `$EVIDENCE_DIR/gate-transcript-<ts>.log`, which is committed per D-03 -- unlike `keda-gate.sh`, whose stdout was never captured to a repo file. Calling `assert_nonempty` directly on `$CLIENT_KEY` (the minted API key) or the D-09 presigned `$RESULT_URL` (which may embed a short-lived signature/token) would have leaked those secrets into a permanently committed evidence file, violating the plan's own threat register entry T-28-04 ("never echo $CLIENT_KEY / $DATABASE_URL / $API_KEY_SALT into $EVIDENCE_DIR files").
- **Fix:** Added `assert_nonempty_redacted(value, label)`, identical non-empty check but prints `[REDACTED, N chars]` instead of the raw value. Replaced the two at-risk call sites (`$CLIENT_KEY`, `$RESULT_URL`). Audited every remaining `assert_nonempty` call site (job IDs, pod names, metric names, timestamps) and confirmed none are sensitive.
- **Files modified:** `scripts/keda-load-proof.sh`
- **Verification:** Re-ran the full Task 2 + Task 3 grep/`bash -n` verification suite after the fix -- all checks still pass; manually grepped for any remaining `echo`/assertion of `CLIENT_OUT`/`DATABASE_URL`/`API_KEY_SALT` and found none.
- **Committed in:** `b3b2219`

---

**Total deviations:** 1 auto-fixed (1 missing critical / security)
**Impact on plan:** Necessary for correctness against the plan's own threat model; no scope creep -- the fix is scoped to the assertion helper and its two sensitive call sites only.

## Issues Encountered

None beyond the deviation above. All static verification (Task 1 `uv run` smoke tests, Task 2/3 `bash -n` + literal `grep` checks specified in each task's `<verify>` block) passed on the first or second attempt (comment-only wording had to avoid the literal strings `deletionTimestamp` and `kubectl delete pod` even in explanatory prose, since the verify step greps the whole file -- fixed by rephrasing three header/inline comments without changing their meaning).

## User Setup Required

None - no external service configuration required. This plan builds tooling only; nothing was installed to the cluster, no `docker compose` or live gate run occurred (per this plan's scope boundary -- the live run is plan 28-03).

## Next Phase Readiness

- All three tools are statically verified and ready for plan 28-03 to execute the live in-cluster run: `CALIBRATE=1 PAGE_UNITS=<N> ./scripts/keda-load-proof.sh --calibrate` first (to derive the real page-units for a ~200s conversion), then the full gate.
- `scripts/keda-gate.sh` (Phase 27) confirmed untouched throughout (`git diff --quiet` after every task) -- no regression risk to the existing live gate.
- Open item for 28-03: the exact `PAGE_UNITS` value for the SC3 long job is not yet calibrated against the real in-cluster document-worker (28-RESEARCH.md Pitfall 4 -- no local `soffice`); 28-03's first task is expected to run `CALIBRATE=1` and adjust `PAGE_UNITS` from the observed duration before the full gate run, per this script's own printed guidance ("[30s, 250s]" target band).

---
*Phase: 28-autoscale-load-proof*
*Completed: 2026-07-17*

## Self-Check: PASSED

All created files verified present (scripts/fixtures/gen_heavy_docx.py, scripts/fixtures/render_evidence.py, scripts/keda-load-proof.sh, this SUMMARY.md); all 4 task/deviation commit hashes (bd14fdc, 36676fd, 75d3134, b3b2219) verified present in git log.
