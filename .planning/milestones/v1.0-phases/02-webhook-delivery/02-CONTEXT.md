# Phase 2: Webhook Delivery - Context

**Gathered:** 2026-07-04
**Status:** Ready for planning

<domain>
## Phase Boundary

Clients receive job completion results (`done`/`failed`) pushed via signed webhook callbacks to a per-job `callback_url`, instead of polling `GET /v1/jobs/{id}`. This phase covers: accepting `callback_url` on job creation, HMAC-SHA256 signed delivery, bounded retry with backoff, delivery tracking in `webhook_deliveries`, and dead-lettering after retries are exhausted. It does NOT cover: retry-safety/reconciler for conversion jobs themselves (Phase 3), content validation, storage lifecycle, observability (Phase 4), per-client webhook-secret rotation, manual replay tooling, or granting clients direct S3/MinIO bucket access (all explicitly deferred — see `<deferred>`).

</domain>

<decisions>
## Implementation Decisions

### HMAC Signing Secret
- **D-01:** Single shared secret via a new env var (e.g. `WEBHOOK_SIGNING_SECRET`), used to HMAC-SHA256-sign every outgoing webhook payload for every client. Matches the existing env-var-only configuration convention. Per-client secret rotation (`WEBHOOK-V2-01`) is explicitly deferred — not built in this phase.

### callback_url — Scope & Validation
- **D-02:** `callback_url` stays **per-job**, not per-client. The column already exists on `jobs` (`internal/db/migrations/0001_init.sql:57`) but the API does not yet accept it from clients — `POST /v1/jobs` needs a new field/form parameter wired into the handler. Rationale: a client may route different jobs to different callback destinations; matches the already-established schema (Notion DDL), avoids a new migration/CLI surface for no demonstrated need.
- **D-03 (SSRF guard):** Basic validation only, performed once at job-creation time (`POST /v1/jobs`): require a valid scheme (https, or http in dev) and block obvious loopback/RFC1918/link-local/metadata-endpoint (`169.254.169.254`) ranges after resolving the hostname. Do **not** re-validate before each delivery attempt. Rationale: clients are internal-only services (per PROJECT.md), so full DNS-rebinding protection (re-resolve + re-check on every delivery) is treated as an accepted residual risk for v1, not required.

### Delivery Mechanism & Retry
- **D-04:** New asynq task type/queue (e.g. `webhook:deliver` on queue `webhook`), handled by the **same process/binary** as today (`cmd/worker`), registered in the same `asynq.ServeMux` with a weighted multi-queue `asynq.Server` config (`image` + `webhook` queues, own weights) — mirrors the existing "engine-class queue routing" pattern (`internal/queue/queue.go`, `cmd/worker/main.go`). Enqueued right after `MarkDone`/`MarkFailed` in `internal/worker/worker.go`, carrying only `job_id` (payload stays minimal, details re-read from Postgres — same pattern as `ConvertPayload`).
  - **Future scaling path (noted, not built now):** splitting webhook delivery into its own `cmd/webhook-worker` binary later is a cheap, non-breaking migration — just remove the `webhook` queue from `cmd/worker`'s config and stand up a second binary listening on the same Redis queue. No schema or payload changes needed.
- **D-05:** Retry via asynq `MaxRetry` + a custom `RetryDelayFunc` (exponential backoff + jitter). Conservative v1 numbers: `MaxRetry=6`, backoff schedule approx. `30s → 1m → 2m → 4m → 8m → 15m` (total window ≈ 30 min).
- **D-06 (explicitly deferred, not built):** A circuit-breaker pattern for webhook delivery (short-circuit further attempts to a known-dead `callback_url`) was discussed and rejected for v1 — asynq's per-task bounded retry already prevents retry storms at current internal-only scale, and a real circuit breaker needs cross-replica shared state (not justified without observed flaky-endpoint data; revisit after Phase 4 `OBS-01` metrics land).

### Payload & Success Criteria
- **D-07:** Minimal payload: `job_id`, `status` (`done`/`failed`), `download_url` (presigned, only on `done`), `error_code`/`error_message` (only on `failed`). Success = HTTP `2xx` response within the per-attempt timeout. Any other status code, timeout, or network error triggers a retry per D-05.
- **D-08:** Per-attempt HTTP request timeout: **10 seconds** (industry-standard compromise, independent of the asynq inter-attempt backoff in D-05).
- **D-09 (presigned URL lifetime):** The presigned `download_url` embedded in the webhook payload must **not** be generated once and reused across retries — regenerate a fresh presigned URL at the time of each actual delivery attempt (`internal/storage.PresignGet`), and use a TTL generous enough to comfortably exceed the max retry window from D-05 (e.g. on the order of hours, not minutes). Rationale: without this, a client that's down for the full retry window (or investigated later via dead-letter) would receive/find an already-expired link even though delivery "succeeded" from the service's perspective.

### Dead-Letter
- **D-10:** New `dead_letter boolean NOT NULL DEFAULT false` column on `webhook_deliveries` (new migration, next after `0002_client_api_keys.sql`). Set to `true` on the row for the final attempt once asynq exhausts `MaxRetry` (D-05). Operators investigate dead-lettered deliveries via **direct SQL** in v1 — no CLI/API tooling. A manual-replay CLI/endpoint (`WEBHOOK-V2-02`) is confirmed as the v2 follow-up for this exact gap (user's stated scenario: client unreachable for hours, past the ~30 min retry window).

### Claude's Discretion
- Exact migration file name/number for the `dead_letter` column and any other schema additions (planner/executor to decide, following the existing `NNNN_description.sql` convention).
- Exact presigned-URL TTL value for D-09 (must exceed the ~30 min retry window with comfortable margin; e.g. hours-scale) — not pinned to a specific number by the user.
- Env var naming beyond `WEBHOOK_SIGNING_SECRET` (e.g. exact name for any new webhook-timeout/queue-weight tunables), following the existing `os.Getenv`-only convention.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Project & Requirements
- `.planning/PROJECT.md` — Core value, internal-clients-only scope (informs D-03's SSRF risk acceptance)
- `.planning/REQUIREMENTS.md` — `WEBHOOK-01..05` (locked v1 scope for this phase), `WEBHOOK-V2-01` (per-client secret rotation, deferred — see D-01), `WEBHOOK-V2-02` (manual replay tool, deferred — see D-10)
- `.planning/ROADMAP.md` — Phase 2 goal, success criteria, dependency on Phase 1 (`client_id`/`callback_url` attribution)

### Prior Phase Context (patterns carried forward)
- `.planning/phases/01-merge-auth-rate-limiting/01-CONTEXT.md` — auth/env-var-config conventions this phase must follow (D-01, D-05 tunables)

### Existing Schema (source of truth for what already exists)
- `internal/db/migrations/0001_init.sql` — `jobs.callback_url` (line 57, already exists, not yet API-exposed — see D-02), `webhook_deliveries` table definition (lines 114-127: `id`, `job_id`, `url`, `attempt`, `status_code`, `delivered`, timestamps — needs the new `dead_letter` column per D-10)
- `internal/db/migrations/0002_client_api_keys.sql` — most recent migration; the new `dead_letter` migration should follow immediately after this one, numerically

### Existing Codebase (reference patterns to follow)
- `internal/queue/queue.go` — `TypeImageConvert`/`QueueImage` const pattern to mirror for the new webhook task type/queue (D-04)
- `internal/worker/worker.go` — `Handler` struct + `HandleImageConvert` method pattern to mirror for the webhook delivery handler; this is also where the new task gets enqueued (right after `MarkDone`/`MarkFailed`)
- `internal/storage/storage.go` — `PresignGet` to reuse for regenerating fresh presigned URLs per delivery attempt (D-09)
- `internal/jobs/repo.go` — Postgres-first / guarded-transition pattern to mirror for a new `webhook_deliveries` repo (insert delivery-attempt rows transactionally)
- `cmd/worker/main.go` — where the new `webhook` queue gets added to the `asynq.Server` multi-queue config with a weight (D-04)
- `README.md` (Аутентификация / presigned URL caveat sections) — existing documented caveat about presigned URLs and internal `minio:9000` vs `localhost:9100`, relevant background for D-09

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `internal/queue/queue.go` — task-type/queue-name const pattern (`TypeImageConvert`, `QueueImage`) to extend with `TypeWebhookDeliver`/`QueueWebhook`
- `internal/worker/worker.go` `Handler` — struct-with-narrow-deps + `Handle<Noun>` method naming convention to mirror for the webhook delivery handler
- `internal/storage/storage.go` `PresignGet` — reused directly for D-09 (fresh URL per attempt)
- `internal/jobs/repo.go` `Repo.transition` / `pgx.BeginFunc` pattern — model for a new `webhook_deliveries` repo tracking attempts

### Established Patterns
- Engine-class queue routing (`internal/queue/queue.go`) — extends naturally to a `webhook` queue per D-04
- Postgres-first double write (job row before enqueue) — same discipline should apply to writing a `webhook_deliveries` row before/alongside each delivery attempt
- Environment-variable-only configuration (`os.Getenv`) — `WEBHOOK_SIGNING_SECRET` and any new tunables (timeout, TTL, retry count) follow this, no config file

### Integration Points
- `internal/api/handlers.go` — `POST /v1/jobs` handler must start accepting and validating `callback_url` (currently not read from the request at all) — this is where D-02/D-03 validation happens
- `internal/worker/worker.go` — after `MarkDone`/`MarkFailed`, enqueue the new `webhook:deliver` task (D-04)
- `cmd/worker/main.go` — register the new handler on the `asynq.ServeMux`, add the `webhook` queue with a weight to the `asynq.Server` config
- `internal/db/migrations/` — new migration adding `webhook_deliveries.dead_letter` (D-10)

</code_context>

<specifics>
## Specific Ideas

No UI/UX references — this is a backend-only phase. All concrete asks are captured as decisions above: minimal payload shape, 2xx-only success, 10s per-attempt timeout, 6-retry/~30min backoff window, fresh presigned URL per attempt with generous TTL, dead-letter via a boolean column investigated by direct SQL in v1.

</specifics>

<deferred>
## Deferred Ideas

- **Hybrid callback_url model** (client-level default `callback_url` + per-job override) — v2 backlog. Rationale: per-job alone is sufficient now (matches existing schema); no signal yet that clients repeat the same URL often enough to justify a default layer. Revisit if real usage shows most jobs from a client share one URL.
- **Circuit breaker for webhook delivery** (per client/`callback_url`, short-circuiting attempts to a known-dead endpoint) — v2 backlog. Rationale: asynq's per-task bounded retry already prevents retry storms at current scale; a real circuit breaker needs shared cross-replica state, not justified without observed flaky-endpoint data. Revisit once Phase 4 `OBS-01` metrics show a pattern.
- **Manual replay tool for failed/dead-lettered deliveries** — already tracked as `WEBHOOK-V2-02` in REQUIREMENTS.md v2; user's stated motivating scenario (client unreachable for several hours, past the ~30 min v1 retry window) confirmed as the exact rationale for that v2 item.
- **Direct client read access to the S3/MinIO bucket** (client gets scoped bucket-read credentials/policy, service hands back an object key/path instead of a presigned URL) — explicitly out of scope for this phase. This would introduce a new trust boundary and credential system (separate from API-key auth) and requires client-scoped object-key namespacing; it's a storage-access-model decision deserving its own phase/ADR, not an implementation detail of webhook delivery. The narrower in-scope problem it was trying to solve (presigned URL expiring before a recovering client can fetch it) is solved instead by D-09 (fresh URL per attempt + generous TTL).

</deferred>

---

*Phase: 2-Webhook Delivery*
*Context gathered: 2026-07-04*
