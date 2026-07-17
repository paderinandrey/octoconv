# Phase 29: v1.6 Hardening Tail - Pattern Map

**Mapped:** 2026-07-18
**Files analyzed:** 10 (3 ScaledObject templates treated as one repeated pattern + 7 distinct files)
**Analogs found:** 9 / 10 (in-repo self-analogs; 1 item — checksum/config annotation — has no in-chart precedent, standard Helm idiom supplied instead)

This is a pure hardening/bugfix phase: every touched file already exists. "Analog" below in most cases means **sibling file in the same near-identical trio** (ScaledObject) or **the file's own established internal pattern** that the fix must stay consistent with.

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|---|---|---|---|---|
| `deploy/chart/octoconv/templates/scaledobject-image.yaml` | config (Helm template) | event-driven (metric-triggered scaler) | `scaledobject-document.yaml` / `scaledobject-html.yaml` (siblings) | exact (near-identical trio) |
| `deploy/chart/octoconv/templates/scaledobject-document.yaml` | config (Helm template) | event-driven | `scaledobject-image.yaml` / `scaledobject-html.yaml` (siblings) | exact |
| `deploy/chart/octoconv/templates/scaledobject-html.yaml` | config (Helm template) | event-driven | `scaledobject-image.yaml` / `scaledobject-document.yaml` (siblings) | exact |
| `deploy/chart/octoconv/templates/prometheus.yaml` | config (Helm template) | batch/config-mount | none in-chart (no `checksum/config` precedent anywhere in `deploy/chart/octoconv/templates/`) | no analog — standard Helm idiom |
| `deploy/chart/octoconv/values.yaml` | config | — (comment-only touch) | itself (existing `keda.*` block) | exact (self-edit) |
| `scripts/presets-rest-acceptance.sh` | test (live acceptance gate) | request-response (HTTP assertions) | itself (existing client-mint/base-url/no-leak-404 sections) | exact (self-extend) |
| `docker-compose.yml` (api service env block) | config | — | own file's `webhookWorker`/`values.yaml`-side `operatorClientIds` comment (Go side); NO in-file `${VAR:-}` precedent — closest analog is shell-script usage in `scripts/keda-gate.sh` | role-match, no exact in-file precedent |
| `scripts/keda-load-proof.sh` | test (live gate / gate-tooling) | event-driven (k8s watch/poll) | `scripts/keda-gate.sh` (explicit shared-lineage sibling; header says "reuses its helper shapes") | exact (sibling gate) |
| `scripts/fixtures/render_evidence.py` | utility (fixture/tooling) | transform (CSV→PNG) | itself + invocation site in `scripts/keda-load-proof.sh` | exact (self + caller) |
| `scripts/fixtures/gen_heavy_docx.py` | utility (fixture/tooling) | file-I/O (generate .docx) | itself | exact (self) |
| `scripts/keda-gate.sh` | test (live gate) | event-driven (k8s watch/poll) | `scripts/keda-load-proof.sh` STEP 8 presigned-download block (D-09(1)) — the ONLY existing presigned-from-host download code in the repo | exact (cross-gate reuse target) |

## Pattern Assignments

### `deploy/chart/octoconv/templates/scaledobject-{image,document,html}.yaml` (config, event-driven)

**Analog:** each other (near-identical trio, header explicitly documents this). Read: `scaledobject-image.yaml` (43 lines), `scaledobject-document.yaml` (63 lines, has an extra `advanced.horizontalPodAutoscalerConfig` block for the document class only), `scaledobject-html.yaml` (41 lines).

**D-01 target — `ignoreNullValues` flip** (all three, identical line shape):
```yaml
# scaledobject-image.yaml:38-41 / scaledobject-document.yaml:58-61 / scaledobject-html.yaml:36-39
        # An empty PromQL result (genuinely empty queue) is treated as 0,
        # NOT a scaler failure — false would make a routine api restart
        # spuriously trip fallback.replicas across this class (Pitfall 4).
        ignoreNullValues: "true"
```
Change to `ignoreNullValues: "false"` on all three, and per D-01 **rewrite the comment** — remove the "genuinely empty queue" justification for `true` (that reasoning no longer applies) and instead explain the new fail-safe intent (absence == scaler error == `fallback.replicas: 1`, same fail-closed philosophy as the webhook-worker). Comment style to preserve: `#`-prefixed, 3-line wrapped comment immediately above the field, same indentation as the field (8 spaces, sibling of `serverAddress`/`query`/`threshold`).

**D-03 target — PromQL trigger query** (all three, identical shape except queue name):
```yaml
# scaledobject-image.yaml:36
        query: sum(octoconv_queue_depth{queue="image", state=~"pending|active"})
# scaledobject-document.yaml:56
        query: sum(octoconv_queue_depth{queue="document", state=~"pending|active"})
# scaledobject-html.yaml:34
        query: sum(octoconv_queue_depth{queue="html", state=~"pending|active"})
```
Change `state=~"pending|active"` → `state=~"pending|active|retry"` on all three. Also update the **file-header doc comment** on each file (line 3-6 of each, e.g. `scaledobject-image.yaml:1-6`) which currently reads `"(D-04 — pending+active only, never retry/scheduled/archived)"` — must be revised to reflect the new retry-inclusive intent per D-03 ("retry-таски это неизбежная имминентная работа").

**D-06 fix #1 target — falsy-0 stabilization consumer** (`scaledobject-document.yaml` only, lines 38-48):
```yaml
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
```
Bug: Go template truthy-check on `{{- if ... }}` treats the numeric value `0` identically to unset/`null` — a caller who explicitly wants `stabilizationWindowSeconds: 0` (instant downscale) silently gets the k8s default (300s) instead. Fix per D-06 with `hasKey`/explicit-nil check, e.g.:
```yaml
  {{- if hasKey .Values.keda.document "scaleDownStabilizationSeconds" }}
  {{- if ne .Values.keda.document.scaleDownStabilizationSeconds nil }}
  ...
  {{- end }}
  {{- end }}
```
(exact idiom is Claude's discretion per D-08 — any pattern that distinguishes "unset/null" from "explicit 0" is acceptable; keep the same 2-space Helm indent style used throughout this file).

**Shared boilerplate to preserve on every edit** (do not touch): the `{{- if and .Values.keda.enabled .Values.prometheus.enabled }}` co-dependency guard (`scaledobject-image.yaml:13`), `fallback: {failureThreshold: 3, replicas: 1}` block, `metadata.labels` via `octoconv.labels` include.

---

### `deploy/chart/octoconv/templates/prometheus.yaml` (config, D-02)

**No in-chart analog** — grepped every template in `deploy/chart/octoconv/templates/` for `checksum` / `sha256`: zero hits. This is a first-of-kind addition to this chart.

**Target location** — the pod template metadata block, `prometheus.yaml:29-33`:
```yaml
  template:
    metadata:
      labels:
        {{- include "octoconv.labels" . | nindent 8 }}
        {{- include "octoconv.selectorLabels" (dict "component" "prometheus" "root" $) | nindent 8 }}
```
Add an `annotations:` block here (standard Helm idiom — same "template rendering forces pod restart on config drift" pattern used community-wide):
```yaml
      annotations:
        checksum/config: {{ include (print $.Template.BasePath "/prometheus.yaml") . | sha256sum }}
```
Claude's Discretion (per CONTEXT.md line 36): whether the checksum covers the whole `prometheus.yaml` file (self-referential — includes the Deployment spec too, simplest, matches the common Helm community pattern of hashing the whole template that defines both the ConfigMap and consuming Deployment) or narrows to just the `ConfigMap`'s `data.prometheus.yml` block via a `toYaml`/sub-template. The whole-file self-hash is the more common idiom and requires zero refactor of this single-file convention (this file already deliberately keeps Deployment+ConfigMap+Service in one file, matching `redis.yaml`'s "same single-file convention" per the file's own header comment at `prometheus.yaml:3`).

**Existing conventions to preserve in this file**: `{{- if .Values.prometheus.enabled }}` top-level guard (`prometheus.yaml:16`), `app.kubernetes.io/component: prometheus` labeling discipline (deliberately NOT `octoconv.io/tier: app` — see PITFALL 1 comment at `prometheus.yaml:8-14`), `---` doc separators between Deployment/ConfigMap/Service.

---

### `deploy/chart/octoconv/values.yaml` (config, comment-only)

**Analog:** itself — `keda.document.scaleDownStabilizationSeconds` already carries an inline trailing comment documenting the D-06-adjacent invariant:
```yaml
# values.yaml:157
    scaleDownStabilizationSeconds: null   # NEW (D-10/Pattern 1) — production default OFF (K8s 300s HPA default preserved); load-proof gate overrides via values-loadproof.yaml
```
D-08/WR-06 asks for an invariant comment tying `keda.*.cooldownPeriod` values (lines 148-162) to `internal/queue/queue.go`'s retry backoff schedule — follow this file's established comment style: multi-line `#`-prefixed block comments ABOVE a key for structural rationale (see the `keda:` block header at `values.yaml:138-144`), inline trailing `#` comments for single-value call-outs (as at line 157). Do not invent a new comment convention.

---

### `scripts/presets-rest-acceptance.sh` (test, request-response, D-04)

**Analog:** itself — the script already has every harness primitive the new system-scope section needs.

**Imports/setup pattern to reuse** (lines 29-43):
```bash
set -euo pipefail
cd "$(dirname "$0")/.."
API_BASE="http://localhost:8090"
export DATABASE_URL="postgres://octo:octo-pass@localhost:5434/octo_db"
export API_KEY_SALT="dev-only-change-me-in-real-deploys"
DB_CONTAINER="octoconv-db"
WORKDIR=$(mktemp -d)
trap 'rm -rf "$WORKDIR"' EXIT
```

**Assertion helper pattern to reuse verbatim** (lines 50-82): `assert_eq`, `assert_contains`, `assert_not_contains` — all echo PASS/FAIL and `exit 1` loud on mismatch, incrementing `PASS_COUNT`.

**Client-mint pattern to reuse** (lines 135-153) — this becomes the template for minting the operator + regular client pair (D-04/D-05):
```bash
SUFFIX=$(uuidgen | tr 'A-Z' 'a-z')
CLIENT_A_OUT=$(go run ./cmd/manage-clients create "presets-rest-acceptance-a-$SUFFIX")
CLIENT_A_ID=$(printf '%s\n' "$CLIENT_A_OUT" | awk -F': ' '/^client id:/{print $2}')
KEY_A=$(printf '%s\n' "$CLIENT_A_OUT" | awk -F': ' '/^api key/{print $2}')
[ -n "$CLIENT_A_ID" ] && [ -n "$KEY_A" ] || { echo "FAIL: ..." >&2; exit 1; }
```
For the new operator section, mint a THIRD (or repurpose B) client, export its ID into `OPERATOR_CLIENT_IDS`, then force-recreate the `api` compose service so `cmd/api/main.go` re-reads env (D-05, "Claude's Discretion: `--force-recreate` vs down/up").

**`http_json` helper to reuse verbatim** (lines 93-105) — already parameterized by method/path/out_file/key/body; system-preset endpoints (`/v1/system/presets`, `/v1/system/presets/{name}`) slot straight in.

**No-leak 404 byte-identical assertion pattern to reuse** (lines 258-276) — directly mirrors what D-04 needs for "non-operator получает byte-identical no-leak 404":
```bash
BODY_404_A=$(cat "$WORKDIR/resp-404-nonexistent.json")
BODY_404_B=$(cat "$WORKDIR/resp-404-crossclient.json")
assert_eq "$BODY_404_A" "$BODY_404_B" "... byte-identical (no leak)"
```
For D-04, the third body to compare byte-identical against is a non-operator's 404 from `requireOperator` (see `internal/api/system_presets_handlers.go:54-67` — `requireOperator` always returns the exact same `noSuchPreset` body via `writeError(w, http.StatusNotFound, noSuchPreset)`).

**Summary/footer pattern to extend** (lines 361-374) — append new `PASS`-line entries for D-04's assertions in the same `echo "D-XX/... : PASS"` style, keep "Stack left running for inspection" behavior (no teardown on success, matches Phase 16/17/18 precedent per the file's own header comment).

**Reference for what the new section must exercise:** `internal/api/system_presets_handlers.go` (all 5 handlers: `handleCreateSystemPreset`, `handleListSystemPresets`, `handleShowSystemPreset`, `handleUpdateSystemPreset`, `handleDeactivateSystemPreset`, plus `requireOperator`/`ParseOperatorClientIDs`) and `cmd/manage-presets/main.go` (system-preset seeding, already used at line 160 of the acceptance script: `go run ./cmd/manage-presets create --name "$NAME_SYS" --target webp` with no `--client-id` = system scope).

---

### `docker-compose.yml` api service (config, D-05)

**Target location** — `docker-compose.yml:77-107`, the `api.environment` block. Current block has NO `${VAR:-}` optional-env entries anywhere in this file (grepped both `docker-compose.yml` and `docker-compose.e2e.yml` for `${...:-`: zero matches) — every existing env value is a hardcoded literal string:
```yaml
# docker-compose.yml:77-90 (representative excerpt)
    environment:
      DATABASE_URL: postgres://octo:octo-pass@postgres:5432/octo_db
      ...
      API_KEY_SALT: "dev-only-change-me-in-real-deploys"
      RATE_LIMIT_IP_RPM: "60"
      RATE_LIMIT_CLIENT_RPM: "120"
```
D-05's `OPERATOR_CLIENT_IDS: "${OPERATOR_CLIENT_IDS:-}"` is the FIRST shell-variable-passthrough entry in this file — there is no in-file precedent to copy verbatim; instead follow the closest same-syntax analog, which is shell-script usage of `${VAR:-default}` already established in `scripts/keda-gate.sh:97` (`CALIBRATE="${CALIBRATE:-0}"`) and `scripts/presets-rest-acceptance.sh`-adjacent scripts — same bash parameter-expansion idiom, just inside a compose YAML scalar instead of a shell script. Insert the new line inside the existing `environment:` map, near the other auth-adjacent var `API_KEY_SALT` (line 89), with a comment explaining fail-closed-on-empty (mirroring the Go-side comment already at `deploy/chart/octoconv/values.yaml:34-37` — `"comma-separated operator client UUIDs ... empty = no operators, fail-closed"`).

**Insertion-point pattern to preserve**: existing DEBT-05-flagged trailing comment blocks in this same environment map (e.g. `docker-compose.yml:92-94` explaining why `IMAGE_MAX_RETRY` etc. are read unconditionally) — use the same inline `#`-comment-above-the-var convention for the new `OPERATOR_CLIENT_IDS` line.

---

### `scripts/keda-load-proof.sh` (test, D-06 fixes #2, #3, #4)

**Analog:** `scripts/keda-gate.sh` — explicitly its sibling/lineage (`keda-load-proof.sh:41-42`: "reuses its helper shapes, not a modification of it"). Both share `assert_eq`/`assert_nonempty`, `teardown()`+`trap teardown EXIT`, `waitForReplicasAtLeast`/`waitForReplicasAtMost`.

**D-06 fix #2 target — SC3 stale-pod race** (`keda-load-proof.sh:695-705`):
```bash
BUSY_POD=$(kubectl get pod -n "$NAMESPACE" -l "app.kubernetes.io/component=document-worker" \
	--sort-by=.metadata.creationTimestamp -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
if [ -z "$BUSY_POD" ]; then
	BUSY_POD=$(kubectl get pod -n "$NAMESPACE" -l "app=document-worker" \
		--sort-by=.metadata.creationTimestamp -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
fi
```
Bug: picking `.items[0]` by earliest `creationTimestamp` can select a pod that is already `Terminating` (e.g. from a prior test iteration or reconciler churn) instead of the genuinely-busy pod1. Fix per D-06: exclude `Terminating` pods from selection — e.g. filter `.status.phase==Running` and/or check `.metadata.deletionTimestamp` is empty before taking `.items[0]`. Keep the existing `kubectl get pod -l ... --sort-by=.metadata.creationTimestamp -o jsonpath=...` shape and the two-tier label-selector fallback (`app.kubernetes.io/component=document-worker` then `app=document-worker`).

**D-06 fix #3 target — false-PASS download check** (`keda-load-proof.sh:836-844`):
```bash
RESULT_BYTES=$(curl -s --connect-to "${RESULT_HOST}:${RESULT_PORT}:127.0.0.1:${MINIO_LOCAL_PORT}" -o /tmp/keda-loadproof-long-result.bin -w '%{size_download}' "$RESULT_URL")
kill "$MINIO_PF_PID" >/dev/null 2>&1 || true
MINIO_PF_PID=""
if [ "${RESULT_BYTES:-0}" -le 0 ]; then
	echo "FAIL: D-09(1) -- long job result URL returned 0 bytes" >&2
	exit 1
fi
```
Bug: `%{size_download}` can be nonzero even on an HTTP error response body (e.g. a 403/404 XML error from MinIO has nonzero bytes) — a false PASS. Fix per D-06: add `-f` (`--fail`, curl exits nonzero on HTTP >=400) or capture `%{http_code}` via `-w` and assert `200`, same style as `http_json`'s existing `-w '%{http_code}'` pattern used throughout both gate scripts (e.g. `keda-load-proof.sh:329` `postJob`'s `HTTP_STATUS=$(curl -s -o "$out_file" -w '%{http_code}' ...)`).

**D-06 fix #4 target — orphaned watcher process** (`keda-load-proof.sh:735-752`):
```bash
snapshotLoop() {
	while true; do
		kubectl get pod "$BUSY_POD" -n "$NAMESPACE" -w --output-watch-events \
			-o jsonpath='...' 2>/dev/null \
			| while IFS= read -r watch_line; do
				echo "read_ts=... " >>"$SC3_TIMESTAMPS_FILE"
			done
		if ! kubectl get pod "$BUSY_POD" -n "$NAMESPACE" >/dev/null 2>&1; then
			break
		fi
		sleep 1
	done
}
snapshotLoop &
SNAPSHOT_PID=$!
...
kill "$SNAPSHOT_PID" >/dev/null 2>&1 || true
wait "$SNAPSHOT_PID" 2>/dev/null || true
```
Bug: `kubectl get pod ... -w | while read` is a PIPED subshell — `kill "$SNAPSHOT_PID"` kills the `snapshotLoop` function's own subshell PID but the piped `kubectl ... -w` child process (the actual long-running watch) is a SEPARATE process that survives, orphaned. Fix per D-06: kill the whole process group / the piped `kubectl` child explicitly (e.g. capture its PID via `$!` right after backgrounding the pipeline, or use `pkill -P "$SNAPSHOT_PID"`, or restructure to avoid the pipe-to-while subshell). This mirrors the SAME class of problem `internal/convert/exec.go`'s `Setpgid` + process-group-kill pattern solves for engine subprocesses (CLAUDE.md "Hardened external process execution" — process-group kill on cancellation is the project's established idiom for exactly this orphaned-child-process class of bug), though that's Go code, not directly copy-pasteable into bash — treat it as the project's philosophical precedent for "always kill the whole tree, not just the direct child PID."

**Existing teardown/trap pattern to preserve** (`keda-load-proof.sh:153-202`, verbatim shared with `keda-gate.sh:86-119`) — the `teardown()` function's `SNAPSHOT_PID`/`SAMPLER_PID`/port-forward-PID kill sequence is the right INSERTION POINT for the fix #4 change (make the kill itself effective, don't restructure the trap architecture).

---

### `scripts/fixtures/render_evidence.py` (utility, D-06 fix #5 — interpreter pin)

**Analog:** itself + its two call sites in `keda-load-proof.sh`:
```bash
# keda-load-proof.sh:622-625
uv run --with matplotlib python3 scripts/fixtures/render_evidence.py \
	--csv "$CSV_FILE" \
	--png "$EVIDENCE_DIR/sc1-sc2-burst-${RUN_TS}.png" \
	--title "Phase 28 SC1/SC2: image-class burst 0->N->0"
```
Current file docstring already documents the invocation contract (`render_evidence.py:8-11`): `uv run --with matplotlib python3 scripts/fixtures/render_evidence.py ...` — no `--python` version pin anywhere. D-06 wants an interpreter pin (`uv run --python 3.11` or a shebang-guard). Two possible touch points: (a) the CALL SITE in `keda-load-proof.sh` (add `--python 3.11` to the `uv run` invocation), and/or (b) a runtime guard inside `render_evidence.py` itself checking `sys.version_info`. Given the docstring at the top of the file IS the contract, update BOTH the docstring example (lines 8-11) and the actual call site in `keda-load-proof.sh:622` consistently. Same fix pattern applies in parallel to `gen_heavy_docx.py`'s two call sites (`keda-load-proof.sh:407-408` and `:657-658`, both `uv run --with python-docx python3 scripts/fixtures/gen_heavy_docx.py`).

---

### `scripts/fixtures/gen_heavy_docx.py` (utility, D-06 fix #6 — CWD-relative SAMPLE_IMAGE)

**Bug location** (`gen_heavy_docx.py:31-33`):
```python
# Reuse the existing image fixture so no new binary asset needs to be
# committed for this generator (28-RESEARCH.md Code Examples).
SAMPLE_IMAGE = os.path.join("internal", "e2e", "testdata", "sample.png")
```
Bug: this is CWD-relative — the script only finds the fixture when invoked from the repo root (as `keda-load-proof.sh` currently does via `cd "$(dirname "$0")/.."` at line 49, then `scripts/fixtures/gen_heavy_docx.py` as a relative path). Any other invocation CWD silently degrades `have_sample_image` to `False` (line 63: `os.path.isfile(SAMPLE_IMAGE)` — the script already gracefully skips the image rather than failing, which is WHY this bug is silent). Fix per D-06: resolve relative to `__file__`, e.g.:
```python
SAMPLE_IMAGE = os.path.join(
    os.path.dirname(os.path.abspath(__file__)), "..", "..",
    "internal", "e2e", "testdata", "sample.png",
)
```
(exact relative depth: `scripts/fixtures/gen_heavy_docx.py` → repo root is two `..` up, matching `internal/e2e/testdata/sample.png`'s existing path components already used in the current (broken) join). Preserve the existing `have_sample_image = os.path.isfile(SAMPLE_IMAGE)` graceful-degradation call site (line 63) unchanged — only the constant's construction changes.

---

### `scripts/keda-gate.sh` (test, D-07 — presigned direct-dial)

**Analog:** `scripts/keda-load-proof.sh` STEP 8's presigned-download block is the ONLY existing presigned-URL-fetch code in the repo (`keda-load-proof.sh:793-844`) — it is the pattern to REPLACE/EXTEND with the direct-dial variant, not literally copy, since `keda-gate.sh` currently has ZERO presigned-URL handling at all (grepped `presign|connect-to|download_url|result_url` against `keda-gate.sh`: no matches).

**Current port-forward-based presigned pattern to reference** (`keda-load-proof.sh:816-844`):
```bash
# The presigned URL embeds the IN-CLUSTER S3 endpoint
# (minio.<ns>.svc.cluster.local:9000), which this host cannot resolve --
# and the host name is part of the S3 v4 signature, so it cannot be
# rewritten. Port-forward minio on a free LOCAL port ... and redirect the
# TCP connection there via curl --connect-to: the URL, Host header, and
# signature stay byte-identical while only the transport goes through the
# port-forward.
RESULT_HOSTPORT=$(printf '%s' "$RESULT_URL" | awk -F/ '{print $3}')
RESULT_HOST=${RESULT_HOSTPORT%%:*}
RESULT_PORT=${RESULT_HOSTPORT##*:}
if [ "$RESULT_PORT" = "$RESULT_HOSTPORT" ]; then
	RESULT_PORT=80
fi
MINIO_LOCAL_PORT="19000"
kubectl port-forward -n "$NAMESPACE" svc/minio "${MINIO_LOCAL_PORT}:9000" >/tmp/keda-loadproof-minio-pf.log 2>&1 &
MINIO_PF_PID=$!
sleep 3
RESULT_BYTES=$(curl -s --connect-to "${RESULT_HOST}:${RESULT_PORT}:127.0.0.1:${MINIO_LOCAL_PORT}" -o /tmp/keda-loadproof-long-result.bin -w '%{size_download}' "$RESULT_URL")
kill "$MINIO_PF_PID" >/dev/null 2>&1 || true
```
D-07's new step in `keda-gate.sh` REPLACES this `--connect-to`/port-forward workaround with a DIRECT dial: (1) a pre-flight OrbStack daemon/proxy health check that loud-fails if wedged (do NOT mask with a workaround — this is the opposite instinct from the block above, which exists specifically to work around the wedge), then (2) a bare `curl` against the presigned FQDN URL from the OrbStack host with bounded retry, no `--connect-to`, no port-forward. Reference the closing caveat this closes: `.planning/milestones/v1.6-phases/24-helm-chart-core/24-VERIFICATION.md:11,31` ("Confirm ... that a presigned MinIO URL resolves via a DIRECT host dial ... to fully re-validate the FQDN landmine's host-reachability claim without the workaround").

**Insertion-point conventions to follow in `keda-gate.sh`** (its own established idioms, not load-proof's):
- Step numbering/`log()` helper: `log() { echo ""; echo "--- $* ---"; }` (`keda-gate.sh:79`), each STEP announced via `log "STEP N: ..."`.
- `assert_nonempty`/`assert_eq` (`keda-gate.sh:59-77`) for the new health-check and byte-count assertions — same shape as `keda-load-proof.sh`'s (byte-identical helper, both scripts keep their own copy per each file's "copied verbatim" comment, e.g. `keda-load-proof.sh:106`).
- Teardown/trap (`keda-gate.sh:86-119`) — any new port-forward or background PID this step introduces must be added to `teardown()`'s kill sequence, matching the `API_PF_PID`/`DB_PF_PID` pattern already there.
- The job whose result URL gets probed already exists in this script — SC2's image-class job (`keda-gate.sh:365-373`, `IMAGE_JOB_ID`) is the natural attachment point: after `waitForReplicasAtLeast worker 1 120`, poll the job to `done` (pattern already used at `keda-gate.sh:416-426` for the SC2/D-12c drain wait), extract its `download_url` (same `grep -o '"download_url":"[^"]*"'` extraction shape used in `keda-load-proof.sh:812`), then run the new direct-dial step against it.

## Shared Patterns

### Gate-script skeleton (bash)
**Source:** `scripts/keda-gate.sh` (canonical, older) / `scripts/keda-load-proof.sh` (explicit sibling, "reuses its helper shapes")
**Apply to:** any edit inside these two files, and the extended `presets-rest-acceptance.sh`
```bash
set -euo pipefail
cd "$(dirname "$0")/.."
PASS_COUNT=0
assert_eq() { ... echo "FAIL: ... >&2"; exit 1 ... echo "PASS: $label == $actual"; }
assert_nonempty() { ... }
log() { echo ""; echo "--- $* ---"; }
teardown() { local exit_code=$?; ...; exit "$exit_code"; }
trap teardown EXIT
```
Loud-fail, self-documenting PASS/FAIL transcript lines, EXIT-trap teardown that always runs, never silently swallow a failure.

### ScaledObject co-dependency guard
**Source:** `deploy/chart/octoconv/templates/scaledobject-image.yaml:13` (and identical in the other two)
**Apply to:** any edit to the three ScaledObject templates — must never break `{{- if and .Values.keda.enabled .Values.prometheus.enabled }}`.

### Helm values-file comment discipline
**Source:** `deploy/chart/octoconv/values.yaml:34-37` (operatorClientIds), `:96-97,109-110,123-126` (multi-line block-comment-above-key style), `:157` (inline trailing-comment style)
**Apply to:** `values.yaml` D-08 invariant comment addition, and any new ScaledObject template comment rewrites (D-01/D-03).

### Optional-env `${VAR:-}` idiom
**Source:** `scripts/keda-gate.sh:97` (`CALIBRATE="${CALIBRATE:-0}"`), `scripts/keda-load-proof.sh:103` (`PAGE_UNITS="${PAGE_UNITS:-300}"`)
**Apply to:** `docker-compose.yml`'s new `OPERATOR_CLIENT_IDS: "${OPERATOR_CLIENT_IDS:-}"` line — same bash-parameter-expansion syntax, first use inside a compose YAML scalar rather than a shell script body.

## No Analog Found

| File | Role | Data Flow | Reason |
|---|---|---|---|
| `deploy/chart/octoconv/templates/prometheus.yaml` (checksum/config annotation, D-02) | config | batch/config-mount | No `checksum/*` or `sha256sum` usage anywhere in `deploy/chart/octoconv/templates/` — this chart has never needed a config-drift-triggered pod restart before. Use the standard community Helm idiom (`{{ include (print $.Template.BasePath "/prometheus.yaml") . \| sha256sum }}` under `spec.template.metadata.annotations`) documented above. |
| `scripts/keda-gate.sh` (presigned direct-dial step, D-07) | test | request-response | No presigned-URL-fetch code exists in `keda-gate.sh` today (only in its sibling `keda-load-proof.sh`, and that one is port-forward-based, i.e. the OPPOSITE of what D-07 wants). Adapt `keda-load-proof.sh`'s block by REMOVING the port-forward/`--connect-to` workaround, not by copying it. |

## Metadata

**Analog search scope:** `deploy/chart/octoconv/templates/`, `deploy/chart/octoconv/values*.yaml`, `scripts/*.sh`, `scripts/fixtures/*.py`, `docker-compose*.yml`, `internal/api/system_presets_handlers.go`, `cmd/manage-clients/main.go`, `cmd/manage-presets/main.go`
**Files scanned:** 16 read in full (all under 1000 lines; no grep+offset needed) + repo-wide grep for `checksum`, `${...:-`, `presign|connect-to`
**Pattern extraction date:** 2026-07-18
