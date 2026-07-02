# Requirements: OctoConv

**Defined:** 2026-07-02
**Core Value:** Внутренние сервисы компании могут безопасно и надёжно поставить задачу конвертации изображения и получить результат — без риска для стабильности или безопасности продакшена.

## v1 Requirements

Requirements for этого этапа (production-hardening image-среза). Каждое требование маппится на фазу roadmap.

### Baseline

- [ ] **BASE-01**: Ветка `feat/scaffold-and-infra` влита в `main` до начала hardening-работы

### Authentication

- [ ] **AUTH-01**: Клиент аутентифицируется в API через API-ключ, привязанный к записи в таблице `clients`
- [ ] **AUTH-02**: API отклоняет запросы с отсутствующим/неверным/отозванным ключом (401)
- [ ] **AUTH-03**: API возвращает 404 (не 403) для задач, принадлежащих другому клиенту — не подтверждает существование job_id постороннему вызывающему
- [ ] **AUTH-04**: API-ключи хранятся только в виде хешей (salted SHA-256), plaintext нигде не сохраняется и не логируется
- [ ] **AUTH-05**: Схема `clients` поддерживает два одновременно активных ключа на клиента для ротации без даунтайма

### Reliability

- [ ] **RELY-01**: Воркер различает transient-ошибки (сетевые/таймауты) и terminal-ошибки (невалидный вход, неподдерживаемый формат) при сбое конвертации
- [ ] **RELY-02**: При transient-ошибке job не помечается terminal-failed — retry средствами asynq реально происходит (сейчас каждая задача получает ровно одну попытку независимо от конфигурации retry)

### Rate Limiting

- [ ] **RATE-01**: API применяет per-client rate limit (token bucket) на создание задач, ключ — `client_id`, не IP
- [ ] **RATE-02**: При превышении лимита API возвращает 429 с заголовком `Retry-After`
- [ ] **RATE-03**: Перед auth-мидлварой применяется грубый pre-auth IP-based rate limit как защита от флуда до похода в БД

### Reconciler

- [ ] **RECON-01**: Периодический reconciler находит задачи, зависшие в `queued` без соответствующей задачи в очереди, и переставляет их в очередь (идемпотентно, без дублей)
- [ ] **RECON-02**: Reconciler находит задачи, зависшие в `active` дольше порога (воркер упал), и не дублирует обработку легитимно медленной задачи — только реально зависшие
- [ ] **RECON-03**: Действия reconciler'а (восстановленные, terminal-failed задачи) фиксируются в `job_events`

### Webhooks

- [ ] **WEBHOOK-01**: При завершении задачи (`done`/`failed`) с непустым `callback_url` сервис доставляет вебхук вместо необходимости поллинга
- [ ] **WEBHOOK-02**: Payload вебхука подписан HMAC-SHA256 с меткой времени для защиты от replay-атак
- [ ] **WEBHOOK-03**: Недоставленные вебхуки повторяются с exponential backoff + jitter, с ограниченным числом попыток
- [ ] **WEBHOOK-04**: Каждая попытка доставки фиксируется в `webhook_deliveries` (статус, номер попытки, HTTP-код ответа)
- [ ] **WEBHOOK-05**: После исчерпания попыток доставка помечается terminal (dead-letter), не удаляется молча — доступна для ручного расследования

### Content Validation

- [ ] **VALID-01**: API проверяет содержимое загруженного файла по magic bytes перед сохранением/обработкой
- [ ] **VALID-02**: При несовпадении определённого по содержимому формата с заявленным (расширение/Content-Type) API отклоняет запрос (422) до записи в S3

### Storage Lifecycle

- [ ] **STOR-01**: Загруженные файлы и результаты в S3/MinIO автоматически удаляются по истечении срока хранения (lifecycle TTL на `uploads/` и `results/`)

### Observability

- [ ] **OBS-01**: Сервис экспортирует Prometheus-метрики (глубина очереди, исходы задач, успешность доставки вебхуков)
- [ ] **OBS-02**: Health-эндпоинт реально проверяет доступность Postgres, Redis и S3/MinIO, а не возвращает статичный `{"status":"ok"}`
- [ ] **OBS-03**: Разворачивается asynqmon-дашборд для визуальной инспекции очереди

## v2 Requirements

Отложено на будущее — после того как v1 этого этапа отработает на реальной нагрузке.

### Webhooks

- **WEBHOOK-V2-01**: Per-client ротация webhook-секретов (отдельных от API-ключа)
- **WEBHOOK-V2-02**: Инструмент ручного replay для неудавшихся доставок вебхуков

### Rate Limiting

- **RATE-V2-01**: Многоуровневые (tiered) rate-limit планы на клиента

### Reliability

- **RELY-V2-01**: Idempotency-ключ при создании задачи (защита от дублей при retry со стороны клиента)

### Scaling

- **SCALE-V2-01**: Priority-очереди / fairness между клиентами в общей очереди
- **SCALE-V2-02**: OpenTelemetry distributed tracing через API → очередь → воркер → S3
- **SCALE-V2-03**: Переход на transactional outbox вместо reactive-sweeper reconciler (только если у sweeper'а обнаружатся неприемлемые false-negative/latency характеристики на практике)

## Out of Scope

Явно исключено из этого этапа. Зафиксировано в PROJECT.md.

| Feature | Reason |
|---------|--------|
| CAD-движок | Открытый вопрос по SDK (OSS vs commercial vs cloud API) не решён |
| Другие классы движков (document/LibreOffice, av/ffmpeg, archive, probe) | Следующий этап роста, не hardening текущего среза |
| Контракт ядра (Handler/Capability/Input/Output/Progress) | Рефакторинг откладывается до момента добавления новых движков |
| KEDA-автоскейл / полноценная Kubernetes-оркестрация | Инфраструктурная задача вне фокуса кодовых фаз |
| Публичный релиз (проверка имени, документация для внешних клиентов) | Сервис только для внутренних клиентов компании |
| Публичный developer-портал / self-service key management UI | Нет внешних клиентов, которым это нужно |
| Usage-based billing / метеринг | Нет монетизации для внутренних клиентов |
| Real-time статус через WebSocket/SSE | Вебхуки + существующий polling полностью закрывают потребность |
| mTLS / OAuth2 client-credentials auth | Несоразмерная сложность для закрытой внутренней сети; API-ключ уже поднимает планку выше текущего полностью публичного состояния |

## Traceability

| Requirement | Phase | Status |
|-------------|-------|--------|
| BASE-01 | Phase 1 | Pending |
| AUTH-01 | Phase 1 | Pending |
| AUTH-02 | Phase 1 | Pending |
| AUTH-03 | Phase 1 | Pending |
| AUTH-04 | Phase 1 | Pending |
| AUTH-05 | Phase 1 | Pending |
| RATE-01 | Phase 1 | Pending |
| RATE-02 | Phase 1 | Pending |
| RATE-03 | Phase 1 | Pending |
| WEBHOOK-01 | Phase 2 | Pending |
| WEBHOOK-02 | Phase 2 | Pending |
| WEBHOOK-03 | Phase 2 | Pending |
| WEBHOOK-04 | Phase 2 | Pending |
| WEBHOOK-05 | Phase 2 | Pending |
| RELY-01 | Phase 3 | Pending |
| RELY-02 | Phase 3 | Pending |
| RECON-01 | Phase 3 | Pending |
| RECON-02 | Phase 3 | Pending |
| RECON-03 | Phase 3 | Pending |
| VALID-01 | Phase 4 | Pending |
| VALID-02 | Phase 4 | Pending |
| STOR-01 | Phase 4 | Pending |
| OBS-01 | Phase 4 | Pending |
| OBS-02 | Phase 4 | Pending |
| OBS-03 | Phase 4 | Pending |

**Coverage:**
- v1 requirements: 25 total (corrected count — original summary undercounted by 1; 9 categories × items = 1+5+2+3+3+5+2+1+3 = 25)
- Mapped to phases: 25/25
- Unmapped: 0 ✓

---
*Requirements defined: 2026-07-02*
*Last updated: 2026-07-02 after initial definition*
