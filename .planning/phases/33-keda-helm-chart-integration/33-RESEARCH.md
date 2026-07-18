# Phase 33: KEDA/Helm Chart Integration - Research

**Researched:** 2026-07-18
**Domain:** Helm chart authoring (4th repetition of an established pattern) + KEDA ScaledObject tuning + live scale-from-zero proof on OrbStack k8s
**Confidence:** HIGH (chart/template patterns — direct repo read of all 3 existing worker classes); MEDIUM (new numeric tuning values — derived from established formulas but not yet live-measured); LOW (image-pull-vs-cold-start separability on OrbStack — architecturally reasoned, not yet empirically confirmed)

## Summary

Phase 33 is almost entirely mechanical: copy the proven 3-class chart pattern (Deployment + ScaledObject + ConfigMap keys) a 4th time for `audio-worker`, using the already-measured Phase 32 numbers (`AUDIO_ENGINE_TIMEOUT=742s`, `AUDIO_WORKER_CONCURRENCY=1`, `--cpus=2.0 --memory=1g`). The template shapes, the WR-01 fail-safe fix (`ignoreNullValues:false` + retry-inclusive PromQL + `fallback.replicas:1`), and the `spec.replicas`-omission-under-KEDA convention are all already correct in the 3 sibling ScaledObjects/Deployments — audio's YAML is a structural clone of `document-worker`'s files (closest analog: heaviest resource footprint, has an `extraEnv`-capable pattern precedent, needed a non-default `scaleDownStabilizationSeconds`).

Two things are **not** mechanical and need explicit, deliberate handling: (1) the chart's `configmap.yaml` currently has **zero** `AUDIO_*` keys and still carries the **stale `RECONCILER_ACTIVE_STALE_AFTER: "5m"`** that compose already fixed to `15m` in Phase 32 (IN-16) — deploying `AUDIO_ENGINE_TIMEOUT=742s` against the chart's un-fixed `5m` CAP reopens the exact reconciler double-processing risk Phase 32 closed in compose, so this fix must land in the chart in the same commit as the audio ConfigMap keys; and (2) unlike `document`/`html`/`image`, whose `scaleDownStabilizationSeconds` stays `null` in production (k8s's 300s HPA default is fine because their jobs finish well inside 300s), **audio's own worst-case in-flight duration (742s) exceeds the k8s 300s default** — SC1 explicitly requires this knob be set for audio, and it must be a **production-default non-null value** in `values.yaml`, not a load-proof-overlay-only value like document's.

The scale-from-zero live-proof (SC3) is a new script, not a reuse of `keda-gate.sh`/`keda-load-proof.sh` in place — the codebase's established discipline is to leave those byte-unchanged and clone their helper shapes into a new script (exactly how `audio-rtf-measure.sh` cloned `verapdf-measure.sh` in Phase 32). Phase 33 also inherits an explicit **deferred obligation from Phase 29**: live-run `scripts/keda-load-proof.sh` (unmodified, document class) and confirm zero orphaned `kubectl get pod -w` processes survive teardown and correct `BUSY_POD` selection — this closes Phase 29's approved-with-deferral human-verification gap.

**Primary recommendation:** Clone `deployment-document-worker.yaml` + `scaledobject-document.yaml` → `deployment-audio-worker.yaml` + `scaledobject-audio.yaml`, wire `AUDIO_*` ConfigMap keys + the `RECONCILER_ACTIVE_STALE_AFTER` 5m→15m chart fix in one commit, set `keda.audio.scaleDownStabilizationSeconds` non-null in production `values.yaml` (not overlay-only), add `queue.QueueAudio` to `cmd/api/main.go`'s collector registration, then write a new `scripts/keda-audio-loadproof.sh` (structural clone of `keda-load-proof.sh`) that captures Phase-28-style timestamped evidence of image-pull vs scale-from-zero timing for the 682MB image, before separately re-running the unmodified `keda-load-proof.sh` to close Phase 29's deferred item.

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| AUD-08 | Chart: audio-worker Deployment (class-appropriate grace period) + KEDA ScaledObject (with the scaleDownStabilizationSeconds lesson from v1.6), QueueAudio registered in the api queue-depth collector; scale-from-zero live-proven with the model baked into the image (image-pull vs scale-from-zero measured) | Pattern 1 (Deployment clone), Pattern 2 (ScaledObject clone + WR-01 fail-safe), Pattern 3 (ConfigMap keys + RECONCILER_ACTIVE_STALE_AFTER fix), Pattern 4 (terminationGracePeriodSeconds formula), Pitfall 6 (QueueAudio collector splice), Pitfalls 3/5 (scaleDownStabilizationSeconds + image-pull measurement design), Open Questions 1-2 (live-proof script scope) |
</phase_requirements>

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Audio job queue-depth exposure | API / Backend (`cmd/api`) | — | The always-on `api` process is the only pod alive at genuine 0 replicas; `NewQueueDepthCollector` must include `queue.QueueAudio` or KEDA has no metric to scale on (SC2) |
| Audio worker autoscaling decision | Orchestration (KEDA/HPA) | — | `ScaledObject` + standard k8s HPA own the 0↔N replica decision via the Prometheus scaler; the app never self-scales |
| Audio conversion execution | Worker / Compute (`cmd/audio-worker` pod) | — | Runs ffmpeg→whisper-cli pipeline; already built (Phase 30-32), unchanged this phase |
| Graceful shutdown on downscale | Worker / Compute (asynq `ShutdownTimeout`) + Orchestration (`terminationGracePeriodSeconds`) | — | Both must exceed `AUDIO_ENGINE_TIMEOUT` (742s) — asynq's own timeout is the inner bound, k8s's grace period is the outer bound that must not fire first |
| Chart config propagation (`AUDIO_*` env) | Orchestration (Helm `ConfigMap`) | API/Worker (consume via `envFrom`) | Single choke point (`configmap.yaml`) — every pod pulls the identical env surface (DEBT-05 pattern), so one edit propagates everywhere |
| Scale-from-zero cold-start evidence | Orchestration (live k8s cluster, OrbStack) | — | Must be measured against a real cluster, not asserted — no unit/integration-test substitute exists for this claim |
| Model distribution (baked vs volume) | Worker / Compute (container image) | Storage (PVC, only if NO-GO) | Currently baked (Dockerfile.audio-worker, Phase 32); this phase's SC3 measurement is the go/no-go gate for staying baked |

## Standard Stack

No new libraries or packages this phase — this is Helm/Kubernetes chart authoring against already-pinned tooling.

### Core (already pinned, unchanged this phase)
| Tool | Version | Purpose | Source |
|------|---------|---------|--------|
| KEDA | 2.20.1 | Prometheus-scaler-based autoscaling | `scripts/keda-gate.sh:39`, `scripts/keda-load-proof.sh:56` — re-verified resolvable live at script-run time via `helm search repo kedacore/keda --versions` |
| Helm | (whatever is on the operator's PATH; no version pin found in repo) | Chart templating/install | `deploy/chart/octoconv/Chart.yaml` (`apiVersion: v2`) |
| Prometheus | v3.13.1 | In-chart scrape target for `octoconv_queue_depth` | `deploy/chart/octoconv/values.yaml:129` |

**No `npm install`/`pip install`/`cargo add` — Package Legitimacy Audit is not applicable this phase.** [VERIFIED: repo grep — no new import/dependency touches `go.mod`/`go.sum` for this phase's scope]

## Package Legitimacy Audit

Not applicable — this phase modifies Helm chart YAML, Go wiring in `cmd/api/main.go` (one new constant reference, `queue.QueueAudio`, already defined in-repo since Phase 31), and shell scripts. No new third-party package is introduced.

## Architecture Patterns

### System Architecture Diagram

```
                    ┌─────────────────────────────────────────┐
                    │         api pod (always-on, min=1)       │
                    │  cmd/api/main.go                          │
                    │  NewQueueDepthCollector(inspector,        │
                    │    Image,Document,HTML,Audio,Webhook)  ◄──┼── ADD QueueAudio HERE
                    │  exposes /metrics :9090                   │
                    └───────────────┬───────────────────────────┘
                                    │ scrape (15s)
                                    ▼
                    ┌─────────────────────────────────────────┐
                    │   prometheus pod (in-chart, gated)        │
                    │   scrapes api:9090/metrics                │
                    └───────────────┬───────────────────────────┘
                                    │ PromQL query
                                    │ sum(octoconv_queue_depth{queue="audio",
                                    │     state=~"pending|active|retry"})
                                    ▼
                    ┌─────────────────────────────────────────┐
                    │  ScaledObject: worker-audio-scaledobject  │
                    │  scaleTargetRef: audio-worker             │
                    │  minReplicaCount: 0                       │
                    │  ignoreNullValues: "false" (WR-01)         │
                    │  fallback.replicas: 1                      │
                    │  scaleDownStabilizationSeconds: >742s      │◄── MUST be non-null
                    │  (unlike document/html/image production)   │    in PRODUCTION values.yaml
                    └───────────────┬───────────────────────────┘
                                    │ drives HPA (k8s-native)
                                    ▼
                    ┌─────────────────────────────────────────┐
                    │  Deployment: audio-worker (0↔N replicas)   │
                    │  spec.replicas OMITTED (KEDA owns it)      │
                    │  terminationGracePeriodSeconds: >742s      │
                    │  resources.limits: cpu=2, memory=1Gi       │
                    │  (must match RTF-measurement container     │
                    │   EXACTLY — 742s is only valid at these    │
                    │   limits)                                  │
                    └───────────────┬───────────────────────────┘
                                    │ consumes queue "audio"
                                    ▼
                    ┌─────────────────────────────────────────┐
                    │  cmd/audio-worker: ffmpeg → whisper-cli    │
                    │  ShutdownTimeout = AUDIO_ENGINE_TIMEOUT    │
                    │  + 10s = 752s (inner bound)                │
                    └─────────────────────────────────────────┘
```

### Recommended File Set (new files this phase)
```
deploy/chart/octoconv/templates/
├── deployment-audio-worker.yaml   # clone of deployment-document-worker.yaml shape
├── scaledobject-audio.yaml        # clone of scaledobject-document.yaml shape (has
│                                   # scaleDownStabilizationSeconds precedent already)
templates/configmap.yaml           # EDIT: add 5 AUDIO_* keys, fix RECONCILER_ACTIVE_STALE_AFTER
values.yaml                        # EDIT: add audioWorker block, keda.audio block
                                    #   (scaleDownStabilizationSeconds NON-NULL, unlike document)
values-local.yaml                  # no change needed (keda.enabled/prometheus.enabled already true)
scripts/
└── keda-audio-loadproof.sh        # NEW: structural clone of keda-load-proof.sh, adapted for
                                    #   audio's scale-from-zero + image-pull-vs-cold-start proof
cmd/api/main.go                    # EDIT: add queue.QueueAudio to collector registration (1 line)
```

### Pattern 1: Deployment template — clone `document-worker`, not `worker`/`chromium-worker`
**What:** `document-worker` is the closest structural analog: no `platform:` pin needed (whisper.cpp is source-built portable, same as document-worker's LibreOffice pattern reasoning — actually chromium-worker has the pin; document-worker itself carries `amd64-only image run via OrbStack's Rosetta translation` — re-check per-class before copying verbatim), has the `extraEnv` conditional block precedent (not needed for audio's own concurrency since `AUDIO_WORKER_CONCURRENCY=1` is already the production default, unlike document's tunable-2 default), and is gated on `keda.enabled && prometheus.enabled` for `spec.replicas` omission exactly like the other 2 scaled classes.
**When to use:** As the copy-paste base for `deployment-audio-worker.yaml`.
**Example (from `deploy/chart/octoconv/templates/deployment-document-worker.yaml`):**
```yaml
spec:
  {{- if and .Values.keda.enabled .Values.prometheus.enabled }}
  # spec.replicas intentionally omitted when KEDA/HPA owns this Deployment
  # (WR-02, Phase 28 D-10): rendering a fixed value here would make every
  # `helm upgrade` reset a scaled-to-zero class back to this default,
  # fighting the HPA.
  {{- else }}
  replicas: {{ .Values.audioWorker.replicas }}
  {{- end }}
```
**Important divergence:** `audio-worker` is built with `-DGGML_NATIVE=OFF` from source (`Dockerfile.audio-worker`) — it is **architecture-portable**, not amd64-only like document-worker's LibreOffice binary. Do **not** copy document-worker's Rosetta-emulation framing into the audio header comment; audio-worker has no `platform:` pin in compose (`32-04-SUMMARY.md`: "no platform pin — whisper.cpp is source-built portable, chromium-worker precedent" — note this text actually cites chromium as the portability precedent, not document; verify Dockerfile.audio-worker has no hardcoded `--platform` before finalizing the chart Deployment, since a wrong assumption here silently breaks multi-arch nodes).

### Pattern 2: ScaledObject template — clone `document`, keep the WR-01 fail-safe verbatim
**What:** Every existing ScaledObject (`image`, `document`, `html`) already has the Phase-29 WR-01 fix applied identically: `ignoreNullValues: "false"`, `fallback: {failureThreshold: 3, replicas: 1}`, retry-inclusive PromQL (`state=~"pending|active|retry"`). SC1 explicitly requires audio's ScaledObject ship with this from its first commit — copy it verbatim, do not re-derive it.
**When to use:** Base for `scaledobject-audio.yaml`.
**Example (from `deploy/chart/octoconv/templates/scaledobject-document.yaml`):**
```yaml
{{- if and .Values.keda.enabled .Values.prometheus.enabled }}
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: worker-audio-scaledobject
  namespace: {{ .Values.global.namespace }}
spec:
  scaleTargetRef:
    name: audio-worker
  minReplicaCount: 0
  maxReplicaCount: {{ .Values.keda.audio.maxReplicaCount }}
  pollingInterval: {{ .Values.keda.audio.pollingInterval }}
  cooldownPeriod: {{ .Values.keda.audio.cooldownPeriod }}
  # NOTE (divergence from document): unlike document's optional/null-by-default
  # block, audio's scaleDownStabilizationSeconds MUST be non-null in
  # PRODUCTION values.yaml — see Common Pitfalls below.
  advanced:
    horizontalPodAutoscalerConfig:
      behavior:
        scaleDown:
          stabilizationWindowSeconds: {{ .Values.keda.audio.scaleDownStabilizationSeconds }}
          policies:
            - type: Pods
              value: 1
              periodSeconds: 15
  fallback:
    failureThreshold: 3
    replicas: 1
  triggers:
    - type: prometheus
      metadata:
        serverAddress: http://prometheus.{{ .Values.global.namespace }}.svc.cluster.local:9090
        query: sum(octoconv_queue_depth{queue="audio", state=~"pending|active|retry"})
        threshold: {{ .Values.keda.audio.threshold | quote }}
        ignoreNullValues: "false"
{{- end }}
```
**Divergence from the `hasKey`+`ne nil` falsy-0 guard pattern:** Since audio's `scaleDownStabilizationSeconds` is *always* set (non-null) in production, the `{{- if and (hasKey ...) (ne ... nil) }}` conditional block used by `scaledobject-document.yaml` (needed there because document's value legitimately IS `null` in production) is not strictly required for audio — but keeping the same guarded-block shape (never a bare unconditional render) is still good defensive practice in case a future overlay sets it back to `null`. Recommend keeping the guard for consistency even though it will always evaluate true in the shipped default.

### Pattern 3: ConfigMap — single choke point, additive-only edit
**What:** `octoconv.commonEnv` (`_helpers.tpl`) makes every Deployment pull the entire `ConfigMap` + `Secret` via `envFrom` — DEBT-05 requires every process carry the full retry/timeout surface even when it doesn't need it (mirrors compose's IN-02 propagation-to-all-7-services requirement). Adding `AUDIO_*` keys to `configmap.yaml` is the **only** place they need to be added; no per-Deployment env editing required beyond the new `deployment-audio-worker.yaml` file itself.
**Example — exact 5 keys to add** (values from `.env.example`/`docker-compose.yml`, Phase 32 measured):
```yaml
data:
  # ... existing keys unchanged ...
  AUDIO_WORKER_CONCURRENCY: "1"
  AUDIO_ENGINE_TIMEOUT: "742s"
  AUDIO_MAX_RETRY: "3"
  AUDIO_MAX_DURATION_SECONDS: "1800"
  AUDIO_MODEL_PATH: "/models/ggml-base.bin"
  # FIX (was "5m" — stale, same bug compose fixed in Phase 32 IN-16):
  RECONCILER_ACTIVE_STALE_AFTER: "15m"
```
[VERIFIED: repo read — `deploy/chart/octoconv/templates/configmap.yaml` currently has none of the 5 `AUDIO_*` keys and still reads `RECONCILER_ACTIVE_STALE_AFTER: "5m"` at line 38, while `docker-compose.yml` was corrected to `"15m"` in Phase 32 Plan 04 (IN-16, commit `137c6d0`)]

### Pattern 4: `terminationGracePeriodSeconds` — established formula
**What:** Every existing worker class uses `terminationGracePeriodSeconds = ENGINE_TIMEOUT + 30s` (10s asynq `ShutdownTimeout` margin + 20s k8s SIGKILL margin), confirmed identically across all 3 classes:
- image: `ENGINE_TIMEOUT=120s` → `ShutdownTimeout=130s` → grace=`150s` (margin over ShutdownTimeout: 20s)
- document: `DOCUMENT_ENGINE_TIMEOUT=300s` → `ShutdownTimeout=310s` → grace=`330s` (margin: 20s)
- html: `HTML_ENGINE_TIMEOUT=60s` → `ShutdownTimeout=70s` → grace=`90s` (margin: 20s)
[VERIFIED: repo grep — `cmd/worker/main.go:89`, `cmd/document-worker/main.go:95`, `cmd/chromium-worker/main.go:86`, cross-referenced against `values.yaml` grace values]

**For audio:** `AUDIO_ENGINE_TIMEOUT=742s` → `ShutdownTimeout=752s` (`cmd/audio-worker/main.go:113`, already shipped) → applying the identical +30s-over-ENGINE_TIMEOUT convention → **`terminationGracePeriodSeconds = 772s`**.

**Divergence note:** `docker-compose.yml`'s `stop_grace_period: 762s` for audio-worker (`docker-compose.yml:373`) uses a *different*, smaller margin (`ShutdownTimeout + 10s`, not the chart's established `ENGINE_TIMEOUT + 30s` convention) — compose has no precedent from the other 3 classes to compare against (none of them set `stop_grace_period` explicitly in compose; it's a k8s-chart-only convention). **772s (chart convention) is stricter and satisfies SC4 (`> AUDIO_ENGINE_TIMEOUT`) with more margin than 762s would** — recommend 772s for consistency with the established chart pattern, flagged `[ASSUMED — recommend confirming this specific number in discuss-phase or plan-checker, since it is inferred from a 3-sample pattern, not documented as a formula anywhere]`.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Absent-PromQL-result handling | A custom "is the api pod down" check | `ignoreNullValues: "false"` + `fallback.replicas: 1` (already proven on 3 ScaledObjects) | This exact problem was already solved and hardened across Phases 27-29; re-deriving it for audio risks regressing the WR-01 fix |
| Scale-from-zero timing evidence | Manual screenshots/informal notes | `scripts/keda-load-proof.sh`'s CSV-sampler + `render_evidence.py` PNG pattern (`scripts/fixtures/render_evidence.py`) | Already timestamped, committed-evidence-directory convention exists (`.planning/phases/28-.../evidence/`); reuse the shape for `.planning/phases/33-.../evidence/` |
| Pod-victim selection for a downscale proof | Imperative pod deletion | `controller.kubernetes.io/pod-deletion-cost=-1000` annotation (keda-load-proof.sh SC3 pattern) | Keeps the downscale a genuine KEDA/HPA event, not a scripted fake — required for SC4's "genuine N→N-1" wording |
| cgroup-aware thread sizing | New audio-specific cgroup detection code | Already shipped `cgroupCPULimit()` (Phase 32 Plan 02) | Chart's `resources.limits.cpu: "2"` on the Deployment is read by the SAME runtime cgroup-detection code already in `cmd/audio-worker` — no new code needed, only the correct `resources.limits` values in the chart |

**Key insight:** This phase's failure mode is not "wrong code" — it's "silently dropped invariant from a sibling class." Every number here (grace period, stabilization window, ConfigMap keys, retry-inclusive query, `ignoreNullValues`) already has a correct precedent sitting in the repo; the risk is copying an incomplete subset (e.g. cloning image/html's ScaledObject, which lack `scaleDownStabilizationSeconds` entirely, instead of document's, which has the needed block).

## Common Pitfalls

### Pitfall 1: ConfigMap silently missing `AUDIO_*` keys (chart currently has zero of them)
**What goes wrong:** `helm install`/`helm upgrade` succeeds, `audio-worker` pod starts, but every `envDuration("AUDIO_ENGINE_TIMEOUT", 600*time.Second)`-style call in `cmd/audio-worker/main.go` silently falls back to its Go-code default (600s placeholder) instead of the measured 742s — because the env var is simply absent from the pod's environment.
**Why it happens:** `configmap.yaml` was last touched before audio existed (Phase 27/28 predates Phase 30-32); nothing in Phase 32's scope touched the chart (compose-only, deliberately scope-fenced per the phase's `<additional_context>`).
**How to avoid:** Add the 5 keys explicitly (Pattern 3 above); grep-verify post-edit: `grep -c 'AUDIO_' deploy/chart/octoconv/templates/configmap.yaml` should be 5.
**Warning signs:** A live pod's actual timeout behaves like 600s/2/14400 instead of 742s/1/1800 — only detectable by checking `kubectl exec ... env` or by a timing anomaly in the load-proof, not by `helm template` (which would render fine either way — a missing key is not a template syntax error).

### Pitfall 2: Chart's `RECONCILER_ACTIVE_STALE_AFTER` still at the stale `5m` compose already fixed
**What goes wrong:** Deploying `AUDIO_ENGINE_TIMEOUT=742s` (12.4min) against a `5m` reconciler stale-threshold means the reconciler treats any audio job still legitimately running past 5 minutes as a crashed worker and attempts recovery — reopening the exact double-processing risk Phase 32's IN-16 fix closed in compose.
**Why it happens:** The chart's `configmap.yaml` and compose's `docker-compose.yml` are two independently-maintained env surfaces; Phase 32 fixed only compose (explicitly scope-fenced away from the chart per the roadmap).
**How to avoid:** Change `RECONCILER_ACTIVE_STALE_AFTER: "5m"` → `"15m"` in the SAME commit as the `AUDIO_*` key additions — do not ship one without the other (mirrors compose's IN-16 fix, which explicitly required both changes land together, per `32-04-SUMMARY.md`'s "GO decision explicitly DEPENDS on" language).
**Warning signs:** `742s < 300s` is FALSE (742 > 300) — the invariant `AUDIO_ENGINE_TIMEOUT < RECONCILER_ACTIVE_STALE_AFTER` is violated at `5m`; grep-verify post-fix: the invariant check pattern from `32-04-SUMMARY.md` (`AUDIO_ENGINE_TIMEOUT strictly below RECONCILER_ACTIVE_STALE_AFTER`) should be re-run against the chart's rendered ConfigMap, not just compose.

### Pitfall 3: Copying `image`/`html`'s ScaledObject (no `scaleDownStabilizationSeconds` block) instead of `document`'s
**What goes wrong:** If audio's ScaledObject is cloned from `scaledobject-image.yaml` or `scaledobject-html.yaml` (neither has any `scaleDownStabilizationSeconds`/`advanced.horizontalPodAutoscalerConfig` block at all — confirmed by direct read), the N→N-1 downscale falls back to k8s's HPA default of 300s stabilization — which is LESS than `AUDIO_ENGINE_TIMEOUT` (742s). A genuine 2→1 downscale could then fire while a 742s-long transcription is still in-flight but the pod hasn't been running long enough to "look busy" to the stabilization window, risking a premature SIGTERM race that SC4 exists specifically to prevent.
**Why it happens:** 2 of the 3 existing ScaledObjects (image, html) simply never needed this block — their job durations (120s, 60s ENGINE_TIMEOUT) fit comfortably inside the 300s k8s default, so no override was ever added for them. Only `document`'s ScaledObject has the block (added specifically for the Phase 28 load-proof's SC3 timing requirement, defaulting to `null`/off in production).
**How to avoid:** Clone from `scaledobject-document.yaml`, not `scaledobject-image.yaml`/`scaledobject-html.yaml`. Set `keda.audio.scaleDownStabilizationSeconds` to a concrete non-null value in **production** `values.yaml` (unlike document, whose production default stays `null` because document's own jobs never approach 300s) — see Pattern 2 divergence note above.
**Warning signs:** `helm template` renders cleanly either way (this is a silent correctness gap, not a template error) — only a live SC4-style downscale-soak proof against a genuinely-long audio job would surface it, and only probabilistically (timing-dependent race).

### Pitfall 4: Reusing `keda-load-proof.sh`'s `CALIBRATE` mode mental model for audio
**What goes wrong:** Document's load-proof needed a live `CALIBRATE=1` trial run because `DOCUMENT_ENGINE_TIMEOUT` was NOT yet fixed at gate-authoring time in that phase's precedent workflow, and no local LibreOffice binary exists to dry-run against (`28-RESEARCH.md` Pitfall 4). Audio does not have this problem: `AUDIO_ENGINE_TIMEOUT=742s` is ALREADY a hard, RTF-measured, committed constant from Phase 32 — there is nothing left to calibrate for the timeout itself.
**Why it matters:** Building an unnecessary calibration mode into the new audio load-proof script adds scope and a live-cluster round-trip for no reason; the audio load-proof script's job is narrower — prove scale-from-zero timing and separate image-pull time from orchestration time, not re-derive a timeout.
**How to avoid:** Design the new script around SC3's actual ask (timestamped 0→1 scale-from-zero with image-pull-vs-cold-start evidence for the 682MB image) using `jfk.wav` (already committed at `internal/e2e/testdata/jfk.wav`, 11s audio, 352,078 bytes) as the trigger fixture — a short job is sufficient to observe cold-start timing; a full 742s-worst-case job is not needed for this measurement.

### Pitfall 5: Assuming OrbStack's shared local Docker image store means "no meaningful pull time to measure"
**What goes wrong:** Because OrbStack's k8s runtime shares the SAME local Docker image store the `docker build` command populates, and every chart Deployment uses `imagePullPolicy: IfNotPresent` (or the global equivalent), a locally pre-built `octoconv-audio-worker:dev` image is very likely to resolve near-instantly on `Pulling` — there may be **no real network pull to measure at all** in this environment, unlike a genuine production deployment pulling a 682MB image from a remote registry. If the SC3 measurement script assumes a meaningful, separable "pull phase" exists and tries to force one (e.g. via `imagePullPolicy: Always` against a non-existent registry path), it will fail outright rather than produce a measurement.
**Why it happens:** OrbStack's dev-convenience image-store sharing is architecturally different from a real multi-node cluster with a registry — this is a known environmental limitation, not a code bug.
**How to avoid:** Design the measurement to capture and report the FULL `kubectl describe pod` event timeline (`Scheduled → Pulling → Pulled → Created → Started`) with real timestamps, whatever they turn out to be. If `Pulling→Pulled` is near-zero, that itself IS the measured evidence answering SC3's "image-pull vs scale-from-zero" question for THIS environment (bake-in imposes ~0 extra pull cost on OrbStack) — explicitly document this as an environment-scoped finding, with a residual-risk note that a real registry-backed production deployment was not measured and could behave differently (a genuine 682MB registry pull could take tens of seconds to minutes depending on network).
**Warning signs:** A measurement script that never separates `Pulling`/`Pulled` event timestamps from `Scheduled`/`Started` would produce a single opaque "time to Running" number, which cannot actually answer Key Decision 3's bake-vs-volume question (the question specifically wants pull time isolated from orchestration/cooldown time).

### Pitfall 6: Forgetting `queue.QueueAudio` in `cmd/api/main.go`'s collector registration
**What goes wrong:** `NewQueueDepthCollector` is currently called with exactly 4 queue names (`QueueImage, QueueDocument, QueueHTML, QueueWebhook`) — `QueueAudio` is missing. Without it, `octoconv_queue_depth{queue="audio",...}` never appears in `/metrics` output at all (not zero — genuinely absent, a distinct Prometheus series that was never registered), meaning the audio ScaledObject's PromQL query permanently reads as an absent result and (thanks to the correctly-applied `ignoreNullValues:false`/WR-01 fix) permanently holds `fallback.replicas: 1` — **the audio worker never scales to genuine 0**, violating SC1/SC2 outright.
**Why it happens:** This 4-queue collector call predates the audio engine class (Phase 27, KEDA-01); nothing in Phases 30-32 touched it since queue-depth collection is API-side, not audio-worker-side.
**How to avoid:** One-line change in `cmd/api/main.go`:
```go
prometheus.MustRegister(metrics.NewQueueDepthCollector(asynq.NewInspector(redisOpt),
    queue.QueueImage, queue.QueueDocument, queue.QueueHTML, queue.QueueAudio, queue.QueueWebhook))
```
[VERIFIED: repo read, `cmd/api/main.go:91-92`, current call has exactly 4 queue arguments; `queue.QueueAudio = convert.EngineAudio = "audio"` already defined since Phase 31, `internal/queue/queue.go:38`]
**Warning signs:** `kubectl get scaledobject worker-audio-scaledobject -o yaml` shows the ScaledObject exists and is Active, but `status.replicas` on `audio-worker` never drops below 1 even after a long idle period — the classic symptom of a permanently-triggered `fallback.replicas` due to an absent metric.

### Pitfall 7: `asynq:queues` Redis registry never seeded with "audio" in a fresh-install live gate
**What goes wrong:** `keda-gate.sh`'s STEP 4b explicitly seeds `SADD asynq:queues image document html webhook` to make the WR-01 absent-metric fallback resolve to a genuine zero on a fresh install (asynq only registers a queue name in Redis on its FIRST real enqueue). If a new audio-specific live-proof script reuses this pattern but forgets to add `audio` to the seeded set, the exact same "fallback blip never resolves to zero" symptom as Pitfall 6 will appear — but for a completely different root cause (missing Redis seed, not a missing Go collector arg).
**How to avoid:** Any new script that installs a fresh octoconv release and expects to observe `audio-worker` settle to genuine 0 replicas MUST seed `SADD asynq:queues ... audio ...` (or accept a longer wait for the first real audio job's enqueue to naturally register the queue).
**Warning signs:** Same as Pitfall 6 (worker never reaches 0) but the Go-level `cmd/api/main.go` fix (Pitfall 6) IS correctly applied — distinguishing the two requires checking `redis-cli SMEMBERS asynq:queues` directly.

## Code Examples

### `cmd/api/main.go` — exact splice for QueueAudio (verified current state)
```go
// Source: /Users/apaderin/dev/octoconv/cmd/api/main.go:91-92 (current, BEFORE this phase's fix)
prometheus.MustRegister(metrics.NewQueueDepthCollector(asynq.NewInspector(redisOpt),
	queue.QueueImage, queue.QueueDocument, queue.QueueHTML, queue.QueueWebhook))
// AFTER (this phase): insert queue.QueueAudio between QueueHTML and QueueWebhook
// (order is cosmetic — Collect() iterates whatever set is passed; keeping
// image/document/html/audio/webhook order groups scaled classes before the
// fixed-replica webhook class, matching the existing declaration order in
// internal/queue/queue.go:34-38)
```

### `deploy/chart/octoconv/templates/configmap.yaml` — current state (BEFORE this phase)
```yaml
# Source: /Users/apaderin/dev/octoconv/deploy/chart/octoconv/templates/configmap.yaml:38
# (verified current, no AUDIO_* keys exist anywhere in this file)
RECONCILER_ACTIVE_STALE_AFTER: "5m"   # STALE — compose already fixed this to "15m" (32-04, IN-16)
```

## State of the Art

| Old Approach (Phase 27/28, image/document/html only) | Current Approach (Phase 33, +audio) | When Changed | Impact |
|--------------------------------------------------------|--------------------------------------|---------------|--------|
| 4-queue collector (`Image,Document,HTML,Webhook`) | 5-queue collector (`+Audio`) | This phase | Without this, audio can never scale to genuine 0 (Pitfall 6) |
| `scaleDownStabilizationSeconds: null` universally OK in production (all job durations << 300s k8s default) | Audio needs a non-null production value (742s worst-case > 300s default) | This phase | First class where the k8s HPA default alone is provably insufficient — genuinely new territory, not a copy-paste |
| ConfigMap keys added per-class as each engine ships (image day 1, document/html Phase 27, audio... not yet) | Audio's keys were never added despite the engine shipping in Phase 30-32 | This phase closes the gap | Chart and compose have silently diverged since Phase 30 |

**Deprecated/outdated:** None — no library/tool versions are changing this phase, only chart content.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | `terminationGracePeriodSeconds = 772s` (ENGINE_TIMEOUT+30s convention) is the right value for audio, rather than compose's `762s` (ShutdownTimeout+10s) | Pattern 4 | Low — both values satisfy SC4's literal requirement (`> 742s`); only affects consistency with the established chart pattern, not correctness. Planner/discuss-phase should pick one deliberately. |
| A2 | `keda.audio.threshold="1"`, `maxReplicaCount=2`, `pollingInterval=15`, `cooldownPeriod=180` are reasonable starting values (not derived from any measurement, unlike scaleDownStabilizationSeconds/grace period) | Common Pitfalls / general recommendation | Low-Medium — these are demo/starting values by the same convention as image/document/html's own values.yaml comment ("These are demo starting values; production tuning is Phase 28" — audio has no equivalent follow-up phase in v1.7, so these ARE the shipped production values, not just a starting point). Planner should treat these as needing explicit sign-off in discuss-phase, not silent inheritance. |
| A3 | `document-worker` (not `image`/`html`) is the correct template-clone base for `deployment-audio-worker.yaml` | Pattern 1 | Low — architecturally justified (only document has the scaleDownStabilizationSeconds precedent) but not independently verified against a hypothetical better base |
| A4 | Audio's chart Deployment does NOT need a `startupProbe` (unlike document/chromium, which have one for "heavy image cold start") | Common Pitfalls (implicit) / not covered above by a dedicated pitfall — noted here | Medium — the 682MB image + 147MB baked model is objectively "heavy" by the same reasoning document/chromium's headers use; the Phase 32 E2E's fast observed container-to-first-job time (~6s, compose-only, image already warm) does not test a genuinely cold k8s-node pull scenario. Recommend adding a `startupProbe` (mirroring document/chromium's `periodSeconds:5, failureThreshold:24` = 120s budget) defensively unless SC3's live measurement proves startup is fast even cold. |
| A5 | No `extraEnv` block is needed on `audioWorker` in `values.yaml` (unlike `documentWorker`) because `AUDIO_WORKER_CONCURRENCY` is already hardcoded to the production-safe value of 1 | Pattern 3 | Low — confirmed by Phase 32's measurement that concurrency=2 fails both memory and CPU fit checks; no known future scenario needs a per-overlay override |
| A6 | OrbStack's shared local Docker image store means there is no separable, meaningful "image pull" phase to measure for a `dev`-tagged locally-built image under `IfNotPresent` | Pitfall 5 | Medium-High — this is the crux of Key Decision 3's live-measurement requirement; if wrong (i.e., OrbStack DOES do a real pull step with non-trivial latency for large images even from its own store), the whole "measure pull vs scale-from-zero" plan needs a different mechanism (e.g. explicit `docker rmi` + `orb` cache eviction before the test) that this research did not investigate |

## Open Questions

1. **Does `scripts/keda-gate.sh` need an audio SC2-style entry (0→1 scale-up smoke check), or is the new audio load-proof script sufficient coverage?**
   - What we know: `keda-gate.sh`'s own header frames SC2 as "all three scaled classes (image/document/html) scale 0→1 ... catches doc/html-specific cold-start issues now, not in Phase 28" — audio is now a 4th scaled class with its own cold-start risk profile (682MB image).
   - What's unclear: Whether extending `keda-gate.sh` (an already-passing, "frozen" smoke gate per the established "leave `keda-load-proof.sh` byte-unchanged" precedent) is in scope, or whether the new dedicated audio script fully substitutes for that coverage.
   - Recommendation: Treat as Claude's Discretion / a discuss-phase question — leaning toward a minimal addition to `keda-gate.sh` (postJob + waitForReplicasAtLeast for audio, mirroring the existing 3-class pattern exactly) SEPARATELY from the new dedicated `keda-audio-loadproof.sh` script that carries the heavier SC3 image-pull/cold-start timestamped-evidence work — the two scripts serve different purposes (fast regression smoke vs. flagship timestamped proof) exactly as `keda-gate.sh` and `keda-load-proof.sh` already do for the other 3 classes.

2. **What exact mechanism forces a genuine image-pull (not a local-store cache hit) for the SC3 measurement, given OrbStack's shared Docker store?**
   - What we know: `imagePullPolicy: IfNotPresent` + a pre-built local `:dev` tag is the established convention for all 4 worker classes; this makes `Pulling→Pulled` near-instant in the current dev workflow.
   - What's unclear: Whether Phase 33 should (a) accept and document the near-zero pull time as the honest local-environment answer (Pitfall 5's recommendation), or (b) engineer an artificial cold-pull scenario (e.g. tag with a fresh unique tag + `imagePullPolicy: Always` + a local registry push/pull round-trip) to produce a more production-representative number.
   - Recommendation: Default to (a) for this phase (matches the "measure, don't assume" spirit of Key Decision 3 while staying within the phase's OrbStack-only scope), explicitly flag (b) as a documented residual-risk / out-of-scope item for a future real-cluster validation, rather than building new registry-push tooling this phase.

3. **Exact final values for `keda.audio.{threshold,maxReplicaCount,pollingInterval,cooldownPeriod}` (A2 above)** — no formula precedent exists for these (unlike `scaleDownStabilizationSeconds`/grace-period, which have derivable formulas); recommend surfacing as explicit discuss-phase decision points rather than silently choosing values in the plan.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| OrbStack Kubernetes | Live scale-from-zero proof (SC3), keda-load-proof.sh re-run | Must be started by operator (`orb start k8s`) before live gates run — not verified in this research session (no live cluster probe was run; STATE.md confirms compose AND k8s are both currently down) | — | None — SC3 has no non-live substitute, per the phase's own goal text |
| KEDA v2.20.1 (Helm repo `kedacore/keda`) | ScaledObject CRDs | Re-verified resolvable live at each gate script's own run time (`helm search repo kedacore/keda --versions`), not re-checked in this research session | 2.20.1 (pinned in both existing gate scripts) | Script itself fails loudly if the pinned version becomes unresolvable — re-pin required, not a silent fallback |
| `docker`/OrbStack daemon | Pre-building `octoconv-audio-worker:dev` before any k8s gate | Assumed available (used throughout Phase 32); not re-probed this session since this is a chart/YAML research task, not a live-execution task | — | — |
| `uv` (Python venv runner) | `render_evidence.py` PNG rendering (if the new audio script reuses this pattern) | Used successfully throughout Phase 28's evidence pipeline; assumed still available, not re-probed | — | — |

**Missing dependencies with no fallback:**
- A live, running OrbStack k8s cluster — required for every SC1-SC4 success criterion; this research phase did not (and per its own scope should not) bring one up.

## Security Domain

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V4 Access Control | Partial | No new access-control surface — `audio-worker` pods carry the same `octoconv.io/tier: app` label as the other 3 worker classes, automatically covered by the existing `networkpolicy-metrics.yaml` (podSelector on `tier: app`, no new NetworkPolicy needed) |
| V6 Cryptography | No | No new secrets/crypto — `audio-worker` reuses the existing `octoconv.io/tier` + `octoconv.commonEnv` (ConfigMap/Secret) pattern verbatim |
| V14 Configuration | Yes | ConfigMap is the single source of truth for `AUDIO_*` env (Pattern 3) — misconfiguration here (Pitfalls 1/2) is a correctness/availability risk, not a confidentiality risk, since none of the new keys are secret-shaped |

### Known Threat Patterns for this stack (chart/orchestration-scoped)

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Resource exhaustion via unbounded audio-worker replica count | Denial of Service | `maxReplicaCount` cap (already the established pattern for all 3 sibling classes) — set explicitly for audio, not left unbounded |
| Silent metric/config drift between compose and chart (Pitfalls 1/2) | Tampering (of intended operational invariants, not an external attacker) | Explicit grep-verification steps (as documented in Pitfalls 1/2) as a plan verification gate, mirroring the pattern `32-04-SUMMARY.md` already used for compose's own invariant re-check |

No new external attack surface is introduced by this phase — it is exclusively internal chart/orchestration wiring for an already-hardened, already-authenticated engine class (audio validation/injection-safety was Phase 30's scope, unchanged here).

## Sources

### Primary (HIGH confidence — direct repo read this session)
- `deploy/chart/octoconv/templates/{deployment-worker,deployment-document-worker,deployment-webhook-worker,deployment-chromium-worker,scaledobject-image,scaledobject-document,scaledobject-html,configmap,secret,prometheus,job-e2e,job-createbucket,networkpolicy-metrics,_helpers.tpl}.yaml` — full read, this session
- `deploy/chart/octoconv/{values.yaml,values-local.yaml,values-loadproof.yaml,values-e2e.yaml,Chart.yaml}` — full read, this session
- `cmd/api/main.go`, `cmd/audio-worker/main.go` (grep), `cmd/worker/main.go`, `cmd/document-worker/main.go`, `cmd/chromium-worker/main.go` (grep) — this session
- `internal/queue/queue.go` (QueueAudio, audioRetrySchedule, AudioRetryDelay) — this session
- `internal/convert/convert.go` (`EngineAudio = "audio"`) — this session
- `scripts/keda-gate.sh`, `scripts/keda-load-proof.sh` — full read, this session
- `docker-compose.yml` (audio-worker service block), `.env.example` (AUDIO_* block) — this session
- `.planning/phases/32-containerization-local-e2e-rtf-gate/{32-03,32-04,32-05}-SUMMARY.md` — full read, this session
- `.planning/phases/29-v1-6-hardening-tail/{29-01,29-03}-SUMMARY.md`, `29-VERIFICATION.md`, `29-HUMAN-UAT.md` — this session (deferred keda-load-proof.sh obligation)
- `.planning/milestones/v1.6-phases/28-autoscale-load-proof/28-03-SUMMARY.md`, `28-VERIFICATION.md` — this session (11s/+6s first-replica precedent)
- `.planning/{REQUIREMENTS.md,STATE.md,ROADMAP.md,config.json}` — this session
- `Dockerfile.e2e` — this session (confirms audio E2E test auto-included in in-cluster Job, no job-e2e.yaml edit needed)

### Secondary (MEDIUM confidence)
- None used — all findings this phase are grounded in direct repo reads (this is a chart-authoring phase against an already-built system, not an external-library research phase)

### Tertiary (LOW confidence)
- OrbStack image-store-sharing behavior and its effect on measurable pull latency (Pitfall 5 / Assumption A6) — reasoned from documented OrbStack conventions already present in the repo's own comments (`values.yaml:109-111` "OrbStack re-pulls :latest even from its shared local image store — D-02 landmine fix"), not independently verified against a live cluster this session

## Metadata

**Confidence breakdown:**
- Standard stack / chart template patterns: HIGH — every claim is a direct read of the existing, working 3-class chart plus grep-verified Go source
- Numeric tuning (grace period, ConfigMap fix): HIGH for the load-bearing/formula-derived values (grace period formula, ConfigMap gap); MEDIUM for the non-formula-derived KEDA tuning knobs (threshold/maxReplicaCount/pollingInterval/cooldownPeriod — Assumption A2)
- Scale-from-zero / image-pull-vs-cold-start measurement design: LOW-MEDIUM — architecturally reasoned from OrbStack's documented behavior, but the actual measurement has not been run; this is precisely what SC3 exists to determine empirically

**Research date:** 2026-07-18
**Valid until:** Effectively unbounded for the chart-pattern findings (stable, internal repo conventions); the OrbStack pull-time reasoning (Pitfall 5) should be re-validated the moment SC3's live script actually runs, since it is a prediction, not yet an observation.
