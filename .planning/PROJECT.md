# OctoConv

## What This Is

OctoConv — внутренний асинхронный сервис конвертации файлов на Go для сервисов компании. Клиент отправляет файл через API, сервис кладёт его в S3-совместимое хранилище, ставит задачу в очередь (asynq/Redis), воркер запускает внешний движок конвертации и складывает результат обратно в S3. Сквозной вертикальный срез — конвертация изображений через libvips — на `main` и полностью production-hardened (v1.0, milestone shipped 2026-07-08): обязательная API-key аутентификация (salted-hash ключи, ротация без даунтайма), rate limiting (per-client + pre-auth IP-guard), push-доставка результата через подписанные HMAC-SHA256 webhook'и (bounded retry + backoff, dead-letter), корректный transient/terminal retry в воркере, автоматический reconciler для зависших задач, валидация содержимого файла по magic bytes, автоматическое удаление старых файлов из S3/MinIO по TTL, и полная наблюдаемость (Prometheus-метрики, реальный health-check, asynqmon-дашборд).

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
- ✓ Воркер различает transient/terminal ошибки конвертации; transient-ошибки реально ретраятся asynq'ом по собственному быстрому расписанию image-очереди (2с/5с/15с, `IMAGE_MAX_RETRY`), с `asynq.Unique`-локом против дублей — Phase 3 (5/5 must-haves, live e2e verified)
- ✓ Postgres-driven reconciler восстанавливает задачи, зависшие в `queued`/`active`, идемпотентно (enqueue-first + `asynq.ErrDuplicateTask`-guard), с ограничением числа попыток и terminal-fail + webhook по исчерпании, все действия в `job_events` — Phase 3
- ✓ Валидация содержимого файла по magic bytes (жёсткий список сигнатур под 5 зарегистрированных форматов) отклоняет несовпадения с 422 до записи в S3 — Phase 4 (5/5 must-haves, live e2e verified)
- ✓ MinIO ILM lifecycle-правило автоматически удаляет `uploads/`/`results/` по TTL (7 дней по умолчанию), без ручной очистки — Phase 4
- ✓ Prometheus-метрики (исходы задач, длительность, webhook-доставки, reconciler-действия, глубина очереди) на отдельном localhost-only `/metrics`; реальный `/healthz` (пинг Postgres/Redis/S3, 503 при деградации); asynqmon-дашборд для визуальной инспекции очереди — Phase 4

### Active

<!-- Milestone v1.0 (hardening) shipped 2026-07-08 — все требования этапа закрыты. Следующий milestone ещё не определён; запустить /gsd:new-milestone. Ниже — кандидаты, всплывшие в v1.0-MILESTONE-AUDIT.md как незакрытый tech debt, для рассмотрения при формировании следующего milestone. -->

- [ ] (кандидат) Пересмотреть SSRF-валидацию `callback_url`: сейчас блокирует весь RFC1918/loopback/link-local диапазон без исключений — при реальном деплое на внутренней сети компании (частные IP) это может сделать вебхуки недоставляемыми в принципе
- [ ] (кандидат) Reconciler: досвип для `done`/`failed` задач с непустым `callback_url`, но без записи в `webhook_deliveries` — закрывает узкую гонку потери вебхука при сбое Redis в момент завершения задачи (рекомендовано в Phase 2, не подхвачено Phase 3)
- [ ] (кандидат) Защита от decompression bomb — лимит заявленных размеров изображения при валидации содержимого (явно отложено как D-09 в Phase 4)

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
- **Milestone v1.0 (Hardening MVP) shipped 2026-07-08.** 4 фазы, 15 планов, ~38 задач, ~6100 строк Go, 9 дней. Полный отчёт: `.planning/milestones/v1.0-ROADMAP.md`, `.planning/milestones/v1.0-REQUIREMENTS.md`, `.planning/milestones/v1.0-MILESTONE-AUDIT.md`.
- Изначальные технические долги из `.planning/codebase/CONCERNS.md` (single-attempt processing, отсутствие таймаута вне конвертации, статичный `/healthz`, отсутствие content-валидации) — все закрыты в рамках Phase 3/4.
- Схема БД полностью используется: `clients` (auth, Phase 1), `callback_url`/`webhook_deliveries` (Phase 2); `presets` остаются неиспользуемыми — вне скопа v1.0.
- Milestone-аудит (`v1.0-MILESTONE-AUDIT.md`) прошёл без блокеров (24/24 требования, 14/14 точек интеграции, живой E2E-сценарий подтверждён), но зафиксировал 5 некритичных tech-debt пунктов — три перенесены в Active выше как кандидаты для следующего milestone, остальные два — soak-тест reconciler'а (Phase 3) и разовая ревизия `docker-compose.yml` на скрытые расхождения с `.env.example` (обнаружен и исправлен один реальный: отсутствовал `WEBHOOK_SIGNING_SECRET` у воркера с момента Phase 2, коммит `36b559b`).
- Code review при исполнении Phase 2 нашёл и сразу исправил 2 критических дефекта: webhook-доставка следовала HTTP-редиректам (SSRF-обход валидации `callback_url`) и off-by-one в расписании retry-backoff (сокращал заявленное ~30-минутное окно до ~16 минут). Оба исправления покрыты тестами.

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
| Все пункты hardening (auth, webhooks, reconciler, magic-bytes+TTL, наблюдаемость) — в v1 этого этапа, auth первым | Все критичны для production-готовности; различается только порядок реализации по убыванию риска | ✓ Good — все 4 фазы закрыты, milestone v1.0 shipped 2026-07-08, 24/24 требования, 0 блокеров |
| Retry-safety должен предшествовать reconciler'у внутри Phase 3 | Reconciler поверх однопопыточного воркера дублировал бы обработку задач | ✓ Good — оба закрыты в одной фазе, живой E2E подтвердил отсутствие дублей |
| Content validation, storage TTL и observability объединены в одну закрывающую фазу | Все три независимы друг от друга и от auth/webhook/reconciler критического пути | ✓ Good — Phase 4 закрыта одним блоком, 5 планов в 3 волнах |
| Detected-формат (не расширение) — источник истины для pair-check в Phase 4 | Расширение может лгать; magic bytes — единственный проверяемый факт о содержимом | ✓ Good — reorder подтверждён живым 422 на несовпадении |
| `/metrics` на отдельном localhost-only порту, а не на публичном `API_ADDR` | Операционные данные (глубина очереди, исходы задач) не должны быть доступны любому клиенту с сетевым доступом к API | ✓ Good — подтверждено: порт не публикуется на хост вообще |
| SSRF-валидация `callback_url` блокирует весь RFC1918/loopback без исключений | Принято в Phase 2 как безопасный дефолт | ⚠️ Revisit — milestone-аудит отметил риск: при реальном деплое на приватных IP компании это может сделать вебхуки недоставляемыми; см. Active |
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
*Last updated: 2026-07-08 after v1.0 (Hardening MVP) milestone complete*
