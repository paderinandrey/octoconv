# Phase 15: HTML→PDF Chromium Engine - Context

**Gathered:** 2026-07-11
**Status:** Ready for planning

<domain>
## Phase Boundary

HTML-файлы конвертируются в PDF через новый, полностью изолированный от сети (офлайн-рендеринг) третий engine-class — `html`, следующий engine-class-паттерну из v1.2 (собственный бинарник/контейнер/очередь, `Engine()`/`EngineFor`-роутинг, terminal-классификация таймаута). Движок — `chromium-headless-shell`, запускаемый one-shot через существующий `runCommand`. Print-опции (размер страницы, поля, landscape, printBackground) проходят через тот же validated-opts механизм, что и Phase 14. Требования: HTML-01, HTML-02, HTML-03. Зависит от Phase 14 (переиспользует validated-opts) и требует миграции CHECK-констрейнта `jobs.engine` как жёсткого пререквизита.

</domain>

<decisions>
## Implementation Decisions

### Engine choice & invocation
- **D-01:** Движок — `chromium-headless-shell` (Debian bookworm пакет, ~150.x), НЕ полный `chromium` (5x больше, тянет GUI-стек) и НЕ LibreOffice (слабый современный CSS — причина отдельного движка) и НЕ wkhtmltopdf (заброшен). Запуск — one-shot CLI `--headless --print-to-pdf` через существующий `runCommand` (hardened exec с process-group-kill на таймаут переносится как есть).
- **D-02:** CDP-драйвер (`chromedp`/`rod`) НЕ вводится сейчас — это отход от one-shot runCommand-паттерна и +Go-зависимость. Оставлен ЗАПАСНЫМ путём: эскалация к chromedp-протокольной интерцепции ТОЛЬКО если живой adversarial-тест (D-04) покажет сетевую утечку, которую слоёная CLI-блокировка не закрывает.

### Network isolation (HTML-02 — главная attack surface майлстоуна)
- **D-03:** Сеть блокируется слоями на CLI-уровне (не URL-string-валидацией — она не переносится на chromium, Pitfall 11): `--proxy-server=127.0.0.1:9` (все http(s) → мёртвый порт) + `--host-resolver-rules="MAP * ~NOTFOUND"` (весь DNS → NXDOMAIN). Плюс контейнерный слой (см. D-08). Вход — `file://` на уже скачанный джобом input. Точные флаги верифицируются живьём против установленной версии chromium в ходе фазы.
- **D-04:** Живое доказательство «фетч НЕ произошёл» (success criterion 2) — канареечный HTTP-листенер, поднятый в compose-сети (в принципе достижимый из контейнера воркера); тестовый HTML ссылается на него через `<img src>` И `<script>fetch()`; ассерт — НОЛЬ входящих соединений на листенер за время рендера + PDF успешно создан. Прямое доказательство отсутствия запроса, не косвенное «рендер не упал». Дополнительно фикстура ссылается на 169.254.169.254 и внутренние compose-хосты (redis/postgres).

### JavaScript policy
- **D-05:** JavaScript ВЫКЛЮЧЕН при рендере: `--blink-settings=scriptEnabled=false` (или эквивалентный проверенный флаг). Статический HTML+CSS рендер. Убирает целый класс атак — JS-fetch, DNS-rebinding-TOCTOU, renderer-эксплойты через JS-движок (критично при вынужденном `--no-sandbox`, Pitfall 10) — и делает рендер детерминированным (waitDelay-опция не нужна). Канареечный JS-fetch в тесте D-04 всё равно присутствует — проверяет оба слоя одновременно (JS-off + сетевая блокировка).

### Print options (HTML-03 — через validated-opts Phase 14)
- **D-06:** Закрытый allowlist через тот же паттерн, что `DocOpts` (типизированная Go-структура, `DisallowUnknownFields`, filter/argv строится ТОЛЬКО из серверных констант, клиентские байты не попадают в argv):
  - `page_size`: закрытый enum — `a4` | `letter` | `legal` | `a3` | `a5`
  - `margin_mm`: одно число, границы 0–50, одинаковые поля со всех сторон
  - `landscape`: bool
  - `print_background`: bool
  Без waitDelay (JS выключен) и без произвольных размеров страницы. Значения → chromium-флаги (`--print-to-pdf` + размерные флаги) из серверной таблицы, как PDFAFilterOptions в Phase 14.

### HTML input detection
- **D-07:** HTML — текст без magic bytes, поэтому детект двухчастный: клиент явно указывает `source_format=html` (или расширение `.html`/`.htm`), а fail-closed контент-чек подтверждает: валидный UTF-8 текст без NUL-байтов + HTML-маркер в начале (`<!doctype html` или `<html` после пробелов/BOM, case-insensitive). Бинарный/чужой файл под видом `.html` → 422 ДО записи в S3, симметрично стилю существующей sniff-цепочки. Полный HTML-парсинг (`x/net/html`) отвергнут — +зависимость, а строгость иллюзорна (HTML парсится «как угодно»).

### Container & process model (engine-class паттерн v1.2)
- **D-08:** Отдельный `Dockerfile.chromium-worker` + `cmd/chromium-worker` + `internal/convert/chromium.go`. Контейнерный слой поверх сетевой блокировки: сетевые egress-ограничения на уровне compose (компенсирующий контроль вместо снятого chrome-sandbox, Pitfall 10/11). `--no-sandbox` + `--disable-dev-shm-usage` обязательны под `USER nobody` (chrome-sandbox требует привилегий, недоступных non-root, Pitfall 10).
- **D-09:** `tini`-как-PID-1 из `Dockerfile.document-worker` переносится (chromium форкает zygote/GPU/renderer — тот же reaper-класс проблем, что soffice.bin, Pitfall 12), НО как и для LibreOffice — необходимость `tini` для этой конкретной инвокации подтверждается ЖИВЬЁМ в ходе фазы, не принимается на веру. `--shm-size`/`--disable-dev-shm-usage` задаётся и в compose, и во флагах chromium.

### Claude's Discretion
- Точные chromium-флаги (проверить живьём против установленной версии: имя scriptEnabled-флага, размерные флаги page_size/margins, поведение `--print-to-pdf` в headless-shell).
- Собственный `HTML_ENGINE_TIMEOUT`/`HTML_WORKER_CONCURRENCY` (значения по образцу document-движка) и терминальная классификация таймаута.
- Структура opts-структуры для html и способ диспатча opts по движкам (document vs html) — паттерн Phase 14 наследуется, детали на планировщике.
- Форма миграции `jobs.engine` CHECK (добавить `html` в allow-list) и волновая координация с queue/task-type-скаффолдингом.
- Лимит размера HTML-файла на входе.
- Точный вид canary-листенера в e2e (язык/порт/способ подсчёта соединений).

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Milestone research (v1.3) — Phase 15 требует собственного research/design-паса
- `.planning/research/SUMMARY.md` — chromium как 4-й engine-class; network-blocking — единственный нерешённый архитектурный форк майлстоуна (CDP vs CLI+egress); `tini`-необходимость и точные флаги флагнуты как «verify live»
- `.planning/research/PITFALLS.md` — **Pitfall 10** (chrome-sandbox vs USER nobody → `--no-sandbox` почти обязателен, нужны компенсирующие контроли), **Pitfall 11** (URL-string SSRF-валидация НЕ переносится на chromium — блокировать на протокольном/сетевом уровне), **Pitfall 12** (chromium форкает подпроцессы → нужен init-reaper `tini` + явный `/dev/shm` sizing); валидационные чек-листы (строки 323, 349-351: рендер `<img src="http://169.254.169.254/">` + JS-fetch, оба fail closed)
- `.planning/research/ARCHITECTURE.md` — точки интеграции 4-го engine-class с file:line; `jobs.engine` CHECK как жёсткий пререквизит

### Prior phase decisions (наследуемые паттерны)
- `.planning/phases/14-validated-conversion-options-pdf-a-export/14-CONTEXT.md` — validated-opts паттерн (закрытая структура, DisallowUnknownFields, argv из серверных констант, injection-тест) — HTML-03 переиспользует его целиком
- `.planning/phases/10-*/` и `.planning/phases/11-*/` (SUMMARY) — engine-class-паттерн v1.2: `cmd/document-worker`, `Dockerfile.document-worker` (tini «confirmed live (09-02)»), engine-aware routing `handleCreateJob`, terminal-таймаут-классификация DOC-08

### Existing implementation (source of truth for patterns)
- `internal/convert/exec.go` — `runCommand` (hardened exec, process-group kill; переносится на chromium as-is)
- `internal/convert/libreoffice.go`, `internal/convert/opts.go` — образцы Converter-реализации и validated-opts builder'а из серверных констант
- `internal/api/handlers.go` — sniff-цепочка `handleCreateJob` (место HTML-детект-ветки, стиль 422-сообщений, порядок «валидация до S3»)
- `internal/worker/worker.go` — `HandleDocumentConvert`, `terminalLibreOfficeSignatures`, `isDocumentTerminal` (образец terminal-таймаут-классификации для нового html-хендлера)
- `internal/queue/queue.go` — task-type/queue-константы (место нового `html`-скаффолдинга)
- `internal/db/migrations/` — место миграции `jobs.engine` CHECK
- `Dockerfile.document-worker`, `docker-compose.yml`, `docker-compose.e2e.yml` — образцы контейнер-топологии, tini, engine-worker-сервиса
- `internal/e2e/e2e_test.go` — E2E-харнесс (расширяется html→pdf happy-path + canary-network-block тестом)

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `runCommand` (exec.go) — hardened one-shot exec с process-group-kill на таймаут; переносится на chromium без изменений (D-01)
- validated-opts паттерн Phase 14 (`opts.go`: закрытая структура + DisallowUnknownFields + argv из серверных констант + injection-тест) — HTML print-опции строятся тем же способом (D-06)
- engine-class-паттерн v1.2 (`cmd/document-worker`, `Dockerfile.document-worker`, engine-aware `EngineFor`-роутинг, terminal-таймаут DOC-08) — html-движок его копирует (D-08/D-09)
- worker output-naming/Content-Type уже generic по `job.TargetFormat` — html→pdf выход обрабатывается автоматически

### Established Patterns
- Валидация-до-S3: HTML-детект (D-07) встаёт в sniff-цепочку `handleCreateJob` как новая ветка, до `storage.Upload`, с 422 как все content-отказы
- terminal-таймаут через `isDocumentTerminal` (DOC-08) — html-хендлер повторяет для `HTML_ENGINE_TIMEOUT`
- Converter-интерфейс не меняется — chromium.go реализует `Pairs()`/`Convert()`/`Engine()`, регистрируется в `convert.Default`

### Integration Points
- `jobs.engine` CHECK-констрейнт — жёсткий пререквизит: миграция добавляет `html` ДО любой routing-работы (первая волна вместе с queue/task-type-скаффолдингом)
- `handleCreateJob` — HTML-детект-ветка + engine-роутинг на новую очередь
- `docker-compose.yml`/`.e2e.yml` — новый chromium-worker-сервис + canary-листенер для network-block теста

</code_context>

<specifics>
## Specific Ideas

- Планка приёмки — «live e2e verified» как во всех движковых фазах: свежесобранный стек, реальная html→pdf конвертация, реальный canary-тест сетевой блокировки (ноль соединений на листенер), реальный 422 на не-HTML под видом .html.
- Network-block — не code-review-утверждение, а живой тест с прямым доказательством (success criterion 2 явно требует «proven by a live test, not asserted by code review alone»).
- «Verify live, don't assume» — сквозной принцип фазы: точные chromium-флаги, необходимость tini, поведение scriptEnabled — всё подтверждается на установленной версии в ходе выполнения (research это явно флагует как MEDIUM confidence).

</specifics>

<deferred>
## Deferred Ideas

- JS-рендеринг (charts/JS-генерируемые документы) — включить отдельным решением, когда появится реальный внутренний клиент с такой потребностью (сейчас JS выключен, D-05)
- CDP/chromedp-протокольная интерцепция — запасной путь, вводится ТОЛЬКО если живой тест покажет утечку слоёной CLI-блокировки (D-02)
- Расширенный набор print-опций (раздельные поля по сторонам, произвольный размер страницы, scale) — за пределами HTML-03, добавить при реальном запросе
- Кастомные шрифты / расширенное CJK-RTL покрытие — v2 (DOCV3-03, зафиксировано в REQUIREMENTS.md)
- URL-fetch вход для HTML→PDF — явно Out of Scope (SSRF-поверхность страшнее webhook'овой; file-upload-only)

</deferred>

---

*Phase: 15-html-pdf-chromium-engine*
*Context gathered: 2026-07-11*
