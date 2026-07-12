# Phase 10: Document Worker & Reconciler Integration - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-07-09
**Phase:** 10-Document Worker & Reconciler Integration
**Areas discussed:** Docker-образ split, ресурсные лимиты, конкурентность document-worker

---

## Docker-образ split

Контекст: на milestone-уровне было зафиксировано решение — отдельный `cmd/document-worker` + свой Docker-образ, специально чтобы LibreOffice не попадал в контейнер image-worker'а. Но Phase 9 прагматично нарушил это: чтобы получить живой процесс `soffice` для доказательства process-group-kill (D-03), LibreOffice был добавлен прямо в `Dockerfile.worker` — тот самый образ, что собирает `cmd/worker` (движок изображений).

| Option | Selected |
|--------|----------|
| Откатить Dockerfile.worker к libvips-only, новый Dockerfile.document-worker | ✓ |
| Оставить как есть, cmd/document-worker строится из того же образа | |

**Notes:** Реализует исходное намерение полностью — image-worker снова лёгкий. Тест из Phase 9 (Dockerfile.worker-test) существует независимо от обоих runtime-образов и не требует переноса.

## Ресурсные лимиты document-worker

| Option | Selected |
|--------|----------|
| Такие же: 2 CPU / 1GiB (как у image-worker) | ✓ |
| Больше: 2 CPU / 2GiB | |

**Notes:** Не проверено эмпирически (нет реального корпуса документов) — стартовая точка, как и DOCUMENT_ENGINE_TIMEOUT=300с из Phase 9.

## Конкурентность document-worker

| Option | Selected |
|--------|----------|
| DOCUMENT_WORKER_CONCURRENCY отдельно от WORKER_CONCURRENCY | ✓ |
| Общий WORKER_CONCURRENCY | |

**Notes:** soffice.bin тяжелее по памяти/CPU на один job, чем libvips — нужен отдельный, вероятно меньший дефолт (research предполагал ~2 против 4 у image).

## Reconciler process topology

Pattern-mapper flagged this as an open question: с двумя воркер-бинарниками где живёт reconciler sweep?

| Option | Selected |
|--------|----------|
| Только в cmd/worker, становится engine-aware | ✓ |
| В обоих бинарниках | |

**Notes:** enqueue-first + asynq.ErrDuplicateTask-guard сделал бы двойной sweep безопасным, но избыточным (два DB-скана, лишняя работа). Один sweeper чище.

## Webhook-очередь: consumer в document-worker?

| Option | Selected |
|--------|----------|
| Нет, только продюсер (cmd/worker остаётся единственным consumer'ом) | ✓ |
| Да, document-worker тоже обрабатывает | |

Пользователь изначально предложил третий вариант — вынести webhook-доставку в отдельный `cmd/webhook-worker`. Обсудили: это ортогональная задача (не завязана на документы, применима одинаково к image/document), уже отмеченная как "дешёвая будущая миграция" в Phase 2 (v1.0) CONTEXT.md. Решили не расширять скоуп Phase 10 — зафиксировано как SEED-002.

## Claude's Discretion

- Точное значение DOCUMENT_WORKER_CONCURRENCY по умолчанию
- Точная деривация DocumentUniqueTTL/retry-расписания (зеркалит ImageUniqueTTL, но на базе DOCUMENT_ENGINE_TIMEOUT)
- Судьба Dockerfile.worker-test относительно нового Dockerfile.document-worker
- Копировать ли пакетный список Phase 9 вербатим или перепроверять заново

## Deferred Ideas

Нет — routing в handleCreateJob (Phase 11) не считается deferred scope creep, это следующая фаза по плану.
