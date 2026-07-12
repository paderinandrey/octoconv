# Requirements: OctoConv — Milestone v1.4 CI, Presets & Debt Cleanup

**Defined:** 2026-07-12
**Core Value:** Внутренние сервисы компании могут безопасно (через аутентификацию по API-ключу) и надёжно поставить задачу конвертации файла (изображения, офисные документы, HTML) и получить результат — без риска для стабильности или безопасности продакшена.

## v1 Requirements

Requirements for this milestone. Each maps to roadmap phases.

### CI Pipeline

- [ ] **CI-01**: Каждый push/PR проходит базовый gate — gofmt, go vet, go build, go test ./... (required check)
- [ ] **CI-02**: CI гоняет `go test ./... -race` полным пакетным прогоном — чистый (required; зависит от DEBT-07)
- [ ] **CI-03**: Все 5 Docker-образов (api, worker, document-worker, chromium-worker, webhook-worker) собираются в CI через bake по docker-compose.yml с gha layer-кэшем (per-target scope, required)
- [ ] **CI-04**: Live E2E: полный compose-стек поднимается в CI и `internal/e2e` проходит против него — advisory на PR, required на main; teardown через `if: always()`, логи стека выгружаются артефактом при падении, устаревшие раны отменяются concurrency-группой (зависит от DEBT-08)

### Presets

- [ ] **PRST-01**: Оператор управляет пресетами через `cmd/manage-presets` CLI (create / update / list / show / deactivate; scope system и client; без hard delete — зеркало manage-clients)
- [ ] **PRST-02**: Клиент может создать задачу с `preset=<name>` вместо `target_format`/`opts`; клиентский пресет затеняет системный с тем же именем
- [ ] **PRST-03**: `preset` и явные `target_format`/`opts` взаимоисключающи — оба сразу → 422; несуществующий/неактивный/чужой пресет → одинаковый 422 без утечки существования
- [ ] **PRST-04**: Резолвнутые из пресета opts проходят ту же fail-closed валидацию (ParseDocOpts/ParseHTMLOpts) при каждом использовании — сохранённым opts не доверяем; job фиксирует provenance в уже существующих колонках `jobs.preset_name`/`preset_version`

### Tech Debt

- [ ] **DEBT-06**: Мёртвая webhook-обвязка (webhook.NewRepo/NewDeliverer + чтение WEBHOOK_SIGNING_SECRET) удалена из cmd/document-worker и cmd/chromium-worker
- [ ] **DEBT-07**: fakeEnqueuer race-safe (mutex/atomic на счётчиках вызовов) — `go test ./internal/reconciler/... -race` чистый
- [ ] **DEBT-08**: Image (libvips) E2E-тест в internal/e2e — полный цикл upload → convert → download → webhook для image-движка

## v2 Requirements

Deferred to future release. Tracked but not in current roadmap.

### Document Class (carried from v1.3)

- **DOCV3-01**: Полная ISO 19005 (veraPDF) валидация PDF/A-выходов
- **DOCV3-02**: Различение «файл запаролен» vs «формат устарел» для CFB-входов (парсинг CFB-директории)
- **DOCV3-03**: Кастомные шрифты / расширенное покрытие CJK-RTL для HTML→PDF

### Presets

- **PRST-V2-01**: REST CRUD `/v1/presets` для self-service управления пресетами клиентами (в v1.4 — только операторский CLI)

## Out of Scope

Explicitly excluded. Documented to prevent scope creep.

| Feature | Reason |
|---------|--------|
| REST CRUD для пресетов | Внутренние клиенты + операторский CLI достаточно; API-поверхность и auth-осложнения не окупаются в v1.4 |
| CD (автодеплой) из CI | Нет продакшен-таргета деплоя; CI заканчивается на проверках и сборках |
| golangci-lint / сторонние линтеры | go vet — зафиксированный минимум проекта; добавление линтера = отдельное решение с прогоном по всему коду |
| Self-hosted runners | ubuntu-latest достаточен; free-disk шаг решает проблему места малой кровью |
| Новые классы движков (av/archive/probe/CAD) | Следующие milestone'ы |
| KEDA / Kubernetes | Инфраструктурный этап, вне текущего фокуса |

## Traceability

Which phases cover which requirements. Updated during roadmap creation.

| Requirement | Phase | Status |
|-------------|-------|--------|
| DEBT-06 | Phase 17 | Pending |
| DEBT-07 | Phase 17 | Pending |
| DEBT-08 | Phase 17 | Pending |
| PRST-01 | Phase 18 | Pending |
| PRST-02 | Phase 18 | Pending |
| PRST-03 | Phase 18 | Pending |
| PRST-04 | Phase 18 | Pending |
| CI-01 | Phase 19 | Pending |
| CI-02 | Phase 19 | Pending |
| CI-03 | Phase 19 | Pending |
| CI-04 | Phase 19 | Pending |

**Coverage:**
- v1 requirements: 11 total
- Mapped to phases: 11 ✓
- Unmapped: 0

---
*Requirements defined: 2026-07-12*
*Last updated: 2026-07-12 after roadmap creation (11/11 mapped across Phases 17-19)*
