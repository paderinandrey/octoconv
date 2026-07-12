# OctoConv

## What This Is

OctoConv — внутренний асинхронный сервис конвертации файлов на Go для сервисов компании. Клиент отправляет файл через API, сервис кладёт его в S3-совместимое хранилище, ставит задачу в очередь (asynq/Redis), воркер запускает внешний движок конвертации и складывает результат обратно в S3. На `main` — три production-hardened класса движков: изображения через libvips (v1.0/v1.1), офисные документы через LibreOffice headless — включая кросс-конвертацию docx↔odt/xlsx↔ods/pptx↔odp и PDF/A-2b экспорт через validated opts (v1.2/v1.3), и HTML→PDF через chromium-headless с офлайн-рендерингом (v1.3). Вокруг них: обязательная API-key аутентификация с ротацией, rate limiting, HMAC-подписанная webhook-доставка через выделенные избыточные webhook-воркеры (переживает деплой/падение любого engine-воркера; reconciler-sweeper выбирается через Postgres advisory lock), корректный transient/terminal retry per-engine, fail-closed валидация содержимого по magic bytes (включая отказ OLE-CFB legacy/encrypted входов), защита от decompression bomb, TTL-очистка хранилища и полная наблюдаемость (Prometheus-метрики, реальный health-check, asynqmon-дашборд).

## Core Value

Внутренние сервисы компании могут безопасно (через аутентификацию по API-ключу) и надёжно поставить задачу конвертации файла (изображения, офисные документы, HTML) и получить результат — без риска для стабильности или безопасности продакшена.

## Current State (after v1.3, shipped 2026-07-12)

**Shipped:** v1.3 Document Class v2 — 5 фаз (12–16), 17 планов, 44 задачи, ~2 дня. Документный класс перестал быть «только → PDF» (6 симметричных кросс-пар), появились validated opts + PDF/A-2b, третий engine-class HTML→PDF (chromium, офлайн-рендеринг с live-canary доказательством нулевых сетевых соединений), и webhook-доставка развязана с engine-воркерами (2 избыточных webhook-worker реплики, advisory-lock singleton sweeper, ~11s failover).

**Next milestone goals:** не определены — запустить `/gsd:new-milestone`. Кандидаты из отложенного: DOCV3-01..03 (veraPDF-валидация, CFB legacy-vs-encrypted различение, кастомные шрифты/CJK-RTL), новые классы движков (av/ffmpeg, archive, probe, CAD), K8s+KEDA, image E2E-тест, tech debt из v1.3-аудита (мёртвая webhook-обвязка в document/chromium-worker, fakeEnqueuer -race).

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
- ✓ SSRF-валидация `callback_url` снимает блокировку RFC1918 приватных адресов через явный флаг `WEBHOOK_ALLOW_PRIVATE_IPS`; loopback/link-local/metadata-endpoint остаются заблокированы всегда — Phase 5 (4/4 success criteria, live e2e verified)
- ✓ Reconciler дополнительно находит `done`/`failed` задачи с потерянным webhook enqueue (нет строк в `webhook_deliveries`) и инициирует ровно одну повторную доставку, защищено `asynq.Unique`-локом на webhook-очереди — Phase 6 (RECON-04, live e2e verified)
- ✓ Восстановление зависших `queued`/`active` задач подтверждено автоматическим soak-тестом на реальном прошедшем времени (не mock-часах) — Phase 6 (RECON-05)
- ✓ Защита от decompression bomb: zero-dependency парсеры заявленных размеров изображения для всех 5 форматов (png/jpg/webp/heic/tiff), настраиваемый лимит `MAX_IMAGE_PIXELS` (100МП по умолчанию) — Phase 7 (VALID-03, live e2e verified)
- ✓ Отдельная `document` asynq-очередь (`TypeDocumentConvert`/`QueueDocument`) по паттерну engine-class routing, derived unique-lock TTL и no-jitter retry-расписание, `EnqueueDocumentConvert` — Phase 10 (DOC-08)
- ✓ Reconciler маршрутизирует восстановление зависших задач по `jobs.engine` (image/document), с fail-closed skip и метрикой для нераспознанного engine — Phase 10 (DOC-09)
- ✓ Отдельный бинарник `cmd/document-worker` со своим `DOCUMENT_ENGINE_TIMEOUT`/`DOCUMENT_WORKER_CONCURRENCY`; истечение `DOCUMENT_ENGINE_TIMEOUT` классифицируется terminal (в отличие от image-движка, где таймаут — transient) — Phase 10 (DOC-07, DOC-08)
- ✓ Docker-образ разделён: `Dockerfile.worker` снова libvips-only, LibreOffice изолирован в `Dockerfile.document-worker` с tini как PID 1 — Phase 10 (DOC-07)
- ✓ Engine-aware API-роутинг: `handleCreateJob` выбирает очередь по контенту (`Converter.Engine()`/`Registry.EngineFor`), документы минуют image-only dimension-check; Content-Type parity для pdf и 6 документных форматов — Phase 11 (DOC-10, live e2e verified: все 6 пар docx/xlsx/pptx/odt/ods/odp → pdf + подписанный webhook)

- ✓ Кросс-конвертация внутри документного класса (docx↔odt, xlsx↔ods, pptx↔odp) через явную (source,target) filter-таблицу LibreOffice; выход валидируется тем же SniffContainer, что и вход — Phase 13 (CONV-01, CONV-02, live e2e verified, 6 пар)
- ✓ OLE-CFB входы (legacy binary doc/xls/ppt и запароленные OOXML) отклоняются одним чётким 422 до записи в S3/Postgres — Phase 13 (SAFE-01, live verified оба под-случая)
- ✓ Validated `opts`: закрытый allowlist (типизированная структура), клиентские байты никогда не попадают в argv движка (injection-тест); PDF/A-2b экспорт с worker-side OutputIntent-проверкой — Phase 14 (OPTS-01/02, verified 9/9, live PDF/A на LO 7.4)
- ✓ HTML→PDF через chromium-headless (третий engine-class): офлайн-рендеринг (live-canary: ноль сетевых соединений по всем векторам), JS отключён CSP-инъекцией, print-опции через тот же opts-механизм — Phase 15 (HTML-01/02/03, verified 4/4 + security 14/14)
- ✓ Webhook-доставка развязана с engine-воркерами: выделенный `cmd/webhook-worker` ×2 реплики — единственный consumer webhook-очереди; reconciler-sweeper ровно один на флот (Postgres advisory lock, mutex-guarded conn lifecycle после gap-closure 16-05); SC1-3 live-verified — Phase 16 (WEBH-01)
- ✓ Унаследованный tech debt v1.0–v1.2 закрыт (extra_hosts, engine-константы, E2E-таймауты, gofmt, compose-audit) — Phase 12 (DEBT-01..05)

### Active

<!-- Пусто — milestone v1.3 закрыт; следующий скоуп определит /gsd:new-milestone. -->

(нет активных требований — ожидается новый milestone)

### Out of Scope

- CAD-движок — открытый вопрос в спеке (нативные форматы: OSS vs коммерческий SDK vs cloud API), не решён — отложен
- Другие классы движков (av/ffmpeg, archive, probe) — следующий этап развития, не этот (HTML→PDF и кросс-конвертация документов переехали в Active скоуп v1.3)
- Полный контракт ядра (Handler/Capability/Input/Output/Progress) — решено расширять существующий `Converter`/`Registry` вместо рефакторинга (v1.2 — второй движок укладывается в текущую абстракцию)
- KEDA-автоскейл / полноценная Kubernetes-оркестрация — инфраструктурная задача вне фокуса кодовых фаз
- Публичный релиз и проверка имени (npm/PyPI/Docker Hub/домен) — сервис внутренний, не актуально

## Context

- Полная архитектура и зафиксированный стек задокументированы в Notion: «Сервис конвертации файлов — стек и архитектура (Go)» и «OctoConv — стек и модель данных» (там же — полный DDL модели данных: `clients`, `presets`, `jobs`, `job_inputs`, `job_outputs`, `job_events`, `webhook_deliveries`).
- Статус реализации на 2026-06-30 зафиксирован в Notion-странице «OctoConv — статус реализации» — сделан только image/libvips срез, 7 коммитов на ветке `feat/scaffold-and-infra`, не влито в `main`.
- Рядом существовавший каталог `octo-conv` (Rust-прототип) не используется — разошёлся со спекой; текущая реализация на Go написана с нуля.
- Клиенты сервиса — внутренние сервисы компании, не внешние потребители. Это снижает требования к публичной документации/биллингу, но не снимает требований к auth и rate limiting.
- **Milestone v1.0 (Hardening MVP) shipped 2026-07-08.** 4 фазы, 15 планов, ~38 задач, ~6100 строк Go, 9 дней. Полный отчёт: `.planning/milestones/v1.0-ROADMAP.md`, `.planning/milestones/v1.0-REQUIREMENTS.md`, `.planning/milestones/v1.0-MILESTONE-AUDIT.md`.
- **Milestone v1.1 (Tech Debt Cleanup) shipped 2026-07-08** (тот же день — короткий закрывающий milestone). 3 фазы, 7 планов, 13 задач, 2 дня разработки. Закрыл все 5 tech-debt пунктов из v1.0-аудита без единого переноса. Полный отчёт: `.planning/milestones/v1.1-ROADMAP.md`, `.planning/milestones/v1.1-REQUIREMENTS.md`, `.planning/milestones/v1.1-MILESTONE-AUDIT.md`.
- Изначальные технические долги из `.planning/codebase/CONCERNS.md` (single-attempt processing, отсутствие таймаута вне конвертации, статичный `/healthz`, отсутствие content-валидации) — все закрыты в рамках Phase 3/4.
- Схема БД полностью используется: `clients` (auth, Phase 1), `callback_url`/`webhook_deliveries` (Phase 2); `presets` остаются неиспользуемыми — вне скопа обоих milestone.
- v1.1-аудит (`v1.1-MILESTONE-AUDIT.md`) прошёл без блокеров и без tech debt (4/4 требования, 5/5 точек интеграции, живые smoke-тесты всех новых механизмов по отдельности и в комбинации против пересобранного docker-стека) — впервые за проект milestone закрылся с нулевым переносом.
- Code review при исполнении Phase 2 (v1.0) нашёл и сразу исправил 2 критических дефекта: webhook-доставка следовала HTTP-редиректам (SSRF-обход валидации `callback_url`) и off-by-one в расписании retry-backoff (сокращал заявленное ~30-минутное окно до ~16 минут). Оба исправления покрыты тестами.
- **Milestone v1.2 (Document Engine Class) shipped 2026-07-10.** 4 фазы (8–11), 13 планов (вкл. gap-closure 11-04), 71 коммит, +2754 строк Go (без .planning), ~2 дня. Второй класс движков: docx/xlsx/pptx/odt/ods/odp → PDF через LibreOffice headless в отдельном контейнере, live E2E по всем 6 парам. Аудит: 10/10 требований, 10/10 интеграционных связей. Полный отчёт: `.planning/milestones/v1.2-ROADMAP.md`, `-REQUIREMENTS.md`, `-MILESTONE-AUDIT.md`.
- **Milestone v1.3 (Document Class v2) shipped 2026-07-12.** 5 фаз (12–16), 17 планов (вкл. gap-closure 16-05), 147 коммитов, +4773/−145 строк (без .planning), ~2 дня. Аудит: 14/14 требований, 7/7 интеграционных проверок, 8/8 E2E-потоков. Полный отчёт: `.planning/milestones/v1.3-ROADMAP.md`, `-REQUIREMENTS.md`, `-MILESTONE-AUDIT.md`.
- Tech debt, перенесённый из v1.3-аудита (advisory): мёртвая webhook-обвязка в `cmd/document-worker`/`cmd/chromium-worker` (WR-02/WR-03 из 16-REVIEW); data race в `fakeEnqueuer` тест-хелпере при full-package `-race`; нет dedicated image E2E-теста; SEED-001 dormant.

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
| SSRF-валидация `callback_url` блокирует весь RFC1918/loopback без исключений | Принято в Phase 2 как безопасный дефолт | ✓ Good — Phase 5 добавила узкий opt-out только для RFC1918 (`WEBHOOK_ALLOW_PRIVATE_IPS`); loopback/link-local/metadata-endpoint остаются заблокированы всегда |
| Reconciler webhook-gap sweep: `asynq.Unique` на webhook-очереди с TTL, деривированным из реального retry-бюджета (зеркалит `ImageUniqueTTL`) | Защита от двойной доставки при гонке sweep-тиков; TTL должен учитывать jitter `WebhookRetryDelay`, иначе получится average-case, а не worst-case | ✓ Good — Phase 6, TTL=2477.5с подтверждён тестами, live-verified без дублей |
| Decompression-bomb защита: свои zero-dependency парсеры размеров вместо golang.org/x/image или shell-out в vipsheader | Согласуется с философией zero-new-deps из Phase 4; избегает нового process-exec surface в API | ✓ Good — Phase 7, все 5 форматов (включая HEIC) защищены одинаково, 0 новых зависимостей |
| CAD и остальные классы движков — вне скопа этого этапа | Открытый вопрос по CAD SDK не решён; остальные движки — следующий этап роста, не текущий hardening | — Pending |
| document-движок расширяет существующий `Converter`/`Registry`, а не вводит Handler/Capability/Input/Output контракт | Второй движок (LibreOffice) укладывается в текущую абстракцию без изменений; полноценный контракт остаётся отложен до появления реальной потребности (напр. progress-репортинга) | ✓ Good — v1.2: LibreOfficeConverter + `Engine()`/`EngineFor` вписались в реестр без ломки контракта; live E2E по всем 6 парам |
| HTML→PDF исключён из v1.2 | LibreOffice слабо рендерит современный CSS/JS; нужен отдельный chromium-based движок — самостоятельное решение, не расширение LibreOffice-движка | ✓ Good — реализован в v1.3 Phase 15 как третий engine-class по шаблону v1.2 |
| Кросс-конвертация через явную (source,target) filter-таблицу, а не generic вычисление фильтра | Явная таблица = проверяемый allowlist; generic вычисление рискует тихо включить непроверенные пары | ✓ Good — v1.3 Phase 13: 6 симметричных пар, все live-verified на LO 7.4 |
| OLE-CFB: один 422 на оба случая (legacy и encrypted), без парсинга CFB-директории | Оба случая всё равно неконвертируемы; различение требует настоящего CFB-парсера — отложено (DOCV3-02) | ✓ Good — v1.3 Phase 13: 8-байтовый magic-детект, live-verified |
| PDF/A: sanity-чек OutputIntent вместо полной ISO 19005 (veraPDF) валидации | veraPDF = Java-стек в контейнере воркера; для внутренних клиентов достаточно структурного маркера | ✓ Good — v1.3 Phase 14; полная валидация отложена (DOCV3-01) |
| Webhook-доставка: выделенный webhook-worker ×2 + Postgres advisory lock для singleton-sweeper (вместо leader election или фиксированного «главного» воркера) | Простейший примитив, дающий exactly-one-sweeper на флот без новых зависимостей; консьюмеры симметричны | ✓ Good — v1.3 Phase 16: SC1-3 live-verified, ~11s failover; conn-lifecycle гэпы (CR-01/WR-01) закрыты в 16-05 с mutex + -race тестом |
| Отдельный `cmd/document-worker` бинарник/контейнер вместо второго `asynq.Server` внутри image-воркера | Тяжёлый footprint LibreOffice не должен попадать в контейнер image-воркера; ресурсная изоляция по классам движков | ✓ Good — v1.2 Phase 10: Dockerfile.worker снова libvips-only, LibreOffice изолирован с tini как PID 1 |
| Engine-класс определяется по контент-детектированному формату (`EngineFor(detected, target)`), не по расширению файла | Расширение подконтрольно атакующему; magic-bytes/структурный sniff — единственный проверяемый факт | ✓ Good — v1.2 Phase 11: fail-closed default на нераспознанный engine, live-verified |
| Resource-exhaustion через сложный документ (DOC-V2-05) — accepted residual risk v1.2 | Митигируется только `DOCUMENT_ENGINE_TIMEOUT` + потолком конкуренции document-воркера; активный анализ сложности отложен | — Pending (принятый риск, пересмотреть при росте нагрузки) |
| `file://` residual read внутри chromium-worker — accepted residual risk v1.3 (Phase 15) | Live-tested (Plan 04, item 6): `<img src="file:///usr/share/pixmaps/debian-logo.png">` (world-readable, non-input file) успешно загрузился внутри рендера под USER nobody — passive subresource loads (img/link/script src) читают ЛЮБОЙ файл, доступный uid nobody, включая потенциально workDir других одновременно выполняющихся job'ов (0700 не изолирует общий UID). Активный `fetch()`/XHR к `file://` при этом блокируется самим Chromium — подтверждено отдельно. Матчит DOC-V2-05 precedent (internal-only clients trust model) | — Pending (принятый риск; митигация — bind-mount только собственного workDir job'а — отложена как будущая опция, не блокирует Phase 15) |
| tini как PID 1 в `Dockerfile.chromium-worker` — сохранён, несмотря на неподтверждённую живым тестом необходимость именно для этой invocation-формы | Live-tested (Plan 04, item 7): `runCommand`-точное поведение (SIGKILL всей process-группы через `-PGID`) НЕ оставило зомби-процессов ни с tini, ни без него (3 повтора без tini, 1 с tini) — вероятно потому что весь одномоментный SIGKILL убивает parent+children синхронно, а не оставляет осиротевших детей. Tini оставлен как defence-in-depth (совпадает с собственным биасом D-09 "keep it" + сигнал-форвардинг для graceful shutdown), изменений в Dockerfile не внесено | ✓ Good — поведение задокументировано честно, а не предположено; изменений нет |

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
*Last updated: 2026-07-12 after v1.3 milestone*
