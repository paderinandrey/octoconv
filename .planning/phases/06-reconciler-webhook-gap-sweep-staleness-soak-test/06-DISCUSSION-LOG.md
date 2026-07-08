# Phase 6: Reconciler Webhook-Gap Sweep & Staleness Soak Test - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-07-08
**Phase:** 6-Reconciler Webhook-Gap Sweep & Staleness Soak Test
**Areas discussed:** Защита от двойной доставки при гонке свипов, Порог staleness для детекции гапа, Наблюдаемость детекции гапа, Подход к soak-тесту (RECON-05)

---

## Защита от двойной доставки при гонке свипов

| Option | Description | Selected |
|--------|-------------|----------|
| Добавить asynq.Unique на webhook-очередь | Зеркалит Phase 3's ImageUniqueTTL; ErrDuplicateTask всегда | ✓ |
| Принять узкий остаточный риск | Без Unique-лока, гонка возможна но узкая | |

**User's choice:** Добавить asynq.Unique
**Notes:** Решение защищает не только gap-sweep, но и любой будущий код-путь, который мог бы вызвать EnqueueWebhookDeliver дважды.

---

## Порог staleness для детекции гапа

| Option | Description | Selected |
|--------|-------------|----------|
| Такой же порог, как ActiveStaleAfter | Переиспользовать существующий конфиг | ✓ |
| Отдельный порог поменьше (1-2 мин) | Новый env var, точнее для этого конкретного случая | |

**User's choice:** ActiveStaleAfter
**Notes:** Меньше новых env var, единое понятное число для всех staleness-порогов reconciler'а.

---

## Наблюдаемость детекции гапа

| Option | Description | Selected |
|--------|-------------|----------|
| job_events + Prometheus-счётчик | Новое значение action в существующем RecordReconcilerAction | ✓ |
| Только job_events | Без изменений в internal/metrics | |

**User's choice:** job_events + метрика
**Notes:** Согласуется с уже видимыми recovered/exhausted значениями.

---

## Подход к soak-тесту (RECON-05)

| Option | Description | Selected |
|--------|-------------|----------|
| Автоматический Go-тест с короткими реальными таймаутами | Реальный time.Sleep, живая БД, секунды не минуты | ✓ |
| Ручной runbook | Не автоматизировано, не в тестовом сьюте | |

**User's choice:** Автоматический тест
**Notes:** Гонится в CI, но не занимает продакшн-scale минуты.

---

## Claude's Discretion

- Точное имя/формула деривации WebhookUniqueTTL
- Точное имя repo-метода для поиска webhook-гапов
- Точная форма detail JSON и строковое значение action-лейбла
- Точные короткие значения таймаутов в soak-тесте

## Deferred Ideas

- Отдельный новый staleness-порог для webhook-гапов (вместо переиспользования ActiveStaleAfter)
- Circuit breaker / ручной replay для webhook-доставки
- Re-resolve/re-validate callback_url перед каждой попыткой доставки
