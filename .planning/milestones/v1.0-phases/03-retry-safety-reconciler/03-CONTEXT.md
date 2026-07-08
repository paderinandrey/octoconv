# Phase 3: Retry-Safety & Reconciler - Context

**Gathered:** 2026-07-05
**Status:** Ready for planning

<domain>
## Phase Boundary

The worker correctly distinguishes transient from terminal conversion failures so asynq retry actually functions (currently every job effectively gets exactly one real attempt — a confirmed bug, see `<canonical_refs>`), and jobs stranded by infrastructure hiccups (lost enqueue, crashed worker) are automatically recovered without duplicating work. This phase covers: transient/terminal error classification in `HandleImageConvert`, an image-conversion-specific retry budget/backoff, and a reconciler that sweeps `queued`/`active` jobs past staleness thresholds. It does NOT cover: webhooks (Phase 2, done), content validation, storage lifecycle, or observability/alerting infrastructure (Phase 4) — reconciler actions are logged to `job_events` only, no new logging/metrics in this phase.

</domain>

<decisions>
## Implementation Decisions

### Классификация ошибок: transient vs terminal
- **D-01:** Широкий retry-подход. Terminal — только явно постоянные проблемы: нет конвертера для пары форматов (`registry.Lookup` miss), движок явно сигнализирует "неверный формат/повреждённый файл". Всё остальное (сеть, S3/MinIO, Postgres, таймаут движка) — transient, ведёт к retry.
- **D-02:** Ошибки storage (download/upload) различаются по типу: явное "не найдено" (NoSuchKey/404) = terminal (входа физически нет, повтор бессмыслен); timeout/connection reset = transient.
- **D-03:** Ошибка записи в Postgres ПОСЛЕ успешной конвертации (файл уже в S3, но `AddOutput`/`MarkDone` не прошли) = transient — повторяется вся задача целиком (движок идемпотентно перезапишет output в тот же ключ, повторный запуск безопасен).
- **D-04:** Таймаут движка (`ENGINE_TIMEOUT=120s`) = transient, но с ограниченным числом попыток — не terminal сразу, но и не бесконечно (см. D-07: общий бюджет с остальными transient-ошибками).

### Бюджет повторов и backoff для конвертации
- **D-05:** `MaxRetry` для image-конвертации — небольшой (3-5 попыток), меньше чем у webhook (`MaxRetry=6`).
- **D-06:** Backoff — быстрый график в секундах (например, 2с→5с→15с), НЕ наследовать текущий (случайно унаследованный) график webhook (30с→15мин). **Важно:** `asynq.Config.RetryDelayFunc` общий на весь сервер (`cmd/worker/main.go:72`), поэтому image-очередь сейчас незаметно использует `WebhookRetryDelay`. Планировщик/исполнитель должен ввести различение по типу задачи (`task.Type()`) внутри одной серверной функции, либо иной механизм, чтобы у image и webhook были разные расписания.
- **D-07:** Повторы при таймауте движка используют тот же общий бюджет/расписание, что и остальные transient-ошибки — отдельной, более строгой логики для таймаута не нужно.

### Пороги зависания для reconciler'а
- **D-08:** Порог для `queued` (потерянный enqueue) — короткий, 1-2 минуты.
- **D-09:** Порог для `active` (воркер упал) — с запасом над `ENGINE_TIMEOUT`, примерно 5 минут.
- **D-10:** Интервал sweep reconciler'а — часто, раз в минуту.
- **D-11:** При обнаружении нескольких зависших задач одновременно (например, после долгого простоя воркера) — обрабатывать все сразу батчем, не искусственно ограничивать; обычная конкуррентность воркера (`WORKER_CONCURRENCY`) сама сглаживает нагрузку.

### Бюджет восстановления reconciler'а и итоговый статус
- **D-12:** Лимит на число восстановлений одной и той же задачи reconciler'ом — да, ограничить (например, 3 восстановления), чтобы постоянно ломающаяся задача не зацикливалась навечно.
- **D-13:** После исчерпания лимита восстановлений задача помечается обычным статусом `failed` с собственным `error_code` (например, `reconciler_exhausted`) — никакого нового статуса в state machine (`queued/active/done/failed`) не вводится.
- **D-14:** Reconciler-terminal-failed задача должна триггерить webhook (если задан `callback_url`) так же, как любой другой `failed` — согласуется с контрактом Phase 2 (любой `done`/`failed` даёт вебхук, без исключений).
- **D-15:** Видимость действий reconciler'а ограничена `job_events` (уже есть колонка `detail jsonb` — миграция не нужна) — отдельное логирование/алертинг сверх этого явно отложено на Phase 4 (`OBS-01..03`).

### Claude's Discretion
- Точный механизм периодического запуска reconciler'а (asynq periodic task vs отдельная горутина/тикер внутри `cmd/worker`) — техническая деталь, не обсуждалась.
- Точный набор terminal-кодов ошибок движка сверх "нет конвертера для пары" и явного bad-format сигнала — планировщик/исполнитель определит на основе фактических кодов возврата `vips` и поведения `os/exec`.
- Имена новых env var'ов для порогов reconciler'а (staleness thresholds, sweep interval) и MaxRetry/backoff-констант для image-очереди — следуя существующей конвенции только-env-var конфигурации (`os.Getenv`, без файла конфига).
- Точный механизм различения очередей внутри общего `RetryDelayFunc` (диспетчеризация по `task.Type()`, отдельная обёртка и т.п.) — реализационная деталь.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Project & Requirements
- `.planning/PROJECT.md` — Core value, Key Decisions table (hardening milestone status: Phase 1/2 закрыты, Phase 3 следующая)
- `.planning/REQUIREMENTS.md` — `RELY-01`, `RELY-02`, `RECON-01`, `RECON-02`, `RECON-03` (locked v1 scope for this phase); `RELY-V2-01` (idempotency key, deferred v2)
- `.planning/ROADMAP.md` — Phase 3 goal, success criteria; explicit sequencing note: retry-safety must land before reconciler work within this phase (a reconciler on the current single-attempt state machine would duplicate processing)

### Diagnosed Problem (source of truth for the bug this phase fixes)
- `.planning/codebase/CONCERNS.md` — section "Single-attempt processing despite retry-capable queue" already diagnoses the exact bug (`internal/worker/worker.go:40-63`, `internal/jobs/repo.go:81-106`): `MarkFailed` fires unconditionally on any `process()` error, so a retried task's `MarkActive` call always fails (job already `failed`), wrapped in `asynq.SkipRetry` — every job gets exactly one real attempt regardless of asynq's retry config.

### Existing Codebase (reference patterns to follow)
- `internal/worker/worker.go` — `HandleImageConvert` (current unconditional-`MarkFailed` logic to replace with transient/terminal classification); `HandleWebhookDeliver` is already a correct example of the "unwrap error → let asynq's retry apply" pattern to mirror
- `internal/jobs/repo.go` — guarded-transition pattern (`Repo.transition`, `MarkActive`/`MarkDone`/`MarkFailed`) — the reconciler's recovery actions should reuse this same row-locked, event-logged transaction discipline, not ad-hoc `UPDATE`s
- `internal/queue/queue.go` — `WebhookRetryDelay` (exponential + jitter backoff, D-05 pattern from Phase 2) as the template for the new image-specific `RetryDelayFunc`. **Confirmed defect to fix:** `RetryDelayFunc` is server-wide in `asynq.Config` (`cmd/worker/main.go:72`), so the image queue currently silently inherits `WebhookRetryDelay`'s 30s→15m schedule instead of its own.
- `cmd/worker/main.go` — where `asynq.Config{RetryDelayFunc: ...}` and the multi-queue setup (`image:2`, `webhook:1` weights) are registered; this is where a queue-aware retry-delay dispatch needs to land
- `internal/db/migrations/0001_init.sql` — `jobs` table (statuses `queued`/`active`/`done`/`failed`), `job_events` table (`from_status`, `to_status`, `detail jsonb`) — existing schema already supports reconciler event logging with no new migration needed

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `Repo.transition` (guarded, row-locked via `SELECT ... FOR UPDATE`, event-logging in one transaction) — the model for the reconciler's recovery action; reuse this existing mechanism rather than inventing a new one
- `job_events.detail jsonb` — already able to hold structured reconciler-action details (e.g. recovered-from-status, attempt count) with no schema migration
- `webhook.SignPayload` / `WebhookRetryDelay` (Phase 2) — concrete backoff+jitter code pattern to adapt for the new image-specific schedule

### Established Patterns
- Engine-class queue routing (`image`/`webhook` separate queues with weights) — reconciler work doesn't change this architecture, only fixes retry semantics within it
- Postgres-first, guarded transitions — the reconciler must follow the same disciplined approach (row lock + event log in one transaction), not ad-hoc updates

### Integration Points
- `internal/worker/worker.go` `HandleImageConvert` — where transient/terminal classification is introduced
- `internal/queue/queue.go` + `cmd/worker/main.go` — where the new `MaxRetry`/`RetryDelayFunc` for the image queue is defined and wired to be queue-aware
- New reconciler component (likely `internal/reconciler/` or similar — planner/researcher to decide) — scans `jobs` against the staleness thresholds (D-08/D-09), re-enqueues via `queue.Client`, and uses a `Repo.transition`-like pattern for `job_events` writes

</code_context>

<specifics>
## Specific Ideas

No UI/UX references — this is a backend-only infrastructure phase. All concrete asks are captured as decisions above: broad transient/terminal classification, small bounded retry budget with a fast seconds-scale backoff for image conversion, short queued-staleness / ~5min active-staleness thresholds, frequent (1-minute) sweep with unbounded batch recovery, and a capped (3) reconciler-recovery budget ending in a normal `failed` status with a distinct `error_code` that still fires webhooks.

</specifics>

<deferred>
## Deferred Ideas

- **Dedicated logging/alerting for reconciler actions beyond `job_events`** — explicitly deferred to Phase 4 (`OBS-01..03`).
- **Rate-limiting/staged processing when the reconciler recovers many stuck jobs in one sweep** — explicitly decided against (D-11) in favor of relying on existing worker concurrency; revisit if concurrency proves an insufficient safeguard in practice.
- **A distinct state-machine status for "reconciler exhausted"** — explicitly rejected (D-13) in favor of the existing `failed` status + a distinct `error_code`; revisit only if a real need emerges to distinguish this case at the API level.

</deferred>

---

*Phase: 3-Retry-Safety & Reconciler*
*Context gathered: 2026-07-05*
