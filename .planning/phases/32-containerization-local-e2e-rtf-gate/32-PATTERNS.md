# Phase 32: Containerization & Local E2E + RTF Gate - Pattern Map

**Mapped:** 2026-07-18
**Files analyzed:** 11 (new + modified)
**Analogs found:** 10 / 11

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|--------------------|------|-----------|-----------------|----------------|
| `Dockerfile.audio-worker` (new) | config (multi-stage Dockerfile) | file-I/O (build-time artifact assembly) | `Dockerfile.document-worker` (3rd-party-binary stage) + `Dockerfile.chromium-worker` (apt-only runtime) | exact (structural) |
| `docker-compose.yml` — new `audio-worker` service block | config | request-response / CRUD (orchestration) | `document-worker` / `chromium-worker` service blocks | exact |
| `docker-compose.yml` — `AUDIO_ENGINE_TIMEOUT`/`AUDIO_MAX_RETRY` propagation to `api`/`worker`/`document-worker`/`chromium-worker`/`webhook-worker-1`/`webhook-worker-2` | config | cross-cutting | existing `DOCUMENT_ENGINE_TIMEOUT`/`HTML_ENGINE_TIMEOUT` duplication pattern already in those same 4 blocks | exact |
| `.github/workflows/ci.yml` — bake matrix `set:` entries (`docker-build` + `e2e` jobs) | config (CI) | batch | existing `document-worker.cache-to/from` / `chromium-worker.cache-to/from` lines | exact |
| `scripts/audio-rtf-measure.sh` (new) | utility (measurement script) | batch / transform | `scripts/verapdf-measure.sh` | exact (structural) |
| `internal/e2e/e2e_test.go` — `TestAudioConversionE2E` + `assertDownloadIsNonEmptyTranscript` | test | request-response (live HTTP E2E) | `TestImageConversionE2E` + `assertDownloadIsImage` | exact |
| `internal/e2e/testdata/jfk.wav` (new, copied) | test fixture | file-I/O | `internal/convert/testdata/audio/jfk.wav` (source) / other `internal/e2e/testdata/*` fixtures (destination convention) | exact |
| `internal/convert/whisper.go` — `SetAudioThreads` setter + `threads` param in `whisperArgs` | service (converter engine) | transform | `SetAudioModelPath`/`audioModelPath` (this same file) — also `SetVeraPDFTimeout`/`verapdfTimeout` (`internal/convert/verapdf.go`) | exact |
| `internal/convert/cgroup.go` (new) — cgroup CPU-limit detection + `TestCgroupCPULimit` | utility | file-I/O (single sysfs read) | none — genuinely new pattern in this codebase | no analog |
| `cmd/audio-worker/main.go` — `AUDIO_THREADS` env + cgroup-detect + `convert.SetAudioThreads` call | config/wiring (`main`) | event-driven (startup wiring) | this same file's existing `AUDIO_MODEL_PATH` → `convert.SetAudioModelPath` wiring (lines 74-86) | exact |
| `.env.example` — `AUDIO_ENGINE_TIMEOUT` (real value, drop `[ASSUMED]` placeholder), new `AUDIO_THREADS` doc block | config (docs) | — | existing `AUDIO_*`/`DOCUMENT_*` block conventions in the same file | exact |

## Pattern Assignments

### `Dockerfile.audio-worker` (config, file-I/O)

**Analogs:** `Dockerfile.document-worker` (3-stage build with a from-source/3rd-party binary stage) and `Dockerfile.chromium-worker` (simplest apt-only runtime stage, no `platform:` pin needed)

**Go build stage — copy verbatim, only the binary name/cmd path changes** (`Dockerfile.document-worker:1-7`):
```dockerfile
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/document-worker ./cmd/document-worker
```
For audio: `RUN CGO_ENABLED=0 go build -o /out/audio-worker ./cmd/audio-worker`

**Third-party/from-source artifact stage shape** (`Dockerfile.document-worker:9-17`, veraPDF is pulled pre-built from a pinned image; whisper.cpp instead needs `apt-get install build-essential cmake git` + `git clone --branch` + `cmake --build`, but the SHAPE — a throwaway stage whose only job is producing an artifact the runtime stage `COPY --from=` pulls — is identical):
```dockerfile
FROM verapdf/cli:v1.30.2 AS verapdf
```
(RESEARCH.md's drafted `whisper-build` stage in the Dockerfile skeleton section is the concrete adaptation — reuse it verbatim, it was already cross-checked against this exact analog.)

**Runtime stage — apt-only, `USER nobody`, no `tini`/init needed** (`Dockerfile.chromium-worker:11-32`, chosen over `document-worker`'s runtime stage because audio, like html, has no forking-daemon subprocess chain requiring a reaper):
```dockerfile
FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates \
      tini \
      chromium-headless-shell \
      fonts-liberation2 \
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/chromium-worker /usr/local/bin/chromium-worker
USER nobody
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/chromium-worker"]
```
**Deviation:** audio-worker is a single synchronous two-stage CLI invocation (ffmpeg then whisper-cli, no forking daemon) — per `Dockerfile.worker:19-22`'s documented rationale for libvips, this means **no `tini`** and a **plain `ENTRYPOINT ["/usr/local/bin/audio-worker"]`** (not the `tini --` wrapper form). Copy `Dockerfile.worker`'s comment verbatim as the justification:
```dockerfile
# The image engine is a single synchronous libvips CLI invocation (no forking
# daemon like LibreOffice's oosplash->soffice.bin), so there is no orphaned-
# grandchild risk here and no init/reaper is needed as PID 1.
ENTRYPOINT ["/usr/local/bin/worker"]
```

**No `platform:` pin** — confirmed by both analogs' contrast: `document-worker` needs `platform: linux/amd64` (`docker-compose.yml:227`) because `verapdf/cli` publishes amd64-only; `chromium-worker` needs none. whisper.cpp is source-built here (like the RESEARCH.md skeleton's `-DGGML_NATIVE=OFF` stage), so it follows `chromium-worker`'s no-pin precedent.

---

### `docker-compose.yml` — `audio-worker` service block (config, request-response)

**Analog:** `document-worker` block (`docker-compose.yml:220-266`) — structurally closest: `deploy.resources.limits` block present, `DOCUMENT_*`-prefixed own-engine vars plus DEBT-05 cross-engine vars duplicated underneath.

**Full copyable shape** (`docker-compose.yml:220-266`):
```yaml
  document-worker:
    platform: linux/amd64
    build:
      context: .
      dockerfile: Dockerfile.document-worker
    container_name: octoconv-document-worker
    restart: always
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
      minio:
        condition: service_healthy
    environment:
      DATABASE_URL: postgres://octo:octo-pass@postgres:5432/octo_db
      REDIS_ADDR: redis:6379
      S3_ENDPOINT: minio:9000
      S3_ACCESS_KEY: minioadmin
      S3_SECRET_KEY: minioadmin
      S3_BUCKET: octoconv
      S3_USE_SSL: "false"
      DOCUMENT_WORKER_CONCURRENCY: "2"
      DOCUMENT_ENGINE_TIMEOUT: "300s"
      IMAGE_MAX_RETRY: "4"
      ENGINE_TIMEOUT: "120s"
      DOCUMENT_MAX_RETRY: "3"
      HTML_MAX_RETRY: "3"
      HTML_ENGINE_TIMEOUT: "60s"
      VERAPDF_TIMEOUT: "60s"
      METRICS_ADDR: "127.0.0.1:9090"
    deploy:
      resources:
        limits:
          cpus: "2.0"
          memory: 1g
```
For `audio-worker`: no `platform:` line (see Dockerfile analog note above), `dockerfile: Dockerfile.audio-worker`, own-engine vars become `AUDIO_WORKER_CONCURRENCY`/`AUDIO_ENGINE_TIMEOUT`/`AUDIO_MAX_RETRY`/`AUDIO_MAX_DURATION_SECONDS`/`AUDIO_MODEL_PATH` (+ optional `AUDIO_THREADS`), and — per DEBT-05 — the SAME cross-engine block (`IMAGE_MAX_RETRY`, `ENGINE_TIMEOUT`, `DOCUMENT_MAX_RETRY`, `DOCUMENT_ENGINE_TIMEOUT`, `HTML_MAX_RETRY`, `HTML_ENGINE_TIMEOUT`) as every other worker service. RESEARCH.md's "Compose Service Wiring" section already has the fully drafted block — copy it directly, filling in the RTF-measured `AUDIO_ENGINE_TIMEOUT`/`cpus`/`memory` values once measured.

---

### `docker-compose.yml` — IN-02 `AUDIO_ENGINE_TIMEOUT`/`AUDIO_MAX_RETRY` propagation (config, cross-cutting)

**Analog:** the existing `DOCUMENT_ENGINE_TIMEOUT`/`HTML_ENGINE_TIMEOUT` duplication already present in `api` (`docker-compose.yml:98-106`), `worker` (`:141-147`), `document-worker` (`:250-258`), `chromium-worker` (`:291-298`) — this is the DEBT-05 pattern to extend, not a new pattern.

**Exact excerpt to mirror** (`docker-compose.yml:98-106`, `api` service):
```yaml
      # queue.NewClient() (constructed by cmd/api/main.go) reads these vars
      # UNCONDITIONALLY, even though the api process only enqueues (it
      # never runs the engine) -- DEBT-05.
      IMAGE_MAX_RETRY: "4"
      ENGINE_TIMEOUT: "120s"
      DOCUMENT_MAX_RETRY: "3"
      DOCUMENT_ENGINE_TIMEOUT: "300s"
      HTML_MAX_RETRY: "3"
      HTML_ENGINE_TIMEOUT: "60s"
```
Add `AUDIO_MAX_RETRY: "<measured>"` / `AUDIO_ENGINE_TIMEOUT: "<measured>"` to this exact block (and its 5 siblings) in the SAME commit as the new `audio-worker` service, using the SAME comment style. `webhook-worker-1`/`webhook-worker-2` currently omit ANY `DOCUMENT_*`/`HTML_*` vars (rely on Go defaults) — per RESEARCH.md, this phase should close that gap for `AUDIO_*` specifically on those two services even though it doesn't touch the pre-existing `DOCUMENT_*`/`HTML_*` omission there.

**Verification command** (from RESEARCH.md's own "Warning signs" line): `grep AUDIO_ENGINE_TIMEOUT docker-compose.yml` must return exactly 7 matches after this phase (api, worker, document-worker, chromium-worker, webhook-worker-1, webhook-worker-2, audio-worker).

---

### `.github/workflows/ci.yml` — bake matrix entries (config/CI, batch)

**Analog:** existing `document-worker`/`chromium-worker` cache-scope lines in both the `docker-build` job (`ci.yml:47-74`) and the `e2e` job (`ci.yml:80-107`).

**`docker-build` job — exact lines to extend** (`ci.yml:62-74`):
```yaml
          set: |
            api.cache-to=type=gha,mode=max,scope=api
            api.cache-from=type=gha,scope=api
            worker.cache-to=type=gha,mode=max,scope=worker
            worker.cache-from=type=gha,scope=worker
            document-worker.cache-to=type=gha,mode=max,scope=document-worker
            document-worker.cache-from=type=gha,scope=document-worker
            chromium-worker.cache-to=type=gha,mode=max,scope=chromium-worker
            chromium-worker.cache-from=type=gha,scope=chromium-worker
            webhook-worker-1.cache-to=type=gha,mode=max,scope=webhook-worker
            webhook-worker-1.cache-from=type=gha,scope=webhook-worker
            webhook-worker-2.cache-to=type=gha,mode=max,scope=webhook-worker
            webhook-worker-2.cache-from=type=gha,scope=webhook-worker
```
Add two lines: `audio-worker.cache-to=type=gha,mode=max,scope=audio-worker` / `audio-worker.cache-from=type=gha,scope=audio-worker`.

**`e2e` job — exact lines to extend** (`ci.yml:101-107`):
```yaml
          set: |
            api.cache-from=type=gha,scope=api
            worker.cache-from=type=gha,scope=worker
            document-worker.cache-from=type=gha,scope=document-worker
            chromium-worker.cache-from=type=gha,scope=chromium-worker
            webhook-worker-1.cache-from=type=gha,scope=webhook-worker
            webhook-worker-2.cache-from=type=gha,scope=webhook-worker
```
Add one line: `audio-worker.cache-from=type=gha,scope=audio-worker`. No change needed elsewhere in `ci.yml` — `docker compose ... up -d` (`:109`) has no explicit service list, so `audio-worker` is picked up automatically once the compose block exists. Flag (don't pre-emptively bump) the `docker-build` job's `timeout-minutes: 20` (`ci.yml:50`) and `e2e` job's `go test ... -timeout 30m` (`ci.yml:120`) if live timing after adding the whisper.cpp compile stage / `TestAudioConversionE2E` looks tight.

---

### `scripts/audio-rtf-measure.sh` (new) (utility, batch/transform)

**Analog:** `scripts/verapdf-measure.sh` (full file, 155 lines) — direct structural template per RESEARCH.md.

**Header/setup pattern to copy** (`scripts/verapdf-measure.sh:27-38`):
```bash
set -euo pipefail

cd "$(dirname "$0")/.."

IMAGE_TAG="octoconv-document-worker:verapdf-measure"
RUNS="${VERAPDF_MEASURE_RUNS:-10}"
CONTAINER="octoconv-verapdf-measure-$$"
WORKDIR=$(mktemp -d)
trap 'docker rm -f "$CONTAINER" >/dev/null 2>&1 || true; rm -rf "$WORKDIR"' EXIT
```
Adapt: `IMAGE_TAG="octoconv-audio-worker:rtf-measure"`, `RUNS="${AUDIO_RTF_MEASURE_RUNS:-10}"`, `CONTAINER="octoconv-audio-rtf-measure-$$"`. No `--platform linux/amd64` needed anywhere (unlike veraPDF — whisper.cpp has no arch constraint, per Dockerfile analog note above).

**Build + long-lived container pattern** (`scripts/verapdf-measure.sh:47-57`):
```bash
docker build --platform linux/amd64 -f Dockerfile.document-worker -t "$IMAGE_TAG" .

docker run -d --name "$CONTAINER" --platform linux/amd64 \
	--memory=1g --cpus=2.0 \
	--entrypoint sleep "$IMAGE_TAG" infinity >/dev/null

docker exec "$CONTAINER" mkdir -p /tmp/work /tmp/work-plain
docker cp "$FIXTURE_SRC" "$CONTAINER:/tmp/sample.docx"
```
Adapt (drop `--platform`): `docker build -f Dockerfile.audio-worker -t "$IMAGE_TAG" .` then `docker run -d --name "$CONTAINER" --memory=<compose-matched> --cpus=<compose-matched> --entrypoint sleep "$IMAGE_TAG" infinity`, then `docker cp` the synthetic RTF fixture in.

**In-container timed-loop + nearest-rank p95 pattern (the reusable core)** (`scripts/verapdf-measure.sh:90-119`):
```bash
RAW_MILLIS=$(docker exec "$CONTAINER" sh -c "
  set -e
  for i in \$(seq 1 $RUNS); do
    start=\$(date +%s%N)
    verapdf -f 2b --format xml /tmp/work/sample.pdf > /tmp/work/run_\${i}.xml
    end=\$(date +%s%N)
    echo \$(( (end - start) / 1000000 ))
  done
")

echo "Per-run wall-clock (ms):"
echo "$RAW_MILLIS"

SORTED=$(echo "$RAW_MILLIS" | sort -n)
N=$(echo "$SORTED" | wc -l | tr -d ' ')
RANK=$(awk -v n="$N" 'BEGIN { r = n * 0.95; rr = (r == int(r)) ? r : int(r) + 1; print rr }')
P95_MS=$(echo "$SORTED" | sed -n "${RANK}p")
P95_S=$(awk -v ms="$P95_MS" 'BEGIN { printf "%.3f", ms / 1000 }')

BUDGET_MS=10000
if [ "$P95_MS" -gt "$BUDGET_MS" ]; then
	echo "FAIL: p95=${P95_MS}ms exceeds the D-01 10s budget (${BUDGET_MS}ms) -- NO-GO" >&2
	exit 1
fi
echo "PASS: p95=${P95_MS}ms <= ${BUDGET_MS}ms budget (D-01)"
```
Adapt: replace the `verapdf ...` command with the two-stage `ffmpeg ... && whisper-cli -t <threads> ...` invocation against the synthetic multi-minute fixture; **before** the p95 sort step, divide each raw millisecond value by the fixture's known duration in seconds to get per-run RTF (`RTF_i = wall_clock_seconds_i / fixture_duration_seconds`), THEN sort/rank on RTF values, not raw ms. The `BUDGET_MS`/exit-1 gate becomes a derived-timeout decision per RESEARCH.md's "GO/NO-GO budget derivation" section, not a fixed compare.

**Peak memory observation pattern (directly reusable, copy verbatim)** (`scripts/verapdf-measure.sh:144-151`):
```bash
PEAK_BYTES=$(docker exec "$CONTAINER" cat /sys/fs/cgroup/memory.peak 2>/dev/null || echo "unavailable")
if [ "$PEAK_BYTES" != "unavailable" ]; then
	PEAK_MB=$(awk -v b="$PEAK_BYTES" 'BEGIN { printf "%.1f", b / 1024 / 1024 }')
	echo "Peak memory (JVM + idle LibreOffice footprint): ${PEAK_BYTES} bytes (~${PEAK_MB} MiB) of 1024 MiB limit"
else
	echo "Peak memory: cgroup v2 memory.peak not available in this environment"
fi
```
Also add a `cat /sys/fs/cgroup/cpu.max` `docker exec` spot-check (Wave 0's first task per RESEARCH.md Assumption A2) using this same `docker exec "$CONTAINER" cat ...` idiom, cross-checked against the `-t <n>` value actually passed.

---

### `internal/e2e/e2e_test.go` — `TestAudioConversionE2E` + `assertDownloadIsNonEmptyTranscript` (test, request-response)

**Analog:** `TestImageConversionE2E` + `assertDownloadIsImage` (`internal/e2e/e2e_test.go:1096-1150`) — chosen over `TestDocumentConversionE2E` (table-driven, 6 format pairs — too large a shape for this single-fixture requirement).

**Full analog to copy and adapt** (`internal/e2e/e2e_test.go:1096-1121`):
```go
func TestImageConversionE2E(t *testing.T) {
	cfg := e2eSetup(t)
	apiKey := provisionClient(t)

	data, err := os.ReadFile(filepath.Join("testdata", "sample.png"))
	if err != nil {
		t.Fatalf("read fixture sample.png: %v", err)
	}

	callbackURL, received := startWebhookReceiver(t, cfg.webhookHost)

	jobID := postJob(t, cfg.baseURL, apiKey, "sample.png", data, "jpg", callbackURL, "")

	body := pollUntilDone(t, cfg.baseURL, apiKey, jobID, 2*time.Minute)

	downloadURL, _ := body["download_url"].(string)
	if downloadURL == "" {
		t.Fatalf("job %s done but download_url missing/empty: %v", jobID, body)
	}
	assertDownloadIsImage(t, downloadURL, "jpg")

	assertSignedWebhook(t, received, jobID)
}
```
For audio: fixture `testdata/jfk.wav`, target `"txt"`, `pollUntilDone(..., 5*time.Minute)` (matches document/html's cold-start allowance, per RESEARCH.md, NOT the 2-minute image bound), and a new `assertDownloadIsNonEmptyTranscript` in place of `assertDownloadIsImage` — RESEARCH.md's "Code Examples" section already has the full drafted test body, copy it directly (it was written against this exact analog this session).

**`postJob` signature/shape to call unchanged** (`internal/e2e/e2e_test.go:167-193`):
```go
func postJob(t *testing.T, baseURL, apiKey, filename string, data []byte, target, callbackURL, opts string) string {
	t.Helper()
	req := buildJobRequest(t, baseURL, apiKey, filename, data, target, callbackURL, opts)
	resp, err := e2eHTTP.Do(req)
	...
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /v1/jobs (%s -> %s) status = %d, want 202; body=%s", filename, target, resp.StatusCode, body)
	}
	var out struct{ JobID string `json:"job_id"` }
	...
	return out.JobID
}
```

**`pollUntilDone` — reuse unchanged, pass the 5-minute bound** (`internal/e2e/e2e_test.go:252-288`): polls every 2s, fatals on `"failed"`, returns terminal body on `"done"`.

**`startWebhookReceiver`/`assertSignedWebhook` — reuse unchanged** (`internal/e2e/e2e_test.go:341-388`, `:763-808`): no audio-specific change needed; webhook delivery is fully decoupled to `webhook-worker-1/2` regardless of engine class.

**New assertion helper pattern** (mirrors `assertDownloadIsImage`, `internal/e2e/e2e_test.go:1123-1150`, but swaps `convert.Sniff` format-check for a non-empty-bytes check since ASR output is non-deterministic — Pitfall 9):
```go
func assertDownloadIsImage(t *testing.T, downloadURL, wantFormat string) {
	t.Helper()
	resp, err := downloadClient().Get(downloadURL)
	if err != nil {
		t.Fatalf("GET download_url: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		t.Fatalf("GET download_url status = %d, want 200; body=%s", resp.StatusCode, b)
	}
	body, err := io.ReadAll(resp.Body)
	...
}
```
RESEARCH.md's drafted `assertDownloadIsNonEmptyTranscript` (identical GET/status-check shape, `len(bytes.TrimSpace(body)) == 0` instead of `convert.Sniff`) is ready to copy verbatim.

**Fixture placement:** copy `internal/convert/testdata/audio/jfk.wav` → `internal/e2e/testdata/jfk.wav` (real file copy, never a symlink — no symlinks exist anywhere in `internal/e2e/testdata/`, confirmed by RESEARCH.md).

---

### `internal/convert/whisper.go` — `SetAudioThreads` + `-t` injection (service, transform)

**Analog:** this file's own `audioModelPath`/`SetAudioModelPath`/`model()` 3-tier fallback (`internal/convert/whisper.go:12-89`) — the exact shape to replicate for threads, per RESEARCH.md's explicit instruction. A second, cross-file analog is `verapdfTimeout`/`SetVeraPDFTimeout`/`effectiveVeraPDFTimeout` in `internal/convert/verapdf.go:10-33` (same env-only-in-main + setter convention, 2-tier not 3-tier since it has no test-injection field).

**Exact pattern to replicate** (`internal/convert/whisper.go:12-28`):
```go
// audioModelPath stores the AUDIO_MODEL_PATH budget for every subsequent
// AudioConverter.model() call. It is set once at process startup via
// SetAudioModelPath -- mirroring effectiveVeraPDFTimeout's threading
// (verapdf.go), this package never reads AUDIO_MODEL_PATH (or any env var)
// directly; env-only-in-main is the enforced convention. Zero value (empty
// string) means "never set", in which case model() falls through to
// defaultAudioModelPath.
var audioModelPath string

// SetAudioModelPath stores the AUDIO_MODEL_PATH override for every
// subsequent AudioConverter.model() call. Call exactly once at process
// startup, BEFORE the asynq server starts consuming tasks (single write
// before any concurrent reader -- no mutex needed, mirroring
// SetVeraPDFTimeout's contract in verapdf.go).
func SetAudioModelPath(path string) {
	audioModelPath = path
}
```
For threads: `var audioThreads int` + `func SetAudioThreads(n int) { audioThreads = n }`, called once from `cmd/audio-worker/main.go` before `srv.Start(mux)`, same happens-before contract.

**3-tier fallback to replicate** (`internal/convert/whisper.go:81-89`):
```go
func (c AudioConverter) model() string {
	if c.modelPath != "" {
		return c.modelPath
	}
	if audioModelPath != "" {
		return audioModelPath
	}
	return defaultAudioModelPath
}
```
Threads only need a 2-tier fallback (no per-test override field observed as needed): `audioThreads` (set via `SetAudioThreads`) else `runtime.NumCPU()` — actual cgroup-vs-`runtime.NumCPU()` resolution happens in `cmd/audio-worker/main.go` before calling `SetAudioThreads`, per RESEARCH.md's design.

**Exact injection point — `whisperArgs`** (`internal/convert/whisper.go:154-166`, confirmed no `-t`/`--threads` flag present today):
```go
func whisperArgs(model, normPath, outBase string, outFlags []string, o AudioOpts) []string {
	args := []string{"-m", model, "-f", normPath, "-of", outBase}
	args = append(args, outFlags...)
	lang := o.Language
	if lang == "" {
		lang = "auto"
	}
	args = append(args, "-l", lang)
	if o.Translate {
		args = append(args, "-tr")
	}
	return args
}
```
Add a `threads int` parameter and insert `"-t", strconv.Itoa(threads)` (needs `"strconv"` import, not currently imported in this file — check before adding). Caller (`Convert`, `internal/convert/whisper.go:249`) passes `c.threads()`-equivalent resolved value, mirroring how `c.model()` is called today (`internal/convert/whisper.go:249`: `args := whisperArgs(c.model(), normPath, outBase, outFlags, o)`).

---

### `internal/convert/cgroup.go` (new) — cgroup CPU-limit detection (utility, file-I/O)

**No existing OctoConv analog** — this is confirmed genuinely new territory (RESEARCH.md: "No existing OctoConv precedent"). The nearest structural sibling for "single-file sysfs read with graceful fallback" is `scripts/verapdf-measure.sh`'s own `/sys/fs/cgroup/memory.peak` read (`scripts/verapdf-measure.sh:145`, bash not Go, but same sysfs-read-with-fallback shape) — informative but not a Go code analog.

**Illustrative shape already drafted in RESEARCH.md** (verify against a real running container before trusting):
```go
func cgroupCPULimit() (int, bool) {
	b, err := os.ReadFile("/sys/fs/cgroup/cpu.max")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(b))
	if len(fields) != 2 || fields[0] == "max" {
		return 0, false // unlimited or unparseable — caller must fall back
	}
	quota, err1 := strconv.ParseFloat(fields[0], 64)
	period, err2 := strconv.ParseFloat(fields[1], 64)
	if err1 != nil || err2 != nil || period == 0 {
		return 0, false
	}
	n := int(quota / period) // floor, deliberately not ceil
	if n < 1 {
		n = 1
	}
	return n, true
}
```
Package placement: `internal/convert` (confirmed by the phase's own test map: `go test ./internal/convert/... -run TestCgroupCPULimit`), exported (e.g. `CgroupCPULimit()`) so `cmd/audio-worker/main.go` can call it directly — follows the project's existing "package-level doc comment on exactly one file explaining its role" convention (CLAUDE.md Comments section) if placed in its own new file rather than appended to `whisper.go`.

---

### `cmd/audio-worker/main.go` — `AUDIO_THREADS`/cgroup wiring (config/wiring, event-driven)

**Analog:** this same file's existing `AUDIO_MODEL_PATH` → `convert.SetAudioModelPath` wiring (`cmd/audio-worker/main.go:74-86`).

**Exact pattern to replicate**:
```go
// AUD-05/D-01: AUDIO_MODEL_PATH is read ONLY here (env-only-in-main,
// mirroring VERAPDF_TIMEOUT's threading into internal/convert) and
// injected via a setter -- internal/convert never calls os.Getenv
// directly. This MUST run before srv.Start(mux) below...
convert.SetAudioModelPath(stripInlineComment(os.Getenv("AUDIO_MODEL_PATH")))
```
Add, in the same place (before `srv.Start(mux)`, `cmd/audio-worker/main.go:113`):
```go
threads := envInt("AUDIO_THREADS", 0) // 0 = unset, fall through to cgroup/NumCPU detection
if threads <= 0 {
	if n, ok := convert.CgroupCPULimit(); ok {
		threads = n
	} else {
		threads = runtime.NumCPU()
	}
}
convert.SetAudioThreads(threads)
```
This requires adding `"runtime"` to the import block (`cmd/audio-worker/main.go:5-26`) — not currently imported.

**Existing `envInt`/`envDuration`/`envDurationSeconds`/`firstField`/`stripInlineComment` helpers — reuse unchanged, do not duplicate** (`cmd/audio-worker/main.go:149-217`, full block already present in this exact file):
```go
func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(firstField(v)); err == nil {
			return n
		}
	}
	return def
}
```
`AUDIO_THREADS` should use `envInt` (bare integer, no unit) exactly like `AUDIO_WORKER_CONCURRENCY` already does (`cmd/audio-worker/main.go:92`: `Concurrency: envInt("AUDIO_WORKER_CONCURRENCY", 2)`).

**`AUDIO_ENGINE_TIMEOUT` placeholder to remove** (`cmd/audio-worker/main.go:60`, `:101`):
```go
envDuration("AUDIO_ENGINE_TIMEOUT", 600*time.Second), // [ASSUMED] placeholder, Phase 32 re-derives from RTF measurement -- do NOT copy DOCUMENT_ENGINE_TIMEOUT's 300s
```
Both call sites (the `worker.NewHandler` arg at line 60 AND the `ShutdownTimeout` computation at line 101) read the same env var — once the RTF-measured value ships in `docker-compose.yml`/`.env.example`, this Go code needs NO change (the `def` fallback becomes dead-in-practice once the compose/`.env.example` value is set), but the `[ASSUMED] placeholder` comment should be updated/removed to avoid it going stale.

---

### `.env.example` — `AUDIO_ENGINE_TIMEOUT`/`AUDIO_THREADS` docs (config)

**Analog:** the existing `AUDIO_*` block in the same file (`.env.example:50-63`).

**Exact block to update** (`.env.example:51-63`):
```
AUDIO_ENGINE_TIMEOUT=600s   # [ASSUMED] placeholder only -- Phase 32 re-derives this from measured RTF (real-time factor) against the pinned whisper-cli/model; do NOT treat as tuned, and do NOT copy DOCUMENT_ENGINE_TIMEOUT's 300s verbatim (whisper transcription is expected to run far longer than a LibreOffice conversion)
AUDIO_MAX_RETRY=3   # bounded retry budget for audio conversion, mirrors DOCUMENT_MAX_RETRY/HTML_MAX_RETRY; also feeds the derived per-job uniqueness-lock TTL together with AUDIO_ENGINE_TIMEOUT (AudioUniqueTTL, Phase 31)
AUDIO_WORKER_CONCURRENCY=2   # mirrors DOCUMENT_WORKER_CONCURRENCY/HTML_WORKER_CONCURRENCY; Phase 32 re-sizes from measured per-job RSS (whisper.cpp threads sized to cgroup CPU limit, not host core count)
AUDIO_MAX_DURATION_SECONDS=14400   # ...
```
Replace `AUDIO_ENGINE_TIMEOUT=600s` + its `[ASSUMED]` comment with the RTF-measured value and a comment recording HOW it was derived (mirrors `DOCUMENT_ENGINE_TIMEOUT=300s   # Phase 9 D-01`'s terse "measured/decided in phase N" style, `.env.example:41`). Add a new `AUDIO_THREADS` doc line following the `AUDIO_MODEL_PATH` comment-block style (`.env.example:55-63`, multi-line `#` comments, "Never derived from client input" callout pattern) — document it as optional, default unset (auto-detect).

## Shared Patterns

### DEBT-05 cross-engine env duplication (queue.NewClient() unconditional reads)
**Source:** `docker-compose.yml:98-106` (api), `:141-147` (worker), `:250-258` (document-worker), `:291-298` (chromium-worker)
**Apply to:** the new `audio-worker` block AND retrofit `AUDIO_MAX_RETRY`/`AUDIO_ENGINE_TIMEOUT` into all 6 pre-existing blocks (api, worker, document-worker, chromium-worker, webhook-worker-1, webhook-worker-2) in the same commit.
```yaml
      IMAGE_MAX_RETRY: "4"
      ENGINE_TIMEOUT: "120s"
      DOCUMENT_MAX_RETRY: "3"
      DOCUMENT_ENGINE_TIMEOUT: "300s"
      HTML_MAX_RETRY: "3"
      HTML_ENGINE_TIMEOUT: "60s"
```

### env-only-in-main + setter (no os.Getenv inside internal/convert)
**Source:** `cmd/audio-worker/main.go:86` (`convert.SetAudioModelPath(...)`), `internal/convert/whisper.go:26-28` (`SetAudioModelPath`), `internal/convert/verapdf.go:25-27` (`SetVeraPDFTimeout`)
**Apply to:** `SetAudioThreads`/`audioThreads` in `internal/convert/whisper.go`, called once from `cmd/audio-worker/main.go` before `srv.Start(mux)`.

### Unprivileged runtime + no-init-when-no-forking-daemon
**Source:** `Dockerfile.worker:17-22` (libvips, no tini), `Dockerfile.document-worker:49-55` / `Dockerfile.chromium-worker:23-32` (tini, forking daemons)
**Apply to:** `Dockerfile.audio-worker` — `USER nobody`, plain `ENTRYPOINT` (no tini), matching `ffmpeg`+`whisper-cli`'s single-synchronous-CLI-invocation shape.

### Nearest-rank p95 + fail-closed go/no-go gate
**Source:** `scripts/verapdf-measure.sh:103-119`
**Apply to:** `scripts/audio-rtf-measure.sh`'s RTF computation and `AUDIO_ENGINE_TIMEOUT` derivation, `set -euo pipefail` + `trap ... EXIT` cleanup idiom, exit-1-on-NO-GO.

### E2E test shape (env-gated skip, webhook-confirmed happy path)
**Source:** `internal/e2e/e2e_test.go:1096-1150` (`TestImageConversionE2E`), `:74-88` (`e2eSetup`), `:95` (`provisionClient`), `:347` (`startWebhookReceiver`), `:763` (`assertSignedWebhook`)
**Apply to:** `TestAudioConversionE2E` — reuse every helper unchanged, only the fixture/target/timeout/download-assertion differ.

## No Analog Found

| File | Role | Data Flow | Reason |
|------|------|-----------|--------|
| `internal/convert/cgroup.go` (cgroup CPU-limit detection) | utility | file-I/O | Genuinely new to this codebase — the other three engines never needed runtime container-resource introspection (RESEARCH.md: "No existing OctoConv precedent"). RESEARCH.md's own drafted `cgroupCPULimit` function (Code Examples section) is the closest thing to a template and should be used directly; the closest STRUCTURAL sibling (single sysfs-file read with fail-open fallback) is `scripts/verapdf-measure.sh`'s bash `/sys/fs/cgroup/memory.peak` read, not a Go analog. |

## Metadata

**Analog search scope:** `Dockerfile.*` (repo root), `docker-compose.yml`, `docker-compose.e2e.yml`, `.github/workflows/ci.yml`, `scripts/verapdf-measure.sh`, `internal/e2e/e2e_test.go`, `internal/convert/whisper.go`, `internal/convert/verapdf.go`, `cmd/audio-worker/main.go`, `.env.example`
**Files scanned:** 11 (all read in full or via targeted non-overlapping offset/limit reads this session)
**Pattern extraction date:** 2026-07-18
