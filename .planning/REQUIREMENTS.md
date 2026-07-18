# Requirements: OctoConv

**Defined:** 2026-07-17
**Core Value:** Внутренние сервисы компании могут безопасно и надёжно поставить задачу конвертации файла (изображения, офисные документы, HTML, аудио) и получить результат — без риска для стабильности или безопасности продакшена.

## v1 Requirements

Requirements for milestone v1.7 (Audio Engine & Hardening). Each maps to roadmap phases.

### Hardening (v1.6 tail)

- [x] **HARD-01**: KEDA-триггер корректно ведёт себя при недоступности api — пустой PromQL-результат не читается как «очередь пуста» (WR-01: даунскейл занятого класса с живым бэклогом невозможен, либо задокументированный компенсирующий механизм в триггере)
- [x] **HARD-02**: Оператор может прогнать живой REST-сценарий `/v1/system/presets` (CRUD оператором + no-leak 404 для не-оператора) против compose-стека — `OPERATOR_CLIENT_IDS` проброшен в compose api-сервис, скрипт-приёмка существует и проходит
- [x] **HARD-03**: Шесть warning'ов гейт-тулинга из 28-REVIEW закрыты (falsy-`0` в ScaledObject-шаблоне stabilization, SC3 stale-pod гонка, false-PASS download-чек без `-f`, orphaned watcher-процесс, pin интерпретатора в render_evidence.py, CWD-зависимая SAMPLE_IMAGE в gen_heavy_docx.py)
- [x] **HARD-04**: Presigned result URL резолвится с OrbStack-хоста прямым дайлом (K8S-02 перепроверка без `kubectl port-forward` / `curl --connect-to` обхода)

### Audio Engine — Input & Validation

- [x] **AUD-01**: Клиент может отправить аудиофайл (mp3/wav/m4a/ogg) через `POST /v1/jobs` и получить транскрипт через тот же async-пайплайн; magic-bytes валидация fail-closed до записи в S3, включая ID3v2-aware MP3-детектор (переменный офсет, не fixed-window)
- [x] **AUD-04**: Гард длительности аудио (`AUDIO_MAX_DURATION_SECONDS` через ffprobe до/на входе конверсии) — превышение отклоняется предсказуемым terminal/422; аудио-аналог decompression-bomb защиты

### Audio Engine — Transcription & Output

- [x] **AUD-02**: Выходные форматы txt/srt/vtt/json через существующий Pair-механизм; `target=json` содержит сегментные и пословные таймстампы, схема верифицирована против пиненного `whisper-cli` v1.9.1 (forward-совместимость с SEED-001 mistake-analysis)
- [x] **AUD-03**: `AudioOpts{language (closed allowlist), translate}` через validated-opts паттерн (OPTS-01 прецедент) — клиентские байты никогда не попадают в argv движка

### Audio Engine — Pipeline & Reliability

- [x] **AUD-05**: Стадийная классификация таймаутов (ffmpeg-стадия = terminal для битого входа, whisper-стадия на валидном аудио = transient), собственный `AudioUniqueTTL` (не переиспользовать image/document TTL — гонка T-03-10), `RECONCILER_ACTIVE_STALE_AFTER` для audio выше `AUDIO_ENGINE_TIMEOUT`
- [ ] **AUD-07**: RTF (realtime factor) измерен на реальном resource-limited контейнере (measured go/no-go по прецеденту veraPDF Phase 23) до финализации `AUDIO_ENGINE_TIMEOUT` и KEDA cooldown/stabilization

### Audio Engine — Packaging & Deployment

- [ ] **AUD-06**: Отдельный `cmd/audio-worker` + `Dockerfile.audio-worker` (whisper.cpp v1.9.1 из исходников multi-stage, `-DGGML_NATIVE=OFF`, модель `base` запечена с пиненным SHA-256, ffmpeg из apt) + `AUDIO_ENGINE_TIMEOUT`/`AUDIO_WORKER_CONCURRENCY`/ShutdownTimeout env + compose-сервис + CI bake matrix
- [ ] **AUD-08**: Chart: audio-worker Deployment (class-appropriate grace period) + KEDA ScaledObject (со scaleDownStabilizationSeconds-уроком v1.6), QueueAudio зарегистрирована в api queue-depth коллекторе; scale-from-zero живо доказан с моделью, запечённой в образ (image-pull vs scale-from-zero измерен)

## v2 Requirements

Deferred to future milestones. Tracked but not in current roadmap.

### Lesson Analysis (SEED-001 continuation)

- **LESN-01**: Разбор транскрипта → топ ошибок ученика (LLM-интеграция — первый non-offline runtime-класс зависимостей)
- **LESN-02**: Генерация spaced-repetition колоды из выявленных ошибок

### Audio v2

- **AUDV2-01**: Апгрейд модели whisper на `small`/`medium` (values-переключатель)
- **AUDV2-02**: Speaker diarization (когда whisper.cpp tinydiarize/`-di` созреет или появится channel-separated вход)
- **AUDV2-03**: Progress-нотификации для длинных транскрипций через MCP convert_file

### K8s v2

- **K8SV2-01**: k8s-стек (kind/k3d) в CI helm install + smoke
- **K8SV2-03**: `is_operator` колонка вместо env-allowlist

## Out of Scope

Explicitly excluded. Documented to prevent scope creep.

| Feature | Reason |
|---------|--------|
| Облачный STT API | Воркеры остаются офлайн (без внешних runtime-зависимостей/секретов/эгресса) — сохраняет модель безопасности продакшена |
| Разбор ошибок ученика + колода в v1.7 | Требует LLM-интеграции (новый класс зависимостей); транскрипция сначала доказывается, вертикаль — следующий милстоун |
| Speaker diarization в v1.7 | whisper.cpp `-tdrz` English-only/экспериментальный, `-di` требует stereo-каналов, которых нет у phone/Zoom-записей — ненадёжный сигнал |
| k8s-в-CI | Отдельная инфра-работа (GitHub Actions + kind/k3d ≠ OrbStack), не смешивать с audio-арком |

## Traceability

Which phases cover which requirements. Updated during roadmap creation.

| Requirement | Phase | Status |
|-------------|-------|--------|
| HARD-01 | Phase 29 | Complete |
| HARD-02 | Phase 29 | Complete |
| HARD-03 | Phase 29 | Complete |
| HARD-04 | Phase 29 | Complete |
| AUD-01 | Phase 30 | Complete |
| AUD-02 | Phase 30 | Complete |
| AUD-03 | Phase 30 | Complete |
| AUD-04 | Phase 30 | Complete |
| AUD-05 | Phase 31 | Complete |
| AUD-06 | Phase 32 | Pending |
| AUD-07 | Phase 32 | Pending |
| AUD-08 | Phase 33 | Pending |

**Coverage:**
- v1 requirements: 12 total
- Mapped to phases: 12 (Phase 29: HARD-01..04; Phase 30: AUD-01/02/03/04; Phase 31: AUD-05; Phase 32: AUD-06/07; Phase 33: AUD-08)
- Unmapped: 0

---
*Requirements defined: 2026-07-17*
*Last updated: 2026-07-17 after v1.7 roadmap creation (traceability mapped to Phases 29-33)*
