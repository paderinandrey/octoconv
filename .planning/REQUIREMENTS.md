# Requirements: OctoConv — Milestone v1.3 Document Class v2

**Defined:** 2026-07-10
**Core Value:** Внутренние сервисы компании могут безопасно (через аутентификацию по API-ключу) и надёжно поставить задачу конвертации файла и получить результат — без риска для стабильности или безопасности продакшена.

## v1 Requirements

Requirements for this milestone. Each maps to roadmap phases.

### Tech Debt

- [x] **DEBT-01**: E2E-suite работает на plain-Linux docker: `docker-compose.e2e.yml` даёт `api`-сервису `extra_hosts: host.docker.internal:host-gateway`, webhook-пара E2E проходит вне Docker Desktop (WR-02 из 11-REVIEW.md)
- [x] **DEBT-02**: Engine-class литералы ("image"/"document"/…) определены как экспортированные константы в `internal/convert` и используются API, reconciler'ом и воркерами вместо дублированных строк (WR-03)
- [x] **DEBT-03**: E2E HTTP-клиенты имеют per-request таймауты — зависший API/download endpoint даёт диагностируемое падение теста, а не `go test` binary-timeout panic (WR-04)
- [x] **DEBT-04**: `gofmt -l ./...` не находит файлов (nit в `internal/queue/queue_test.go`, тянется с Phase 9/10)
- [x] **DEBT-05**: docker-compose.yml сверен с `.env.example` — каждая документированная env-переменная либо прокинута в соответствующий сервис, либо расхождение явно обосновано (перенос из v1.0 close)

### Cross-Format Conversion

- [x] **CONV-01**: Клиент может конвертировать docx↔odt, xlsx↔ods, pptx↔odp через существующий `POST /v1/jobs` поток (upload → convert → download → webhook), тем же LibreOffice-движком
- [x] **CONV-02**: Невалидный/битый не-PDF выход конвертации детектится структурно (проверка контейнера по ожидаемому целевому формату) до пометки задачи `done` — terminal-ошибка, не ложный успех

### Input Safety

- [x] **SAFE-01**: OLE-CFB файлы (сигнатура `D0 CF 11 E0 A1 B1 1A E1` — legacy doc/xls/ppt и запароленные OOXML) отклоняются с чётким 422 до записи в S3; различение legacy vs encrypted — вне скоупа (см. Out of Scope)

### Conversion Options

- [x] **OPTS-01**: Клиент может передать валидируемые опции конвертации (`opts`) в `POST /v1/jobs`; opts — закрытый allowlist (типизированная Go-структура), невалидные значения → 422, клиентские байты никогда не попадают сырыми в CLI-аргументы/filter-JSON движка
- [x] **OPTS-02**: Клиент может запросить PDF/A-2b вариант экспорта для document→pdf через opts; выходной PDF несёт PDF/A OutputIntent-маркер (sanity-чек; полная ISO 19005-валидация veraPDF — принятый residual risk)

### HTML→PDF Engine

- [ ] **HTML-01**: Клиент может конвертировать загруженный HTML-файл в PDF через отдельный chromium-based движок — третий engine-class: своя asynq-очередь, свой воркер-бинарник/контейнер, свой таймаут с terminal-классификацией (по шаблону document-класса v1.2; требует миграции CHECK-констрейнта `jobs.engine`)
- [ ] **HTML-02**: HTML-рендеринг офлайн: движок не может фетчить внешние сетевые ресурсы, на которые ссылается HTML (SSRF-safe); входного режима URL-fetch не существует
- [ ] **HTML-03**: Базовые print-опции (размер страницы, поля, printBackground) доступны через тот же validated-opts механизм, что и OPTS-01

### Webhook Reliability

- [ ] **WEBH-01**: Webhook-доставка работает при деплое любого подмножества engine-воркеров — отсутствие `cmd/worker` (image) не приводит к молчаливой потере вебхуков; выживает падение любого одного процесса

## v2 Requirements

Deferred to future release. Tracked but not in current roadmap.

### Document Class

- **DOCV3-01**: Полная ISO 19005 (veraPDF) валидация PDF/A-выходов
- **DOCV3-02**: Различение «файл запаролен» vs «формат устарел» для CFB-входов (парсинг CFB-директории)
- **DOCV3-03**: Кастомные шрифты / расширенное покрытие CJK-RTL для HTML→PDF

## Out of Scope

Explicitly excluded. Documented to prevent scope creep.

| Feature | Reason |
|---------|--------|
| URL-fetch вход для HTML→PDF | Открывает SSRF-поверхность страшнее webhook'овой: полноценный браузер исполняет ответ цели; file-upload-only |
| veraPDF в document-worker контейнере | Java-стек, заметное утяжеление образа и времени конвертации; sanity-чек OutputIntent достаточен для внутренних клиентов |
| Различение legacy vs encrypted CFB | Требует настоящего CFB-парсера (свой код или зависимость); оба случая всё равно неконвертируемы — один 422 достаточен |
| Анти-DoS по сложности документа (DOC-V2-05) | Остаётся принятым residual risk v1.2: mitigation — engine-таймауты + потолок конкуренции воркеров |
| CAD/AV/archive/probe движки | Следующие классы — отдельные milestone'ы |

## Traceability

Which phases cover which requirements. Updated during roadmap creation.

| Requirement | Phase | Status |
|-------------|-------|--------|
| DEBT-01 | Phase 12 | Complete |
| DEBT-02 | Phase 12 | Complete |
| DEBT-03 | Phase 12 | Complete |
| DEBT-04 | Phase 12 | Complete |
| DEBT-05 | Phase 12 | Complete |
| CONV-01 | Phase 13 | Complete |
| CONV-02 | Phase 13 | Complete |
| SAFE-01 | Phase 13 | Complete |
| OPTS-01 | Phase 14 | Complete |
| OPTS-02 | Phase 14 | Complete |
| HTML-01 | Phase 15 | Pending |
| HTML-02 | Phase 15 | Pending |
| HTML-03 | Phase 15 | Pending |
| WEBH-01 | Phase 16 | Pending |

**Coverage:**
- v1 requirements: 14 total
- Mapped to phases: 14/14 ✓
- Unmapped: 0

---
*Requirements defined: 2026-07-10*
*Last updated: 2026-07-10 after roadmap creation (Phases 12-16 mapped, 14/14 coverage)*
