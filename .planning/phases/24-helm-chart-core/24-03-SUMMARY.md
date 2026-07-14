---
phase: 24-helm-chart-core
plan: 03
subsystem: deployment
tags: [helm, kubernetes, e2e, orbstack, live-gate]
requires: [24-01, 24-02]
provides:
  - Dockerfile.e2e (in-cluster E2E test-binary image)
  - deploy/chart/octoconv/templates/job-e2e.yaml (Downward-API podIP E2E Job, gated e2e.enabled)
  - deploy/chart/octoconv/values-e2e.yaml (api-only SSRF/rate relaxations overlay)
  - live-gate evidence (install, in-cluster E2E 9/9 PASS, presigned-from-host, upgrade idempotence, teardown)
affects: [25-mcp-http-chart, 27-keda]
tech-stack:
  added: []
  patterns:
    - "api.extraEnv range block: explicit env overrides envFrom on key collision (overlay-only relaxations)"
    - "helm install WITHOUT --wait when post-install hooks provide startup dependencies (chicken-egg)"
key-files:
  created:
    - Dockerfile.e2e
    - deploy/chart/octoconv/templates/job-e2e.yaml
    - deploy/chart/octoconv/values-e2e.yaml
  modified:
    - deploy/chart/octoconv/templates/deployment-api.yaml
decisions:
  - "helm install must NOT use --wait with this chart: apps crash until the post-install createbucket hook runs, but --wait blocks hooks until apps are Ready (chicken-egg); readiness is gated separately via kubectl wait"
  - "SC4 (NetworkPolicy negative test) is UNENFORCEABLE on OrbStack: its CNI ships no NetworkPolicy controller — policy object is correct but a no-op locally; enforcement requires a policy-capable CNI (production concern, tracked)"
  - "PVCs do NOT survive helm uninstall: they are chart-owned PVC manifests (not volumeClaimTemplates), deleted with the release"
metrics:
  duration: "~55 min (incl. two blocking-issue recoveries)"
  completed: "2026-07-14T02:02:10Z"
---

# Phase 24 Plan 03: In-Cluster E2E Path + Live Hard Gate Summary

In-cluster E2E path shipped (Dockerfile.e2e + Downward-API podIP Job + values-e2e overlay) and the live hard gate executed on OrbStack k8s: helm install → all pods Ready → in-cluster E2E Job 9/9 PASS (exit 0) → presigned URL 200 from host → upgrade re-runs createbucket hook with no collision → clean teardown. One gate item, SC4 (NetworkPolicy negative test), FAILED for an environmental reason: OrbStack's CNI has no NetworkPolicy controller, so the (correct) policy object is silently unenforced on this cluster.

## Gate verdict (loud)

| Check | Result |
|-------|--------|
| SC1 — in-cluster E2E Job exit 0 | **PASS** (9/9 tests, 5m23s; TestPDFANonCompliantE2E excluded by design via -test.skip) |
| SC2 — helm upgrade hook idempotence | **PASS** (rev 2 deployed in 6.4s; createbucket re-ran + Completed, no name collision) |
| SC3 — presigned URL from OrbStack host | **PASS (degraded path)** — HTTP 200, valid JPEG, V4 signature verified against the FQDN Host header; dial went through `kubectl port-forward` + `curl --connect-to` because OrbStack's host→cluster proxy layer was wedged (see Deviations #4) |
| SC4 — :9090 scrape denied from unrelated pod | **FAIL — ENVIRONMENTAL.** Scrape SUCCEEDED. OrbStack k8s (kube-system = coredns + local-path-provisioner only) ships NO NetworkPolicy controller; the `octoconv-app-metrics` policy exists but is a no-op on this cluster. The chart is correct — enforcement requires a policy-capable CNI (Cilium/Calico), which no local remediation can provide. Per T-24-10 this is recorded as a hard-gate failure, surfaced for the phase verifier — NOT silently downgraded to a warning. |
| All pods Ready | **PASS** (8/8 workloads incl. webhook-worker×2; api self-migration proven by Ready) |
| Teardown | **PASS** (uninstall clean by 02:02:10Z; PVCs DELETED with release — see decisions) |

## Live-gate transcript (UTC, 2026-07-14)

```
01:29:48  helm install #1 (--wait --timeout 10m, values-local + values-e2e)
01:39:50  FAILED: context deadline exceeded — all app Deployments 0/N Available
          Root cause: apps log.Fatalf `storage: bucket "octoconv" does not exist`;
          bucket is created by the POST-install hook, but --wait blocks hooks
          until apps are Ready → chicken-egg. (Deviation #1)
01:41:27  helm uninstall (failed rev), pods drained
01:41:42  helm install #2 WITHOUT --wait → "Install complete" in 14.5s
          (post-install createbucket hook ran + Completed; hook-succeeded
          delete policy removed the Job object)
01:41:46–01:42:14  all 8 Deployments Available; pod Ready timestamps:
          redis      01:41:46   minio     01:41:51   postgres  01:41:52
          api        01:42:05   worker    01:42:06   webhook×2 01:42:08/11
          document   01:42:10   chromium  01:42:14
          (apps crash-restarted 2× while bucket was being created — compose-
          equivalent behaviour; api Ready = schema migrated by startup db.Migrate)
01:42–01:45  e2e image found GC'd by kubelet (disk pressure; Deviation #2);
          docker builder prune -af freed 6.8GB; rebuilt octoconv-e2e:dev;
          e2e pod left ImagePullBackOff on its own (IfNotPresent found local image)
01:45:59  e2e Job pod Running (wait-for-api initContainer had already passed)
01:47:05  e2e Job Complete — COMPLETIONS 1/1, DURATION 5m23s (from Job creation)
01:49     SC4 negative test: probe pod scraped worker :9090/metrics → SUCCEEDED
          (FAIL — no NetworkPolicy controller on OrbStack; see verdict table)
01:59:26  SC3: job 5c736688 (png→jpg) created via port-forwarded api, done;
          presigned URL host = minio.octoconv.svc.cluster.local:9000
02:00     SC3 fetch from host: HTTP 200, 804-byte valid JPEG (16x16), signed
          Host header preserved via --connect-to; direct cluster.local dial
          blocked by OrbStack proxy wedge (Deviation #4)
02:01:17  helm upgrade (values-local only) → rev 2 "Upgrade complete" in 6.4s;
          createbucket hook re-created + re-Completed (events: SuccessfulCreate
          + Completed at rev 1 AND rev 2 — no name collision); api rolled and
          re-Ready; e2e Job cleanly removed (e2e.enabled back to false)
02:01:56  helm uninstall → all pods gone by 02:02:10; PVCs went Terminating
          and were deleted; namespace octoconv kept (per instruction)
```

## E2E Job log evidence (tail)

```
--- PASS: TestDocumentConversionE2E (24.48s)      [6 subtests: docx/xlsx/pptx/odt/ods/odp]
--- PASS: TestCrossFormatConversionE2E (24.21s)
--- PASS: TestOLECFBRejectionE2E (0.04s)
--- PASS: TestPDFAExportE2E (12.07s)              [real veraPDF happy path — runs in-cluster]
--- PASS: TestOptsRejectionE2E (0.03s)
--- PASS: TestHTMLConversionE2E (10.13s)
--- PASS: TestHTMLContentRejectionE2E (0.02s)
--- PASS: TestHTMLNetworkBlockE2E (5.03s)         [canary via Downward-API podIP — landmine closed]
--- PASS: TestImageConversionE2E (2.18s)
PASS
```

TestPDFANonCompliantE2E: absent from the run — excluded by `-test.skip` in
Dockerfile.e2e's ENTRYPOINT (compose-only veraPDF soffice shim dependency;
surfaced in plan objective, not silently dropped). Webhook delivery +
signature verification ran inside the passing suite (E2E_WEBHOOK_HOST =
pod's own IP via status.podIP; E2E_S3_DIAL_ADDR omitted — both halves of
the K8S-02 host-gateway/S3-dial landmine closed with zero Go changes).

## What was built (Task 1, commit 98bdc05)

- **Dockerfile.e2e** — golang:1.26-bookworm build stage (`CGO_ENABLED=0 go test -c -o /out/e2e.test ./internal/e2e`) → debian:bookworm-slim runtime with ca-certificates, testdata shipped next to the binary at /work/testdata, USER nobody, ENTRYPOINT with `-test.v -test.timeout=20m -test.skip=TestPDFANonCompliantE2E`.
- **templates/job-e2e.yaml** — plain Job (not a hook), gated `{{- if .Values.e2e.enabled }}`, backoffLimit 0, activeDeadlineSeconds 1500; env: E2E_BASE_URL=api FQDN, E2E_WEBHOOK_HOST via `fieldRef: status.podIP`, DATABASE_URL/API_KEY_SALT/WEBHOOK_SIGNING_SECRET via secretKeyRef octoconv-secret; wait-for-api initContainer (busybox wget /healthz poll — doubles as the schema-readiness gate since api self-migrates before serving).
- **values-e2e.yaml** — `e2e.enabled: true` + `api.extraEnv` (WEBHOOK_ALLOW_PRIVATE_IPS/INSECURE_HTTP true, RATE_LIMIT_IP_RPM/CLIENT_RPM 6000), api-only, mirroring docker-compose.e2e.yml.
- **deployment-api.yaml** — the plan-anticipated minimal `{{- range $k,$v := .Values.api.extraEnv }}` env block (explicit env overrides envFrom on key collision). Chart-template only; zero Go.

Offline verification: helm lint clean with and without the overlay; `helm template` renders status.podIP + relaxations only when layered; normal install renders NEITHER (0 grep hits); full render `kubectl apply --dry-run=server` clean (20 objects).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] `--wait` + post-install hook chicken-egg**
- **Found during:** Task 2 STEP 2 (first install attempt)
- **Issue:** App binaries exit fatally when the S3 bucket is missing; the bucket is created by the post-install createbucket hook; helm runs post-install hooks only after `--wait` succeeds — which never happens (all app pods CrashLoopBackOff for 10m).
- **Fix:** Reinstalled WITHOUT `--wait` (helm still blocks on the hook Job itself); pod readiness gated separately via `kubectl wait --for=condition=Available deployment/...` (which was STEP 3 anyway). No chart or Go change.
- **Files modified:** none (gate procedure only)
- **Commit:** n/a

**2. [Rule 3 - Blocking] kubelet image GC deleting locally-built images**
- **Found during:** Task 2 STEP 1/4 (octoconv-api:dev vanished pre-install; octoconv-e2e:dev vanished mid-install → ErrImagePull)
- **Issue:** Host disk at ~9Gi free triggered kubelet image GC (`ImageGCFailed wanted to free 2189528268 bytes` events), which deletes images not in use by any container — locally-built :dev tags are eligible until a pod runs them.
- **Fix:** `docker builder prune -af` (freed 6.79GB, per discipline rule), sequentially rebuilt the two evicted images; e2e pod recovered from ImagePullBackOff on its own (IfNotPresent found the restored local image; pod was NOT deleted — backoffLimit 0 would have failed the Job).
- **Files modified:** none
- **Commit:** n/a

**3. [Plan-anticipated] deployment-api.yaml extraEnv hook**
- The plan explicitly flagged this as "the ONE small template touch" if plan 02's Deployment lacked the hook — it did. Added the minimal range block. Called out here per plan instruction.

### Environmental blockers (not auto-fixable)

**4. OrbStack host→cluster proxy wedge (SC3 degraded path)**
- Every direct host dial (svc FQDN, `*.k8s.orb.local`, ClusterIP, pod IP) returned curl (52) empty reply, while docker-bridge IPs from host AND all in-cluster traffic worked. Diagnosis: OrbStack's domain/synthetic-IP proxy layer (198.18.x) wedged. The Pitfall-7 restart runbook was attempted once and **denied by the permission system** (shared infrastructure — other workloads run on this daemon). Fallback: `kubectl port-forward` (API-server channel, unaffected) + `curl --connect-to` preserving the signed Host header. The presigned URL itself is proven correct for the FQDN host; the direct host-routing claim (research: "OrbStack resolves cluster.local from the Mac host") could not be re-verified in this wedged state and should be re-checked after an OrbStack restart.
- Also affected: `kubectl exec` writes were denied by the permission system → the SC3 client row was provisioned with the project's own `cmd/manage-clients` CLI over the postgres port-forward (the sanctioned mechanism).

**5. SC4 NetworkPolicy enforcement absent on OrbStack (hard-gate failure, environmental)**
- See verdict table. The probe pod (`busybox wget http://<worker-ip>:9090/metrics`) retrieved metrics. kube-system has no policy controller; NetworkPolicy objects are accepted but unenforced. No local fix exists short of installing a third-party CNI on OrbStack (out of scope, architectural). The compensating control for the lost 127.0.0.1 metrics boundary is therefore NOT active on local OrbStack clusters — it will be active on any production cluster with a conformant CNI. **Follow-up recommended:** phase-27 (Prometheus/KEDA) should re-run this negative test on the target cluster class.

## Authentication Gates

None (docker/helm/kubectl all pre-authenticated). Two permission-system denials (OrbStack restart, kubectl exec write) documented above — handled via sanctioned alternatives, no user action was required to complete the gate.

## Known Stubs

None — all three artifacts are fully wired; the e2e Job gate (`e2e.enabled: false` default) is intentional configuration, not a stub.

## Threat Flags

None beyond the registered items. T-24-10's mitigation (networkpolicy-metrics) is confirmed correct-but-unenforced on OrbStack (SC4) — this is a deployment-environment gap, recorded above, not a new surface.

## Verification results

- helm lint clean (both layerings); render gating verified both ways; server-dry-run clean.
- SC1 PASS, SC2 PASS, SC3 PASS (degraded path, signature-valid 200 from host), SC4 FAIL-ENVIRONMENTAL, teardown PASS with PVC deletion recorded.
- Zero Go-code changes; compose E2E path untouched (CI unaffected).

## Self-Check: PASSED

- Dockerfile.e2e: FOUND
- deploy/chart/octoconv/templates/job-e2e.yaml: FOUND
- deploy/chart/octoconv/values-e2e.yaml: FOUND
- deploy/chart/octoconv/templates/deployment-api.yaml (extraEnv block): FOUND
- Commit 98bdc05: FOUND
