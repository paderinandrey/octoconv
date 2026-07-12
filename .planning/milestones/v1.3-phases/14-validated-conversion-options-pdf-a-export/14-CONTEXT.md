# Phase 14: Validated Conversion Options & PDF/A Export - Context

**Gathered:** 2026-07-10
**Status:** Ready for planning

<domain>
## Phase Boundary

Клиенты могут безопасно передавать опции конвертации через `opts` в `POST /v1/jobs` — закрытый allowlist (типизированная Go-структура), невалидные значения → 422, клиентские байты никогда не попадают сырыми в CLI-аргументы/filter-JSON движка. Первый реальный потребитель механизма — PDF/A-2b экспорт для document→pdf задач с проверяемым OutputIntent-маркером. Требования: OPTS-01, OPTS-02. Зависит от Phase 13 (переиспользует target-format-aware `validateDocumentOutput`).

</domain>

<decisions>
## Implementation Decisions

### API shape of opts
- **D-01:** PDF/A запрашивается enum-профилем: `opts = {"pdf_profile": "pdf/a-2b"}`. Закрытый enum с единственным допустимым значением сейчас (`pdf/a-2b`); `pdf/a-1b`/`pdf/a-3b` — будущие расширения без ломки API (research: near-free follow-on, тот же `SelectPdfVersion` enum). Boolean-флаг отвергнут.
- **D-02:** `opts` едет одним multipart form-полем `opts` с JSON-строкой. Парсинг через `json.Decoder` с `DisallowUnknownFields` в типизированную Go-структуру — одна точка валидации, естественно ложится в `jobs.options jsonb`, масштабируется на opts HTML-движка в Phase 15 (HTML-03 использует тот же механизм).

### Applicability validation
- **D-03:** Опция, неприменимая к (engine, target) задачи — например `pdf_profile` при image-задаче или docx→odt — даёт 422 fail-closed ДО записи в S3/Postgres, как все существующие content-отказы. Молчаливый игнор отвергнут: скрывает ошибку клиента (ждал PDF/A — получил обычный вывод).
- **D-04:** Валидация двухшаговая: (1) синтаксис — JSON→struct + допустимость значений; (2) применимость — сразу после существующей format-pair валидации в `handleCreateJob`, когда (engine, source, target) уже известны. Логика применимости живёт в `internal/convert` рядом с таблицей пар; API-слой только вызывает её.

### PDF/A output verification (worker-side)
- **D-05:** `validateDocumentOutput` расширяется: при запрошенном `pdf_profile` произведённый PDF обязан нести `/GTS_PDFA`-OutputIntent-маркер; отсутствие маркера — terminal-ошибка задачи (без ретраев, детерминированный исход). Вариант «только e2e-assertion» отвергнут — регрессия LO молча отдавала бы обычный PDF под видом PDF/A.
- **D-06:** КРИТИЧНО (паттерн D-04 из Phase 13): текст новой terminal-ошибки добавляется в `terminalLibreOfficeSignatures` (internal/worker/worker.go) тем же коммитом, что и проверка — иначе битый PDF/A-выход будет бессмысленно ретраиться.
- **D-07:** Filter-JSON для soffice строится ТОЛЬКО из серверных констант по полям валидированной структуры: `pdf_profile: "pdf/a-2b"` → жёсткая пара `SelectPdfVersion=2` + `EmbedStandardFonts=true` (Pitfall 7 — версия без шрифтов даёт неконформный PDF/A). Клиентские байты не появляются в argv/filter-JSON ни при каком пути (Pitfall 9 — UNO filter-property injection, главная attack surface майлстоуна). Проверяется целевым injection-тестом (success criterion 1).

### Persistence & echo
- **D-08:** В `jobs.options` пишется сериализованная нормализованная структура, НЕ сырой клиентский JSON. Сырые байты не переживают `handleCreateJob` — единая точка доверия.
- **D-09:** Принятые opts возвращаются клиенту (нормализованная форма) в ответах `POST /v1/jobs` (201) и `GET /v1/jobs/{id}`; пустые opts — поле опускается (omitempty), существующие ответы без opts не меняются.
- **D-10:** Worker при чтении из БД делает строгий парсинг в ту же структуру (`DisallowUnknownFields`; мусор в колонке → terminal-ошибка), но НЕ дублирует бизнес-правила валидации (значения/применимость) — источник доверия один, API. Полная ре-валидация отвергнута (два места истины разъезжаются).

### Claude's Discretion
- Точная сигнатура и размещение opts-структуры (пакет, имя типа, как она проходит через `queue.ConvertPayload`/повторное чтение из Postgres).
- Точный вид OutputIntent-грепа (`/GTS_PDFA2` строго под профиль vs `/GTS_PDFA` семейство) — согласовать с фактическим выводом LO 7.4 вживую.
- Формулировки 422-сообщений (стиль существующих: короткие, английские, с подсказкой лекарства).
- Форма injection-теста (unit против builder'а filter-JSON + live-попытка через API — планировщик решает состав).
- Лимит размера form-поля `opts` и семантика пустого `{}` vs отсутствующего поля.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Milestone research (v1.3)
- `.planning/research/SUMMARY.md` — синтез: opts-плюмбинг = 5 механических изменений (jobs.go/repo.go/handlers.go/worker.go/libreoffice.go), колонка `jobs.options jsonb` уже существует и инертна; security-review gate для opts-дизайна
- `.planning/research/PITFALLS.md` — Pitfall 7 (`EmbedStandardFonts` не подразумевается `SelectPdfVersion`), Pitfall 8 (OutputIntent-греп как явно неавторитетный sanity-чек; veraPDF — принятый residual risk), Pitfall 9 (UNO filter-property injection — никогда не marshal'ить клиентские opts в filter-строку)
- `.planning/research/ARCHITECTURE.md` — точки интеграции opts-цепочки с file:line

### Prior phase decisions
- `.planning/phases/13-cross-format-conversion-input-safety/13-CONTEXT.md` — D-03/D-04: выбор валидатора по целевому формату, координация terminal-сигнатур тем же коммитом (паттерн наследуется этой фазой)

### Existing implementation (source of truth for patterns)
- `internal/convert/libreoffice.go` — `Convert` (сейчас игнорирует opts: `_ map[string]any`), `filterFor`, `validateDocumentOutput` (точка расширения PDF/A-проверки)
- `internal/api/handlers.go` — `handleCreateJob`: место парсинга form-поля `opts` и двухшаговой валидации; стиль 422-сообщений
- `internal/worker/worker.go` — `terminalLibreOfficeSignatures` (синхронное обновление, D-06), чтение job из Postgres (место строгого парсинга opts)
- `internal/jobs/jobs.go`, `internal/jobs/repo.go` — доменная модель Job и Create/Get (колонка `options` сейчас не читается/не пишется кодом)
- `internal/db/migrations/0001_init.sql:25` — `options jsonb NOT NULL DEFAULT '{}'::jsonb` (миграция НЕ нужна)
- `internal/e2e/e2e_test.go` — E2E-харнесс (расширяется PDF/A-парой и негативными opts-кейсами)

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- Колонка `jobs.options jsonb` существует с 0001_init и полностью инертна — schema-миграция не требуется, только плюмбинг чтения/записи
- `Converter.Convert` уже принимает `opts map[string]any` в сигнатуре интерфейса — интерфейс не меняется, LibreOfficeConverter перестаёт игнорировать параметр
- `validateDocumentOutput` (Phase 13) уже диспатчит валидатор по целевому формату — PDF/A-проверка встаёт внутрь pdf-ветки
- Фикстуры `internal/e2e/testdata/sample.*` — готовые входы для PDF/A e2e-пары

### Established Patterns
- 422-до-записи-в-S3: все content/validation отказы происходят до `storage.Upload`/`repo.Create` — obе opts-проверки (синтаксис, применимость) следуют тому же порядку
- Terminal-классификация через substring-матчи в `terminalLibreOfficeSignatures` — новая PDF/A-ошибка требует синхронного обновления списка (тот же коммит)
- Payload asynq несёт только `job_id` — opts доедут до worker'а через Postgres, не через очередь

### Integration Points
- `handleCreateJob` — парсинг form-поля `opts` после существующих проверок формата, применимость после format-pair валидации
- `LibreOfficeConverter.Convert` — построение `--convert-to pdf:writer_pdf_Export:{filter-JSON}` из серверных констант по валидированной структуре
- `worker.process()` — чтение `job.Options` из Postgres, передача структуры в `Convert`

</code_context>

<specifics>
## Specific Ideas

- Планка приёмки — «live e2e verified»: свежесобранный стек, реальный PDF/A-2b экспорт с проверкой OutputIntent, реальный 422 на невалидные/неприменимые opts, регрессия обычного document→pdf без opts.
- Injection-тест из success criterion 1 — обязательный артефакт фазы, не опция: доказывает, что клиентские байты не достигают argv/filter-JSON.
- Research флагует opts-дизайн как «единственная highest-severity net-new attack surface майлстоуна» — фаза заслуживает явного security-взгляда при ревью, не только стандартного code review.

</specifics>

<deferred>
## Deferred Ideas

- PDF/A-1b и PDF/A-3b как дополнительные значения `pdf_profile` — добавить, когда появится реальный клиент (trivial extension, тот же `SelectPdfVersion` enum)
- Полная ISO 19005 (veraPDF) валидация — v2 (DOCV3-01, зафиксировано в REQUIREMENTS.md)
- Print-опции HTML-движка (размер страницы, поля, printBackground) — Phase 15 (HTML-03), переиспользует механизм этой фазы

</deferred>

---

*Phase: 14-validated-conversion-options-pdf-a-export*
*Context gathered: 2026-07-10*
