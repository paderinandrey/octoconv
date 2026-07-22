# OctoConv

## What This Is

OctoConv — внутренний асинхронный сервис конвертации файлов на Go для сервисов компании. Клиент отправляет файл через API, сервис кладёт его в S3-совместимое хранилище, ставит задачу в очередь (asynq/Redis), воркер запускает внешний движок конвертации и складывает результат обратно в S3. На `main` — три production-hardened класса движков: изображения через libvips (v1.0/v1.1), офисные документы через LibreOffice headless — включая кросс-конвертацию docx↔odt/xlsx↔ods/pptx↔odp и PDF/A-2b экспорт через validated opts (v1.2/v1.3), и HTML→PDF через chromium-headless с офлайн-рендерингом (v1.3). Вокруг них: обязательная API-key аутентификация с ротацией, rate limiting, HMAC-подписанная webhook-доставка через выделенные избыточные webhook-воркеры (переживает деплой/падение любого engine-воркера; reconciler-sweeper выбирается через Postgres advisory lock), корректный transient/terminal retry per-engine, fail-closed валидация содержимого по magic bytes (включая отказ OLE-CFB legacy/encrypted входов), защита от decompression bomb, TTL-очистка хранилища и полная наблюдаемость (Prometheus-метрики, реальный health-check, asynqmon-дашборд).

## Core Value

Внутренние сервисы компании могут безопасно (через аутентификацию по API-ключу) и надёжно поставить задачу конвертации файла (изображения, офисные документы, HTML) и получить результат — без риска для стабильности или безопасности продакшена.

## Current State

**v1.7 Audio Engine & Hardening — SHIPPED 2026-07-18.** Четвёртый engine-класс (офлайн whisper.cpp-транскрипция) в полном контуре: fail-closed валидация → async-пайплайн со stage-aware retry → контейнер с RTF-измеренным таймаутом → KEDA scale-from-zero (live-proven). Плюс закрыт hardening-хвост v1.6. Аудит 12/12 требований, интеграция 22/22, E2E 2/2.

**v1.8 в работе — Phase 35 (Queue, Worker & Routing Integration) завершена 2026-07-22.** `AVConverter` подключён к async-пайплайну: очередь `av`, `cmd/av-worker`, роутинг в API и реконсилере, stage-aware retry-классификатор (`isAVTerminal`: transcode-таймаут transient, остальное terminal), `AVUniqueTTL`. Видео→транскрипт едет через существующий audio-воркер (пары `AudioConverter` расширены 16→36, `Engine()` остаётся audio). Дифференциал доказан живьём: один mp4, `webm`→очередь av, `srt`→очередь audio. Верификация 4/4, security 31/31, требования AVE-03/AVT-01. `AV_ENGINE_TIMEOUT`=600s пока `[ASSUMED]` — RTF-замер это Phase 36.

<details>
<summary>Phase 34 (AV Engine Foundation) — завершена 2026-07-20</summary>

Standalone `AVConverter` (транскод / извлечение аудио / thumbnail) собран и проверен против живого ffmpeg 8.1.2, плюс магик-байтовые снифферы видео-контейнеров и закрытый `AVOpts` allowlist. Верификация 5/5 критериев, 10/10 требований (AVC-01..05, AVO-01..03, AVE-01/02). Конвертер намеренно **не был зарегистрирован** в `convert.Default` — это сделал Phase 35.

</details>

## Current Milestone: v1.8 AV Engine (video/ffmpeg)

**Goal:** Пятый engine-класс — обработка видео через ffmpeg в отдельном av-воркере по проверенному паттерну (своя очередь, свой контейнер, свои таймауты, KEDA), включая сквозную цепочку видео → транскрипт через существующий whisper-пайплайн.

**Target features:**
- Транскод видео (mov/avi/mkv/webm → mp4 H.264 и т.п.) с собственным RTF-гейтом по образцу аудио — самая дорогая операция класса
- Извлечение аудио: видео → mp3/wav/m4a
- Превью/thumbnail: кадр из видео → jpg/png/webp (таймкод через opts)
- Видео → транскрипт: mp4/mov → txt/srt/vtt/json одним job'ом (ffmpeg-экстракция + whisper)
- Отдельный `cmd/av-worker`: очередь `av`, Dockerfile с ffmpeg, fail-closed magic-bytes валидация видео-контейнеров, transient/terminal retry, compose + chart + KEDA ScaledObject

**Key context:** воркеры остаются офлайн (ffmpeg локальный, без внешних API); паттерн v1.7 переиспользуется целиком (RTF-гейт → measured timeout, stage-aware retry, IN-02 пропагация, scale-from-zero proof); способ реализации видео→транскрипт (whisper внутри av-контейнера vs межочередная цепочка) решается на research/planning. SEED-001 остаётся в банке семян — не выбран в этот milestone.

<details>
<summary>v1.7 Audio Engine & Hardening — SHIPPED 2026-07-18</summary>

**Goal:** Четвёртый engine-класс — офлайн-транскрипция аудио через whisper.cpp по проверенному паттерну (отдельная очередь/воркер/бинарник, hardened exec, KEDA) — плюс закрытие hardening-хвоста v1.6.

**Target features:**
- Hardening-хвост v1.6: WR-01 (семантика пустого PromQL при падении api), live-скрипт OPER-01 + проброс OPERATOR_CLIENT_IDS в compose, гейт-тулинг warnings из 28-REVIEW, K8S-02 direct-dial перепроверка
- Audio engine-класс: audio-форматы → транскрипт через whisper.cpp CLI в отдельном контейнере; magic-bytes валидация, transient/terminal retry, отдельная asynq-очередь + KEDA ScaledObject, chart-интеграция
- SEED-001 фундамент: контракт результата транскрипции (формат, таймстампы) спроектирован с прицелом на будущий разбор урока

**Key context:** движок локальный (без внешних API — воркеры остаются офлайн); анализ ошибок отложен; k8s-в-CI отложен (K8SV2-01). Прошлое состояние: v1.6 shipped 2026-07-17 (см. Context).

</details>

<details>
<summary>v1.6 Kubernetes & KEDA — SHIPPED 2026-07-17</summary>

**Goal:** Весь стек OctoConv поднимается в Kubernetes из Helm-чарта, воркеры автоскейлятся KEDA по глубине очередей (0→N→0 под нагрузкой — доказано), MCP получает in-cluster HTTP-эндпоинт, а пресеты — system-scope REST.

**Target features:**
- Helm-чарт + полный стек на OrbStack k8s (SEED-004): Deployments/probes, Secrets/ConfigMaps, migrate/createbucket как Jobs; четыре известные мины переноса закрыты (METRICS_ADDR localhost-bind, host-gateway E2E-трюк, one-shot ordering, compose-DNS в presigned URL)
- KEDA ScaledObjects per engine-class по существующей Prometheus queue_depth метрике
- Нагрузочная проверка автоскейла: скейл 0→N→0 с наблюдаемыми критериями
- MCP streamable HTTP (MCPV2-01): контейнер в кластере, внутренний эндпоинт
- system-пресеты REST (PRAPIV2-01)

**Key context:** первый инфраструктурный милстоун; research first. Прошлое состояние: v1.5 shipped 2026-07-13 (см. Context).

</details>

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

- ✓ 4-уровневый CI (GitHub Actions): gate → -race → bake 5 образов (per-target gha cache, free-disk) → live compose-E2E (advisory на PR / required на main, teardown if:always, логи артефактом, concurrency-отмена) — Phase 19 (CI-01..04, live-proven run 29207810893; первый ран сам вскрыл 429-конфиг-баг E2E-окружения и доказал failure-path)
- ✓ Именованные пресеты: internal/presets (shadowing client>system в SQL, bump-on-update, no-leak 422), cmd/manage-presets (5 глаголов), preset=<name> в POST /v1/jobs c XOR, re-валидацией хранимых opts и TOCTOU-речеком; provenance в jobs.preset_name/version — Phase 18 (PRST-01..04, live 33/33 ×3)
- ✓ Хвост v1.3-долга закрыт: мёртвая webhook-обвязка удалена, fakeEnqueuer race-safe, image E2E-тест; бонус — терминальный PGAdvisoryLock.Close (DEFER-17-01) — Phase 17 (DEBT-06..08)

- ✓ REST self-service пресетов (/v1/presets, узкий DTO, no-leak, 409) + /v1/formats (registry-derived) — Phase 20 (PRAPI-01..03, live 42/42 ×3)
- ✓ MCP-сервер: cmd/mcp-server (stdio, go-sdk v1.6.1 — первая новая зависимость с v1.0), 5 инструментов, блокирующий convert_file с progress, zero-privilege HTTP-клиент с редакцией ключа — Phase 21 (MCP-01..05, live stdio-гейт ×2; SEED-003 implemented)
- ✓ CFB-различение: собственный bounded-парсер директории (cycle-guard, fuzz 3.5M/0), три различённых 422 — Phase 22 (CFB-01..02)
- ✓ Настоящая ISO 19005-2b валидация PDF/A: veraPDF в document-worker (Debian-JRE, amd64 pin), terminal fail-closed, замеренный go/no-go (p95 4.65s/10s) — Phase 23 (PDFA-01..02)
- ✓ Полный стек разворачивается в k8s одной командой (`helm install deploy/chart/octoconv` + values-local) на OrbStack и проходит E2E внутри кластера как Job (9/9, presigned FQDN, NetworkPolicy-scoped /metrics, migrate/createbucket ordering) — Phase 24 (K8S-01..03)
- ✓ MCP доступен как in-cluster streamable-HTTP-эндпоинт: cmd/mcp-http, per-request caller-key pass-through (под без ключей), presigned-only результаты, live-гейт с реальной конверсией и 401/403-кейсами — Phase 25 (MCPH-01/02)
- ✓ Operator-only REST для system-пресетов: /v1/system/presets за OPERATOR_CLIENT_IDS env-allowlist (fail-closed/fail-loud), byte-identical no-leak 404, ноль миграций; попутно закрыт version-collision в repo.Create (deactivate→recreate) — Phase 26 (OPER-01, verification 5/5)
- ✓ KEDA-автоскейл per engine-class: queue-depth экспозиция перенесена на always-on api (все 4 очереди; воркеры больше не регистрируют коллектор), per-class asynq ShutdownTimeout (8s-дефолт делал grace-периоды мёртвыми), in-chart Prometheus + 3 ScaledObject (minReplicaCount 0, pending+active PromQL, двойной флаг keda.enabled&&prometheus.enabled), webhook-worker жёстко 2 реплики без ScaledObject; live-гейт scripts/keda-gate.sh 18/18 на OrbStack (SC1 через external metrics API при 0 реплик, все 3 класса 0→1, image полный цикл →0) — Phase 27 (KEDA-01/02, verification 8/8; залповый 0→N→0 под нагрузкой — Phase 28)
- ✓ Load-proof автоскейла с таймстамп-доказательством: залп 20 image-джобов при истинном нуле → 4 реплики за 11s (SC1 ≥2/60s), drain +76s, scale-to-zero +136s; 178s document-конверсия пережила настоящий KEDA/HPA-даунскейл 2→1 (SIGTERM за 142.8s до завершения, exit 0, ровно один queued→active, 188s запаса grace); evidence (CSV+PNG+транскрипт 27/27+таймстампы) закоммичен в phases/28/evidence/; попутно WR-02 закрыт (условный spec.replicas на scaled-классах) + values-gated HPA scaleDown stabilization override — Phase 28 (KEDA-03, verification 10/10; четвёртый OrbStack-клин задокументирован и восстановлен k8s hard-cycle) — milestone v1.6 phases complete
- ✓ Hardening-хвост v1.6 закрыт: WR-01 (ignoreNullValues:false на всех 3 ScaledObject — падение api больше не читается как пустая очередь, fallback держит 1 реплику) + WR-05 checksum (prometheus config-hash через named-template, без рекурсии) + WR-06 (retry-в-PromQL-триггере); OPER-01 live-гейт `/v1/system/presets` против compose (61/61, OPERATOR_CLIENT_IDS проброшен); шесть гейт-тулинг warning'ов; K8S-02 presigned direct-dial с хоста без обхода (keda-gate.sh 21/21) — Phase 29 (HARD-01..04, verification 4/4; live-re-run keda-load-proof.sh отложен на Phase 33, watcher-kill упрочён pkill-fallback)
- ✓ Standalone AudioConverter (четвёртый engine-класс, standalone — регистрация в Registry отложена на Phase 31 по scope fence): двухстадийный ffmpeg→whisper-cli v1.9.1 пайплайн (пиненный тег, -DGGML_NATIVE=OFF, SHA-256-пин модели ggml-base), txt/srt/vtt/json через Pair (16 пар), target=json с сегментными+пословными таймстампами (схема live-верифицирована против реального бинаря); ID3v2-aware fail-closed SniffAudio (synchsafe-скип, footer-flag, m4aBrands только M4A/M4B); ffprobe duration-гард с float-space валидацией (NaN/Inf/negative/overflow — CR-01 amd64-обход закрыт); AudioOpts через checkStrictObject c closed allowlist (auto/en/ru/es/fr/de) + injection-тест; hallucination-on-silence — accepted residual risk — Phase 30 (AUD-01..04, verification 5/5; code review 1 Critical + 4 Warning найдены и исправлены)
- ✓ Audio-класс встроен в async-контур end-to-end: миграция 0006 (jobs.engine += 'audio'), регистрация AudioConverter, выделенная audio-очередь (TypeAudioConvert/QueueAudio, свой retry schedule, AudioUniqueTTL 2570s > worst-case 2450s — T-03-10), stage-aware isAudioTerminal (Key Decision 1: ffmpeg-стадия/ffprobe-детерминированные — terminal, whisper-стадия timeout — transient, duration_exceeded — отдельный error_code), duration-гард реально вызывается в process() до Convert (T-30-08 закрыт, IN-02 pinning-тест), SniffAudio врезан в API с байт-точным тестом (rest-reader, не file.ReadAt), reconciler-роутинг + SC4-тест нулевых ложных recovery, cmd/audio-worker; live E2E: jfk.wav → 202 → queued→active→done за ~2.8s, транскрипт совпал — Phase 31 (AUD-05, verification 4/4; code review 6 Warning найдены и исправлены, вкл. ffprobe-ветку классификатора и budget-floor против S3-столов)
- ✓ Audio-worker контейнеризован с measured RTF-гейтом: Dockerfile.audio-worker (3-stage, whisper.cpp v1.9.1 по commit-hash пину с rev-parse guard, -DGGML_NATIVE=OFF, модель запечена с SHA-256 fail-closed, образ 682MB arm64); cgroup v2 детекция CPU-лимита → whisper -t (AUDIO_THREADS → cpu.max → NumCPU, ParseInt+positivity против Inf/NaN); scripts/audio-rtf-measure.sh: p95 RTF=0.206 (N=10, --cpus=2/-t 2/1g, arm64-каверза записана) → AUDIO_ENGINE_TIMEOUT=742s GO (17.6% под 900s CAP), NO-GO-рычаг применён: AUDIO_MAX_DURATION_SECONDS 14400→1800, AUDIO_WORKER_CONCURRENCY=1 (RSS ~728MiB); compose-сервис + IN-02 7-way пропагация (742s ×7) + stale 5m CAP-оверрайды webhook-worker'ов исправлены на 15m + stop_grace_period 762s; CI bake matrix; повторяемый TestAudioConversionE2E в internal/e2e (PASSED live 8.10s через контейнер, signed webhook, DB-proof) — Phase 32 (AUD-06/07, verification 6/6; plan-checker поймал 2 блокера до исполнения — stale CAP-дрейф и echo-верify; code review 5 Warning исправлены)
- ✓ Audio-класс автоскейлится в k8s с production-паритетом: audio-worker Deployment (terminationGracePeriodSeconds 772 > 752 ShutdownTimeout > 742 ENGINE_TIMEOUT) + KEDA ScaledObject с WR-01 триадой verbatim (ignoreNullValues:"false", fallback.replicas:1, retry-inclusive PromQL) и первым non-null production scaleDownStabilizationSeconds 900 (742s-задачи не влезают в 300s HPA-дефолт); configmap: 5 AUDIO_* ключей + chart-сторона stale 5m→15m фикса; QueueAudio в api-коллекторе (5 очередей); scripts/keda-audio-loadproof.sh (клон, frozen-скрипты byte-unchanged); live proof: 0→1 scale-from-zero 10/10 с запечённой моделью, Pulling→Pulled ≈0 на OrbStack shared store (bake-in подтверждён, реверсивен, registry cold-pull отложен до реального registry); отложенный Phase-29 re-run keda-load-proof.sh: SC1/SC2 live PASS, его SC3 BUSY_POD упал громко на уже принятом WR-05 jsonpath-резидуале (эмпирически подтверждён, скрипт не тронут) — Phase 33 (AUD-08, verification 4/4; code review 2 Warning исправлены) — **milestone v1.7 phases complete (29-33)**

- ✓ Клиент может отправить аудиофайл (mp3/wav/m4a/ogg) и получить транскрипт (whisper.cpp v1.9.1, офлайн) через тот же async-пайплайн, что и остальные классы — v1.7 (live E2E compose 8.10s + k8s scale-from-zero 10/10)
- ✓ Audio-класс встроен в полный контур: fail-closed валидация содержимого, stage-aware retry-семантика, KEDA-скейлинг, chart с production-паритетом — v1.7 (audit 12/12, integration 22/22)

### Active

<!-- Milestone v1.8 AV Engine (video/ffmpeg) — defined 2026-07-19. -->

- [ ] Клиент может транскодировать видео (mov/avi/mkv/webm → mp4 и др.) через тот же async-пайплайн, с RTF-измеренным таймаутом
- [ ] Клиент может извлечь аудиодорожку из видео (mp3/wav/m4a)
- [ ] Клиент может получить превью-кадр из видео (jpg/png/webp, таймкод через opts)
- [ ] Клиент может получить транскрипт видео (txt/srt/vtt/json) одним job'ом
- [ ] av-класс встроен в полный контур: fail-closed валидация, stage-aware retry, отдельный воркер/контейнер, compose + chart + KEDA

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
- **Milestone v1.7 (Audio Engine & Hardening) shipped 2026-07-18.** 5 фаз (29-33), 18 планов, 44 задачи, ~170 коммитов, +4495/−38 строк (без .planning, фазы 30-33), ~2 дня. Четвёртый engine-класс: аудио-транскрипция whisper.cpp v1.9.1 офлайн (mp3/wav/m4a/ogg → txt/srt/vtt/json с пословными таймстампами), RTF-измеренный AUDIO_ENGINE_TIMEOUT=742s, KEDA scale-from-zero live-proven 10/10. Аудит: 12/12 требований, 22/22 интеграции, 2/2 E2E. Полный отчёт: `.planning/milestones/v1.7-*`.
- **Milestone v1.6 (Kubernetes & KEDA) shipped 2026-07-17.** 5 фаз (24-28), 14 планов, 33 задачи, 129 коммитов, +5978/−56 строк (без .planning), ~3.5 дня. Первый инфраструктурный милстоун: Helm-чарт на OrbStack k8s, KEDA scale-from-zero per engine-class, таймстампированный load-proof 0→4→0 (evidence в phases/28/evidence/), MCP streamable-HTTP, operator system-presets REST. Аудит: passed 9/9, advisory tech debt (OPER-01 live-script gap, WR-01 trigger semantics, gate-tooling warnings). Четвёртый задокументированный OrbStack-клин случился и восстановлен в ходе load-proof. Полный отчёт: `.planning/milestones/v1.6-*`.
- **Milestone v1.5 (MCP Access & Document Fidelity) shipped 2026-07-13.** 4 фазы (20–23), 10 планов, 71 коммит, +5537/−84 строк (без .planning). Аудит: 12/12, 6/6 интеграции. Первый measured go/no-go гейт (veraPDF JVM). Полный отчёт: `.planning/milestones/v1.5-*`.
- **Milestone v1.4 (CI, Presets & Debt Cleanup) shipped 2026-07-13.** 3 фазы (17–19), 8 планов, 54 коммита, +2261/−60 строк (без .planning), ~2 дня. Аудит: 11/11 требований, 6/6 интеграции. Репозиторий публичный; CI живой (badge passing). Полный отчёт: `.planning/milestones/v1.4-*`.
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
| Webhook-доставка: выделенный webhook-worker ×2 + Postgres advisory lock для singleton-sweeper (вместо leader election или фиксированного «главного» воркера) | Простейший примитив, дающий exactly-one-sweeper на флот без новых зависимостей; консьюмеры симметричны | ✓ Good — v1.3 Phase 16: SC1-3 live-verified, ~11s failover; conn-lifecycle гэпы (CR-01/WR-01) закрыты в 16-05 с mutex + -race тестом; v1.4 Phase 17 добавила терминальный Close (DEFER-17-01) |
| Пресеты: preset XOR явные opts, client затеняет system, bump-on-update, re-валидация хранимых opts при каждом использовании | Сохранённым конфигам нельзя доверять после смены allowlist; XOR исключает неоднозначный merge | ✓ Good — v1.4 Phase 18: live 33/33; TOCTOU-речек перед INSERT |
| CI: e2e-уровень advisory на PR / required на main; bake по compose-файлу; тест-лимиты только в e2e-оверрайде | PR не блокируется флаки-инфраструктурой, main защищён; одна декларация build-таргетов; продакшен-лимиты неприкосновенны | ✓ Good — v1.4 Phase 19: run 2 полностью зелёный; 429-урок первого рана зафиксирован |
| Отдельный `cmd/document-worker` бинарник/контейнер вместо второго `asynq.Server` внутри image-воркера | Тяжёлый footprint LibreOffice не должен попадать в контейнер image-воркера; ресурсная изоляция по классам движков | ✓ Good — v1.2 Phase 10: Dockerfile.worker снова libvips-only, LibreOffice изолирован с tini как PID 1 |
| Engine-класс определяется по контент-детектированному формату (`EngineFor(detected, target)`), не по расширению файла | Расширение подконтрольно атакующему; magic-bytes/структурный sniff — единственный проверяемый факт | ✓ Good — v1.2 Phase 11: fail-closed default на нераспознанный engine, live-verified |
| Resource-exhaustion через сложный документ (DOC-V2-05) — accepted residual risk v1.2 | Митигируется только `DOCUMENT_ENGINE_TIMEOUT` + потолком конкуренции document-воркера; активный анализ сложности отложен | — Pending (принятый риск, пересмотреть при росте нагрузки) |
| `file://` residual read внутри chromium-worker — accepted residual risk v1.3 (Phase 15) | Live-tested (Plan 04, item 6): `<img src="file:///usr/share/pixmaps/debian-logo.png">` (world-readable, non-input file) успешно загрузился внутри рендера под USER nobody — passive subresource loads (img/link/script src) читают ЛЮБОЙ файл, доступный uid nobody, включая потенциально workDir других одновременно выполняющихся job'ов (0700 не изолирует общий UID). Активный `fetch()`/XHR к `file://` при этом блокируется самим Chromium — подтверждено отдельно. Матчит DOC-V2-05 precedent (internal-only clients trust model) | — Pending (принятый риск; митигация — bind-mount только собственного workDir job'а — отложена как будущая опция, не блокирует Phase 15) |
| tini как PID 1 в `Dockerfile.chromium-worker` — сохранён, несмотря на неподтверждённую живым тестом необходимость именно для этой invocation-формы | Live-tested (Plan 04, item 7): `runCommand`-точное поведение (SIGKILL всей process-группы через `-PGID`) НЕ оставило зомби-процессов ни с tini, ни без него (3 повтора без tini, 1 с tini) — вероятно потому что весь одномоментный SIGKILL убивает parent+children синхронно, а не оставляет осиротевших детей. Tini оставлен как defence-in-depth (совпадает с собственным биасом D-09 "keep it" + сигнал-форвардинг для graceful shutdown), изменений в Dockerfile не внесено | ✓ Good — поведение задокументировано честно, а не предположено; изменений нет |
| Stage-aware классификация таймаутов аудио (Key Decision 1, v1.7): ffmpeg-стадия/детерминированный ffprobe — terminal, whisper-timeout на валидированном аудио — transient | Малформленный вход не ретраится впустую; честный timeout дорогой транскрипции получает свежий CPU; blanket-terminal отвергнут как строго менее корректный | ✓ Good — Phase 31: TestIsAudioTerminal pinning; code review добавил недостающую ffprobe-ветку и budget-floor против S3-столов до продакшена |
| Модель `base` по умолчанию, реверсивно через build-arg (Key Decision 2, v1.7) | `small` утяжеляет образ к ~1GB и бьёт по scale-from-zero; выбор не запечатан навсегда | ✓ Good — Phase 32: образ 682MB, RTF p95=0.206 на base достаточен (742s timeout с запасом) |
| Model bake-in (не volume) с обязательным measured load-proof (Key Decision 3, v1.7) | Простейший вариант при офлайн-ограничении, но обязан быть доказан измерением, не предположением | ✓ Good — Phase 33: 0→1 scale-from-zero 10/10 live; Pulling→Pulled ≈0 на OrbStack shared store; registry cold-pull задокументирован как неизмеримый локально — решение реверсивно |
| AUDIO_ENGINE_TIMEOUT измеряется RTF-гейтом, не копируется от соседних классов | Копированная константа для самого дорогого класса — прямой путь к гонке T-03-10 или мёртвым таймаутам | ✓ Good — Phase 32: 742s из p95×2.0 формулы; NO-GO-рычаг сработал по назначению (max duration 4h→30min вместо раздувания таймаута) |
| Замороженные gate-скрипты (keda-load-proof.sh, keda-gate.sh) byte-unchanged; новая функциональность — только новыми скриптами | Стабильность доказательной базы: прошедшие live-гейты не мутируют задним числом | ✓ Good — Phase 33: WR-05 дефект замороженного скрипта эмпирически подтверждён и честно задокументирован вместо тихого фикса; forward-fix отложен осознанно |

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
*Last updated: 2026-07-22 after Phase 35 (Queue, Worker & Routing Integration) completion*
