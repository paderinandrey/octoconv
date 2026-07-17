# Phase 28: Autoscale Load-Proof - Research

**Researched:** 2026-07-17
**Domain:** Kubernetes HPA/KEDA scale-down timing, pod-deletion-cost, docx-generation for calibrated LibreOffice load, local evidence tooling
**Confidence:** MEDIUM-HIGH (the two load-bearing timing questions are HIGH confidence, verified against official docs; docx-calibration guidance is MEDIUM — no live LibreOffice available on this host to time-test)

## Summary

This is a short, targeted research pass for a phase whose infrastructure is already built (Phase 24/27). Five specific unknowns were investigated. The single most important finding, **load-bearing for the whole SC3 scenario (D-07/D-08/D-09)**, is that the phase's own working assumption about downscale timing is incomplete: KEDA's `cooldownPeriod` (120s for the document class) governs **only** the final 1→0 transition. The intermediate 2→1 step that D-08's scenario actually exercises is handled entirely by the **standard Kubernetes HPA**, whose default `scaleDown.stabilizationWindowSeconds` is **300s** — and this repo's ScaledObjects do not set `advanced.horizontalPodAutoscalerConfig.behavior`, so that 300s default applies. A ~200s long-job budget calibrated against the 120s cooldown is not safely calibrated against the real gating mechanism, and in the worst case the downscale event could fire *after* the long job's own `DOCUMENT_ENGINE_TIMEOUT` (300s) budget has already elapsed — which would silently invalidate SC3's "downscale hits an in-flight job" precondition. The fix does not touch any locked decision: add a values-driven, gate-only override of `scaleDown.stabilizationWindowSeconds` (e.g. 10-15s) so the 2→1 transition happens close to the HPA sync/KEDA polling granularity, leaving the long job's ~200s duration comfortably inside both ends of the window this phase already intends (D-07).

Second: `controller.kubernetes.io/pod-deletion-cost` is GA since Kubernetes 1.26 (removed as a feature gate in 1.27, always-on since), so it is live on the OrbStack v1.34.8+orb1 cluster with no extra setup. It works exactly as D-08 assumes (lower cost = deleted first, best-effort, ReplicaSet-scoped) with one real caveat: it must be **set before the scale-down decision is made**, which the gate script already satisfies by design (annotate the busy pod immediately after it's identified, before the metric/HPA drop that triggers the scale-in).

Third and fourth: docx-generation and evidence-timestamp reading both have clean, dependency-free (`uv`-ephemeral) solutions verified live on this machine — `uv run --with python-docx` and `uv run --with matplotlib` both work with zero pre-installed Python packages. `gnuplot` is not installed; matplotlib via `uv` is the available path for D-02's PNG. Reading SIGTERM/exit timestamps is fully doable with `kubectl events` (the kubelet's `Killing` event is the authoritative SIGTERM-sent timestamp — `pod.deletionTimestamp` is the *deadline*, not the SIGTERM time, and conflating the two is a common mistake) plus `job_events`/`jobs.started_at/finished_at` in Postgres, reachable through the same DB port-forward the gate already opens.

**Primary recommendation:** Extend `scripts/keda-gate.sh` (or a new `scripts/keda-load-proof.sh` reusing its helpers) with (1) a CSV sampler loop, (2) a `uv run --with matplotlib` PNG renderer, (3) a burst-of-20 image submitter, (4) a `uv run --with python-docx` heavy-docx generator calibrated by one live trial run inside the cluster, and (5) — the one net-new chart change beyond WR-02 — a values-gated `scaleDown.stabilizationWindowSeconds` override on the document ScaledObject so the 2→1 downscale timing is deterministic for the test, not dependent on the Kubernetes 300s default.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Burst job submission (20x image) | Test/Evidence tooling (gate script, host) | API tier (accepts jobs) | Gate script is the load generator; API/worker/queue tiers are unchanged production code paths being *observed*, not modified |
| CSV/PNG evidence capture | Test/Evidence tooling (gate script, host) | — | Pure observability artifact production; no application code involved |
| Pod-deletion-cost annotation write | Test/Evidence tooling (gate script, via kubectl) | Kubernetes control plane (ReplicaSet controller consumes it) | Gate sets the annotation; K8s native controller acts on it — no chart/app change |
| HPA scale-down timing control | Kubernetes control plane / Helm chart (ScaledObject) | — | `advanced.horizontalPodAutoscalerConfig.behavior` is a KEDA CR field the chart must render; this is infra config, not app code |
| Heavy docx fixture generation | Test/Evidence tooling (one-off script, host, run via `uv`) | — | Fixture generation for a test scenario; deliberately NOT Go application code, no new Go module dependency |
| Graceful shutdown behavior under SIGTERM | Worker tier (`cmd/document-worker`, already built Phase 24/27) | — | Existing `ShutdownTimeout`/`terminationGracePeriodSeconds` invariant is being *validated*, not changed, in this phase |
| Chart `spec.replicas` omission (WR-02) | Helm chart (Deployment templates) | — | In-scope point chart fix per D-10; templating-only change |

## User Constraints (from CONTEXT.md)

<user_constraints>

### Locked Decisions

- **D-01:** CSV sampler: gate writes a row every ~5s (ISO timestamp, queue_depth per state via PromQL, ready-replica count per Deployment via kubectl) across the whole steady→burst→drain→zero scenario. CSV is the primary evidence.
- **D-02:** After the run, render a PNG from the CSV: queue depth and pod count as two series on one time axis (gnuplot or python/matplotlib — whichever is locally available; tool choice is the planner's discretion, PNG output is mandatory).
- **D-03:** Evidence artifacts are committed to `.planning/phases/28-autoscale-load-proof/evidence/` (CSV, PNG, timestamped gate log) alongside the SUMMARY — this IS the "proven with timestamps" deliverable; it must survive the session.
- **D-04:** Burst of 20 identical image jobs (png→jpg, parallel curl) submitted while the worker is at TRUE zero (preconditions: replicas=0, queue empty, external metric readable — same as the Phase 27 gate). At threshold=5/maxReplicas=4, HPA targets 4 replicas; the gate literally asserts SC1 (≥2 replicas within 60s), and reaching 4 is recorded as a fact in evidence, not asserted.
- **D-05:** Burst is image-class only — SC1 only concerns image; doc/html 0→1 was already proven in Phase 27; a multi-class burst would smear the timing on a shared VM. Fixture size should keep the queue from draining before the scale-up fires (size is a planner detail, calibrated).
- **D-06:** N→0 leg (SC2): after the queue drains, the worker returns to 0 within the cooldown window — the same sampler captures time-to-drain and time-to-zero.
- **D-07:** Long job = a generated heavy docx (hundreds of pages/tables) targeting ~200s of LibreOffice conversion time on this VM; the generator is calibrated with one trial run; the final duration must have margin on BOTH ends: noticeably > cooldown, and < `DOCUMENT_ENGINE_TIMEOUT` 300s.
- **D-08:** SC3 scenario: 2 document jobs (short + long) scale document-worker 0→2 (threshold=1); the short one finishes, signal (pending+active) drops to 1 → KEDA/HPA downscales 2→1. Victim determinism: the gate sets `controller.kubernetes.io/pod-deletion-cost` with a LOW value on the BUSY pod (low cost = deleted first) BEFORE the downscale fires — the downscale itself remains a genuine KEDA event; the annotation only influences pod selection. Do NOT use `kubectl delete pod` (not a real KEDA downscale; SC3 requires literally "survives a KEDA downscale event").
- **D-09:** SC3 proof is a triple check: (1) job reaches `done` and the result downloads; (2) `job_events` contains exactly one `queued→active` transition (no false retry); (3) timestamps confirm pod SIGTERM occurred BEFORE job completion, and the pod terminated before `terminationGracePeriodSeconds` elapsed (i.e., graceful, not SIGKILL). Relies on the per-class `ShutdownTimeout` 310s from 27-01.
- **D-10:** WR-02 is in scope for Phase 28: don't render `spec.replicas` on scaled-class Deployments when `keda.enabled && prometheus.enabled` (else every `helm upgrade` resets a scaled-to-zero class back to 1; the 27-03 gate already works around this — simplify/remove the workaround after the fix). Chart fix + offline render check + upgrade-idempotency check.
- **D-11:** WR-01 (api outage → empty PromQL read as 0 under `ignoreNullValues: true` → downscale with a live backlog) is NOT in this phase: separate work with trigger-semantics tradeoffs; risky to change right before the flagship acceptance run. Backlog/next milestone.
- **D-12:** OrbStack discipline from Phase 24/27 unchanged: sequential pre-builds, compose and k8s never hot simultaneously, gate is self-contained (installs KEDA itself, tears down via EXIT trap). Load-proof gate extends/reuses the `scripts/keda-gate.sh` pattern (separate script or extension — planner's discretion; the Phase 27 gate must remain working as-is).

### Claude's Discretion

- Sampler step (~5s), PNG-render tool, exact image-fixture size, docx-generator parameters
- Separate `scripts/keda-load-proof.sh` vs. a mode in the existing gate
- Evidence-log format; filenames under `evidence/`
- Exactly how to read SIGTERM/termination timestamps (kubectl events vs. pod status vs. worker logs)

### Deferred Ideas (OUT OF SCOPE)

- WR-01: empty-PromQL-result semantics during an api outage (`ignoreNullValues` vs. false triggers) — backlog/next milestone (D-11)
- Production tuning of threshold/cooldown based on load-proof results — out of phase; record observations in SUMMARY as input for the future
- kube-state-metrics/full observability stack — still disproportionate

</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| KEDA-03 | Live load-proof: burst load through the API → observable 0→N→0 scale with timestamps (0→N leg proven separately); scale-down soak — a long document conversion in flight survives a downscale gracefully (asynq graceful shutdown, not SIGKILL mid-job) | All 5 targeted research questions below feed this single requirement: pod-deletion-cost mechanics (victim determinism for the soak), heavy-docx generation (a realistic long job), HPA/KEDA downscale timing chain (the critical finding — see Summary — that the phase's ~200s/cooldown margin needs to be re-derived against the real gating mechanism, not `cooldownPeriod`), timestamp-reading mechanics (D-09 triple-check evidence), and local PNG tooling (D-02 deliverable) |

</phase_requirements>

## Project Constraints (from CLAUDE.md)

- **Tech stack is locked:** Go 1.26, chi, asynq+Redis, PostgreSQL 18, S3/MinIO, Docker/docker-compose, Kubernetes+KEDA. Nothing in this phase proposes changing this — the docx-generator and PNG-render tooling recommended below are one-off evidence/test scripts (analogous to `scripts/*.sh`), not application code, and deliberately avoid adding a Go module dependency for either task.
- **No config file support; env-var-only configuration** for the app processes — irrelevant to this phase (no app-code changes), but the `advanced.horizontalPodAutoscalerConfig.behavior` override recommended below must be threaded through `values.yaml`/`values-local.yaml`, consistent with existing chart convention (all KEDA trigger knobs are already values-driven).
- **Never use `panic` for control flow; wrap errors with `fmt.Errorf("...: %w", err)`; `ctx` always first param** — not applicable to bash/python evidence scripts, but any Go code touched (none expected this phase) must follow these.
- **Logging convention:** only `cmd/*/main.go` logs, with emoji-prefixed startup/shutdown lines — not applicable; no `cmd/` changes expected.
- **OrbStack discipline (D-12, restated from CLAUDE.md-adjacent project convention):** compose and k8s stacks must never run simultaneously; images pre-built sequentially with non-`latest` tags; gate scripts must tear down unconditionally via EXIT trap. `scripts/keda-gate.sh` is the canonical reference implementation.
- **GSD workflow enforcement:** file-changing work must go through a GSD entry point (`/gsd-execute-phase` etc.) — this research document itself makes no repo edits.
- **Naming conventions** apply to any new Go code (none expected); the load-proof gate script, if separate, should follow the existing `scripts/*.sh` shape (`set -euo pipefail`, `assert_*` helpers, loud PASS/FAIL, exit code IS the gate — established in `scripts/keda-gate.sh` and `scripts/presets-acceptance.sh`).

## Standard Stack

### Core

| Tool | Version (verified) | Purpose | Why Standard |
|------|---------|---------|--------------|
| `kubectl` | v1.36.2 client / v1.34.8+orb1 server (OrbStack) `[VERIFIED: kubectl version --client / kubectl version, run locally]` | Poll replica counts, read events, set annotations, raw metrics API | Already the sole cluster-interaction tool in `scripts/keda-gate.sh` |
| `uv` | 0.7.3 `[VERIFIED: uv --version, run locally]` | Ephemeral Python environments for the docx generator and PNG renderer, with zero persistent dependency footprint | Confirmed on this machine; `uv run --with <pkg>` resolves and caches in milliseconds after first pull — no venv bookkeeping, no repo dependency addition |
| `python-docx` | 1.2.0 `[VERIFIED: uv run --with python-docx, resolved+imported live in this session]` | Programmatic .docx generation (pages, tables, images, low-level TOC field XML) | Standard, actively maintained OOXML-authoring library; far simpler than hand-assembling OOXML XML in bash for tables/TOC |
| `matplotlib` | 3.11.0 `[VERIFIED: uv run --with matplotlib, resolved+imported live in this session]` | Render the D-02 CSV → PNG dual-axis (queue depth + pod count) time-series chart | `gnuplot` is NOT installed on this host (`command -v gnuplot` returned nothing); matplotlib via `uv` is the only locally-available charting path verified this session |
| `psql` | 18.4 `[VERIFIED: psql --version, run locally]` | Query `job_events`/`jobs.started_at/finished_at` through the existing DB port-forward for the D-09 triple-check | Already the natural fit — `keda-gate.sh` already opens a `svc/postgres` port-forward for client-key minting; reusing it for evidence queries needs no new tooling |

### Supporting

| Tool | Version | Purpose | When to Use |
|------|---------|---------|-------------|
| `Pillow` (PIL) | pulled transitively by `matplotlib` via `uv` `[VERIFIED: seen in uv run --with matplotlib dependency resolution]` | Optional: synthesize placeholder images to embed in the heavy docx if `internal/e2e/testdata/sample.png` repetition isn't varied enough | Only if page-count/table-count alone don't reach the ~200s target and image-heavy pages are needed |

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `uv run --with python-docx` (ephemeral) | A Go docx-generation library (e.g. an OOXML templating package) | Would add a new Go module dependency to a project whose CLAUDE.md documents an already-curated, minimal dependency set; the generator is a one-off fixture producer, not app code — no reason to touch `go.mod` |
| `uv run --with matplotlib` for PNG | gnuplot | gnuplot is simply not installed on this host and there's no indication it's expected to be; `uv`'s ephemeral resolve is faster to bootstrap than installing gnuplot via Homebrew and has zero footprint after the run |
| `kubectl get events` for SIGTERM timing | Adding a sidecar/metrics-server for termination lifecycle tracking | Massively disproportionate for one test scenario; native `kubectl events` + `containerStatuses.lastState.terminated` + Postgres timestamps is sufficient and available with zero extra tooling |
| Values-driven `scaleDown.stabilizationWindowSeconds` override for the gate | Waiting out the real 300s default | Would make the SC3 job need a >300s duration to safely straddle the worst case, which collides with `DOCUMENT_ENGINE_TIMEOUT`=300s (D-07's own upper bound) — mathematically incompatible without either raising the engine timeout (a locked production value, out of scope) or shortening the effective stabilization window for the test |

**Installation:**
```bash
# No persistent installs needed — both verified via ephemeral uv runs:
uv run --with python-docx python3 gen_heavy_docx.py
uv run --with matplotlib python3 render_evidence.py
```

**Version verification:** All four core tools above were verified live in this research session on the actual development host (`uv --version`, `psql --version`, `kubectl version`, and live `uv run --with <pkg>` imports) — not training-data guesses.

## Package Legitimacy Audit

This phase installs no persistent packages into the Go module graph, the container images, or any lockfile. `python-docx` and `matplotlib` are pulled ephemerally by `uv run --with` for one-off evidence/fixture scripts and never touch `go.mod`, `go.sum`, `Dockerfile.*`, or any committed dependency manifest — they are equivalent in kind to a developer running a throwaway script, not a shipped dependency.

| Package | Registry | Age | Downloads | Source Repo | slopcheck | Disposition |
|---------|----------|-----|-----------|-------------|-----------|-------------|
| `python-docx` | PyPI | 10+ yrs (long-established) | Very high (millions/mo) `[ASSUMED — not measured this session]` | github.com/python-openxml/python-docx | not run (see note) | Approved, ephemeral use only |
| `matplotlib` | PyPI | 20+ yrs, foundational scientific-Python package | Very high (tens of millions/mo) `[ASSUMED — not measured this session]` | github.com/matplotlib/matplotlib | not run (see note) | Approved, ephemeral use only |

**slopcheck was not installed/run this session** (`pip install slopcheck` was not attempted — the two packages recommended are extremely well-known, long-established libraries with unambiguous, decades-long provenance, and they are never persisted into the project's dependency graph). Per the graceful-degradation protocol, both packages are tagged `[ASSUMED]` rather than `[VERIFIED]` for registry legitimacy; however, given they are consumed only via ephemeral `uv run --with` invocations (never written to `go.mod`/`requirements.txt`/`package.json`), the planner may treat the human-verification checkpoint as satisfied by the well-known-package exception rather than requiring a blocking `checkpoint:human-verify` — but if strict adherence to the protocol is preferred, gate their first `uv run` invocation behind a `checkpoint:human-verify` task.

**Packages removed due to slopcheck [SLOP] verdict:** none (slopcheck not run)
**Packages flagged as suspicious [SUS]:** none

## Architecture Patterns

### System Architecture Diagram — SC3 downscale-soak scenario

```
[gate script, host]
   |
   |--(1) submit short docx job -----+
   |--(2) submit long/heavy docx job-+--> [api :8090, port-forwarded] --> Postgres (jobs row, status=queued)
   |                                        |                              asynq enqueue (document queue)
   |                                        v
   |                              [KEDA Prometheus scaler] <--- scrapes --- [prometheus, in-cluster]
   |                                        |  polls octoconv_queue_depth{queue=document,state=~pending|active}
   |                                        v
   |                              [HPA controller] --scales--> [document-worker Deployment: 0 -> 2]
   |
   |--(3) poll pods, identify which pod picked up the LONG job (via logs or job status)
   |--(4) kubectl annotate <busy-pod> controller.kubernetes.io/pod-deletion-cost=-1000  <-- BEFORE downscale fires (race-safe)
   |
   |   [short job finishes] --> asynq reports done --> Postgres jobs.status=done
   |                                        |
   |                                        v
   |                              queue_depth metric drops toward desired=1
   |                                        |
   |                    HPA scaleDown.stabilizationWindowSeconds elapses
   |                    (default 300s UNLESS overridden -- see Common Pitfalls)
   |                                        v
   |                              [HPA/ReplicaSet] deletes the LOW-cost (busy) pod
   |                                        |
   |                              kubelet sends SIGTERM to the busy pod's container
   |                                        |  (kubectl events: reason=Killing, THIS is the real SIGTERM timestamp)
   |                                        v
   |                              asynq server.Shutdown() -- ShutdownTimeout=310s window
   |                                        |  in-flight LibreOffice conversion keeps running
   |                                        v
   |                              conversion completes --> job marked done in Postgres
   |                                        |
   |                              process exits cleanly, pod terminates BEFORE
   |                              terminationGracePeriodSeconds=330s elapses (graceful, no SIGKILL)
   |
   |--(5) read evidence: kubectl events (Killing ts), containerStatuses.lastState.terminated (exit ts),
   |       job_events (exactly one queued->active row), jobs.finished_at (job completion ts)
   |--(6) assert SIGTERM_ts < job_completion_ts < pod_exit_ts < grace_deadline
```

### Recommended Project Structure

```
scripts/
├── keda-gate.sh              # Phase 27 gate — untouched, must keep passing (D-12)
├── keda-load-proof.sh        # NEW (or a mode flag on keda-gate.sh) — Phase 28 gate
└── lib/                      # optional: shared helpers (waitForReplicasAtLeast etc.)
    └── keda-common.sh        # only if extracting shared code; not required

.planning/phases/28-autoscale-load-proof/
├── evidence/
│   ├── sc1-sc2-burst-<timestamp>.csv       # D-01 sampler output, burst scenario
│   ├── sc1-sc2-burst-<timestamp>.png       # D-02 chart
│   ├── sc3-downscale-soak-<timestamp>.csv  # sampler output, soak scenario (optional, if sampled)
│   ├── gate-transcript-<timestamp>.log     # full stdout of the gate run
│   └── sc3-timestamps.txt                  # D-09 triple-check raw evidence (kubectl events + psql output)

scripts/fixtures/                            # NEW, or internal/e2e/testdata/ extension
└── gen_heavy_docx.py                        # uv-run python-docx generator, calibration-parameterized
```

### Pattern 1: Values-gated HPA `behavior` override for deterministic test timing

**What:** Add an optional `advanced.horizontalPodAutoscalerConfig.behavior.scaleDown` block to `scaledobject-document.yaml` (and, if the burst scenario ever needs it, `scaledobject-image.yaml`), driven by a new `values.yaml` key defaulting to *omitted* (preserving current K8s-default behavior for production), with the load-proof gate passing a small override value via its own `-f values-loadproof.yaml` (or `--set`) at `helm install` time.

**When to use:** Whenever a test scenario needs the 2→1 (or generally N→N-1) HPA downscale to happen on a short, predictable timeline instead of the Kubernetes default 300s `scaleDown.stabilizationWindowSeconds`.

**Example:**
```yaml
# deploy/chart/octoconv/templates/scaledobject-document.yaml (illustrative addition)
spec:
  scaleTargetRef:
    name: document-worker
  minReplicaCount: 0
  maxReplicaCount: {{ .Values.keda.document.maxReplicaCount }}
  pollingInterval: {{ .Values.keda.document.pollingInterval }}
  cooldownPeriod: {{ .Values.keda.document.cooldownPeriod }}
  {{- if .Values.keda.document.scaleDownStabilizationSeconds }}
  advanced:
    horizontalPodAutoscalerConfig:
      behavior:
        scaleDown:
          stabilizationWindowSeconds: {{ .Values.keda.document.scaleDownStabilizationSeconds }}
          policies:
            - type: Pods
              value: 1
              periodSeconds: 15
  {{- end }}
  fallback:
    failureThreshold: 3
    replicas: 1
  triggers: [...]
```
```yaml
# values.yaml — default omitted (nil), production behavior unchanged
keda:
  document:
    threshold: "1"
    maxReplicaCount: 2
    pollingInterval: 15
    cooldownPeriod: 120
    scaleDownStabilizationSeconds: null   # NEW key, default off
```
```yaml
# a load-proof-only values overlay passed by the gate script, e.g. values-loadproof.yaml
keda:
  document:
    scaleDownStabilizationSeconds: 15
```
*(No official-docs code fetch was available for this exact block via WebFetch in this session — synthesized from the verified KEDA `scaledobject-spec` doc text on `advanced.horizontalPodAutoscalerConfig.behavior` feeding "directly to the HPA's behavior field", cross-referenced against the standard Kubernetes HPA v2 `behavior` schema. Tag: `[CITED: keda.sh/docs/2.20/reference/scaledobject-spec/, kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/]`)*

### Pattern 2: `kubectl events` as the authoritative SIGTERM timestamp source

**What:** `pod.metadata.deletionTimestamp` is set by the API server to `(delete-request-time + gracePeriodSeconds)` — it is the **deadline**, not the moment SIGTERM was actually sent. The kubelet's `Killing` event (`reason=Killing`, message `Stopping container <name>`) fires at the moment the kubelet actually issues SIGTERM, which is the value D-09 needs.

**When to use:** Any time a plan needs to prove "SIGTERM occurred before X" with a real timestamp, not an inferred one.

**Example:**
```bash
# Capture as soon as possible after the downscale fires — default event TTL
# on most clusters is ~1h, but OrbStack's default may differ; read promptly.
kubectl get events -n octoconv \
  --field-selector involvedObject.name=<busy-pod-name>,reason=Killing \
  -o jsonpath='{.items[0].firstTimestamp}'

# Container's own exit time (must be read BEFORE the Pod object is
# garbage-collected post-termination):
kubectl get pod <busy-pod-name> -n octoconv \
  -o jsonpath='{.status.containerStatuses[0].lastState.terminated.finishedAt}'
```
`[ASSUMED: kubelet Killing-event semantics — based on well-established Kubernetes behavior, not verified against a version-pinned official doc page this session; cross-check against observed OrbStack 1.34.8 behavior during gate execution before treating as ground truth for the SUMMARY]`

### Anti-Patterns to Avoid

- **Asserting `deletionTimestamp` as the SIGTERM-sent time:** it's the *deadline* (`now + grace`), not the send time — using it directly for the D-09 "SIGTERM before completion" check would silently overstate the graceful window by up to the full grace period.
- **Setting `pod-deletion-cost` after or concurrently with the scale-down trigger:** the annotation is best-effort and must land before the ReplicaSet controller makes its deletion decision — D-08's "before the downscale fires" ordering is correct and must be preserved exactly as designed, not treated as a minor sequencing detail.
- **Calibrating the long-job duration only against `cooldownPeriod`:** as established in the Summary, `cooldownPeriod` doesn't govern this scenario's downscale step at all — see Common Pitfalls.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Programmatic .docx with tables/TOC/images | Manual OOXML zip/XML assembly in bash | `python-docx` via `uv run --with python-docx` | python-docx already exposes paragraphs/tables/images/headings at the right abstraction level; hand-rolled XML would need to reimplement OOXML's package-relationship plumbing for zero benefit |
| CSV→PNG dual-axis time-series chart | A bespoke SVG/plotting script | `matplotlib` via `uv run --with matplotlib` | Two-line-series-on-one-time-axis is matplotlib's bread-and-butter; verified working in this session with zero setup cost |
| Pod termination lifecycle tracking | A sidecar or custom controller watching pod events | `kubectl get events` + `containerStatuses.lastState.terminated`, polled by the gate script | Native Kubernetes API surface already exposes everything D-09 needs; building anything extra is exactly the kind of disproportionate tooling CONTEXT.md explicitly rejects (see "kube-state-metrics ... still disproportionate") |

**Key insight:** Every "don't hand-roll" item in this phase has a zero-install or already-installed answer verified live on this machine this session — there is no case here where a custom tool is justified.

## Common Pitfalls

### Pitfall 1: Assuming `cooldownPeriod` governs the 2→1 downscale step in SC3 (CRITICAL — load-bearing for D-07/D-08)

**What goes wrong:** The phase description and D-07 frame the long-job duration as needing margin "> cooldown (120s) and < DOCUMENT_ENGINE_TIMEOUT (300s)". If the plan calibrates purely against the 120s cooldown, the actual downscale (2→1) may not fire until up to **300s** after the short job completes — the Kubernetes HPA's default `scaleDown.stabilizationWindowSeconds`, which applies because `[CITED: keda.sh/docs/2.20/reference/scaledobject-spec/]` states unambiguously: *"the KEDA `cooldownPeriod` only applies when scaling to 0; scaling from 1 to N replicas is handled by the Kubernetes Horizontal Pod Autoscaler"* — and this repo's ScaledObjects set no `advanced.horizontalPodAutoscalerConfig.behavior`, so the Kubernetes-wide default applies: `[CITED: kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/]` *"scaleDown: stabilizationWindowSeconds: 300, policies: - type: Percent value: 100 periodSeconds: 15"*. In the worst case this exceeds the long job's own `DOCUMENT_ENGINE_TIMEOUT` (300s, measured from the job's own start), so the job could finish or self-timeout before the pod is ever asked to scale down — silently invalidating the "downscale hits a job in flight" precondition without any assertion actually failing loudly (the gate would just never observe a downscale during the job's lifetime, or worse, observe one that has nothing to do with the job).
**Why it happens:** `cooldownPeriod` and HPA `stabilizationWindowSeconds` are two different mechanisms controlling two different transitions (1↔0 vs N↔N-1 for N>1), and KEDA's docs are easy to skim past this distinction — the ScaledObject spec only has one obviously-relevant-looking timing knob (`cooldownPeriod`), so it's natural to assume it covers all downscaling.
**How to avoid:** Add a values-gated `advanced.horizontalPodAutoscalerConfig.behavior.scaleDown.stabilizationWindowSeconds` override (Pattern 1 above), applied ONLY via the load-proof gate's values overlay (never touching the production-facing `values.yaml` defaults), set low enough (e.g. 10-15s, near the HPA sync period / KEDA polling interval floor) that the 2→1 transition is deterministic and fast. Re-derive the long-job target duration against this new, controlled number instead of `cooldownPeriod`.
**Warning signs:** If a live gate run shows the busy pod's `Killing` event landing suspiciously close to (or after) the long job's own completion timestamp, this is the pitfall manifesting — the "survives a downscale" proof is not actually testing what it claims to.

### Pitfall 2: Reading `pod.deletionTimestamp` as "when SIGTERM was sent"

**What goes wrong:** `deletionTimestamp` is set to the deletion deadline (`request-time + terminationGracePeriodSeconds`), not the moment the kubelet issued SIGTERM. Using it as the SIGTERM timestamp in D-09's "SIGTERM before job completion" check silently gives the graceful-shutdown proof more slack than actually exists, potentially masking a real SIGKILL-adjacent race.
**Why it happens:** The field name and mental model ("this is when it got deleted") suggest it's an event timestamp, when it's actually a scheduling deadline written at request time.
**How to avoid:** Use the kubelet's `Killing` event (`kubectl get events --field-selector reason=Killing`) as the authoritative SIGTERM-sent timestamp instead (Pattern 2 above).
**Warning signs:** SIGTERM-to-completion deltas that look implausibly large relative to observed conversion durations.

### Pitfall 3: Losing pod status before it's garbage-collected

**What goes wrong:** Once a terminated pod's owning ReplicaSet finishes replacing/reconciling, the old Pod object can be removed from the API relatively quickly; if the gate only checks `containerStatuses.lastState.terminated` after the scenario completes (rather than polling continuously through the SC3 window), the exit timestamp may already be gone.
**Why it happens:** Kubernetes doesn't retain terminated Pod objects indefinitely once ownership/GC conditions are met; `kubectl events` has its own TTL too (commonly ~1h, but not guaranteed identical on OrbStack).
**How to avoid:** Poll (not a single late read) `kubectl get pod <name> -o json` throughout the SC3 window and persist each snapshot (with a local wall-clock read-timestamp) to the evidence log as it happens, rather than relying on a single post-hoc query.
**Warning signs:** `kubectl get pod` returning `NotFound` when trying to read `lastState.terminated` after the fact.

### Pitfall 4: Calibrating docx heaviness on the wrong architecture

**What goes wrong:** No `soffice`/`libreoffice` binary is available on this Mac (`command -v soffice` returned nothing) — there is no way to locally pre-time a candidate docx before running it in the cluster. If a plan assumes "test locally first, then verify in-cluster," it will stall on a missing dependency.
**Why it happens:** The document-worker's LibreOffice runs only inside its container (amd64-under-Rosetta on this OrbStack host, per `[CITED: Dockerfile.worker/document-worker, deploy chart comments]`), and no native LibreOffice install exists on the host.
**How to avoid:** D-07 already accounts for this correctly ("генератор калибруется одним пробным прогоном" — calibrated by one live trial run) — the calibration step MUST be an actual live job submitted to the real document-worker pod in the cluster, not a local dry run. Plan the calibration as its own gate step/checkpoint that measures real conversion time and adjusts generator parameters (page/table/image count) before the final SC3 run.
**Warning signs:** Any task description that says "time the conversion locally" or "test with local LibreOffice" — there is none on this host.

## Code Examples

### Heavy docx generator (uv-ephemeral, python-docx)

```python
# Source: synthesized from python-docx public API (verified live: uv run
# --with python-docx python3 -c "import docx; ...", python-docx 1.2.0)
# Illustrative skeleton — planner tunes PAGE_UNITS to hit ~200s after one
# calibration run against the real in-cluster document-worker.
import docx
from docx.oxml.ns import qn
from docx.oxml import OxmlElement

def add_toc_field(document):
    paragraph = document.add_paragraph()
    run = paragraph.add_run()
    fld_begin = OxmlElement('w:fldChar'); fld_begin.set(qn('w:fldCharType'), 'begin')
    instr = OxmlElement('w:instrText'); instr.set(qn('xml:space'), 'preserve')
    instr.text = 'TOC \\o "1-3" \\h \\z \\u'
    fld_sep = OxmlElement('w:fldChar'); fld_sep.set(qn('w:fldCharType'), 'separate')
    fld_end = OxmlElement('w:fldChar'); fld_end.set(qn('w:fldCharType'), 'end')
    run._r.append(fld_begin); run._r.append(instr)
    run._r.append(fld_sep); run._r.append(fld_end)

d = docx.Document()
add_toc_field(d)
d.add_page_break()

PAGE_UNITS = 300  # calibration knob — raise/lower after the trial run
for i in range(PAGE_UNITS):
    d.add_heading(f"Section {i}", level=1)
    d.add_paragraph("Lorem ipsum " * 80)
    table = d.add_table(rows=8, cols=6)
    for row in table.rows:
        for cell in row.cells:
            cell.text = "data " * 4
    if i % 10 == 0:
        d.add_picture("internal/e2e/testdata/sample.png")  # reuse existing fixture
    d.add_page_break()

d.save("heavy.docx")
```

### PNG evidence render (uv-ephemeral, matplotlib)

```python
# Source: synthesized from matplotlib public API (verified live: uv run
# --with matplotlib python3 -c "import matplotlib; ...", matplotlib 3.11.0)
import csv
import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
import matplotlib.dates as mdates
from datetime import datetime

ts, queue_depth, pod_count = [], [], []
with open("evidence.csv") as f:
    for row in csv.DictReader(f):
        ts.append(datetime.fromisoformat(row["timestamp"]))
        queue_depth.append(int(row["queue_depth"]))
        pod_count.append(int(row["pod_count"]))

fig, ax1 = plt.subplots(figsize=(12, 5))
ax1.plot(ts, queue_depth, color="tab:blue", label="queue depth")
ax1.set_ylabel("queue depth", color="tab:blue")
ax2 = ax1.twinx()
ax2.plot(ts, pod_count, color="tab:red", label="pod count")
ax2.set_ylabel("pod count", color="tab:red")
ax1.xaxis.set_major_formatter(mdates.DateFormatter("%H:%M:%S"))
fig.autofmt_xdate()
plt.title("Phase 28: 0->N->0 autoscale timeline")
plt.savefig("evidence.png", dpi=120)
```

### D-09 triple-check queries (psql via existing DB port-forward)

```bash
# Source: internal/db/migrations/0001_init.sql schema (VERIFIED: read this
# session), reusing keda-gate.sh's existing DB_LOCAL_PORT port-forward.

# (2) exactly one queued->active transition
psql "postgres://octo:octo-pass@127.0.0.1:${DB_LOCAL_PORT}/octo_db" -tAc \
  "SELECT count(*) FROM job_events WHERE job_id='${LONG_JOB_ID}' AND from_status='queued' AND to_status='active';"
# expect: 1

# job completion timestamp for the SIGTERM-before-completion check
psql "postgres://octo:octo-pass@127.0.0.1:${DB_LOCAL_PORT}/octo_db" -tAc \
  "SELECT finished_at FROM jobs WHERE id='${LONG_JOB_ID}';"
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|---------------|--------|
| Assuming `cooldownPeriod` governs all KEDA downscaling | `cooldownPeriod` governs only 1→0; N→N-1 (N>1) is standard HPA behavior with its own (default 300s) stabilization window unless `advanced.horizontalPodAutoscalerConfig.behavior` is set | Documented behavior since KEDA's HPA-behavior passthrough was added (`advanced.horizontalPodAutoscalerConfig`); not a recent change, but easy to miss | Directly determines whether D-07/D-08's timing assumptions hold — see Pitfall 1 |
| `PodDeletionCost` treated as beta/opt-in-feature-gated | GA since Kubernetes 1.26; feature gate removed in 1.27 (always enabled) | Kubernetes 1.26/1.27 (well before this cluster's 1.34.8) | No feature-gate concerns on OrbStack v1.34.8+orb1 — the annotation just works |

**Deprecated/outdated:** None specific to this phase's tooling — all recommended tools (`kubectl`, `uv`, `python-docx`, `matplotlib`, `psql`) are current, actively maintained.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | The kubelet's `Killing` event timestamp is a reliable, always-emitted proxy for "SIGTERM actually sent" on OrbStack's k8s v1.34.8+orb1 (not independently verified against a version-pinned official doc this session) | Code Examples / Pattern 2 | If OrbStack's kubelet doesn't emit this event reliably, D-09's SIGTERM timestamp would need a fallback (e.g. worker process log line at signal-receipt, already implicitly available since `cmd/document-worker/main.go` logs `"🛑 shutting down document-worker..."` on `<-ctx.Done()` — this is actually a solid fallback/cross-check already in the codebase) |
| A2 | Default event retention (TTL) on OrbStack's k8s is close to the common ~1h Kubernetes default, sufficient for reading events promptly after the SC3 scenario, before the gate's teardown | Common Pitfalls #3 | If retention is much shorter, the gate must read/persist events immediately after each transition rather than in a batch at the end — mitigated by recommending continuous polling regardless |
| A3 | `python-docx`'s low-level OXML API (`OxmlElement`/`qn`) TOC-field-insertion pattern shown in Code Examples works unmodified in python-docx 1.2.0 (the exact version resolved live this session) | Code Examples | If the API surface differs slightly, the TOC field may need adjustment; core page/table/image generation (the primary duration driver) does not depend on this and is unaffected |
| A4 | `python-docx` and `matplotlib` package ages/download counts in the Package Legitimacy Audit table | Package Legitimacy Audit | Purely informational context (both are extremely well-known, long-established libraries); no material risk since neither is persisted into the dependency graph |

## Open Questions

1. **Exact effective HPA sync/KEDA polling floor for the tuned `scaleDownStabilizationSeconds`**
   - What we know: KEDA's document class `pollingInterval` is 15s (values.yaml, verified); the Kubernetes HPA controller's own sync period defaults to 15s (`--horizontal-pod-autoscaler-sync-period`), and this flag could not be inspected on OrbStack's control plane this session (`kube-system` shows no accessible `kube-controller-manager` pod — OrbStack likely runs the control plane outside a visible Pod).
   - What's unclear: Whether OrbStack's k8s distribution has customized this flag away from the Kubernetes default.
   - Recommendation: Treat 15s as the working assumption (Kubernetes default, `[CITED]`), but have the calibration/trial-run step (already planned per D-07) also observationally confirm the 2→1 transition timing once `scaleDownStabilizationSeconds` is set low, adjusting the long-job target duration from the OBSERVED number rather than the theoretical one if they diverge.

2. **Exact evidence-log TTL for `kubectl events` on this OrbStack version**
   - What we know: Kubernetes' common default is ~1h; OrbStack has not been independently confirmed to match.
   - What's unclear: Whether OrbStack customizes `--event-ttl` on `kube-apiserver`.
   - Recommendation: Design the gate to read/persist events promptly (within the same script run, well under any plausible TTL) rather than depending on long retention — already the natural implementation given the gate's teardown-on-exit design (D-12).

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| `kubectl` | All scenarios (cluster interaction) | Yes | v1.36.2 client / v1.34.8+orb1 server | — |
| `helm` | Gate install flow (reused from Phase 27) | Yes (per `27-03-SUMMARY.md`, unchanged this phase) | — | — |
| `uv` | docx generator, PNG renderer | Yes | 0.7.3 | — |
| `python-docx` (via `uv run --with`) | Heavy docx generation (D-07) | Yes (ephemeral, verified live) | 1.2.0 | — |
| `matplotlib` (via `uv run --with`) | PNG evidence render (D-02) | Yes (ephemeral, verified live) | 3.11.0 | — |
| `gnuplot` | PNG evidence render (alternative to matplotlib) | No | — | Use matplotlib via `uv` (already the verified path) |
| `psql` | D-09 Postgres evidence queries | Yes | 18.4 | — |
| `soffice`/`libreoffice` (local, native) | Local docx-conversion-time pre-testing | No | — | None needed/expected — D-07 already specifies calibration via a live in-cluster trial run, not local pre-testing (see Pitfall 4) |

**Missing dependencies with no fallback:** None.

**Missing dependencies with fallback:**
- `gnuplot` — matplotlib via `uv run --with matplotlib` is the confirmed, working alternative (already the effective D-02 tool choice given gnuplot's absence).

## Security Domain

`security_enforcement` is absent from `.planning/config.json` (treated as enabled per protocol), but this phase touches no authentication, input-validation, or cryptography surface — it is a live-observability/load-testing phase against already-shipped, already-reviewed infrastructure (Phase 24/27) plus one narrow chart templating fix (D-10/WR-02). No new attack surface is introduced.

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | No | Gate reuses the existing sanctioned client-key-minting mechanism (`cmd/manage-clients`, per Phase 24/25 precedent) — no new auth path |
| V3 Session Management | No | N/A — no session concept in this API |
| V4 Access Control | No | No new endpoints or access paths added |
| V5 Input Validation | No | Job submissions use the existing, already-hardened `/v1/jobs` upload path with existing test fixtures; the heavy docx is a well-formed OOXML file generated by a standard library, not adversarial input |
| V6 Cryptography | No | No cryptographic surface touched |

### Known Threat Patterns for this stack

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Gate script leaking DB/API credentials into evidence logs (e.g. accidentally capturing `DATABASE_URL` or `CLIENT_KEY` in the committed CSV/log) | Information Disclosure | Follow `scripts/keda-gate.sh`'s existing pattern of using dev-only, throwaway credentials (`dev-only-change-me-in-real-deploys`) and ensure the evidence-writing code never echoes `$CLIENT_KEY`/`$DATABASE_URL` into files destined for `evidence/` (which gets committed per D-03) |
| A committed heavy-docx fixture inadvertently bloating the git repo | N/A (hygiene, not a STRIDE threat) | Keep the generator script in the repo (small), NOT the generated `heavy.docx` itself — generate it at gate-run time, don't commit the multi-hundred-page artifact |

## Sources

### Primary (HIGH confidence)
- `keda.sh/docs/2.20/reference/scaledobject-spec/` — `cooldownPeriod` scope (1→0 only), `advanced.horizontalPodAutoscalerConfig.behavior` passthrough semantics (fetched and quoted this session)
- `kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/` — HPA v2 default `behavior.scaleDown` (stabilizationWindowSeconds: 300, policies) (search-verified this session, cross-referenced against multiple independent pages returning identical default values)
- Live tool checks run in this session: `kubectl version`, `uv --version`, `psql --version`, `uv run --with python-docx ...`, `uv run --with matplotlib ...`, `command -v gnuplot`/`soffice` (all empty)

### Secondary (MEDIUM confidence)
- `kubernetes.io/docs/concepts/workloads/controllers/replicaset/` (via WebFetch) and cross-referenced `github.com/kubernetes/enhancements` KEP-2255 history — `PodDeletionCost` GA in 1.26, feature gate removed 1.27, best-effort semantics, must-set-before-decision timing caveat. WebFetch's GA claim was corroborated by a second, independent WebSearch pass against the KEP history (Alpha 1.21 → Beta 1.22-1.25 → GA 1.26 → feature gate removed 1.27), giving cross-source agreement.
- `github.com/kedacore/keda/issues/7204` (KEDA "Honour HPA Scale-Down Policy" open issue) — confirms current KEDA behavior forces scale-to-zero regardless of HPA scale-down policy, supporting the cooldownPeriod-is-0-only-transition finding from a second angle

### Tertiary (LOW confidence)
- General LibreOffice-headless-performance search results (startup overhead ~3.3s/doc, sequential single-process conversion model, no thread-safety) — directionally useful but not specific enough on page/table/image/TOC cost breakdown to be more than a sanity-check backdrop; the real calibration must come from D-07's planned live trial run in-cluster, not this research

## Metadata

**Confidence breakdown:**
- HPA/KEDA downscale timing chain (Q3): HIGH — directly quoted from official KEDA docs + official Kubernetes docs, cross-verified by an independent open KEDA issue describing the same gap from a different angle
- pod-deletion-cost semantics (Q1): MEDIUM-HIGH — GA status corroborated by two independent search passes agreeing on the same version history (1.21/1.22/1.26/1.27), but no single canonical doc page was fetched verbatim confirming "GA in 1.34" specifically (inferred from graduation history, not a version-1.34-specific doc)
- Heavy docx generation (Q2): MEDIUM — tooling (python-docx via uv) verified live; actual conversion-time-driving factors are best-practice/training-knowledge (`[ASSUMED]`), not verified against a LibreOffice performance benchmark doc — D-07's own planned live calibration is the real source of truth
- Timestamp reading (Q4): MEDIUM — mechanism (kubectl events, containerStatuses, Postgres) is well-established Kubernetes/project knowledge but the `Killing`-event-exists-and-is-reliable claim on this specific OrbStack build is `[ASSUMED]` (A1)
- PNG tooling (Q5): HIGH — directly and live-verified this session (gnuplot absent, uv+matplotlib present and working)

**Research date:** 2026-07-17
**Valid until:** 2026-08-16 (30 days — infra/tooling landscape here is stable; the one time-sensitive finding, KEDA's HPA-behavior passthrough, is unlikely to change on the pinned KEDA v2.20.1)
