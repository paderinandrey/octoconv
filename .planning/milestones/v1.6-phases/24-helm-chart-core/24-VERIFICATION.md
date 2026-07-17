---
phase: 24-helm-chart-core
verified: 2026-07-14T02:10:20Z
status: passed
score: 4/5 roadmap success criteria fully verified; 1 requires human decision (SC4 environmental residual)
overrides_applied: 0
human_verification:
  - test: "Decide whether SC4's environmental NetworkPolicy-enforcement gap is an acceptable residual for this milestone, or whether real enforcement must be proven (e.g. on a policy-capable CNI / real cluster) before Phase 24 is considered fully closed."
    expected: "A recorded decision: (a) accept as documented residual with a mandatory Phase 27 re-test note (chart is correct, OrbStack's CNI has no NetworkPolicy controller), or (b) require a policy-capable cluster/CNI validation before sign-off."
    why_human: "This is a security-control verification gap, not a code defect — the NetworkPolicy object is correct (verified in templates/networkpolicy-metrics.yaml: podSelector scoped to octoconv.io/tier: app, ports 8090/9090 correctly ingress-restricted) but its enforcement could not be observed on the only cluster available (OrbStack ships no NetworkPolicy controller in kube-system). No code change can fix this — it requires either accepting the residual or a different validation environment. Precedent exists in this project for accepting similar environmental residuals (v1.4 CACHED, v1.5 amd64 pin) but that judgment belongs to the developer, not the verifier."
  - test: "Confirm (post-OrbStack-restart, when convenient) that a presigned MinIO URL resolves via a DIRECT host dial (not through kubectl port-forward), to fully re-validate the FQDN landmine's host-reachability claim without the workaround."
    expected: "curl of http://minio.octoconv.svc.cluster.local:9000/... from the bare Mac host returns 200 without any port-forward/--connect-to indirection."
    why_human: "SC3 passed via a degraded path (kubectl port-forward + curl --connect-to) because OrbStack's host-to-cluster proxy layer was wedged during the live gate (documented in 24-03-SUMMARY.md, Deviation #4). The mechanism (FQDN + signed URL) is proven correct; the direct-dial claim from research was not re-confirmed in this run. Low risk (the same OrbStack proxy also served all other in-cluster traffic fine) but worth a cheap follow-up dial once the daemon is healthy."
---

# Phase 24: Helm Chart Core & Landmine Closure Verification Report

**Phase Goal:** Full OctoConv stack deploys to OrbStack k8s from a single Helm chart and passes E2E inside the cluster; four SEED-004 landmines closed.
**Verified:** 2026-07-14T02:10:20Z
**Status:** human_needed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths (ROADMAP Success Criteria, verified independently of SUMMARY claims)

| # | Truth (ROADMAP SC) | Status | Evidence |
|---|------|--------|----------|
| 1 | `helm install` on OrbStack k8s reaches all-pods-Ready and the in-cluster E2E Job completes exit 0 | ✓ VERIFIED | Re-ran `helm template` + `kubectl --context orbstack -n octoconv apply --dry-run=server -f -` myself against the LIVE cluster (still reachable, node Ready v1.34.8+orb1) — all 18 objects accepted with zero errors. Live install/E2E transcript in 24-03-SUMMARY.md is detailed, timestamped, and internally consistent (pod-by-pod Ready timestamps, Job log tail showing 9/9 `--- PASS` lines, 5m23s duration) — treated as credible executor-run evidence, not a bare claim. `mcp-http` from the ROADMAP's literal SC1 wording is explicitly and correctly deferred to Phase 25 per 24-CONTEXT.md ("NO mcp-http... K8S-01's mcp-http mention is satisfied incrementally") — this is a pre-authorized scope note, not a gap. |
| 2 | migrate + createbucket run exactly once, before anything depends on them, verified on install AND upgrade | ✓ VERIFIED (via documented D-05 refinement) | No migrate hook Job exists anywhere in the chart (confirmed: `grep -c 'command: \["/usr/local/bin/migrate"\]'` context absent, no job-migrate.yaml file). Verified in source: `cmd/api/main.go` calls `db.Migrate` unconditionally at startup (single replica ⇒ race-free); `grep -rn "db.Migrate" cmd/` confirms only `cmd/api`/`cmd/migrate` call it, and `cmd/migrate` is never invoked by the chart. `templates/job-createbucket.yaml` carries `helm.sh/hook: post-install,post-upgrade` + `hook-delete-policy: before-hook-creation,hook-succeeded` (read directly). Live gate confirms `helm upgrade` rev 2 re-ran createbucket with no name collision (24-03-SUMMARY transcript, 02:01:17). This is the explicitly authorized D-05 refinement (documented in CONTEXT.md and both plan/summary), not a deviation from the phase goal I was given. |
| 3 | Presigned result URL resolves from inside a pod AND from the OrbStack host, via FQDN S3_ENDPOINT | ✓ VERIFIED (degraded transport, mechanism proven) | `helm template` render confirms `S3_ENDPOINT: "minio.octoconv.svc.cluster.local:9000"` in the ConfigMap (verified myself). Live gate: presigned URL fetched HTTP 200, valid 804-byte JPEG, V4 signature verified against the FQDN Host header — but the direct host→cluster dial was blocked by an OrbStack proxy wedge, so the fetch used `kubectl port-forward` + `curl --connect-to` (preserves the same signed Host header; proves the FQDN/signature mechanism, not the raw network path). Flagged as human-verification item #2 above — low risk, not blocking. |
| 4 | `/metrics` binds 0.0.0.0 yet is reachable only from scoped source via NetworkPolicy — unauthorized ingress denied | ✗ FAILED on this cluster / UNCERTAIN for sign-off | `METRICS_ADDR: "0.0.0.0:9090"` confirmed in rendered ConfigMap. `templates/networkpolicy-metrics.yaml` verified correct by direct read: `podSelector.matchLabels: octoconv.io/tier: app` (exactly, not part-of — confirmed by counting 6 total occurrences of the label in the default render: 1 in the NetworkPolicy selector + exactly 5 on the app Deployment pod templates, 0 on postgres/redis/minio). Ingress rules correctly scope :9090 to a `monitoring` namespaceSelector and :8090 to the `octoconv` namespace. **However**, the live negative test (unrelated pod scraping `:9090/metrics`) SUCCEEDED — i.e. ingress was NOT denied — because OrbStack's k8s (`kube-system` = coredns + local-path-provisioner only) ships no NetworkPolicy controller; the policy object is accepted by the API server but is a silent no-op. This is a genuine failure of the literal observable truth on the validation cluster, caused by a platform limitation outside the chart's control. Escalated as human-verification item #1 — see rationale there. |
| 5 | Per-class terminationGracePeriodSeconds + dependency-aware probes | ✓ VERIFIED | Read every Deployment template directly: `deployment-document-worker.yaml` grace 330, `deployment-chromium-worker.yaml` grace 90, `deployment-worker.yaml` grace 150, `deployment-webhook-worker.yaml` grace 60, `deployment-api.yaml` grace 30 — all via `{{ .Values.<svc>.terminationGracePeriodSeconds }}` or a literal matching `values.yaml`'s declared values (150/330/90/60/30), each ≥ its class's engine timeout (document ENGINE_TIMEOUT 300s<330, html 60s<90 — wait, image 120s<150, webhook n/a<60). api probes `/healthz:8090` (readiness tight 5s/2, liveness tolerant 10s/6); all four workers probe `/metrics:9090` (readiness+liveness); document-worker and chromium-worker additionally carry a generous `startupProbe` (24×5s ≈120s budget) for cold-start tolerance. chromium-worker mounts `emptyDir{medium: Memory, sizeLimit: 256Mi}` at `/dev/shm` (confirmed by direct read). webhook-worker `replicas: 2` fixed (confirmed in values.yaml and template, plus an explicit anti-KEDA-scale-down comment). |

**Score:** 4/5 fully verified; SC4 requires an explicit human accept/reject decision (see frontmatter `human_verification`).

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `deploy/chart/octoconv/Chart.yaml` | Helm v2 chart metadata | ✓ VERIFIED | `apiVersion: v2`, name `octoconv`, no chart deps |
| `deploy/chart/octoconv/values.yaml` | Complete per-service contract | ✓ VERIFIED | All blocks present (global/image/metrics/s3/api/worker/documentWorker/chromiumWorker/webhookWorker/asynqmon/postgres/redis/minio/e2e); plans 02/03 never edited it (confirmed no diff needed) |
| `templates/configmap.yaml` | Non-secret env incl. landmine values | ✓ VERIFIED | S3_ENDPOINT FQDN + METRICS_ADDR 0.0.0.0:9090 confirmed by live `helm template` render |
| `templates/secret.yaml` | Dev-cred Secret from `.Values.secrets.*` | ✓ VERIFIED | All 7 keys sourced from values, no literal secret in the template; note: `values-local.yaml`'s `secrets.databaseUrl` key is unused (secret.yaml builds DATABASE_URL from postgres.user/db + secrets.postgresPassword instead) — dead value, cosmetic only, not a functional gap |
| `templates/postgres.yaml`, `redis.yaml`, `minio.yaml` | Stateful backbone | ✓ VERIFIED | Deployment+Service(+PVC), literal Service names (postgres/redis/minio), postgres mount at `/var/lib/postgresql` (compose parity), MinIO on concrete RELEASE tags (0 `:latest` in full render, confirmed by grep), zero `octoconv.io/tier: app` on any of the three (confirmed) |
| `templates/deployment-api.yaml` + `service-api.yaml` | api Deployment/Service | ✓ VERIFIED | tier=app label, /healthz probes, grace 30, literal Service name `api` |
| `templates/deployment-{worker,document-worker,chromium-worker,webhook-worker}.yaml` | 4 engine-class workers | ✓ VERIFIED | All present, all render, all carry tier=app + correct grace/probes/limits (see truth #5) |
| `templates/networkpolicy-metrics.yaml` | Metrics NetworkPolicy | ✓ VERIFIED (object) / see SC4 for enforcement | Correct selector/ports/sources as designed; enforcement unverifiable on OrbStack |
| `templates/job-createbucket.yaml` | Idempotent hook Job | ✓ VERIFIED | post-install,post-upgrade hook-weight 0, before-hook-creation+hook-succeeded delete policy, wait-for-minio initContainer, `mc mb --ignore-existing` |
| `Dockerfile.e2e` | In-cluster E2E image | ✓ VERIFIED | `go test -c -o /out/e2e.test ./internal/e2e`, testdata shipped, `-test.skip=TestPDFANonCompliantE2E` documented and justified (compose-only shim dependency) |
| `templates/job-e2e.yaml` | Downward-API E2E Job | ✓ VERIFIED | `E2E_WEBHOOK_HOST` via `fieldRef: status.podIP` (confirmed), `E2E_S3_DIAL_ADDR` correctly omitted, gated on `.Values.e2e.enabled` (confirmed: 0 hits for `status.podIP`/`WEBHOOK_ALLOW_PRIVATE_IPS` in the default render, exactly 1 hit each with `values-e2e.yaml` layered) |
| `values-e2e.yaml` | api-only SSRF/rate relaxations overlay | ✓ VERIFIED | `e2e.enabled: true`, `api.extraEnv` with WEBHOOK_ALLOW_PRIVATE_IPS/INSECURE_HTTP/RATE_LIMIT_* — confined to `api.extraEnv`, not applied to webhook-worker (confirmed by reading `deployment-webhook-worker.yaml`, which has no extraEnv block) |

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| `templates/configmap.yaml` | minio Service FQDN | `S3_ENDPOINT` value | ✓ WIRED | `minio.octoconv.svc.cluster.local:9000` present in render |
| `templates/postgres.yaml` Service | `DATABASE_URL` host | Service name `postgres` | ✓ WIRED | `secret.yaml` builds `postgres.octoconv.svc.cluster.local` matching the literal Service name |
| `deployment-*.yaml` | `octoconv-config`/`octoconv-secret` | `envFrom` via `octoconv.commonEnv` | ✓ WIRED | Confirmed in every app Deployment template |
| worker probes | metrics listener :9090 | httpGet /metrics | ✓ WIRED | Valid because METRICS_ADDR=0.0.0.0:9090 ships in the same ConfigMap |
| `networkpolicy-metrics` podSelector | 5 app pod templates | `octoconv.io/tier: app` | ✓ WIRED (object-level) | Exactly 5 app pods + 1 NetworkPolicy selector carry the label (6 total occurrences, counted directly in the rendered manifest) |
| `job-createbucket` initContainer | minio Service | `mc ready local` poll against `http://minio:9000` | ✓ WIRED | Confirmed in template |
| `job-e2e.yaml` `E2E_WEBHOOK_HOST` | Job pod's own IP | Downward API `status.podIP` | ✓ WIRED | Confirmed in template |
| `job-e2e.yaml` `E2E_BASE_URL` | api Service | `http://api.octoconv.svc.cluster.local:8090` | ✓ WIRED | Confirmed in template |
| `values-e2e.yaml` api env | `validateCallbackURL` SSRF guard | `WEBHOOK_ALLOW_PRIVATE_IPS`/`INSECURE_HTTP` | ✓ WIRED | Confirmed api-only, via `api.extraEnv` range block in `deployment-api.yaml` |

### Offline + Live Gates Re-Run By Verifier (not just trusting SUMMARY)

| Check | Command | Result |
|-------|---------|--------|
| helm lint (default) | `helm lint deploy/chart/octoconv --values values-local.yaml` | 0 chart(s) failed |
| helm lint (+e2e overlay) | `helm lint ... --values values-local.yaml --values values-e2e.yaml` | 0 chart(s) failed |
| helm template (default) | renders 8 Deployments, 4 Services, 2 PVCs, 1 ConfigMap, 1 Secret, 1 NetworkPolicy, 1 Job (createbucket) | clean, no errors |
| `:latest` tag grep | `grep -c ':latest'` on full render | 0 |
| `octoconv.io/tier: app` count | grep on full render | 6 (1 NetworkPolicy selector + 5 app pod templates; 0 on postgres/redis/minio) |
| Live server dry-run | `helm template ... \| kubectl --context orbstack -n octoconv apply --dry-run=server -f -` | 18/18 objects accepted, zero errors (cluster still live/reachable at verification time) |
| e2e overlay render | `helm template ... --values values-local.yaml --values values-e2e.yaml` | `status.podIP` ×1, `WEBHOOK_ALLOW_PRIVATE_IPS` ×1, `job-e2e` renders correctly |
| Go source diff | `git diff --stat` across all phase-24 commits, `*.go` filter | empty — zero Go changes confirmed |
| `gofmt -l .` | repo-wide | clean |
| `go vet ./...` | repo-wide | clean |
| `go build ./...` | repo-wide | clean |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|--------------|--------|----------|
| K8S-01 | 24-01, 24-02, 24-03 | Full stack via `helm install` + in-cluster E2E Job | ✓ SATISFIED | See truth #1; mcp-http deferral to Phase 25 pre-authorized in CONTEXT.md |
| K8S-02 | 24-01, 24-02, 24-03 | Four SEED-004 landmines closed | ⚠ PARTIALLY SATISFIED | METRICS bind+NP: object correct, enforcement unverifiable (SC4); Downward-API E2E: verified; ordering (self-migration+createbucket): verified; FQDN S3: verified (degraded transport) |
| K8S-03 | 24-02 | Probes + per-class grace periods | ✓ SATISFIED | See truth #5 |

Note: `.planning/REQUIREMENTS.md` still shows K8S-01/02/03 as "Pending" with unchecked boxes — this reflects that the tracking-doc update happens after verification sign-off in this project's workflow, not a gap in the phase's actual deliverable.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `values-local.yaml` / `templates/secret.yaml` | — | `secrets.databaseUrl` value declared but never referenced (secret.yaml constructs DATABASE_URL itself from other values) | ℹ️ Info | Dead config key, zero functional impact — safe to remove in a future cleanup pass, not a phase-blocking issue |
| `templates/postgres.yaml`, `minio.yaml` | — | 24-CONTEXT.md's D-02 literally said "StatefulSets"; implementation uses Deployment+Recreate+PVC instead | ℹ️ Info | Functionally equivalent for single-replica/RWO-PVC/no-stable-identity-needed use case (Service already provides the stable name); PLAN 01 explicitly specified Deployment, refining D-02 at planning time the same way D-05 was refined — not silently deviated |
| No TBD/FIXME/XXX/unreferenced debt markers found anywhere in `deploy/chart/octoconv/` or `Dockerfile.e2e` | — | — | — | Clean |

No blocker-level anti-patterns found. No stub components — every rendered artifact contains real, non-placeholder logic (probes hit real endpoints, hooks run real `mc` commands, the E2E Job runs the real compiled test binary).

### Behavioral Spot-Checks / Probe Execution

No `scripts/*/tests/probe-*.sh` convention files exist for this phase; the phase's own "probe" is THE LIVE HARD GATE executed by the 24-03 executor directly against the live OrbStack cluster (not a standalone probe script this verifier can independently re-invoke without redoing a full stateful install/E2E run, which is out of scope for a verification pass). In lieu of re-running the full live gate, this verifier re-ran the cheap, safe, non-mutating gates against the still-live cluster (helm lint/template, server-dry-run) and found them fully consistent with the SUMMARY's claims — see "Offline + Live Gates Re-Run By Verifier" above. The full mutating gate (actual `helm install`/E2E Job run) was not re-executed to avoid disrupting a shared OrbStack instance outside a sanctioned gate window; the existing transcript in 24-03-SUMMARY.md is treated as sufficiently detailed, timestamped, and internally consistent (specific pod Ready timestamps, specific Job durations, a literal 9/9 test-name PASS log tail) to serve as primary evidence for the mutating portions of SC1–SC3.

### Human Verification Required

See frontmatter `human_verification` — two items:
1. **SC4 disposition decision** (blocking sign-off until resolved) — accept the documented environmental residual (NetworkPolicy object correct, enforcement unverifiable on OrbStack's CNI, re-test mandated for Phase 27) or require enforcement proof on a policy-capable cluster.
2. **SC3 direct-dial re-check** (non-blocking, cheap follow-up) — confirm host→cluster FQDN dial works without the `kubectl port-forward` workaround once OrbStack's proxy layer is healthy.

### Gaps Summary

No code-level gaps. All chart artifacts exist, are substantive (not stubs), and are correctly wired — verified independently by direct template reads, a fresh `helm lint`/`helm template` run, and a fresh live server-dry-run against the still-reachable OrbStack cluster (not merely trusting SUMMARY.md's claims). Zero Go-code changes confirmed via `git diff --stat -- '*.go'` across every phase-24 commit, and `gofmt`/`go vet`/`go build` are all clean.

The one open item is not a defect in this phase's deliverable but a verification gap caused by the local validation platform (OrbStack's CNI has no NetworkPolicy controller) — the 24-03 executor discovered and honestly reported this via a genuine negative test rather than skipping or fudging it. Per the phase's own explicit escalation instruction, this judgment is routed to the developer rather than unilaterally resolved by the verifier.

---

_Verified: 2026-07-14T02:10:20Z_
_Verifier: Claude (gsd-verifier)_

## Operator Resolution (2026-07-14)

The operator ACCEPTED SC4 as an environmental residual (option 1): the NetworkPolicy object is
verified correct (selector octoconv.io/tier: app, ports/sources per design), but OrbStack's k8s
ships no NetworkPolicy controller, so no policy can be enforced on this local-validation cluster
class — a platform limitation, not a chart defect (precedents: v1.4 CACHED residual, v1.5 amd64
pin). MANDATORY RE-TEST recorded: the :9090 unauthorized-scrape negative test must be re-run on
any NP-capable CNI cluster (first real-cluster deploy, K8SV2-02); also re-check SC3's direct
host-dial (proven via port-forward while OrbStack's host-proxy was wedged) when the stack is next
installed (Phase 27). Installing a third-party CNI locally was considered and rejected as
tool-validation scope creep. Status flipped to passed per the operator's checkpoint decision.
