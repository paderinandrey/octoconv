# Phase 13: Cross-Format Conversion & Input Safety - Context

**Gathered:** 2026-07-10
**Status:** Ready for planning

<domain>
## Phase Boundary

Клиенты могут конвертировать между офисными форматами внутри документного класса (не только → PDF) через существующий `POST /v1/jobs` поток; произведённый не-PDF выход структурно валидируется до пометки `done`; legacy/запароленные OLE-CFB документы отклоняются с чётким 422 на входе. Требования: CONV-01, CONV-02, SAFE-01.

</domain>

<decisions>
## Implementation Decisions

### Conversion pair matrix
- **D-01:** Ровно 6 симметричных внутрисемейных пар регистрируются в `LibreOfficeConverter.Pairs()`: docx→odt, odt→docx, xlsx→ods, ods→xlsx, pptx→odp, odp→pptx. Никаких кросс-семейных пар (docx→ods и т.п.). Существующие 6 пар →pdf остаются без изменений.
- **D-02:** Экспорт-фильтр задаётся явно для каждой пары: `filterFor` расширяется до таблицы (source, target)→имя фильтра (например docx→odt = `writer8`, odt→docx = `MS Word 2007 XML`). Никакого автовыбора по расширению — поведение должно быть детерминированным. Точные имена фильтров верифицируются против LibreOffice 7.4 (bookworm) в ходе фазы — research предупреждал о дрейфе имён между мажорными версиями LO.

### Output validation (non-PDF targets)
- **D-03:** Произведённый не-PDF выход прогоняется через полный `convert.SniffContainer` (та же функция, что валидирует вход): детектированный формат обязан совпасть с целевым форматом задачи. Симметрично входной гарантии, ноль нового кода валидации. `validatePDF` остаётся для →pdf целей; выбор валидатора — по целевому формату.
- **D-04:** Невалидный/несоответствующий выход — terminal-ошибка сразу (без ретраев, детерминированный исход — как validatePDF сегодня). КРИТИЧНО: текст новой ошибки валидации добавляется в `terminalLibreOfficeSignatures` (internal/worker/worker.go) тем же коммитом, что и валидатор — иначе битый выход будет бессмысленно ретраиться (питфолл из milestone-research).

### OLE-CFB rejection (SAFE-01)
- **D-05:** Единый 8-байтовый magic-check `D0 CF 11 E0 A1 B1 1A E1` в `handleCreateJob` как отдельная fail-closed ветка sniff-цепочки (НЕ в registry-таблицу sniff.go — её контракт «поддерживаемый формат», а не «детектируем и отклоняем»; решение из milestone-research). Различение legacy vs encrypted — вне скоупа (решено при определении требований v1.3).
- **D-06:** Текст 422 называет оба случая и подсказывает лекарство, в стиле существующих сообщений (английский): например «legacy binary or password-protected Office format is not supported; convert to docx/xlsx/pptx or remove the password». Проверка — до записи в S3, как все существующие 422-ветки.

### Live E2E coverage
- **D-07:** internal/e2e расширяется всеми 6 кросс-парами живьём (существующие фикстуры sample.* переиспользуются как входы) + один live-тест, что CFB-файл (реальная фикстура legacy .doc или запароленный .docx) получает 422. Соответствует планке «live e2e verified» из success criteria фазы.

### Claude's Discretion
- Точные имена LibreOffice-фильтров для каждой пары (верифицировать на живом LO 7.4 в ходе выполнения).
- Имя/формат CFB-фикстуры для E2E и способ её создания.
- Точная формулировка 422 (D-06 задаёт смысл и стиль, слова — на усмотрение исполнителя).
- Расположение выходного файла/расширения в конвертере при не-PDF целях (уже частично generic в worker.process()).

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Milestone research (v1.3)
- `.planning/research/SUMMARY.md` — синтез: build order, риски, зависимости фазы
- `.planning/research/PITFALLS.md` — питфоллы: hardcoded `.pdf` в Convert, координация terminalLibreOfficeSignatures, CFB-неоднозначность (legacy vs encrypted — одна сигнатура), fidelity-ловушки кросс-конвертации
- `.planning/research/ARCHITECTURE.md` — точки интеграции с file:line: `libreoffice.go` (Pairs/filterFor/validatePDF), sniff-цепочка `handleCreateJob`, `worker.process()` уже generic по output filename/Content-Type

### Existing implementation (source of truth for patterns)
- `internal/convert/libreoffice.go` — конвертер: Pairs(), filterFor(), validatePDF() — всё расширяется этой фазой
- `internal/convert/sniff.go` — SniffContainer (переиспользуется для валидации выхода), MIMEType (уже знает все 6 форматов после 11-04)
- `internal/api/handlers.go` — sniff-цепочка в handleCreateJob (место CFB-ветки, стиль 422-сообщений)
- `internal/worker/worker.go` — terminalLibreOfficeSignatures (строки 38-53), isDocumentTerminal
- `internal/e2e/e2e_test.go` — E2E-харнесс и паттерн пар (расширяется)

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `convert.SniffContainer`: готовый структурный валидатор ZIP/OOXML/ODF — используется для валидации выхода без нового кода
- Фикстуры `internal/e2e/testdata/sample.{docx,xlsx,pptx,odt,ods,odp}`: готовые входы для всех 6 кросс-пар
- `convert.MIMEType`: уже возвращает канонические MIME для всех 6 форматов (закрыто в 11-04) — Content-Type выхода корректен автоматически

### Established Patterns
- `worker.process()` уже derive'ит имя/Content-Type выхода из `job.TargetFormat` generic-образом — worker-сторона почти не меняется
- 422-до-записи-в-S3: все content-отказы происходят до `storage.Upload`/`repo.Create` — CFB-ветка следует тому же порядку
- Terminal-классификация через substring-匹配 в `terminalLibreOfficeSignatures` — новые terminal-ошибки требуют синхронного обновления списка

### Integration Points
- `LibreOfficeConverter.Convert` — жёстко зашитые `.pdf`-выход и `validatePDF` — центральное место генерализации
- `handleCreateJob` sniff-цепочка — CFB-ветка встаёт после ZIP-ветки, до generic-422
- `Registry`/`EngineFor` — новые пары автоматически получают engine=document routing (ничего менять не надо)

</code_context>

<specifics>
## Specific Ideas

- Планка приёмки — «live e2e verified» как во всех движковых фазах: свежесобранный стек, реальные конвертации, реальный 422.
- Симметрия вход/выход: «выход валидируется тем же сниффом, что и вход» — формулировка, которую стоит сохранить в doc-комментариях.

</specifics>

<deferred>
## Deferred Ideas

- Различение «файл запаролен» vs «формат устарел» (CFB-парсинг) — v2 (DOCV3-02, зафиксировано в REQUIREMENTS.md)
- Fidelity-метрики кросс-конвертации (сравнение содержимого до/после) — не запрошено, вне планки v1.3

</deferred>

---

*Phase: 13-cross-format-conversion-input-safety*
*Context gathered: 2026-07-10*
