---
phase: 10-document-worker-reconciler-integration
reviewed: 2026-07-09T00:00:00Z
depth: standard
files_reviewed: 14
files_reviewed_list:
  - .env.example
  - Dockerfile.document-worker
  - Dockerfile.worker
  - cmd/document-worker/main.go
  - docker-compose.yml
  - internal/jobs/repo.go
  - internal/metrics/metrics.go
  - internal/queue/client.go
  - internal/queue/queue.go
  - internal/queue/queue_test.go
  - internal/reconciler/reconciler.go
  - internal/reconciler/reconciler_test.go
  - internal/worker/worker.go
  - internal/worker/worker_test.go
---

# Phase 10: Code Review Report

**Reviewed:** 2026-07-09T00:00:00Z
**Depth:** standard
**Files Reviewed:** 14
**Status:** issues_found

## Summary

This phase wires up the document-class worker (`cmd/document-worker`), extends the
reconciler to route recoveries by engine class, and adds the `Dockerfile.document-worker`
/ `docker-compose.yml` deployment plumbing. The code is unusually well-commented, and the
core state-machine/asynq-uniqueness reasoning in `internal/queue/queue.go` and
`internal/reconciler/reconciler.go` is careful and internally consistent, backed by solid
unit tests (queue TTL derivations, reconciler recovery-cap/duplicate-guard/bounded-retry
behavior).

Two real defects were found in the new `HandleDocumentConvert` path and in the
docker-compose wiring for the new document-engine env vars; both are traceable to this
phase's own commits. No BLOCKER-level (crash/security/data-loss) issues were found, but
several WARNING-level correctness/config-consistency/test-coverage gaps should be fixed
before this ships.

## Warnings

### WR-01: Document-engine timeout failures are mis-reported to clients as "corrupted input"

**File:** `internal/worker/worker.go:250-264` (introduced by commit `411da5a`)
**Issue:** `isDocumentTerminal` intentionally classifies a `DOCUMENT_ENGINE_TIMEOUT`
expiry (`context.DeadlineExceeded`) as terminal (DOC-08), which is a reasonable retry
policy. However, `HandleDocumentConvert`'s terminal branch always calls:
```go
_ = h.repo.MarkFailed(ctx, jobID, "engine_error", "unsupported or corrupted input format", ...)
```
This hardcoded message is used identically whether the terminal error was an actual
LibreOffice content-validation failure (`terminalLibreOfficeSignatures` — genuinely a bad
document) **or** a plain timeout (a large-but-valid document, or an engine hang unrelated
to input corruption). A client polling `GET /jobs/{id}` or receiving the completion
webhook for a job that simply took longer than `DOCUMENT_ENGINE_TIMEOUT` will be told
their file is "unsupported or corrupted," which is actively misleading and will send
users down the wrong troubleshooting path. The raw stderr is preserved in
`job_events.detail` for internal diagnostics, but the *client-facing* message is wrong
for the timeout case.
**Fix:** Branch the message (and ideally the `error_code`) on the actual cause before
calling `MarkFailed`, e.g.:
```go
if err := h.process(ctx, job); err != nil {
    if isDocumentTerminal(err) {
        code, msg := "engine_error", "unsupported or corrupted input format"
        if errors.Is(err, context.DeadlineExceeded) {
            code, msg = "engine_timeout", "document conversion exceeded the configured timeout"
        }
        _ = h.repo.MarkFailed(ctx, jobID, code, msg, map[string]any{"engine_stderr": err.Error()})
        ...
    }
}
```

### WR-02: New `DOCUMENT_MAX_RETRY` / `DOCUMENT_ENGINE_TIMEOUT` (and pre-existing `IMAGE_MAX_RETRY` / `ENGINE_TIMEOUT`) are never actually wired into docker-compose for the services that need them

**File:** `docker-compose.yml:64-93` (api service), `docker-compose.yml:95-124` (worker service) — gap introduced/extended by commit `f4c5f61`
**Issue:** `queue.NewClient()` (`internal/queue/client.go:52-69`) reads
`IMAGE_MAX_RETRY`, `ENGINE_TIMEOUT`, `DOCUMENT_MAX_RETRY`, and
`DOCUMENT_ENGINE_TIMEOUT` from the environment of *whichever process constructs the
client* to derive `imageMaxRetry`/`imageUniqueTTL`/`documentMaxRetry`/
`documentUniqueTTL` — and both `cmd/api/main.go` and `cmd/worker/main.go` (the
reconciler's enqueuer) construct a `queue.Client`. `queue.go`'s extensive doc comments
on `ImageUniqueTTL`/`DocumentUniqueTTL` stress that this TTL derivation must never
"silently drift" from the real worst-case retry lifetime of the *consuming* worker.
Despite that, `docker-compose.yml` only sets `DOCUMENT_WORKER_CONCURRENCY` and
`DOCUMENT_ENGINE_TIMEOUT` on the `document-worker` service itself — it never sets
`DOCUMENT_MAX_RETRY` anywhere (not even on `document-worker`), and never sets
`IMAGE_MAX_RETRY` / `ENGINE_TIMEOUT` / `DOCUMENT_MAX_RETRY` / `DOCUMENT_ENGINE_TIMEOUT`
on the `api` or `worker` services at all. Today the values happen to line up only
because every service falls back to the same hardcoded Go defaults (`IMAGE_MAX_RETRY=4`,
`ENGINE_TIMEOUT=120s`, `DOCUMENT_MAX_RETRY=3`, `DOCUMENT_ENGINE_TIMEOUT=300s`) — but this
is implicit and fragile: a future change to e.g. `DOCUMENT_ENGINE_TIMEOUT` on
`document-worker` alone (the obvious place an operator would look) would silently leave
`api`'s and `worker`'s enqueue-time `MaxRetry`/`asynq.Unique` TTL derivation stale,
reopening exactly the double-processing race (`T-03-10`) the code's own comments warn
about.
**Fix:** Explicitly set `IMAGE_MAX_RETRY`, `ENGINE_TIMEOUT`, `DOCUMENT_MAX_RETRY`, and
`DOCUMENT_ENGINE_TIMEOUT` identically on every service that constructs a `queue.Client`
(`api`, `worker`, `document-worker`), e.g. via a shared `.env` file referenced by all
three (`env_file:`) rather than duplicated inline `environment:` blocks, so the values
can never drift apart.

### WR-03: `HandleDocumentConvert` has no handler-level test coverage

**File:** `internal/worker/worker_test.go` (entire file); `internal/worker/worker.go:231-278`
**Issue:** `worker_test.go` only exercises the pure predicate functions
(`isTerminal`, `isDocumentTerminal`). There is no test that constructs a `Handler` with
fake `repo`/`store`/`registry`/`enqueuer` and drives `HandleDocumentConvert` end to end
(success path, terminal-failure path, transient-failure path). This is exactly the kind
of gap that let WR-01 ship undetected — a test asserting the `error_code`/`error_message`
recorded by `MarkFailed` on a timeout-classified failure would have caught it.
**Fix:** Add fakes for `jobs.Repo`/`storage.Client`/`convert.Registry`/`queue.Client` (or
extract narrower interfaces as `internal/api` already does) and add table-driven tests
for `HandleDocumentConvert` mirroring the classifier-level coverage that already exists.

### WR-04: Jobs with an unroutable `engine` value are swept forever with no cap or escalation

**File:** `internal/reconciler/reconciler.go:131-149`
**Issue:** The `default:` branch for an unrecognized `j.Engine` (fail-closed, by design)
does `continue` without ever calling `RequeueStale` or `MarkFailed` — which means
`RecoveryCount` for that job never increments (only `detailActionRecovery` events count
toward it), so the job can never reach `MaxRecoveries` and be terminally exhausted like
every other stale-job path. A job stuck with a corrupted/unrecognized `engine` column
value (data corruption, a future engine value rolled out to the DB before its worker
exists, etc.) will be re-flagged as `"unroutable_engine"` on every sweep tick,
indefinitely, with no automatic path to resolution — unlike the queued/active and
webhook-gap paths, which all eventually terminate (recovered, exhausted, or resolved).
**Fix:** At minimum, document this as an intentional operational trade-off requiring
manual intervention (a metrics-based alert on sustained `unroutable_engine` counts), or
add a bounded escalation (e.g., after N sweep ticks seeing the same unroutable job,
terminally fail it with a distinct `error_code` so it stops being swept).

## Info

### IN-01: `envInt`/`envDuration`/`firstField` are now duplicated across four locations

**File:** `cmd/document-worker/main.go:130-155`, `internal/queue/client.go:111-146`, and (pre-existing) `cmd/api/main.go`, `cmd/worker/main.go`
**Issue:** This phase adds a fourth verbatim copy of the `firstField`/`envInt`/
`envDuration` trio in `cmd/document-worker/main.go`. This mirrors an already-accepted
project convention (per-package unexported duplication, documented in project
conventions), but four copies is a real drift risk — a fix to the inline-comment-
stripping logic in one copy will not propagate to the others.
**Fix:** Consider extracting a small shared `internal/envutil` package now that a third
consumer (`cmd/document-worker`) has been added, rather than continuing to duplicate.

---

_Reviewed: 2026-07-09T00:00:00Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
