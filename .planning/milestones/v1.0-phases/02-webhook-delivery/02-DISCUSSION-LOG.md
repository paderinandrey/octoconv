# Phase 2: Webhook Delivery - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-07-04
**Phase:** 2-Webhook Delivery
**Areas discussed:** HMAC-секрет для подписи, SSRF-защита callback_url, Механизм доставки и retry, Payload и критерии успеха, Dead-letter

---

## HMAC-секрет для подписи

| Option | Description | Selected |
|--------|-------------|----------|
| Один общий секрет (env var) | WEBHOOK_SIGNING_SECRET shared across all clients; per-client rotation deferred to v2 | ✓ |
| Секрет на клиента (новая колонка) | New column on `clients`, generated + printed once via `manage-clients`, no rotation in v1 | |

**User's choice:** Один общий секрет (env var)
**Notes:** Matches env-var-only config convention; per-client rotation is already `WEBHOOK-V2-01` in v2 backlog.

---

## callback_url — scope

| Option | Description | Selected |
|--------|-------------|----------|
| Per-job, полная защита (SSRF) | callback_url in POST /v1/jobs body; scheme+resolve+re-check before every delivery | |
| Per-job, базовая защита (SSRF) | callback_url in POST /v1/jobs body; validate once at creation only | ✓ (combined with SSRF answer below) |
| Другая модель | User flagged: is callback_url per-request or fixed on `clients`? | (led to clarification round) |

**User's choice:** Per-job (matches existing `jobs.callback_url` schema column). User asked whether fixing it on `clients` might be simpler; also proposed a hybrid (client default + per-job override).
**Notes:** Claude recommended per-job as primary (matches existing schema, more flexible, no new migration/CLI needed) while acknowledging the hybrid's convenience benefit isn't demonstrated yet. User agreed to record hybrid as a v2 deferred idea rather than build it now.

---

## SSRF-защита callback_url (strictness)

| Option | Description | Selected |
|--------|-------------|----------|
| Полная защита: схема + resolve + re-check | Validate at job creation AND before every delivery (DNS-rebinding protection) | |
| Базовая защита: только при создании job'а | Validate scheme + block loopback/private/link-local/metadata ranges once, at creation only | ✓ |

**User's choice:** Базовая защита: только при создании job'а
**Notes:** Internal-only clients (per PROJECT.md) make full DNS-rebinding protection lower priority for v1; residual risk explicitly accepted.

---

## Механизм доставки и retry — where the handler runs

| Option | Description | Selected |
|--------|-------------|----------|
| Та же очередь (image), тот же воркер | Reuse existing queue/worker entirely | |
| Новая очередь (webhook), тот же процесс/бинарь | New asynq queue `webhook`, same `cmd/worker` binary, weighted multi-queue server | ✓ |
| Отдельный бинарь/процесс | New `cmd/webhook-worker` binary, independent deployment | |

**User's choice:** Новая очередь (webhook), тот же процесс/бинарь
**Notes:** User asked whether they could migrate to a separate binary later under high load. Claude confirmed: asynq's queue mechanism is Redis-backed and decoupled from which binary consumes it, so splitting into `cmd/webhook-worker` later is a cheap, non-breaking config change (no schema/payload changes needed).

---

## Механизм доставки и retry — retry parameters

| Option | Description | Selected |
|--------|-------------|----------|
| Консервативные: ~6 попыток, до ~30 мин | MaxRetry=6, backoff ~30s→15min with jitter | ✓ |
| Шире: ~10 попыток, до нескольких часов | MaxRetry=10, longer backoff cap | |
| Claude's Discretion | Defer numbers to planning, as was done for rate-limit numbers in Phase 1 | |

**User's choice:** Консервативные: ~6 попыток, до ~30 мин
**Notes:** User asked about adding a circuit-breaker pattern here. Claude recommended against building one in v1 — asynq's per-task bounded retry already prevents retry storms at current scale; a real circuit breaker needs cross-replica shared state, not justified without observed flaky-endpoint data. User agreed to defer circuit breaker to v2 backlog (revisit after Phase 4 OBS-01 metrics).

---

## Payload и критерии успеха

| Option | Description | Selected |
|--------|-------------|----------|
| Минимальный payload + только 2xx = успех | job_id, status, download_url (done)/error_code+error_message (failed); success = 2xx | ✓ |
| Расширенный payload (+timestamps, +job metadata) | Adds created_at/finished_at, source/target format, client_id | |

**User's choice:** Минимальный payload + только 2xx = успех

---

## Payload — delivery HTTP timeout

| Option | Description | Selected |
|--------|-------------|----------|
| 5 секунд | Aggressive timeout | |
| 10 секунд (рекомендую) | Standard compromise (Stripe/GitHub-like) | ✓ |
| 30 секунд | More slack for slow internal services | |

**User's choice:** 10 секунд

---

## Follow-up: manual replay for undelivered webhooks

**User's question:** Could there also be an endpoint to ask for redelivery of undelivered events, if the client was unreachable for several hours?
**Claude's response:** This is already tracked as `WEBHOOK-V2-02` ("Инструмент ручного replay для неудавшихся доставок вебхуков") in `.planning/REQUIREMENTS.md` v2 section — confirmed the user's scenario is exactly that item's motivation; no new decision needed, just confirmed alignment.

---

## Dead-letter representation

| Option | Description | Selected |
|--------|-------------|----------|
| Новая колонка dead_letter (bool) + прямой SQL | New migration adds `dead_letter boolean`; set true when asynq exhausts MaxRetry; operator queries via direct SQL in v1 | ✓ |
| Использовать asynq's Archived tasks + delivered=false | No new column; rely on asynq's own archived-task state (visible via asynqmon in Phase 4) plus `delivered=false` | |

**User's choice:** Новая колонка dead_letter (bool) + прямой SQL

---

## Follow-up: direct client access to S3/MinIO bucket

**User's question:** Instead of a time-limited presigned URL, could clients be granted read access to the bucket directly (service returns an object path instead of a presigned URL)?
**Claude's response:** Recommended against it for this phase — it's a new trust boundary and credential system (separate from API-key auth), requires client-scoped object-key namespacing, and is a storage-access-model decision deserving its own phase/ADR, not an implementation detail of webhook delivery. The narrower underlying problem (presigned URL expiring before a client recovers from an outage) is solved instead by regenerating the presigned URL fresh on every delivery attempt with a generous TTL (D-09).
**User's choice:** Agreed — direct bucket access noted as a deferred idea outside this phase; adopted the fresh-URL-per-attempt + generous TTL fix instead.

---

## Claude's Discretion

- Exact migration file name/number for the `dead_letter` column (follows existing `NNNN_description.sql` convention, next after `0002_client_api_keys.sql`)
- Exact presigned-URL TTL value (must comfortably exceed the ~30 min retry window — hours-scale, not pinned to a specific number)
- Env var naming for any new tunables beyond `WEBHOOK_SIGNING_SECRET`

## Deferred Ideas

- Hybrid callback_url model (client-level default + per-job override) — v2 backlog
- Circuit breaker for webhook delivery per client/callback_url — v2 backlog
- Manual replay tool for failed/dead-lettered deliveries — confirmed as existing `WEBHOOK-V2-02`
- Direct client read access to the S3/MinIO bucket (bypassing presigned URLs) — deferred, out of scope for this phase; needs its own phase/ADR
