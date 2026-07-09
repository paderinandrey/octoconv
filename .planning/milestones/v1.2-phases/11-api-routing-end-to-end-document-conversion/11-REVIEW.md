---
phase: 11-api-routing-end-to-end-document-conversion
reviewed: 2026-07-10T00:00:00Z
depth: standard
files_reviewed: 9
files_reviewed_list:
  - internal/convert/convert.go
  - internal/convert/libvips.go
  - internal/convert/libreoffice.go
  - internal/convert/convert_test.go
  - internal/api/api.go
  - internal/api/handlers.go
  - internal/api/handlers_test.go
  - internal/e2e/e2e_test.go
  - docker-compose.e2e.yml
findings:
  critical: 0
  warning: 4
  info: 7
  total: 11
status: issues_found
---

# Phase 11: Code Review Report

**Reviewed:** 2026-07-10
**Depth:** standard
**Files Reviewed:** 9
**Status:** issues_found

## Summary

Reviewed the Phase 11 engine-aware routing changes (`Converter.Engine()`, `Registry.EngineFor`, `handleCreateJob`'s engine switch), the LibreOffice converter, and the new live E2E suite plus its compose override. Cross-checked the call graph beyond the listed files: `internal/worker/worker.go` (inPath `in.<sourceFormat>` convention matches `libreoffice.go`'s `producedPath` derivation), `internal/reconciler/reconciler.go` (engine-aware recovery switch exists), `internal/db/migrations/0001_init.sql` (`engine` CHECK constraint includes `'document'`), `internal/convert/docsniff.go` (`SniffContainer` is `io.ReaderAt`-only, so the `rest` stream reaching `storage.Upload` is not disturbed), and `internal/webhook/sign.go` (E2E HMAC assertion matches the delivery-side signature format). `go vet` and `gofmt` are clean; unit tests for all three reviewed packages pass.

No critical (security/data-loss) defect found. Four warnings: the MIME-type table was never extended for the document formats this phase routes (PDF outputs are served as `application/octet-stream`), the E2E compose override breaks on Linux docker engines because the `api` service cannot resolve `host.docker.internal` when validating `callback_url`, engine-name string literals are hand-duplicated across four locations despite `Engine()` being documented as the single source of truth, and the E2E HTTP calls have no client timeout so a hung endpoint bypasses the suite's intended time bounds. The deliberate SSRF relaxation in `docker-compose.e2e.yml` is confined to the override file (verified absent from the base compose) and is not reported as a finding per the review brief.

## Warnings

### WR-01: `convert.MIMEType` has no entries for document formats or PDF — outputs served as `application/octet-stream`

**File:** `internal/api/handlers.go:231` (root cause `internal/convert/sniff.go:99-114`; also manifests at `internal/worker/worker.go:409,420`)
**Issue:** `handleCreateJob` stores the input's Content-Type as `convert.MIMEType(detected)` with a comment claiming it is "the canonical MIME of the detected format (D-06)". `MIMEType`'s switch covers only the five image formats; every document format this phase routes (`docx`/`xlsx`/`pptx`/`odt`/`ods`/`odp`) falls through to `application/octet-stream`. Worse, the worker uploads and records the conversion **output** with `convert.MIMEType(job.TargetFormat)` — `MIMEType("pdf")` is also `application/octet-stream` — so every presigned PDF download produced by the new document pipeline is served with the wrong Content-Type (browsers download instead of rendering, downstream services relying on Content-Type misbehave). The phase added document detection and routing but never extended the MIME table, silently violating the D-06 contract stated inline.
**Fix:** Extend `MIMEType` in `internal/convert/sniff.go`:
```go
case "pdf":
    return "application/pdf"
case "docx":
    return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
case "xlsx":
    return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
case "pptx":
    return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
case "odt":
    return "application/vnd.oasis.opendocument.text"
case "ods":
    return "application/vnd.oasis.opendocument.spreadsheet"
case "odp":
    return "application/vnd.oasis.opendocument.presentation"
```
Also add a `store.contentType` assertion to `TestCreateJob_DocumentDetectedAndAccepted` / `TestCreateJob_ODFDetectedAndAccepted` (they currently assert everything except Content-Type, which is why this gap survived; the PNG test at `handlers_test.go:336-338` does assert it).

### WR-02: E2E compose override omits `extra_hosts` on the `api` service — suite fails on Linux docker engines at `callback_url` validation

**File:** `docker-compose.e2e.yml:17-30`
**Issue:** The E2E suite's default callback host is `host.docker.internal` (`internal/e2e/e2e_test.go:67-74`). `validateCallbackURL` runs **in the API process at job creation** and resolves non-IP hosts via `net.LookupHost` (`internal/api/callbackurl.go:53-56`), rejecting with 400 on resolution failure. The override adds `extra_hosts: host.docker.internal:host-gateway` only to `worker` and `document-worker` — not to `api`. On Docker Desktop (macOS/Windows) `host.docker.internal` resolves in every container natively, so this works on the development machine; on a plain Linux docker engine (the standard CI/server platform) it does not resolve without `extra_hosts`, so the `sample.docx` subtest fails at `postJob` with `400 invalid callback_url` before any conversion runs. The harness deliverable is broken-by-construction on Linux.
**Fix:**
```yaml
  api:
    environment:
      WEBHOOK_ALLOW_PRIVATE_IPS: "true"
      WEBHOOK_ALLOW_INSECURE_HTTP: "true"
    extra_hosts:
      - "host.docker.internal:host-gateway"
```

### WR-03: Engine-class names are hand-duplicated string literals across four locations despite the "single source of truth" claim

**File:** `internal/api/handlers.go:27-28,266-277` (also `internal/convert/libvips.go:38`, `internal/convert/libreoffice.go:71`, `internal/reconciler/reconciler.go:132`, `internal/db/migrations/0001_init.sql:48`)
**Issue:** `Converter.Engine()`'s doc comment (`convert.go:25-28`) declares it "the single source of truth for engine-class routing", yet the values `"image"` / `"document"` are independently re-declared as unexported constants in `internal/api` (`engineImage`, `engineDocument`), returned as bare literals from each converter, switched on again in the reconciler, and enumerated in the DB CHECK constraint. Nothing ties these copies together at compile time. A future converter returning a typo'd engine string (or a new engine class added to `convert` but not to the api/reconciler switches) compiles cleanly and then: the API creates the job row + uploads to S3, hits the fail-closed `default` branch, returns 500, and the row is stranded in `queued` for the reconciler to count as `unroutable_engine`. Fail-closed is the right behavior, but the drift surface is unnecessarily wide.
**Fix:** Export canonical constants from the owning package and use them everywhere:
```go
// internal/convert/convert.go
const (
    EngineImage    = "image"
    EngineDocument = "document"
)
```
Then `func (LibvipsConverter) Engine() string { return EngineImage }`, replace `engineImage`/`engineDocument` in `internal/api/handlers.go` with `convert.EngineImage`/`convert.EngineDocument`, and use the same constants in the reconciler switch.

### WR-04: E2E HTTP calls use clients with no timeout — a hung endpoint bypasses every intended time bound

**File:** `internal/e2e/e2e_test.go:143,181,315,391-404`
**Issue:** `postJob` and `pollUntilDone` use `http.DefaultClient` (zero `Timeout`), and `downloadClient()` builds a custom-transport client also without `Timeout`. `pollUntilDone`'s 5-minute deadline is only checked *between* polls; a single GET that hangs (API wedged, half-open TCP through the compose network) blocks forever inside `Do`, so the suite never produces its intended per-job diagnostic (`job X did not reach a terminal state within 5m, last=...`) and instead dies much later with the `go test` binary-timeout panic and a raw goroutine dump. For a suite whose entire purpose is CI signal quality, this converts the most likely live-stack failure mode (hung service) into the least diagnosable output.
**Fix:** Use one shared client with a per-request timeout, e.g.:
```go
var e2eHTTP = &http.Client{Timeout: 30 * time.Second}
```
and use it in `postJob`/`pollUntilDone`; set `Timeout: 60 * time.Second` on the client returned by `downloadClient()` (both branches).

## Info

### IN-01: Stale package doc comment on `convert.go`

**File:** `internal/convert/convert.go:1-2`
**Issue:** The package comment still reads "concrete engine implementations (libvips for images)" — this phase's sibling work added `LibreOfficeConverter` for documents, so the doc is no longer accurate about the package's contents.
**Fix:** Update to "...(libvips for images, LibreOffice for documents)".

### IN-02: Server-side read errors are misreported as client errors (400/422)

**File:** `internal/api/handlers.go:122-125,199-205`
**Issue:** `convert.Sniff` returns an error only for a genuine I/O failure reading the (already-parsed, server-buffered) multipart file, yet the handler maps it to `400 "invalid multipart form"`. Similarly, `convert.Dimensions` can fail with either `ErrDimensionsUnknown` (client's fault, correct 422) or a real read error; the handler treats both as the 422 "cannot determine declared image dimensions" rejection. Transient server-side I/O failures get blamed on the client and logged as content-validation rejections, polluting the D-08 rejection log.
**Fix:** Distinguish with `errors.Is(err, convert.ErrDimensionsUnknown)` → 422, anything else → 500 "failed to read upload"; map a non-nil `Sniff` error to 500 as well.

### IN-03: `ParseMultipartForm` uses the full upload cap as `maxMemory`

**File:** `internal/api/handlers.go:80`
**Issue:** `r.ParseMultipartForm(s.maxUploadByte)` passes the upload limit (default 100 MiB) as the in-memory threshold, so every concurrent request may buffer up to ~100 MiB in RAM before spilling to disk. `MaxBytesReader` bounds the total, but N concurrent large uploads multiply this; the API container has no compose memory limit (unlike the worker).
**Fix:** Pass a small fixed threshold so large files spill to temp files: `r.ParseMultipartForm(10 << 20)` (the body remains capped by `MaxBytesReader` independently).

### IN-04: Fail-closed enqueue paths have zero test coverage and are structurally untestable

**File:** `internal/api/handlers.go:266-282`, `internal/api/handlers_test.go:72-85`
**Issue:** The `default:` unknown-engine branch and the `enqueueErr != nil` branch (both → 500, job stranded `queued`) are never exercised: `fakeQueue` cannot return errors, and the engine value comes from the package-global `convert.Default` (not injected), so no fake can produce an unknown engine. The T-11-02 fail-closed guarantee is asserted only by a comment.
**Fix:** Add an `enqueueErr error` field to `fakeQueue` and a test asserting 500 + `repo.created != nil` (row persisted for the reconciler) on enqueue failure. Testing the `default` branch would additionally require injecting the registry into `Server` — worth considering when a third engine lands.

### IN-05: `handleHealth` shares one sequential 3s budget across all three pings

**File:** `internal/api/handlers.go:36-61`
**Issue:** The pings run sequentially under a single 3-second context. If Postgres consumes the whole budget, the Redis and S3 pings fail instantly on the expired context and are reported "unreachable" even though they were never genuinely probed — the per-dependency detail (the stated point of OBS-02) is misleading in exactly the degraded scenarios it exists for.
**Fix:** Run the three pings concurrently (each with the shared 3s deadline), or give each its own sub-deadline.

### IN-06: E2E client provisioning never cleans up its rows

**File:** `internal/e2e/e2e_test.go:82-107`
**Issue:** `provisionClient` inserts a fresh `e2e-test-client-<uuid>` row into the live stack's Postgres per run and never deletes it; repeated runs against a long-lived stack accumulate orphaned client rows (each with a valid API key hash).
**Fix:** `t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM clients WHERE name = $1", name) })` after a successful create.

### IN-07: E2E suite worst-case runtime exceeds `go test`'s default 10m timeout, undocumented

**File:** `internal/e2e/e2e_test.go:264-308` (package doc `:1-27`)
**Issue:** Six sequential subtests, each allowed up to 5 minutes of polling (plus 90s webhook wait for docx), give a worst-case ~31 minutes — past the default `go test` 10-minute binary timeout. On a slow/cold stack the suite panics with a goroutine dump partway through instead of reporting per-pair results. The otherwise-thorough package doc lists every required env var but not the required `-timeout` flag.
**Fix:** Document `go test -timeout 40m ./internal/e2e/` in the package comment (or lower the per-job bound after measuring real cold-start latency).

---

_Reviewed: 2026-07-10_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
