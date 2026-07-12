# Phase 3: Retry-Safety & Reconciler - Research

**Researched:** 2026-07-05
**Domain:** asynq (Go task queue) retry semantics, Postgres-backed job state machines, reconciliation/sweeper patterns
**Confidence:** HIGH

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

#### Классификация ошибок: transient vs terminal
- **D-01:** Широкий retry-подход. Terminal — только явно постоянные проблемы: нет конвертера для пары форматов (`registry.Lookup` miss), движок явно сигнализирует "неверный формат/повреждённый файл". Всё остальное (сеть, S3/MinIO, Postgres, таймаут движка) — transient, ведёт к retry.
- **D-02:** Ошибки storage (download/upload) различаются по типу: явное "не найдено" (NoSuchKey/404) = terminal (входа физически нет, повтор бессмыслен); timeout/connection reset = transient.
- **D-03:** Ошибка записи в Postgres ПОСЛЕ успешной конвертации (файл уже в S3, но `AddOutput`/`MarkDone` не прошли) = transient — повторяется вся задача целиком (движок идемпотентно перезапишет output в тот же ключ, повторный запуск безопасен).
- **D-04:** Таймаут движка (`ENGINE_TIMEOUT=120s`) = transient, но с ограниченным числом попыток — не terminal сразу, но и не бесконечно (см. D-07: общий бюджет с остальными transient-ошибками).

#### Бюджет повторов и backoff для конвертации
- **D-05:** `MaxRetry` для image-конвертации — небольшой (3-5 попыток), меньше чем у webhook (`MaxRetry=6`).
- **D-06:** Backoff — быстрый график в секундах (например, 2с→5с→15с), НЕ наследовать текущий (случайно унаследованный) график webhook (30с→15мин). **Важно:** `asynq.Config.RetryDelayFunc` общий на весь сервер (`cmd/worker/main.go:72`), поэтому image-очередь сейчас незаметно использует `WebhookRetryDelay`. Планировщик/исполнитель должен ввести различение по типу задачи (`task.Type()`) внутри одной серверной функции, либо иной механизм, чтобы у image и webhook были разные расписания.
- **D-07:** Повторы при таймауте движка используют тот же общий бюджет/расписание, что и остальные transient-ошибки — отдельной, более строгой логики для таймаута не нужно.

#### Пороги зависания для reconciler'а
- **D-08:** Порог для `queued` (потерянный enqueue) — короткий, 1-2 минуты.
- **D-09:** Порог для `active` (воркер упал) — с запасом над `ENGINE_TIMEOUT`, примерно 5 минут.
- **D-10:** Интервал sweep reconciler'а — часто, раз в минуту.
- **D-11:** При обнаружении нескольких зависших задач одновременно (например, после долгого простоя воркера) — обрабатывать все сразу батчем, не искусственно ограничивать; обычная конкуррентность воркера (`WORKER_CONCURRENCY`) сама сглаживает нагрузку.

#### Бюджет восстановления reconciler'а и итоговый статус
- **D-12:** Лимит на число восстановлений одной и той же задачи reconciler'ом — да, ограничить (например, 3 восстановления), чтобы постоянно ломающаяся задача не зацикливалась навечно.
- **D-13:** После исчерпания лимита восстановлений задача помечается обычным статусом `failed` с собственным `error_code` (например, `reconciler_exhausted`) — никакого нового статуса в state machine (`queued/active/done/failed`) не вводится.
- **D-14:** Reconciler-terminal-failed задача должна триггерить webhook (если задан `callback_url`) так же, как любой другой `failed` — согласуется с контрактом Phase 2 (любой `done`/`failed` даёт вебхук, без исключений).
- **D-15:** Видимость действий reconciler'а ограничена `job_events` (уже есть колонка `detail jsonb` — миграция не нужна) — отдельное логирование/алертинг сверх этого явно отложено на Phase 4 (`OBS-01..03`).

### Claude's Discretion
- Точный механизм периодического запуска reconciler'а (asynq periodic task vs отдельная горутина/тикер внутри `cmd/worker`) — техническая деталь, не обсуждалась.
- Точный набор terminal-кодов ошибок движка сверх "нет конвертера для пары" и явного bad-format сигнала — планировщик/исполнитель определит на основе фактических кодов возврата `vips` и поведения `os/exec`.
- Имена новых env var'ов для порогов reconciler'а (staleness thresholds, sweep interval) и MaxRetry/backoff-констант для image-очереди — следуя существующей конвенции только-env-var конфигурации (`os.Getenv`, без файла конфига).
- Точный механизм различения очередей внутри общего `RetryDelayFunc` (диспетчеризация по `task.Type()`, отдельная обёртка и т.п.) — реализационная деталь.

### Deferred Ideas (OUT OF SCOPE)
- **Dedicated logging/alerting for reconciler actions beyond `job_events`** — explicitly deferred to Phase 4 (`OBS-01..03`).
- **Rate-limiting/staged processing when the reconciler recovers many stuck jobs in one sweep** — explicitly decided against (D-11) in favor of relying on existing worker concurrency; revisit if concurrency proves an insufficient safeguard in practice.
- **A distinct state-machine status for "reconciler exhausted"** — explicitly rejected (D-13) in favor of the existing `failed` status + a distinct `error_code`; revisit only if a real need emerges to distinguish this case at the API level.
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-------------------|
| RELY-01 | Воркер различает transient-ошибки (сетевые/таймауты) и terminal-ошибки (невалидный вход, неподдерживаемый формат) при сбое конвертации | See "Pattern 2: Terminal vs transient classification" and verified vips stderr signatures under Code Examples; `minio.ToErrorResponse` for storage-404 detection |
| RELY-02 | При transient-ошибке job не помечается terminal-failed — retry средствами asynq реально происходит | See "Critical Architectural Insight", Pattern 1 (idempotent `MarkActive` re-entry), Pattern 3/4 (queue-aware `RetryDelayFunc` + per-task `MaxRetry`) — these three changes together are what make asynq's own retry mechanism actually take effect |
| RECON-01 | Периодический reconciler находит задачи, зависшие в `queued` без соответствующей задачи в очереди, и переставляет их в очередь (идемпотентно, без дублей) | See Pattern 5 (ticker-driven sweeper) and Pattern 6 (`RequeueStale` guarded transition covering the `queued` case); Open Question 3 / Alternatives Considered explain why Postgres-timestamp-only (not asynq Inspector) is the correct, idempotent-by-construction approach |
| RECON-02 | Reconciler находит задачи, зависшие в `active` дольше порога (воркер упал), и не дублирует обработку легитимно медленной задачи — только реально зависшие | See Pattern 6 (`RequeueStale` covering the `active` case), Pitfall 2 (`started_at` must not reset on retry) and Pitfall 4 (residual race with a legitimately-slow worker, documented as an accepted MVP limitation) |
| RECON-03 | Действия reconciler'а (восстановленные, terminal-failed задачи) фиксируются в `job_events` | See Pattern 6 (`RecoveryCount` query against `job_events.detail`), Pitfall 5 (consistent `detail` tagging required for the cap to work), Open Question 2 (`transition()` needs an optional `detail` parameter) |
</phase_requirements>

## Summary

This phase fixes a confirmed bug (`CONCERNS.md`, verified against source in this session): `HandleImageConvert` calls `MarkActive` (guarded `queued -> active` only) unconditionally, then unconditionally calls `MarkFailed` on any `process()` error. Because `MarkFailed` is a terminal transition, any subsequent asynq-internal retry of the *same task* fails at the `MarkActive` step (job is already `failed`), and that failure is wrapped in `asynq.SkipRetry` — so every job gets exactly one real attempt no matter what `asynq.Config.RetryDelayFunc`/`MaxRetry` say. This was verified directly by reading `asynq@v0.26.0` source: `processor.handleFailedMessage` only re-invokes the handler for the *same* task when the returned error is a plain (non-`SkipRetry`) error and `msg.Retried < msg.Retry` — asynq's retry model works by re-calling the handler for the same task/message, not by re-enqueueing a new one. This has a direct consequence the planner must design around (see "Critical Architectural Insight" below): once transient-failure handling is fixed, jobs can now get durably stuck in `active` in Postgres forever (asynq exhausts its own small retry budget and silently archives the task in Redis, but never calls the handler again to flip Postgres status) — this is precisely why RECON-02 must exist as the backstop, not an optional nice-to-have.

All library-level claims in this document (asynq v0.26.0 API surface, minio-go v7.2.1 error types, vips CLI exit-code/stderr behavior) were verified directly against installed module source (`$(go env GOMODCACHE)/github.com/hibiken/asynq@v0.26.0`) or a live `debian:bookworm-slim` + `libvips-tools` container matching `Dockerfile.worker` exactly — not training-data recall. No new external packages are required for this phase; everything is achievable with the already-vendored `asynq`, `pgx`, and `minio-go` APIs.

**Primary recommendation:** (1) Make `Repo.MarkActive` idempotent for `active -> active` so asynq's same-task internal retries don't error out at the top of the handler; (2) on `process()` failure, classify the error via a small terminal-detector function and only call `MarkFailed` (+`SkipRetry`) for terminal errors — for transient errors, return the raw error unwrapped and leave the job `active`; (3) give the image queue its own `MaxRetry` (task option) and its own backoff schedule (dispatch inside a single `RetryDelayFunc` on `t.Type()`); (4) add a new `internal/reconciler` package driven by a `time.Ticker` goroutine wired explicitly into `cmd/worker/main.go` (switching `srv.Run` to `srv.Start`/`srv.Shutdown` to allow coordinated shutdown, mirroring `cmd/api/main.go`'s existing `signal.NotifyContext` pattern) that scans Postgres (not Redis/asynq Inspector) for stale `queued`/`active` jobs, requeues them through a new guarded `active|queued -> queued` transition, and terminally fails jobs that exceed a reconciler-recovery cap by counting prior recovery events in `job_events`.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Transient/terminal error classification | API/Backend (worker process) | — | Pure Go logic inside `internal/worker`; no I/O boundary of its own |
| Image-queue retry budget & backoff | API/Backend (queue producer + asynq server config) | — | `MaxRetry` is set at enqueue time (`internal/queue`); `RetryDelayFunc` is asynq server config (`cmd/worker/main.go`) |
| Reconciler sweep | API/Backend (new `internal/reconciler` package, run inside the worker process) | Database/Storage (Postgres queries) | Reads/writes Postgres as system of record; Postgres, not Redis, decides what's stale |
| Requeue / recovery transition | Database/Storage (guarded transition in `internal/jobs`) | API/Backend (asynq enqueue call) | State change must be atomic+locked in Postgres before a new task is dispatched to Redis |
| Reconciler action audit trail | Database/Storage (`job_events`) | — | Already-existing table/column (`detail jsonb`); no new tier introduced |

This phase touches zero HTTP/browser/CDN surface — it is entirely a backend-worker-process and database concern.

## Standard Stack

### Core
No new libraries. This phase is implemented entirely with libraries already in `go.mod` (verified versions below — unchanged from current `go.mod`, confirmed still current in the local module cache, no upgrade needed):

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/hibiken/asynq` | v0.26.0 [VERIFIED: local module cache, `go.mod:9`] | Per-task `MaxRetry`, queue-aware `RetryDelayFunc`, `SkipRetry` sentinel, `GetRetryCount`/`GetMaxRetry` context helpers | Already the project's queue; this phase uses documented public API only, no undocumented behavior |
| `github.com/jackc/pgx/v5` | v5.10.0 [VERIFIED: local module cache, `go.mod:10`] | `pgx.BeginFunc` + `SELECT ... FOR UPDATE` for the new requeue transition and reconciler's stale-job scan | Matches existing `Repo.transition` pattern exactly |
| `github.com/minio/minio-go/v7` | v7.2.1 [VERIFIED: local module cache, `go.mod:11`] | `minio.ToErrorResponse(err).Code == minio.NoSuchKey` for storage-404 terminal detection | Already the project's S3 client |

### Supporting
None needed — no new supporting libraries for this phase.

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Plain `time.Ticker` goroutine for reconciler sweep | `asynq.PeriodicTaskManager` / `asynq.Scheduler` | Rejected — `PeriodicTaskManager` is designed to periodically **enqueue new tasks from a cron-like config** (e.g., "run this task every day at midnight"), not to run custom Go logic that scans Postgres and conditionally acts. Using it here would mean writing a "reconciler-sweep" task type just to get a timer, which is more indirection than a direct ticker goroutine, and still requires the same explicit-wiring-in-`main()` code. [VERIFIED: `asynq@v0.26.0/periodic_task_manager.go`] |
| Postgres staleness-timestamp scan | asynq `Inspector.ListPendingTasks`/`ListActiveTasks` cross-check against Redis queue state | Rejected as primary mechanism — `Inspector` has no "list tasks by job_id" or "find orphaned tasks" API; the only way to cross-check would be listing all pending/active tasks per queue and decoding each payload's `job_id`, an O(n) scan that still needs Postgres as the source of truth for "job status." This also fights CLAUDE.md's explicit "Postgres, not Redis, is system of record" constraint. [VERIFIED: `asynq@v0.26.0/inspector.go`, full method list confirmed, no job-id-indexed lookup exists] |
| Dedicated `jobs.recovery_count` column (new migration) | Count `reconciler_recovery`-tagged rows in existing `job_events.detail jsonb` | Either works. Counting via `job_events` avoids a schema migration (CONTEXT.md canonical_refs signal: "existing schema already supports reconciler event logging with no new migration needed"); a dedicated column would be marginally cheaper per-sweep-iteration but adds migration/trigger overhead for a cap that is checked at most once per stale job per sweep (small N). Recommend `job_events` counting as primary; flag the counter-column as an option only if reconciler query load becomes a real concern in practice. |

**Installation:** None — no new packages.

## Package Legitimacy Audit

Not applicable — this phase introduces zero new external dependencies. All work uses `asynq`, `pgx/v5`, and `minio-go/v7`, already present in `go.mod` and already running in production containers. Skipping the slopcheck/registry-verification gate is correct here (nothing new to verify).

## Architecture Patterns

### System Architecture Diagram

```text
                         ┌─────────────────────────────────────────┐
                         │            cmd/worker/main.go            │
                         │  (asynq.Server + reconciler ticker,      │
                         │   both under one signal.NotifyContext)   │
                         └───────────────┬───────────────────────────┘
                                         │
                 ┌───────────────────────┼────────────────────────────┐
                 │                       │                            │
                 ▼                       │                            ▼
     ┌───────────────────────┐           │                ┌───────────────────────────┐
     │ asynq.Server (image,   │           │                │ reconciler.Sweeper         │
     │ webhook queues)        │           │                │ (time.Ticker, e.g. 1 min)  │
     │                        │           │                └─────────────┬─────────────┘
     │ HandleImageConvert:    │           │                              │
     │  1. parse payload      │           │              1. SELECT jobs WHERE
     │  2. MarkActive          │◄──idempotent for            (status='queued' AND created_at < now()-thresh)
     │     (queued OR active   │  active retries              OR
     │     -> active)           │                          (status='active' AND started_at < now()-thresh)
     │  3. process()            │           │              2. count prior reconciler_recovery
     │     - download (S3)      │           │                 events per job (job_events)
     │     - convert (vips)     │           │              3. if under cap:
     │     - upload (S3)        │           │                   Repo.RequeueStale(active|queued -> queued)
     │     - AddOutput+MarkDone │           │                   + log job_events(detail={action:
     │  4. on error: classify   │           │                     "reconciler_recovery", from_status,...})
     │     terminal vs transient│           │                   + queue.EnqueueImageConvert (new task)
     │       terminal:          │           │                 else (cap exceeded):
     │         MarkFailed        │──────────┼────────────────►   Repo.MarkFailed(code="reconciler_exhausted")
     │         SkipRetry return  │           │                   + log job_events
     │       transient:          │           │                   + if CallbackURL: EnqueueWebhookDeliver
     │         return raw err    │           │
     │         (job stays active,│           │
     │         asynq retries via │           │
     │         its own schedule) │           │
     └────────────┬─────────────┘           │
                  │                          │
                  ▼                          ▼
     ┌─────────────────────┐      ┌─────────────────────────┐
     │ Postgres (jobs,      │      │ Redis (asynq queue/retry │
     │ job_events)          │      │ /archive state — transient,
     │ SYSTEM OF RECORD     │      │ never consulted for job  │
     │                      │      │ status truth)             │
     └─────────────────────┘      └─────────────────────────┘
```

### Recommended Project Structure
```
internal/
├── worker/
│   └── worker.go        # HandleImageConvert: add classifyErr() + idempotent MarkActive re-entry
├── jobs/
│   └── repo.go           # add RequeueStale (active|queued -> queued), reuse MarkFailed for exhaustion
├── queue/
│   └── queue.go          # add ImageRetryDelay, RetryDelayFunc dispatcher (by t.Type()), per-task MaxRetry
└── reconciler/            # NEW package
    ├── reconciler.go      # Sweeper struct, NewSweeper, Run(ctx) ticker loop, sweep() scan+act
    └── reconciler_test.go
cmd/
└── worker/
    └── main.go            # switch srv.Run -> srv.Start/Shutdown; wire signal.NotifyContext; start reconciler goroutine
```

### Pattern 1: Idempotent re-entry for `MarkActive`
**What:** Widen `MarkActive`'s guarded-transition allow-list from `[]string{StatusQueued}` to `[]string{StatusQueued, StatusActive}`.
**When to use:** Required so that when asynq internally retries the *same task* after a transient failure (handler returns a plain error, `msg.Retried < msg.Retry`), the handler's first line (`MarkActive`) does not fail with "illegal transition active -> active."
**Example:**
```go
// Source: existing internal/jobs/repo.go:83-89, pattern extended
func (r *Repo) MarkActive(ctx context.Context, id uuid.UUID) error {
    return r.transition(ctx, id, StatusActive, []string{StatusQueued, StatusActive}, func(ctx context.Context, tx pgx.Tx) error {
        _, err := tx.Exec(ctx,
            `UPDATE jobs SET status = 'active', started_at = COALESCE(started_at, now()), attempts = attempts + 1 WHERE id = $1`, id)
        return err
    })
}
```
Note the `COALESCE(started_at, now())` change: `started_at` must stay pinned to the *first* time the job went active, not reset on every asynq-internal retry — the reconciler's active-staleness check (RECON-02) depends on `started_at` reflecting "how long has this job actually been running," not "how long since the last retry attempt." This is a concrete, non-obvious detail the planner must include as an explicit task.

### Pattern 2: Terminal vs transient classification (D-01/D-02/D-03/D-04)
**What:** A small pure function that inspects an error returned from `process()` and decides `MarkFailed`+`SkipRetry` vs plain-return.
**When to use:** Called once, at the single point in `HandleImageConvert` where `process()` errors are currently handled unconditionally.
**Example:**
```go
// Source: derived from live vips exit-code/stderr testing in this session (see Code Examples)
// and github.com/minio/minio-go/v7 v7.2.1 api-error-response.go (ToErrorResponse)
func isTerminal(err error) bool {
    var noConv noConverterError // sentinel/typed error from registry.Lookup miss
    if errors.As(err, &noConv) {
        return true // D-01: no converter for format pair
    }
    if resp := minio.ToErrorResponse(err); resp.Code == minio.NoSuchKey {
        return true // D-02: storage input genuinely missing, retry is pointless
    }
    msg := strings.ToLower(err.Error())
    for _, sig := range terminalVipsSignatures {
        if strings.Contains(msg, sig) {
            return true // D-01: engine explicitly signals bad/corrupted format
        }
    }
    return false // D-01/D-03/D-04: everything else (network, timeout, Postgres write
                 // failure after successful conversion) is transient by default —
                 // broad-retry philosophy per D-01
}

var terminalVipsSignatures = []string{
    "is not a known file format",     // corrupted / unrecognized input, verified via live vips test
    "premature end of jpeg file",     // truncated/corrupted jpeg, verified via live vips test
    "jpeg datastream contains no image",
}
```
**Important:** vips's own process exit code (0 or 1) does **not** distinguish transient from terminal — both a missing output directory (an environment bug, arguably transient/our-bug) and a corrupted input file (terminal) exit 1. Classification must be done on **stderr substring content**, not exit code alone. See Code Examples for the exact verified strings.

### Pattern 3: Queue-aware `RetryDelayFunc` dispatch (D-06)
**What:** asynq's `RetryDelayFunc` is server-wide (`asynq.Config.RetryDelayFunc`, one function for the whole `asynq.Server`), but `t.Type()` is available inside that function and is a normal string equality check.
**When to use:** Replace the current `RetryDelayFunc: queue.WebhookRetryDelay` (which every task type currently receives, including image tasks — a confirmed defect) with a small dispatcher.
**Example:**
```go
// Source: asynq@v0.26.0 server.go:297 (RetryDelayFunc type: func(n int, e error, t *Task) time.Duration)
//         asynq@v0.26.0 asynq.go:40   (func (t *Task) Type() string)
func RetryDelayFunc(n int, e error, t *asynq.Task) time.Duration {
    switch t.Type() {
    case TypeImageConvert:
        return ImageRetryDelay(n, e, t)
    case TypeWebhookDeliver:
        return WebhookRetryDelay(n, e, t)
    default:
        return asynq.DefaultRetryDelayFunc(n, e, t)
    }
}

// imageRetrySchedule: fast seconds-scale backoff (D-06), distinct from
// webhookRetrySchedule's 30s->15m schedule.
var imageRetrySchedule = []time.Duration{2 * time.Second, 5 * time.Second, 15 * time.Second}

func ImageRetryDelay(n int, e error, t *asynq.Task) time.Duration {
    idx := n
    if idx < 0 {
        idx = 0
    }
    if idx >= len(imageRetrySchedule) {
        idx = len(imageRetrySchedule) - 1
    }
    return imageRetrySchedule[idx] // jitter optional; webhook's ±25% jitter pattern can be reused if desired
}
```
Then in `cmd/worker/main.go`:
```go
srv := asynq.NewServer(redisOpt, asynq.Config{
    Concurrency:    envInt("WORKER_CONCURRENCY", 4),
    Queues:         map[string]int{queue.QueueImage: 2, queue.QueueWebhook: 1},
    RetryDelayFunc: queue.RetryDelayFunc, // was queue.WebhookRetryDelay — confirmed defect fixed here
})
```

### Pattern 4: Per-task `MaxRetry` for the image queue (D-05)
**What:** `asynq.MaxRetry(n)` is an `Option` passed at task-creation time (`asynq.NewTask(typename, payload, opts...)`), independent of the server-wide config. [VERIFIED: `asynq@v0.26.0/client.go:95` — `func MaxRetry(n int) Option`]
**Example:**
```go
// Source: existing internal/queue/queue.go:65-71 NewWebhookDeliverTask pattern, mirrored
func NewImageConvertTask(jobID uuid.UUID, maxRetry int) (*asynq.Task, error) {
    b, err := json.Marshal(ConvertPayload{JobID: jobID})
    if err != nil {
        return nil, fmt.Errorf("marshal convert payload: %w", err)
    }
    return asynq.NewTask(TypeImageConvert, b, asynq.Queue(QueueImage), asynq.MaxRetry(maxRetry)), nil
}
```
**Where does `maxRetry` come from at call sites?** `EnqueueImageConvert` is called from both `cmd/api/main.go` (initial job creation) and the new reconciler / worker re-enqueue paths — the same env var (`IMAGE_MAX_RETRY`) must be honored consistently wherever a task is created. Recommend following the exact precedent already set by `queue.RedisOpt()` (which reads `REDIS_ADDR` directly inside the `queue` package): store the configured value on `queue.Client` at construction (`queue.NewClient()` reads `IMAGE_MAX_RETRY` once, defaults to e.g. 4, stores it as a field), so `EnqueueImageConvert(ctx, jobID)`'s signature does not need to change at every call site. This is Claude's-discretion territory per CONTEXT.md — present as the recommended default, not a hard requirement.

### Pattern 5: Reconciler as a ticker goroutine (D-10/D-11, Claude's Discretion item)
**What:** A `time.Ticker`-driven goroutine started explicitly in `cmd/worker/main.go`, following the exact same `signal.NotifyContext` + graceful-shutdown shape already used in `cmd/api/main.go` (currently absent from `cmd/worker/main.go`, which just calls the blocking `srv.Run(mux)`).
**When to use:** Recommended over `asynq.PeriodicTaskManager` (see Alternatives Considered).
**Example:**
```go
// Source: pattern mirrors cmd/api/main.go:26 (signal.NotifyContext) + asynq@v0.26.0 server.go:680 (Start) / :724 (Shutdown)
func main() {
    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()
    // ... existing pool/store/redisOpt/qc construction unchanged ...

    sweeper := reconciler.NewSweeper(jobs.NewRepo(pool), qc, reconciler.Config{
        QueuedStaleAfter: envDuration("RECONCILER_QUEUED_STALE_AFTER", 90*time.Second),
        ActiveStaleAfter: envDuration("RECONCILER_ACTIVE_STALE_AFTER", 5*time.Minute),
        SweepInterval:    envDuration("RECONCILER_SWEEP_INTERVAL", 1*time.Minute),
        MaxRecoveries:    envInt("RECONCILER_MAX_RECOVERIES", 3),
    })

    srv := asynq.NewServer(redisOpt, asynq.Config{ /* ... */ })
    mux := asynq.NewServeMux()
    mux.HandleFunc(queue.TypeImageConvert, h.HandleImageConvert)
    mux.HandleFunc(queue.TypeWebhookDeliver, h.HandleWebhookDeliver)

    if err := srv.Start(mux); err != nil { // was srv.Run(mux) — Start is non-blocking
        log.Fatalf("worker: %v", err)
    }
    go sweeper.Run(ctx) // internal ticker loop; returns when ctx is cancelled

    <-ctx.Done()
    log.Println("🛑 shutting down worker...")
    srv.Shutdown() // asynq's own graceful drain
    log.Println("bye 👋")
}
```

### Pattern 6: Requeue transition + recovery-count cap (D-12/D-13, RECON-01/RECON-02)
**What:** One new guarded transition covering both reconciler recovery paths (lost-enqueue `queued` job, and crashed-worker `active` job), plus a count of prior recoveries read from `job_events`.
**Example:**
```go
// Source: extends existing internal/jobs/repo.go transition() helper — same
// row-locked, event-logged discipline as MarkActive/MarkDone/MarkFailed.
func (r *Repo) RequeueStale(ctx context.Context, id uuid.UUID, reason string) error {
    return r.transition(ctx, id, StatusQueued, []string{StatusQueued, StatusActive}, func(ctx context.Context, tx pgx.Tx) error {
        _, err := tx.Exec(ctx, `UPDATE jobs SET status = 'queued' WHERE id = $1`, id)
        return err
    })
    // NOTE: transition() already inserts a job_events row with from_status/to_status;
    // RECON-03 needs the `detail` jsonb populated too (action=reconciler_recovery,
    // reason=reason) — transition()'s apply-closure signature does not currently
    // expose a way to set `detail` on the auto-inserted event row. The planner must
    // either (a) extend transition() to accept an optional detail payload, or
    // (b) have the reconciler do its own explicit BeginFunc block mirroring
    // transition()'s row-lock + update + job_events-insert-with-detail, rather than
    // reusing transition() unmodified. Recommend (a): a minimal, backward-compatible
    // extension (nil detail for existing callers).
}

// Count prior recoveries for the cap (D-12). No new migration — job_events.detail
// is already jsonb (0001_init.sql:109).
func (r *Repo) RecoveryCount(ctx context.Context, id uuid.UUID) (int, error) {
    var n int
    err := r.pool.QueryRow(ctx, `
        SELECT count(*) FROM job_events
        WHERE job_id = $1 AND detail->>'action' = 'reconciler_recovery'`, id,
    ).Scan(&n)
    return n, err
}
```

### Anti-Patterns to Avoid
- **Relying on vips exit code alone to classify terminal vs transient:** verified in this session (live container test) that vips returns exit code 1 both for "file is not a known format" (terminal) and for environment-level issues like a missing output directory (arguably transient/our-bug). Always inspect stderr content.
- **Resetting `started_at` on every internal asynq retry:** if `MarkActive`'s `UPDATE` unconditionally sets `started_at = now()` instead of `COALESCE(started_at, now())`, the reconciler's active-staleness threshold effectively never fires for a job stuck in a transient-retry loop, because every internal retry "resets the clock."
- **Using asynq's `Inspector` as the reconciler's stale-job source of truth:** contradicts CLAUDE.md's "Postgres is system of record" and adds Redis-decode coupling with no indexed lookup by job id.
- **Letting the reconciler bypass `Repo.transition`'s row lock:** any ad-hoc `UPDATE jobs SET status = ...` outside a `SELECT ... FOR UPDATE` transaction reopens exactly the kind of race this phase exists to close.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Per-queue/per-task-type retry backoff | A custom retry-scheduling goroutine/queue-poller | `asynq.Config.RetryDelayFunc` dispatched by `t.Type()` (Pattern 3) | asynq already re-invokes the handler on its own schedule; duplicating that outside asynq would double-process tasks |
| Per-task retry budget | A manual attempt counter checked inside the handler | `asynq.MaxRetry(n)` task option (Pattern 4) | asynq already tracks `Retried`/`Retry` per task message; a hand-rolled counter would drift from asynq's own bookkeeping and double-count retries |
| Detecting "object not found" in S3/MinIO | String-matching raw error text from `minio-go` | `minio.ToErrorResponse(err).Code == minio.NoSuchKey` [VERIFIED via `minio-go@v7.2.1/api-error-response.go`] | `ToErrorResponse` already normalizes MinIO's XML error body into a typed `Code` field; string-matching the raw error is fragile across minio-go versions |
| Periodic background sweep timing | A cron library or external scheduler | `time.Ticker` inside the already-running worker process (Pattern 5) | The project has exactly one long-running worker process; introducing an external scheduler for a 1-minute in-process sweep is unwarranted complexity for this scale |

**Key insight:** Every piece of retry/backoff machinery this phase needs already exists in asynq's public API (`RetryDelayFunc`, `MaxRetry`, `SkipRetry`, `GetRetryCount`/`GetMaxRetry`). The actual engineering work is entirely in (a) correctly classifying errors and (b) not fighting the guarded-transition state machine — not in inventing new retry infrastructure.

## Critical Architectural Insight (verified via asynq source read)

Reading `asynq@v0.26.0/processor.go:333-345` (`handleFailedMessage`) confirms:

```go
switch {
case errors.Is(err, RevokeTask):
    p.markAsDone(l, msg)
case msg.Retried >= msg.Retry || errors.Is(err, SkipRetry):
    p.logger.Warnf("Retry exhausted for task id=%s", msg.ID)
    p.archive(l, msg, err)   // <-- task moves to Redis "archived" state; handler is NEVER called again for it
default:
    p.retry(l, msg, err, p.isFailureFunc(err))
}
```

Once the image queue's own `MaxRetry` budget (D-05: 3-5 attempts) is exhausted for a *transient* error, asynq archives the task in Redis silently — it does not call `HandleImageConvert` one more time to let it mark the job `failed`. Since this phase's design intentionally leaves the job `active` in Postgres on transient failure (rather than calling `MarkFailed`), **a job that keeps failing transiently past its asynq-level retry budget will sit in Postgres status `active` forever unless something else intervenes.**

This is not a gap to patch around — it is *exactly* the job of RECON-02: the reconciler's active-staleness sweep is the mechanism that eventually notices this job, decides (via the D-12 recovery cap) whether to give it a fresh asynq-level retry budget by requeuing it, or — once the cap is exhausted — mark it `failed` with `error_code=reconciler_exhausted` (D-13). This confirms and sharpens the phase's own stated sequencing note ("retry-safety must be implemented before reconciler work") — the inverse is equally true: the reconciler is a *required* backstop the moment retry-safety is implemented, not an independent nice-to-have. The planner should treat RELY-01/02 and RECON-02 as functionally coupled, not just ordered.

## Common Pitfalls

### Pitfall 1: `MarkActive` re-entry failure silently defeats the whole fix
**What goes wrong:** Even after correctly classifying transient errors and returning them unwrapped, if `MarkActive`'s allowed-from list is not widened to include `StatusActive`, the *next* asynq-internal retry attempt will fail at the very first line of the handler (`MarkActive` on an already-`active` job), get wrapped in `asynq.SkipRetry` (matching the existing unrelated-looking "already active/done/canceled — let asynq drop it" comment), and the bug persists in a slightly different disguise.
**Why it happens:** The guarded-transition pattern is designed for one-shot state changes; asynq's retry-by-re-invoking-the-same-handler model doesn't naturally fit a "single valid predecessor state" transition.
**How to avoid:** Widen `MarkActive`'s allow-list to `[]string{StatusQueued, StatusActive}` (idempotent re-entry) as a required, explicit task — not an incidental side effect.
**Warning signs:** Integration test where a transient failure is injected twice in a row for the same job should show the job still `active` (not silently `failed`) and a second real engine attempt actually happening.

### Pitfall 2: `started_at` reset on every retry breaks RECON-02's staleness math
**What goes wrong:** If `MarkActive`'s `UPDATE` sets `started_at = now()` unconditionally (as it currently does), every internal asynq retry "restarts the clock" for staleness purposes, and a job endlessly retrying (even one that will never succeed) never crosses the `active`-staleness threshold because `started_at` keeps refreshing.
**Why it happens:** The current single-attempt design never needed `started_at` to survive multiple `MarkActive` calls for the same job — this phase is the first time `MarkActive` is called more than once per job.
**How to avoid:** Use `started_at = COALESCE(started_at, now())` in the widened `MarkActive`.
**Warning signs:** A job retried transiently many times, well past the intended active-staleness window, that the reconciler never picks up.

### Pitfall 3: Raw vips stderr leaking into API/webhook responses
**What goes wrong:** `HandleImageConvert`'s current code calls `h.repo.MarkFailed(ctx, jobID, "engine_error", err.Error())`, and `internal/api/handlers.go:190-191` already returns `job.ErrorMessage` verbatim to API clients, and Phase 2's webhook payload includes `error_message` too (`internal/worker/worker.go:145-147`). Raw vips stderr can include local filesystem paths (`workDir` under `os.TempDir()`, built from `job.ID.String()`) — verified in this session's live vips tests, stderr consistently includes the exact input/output file paths passed to `vips copy`. This is not new to this phase, but this phase is the first place error classification/messages for image conversion get deliberately redesigned, making it the natural place to also sanitize what's stored in `error_message` (e.g., store a short classified reason string instead of raw stderr, keep full stderr only in `job_events.detail` for internal diagnostics).
**Why it happens:** `err.Error()` from a wrapped `os/exec` failure includes everything captured on stderr by design (`internal/convert/exec.go:42`).
**How to avoid:** When calling `MarkFailed` for a terminal engine error, pass a short, classified message (e.g. `"unsupported or corrupted input format"`) as `error_message`, and put the full raw stderr into a `job_events.detail` field instead (internal-only, not exposed via API/webhook).
**Warning signs:** A webhook payload or `GET /jobs/{id}` response containing a local temp directory path like `/tmp/octoconv-<uuid>-.../in.png`.

### Pitfall 4: Reconciler race with a legitimately-slow-but-healthy worker
**What goes wrong:** If a worker is still genuinely processing a job when the reconciler's active-staleness threshold fires (D-09: ~5 min, well above `ENGINE_TIMEOUT=120s`, but still a fixed threshold), the reconciler flips the job back to `queued` and re-enqueues it. If the original (slow, not crashed) worker then finishes and calls `MarkDone` (guarded `active -> done` only), that transition now fails because status is `queued` — the correct, real result is silently discarded (an error is returned from `MarkDone`, but there's no user-facing path for that failure once inside `process()`'s success branch).
**Why it happens:** This is the fundamental limitation of a timestamp-based staleness sweep (a "lease" without a true heartbeat/fencing token) — it can produce false positives under network partition or unusually slow processing, which is a known, accepted tradeoff (see `.planning/STATE.md` Blockers/Concerns: "Lease/heartbeat staleness thresholds ... need concrete values during planning" and v2 deferred item `SCALE-V2-03`: "transactional outbox instead of reactive-sweeper reconciler, only if sweeper's false-negative/latency characteristics prove unacceptable").
**How to avoid:** Cannot be fully eliminated within this phase's MVP scope (no fencing tokens / lease tokens introduced). Mitigate by setting `RECONCILER_ACTIVE_STALE_AFTER` comfortably above `ENGINE_TIMEOUT` (D-09 already does this) and by flagging this as a known, accepted residual risk for the planner/user rather than something a task must "solve." A duplicate-in-flight scenario after the race (old worker still writing to S3/Postgres after reconciler already requeued) also means: even the *retry* path itself can encounter `MarkDone`/`AddOutput` failing — this should be treated as a non-fatal, logged condition, not a crash.
**Warning signs:** `job_events` showing a job transition `active -> queued` (reconciler recovery) followed shortly by a failed `MarkDone`/`AddOutput` call from the "orphaned" original attempt.

### Pitfall 5: `job_events.detail` tagging inconsistency breaks the recovery-count cap
**What goes wrong:** If different code paths that log reconciler actions use different keys/values in `detail` (e.g., one uses `{"action":"recovery"}` and another `{"reason":"reconciler"}`), `Repo.RecoveryCount`'s `detail->>'action' = 'reconciler_recovery'` query silently undercounts, and the D-12 cap (3 recoveries) never triggers — a permanently-broken job retries forever instead of terminally failing.
**Why it happens:** `jsonb` gives no compile-time guarantee of a consistent shape across call sites.
**How to avoid:** Define one Go struct/constant set for the reconciler's `job_events.detail` payload shape (e.g. `type RecoveryDetail struct { Action string; Reason string }` with `Action = "reconciler_recovery"` as a package constant) and use it everywhere the reconciler writes an event.
**Warning signs:** A job's recovery count staying at 0 across multiple actual reconciler interventions visible in `job_events`.

## Code Examples

### Verified vips CLI behavior (live-tested in this session, `debian:bookworm-slim` + `libvips-tools`, matching `Dockerfile.worker` exactly)

```
$ vips --version
vips-8.14.1

# Corrupted/non-image input (text file with .png extension):
$ vips copy bad.png out.jpg
VipsForeignLoad: "bad.png" is not a known file format
exit=1

# Truncated real JPEG:
$ vips copy trunc.jpg out.png
VipsJpeg: Premature end of JPEG file
VipsJpeg: JPEG datastream contains no image
exit=1

# Nonexistent input path:
$ vips copy doesnotexist.png out.jpg
VipsForeignLoad: file "doesnotexist.png" does not exist
exit=1

# Missing output directory (environment/bug condition, not user input):
$ vips copy good.png /nonexistent_dir/out.jpg
/nonexistent_dir/out.jpg: unable to open for write
unix error: No such file or directory
exit=1

# Successful conversion:
$ vips copy good.png out.jpg
exit=0
```
[VERIFIED: live test in this session, container `debian:bookworm-slim` + `apt-get install libvips-tools`, `vips-8.14.1` — same base image and package as `Dockerfile.worker:16`]

**Key takeaway for the planner:** exit code is `1` for essentially every failure mode (both terminal-format-error and environment-bug cases) — exit code alone cannot distinguish transient from terminal. Only stderr substring content distinguishes them reliably: `"is not a known file format"` and `"Premature end of JPEG file"` / `"JPEG datastream contains no image"` are strong, verified signals for terminal (D-01's "engine explicitly signals bad/corrupted format"). No other codec-specific corruption strings were tested (only JPEG source-format truncation was exercised); the planner/implementer should treat the terminal-signature list as a starting point to extend with additional formats (PNG/WebP/TIFF/HEIC corruption messages) if time allows, and default anything unmatched to transient per D-01's broad-retry philosophy.

### MinIO storage-404 detection
```go
// Source: github.com/minio/minio-go/v7 v7.2.1 api-error-response.go:79 (func ToErrorResponse),
// s3-error.go:23 (NoSuchKey = "NoSuchKey") — installed module source, verified in this session
resp := minio.ToErrorResponse(err)
if resp.Code == minio.NoSuchKey {
    // terminal: input object genuinely absent, retrying will never succeed (D-02)
}
```

### asynq per-task-type retry dispatch (already shown in Pattern 3/4 above) — confirmed API surface:
```go
// asynq@v0.26.0 server.go:297
type RetryDelayFunc func(n int, e error, t *Task) time.Duration
// asynq@v0.26.0 asynq.go:40-42
func (t *Task) Type() string    { return t.typename }
func (t *Task) Payload() []byte { return t.payload }
// asynq@v0.26.0 client.go:95
func MaxRetry(n int) Option
// asynq@v0.26.0 client.go:307
defaultMaxRetry = 25   // confirms CONCERNS.md's "default up to 25 retries" claim
// asynq@v0.26.0 context.go:25,33
func GetRetryCount(ctx context.Context) (n int, ok bool)
func GetMaxRetry(ctx context.Context) (n int, ok bool)
```

## State of the Art

| Old Approach (current code) | Current/Recommended Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `RetryDelayFunc: queue.WebhookRetryDelay` applied server-wide to all queues | `RetryDelayFunc: queue.RetryDelayFunc` dispatching on `t.Type()` | This phase | Image queue gets its own fast (2s/5s/15s) schedule instead of silently inheriting webhook's 30s-15min schedule |
| `NewImageConvertTask` has no `MaxRetry` option (defaults to asynq's `defaultMaxRetry = 25`) | `asynq.MaxRetry(3-5)` (D-05) set explicitly, matching the already-established `NewWebhookDeliverTask`'s `asynq.MaxRetry(6)` pattern | This phase | Image conversion gets a small, deliberate retry budget instead of an accidental 25-attempt default |
| `MarkActive` allows only `queued -> active` | `MarkActive` allows `queued -> active` and `active -> active` (idempotent) | This phase | Enables asynq's same-task internal retry to actually re-invoke the handler successfully |
| No reconciler; jobs stuck in `queued`/`active` require manual intervention | Ticker-driven `internal/reconciler` sweep every ~1 min | This phase | RECON-01/02/03 satisfied |

**Deprecated/outdated:** None — this is a bugfix + additive-feature phase on top of an existing, still-current stack (asynq v0.26.0, pgx v5.10.0 are both current as installed; no upstream deprecation notices found for the APIs used).

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | The full list of terminal vips stderr signatures beyond the 3 verified in this session (`"is not a known file format"`, `"Premature end of JPEG file"`, `"JPEG datastream contains no image"`) is incomplete — other formats (PNG/WebP/TIFF/HEIC corruption) were not individually live-tested. | Common Pitfalls / Code Examples | If a corrupted PNG/WebP/TIFF/HEIC input produces a different, unmatched stderr string, it will be (safely, per D-01's broad-retry default) classified transient and retried a few times before falling to the reconciler's exhaustion path — wastes a few retry cycles but does not cause incorrect terminal behavior or duplicate processing. Low risk given D-01's explicit "broad retry, terminal is the exception" philosophy. |
| A2 | Storing the image queue's `MaxRetry` value on `queue.Client` at construction time (reading `IMAGE_MAX_RETRY` once, similar to `RedisOpt()`'s existing `REDIS_ADDR` pattern) is the best way to keep `EnqueueImageConvert`'s call sites (API + worker + reconciler) consistent — this is a design recommendation, not verified against any existing project precedent beyond `RedisOpt()`. | Pattern 4 | If the planner instead threads `maxRetry` as an explicit parameter through every call site, that's equally valid and arguably more explicit (matches `NewHandler`'s style of passed-in config) — no functional risk, just a stylistic choice left to planner/implementer discretion (also called out explicitly in CONTEXT.md `Claude's Discretion`). |
| A3 | `transition()`'s current signature has no way to attach a `detail jsonb` payload to the auto-inserted `job_events` row — extending it (Pattern 6) is recommended as the cleanest fix, but alternate designs (a separate explicit transaction in the reconciler package bypassing `transition()`) were not fully evaluated against every existing `transition()` call site. | Pattern 6 | If `transition()` is extended incorrectly, existing `MarkActive`/`MarkDone`/`MarkFailed` call sites need trivial updates (pass `nil` detail) — low risk, mechanical change, should be caught by existing `repo_test.go` tests. |

## Open Questions (RESOLVED)

1. **Exact env var names for reconciler thresholds and image retry budget**
   - What we know: CONTEXT.md explicitly leaves these to Claude's discretion, following the existing only-env-var convention (`.env.example`).
   - What's unclear: Whether the planner should introduce all five (`IMAGE_MAX_RETRY`, `RECONCILER_QUEUED_STALE_AFTER`, `RECONCILER_ACTIVE_STALE_AFTER`, `RECONCILER_SWEEP_INTERVAL`, `RECONCILER_MAX_RECOVERIES`) as new `.env.example` entries in this phase's plan (recommended) or defer some to hardcoded constants.
   - Recommendation: Add all five to `.env.example` with the defaults proposed in this document (`90s`/`5m`/`1m`/`3`/`4`), consistent with every other tunable in the project being env-var-driven.
   - RESOLVED: All five env vars adopted with these exact defaults — `IMAGE_MAX_RETRY=4` (Plan 01), `RECONCILER_QUEUED_STALE_AFTER=90s` / `RECONCILER_ACTIVE_STALE_AFTER=5m` / `RECONCILER_SWEEP_INTERVAL=1m` / `RECONCILER_MAX_RECOVERIES=3` (Plan 03). A per-job `asynq.Unique` lock TTL (`imageTaskUniqueTTL`, ~10m) was additionally introduced as a package constant in Plan 01.

2. **Whether `transition()` needs to be extended to accept a `detail` payload, or whether the reconciler should write its own transaction**
   - What we know: `job_events.detail jsonb` exists and is unused by any current code path; `transition()` currently only ever inserts a `(job_id, from_status, to_status)` with `detail` left NULL.
   - What's unclear: The cleanest way to thread an optional detail payload through the shared `transition()` helper without breaking its three existing callers.
   - Recommendation: Extend `transition()`'s signature with an additional `detail any` (or `map[string]any`) parameter, defaulting existing callers to `nil` — a small, mechanical, low-risk change; the planner should size this as an explicit task since it touches a shared, well-tested helper.
   - RESOLVED: `transition()` extended with a `detail map[string]any` parameter in Plan 02 (existing MarkActive/MarkDone/MarkFailed callers pass through); the reconciler's RequeueStale (Plan 03) uses it to tag `job_events.detail` with `action=reconciler_recovery`, which RecoveryCount reads for the cap.

3. **Should the residual race described in Pitfall 4 (reconciler vs. legitimately-slow worker) be addressed with any additional guard in this phase, or purely documented as an accepted risk?**
   - What we know: `.planning/STATE.md` already flags this exact concern as a blocker needing "concrete values during planning, based on actual job-duration data" — no real production job-duration data exists yet (this is the first hardening pass on a single vertical slice).
   - What's unclear: Whether the planner should add a lightweight guard (e.g., re-check the job's current status immediately before requeuing, inside the same locked transaction — which `Repo.transition`'s `SELECT ... FOR UPDATE` already does) is sufficient, or whether this needs explicit user sign-off as an accepted MVP limitation.
   - Recommendation: The existing row-lock already prevents the worst case (concurrent double-processing) since `MarkDone`/`AddOutput` will fail loudly if status was flipped out from under it; document this as an accepted MVP limitation (matches the already-deferred `SCALE-V2-01` outbox idea) rather than building additional fencing-token infrastructure in this phase.
   - RESOLVED: Documented and accepted as an MVP limitation (Plan 03 threat T-03-09). A stronger duplicate guard than the row lock alone was additionally adopted — the per-job `asynq.Unique` lock (Plan 01) plus the reconciler's enqueue-first / ErrDuplicateTask-no-op sweep (Plan 03 threat T-03-10) — so the reconciler cannot even create a competing task while a legitimately-slow worker's task is still active-and-unarchived; only the residual >TTL backlog case remains within the accepted envelope.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Go toolchain | All code changes | ✓ | go1.26.4 darwin/arm64 [VERIFIED: `go.mod:3`, project CLAUDE.md] | — |
| asynq (Go module, vendored) | Retry/backoff/MaxRetry mechanics | ✓ | v0.26.0 [VERIFIED: local module cache] | — |
| pgx/v5 (Go module, vendored) | Guarded transitions, reconciler queries | ✓ | v5.10.0 [VERIFIED: local module cache] | — |
| minio-go/v7 (Go module, vendored) | Storage-404 detection | ✓ | v7.2.1 [VERIFIED: local module cache] | — |
| Docker | Live vips CLI verification (research-time only, not a runtime dependency of this phase) | ✓ | confirmed via `docker info` in this session | — |
| `vips` CLI in worker container | Terminal-error signature detection (already a runtime dependency, unchanged by this phase) | ✓ | vips-8.14.1 in `debian:bookworm-slim` + `libvips-tools` [VERIFIED: live container test this session, matches `Dockerfile.worker:16`] | — |

**Missing dependencies with no fallback:** None.
**Missing dependencies with fallback:** None — this phase adds no new environment dependencies.

## Security Domain

`security_enforcement` is not set in `.planning/config.json` (absent = enabled per protocol), so this section is included. This phase adds no new HTTP endpoints and no new external inputs (it operates entirely on data already validated/stored by Phases 1-2), so most ASVS categories are not newly applicable — the one concrete, newly-relevant concern is information exposure through error messages, already flagged as Pitfall 3.

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-------------------|
| V2 Authentication | No | Unchanged — no new auth surface in this phase |
| V3 Session Management | No | N/A — backend worker/reconciler only |
| V4 Access Control | No | No new endpoints; reconciler operates on all jobs indiscriminately by design (it is not client-scoped, matching the existing internal-only trust model) |
| V5 Input Validation | No (new) | Reconciler's stale-job query is a fixed, parameterless `SELECT` on internal timestamps — no user-controlled input reaches this query |
| V6 Cryptography | No | Not touched by this phase |
| V7 Error Handling & Information Exposure (ASVS 7.4) | Yes | Do not store raw `os/exec` stderr (which can include local filesystem paths) in `jobs.error_message`, since that field is already returned verbatim via `GET /jobs/{id}` (`internal/api/handlers.go:190-191`) and via webhook payloads (`internal/worker/worker.go:145-147`). Store a short classified reason in `error_message`; keep full diagnostic stderr only in `job_events.detail` (internal-only). |

### Known Threat Patterns for this stack

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|----------------------|
| Information disclosure via verbose engine error messages surfaced to API/webhook clients | Information Disclosure | Classify + truncate `error_message` before `MarkFailed` (Pitfall 3); this is a pre-existing gap this phase's error-classification work is well-positioned to close, not a new risk this phase introduces |
| Reconciler double-processing due to bypassing the row lock | Tampering / Repudiation | Always route reconciler status changes through `Repo.transition` (or an equivalent `SELECT ... FOR UPDATE`-guarded helper), never an ad-hoc `UPDATE` |
| Unbounded reconciler retry loop on a permanently-broken job | Denial of Service (resource exhaustion via repeated engine invocation) | D-12's recovery cap (3) + D-13's terminal `reconciler_exhausted` status bounds this |

## Sources

### Primary (HIGH confidence — verified directly against installed source / live test in this session)
- `$(go env GOMODCACHE)/github.com/hibiken/asynq@v0.26.0/server.go` — `RetryDelayFunc` type (line 297), `DefaultRetryDelayFunc` (line 401), `Config.RetryDelayFunc` wiring (line 459-461), `Server.Run`/`Start`/`Shutdown`/`Stop` (lines 663-761)
- `$(go env GOMODCACHE)/github.com/hibiken/asynq@v0.26.0/processor.go` — `handleFailedMessage` retry/archive decision logic (lines 333-360), `SkipRetry`/`RevokeTask` sentinels (lines 327-331)
- `$(go env GOMODCACHE)/github.com/hibiken/asynq@v0.26.0/client.go` — `MaxRetry(n) Option` (line 95), `defaultMaxRetry = 25` (line 307)
- `$(go env GOMODCACHE)/github.com/hibiken/asynq@v0.26.0/asynq.go` — `Task.Type()`/`Task.Payload()` (lines 40-42)
- `$(go env GOMODCACHE)/github.com/hibiken/asynq@v0.26.0/context.go` — `GetRetryCount`/`GetMaxRetry` (lines 25, 33)
- `$(go env GOMODCACHE)/github.com/hibiken/asynq@v0.26.0/inspector.go` — full `Inspector` method inventory (no job-id-indexed lookup exists, confirmed by exhaustive grep)
- `$(go env GOMODCACHE)/github.com/hibiken/asynq@v0.26.0/periodic_task_manager.go` — `PeriodicTaskManager`/`Scheduler` purpose (cron-style task enqueueing, not custom sweep logic)
- `$(go env GOMODCACHE)/github.com/minio/minio-go/v7@v7.2.1/api-error-response.go` — `ToErrorResponse` (line 79)
- `$(go env GOMODCACHE)/github.com/minio/minio-go/v7@v7.2.1/s3-error.go` — `NoSuchKey = "NoSuchKey"` (line 23)
- Live `docker run debian:bookworm-slim` + `apt-get install libvips-tools` session (this session) — `vips-8.14.1` exit-code/stderr behavior for 7 distinct scenarios (corrupted input, truncated JPEG, missing input, missing output dir, successful conversion) — matches `Dockerfile.worker:16` base image and package exactly
- Project source: `internal/worker/worker.go`, `internal/jobs/repo.go`, `internal/queue/queue.go`, `internal/queue/client.go`, `cmd/worker/main.go`, `cmd/api/main.go`, `internal/convert/{convert,exec,libvips}.go`, `internal/storage/storage.go`, `internal/api/handlers.go`, `internal/db/migrations/0001_init.sql`, `.env.example`, `go.mod`
- `.planning/codebase/CONCERNS.md` — original bug diagnosis, cross-verified (not just trusted) against actual asynq/repo source in this session
- `.planning/phases/03-retry-safety-reconciler/03-CONTEXT.md`, `.planning/REQUIREMENTS.md`, `.planning/STATE.md` — locked decisions and requirement IDs

### Secondary (MEDIUM confidence)
- None used as load-bearing — all significant claims were verifiable directly against source/live test in this session.

### Tertiary (LOW confidence)
- WebSearch result on `vipsthumbnail` (a *different* binary from the `vips` CLI driver this project actually uses) returning exit code 0 on load failure — noted but **not applicable**: this project's `LibvipsConverter.Convert` calls `vips copy`, not `vipsthumbnail`, and live-testing in this session confirmed `vips copy` reliably returns exit code 1 on load failure. Included only to flag that exit-code behavior is not uniform across libvips' various CLI frontends, reinforcing why stderr-content matching (not exit code) is the correct classification approach.

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — no new libraries; all APIs used were read directly from installed module source, not recalled from training data
- Architecture: HIGH — the core mechanism (idempotent `MarkActive`, error classification, queue-aware retry dispatch, ticker-based reconciler) is derived from direct source inspection of the exact asynq version in use, plus direct reading of every relevant file in this codebase
- Pitfalls: HIGH for Pitfalls 1/2/3/5 (derived from direct source/code reading); MEDIUM-HIGH for Pitfall 4 (the race condition is a well-understood category of problem for lease/heartbeat-based reconcilers generally, and the project's own STATE.md already independently flagged it as a concern, but no production job-duration data exists yet to size the threshold with full confidence)

**Research date:** 2026-07-05
**Valid until:** 30 days (stable stack, no fast-moving dependencies; re-verify if `asynq` or `minio-go` are upgraded before this phase is implemented)
