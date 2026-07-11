# Phase 16: Webhook Delivery Decoupling - Context

**Gathered:** 2026-07-11

**Status:** Ready for planning

<domain>
## Phase Boundary

Webhook-доставка результата перестаёт зависеть от какого-либо одного engine-воркер-процесса. Сегодня `cmd/worker` (image) — единственный консюмер `TypeWebhookDeliver` И держит singleton reconciler-sweeper (`cmd/worker/main.go:76,85`), поэтому деплой без image-воркера (только document/html) молча теряет вебхуки. Фаза выносит webhook-консюмер и sweeper в новый выделенный `cmd/webhook-worker`, деплоит его избыточно (≥2 консюмера), и решает где живёт «ровно один» sweeper через Postgres advisory-lock. Требование: WEBH-01. Зависит: ничего (ортогонально engine-фазам; специально последней, чтобы ни один engine-бинарник не соблазнился регистрировать webhook-очередь).

</domain>

<decisions>
## Implementation Decisions

### Singleton-sweeper topology
- **D-01:** Единственный fleet-wide sweeper обеспечивается **Postgres session-level advisory-lock** (`pg_try_advisory_lock`), НЕ отдельным `cmd/reconciler`-бинарником и НЕ привязкой к назначенному процессу. Каждый `webhook-worker` пробует взять lock; взявший свипает, остальные только консюмируют webhook-очередь. Zero-dep (Postgres уже есть), однородный деплой webhook-worker'ов. Отвергнуто: отдельный single-replica процесс (+бинарник/контейнер, зависимость от оркестратора что replicas=1); привязка к назначенному (тот же SPOF).
- **D-02:** Модель — **постоянная попытка каждый tick**: каждый `webhook-worker` держит выделенную long-lived Postgres-сессию и пробует `pg_try_advisory_lock` на каждом sweep-tick; проигравшие консюмируют очередь и продолжают пробовать. Смерть leader'а → его сессия закрывается → Postgres автоматически освобождает lock → следующий tick другого воркера подхватывает свип. Авто-failover без внешней координации. КРИТИЧНО: lock — session-level (не transaction-level), держится на выделенном соединении, отдельном от pool'а repo, чтобы освобождение было привязано именно к жизни процесса.

### Migration / cutover
- **D-03:** **Чистый срез в этой фазе** — `mux.HandleFunc(queue.TypeWebhookDeliver, ...)` И запуск sweeper'а убираются из `cmd/worker/main.go`, переносятся в `cmd/webhook-worker`. Конечное состояние фазы атомарно: вебхуки только в webhook-worker'ах, image-worker снова чисто image. asynq pull-based → ноль риска double-delivery в момент самого деплоя (старый и новый консюмер физически могут сосуществовать, но фаза оставляет одно согласованное состояние в коде). Двухшаговый overlap отвергнут — оставил бы фазу в промежуточном состоянии, где SC1 не достигнут.
- **D-04:** Sweeper уезжает **полностью в `cmd/webhook-worker`** (под advisory-lock D-01/D-02). Engine-воркеры (image/document/chromium) больше не знают про reconciler. Обоснование: sweeper уже сам шлёт вебхуки (recovery-exhaustion + webhook-gap recovery, `reconciler.go` sweep) — логично живёт рядом с доставкой.

### Redundancy shape & live test
- **D-05:** «≥2 консюмера» = **два именованных compose-сервиса** `webhook-worker-1`/`webhook-worker-2` (не `deploy.replicas`, который docker-compose без swarm/`--compatibility` игнорирует — непредсказуемо для локального e2e). Live-приёмка (планка «live e2e verified»): SC1 — остановить `cmd/worker`, document/html-job всё равно шлёт webhook; SC2 — kill одного webhook-worker mid-delivery, второй доставляет без потери/дубля; SC3 — при двух живых ровно один держит advisory-lock (проверка через job_events/лог: только один инстанс свипает).
- **D-06:** no-loss/no-dup in-flight (SC2) держится на **существующих гарантиях asynq** (at-least-once: убитый консюмер → task возвращается в очередь по lease-таймауту, второй подхватывает) + **существующей идемпотентности доставки** (`webhook_deliveries`-запись + `asynq.Unique`). Фаза НЕ изобретает новую доставку — переиспользует `internal/webhook` вербатим, меняет только ГДЕ он консюмится. Live-тест подтверждает поведение, не новый код. Своего in-flight-tracking сверх asynq не добавляем.

### New binary config
- **D-07:** `cmd/webhook-worker` — тримнутая копия `cmd/worker/main.go`. **Нужно**: Postgres (repo + advisory-lock-соединение + sweeper), Redis (asynq-консюмер `TypeWebhookDeliver`), `WEBHOOK_SIGNING_SECRET`, `WEBHOOK_ALLOW_PRIVATE_IPS` (SSRF-гард доставки Phase 5), sweep/reconciler-env, `WEBHOOK_WORKER_CONCURRENCY`. **НЕ нужно**: S3/MinIO, convert-registry, engine-таймауты, presign — webhook-доставка читает job из Postgres, не трогает S3. Свой `Dockerfile.webhook-worker` (чистый debian-slim, без libvips/LibreOffice/chromium).

### Claude's Discretion
- Числовой ключ advisory-lock (константа) и точный способ держать выделенное соединение (`pgxpool.Acquire` long-lived vs отдельный `pgx.Conn`).
- Куда деть `WEBHOOK_SIGNING_SECRET`/sweeper-env из `cmd/worker`/`cmd/document-worker` после снятия webhook-роли (нужны ли ещё — engine-воркеры больше не шлют вебхуки напрямую? проверить: воркеры фактически enqueue'ят `EnqueueWebhookDeliver` при завершении job, доставку делает webhook-worker — так что signing secret нужен только webhook-worker'у).
- Точная compose-топология e2e (`docker-compose.e2e.yml`): как оркеструется «стоп image-worker» и «kill одного webhook-worker» в тесте.
- `WEBHOOK_WORKER_CONCURRENCY`/sweep-интервалы значения по образцу существующих.
- Нужна ли миграция БД (advisory-lock — рантайм, не схема; вероятно нет).

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Milestone research (v1.3)
- `.planning/research/SUMMARY.md` — webhook-decoupling: `cmd/webhook-worker` + `Dockerfile.webhook-worker` (тримнутая копия cmd/worker), asynq pull-based safe migration, «≥2 consumers + exactly-1 sweeper» success criteria, topology как Key Decision
- `.planning/research/PITFALLS.md` — **Pitfall 13** (единственный перенесённый консюмер = тот же SPOF, просто перемещённый — нужна реальная избыточность ≥2), **Pitfall 14** (наивный запуск sweeper'а в каждой реплике = double-sweep race — нужен ровно один активный sweeper fleet-wide)

### Existing implementation (source of truth for patterns)
- `cmd/worker/main.go` — текущий единственный webhook-консюмер (`:85` HandleFunc TypeWebhookDeliver) + sweeper (`:76` NewSweeper + Run); ШАБЛОН для тримнутого cmd/webhook-worker и место снятия обеих ролей (D-03)
- `cmd/document-worker/main.go:51` — комментарий «cmd/worker remains the sole webhook consumer» (задокументированный gap, который закрывается)
- `internal/reconciler/reconciler.go` — `Sweeper.Run` ticker-loop (`:66`, без leader-election), `sweep` (recovery + webhook-gap); место обёртки advisory-lock
- `internal/webhook/` — доставка (deliver.go, HMAC-подпись, retry) — переиспользуется вербатим, D-06
- `internal/queue/queue.go`, `internal/queue/client.go` — `TypeWebhookDeliver`/webhook-очередь константы, `EnqueueWebhookDeliver`
- `internal/worker/worker.go` — `HandleWebhookDeliver` (хендлер, переезжает в webhook-worker mux)
- `docker-compose.yml`, `docker-compose.e2e.yml` — топология сервисов (образец document-worker); место двух webhook-worker-сервисов
- `Dockerfile.worker` — образец чистого (движок-специфичного) runtime-образа для Dockerfile.webhook-worker

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `internal/webhook` (доставка + HMAC + retry) и `internal/reconciler` (sweep + webhook-gap recovery) переиспользуются вербатим — фаза только меняет ГДЕ они запускаются (D-06)
- `cmd/worker/main.go` — прямой шаблон для `cmd/webhook-worker` (тримнуть engine/storage/convert-wiring, оставить Postgres+Redis+webhook+sweeper)
- asynq pull-based multi-consumer: N консюмеров на одной очереди — нативно, бесплатно, zero double-delivery (SUMMARY)
- `asynq.Unique` + `webhook_deliveries`-идемпотентность уже дают no-dup (D-06)

### Established Patterns
- engine-class-per-binary/container (v1.2): cmd/worker, cmd/document-worker, cmd/chromium-worker — cmd/webhook-worker следует той же конвенции, но это НЕ engine (нет convert-registry)
- Postgres-first: job-состояние в Postgres, доставка читает оттуда, не из S3 (потому webhook-worker не нужен storage)
- `log.Fatalf` только на старте для unrecoverable init (образец для webhook-worker init: connect Postgres/Redis, взять lock-соединение)

### Integration Points
- `cmd/worker/main.go:76,85` — удалить sweeper + webhook HandleFunc
- `internal/reconciler/reconciler.go` sweep-loop — обернуть advisory-lock (взял → sweep, не взял → skip)
- `docker-compose.yml`/`.e2e.yml` — добавить webhook-worker-1/-2, убрать webhook-роль из image-worker
- `reconciler` engine-routing (RECON-04/05, DOC-09) — sweeper переезжает как есть, engine-aware восстановление зависших задач сохраняется (просто хостится в другом процессе)

</code_context>

<specifics>
## Specific Ideas

- Планка приёмки — «live e2e verified»: свежесобранный стек с двумя webhook-worker'ами; SC1 (стоп image-worker → document webhook доставлен), SC2 (kill одного webhook-worker mid-delivery → второй добивает без потери/дубля), SC3 (ровно один свипает под lock) — все живьём, не по code-review.
- Финальная фаза майлстоуна: после неё вопрос «доставляет ли этот бинарник вебхуки?» имеет единственный settled ответ — да, cmd/webhook-worker, и только он.
- Комментарий в `cmd/document-worker/main.go:51` про «sole webhook consumer» надо обновить/убрать — закрываемый gap.

</specifics>

<deferred>
## Deferred Ideas

- Kubernetes + KEDA автоскейл webhook-worker'ов — вне текущего фокуса (compose-деплой; K8s — будущее, PROJECT.md constraint)
- Вынос sweeper в собственный `cmd/reconciler`-singleton — отвергнуто в пользу advisory-lock (D-01); можно вернуться, если появится потребность в отдельном reconciler-жизненном-цикле
- Своё in-flight webhook tracking/дедуп сверх asynq — не нужно (D-06), asynq + webhook_deliveries достаточно

</deferred>

---

*Phase: 16-webhook-delivery-decoupling*
*Context gathered: 2026-07-11*
