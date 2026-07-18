# Phase 33: KEDA/Helm Chart Integration - Pattern Map

**Mapped:** 2026-07-18
**Files analyzed:** 7 (2 new templates, 3 edits, 1 new script, 1 evidence dir convention)
**Analogs found:** 7 / 7

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|--------------------|------|-----------|-----------------|----------------|
| `deploy/chart/octoconv/templates/deployment-audio-worker.yaml` (new) | config (k8s Deployment manifest template) | event-driven (KEDA-scaled worker pod) | `deploy/chart/octoconv/templates/deployment-document-worker.yaml` | exact (structural clone target per RESEARCH Pattern 1) |
| `deploy/chart/octoconv/templates/scaledobject-audio.yaml` (new) | config (KEDA CRD template) | event-driven (Prometheus-scaler-driven autoscale) | `deploy/chart/octoconv/templates/scaledobject-document.yaml` | exact (only sibling with the `scaleDownStabilizationSeconds` block) |
| `deploy/chart/octoconv/templates/configmap.yaml` (edit) | config | CRUD (env key-value store) | itself, current state (add 5 `AUDIO_*` keys + fix stale `5m`→`15m`) | exact (additive edit to existing file) |
| `deploy/chart/octoconv/values.yaml` (edit) | config | CRUD | itself — existing `documentWorker`/`keda.document` blocks as the template for `audioWorker`/`keda.audio` | exact (additive edit) |
| `deploy/chart/octoconv/values-local.yaml` (no change) | config | — | N/A | not-applicable (`keda.enabled`/`prometheus.enabled` already `true` globally, no per-class local override exists for image/document/html either) |
| `cmd/api/main.go` (edit, 1 line) | controller / wiring (metrics collector registration) | request-response (Prometheus scrape → collector) | itself, current 4-arg `NewQueueDepthCollector` call (`cmd/api/main.go:91-92`) | exact (splice one more const into an existing variadic call) |
| `scripts/keda-audio-loadproof.sh` (new) | utility (live-cluster gate/proof script) | event-driven / batch (submits jobs, polls k8s state, emits timestamped evidence) | `scripts/keda-load-proof.sh` (structural clone; itself stays byte-unchanged) | role-match (same shape, narrower scope — no CALIBRATE mode per Pitfall 4) |
| `.planning/phases/33-keda-helm-chart-integration/evidence/` (new dir, populated at execution time) | artifact/evidence directory | file-I/O (timestamped CSV/PNG/log/txt artifacts) | `.planning/milestones/v1.6-phases/28-autoscale-load-proof/evidence/` | exact (same 4-artifact-type convention) |

## Pattern Assignments

### `deploy/chart/octoconv/templates/deployment-audio-worker.yaml` (config, event-driven)

**Analog:** `deploy/chart/octoconv/templates/deployment-document-worker.yaml` (full file, 87 lines — read in one pass)

**Header comment pattern** (lines 1-16 of analog):
```yaml
{{/*
document-worker Deployment — document-conversion engine class (LibreOffice
+ veraPDF), amd64-only image run via OrbStack's Rosetta translation (D-10;
no nodeSelector needed on this single-node local cluster — a multi-node
cluster would need one, out of scope here).

TIER LABEL: pod template carries octoconv.io/tier: app (see
deployment-worker.yaml header for the full rationale — identical here).

Grace 330s (D-09) is ≥ DOCUMENT_ENGINE_TIMEOUT (300s, ConfigMap) + margin.

startupProbe: LibreOffice cold start under Rosetta emulation is slow — a
generous startup budget (failureThreshold x periodSeconds ≈ 120s) keeps
this pod out of a CrashLoopBackOff before the metrics listener is even up,
while readiness/liveness stay tight once startup has succeeded.
*/}}
```
**CRITICAL divergence for audio:** do NOT copy the amd64/Rosetta framing verbatim — `audio-worker` is built `-DGGML_NATIVE=OFF` (source-built, portable), confirmed no `--platform` pin anywhere in `Dockerfile.audio-worker` (grepped: only `FROM golang:1.26-bookworm`, `FROM debian:bookworm-slim` x2, no `--platform`) and explicitly called out as portable in `scripts/audio-rtf-measure.sh:26-28`. Grace value is `772s` (`742s + 30s`, per RESEARCH Pattern 4), not `330s`.

**`spec.replicas` KEDA-omission pattern** (lines 24-33, copy verbatim, only `documentWorker`→`audioWorker`):
```yaml
spec:
  {{- if and .Values.keda.enabled .Values.prometheus.enabled }}
  # spec.replicas intentionally omitted when KEDA/HPA owns this Deployment
  # (WR-02, Phase 28 D-10): rendering a fixed value here would make every
  # `helm upgrade` reset a scaled-to-zero class back to this default,
  # fighting the HPA. Once KEDA/HPA has taken ownership it manages
  # spec.replicas directly via the scale subresource.
  {{- else }}
  replicas: {{ .Values.documentWorker.replicas }}
  {{- end }}
```

**Container/env/probes/resources pattern** (lines 43-87, copy verbatim structure — drop the `extraEnv` block per Assumption A5, `AUDIO_WORKER_CONCURRENCY` has no need for a per-overlay override):
```yaml
    spec:
      terminationGracePeriodSeconds: {{ .Values.documentWorker.terminationGracePeriodSeconds }}
      containers:
        - name: document-worker
          image: "{{ .Values.documentWorker.image.repository }}:{{ .Values.global.imageTag }}"
          imagePullPolicy: IfNotPresent
          {{- include "octoconv.commonEnv" . | nindent 10 }}
          ports:
            - name: metrics
              containerPort: 9090
          startupProbe:
            httpGet:
              path: /metrics
              port: 9090
            periodSeconds: 5
            failureThreshold: 24
          readinessProbe:
            httpGet:
              path: /metrics
              port: 9090
            periodSeconds: 5
            failureThreshold: 3
          livenessProbe:
            httpGet:
              path: /metrics
              port: 9090
            periodSeconds: 10
            failureThreshold: 6
          resources:
            limits:
              cpu: {{ .Values.documentWorker.resources.limits.cpu | quote }}
              memory: {{ .Values.documentWorker.resources.limits.memory | quote }}
```
Rename every `document-worker`/`documentWorker` token to `audio-worker`/`audioWorker`. Keep the `startupProbe` block (Assumption A4 recommends keeping it defensively — 682MB image + 147MB baked model is "heavy" by the same reasoning as document/chromium). No `volumeMounts`/`volumes` block needed (that's `chromium-worker`'s `/dev/shm` special-case, not applicable to audio — see `deploy/chart/octoconv/templates/deployment-chromium-worker.yaml:75-82` for reference/contrast only, not to copy).

---

### `deploy/chart/octoconv/templates/scaledobject-audio.yaml` (config, event-driven)

**Analog:** `deploy/chart/octoconv/templates/scaledobject-document.yaml` (full file, 79 lines — read in one pass)

**Full structure to clone** (lines 33-78 of analog, rename `document`→`audio` throughout):
```yaml
{{- if and .Values.keda.enabled .Values.prometheus.enabled }}
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: worker-document-scaledobject
  namespace: {{ .Values.global.namespace }}
  labels:
    {{- include "octoconv.labels" . | nindent 4 }}
    app.kubernetes.io/component: worker
spec:
  scaleTargetRef:
    name: document-worker
  minReplicaCount: 0
  maxReplicaCount: {{ .Values.keda.document.maxReplicaCount }}
  pollingInterval: {{ .Values.keda.document.pollingInterval }}
  cooldownPeriod: {{ .Values.keda.document.cooldownPeriod }}
  {{- if and (hasKey .Values.keda.document "scaleDownStabilizationSeconds") (ne .Values.keda.document.scaleDownStabilizationSeconds nil) }}
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
  triggers:
    - type: prometheus
      metadata:
        serverAddress: http://prometheus.{{ .Values.global.namespace }}.svc.cluster.local:9090
        query: sum(octoconv_queue_depth{queue="document", state=~"pending|active|retry"})
        threshold: {{ .Values.keda.document.threshold | quote }}
        ignoreNullValues: "false"
{{- end }}
```
**Divergence:** keep the `hasKey`+`ne nil` guard exactly as-is even though `keda.audio.scaleDownStabilizationSeconds` will always be non-null in production (defensive consistency, per RESEARCH Pattern 2 divergence note) — do NOT hardcode an unconditional render. The metadata `name:` becomes `worker-audio-scaledobject`, `scaleTargetRef.name:` becomes `audio-worker`, PromQL `queue="audio"`.

**Anti-pattern warning (Pitfall 3):** do NOT clone `scaledobject-image.yaml` (`deploy/chart/octoconv/templates/scaledobject-image.yaml`, read in full — 50 lines) — it has NO `scaleDownStabilizationSeconds`/`advanced.horizontalPodAutoscalerConfig` block at all, which is correct for image (120s ENGINE_TIMEOUT << 300s k8s default) but wrong for audio (742s > 300s default). Its `triggers`/`fallback`/`ignoreNullValues` shape is otherwise identical to document's and can be used to sanity-check the WR-01 fail-safe fields are unchanged across all 3+1 classes.

---

### `deploy/chart/octoconv/templates/configmap.yaml` (config, CRUD)

**Analog:** itself, current 43-line file (already read in full)

**Exact edit — insert new keys near the other per-class blocks, and fix the stale reconciler key** (current lines 25-40):
```yaml
  WORKER_CONCURRENCY: "4"
  DOCUMENT_WORKER_CONCURRENCY: "2"
  HTML_WORKER_CONCURRENCY: "2"
  WEBHOOK_WORKER_CONCURRENCY: "4"
  ENGINE_TIMEOUT: "120s"
  IMAGE_MAX_RETRY: "4"
  DOCUMENT_ENGINE_TIMEOUT: "300s"
  DOCUMENT_MAX_RETRY: "3"
  HTML_ENGINE_TIMEOUT: "60s"
  HTML_MAX_RETRY: "3"
  VERAPDF_TIMEOUT: "60s"
  WEBHOOK_PRESIGN_TTL: "6h"
  RECONCILER_QUEUED_STALE_AFTER: "90s"
  RECONCILER_ACTIVE_STALE_AFTER: "5m"          # <- MUST become "15m" (Pitfall 2)
  RECONCILER_SWEEP_INTERVAL: "1m"
  RECONCILER_MAX_RECOVERIES: "3"
```
**Add these 5 keys** (values verified against `docker-compose.yml:389-399` and `.env.example:51-63`, Phase 32 measured, must match exactly):
```yaml
  AUDIO_WORKER_CONCURRENCY: "1"
  AUDIO_ENGINE_TIMEOUT: "742s"
  AUDIO_MAX_RETRY: "3"
  AUDIO_MAX_DURATION_SECONDS: "1800"
  AUDIO_MODEL_PATH: "/models/ggml-base.bin"
```
**Change:** `RECONCILER_ACTIVE_STALE_AFTER: "5m"` → `"15m"` — MUST land in the same commit as the 5 new keys (Pitfall 2; mirrors compose's already-shipped IN-16 fix, `docker-compose.yml`'s equivalent key is already `"15m"`).

**Post-edit verification commands** (from RESEARCH Pitfalls 1-2, copy verbatim into the plan's verification step):
```bash
grep -c 'AUDIO_' deploy/chart/octoconv/templates/configmap.yaml   # expect 5
grep 'RECONCILER_ACTIVE_STALE_AFTER' deploy/chart/octoconv/templates/configmap.yaml   # expect "15m"
```

---

### `deploy/chart/octoconv/values.yaml` (config, CRUD)

**Analog:** itself, current file (already read in full, 171 lines)

**`audioWorker` block — clone shape from `documentWorker`** (current lines 49-57):
```yaml
documentWorker:
  image:
    repository: "octoconv-document-worker"
  replicas: 1
  terminationGracePeriodSeconds: 330
  resources:
    limits:
      cpu: "2"
      memory: "1Gi"
```
New `audioWorker` block: `repository: "octoconv-audio-worker"`, `terminationGracePeriodSeconds: 772` (per RESEARCH Pattern 4 — flagged `[ASSUMED]`, confirm in discuss-phase/plan-checker), `resources.limits` matching the RTF-measurement container exactly — `cpu: "2"`, `memory: "1Gi"` (must match `scripts/audio-rtf-measure.sh`'s `CPUS`/memory limits and `docker-compose.yml`'s audio-worker `deploy.resources` — verify against `docker-compose.yml:414+` before finalizing, since a mismatch here silently invalidates the 742s timeout's validity per the RESEARCH diagram note "must match RTF-measurement container EXACTLY").

**`keda.audio` block — clone shape from `keda.document`, but non-null stabilization** (current lines 160-165):
```yaml
  document:
    threshold: "1"
    maxReplicaCount: 2
    pollingInterval: 15
    cooldownPeriod: 120
    scaleDownStabilizationSeconds: null   # NEW (D-10/Pattern 1) — production default OFF (K8s 300s HPA default preserved); load-proof gate overrides via values-loadproof.yaml
```
New `audio` block under `keda:` — same 4 base keys (values are Assumption A2, "starting values," flagged for explicit discuss-phase sign-off per RESEARCH), but **`scaleDownStabilizationSeconds` must be a concrete non-null number in THIS file** (not `null`, unlike document) — e.g. a value derived to comfortably exceed 742s, since this is the first class where the block is load-bearing in production, not just a load-proof-overlay knob.

**Cooldown invariant to preserve** (comment at values.yaml:146-152, extend the same reasoning to audio):
```yaml
# INVARIANT (WR-06): each class's cooldownPeriod below must exceed that
# class's max per-task retry backoff step (internal/queue/queue.go —
# imageRetrySchedule tops out at 15s; documentRetrySchedule/htmlRetrySchedule
# top out at 30s), otherwise a ScaledObject can cool down to 0 replicas
# while a task is still legitimately mid-retry, stranding it until the
# reconciler recovers it.
```
Check `audioRetrySchedule` in `internal/queue/queue.go` (referenced in RESEARCH sources, not yet grepped in this pattern pass — planner/plan-checker should verify audio's chosen `cooldownPeriod` clears its own retry schedule's max backoff step, same invariant class as image/document/html).

---

### `cmd/api/main.go` (controller/wiring, request-response)

**Analog:** itself, current state (`cmd/api/main.go:91-92`, exact text verified this session)

**Current (before this phase):**
```go
prometheus.MustRegister(metrics.NewQueueDepthCollector(asynq.NewInspector(redisOpt),
	queue.QueueImage, queue.QueueDocument, queue.QueueHTML, queue.QueueWebhook))
```
**After (insert `queue.QueueAudio` between `QueueHTML` and `QueueWebhook`, matching declaration order in `internal/queue/queue.go:34-38`):**
```go
prometheus.MustRegister(metrics.NewQueueDepthCollector(asynq.NewInspector(redisOpt),
	queue.QueueImage, queue.QueueDocument, queue.QueueHTML, queue.QueueAudio, queue.QueueWebhook))
```
`queue.QueueAudio = convert.EngineAudio = "audio"` already defined since Phase 31 (`internal/queue/queue.go:38`) — no new constant needed, this is purely the collector-registration splice.

---

### `scripts/keda-audio-loadproof.sh` (utility, event-driven/batch)

**Analog:** `scripts/keda-load-proof.sh` (1010 lines — do NOT read in full; targeted sections read this session: header lines 1-60, `teardown()` lines 150-208, `postJob`/`postJobPath`/`waitForReplicasAtLeast`/`waitForReplicasAtMost` lines 330-399. Additional un-read sections — `sampleLoop` (497+), the CSV/PNG evidence-rendering block (634-773), `snapshotLoop` (751+), and the final summary block (999+) — should be read by whoever implements the new script, non-overlapping with what's excerpted here.)

**Header/self-containment convention to replicate** (lines 1-46 of analog):
```bash
#!/usr/bin/env bash
# keda-load-proof.sh -- Phase 28 (autoscale-load-proof) live flagship gate
# (KEDA-03).
# ...
# This gate is SELF-CONTAINED (D-12): it installs KEDA itself, layers
# `-f values-local.yaml -f values-loadproof.yaml` on top of the chart, and
# tears everything down via an EXIT trap -- success or failure, OrbStack is
# never left hot. It refuses to run if the docker-compose stack is up.
# `scripts/keda-gate.sh` (Phase 27) is left byte-unchanged (D-12) -- this is
# a SEPARATE script that reuses its helper shapes, not a modification of it.
set -euo pipefail
cd "$(dirname "$0")/.."
```
**Precedent for how a script gets cloned in this codebase** — `scripts/audio-rtf-measure.sh`'s own header explicitly documents itself as a "Structural clone of scripts/verapdf-measure.sh," listing exactly what changed and what stayed the same. Model `keda-audio-loadproof.sh`'s header the same way: "Structural clone of scripts/keda-load-proof.sh," listing (a) narrower scope — no `CALIBRATE` mode (Pitfall 4, `AUDIO_ENGINE_TIMEOUT` is already RTF-measured, nothing to calibrate), (b) new scope — image-pull-vs-cold-start timestamp separation via `kubectl describe pod` event timeline (Pitfall 5), using `jfk.wav` (`internal/e2e/testdata/jfk.wav`, 11s, 352,078 bytes — already committed) as the trigger fixture instead of a synthesized heavy document.

**Teardown-trap pattern — copy verbatim, this is the process-group-kill fix from Phase 29 WR-01, must not regress** (lines 153-208):
```bash
teardown() {
	local exit_code=$?
	echo ""
	echo "=== TEARDOWN (D-12: OrbStack must never be left hot) ==="

	if [ -n "$SAMPLER_PID" ]; then
		kill "$SAMPLER_PID" >/dev/null 2>&1 || true
		wait "$SAMPLER_PID" 2>/dev/null || true
	fi
	if [ -n "$SNAPSHOT_PID" ]; then
		# WR-04 / 29-REVIEW WR-01: kill the whole process group (own group via
		# parent `set -m` at the snapshotLoop launch site) so a reparented
		# `kubectl -w` pipeline cannot survive this EXIT trap.
		kill -- -"$SNAPSHOT_PID" >/dev/null 2>&1 || true
		wait "$SNAPSHOT_PID" 2>/dev/null || true
		# Belt-and-suspenders (macOS process-group semantics are unreliable):
		# deterministically reap any orphaned watch by its exact command shape.
		[ -n "$BUSY_POD" ] && pkill -f "kubectl get pod ${BUSY_POD} .* -w" >/dev/null 2>&1 || true
	fi
	...
	helm uninstall octoconv -n "$NAMESPACE" >/dev/null 2>&1 || true
	helm uninstall keda -n "$KEDA_NAMESPACE" >/dev/null 2>&1 || true
	...
}
trap teardown EXIT
```
If the new script uses a `kubectl get pod ... -w` watcher for its own event-timeline capture, it MUST use the same `set -m` + process-group-kill + `pkill -f` belt-and-suspenders pattern — this exact defect class was the subject of Phase 29's WR-01/WR-02/WR-03 fixes (see git log `db14b42`, `5440263`).

**Job-submission + bounded-poll helpers — copy verbatim, rename fixture path** (lines 332-399):
```bash
postJob() {
	local filename="$1" target="$2" content_type="$3"
	local out_file="/tmp/keda-loadproof-post-${filename//\//_}.json"
	HTTP_STATUS=$(curl -s -o "$out_file" -w '%{http_code}' -X POST "$API_BASE/v1/jobs" \
		-H "Authorization: ApiKey $CLIENT_KEY" \
		-F "target=$target" \
		-F "file=@internal/e2e/testdata/${filename};type=${content_type}")
	if [ "$HTTP_STATUS" != "202" ]; then
		echo "FAIL: POST /v1/jobs for $filename -> $target returned $HTTP_STATUS, body: $(cat "$out_file")" >&2
		exit 1
	fi
	grep -o '"job_id":"[^"]*"' "$out_file" | head -1 | cut -d'"' -f4
}

waitForReplicasAtLeast() {
	local deployment="$1" floor="$2" timeout_s="$3" observed="0"
	local waited=0
	while [ "$waited" -lt "$timeout_s" ]; do
		observed=$(kubectl get deployment "$deployment" -n "$NAMESPACE" -o jsonpath='{.status.replicas}' 2>/dev/null || echo "0")
		observed="${observed:-0}"
		if [ "$observed" -ge "$floor" ]; then
			echo "$observed"
			return 0
		fi
		sleep 3
		waited=$((waited + 3))
	done
	echo "TIMEOUT(last=$observed)"
	return 1
}
```
For the new script, use `internal/e2e/testdata/jfk.wav` as the `filename` argument (already committed, no new fixture needed per Pitfall 4).

**Byte-unchanged constraint (critical):** `scripts/keda-load-proof.sh` and `scripts/keda-gate.sh` themselves must NOT be modified by this phase — Phase 33 separately re-runs the unmodified `keda-load-proof.sh` live to close Phase 29's deferred human-verification item (orphaned-watcher-process check), so any edit to that script would invalidate the "closes Phase 29's gap" claim. The new `AUDIO_*` proof logic goes exclusively into the new `scripts/keda-audio-loadproof.sh` file.

---

### `.planning/phases/33-keda-helm-chart-integration/evidence/` (artifact directory, file-I/O)

**Analog:** `.planning/milestones/v1.6-phases/28-autoscale-load-proof/evidence/` (directory listing, 4 files):
```
gate-transcript-20260717T100342Z.log   (11030 bytes — full stdout/stderr transcript of the gate run)
sc1-sc2-burst-20260717T100342Z.csv     (847 bytes   — timestamped replica-count sampler output)
sc1-sc2-burst-20260717T100342Z.png     (57669 bytes — rendered chart, via scripts/fixtures/render_evidence.py)
sc3-timestamps-20260717T100342Z.txt    (1367 bytes  — kubectl describe pod event-timeline extract for the downscale proof)
```
**Naming convention:** `<scenario-tag>-<UTC-timestamp:%Y%m%dT%H%M%SZ>.<ext>` — reuse exactly for `scripts/keda-audio-loadproof.sh`'s output, e.g. `sc3-audio-scale-from-zero-<ts>.txt` for the image-pull-vs-cold-start timeline, `sc-audio-burst-<ts>.csv`/`.png` if a sampler chart is produced, `gate-transcript-<ts>.log` for the full run transcript. Target directory: `.planning/phases/33-keda-helm-chart-integration/evidence/` (create at execution time, not by this pattern-mapping pass).

## Shared Patterns

### KEDA co-dependency guard (`and .Values.keda.enabled .Values.prometheus.enabled`)
**Source:** every existing `deployment-*-worker.yaml` (`spec.replicas` omission) and every `scaledobject-*.yaml` (whole-resource gate)
**Apply to:** both new templates — `deployment-audio-worker.yaml`'s `spec.replicas` block and the entirety of `scaledobject-audio.yaml`
```yaml
{{- if and .Values.keda.enabled .Values.prometheus.enabled }}
...
{{- end }}
```

### WR-01 fail-safe triad (ignoreNullValues + fallback + retry-inclusive PromQL)
**Source:** `deploy/chart/octoconv/templates/scaledobject-{image,document,html}.yaml` — identical on all 3
**Apply to:** `scaledobject-audio.yaml`, copy verbatim, do not re-derive:
```yaml
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
```

### `octoconv.commonEnv` single-envFrom-source pattern
**Source:** `deploy/chart/octoconv/templates/_helpers.tpl` (referenced, not re-read this session — already read in RESEARCH's primary sources list), consumed identically by every Deployment template
**Apply to:** `deployment-audio-worker.yaml` — one `{{- include "octoconv.commonEnv" . | nindent 10 }}` line pulls the entire ConfigMap+Secret via `envFrom`; the 5 new `AUDIO_*` keys need no per-Deployment env wiring beyond that single include, since `configmap.yaml` is the sole choke point (DEBT-05).

### `terminationGracePeriodSeconds = ENGINE_TIMEOUT + 30s` formula
**Source:** `cmd/worker/main.go:89`, `cmd/document-worker/main.go:95`, `cmd/chromium-worker/main.go:86`, cross-referenced against `values.yaml`'s `terminationGracePeriodSeconds` per class (150/330/90 respectively — all `ENGINE_TIMEOUT + 30`)
**Apply to:** `values.yaml`'s new `audioWorker.terminationGracePeriodSeconds: 772` (`742 + 30`)

## No Analog Found

None — every file in RESEARCH.md's "Recommended File Set" has a direct structural analog already in the repo (this phase is explicitly framed as "4th repetition of an established pattern," RESEARCH.md:4).

## Metadata

**Analog search scope:** `deploy/chart/octoconv/templates/` (all Deployment + ScaledObject templates), `deploy/chart/octoconv/{values.yaml,values-local.yaml,values-loadproof.yaml}`, `cmd/api/main.go`, `internal/queue/queue.go`, `scripts/{keda-load-proof.sh,keda-gate.sh,audio-rtf-measure.sh}`, `.planning/milestones/v1.6-phases/28-autoscale-load-proof/evidence/`, `docker-compose.yml` (audio-worker service block), `.env.example` (AUDIO_* block), `Dockerfile.audio-worker`
**Files scanned:** 14 (7 analogs read in full, 7 additional files grepped/spot-checked for cross-verification)
**Pattern extraction date:** 2026-07-18
