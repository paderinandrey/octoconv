# OctoConv

## What This Is

OctoConv — внутренний асинхронный сервис конвертации файлов на Go для сервисов компании. Клиент отправляет файл через API, сервис кладёт его в S3-совместимое хранилище, ставит задачу в очередь (asynq/Redis), воркер запускает внешний движок конвертации и складывает результат обратно в S3. Реализован сквозной вертикальный срез — конвертация изображений через libvips — на `main`, теперь с обязательной API-key аутентификацией (salted-hash ключи, ротация без даунтайма), rate limiting (per-client + pre-auth IP-guard) и push-доставкой результата через подписанные HMAC-SHA256 webhook'и (bounded retry + backoff, dead-letter). Остальные пункты hardening (reconciler, content validation, storage TTL, observability) — впереди.

## Core Value

Внутренние сервисы компании могут безопасно (через аутентификацию по API-ключу) и надёжно поставить задачу конвертации изображения и получить результат — без риска для стабильности или безопасности продакшена.

## Requirements

### Validated

<!-- Существующий код, вертикальный срез image/libvips на ветке feat/scaffold-and-infra. -->

- ✓ API принимает файл через multipart `POST /v1/jobs`, валидирует пару форматов (422 при неподдерживаемой) и лимит размера (413), ставит задачу в очередь — existing
- ✓ Воркер конвертирует изображения (png/jpg/webp/heic/tiff) через libvips, запуская внешний бинарник с таймаутом и убийством всей process group — existing
- ✓ Жизненный цикл задачи отслеживается в PostgreSQL (`queued → active → done/failed`) с append-only журналом переходов (`job_events`) — existing
- ✓ `GET /v1/jobs/{id}` отдаёт статус и presigned download URL готового результата — existing
- ✓ Graceful shutdown API и воркера — existing
- ✓ Ветка `feat/scaffold-and-infra` влита в `main` — Phase 1 (уже была слита до начала фазы, подтверждено при планировании)
- ✓ API-ключи для клиентов через таблицу `clients` (`cmd/manage-clients` CLI: create/add-key/revoke), salted SHA-256 хеш, два активных слота на ротацию без даунтайма — Phase 1
- ✓ Обязательная аутентификация на всех `/v1/*` (hard cutover, 401), `/healthz` остаётся публичным, cross-client доступ → 404 (никогда 403) — Phase 1
- ✓ Rate limiting: per-client лимит по `client_id` (429 + Retry-After) и pre-auth IP-guard (`middleware.ClientIPFromRemoteAddr`, не спуфится) — Phase 1
- ✓ Webhook-доставка результата (`jobs.callback_url` + `webhook_deliveries`) вместо поллинга статуса: HMAC-SHA256-подписанный payload с timestamp, bounded retry (`MaxRetry=6`, экспоненциальный backoff + jitter, ~30 мин окно), dead-letter после исчерпания попыток, каждая попытка доставки записана в `webhook_deliveries` — Phase 2 (12/12 must-haves, live e2e verified)

### Active

<!-- Следующий этап: Phase 3 — Retry-Safety & Reconciler. -->

- [ ] Reconciler/свипер задач, зависших в `queued` без работы в очереди (и наоборот)
- [ ] Валидация содержимого файла по magic bytes вместо доверия расширению/Content-Type
- [ ] Lifecycle TTL на бакете S3/MinIO для автоудаления `uploads/` и `results/`
- [ ] Метрики и наблюдаемость (asynqmon + Prometheus)

### Out of Scope

- CAD-движок — открытый вопрос в спеке (нативные форматы: OSS vs коммерческий SDK vs cloud API), не решён — отложен
- Другие классы движков (document/LibreOffice, av/ffmpeg, archive, probe) — следующий этап развития, не этот
- Контракт ядра (Handler/Capability/Input/Output/Progress) — рефакторинг откладывается до момента добавления новых движков, чтобы не переделывать дважды
- KEDA-автоскейл / полноценная Kubernetes-оркестрация — инфраструктурная задача вне фокуса кодовых фаз
- Публичный релиз и проверка имени (npm/PyPI/Docker Hub/домен) — сервис внутренний, не актуально

## Context

- Полная архитектура и зафиксированный стек задокументированы в Notion: «Сервис конвертации файлов — стек и архитектура (Go)» и «OctoConv — стек и модель данных» (там же — полный DDL модели данных: `clients`, `presets`, `jobs`, `job_inputs`, `job_outputs`, `job_events`, `webhook_deliveries`).
- Статус реализации на 2026-06-30 зафиксирован в Notion-странице «OctoConv — статус реализации» — сделан только image/libvips срез, 7 коммитов на ветке `feat/scaffold-and-infra`, не влито в `main`.
- Рядом существовавший каталог `octo-conv` (Rust-прототип) не используется — разошёлся со спекой; текущая реализация на Go написана с нуля.
- Клиенты сервиса — внутренние сервисы компании, не внешние потребители. Это снижает требования к публичной документации/биллингу, но не снимает требований к auth и rate limiting.
- Известные технические долги и риски подробно задокументированы в `.planning/codebase/CONCERNS.md`: single-attempt job processing маскирует transient failures как terminal (asynq retry фактически не работает), нет таймаута на storage/DB вызовы вне шага конвертации, `/healthz` не проверяет зависимости, нет CI pipeline, нет теста реальной libvips-конвертации, HEIC-поддержка в образе воркера не подтверждена явно.
- Схема БД шире, чем используемый код: `clients`, `callback_url`, `webhook_deliveries` теперь полностью используются (auth — Phase 1, webhooks — Phase 2); `presets` остаются неиспользуемыми.
- Code review при исполнении Phase 2 нашёл и сразу исправил 2 критических дефекта: webhook-доставка следовала HTTP-редиректам (SSRF-обход валидации `callback_url`) и off-by-one в расписании retry-backoff (сокращал заявленное ~30-минутное окно до ~16 минут). Оба исправления покрыты тестами. 4 warning + 3 info найдены и оставлены как некритичный follow-up (см. `.planning/phases/02-webhook-delivery/02-REVIEW.md`): тихо отбрасываемые ошибки на hot path доставки, отсутствие таймаута на DNS-резолвинг в SSRF-проверке, конструктор с 9 позиционными параметрами.

## Constraints

- **Tech stack**: Go 1.26, chi (API), asynq + Redis (очередь), PostgreSQL 18 (система записи), S3/MinIO (хранилище) — зафиксировано в Notion-спеке, не пересматривается на этом этапе
- **Auth**: API-ключи через существующую таблицу `clients` — не вводить отдельный внешний auth-провайдер
- **Deployment**: Docker / docker-compose для локальной разработки; Kubernetes + KEDA — будущее, вне текущего фокуса
- **Сlients**: только внутренние сервисы компании — публичная многоарендность и биллинг не требуются на этом этапе

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Слить `feat/scaffold-and-infra` в `main` в начале этапа | Дальнейший hardening должен идти поверх `main`, а не в изолированной ветке | ✓ Good — уже была слита к моменту планирования Phase 1 |
| Auth + rate limiting — первый приоритет hardening | API сейчас полностью публичный без аутентификации — самый большой риск | ✓ Good — Phase 1 закрыта, 12/12 must-haves, включая gap-closure по spoofable IP-guard |
| Все пункты hardening (auth, webhooks, reconciler, magic-bytes+TTL, наблюдаемость) — в v1 этого этапа, auth первым | Все критичны для production-готовности; различается только порядок реализации по убыванию риска | ◐ In Progress — Phase 1 (auth/rate-limit) и Phase 2 (webhooks) закрыты |
| CAD и остальные классы движков — вне скопа этого этапа | Открытый вопрос по CAD SDK не решён; остальные движки — следующий этап роста, не текущий hardening | — Pending |
| Контракт ядра (Handler/Capability/Input/Output) отложен | Рефакторинг делать при добавлении новых движков, а не сейчас — иначе придётся переделывать дважды | — Pending |

## Evolution

This document evolves at phase transitions and milestone boundaries.

**After each phase transition** (via `/gsd-transition`):
1. Requirements invalidated? → Move to Out of Scope with reason
2. Requirements validated? → Move to Validated with phase reference
3. New requirements emerged? → Add to Active
4. Decisions to log? → Add to Key Decisions
5. "What This Is" still accurate? → Update if drifted

**After each milestone** (via `/gsd:complete-milestone`):
1. Full review of all sections
2. Core Value check — still the right priority?
3. Audit Out of Scope — reasons still valid?
4. Update Context with current state

---
*Last updated: 2026-07-04 after Phase 2 (Webhook Delivery) complete*
