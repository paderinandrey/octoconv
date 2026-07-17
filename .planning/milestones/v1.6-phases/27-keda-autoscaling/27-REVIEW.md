---
phase: 27-keda-autoscaling
reviewed: 2026-07-16T22:21:56Z
depth: standard
files_reviewed: 17
files_reviewed_list:
  - cmd/api/main.go
  - cmd/worker/main.go
  - cmd/document-worker/main.go
  - cmd/chromium-worker/main.go
  - cmd/webhook-worker/main.go
  - internal/metrics/metrics_test.go
  - internal/e2e/e2e_test.go
  - docker-compose.e2e.yml
  - deploy/chart/octoconv/templates/prometheus.yaml
  - deploy/chart/octoconv/templates/scaledobject-image.yaml
  - deploy/chart/octoconv/templates/scaledobject-document.yaml
  - deploy/chart/octoconv/templates/scaledobject-html.yaml
  - deploy/chart/octoconv/templates/service-api.yaml
  - deploy/chart/octoconv/templates/networkpolicy-metrics.yaml
  - deploy/chart/octoconv/values.yaml
  - deploy/chart/octoconv/values-local.yaml
  - scripts/keda-gate.sh
findings:
  critical: 0
  warning: 6
  info: 9
  total: 15
status: issues_found
---

# Phase 27: Code Review Report

**Reviewed:** 2026-07-16T22:21:56Z
**Depth:** standard
**Files Reviewed:** 17
**Status:** issues_found

## Summary

Reviewed the Phase 27 (KEDA autoscaling) surface: queue-depth collector relocation to the api process, per-class asynq `ShutdownTimeout`, in-chart Prometheus + ScaledObjects + NetworkPolicy fix, the compose E2E metrics-relocation test, and the live gate script. Cross-referenced against `internal/metrics/queue_collector.go`, `internal/queue/queue.go`, the chart Deployments/`_helpers.tpl`/`configmap.yaml`, and the base `docker-compose.yml`.

Verified clean: `go vet` passes on all reviewed packages; `helm template -f values-local.yaml` renders all 3 ScaledObjects and the Prometheus trio; compose base + e2e override merges cleanly; collector label names (`queue`, `state`) and values (`pending|active`) match the ScaledObject PromQL exactly; queue constants (`image`/`document`/`html`/`webhook`) match both the collector registration and the E2E assertions; the gate script's `grep -o '"job_id":"..."'` parsing matches the API's compact `json.NewEncoder` output; per-class ShutdownTimeout + 15s metrics-shutdown sums fit inside each class's `terminationGracePeriodSeconds` (145/325/85/45 vs 150/330/90/60); the webhook-worker advisory-lock defer ordering is correct; the `redisPinger`'s Addr-only copy is consistent with `queue.RedisOpt()` being Addr-only.

No Critical findings. Six Warnings concern robustness of the autoscaling control loop and chart coupling — the most important are the `ignoreNullValues: "true"` blind spot during a prolonged api outage (WR-01) and Helm re-applying `spec.replicas: 1` to KEDA-owned Deployments on every upgrade (WR-02).

## Warnings

### WR-01: `ignoreNullValues: "true"` makes a prolonged api outage scale busy worker classes to 0 without ever tripping fallback

**File:** `deploy/chart/octoconv/templates/scaledobject-image.yaml:41` (identical in `scaledobject-document.yaml:39`, `scaledobject-html.yaml:39`)
**Issue:** The api process is now the *sole* exporter of `octoconv_queue_depth` (cmd/api/main.go:91). When the api pod is down or unscrapeable, Prometheus writes staleness markers within one scrape interval and the PromQL `sum(...)` returns an *empty* result — which `ignoreNullValues: "true"` converts to a valid 0, not a scaler error. KEDA's `fallback` (failureThreshold: 3, replicas: 1) only triggers on scaler *errors*, so it never fires for this case. Result: an api outage longer than the class's cooldownPeriod scales all three worker classes to 0 while a genuine Redis backlog exists; jobs stall until the api recovers (nothing is lost — Postgres/Redis retain state — but draining stops entirely). The in-template comment only considers the "genuinely empty queue" and "routine api restart" cases, not sustained absence.
**Fix:** Once a queue is registered (first enqueue), the api reports a series with value 0 for an empty queue — so an empty PromQL result almost always means "metric source gone", not "queue empty". Flip to `ignoreNullValues: "false"` so sustained absence becomes a scaler error and fallback holds 1 replica per class (the brief fallback blip on a fresh install / short api restart is benign), or keep `"true"` and add an `absent(octoconv_queue_depth)` alert plus a comment acknowledging the api-outage → wrong-scale-to-zero trade-off.

### WR-02: Helm-rendered `spec.replicas` fights KEDA on every `helm upgrade`

**File:** `deploy/chart/octoconv/templates/deployment-worker.yaml:23` (same pattern in `deployment-document-worker.yaml`, `deployment-chromium-worker.yaml`; interacts with all three `scaledobject-*.yaml`)
**Issue:** The three KEDA-scaled Deployments unconditionally render `replicas: {{ .Values.*.replicas }}` (1). Once KEDA owns scaling, any `helm upgrade` re-applies `spec.replicas: 1` to a Deployment KEDA has scaled to 0, spuriously cold-starting a worker pod per class until KEDA scales it back down after cooldownPeriod — replica flapping on every deploy, and it can mask genuine scale-from-zero behavior. `scripts/keda-gate.sh:228-240` already has to code around exactly this ("chart-owned spec.replicas defaults to 1 ... poll rather than assert"), which is the symptom of the defect, not a fix.
**Fix:** Omit `spec.replicas` when the ScaledObject renders, per KEDA's documented Helm guidance:
```yaml
spec:
  {{- if not (and .Values.keda.enabled .Values.prometheus.enabled) }}
  replicas: {{ .Values.worker.replicas }}
  {{- end }}
```
(Kubernetes defaults an omitted `replicas` to 1 on first create, so fresh installs still start at 1 before KEDA takes over.)

### WR-03: ScaledObjects hardcode `metadata.namespace` while every other chart resource uses the release namespace

**File:** `deploy/chart/octoconv/templates/scaledobject-image.yaml:18` (also `scaledobject-document.yaml:16`, `scaledobject-html.yaml:16`, and the FQDNs at `prometheus.yaml:74`, `scaledobject-*.yaml` serverAddress)
**Issue:** The three ScaledObjects are the only namespaced chart resources that set `namespace: {{ .Values.global.namespace }}`; the Deployments they target, the Prometheus Deployment/Service, and everything else render into the Helm release namespace (`-n` flag). If the chart is ever installed with `-n` ≠ `global.namespace`, the ScaledObjects land in a different namespace than their `scaleTargetRef` Deployments (KEDA resolves scaleTargetRef in the ScaledObject's own namespace — targets won't exist), and the `serverAddress`/scrape-target FQDNs built from `global.namespace` point at a Service that was created elsewhere. Today it only works because `keda-gate.sh:193` happens to pass `-n octoconv` matching the value.
**Fix:** Drop `metadata.namespace` from the ScaledObjects and build FQDNs from `{{ .Release.Namespace }}` instead of `{{ .Values.global.namespace }}` (or at minimum use `.Release.Namespace` consistently for both), so the chart is namespace-coherent regardless of the `-n` flag.

### WR-04: ShutdownTimeout ↔ terminationGracePeriodSeconds is a 5-second-margin invariant maintained by hand in two disconnected files

**File:** `cmd/worker/main.go:88` (also `cmd/document-worker/main.go:94`, `cmd/chromium-worker/main.go:85`; vs `deploy/chart/octoconv/values.yaml:43,53,63` and `configmap.yaml:29-33`)
**Issue:** Worst-case worker shutdown is `ShutdownTimeout` (ENGINE_TIMEOUT + 10s, read from the ConfigMap) + up to 15s of metrics-server shutdown = 145s/325s/85s against pod grace 150s/330s/90s — a 5s margin. The two sides of the invariant live in different artifacts with no cross-reference from the ConfigMap side: an operator raising `ENGINE_TIMEOUT`/`DOCUMENT_ENGINE_TIMEOUT`/`HTML_ENGINE_TIMEOUT` in `configmap.yaml` (or via env in compose) without also raising `terminationGracePeriodSeconds` in `values.yaml` silently pushes ShutdownTimeout past pod grace, producing the exact SIGKILL-mid-conversion + requeue this change ("Pattern 2") was built to prevent. The Go-side comments name the grace values, but nothing on the chart side warns in the direction operators actually edit.
**Fix:** Either derive `terminationGracePeriodSeconds` in the chart from the same timeout value (e.g. a single `values.yaml` timeout per class feeding both the ConfigMap entry and grace = timeout + 30s), or add a loud comment on each `*_ENGINE_TIMEOUT` line in `configmap.yaml`: "must stay ≤ <class>.terminationGracePeriodSeconds − 25s (asynq ShutdownTimeout = this + 10s, plus 15s metrics shutdown)".

### WR-05: Prometheus pod never picks up ConfigMap changes — no checksum annotation, no hot-reload

**File:** `deploy/chart/octoconv/templates/prometheus.yaml:29-56`
**Issue:** The Deployment's pod template carries no `checksum/config` annotation, and the container runs stock `prom/prometheus` without `--web.enable-lifecycle`. A `helm upgrade` that changes `prometheus-config` (e.g. adding a scrape job in Phase 28, or a namespace change) updates the ConfigMap but never restarts the pod, and Prometheus does not re-read its config file — the running scraper silently keeps the stale config until someone manually deletes the pod. Because this Prometheus feeds KEDA, a stale scrape config degrades into WR-01's empty-result behavior.
**Fix:** Add the standard checksum annotation to the pod template:
```yaml
template:
  metadata:
    annotations:
      checksum/config: {{ include (print $.Template.BasePath "/prometheus.yaml") . | sha256sum }}
```
(or hash just the config block) so config changes roll the Deployment.

### WR-06: Scale-to-zero + retry-state tasks depend on an undocumented `cooldownPeriod > max retry backoff` invariant

**File:** `deploy/chart/octoconv/templates/scaledobject-image.yaml:36` (all three ScaledObjects; interacts with `internal/queue/queue.go` retry schedules)
**Issue:** The trigger query counts only `pending|active` (D-04, deliberate). asynq's forwarder — the component that moves a due `retry`/`scheduled` task back to `pending` — runs only inside an asynq *server* configured for that queue. If a class's worker count hits 0 while a task sits in `retry`, nothing forwards it, the metric stays 0, and KEDA never scales up; recovery waits for the Postgres reconciler sweep, which itself must wait out the still-held unique lock (~12.6 min worst case for image defaults). Today this is masked only because each class's `cooldownPeriod` (60/120/90s) happens to exceed its max retry backoff (15/30/30s), so a live worker normally survives the retry gap — but that relationship is stated nowhere, and the values are explicitly labeled "demo starting values; production tuning is Phase 28". Someone tuning `cooldownPeriod` below ~30s in Phase 28 makes routine retries strand for minutes.
**Fix:** Document the invariant next to the `keda.*.cooldownPeriod` values in `values.yaml` ("must exceed the class's max retry backoff — see internal/queue/queue.go schedules — or retry-state tasks strand at 0 replicas until the reconciler recovers them"), or eliminate the blind spot by scaling on `state=~"pending|active|retry"` (retry tasks are still imminent work) and updating the D-04 comment.

## Info

### IN-01: Four-queue Describe test cannot detect the regression its name claims to cover

**File:** `internal/metrics/metrics_test.go:74-89`
**Issue:** `TestNewQueueDepthCollectorDescribeAllFourQueues` is byte-for-byte the prior test with two more constructor args, but `Describe` (queue_collector.go:28-30) never reads `c.queues` — the test passes identically for 0, 2, or 4 queues and proves only "constructor doesn't panic". Its doc comment presents it as "the Redis-free half of D-03", overstating coverage; the real proof lives entirely in the E2E test.
**Fix:** Either fold it into the existing Describe test or assert something queue-sensitive offline (e.g. exercise `Collect` against a stub and assert per-queue series), and soften the comment.

### IN-02: `ENGINE_TIMEOUT`-family env vars read twice per worker main

**File:** `cmd/worker/main.go:64,88` (also `cmd/document-worker/main.go:59,94`, `cmd/chromium-worker/main.go:59,85`)
**Issue:** The engine timeout is read via `envDuration` once for `NewHandler` and again for `ShutdownTimeout`. Same key so no behavioral drift today, but the `+10*time.Second` margin relationship is easier to keep correct with a single read.
**Fix:** `engineTimeout := envDuration("ENGINE_TIMEOUT", 120*time.Second)` once; pass it to both call sites.

### IN-03: `seedQueueRegistry` couples to asynq's private Redis key, and the suite doc omits its REDIS_ADDR requirement

**File:** `internal/e2e/e2e_test.go:1251-1271` (doc header lines 7-22)
**Issue:** (a) SADD to the literal `"asynq:queues"` reaches into asynq v0.26 internals (`base.AllQueues`); an asynq upgrade renaming the key breaks the test (loudly, via missing series, but confusingly). (b) The package doc's required/optional env list does not mention that `TestQueueDepthMetricRelocationE2E` reads `REDIS_ADDR` (default `localhost:6379`); a developer who sources the container-oriented `.env` (`REDIS_ADDR=redis:6379`) gets a wrong-host dial failure with no hint in the documented env contract.
**Fix:** Add `REDIS_ADDR` to the optional-env doc block with the "host-published 6379" note, and reference `base.AllQueues` in the seed comment so an asynq upgrade audit finds it.

### IN-04: api shutdown shares one 15s context sequentially across two servers

**File:** `cmd/api/main.go:173-180`
**Issue:** `httpSrv.Shutdown` and `metricsSrv.Shutdown` share the same 15s `shutdownCtx`; if the public server consumes the full budget draining requests, the metrics server gets ~0s and force-closes. Harmless for short-lived scrape connections, but the intent ("graceful") isn't what the code guarantees.
**Fix:** Shut them down concurrently (two goroutines + WaitGroup) or give each its own timeout.

### IN-05: keda-gate.sh operational nits

**File:** `scripts/keda-gate.sh:126-145,301-302,86-118`
**Issue:** (a) No preflight `command -v` for helm/kubectl/curl/go/docker — missing tools surface as raw `set -e` aborts mid-gate. (b) The KEDA version check `helm search repo kedacore/keda --versions | awk '{print $2}'` also matches sibling charts (`kedacore/keda-add-ons-http`), so a version string present only on an add-on chart would false-pass (install would then fail at STEP 2, so still bounded). (c) `CLIENT_KEY` parsing (`awk -F': ' '/^api key/'`) is coupled to `cmd/manage-clients`' human-readable output line — a wording change breaks the gate (loudly, via `assert_nonempty`). (d) Teardown removes releases but leaves the `octoconv`/`keda` namespaces and PVCs (postgres/minio data persists across runs), so "never left hot" holds for compute only and stale DB state accumulates gate clients.
**Fix:** Add a preflight tool loop; pin the version grep to the `kedacore/keda\s` row; have manage-clients grow a machine-readable output (or parse the last whitespace field); optionally `kubectl delete ns octoconv --wait=false` in teardown.

### IN-06: networkpolicy-metrics.yaml missing chart labels; prometheus rule renders unconditionally

**File:** `deploy/chart/octoconv/templates/networkpolicy-metrics.yaml:40-58`
**Issue:** (a) Every other chart object (including `networkpolicy-mcp-http.yaml:24-25`) carries `octoconv.labels`; this policy's metadata has none — inconsistent with the chart's own convention and invisible to label-based tooling. (b) The :9090-from-prometheus ingress rule renders even when `prometheus.enabled=false`, admitting from a pod that doesn't exist. Harmless (9090 is simply unreachable, same as before), but the asymmetry with the `{{- if .Values.prometheus.enabled }}` gating elsewhere is worth a comment or a guard.
**Fix:** Add the standard labels block; optionally wrap rule (a) in the same `prometheus.enabled` guard.

### IN-07: In-chart Prometheus scrapes only the api — worker job/duration metrics are exposed but unobserved

**File:** `deploy/chart/octoconv/templates/prometheus.yaml:69-74`
**Issue:** `scrape_configs` contains only `octoconv-api`. The workers still serve `octoconv_job_outcomes_total`/`octoconv_job_duration_seconds` on :9090 (and the NetworkPolicy was specifically fixed to admit Prometheus to those ports), but nothing scrapes them — the D-11 "minimal, KEDA-only" scope makes this deliberate today, yet the NetworkPolicy work implies broader scraping was the intent.
**Fix:** Note it for Phase 28: add a kubernetes_sd or static scrape job for the worker Deployments (they're the pods the :9090 ingress rule was widened for).

### IN-08: ScaledObject fallback knobs hardcoded while sibling knobs are values-driven

**File:** `deploy/chart/octoconv/templates/scaledobject-image.yaml:29-31` (all three)
**Issue:** `fallback.failureThreshold: 3` / `replicas: 1` are literals, while threshold/pollingInterval/cooldownPeriod/maxReplicaCount come from `values.yaml`. Phase 28 tuning will want these too; magic numbers in three copies invite drift.
**Fix:** Move to `keda.<class>.fallback.{failureThreshold,replicas}` in values.yaml with the current values as defaults.

### IN-09: Comment typo and missing Prometheus readiness probe

**File:** `cmd/webhook-worker/main.go:73`; `deploy/chart/octoconv/templates/prometheus.yaml:35-52`
**Issue:** (a) Comment reads `h.engineTimout` (missing "e"). (b) The prometheus container has no readiness/liveness probe, so `kubectl wait --for=condition=Available deployment/prometheus` (keda-gate.sh STEP 4) passes on Running alone — a crash-looping or not-yet-serving Prometheus shows up later as an SC1 metric-resolution timeout instead of at the wait.
**Fix:** Fix the typo; add a `readinessProbe: httpGet: /-/ready port 9090` (and `/-/healthy` for liveness) to the prometheus container.

---

_Reviewed: 2026-07-16T22:21:56Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
