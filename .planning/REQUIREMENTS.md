# Requirements: OctoConv — Milestone v1.5 MCP Access & Document Fidelity

**Defined:** 2026-07-13
**Core Value:** Внутренние сервисы компании могут безопасно (через аутентификацию по API-ключу) и надёжно поставить задачу конвертации файла (изображения, офисные документы, HTML) и получить результат — без риска для стабильности или безопасности продакшена.

## v1 Requirements

Requirements for this milestone. Each maps to roadmap phases.

### MCP Server

- [ ] **MCP-01**: Агент конвертирует файл одним блокирующим вызовом `convert_file(path, target_format|preset, opts?)` — stdio-сервер (`cmd/mcp-server`, официальный modelcontextprotocol/go-sdk ≥v1.6.1), API-ключ из env, внутренний поллинг с progress-notification на каждом тике (30-мин stdio idle-окно)
- [ ] **MCP-02**: Результат конвертации — presigned URL + путь локально скачанного файла; байты файла никогда не инлайнятся в tool result
- [ ] **MCP-03**: `get_job_status(job_id)` / `download_result(job_id)` доступны для неблокирующего сценария
- [ ] **MCP-04**: `list_supported_formats` и `list_presets` (merged client+system view) доступны как tools
- [ ] **MCP-05**: Безопасность MCP-поверхности: API-ключ не появляется в tool results/error text; agent-supplied пути канонизируются (без traversal); stdout несёт только JSON-RPC (лог в stderr); ошибки API мапятся в `isError`-results, не в protocol errors

### Presets REST API

- [ ] **PRAPI-01**: Клиент управляет своими (client-scope) пресетами через `/v1/presets`: create / list / show / update / deactivate; scope и client_id берутся только из auth-контекста (узкий DTO без этих полей — mass-assignment невозможен); system-scope остаётся за операторским CLI
- [ ] **PRAPI-02**: REST-семантика зеркалит CLI через общий `internal/presets.Repo` (bump-on-update, единственная активная версия, no hard delete); 409 на дубль create; no-leak на чужие/несуществующие пресеты
- [ ] **PRAPI-03**: `GET /v1/formats` отдаёт поддерживаемые пары форматов и engine-классы (предпосылка MCP-04)

### CFB Classification

- [ ] **CFB-01**: OLE-CFB входы получают различённые 422 — «файл запаролен» (EncryptionInfo/EncryptedPackage стримы) vs «устаревший бинарный формат» (WordDocument/Workbook/PowerPoint Document) — через собственный bounded-парсер CFB-директории (ноль новых зависимостей; visited-set cycle-guard; неопознанная структура → прежний generic 422, fail-closed)
- [ ] **CFB-02**: CFB-парсер выдерживает Go native fuzzing (crash-free, bounded) — exit-gate фазы

### veraPDF Validation

- [ ] **PDFA-01**: PDF/A-2b выходы проходят полную ISO 19005 валидацию (veraPDF) в document-worker; non-compliant экспорт фейлится terminally (прецедент OutputIntent-проверки v1.3)
- [ ] **PDFA-02**: veraPDF упакован в Dockerfile.document-worker (multi-stage COPY из verapdf/cli, glibc-совместимость проверена живьём), вызывается через hardened exec (`runCommand`) со своим таймаутом; terminal-error signatures добавлены same-commit (D-04 дисциплина)

## v2 Requirements

Deferred to future release.

### MCP
- **MCPV2-01**: Streamable HTTP транспорт + контейнер в compose (общий внутренний MCP-эндпоинт)
- **MCPV2-02**: MCP resources в дополнение к tools (когда host-поддержка выровняется)

### Presets
- **PRAPIV2-01**: system-scope пресеты через REST (сейчас — CLI only)

### Document Class
- **DOCV3-03**: Кастомные шрифты / CJK-RTL для HTML→PDF (carried)

## Out of Scope

| Feature | Reason |
|---------|--------|
| MCP по HTTP/SSE | stdio покрывает разработчиков и агентов; сетевой MCP = новая auth-поверхность без запроса |
| veraPDF REST-sidecar | Начинаем с CLI-в-контейнере; daemon — только если JVM-cost провалит бюджет (зафиксировать замером) |
| CFB-библиотека (mscfb) | Решение синтеза: свой bounded-парсер — openmcdf-класс уязвимостей есть и у библиотек, скоуп узкий (имена стримов), zero-deps прецедент |
| Расшифровка запароленных файлов | Различаем и отклоняем — никогда не расшифровываем |
| MCP write-tools (создание пресетов и т.п.) | Read+convert only в v1.5 — минимальная поверхность для агентов |

## Traceability

| Requirement | Phase | Status |
|-------------|-------|--------|
| PRAPI-01 | Phase 20 | Pending |
| PRAPI-02 | Phase 20 | Pending |
| PRAPI-03 | Phase 20 | Pending |
| MCP-01 | Phase 21 | Pending |
| MCP-02 | Phase 21 | Pending |
| MCP-03 | Phase 21 | Pending |
| MCP-04 | Phase 21 | Pending |
| MCP-05 | Phase 21 | Pending |
| CFB-01 | Phase 22 | Pending |
| CFB-02 | Phase 22 | Pending |
| PDFA-01 | Phase 23 | Pending |
| PDFA-02 | Phase 23 | Pending |

**Coverage:**
- v1 requirements: 12 total
- Mapped to phases: 12 ✓
- Unmapped: 0

---
*Requirements defined: 2026-07-13*
*Last updated: 2026-07-13 after roadmap creation (v1.5 Phases 20-23)*
