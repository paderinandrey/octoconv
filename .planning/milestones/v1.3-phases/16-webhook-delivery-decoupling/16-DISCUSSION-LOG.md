# Phase 16: Webhook Delivery Decoupling - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-07-11
**Phase:** 16-webhook-delivery-decoupling
**Areas discussed:** Топология singleton-sweeper, Миграция/cutover, Форма избыточности и live-тест, Конфиг нового бинарника

---

## Топология singleton-sweeper

| Option | Description | Selected |
|--------|-------------|----------|
| Postgres advisory-lock (Рекомендую) | pg_try_advisory_lock; взявший свипает, остальные консюмируют; авто-failover при смерти leader'а (сессия закрылась → lock отпущен); zero-dep | ✓ |
| Отдельный cmd/reconciler | single-replica процесс; чёткое разделение, но +бинарник/контейнер и зависимость от оркестратора что replicas=1 | |
| Привязать к назначенному | sweeper в одном конкретном процессе; просто, но тот же SPOF | |

**User's choice:** Postgres advisory-lock

| Option | Description | Selected |
|--------|-------------|----------|
| Постоянная попытка (Рекомендую) | long-lived сессия, pg_try_advisory_lock каждый tick; проигравшие продолжают пробовать; failover на следующем tick | ✓ |
| Lock вокруг каждого sweep | брать/отпускать внутри каждого tick; быстрее failover, но больше lock-трафика и окно гонки | |

**User's choice:** Постоянная попытка

---

## Миграция/cutover

| Option | Description | Selected |
|--------|-------------|----------|
| Чистый срез в этой фазе (Рекомендую) | TypeWebhookDeliver + sweeper убираются из cmd/worker, переносятся в cmd/webhook-worker; конечное состояние атомарно; asynq pull-based → zero double-delivery в деплое | ✓ |
| Двухшаговый overlap | cmd/worker продолжает консюмировать, старую регистрацию убрать позже; безопасно для zero-downtime, но SC1 не достигнут до удаления | |

**User's choice:** Чистый срез в этой фазе

| Option | Description | Selected |
|--------|-------------|----------|
| Только в webhook-worker (Рекомендую) | sweeper полностью уезжает в cmd/webhook-worker (под advisory-lock); engine-воркеры не знают про reconciler | ✓ |
| You decide | на усмотрение планировщика | |

**User's choice:** Только в webhook-worker

---

## Форма избыточности и live-тест

| Option | Description | Selected |
|--------|-------------|----------|
| 2 именованных сервиса (Рекомендую) | webhook-worker-1/-2 в compose; SC1 стоп image-worker, SC2 kill одного mid-delivery, SC3 ровно один под lock | ✓ |
| replicas через deploy.mode | deploy.replicas:2; docker-compose без swarm игнорирует без --compatibility, непредсказуемо для e2e | |

**User's choice:** 2 именованных сервиса

| Option | Description | Selected |
|--------|-------------|----------|
| Опереться на asynq (Рекомендую) | at-least-once (lease-таймаут переотдаёт task) + существующая идемпотентность (webhook_deliveries + asynq.Unique); переиспользовать internal/webhook вербатим | ✓ |
| Доп. гарантии | свой in-flight tracking/дедуп сверх asynq; больше кода, обычно не нужно | |

**User's choice:** Опереться на asynq

---

## Конфиг нового бинарника

| Option | Description | Selected |
|--------|-------------|----------|
| Только webhook-нужное (Рекомендую) | Postgres+Redis+WEBHOOK_SIGNING_SECRET+WEBHOOK_ALLOW_PRIVATE_IPS+sweep-env+concurrency; БЕЗ S3/convert/engine-таймаутов/presign; свой чистый Dockerfile | ✓ |
| You decide | точный набор на усмотрение планировщика | |

**User's choice:** Только webhook-нужное

---

## Claude's Discretion

- Числовой ключ advisory-lock и способ держать выделенное соединение
- Судьба WEBHOOK_SIGNING_SECRET/sweeper-env в cmd/worker/document-worker после снятия webhook-роли
- Точная compose-e2e-топология (стоп image-worker, kill одного webhook-worker)
- WEBHOOK_WORKER_CONCURRENCY/sweep-интервалы значения
- Нужна ли миграция БД (advisory-lock рантайм, вероятно нет)

## Deferred Ideas

- K8s + KEDA автоскейл webhook-worker'ов
- Вынос sweeper в собственный cmd/reconciler-singleton (отвергнуто в пользу advisory-lock)
- Своё in-flight webhook tracking сверх asynq
