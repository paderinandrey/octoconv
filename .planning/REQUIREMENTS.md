# Requirements: OctoConv — Milestone v1.8 AV Engine (video/ffmpeg)

**Defined:** 2026-07-19
**Core Value:** Внутренние сервисы компании могут безопасно (через аутентификацию по API-ключу) и надёжно поставить задачу конвертации файла (изображения, офисные документы, HTML, аудио, видео) и получить результат — без риска для стабильности или безопасности продакшена.

## v1 Requirements

Requirements for milestone v1.8. Each maps to roadmap phases.

### Конвертации (AVC)

- [ ] **AVC-01**: Клиент может транскодировать видео (mov/avi/mkv/webm) → mp4 (H.264 video + AAC audio, `-movflags +faststart`) через тот же async-пайплайн, что и остальные классы
- [ ] **AVC-02**: Клиент может транскодировать mp4 → webm (VP9/Opus) — всегда полный re-encode, входит в worst-case RTF-гейта
- [ ] **AVC-03**: Клиент может извлечь аудиодорожку из видео → mp3/wav/m4a; при AAC-источнике и m4a-таргете используется stream-copy (`-c:a copy`) вместо re-encode
- [ ] **AVC-04**: Клиент может получить кадр-превью из видео → jpg/png/webp; быстрый input-side `-ss` seek; дефолтный таймкод 1.0s (clamped к длительности) при отсутствии opts
- [ ] **AVC-05**: Транскод использует авто stream-copy fast path: ffprobe-проверка кодека источника → remux вместо re-encode, когда кодек уже легален в target-контейнере

### Opts (AVO)

- [ ] **AVO-01**: Клиент может задать таймкод превью через типизированный закрытый AVOpts allowlist (checkStrictObject-паттерн; клиентские байты никогда не попадают в argv движка — OPTS-01/02 прецедент)
- [ ] **AVO-02**: Клиент может ограничить разрешение выхода транскода закрытым enum высот (480/720/1080) — никаких произвольных WxH-строк
- [ ] **AVO-03**: Клиент может выбрать H.265/HEVC кодек для mp4-таргета через тот же закрытый allowlist (свой CRF-дефолт x265, не копия x264)

### Транскрипт (AVT)

- [ ] **AVT-01**: Клиент может получить транскрипт видео (mp4/mov и др. → txt/srt/vtt/json, контракт whisper verbatim, включая пословные таймстампы): видео-источники добавлены в `AudioConverter.Pairs()` (Engine остаётся audio), задачи едут на существующий audio-воркер; непересечение пар AVConverter/AudioConverter закреплено явным тестом; RTF-допущение `AUDIO_ENGINE_TIMEOUT` перепроверено для видео-источников (demux overhead)

### Инфраструктура (AVE)

- [ ] **AVE-01**: Fail-closed magic-bytes валидация видео-контейнеров до записи в S3: ISO-BMFF ftyp brands (mp4/mov), EBML DocType (mkv vs webm, bounded-peek парсер), RIFF/`AVI ` — с тестами непересечения с существующими снифферами (WAV/RIFF, m4aBrands/heicBrands)
- [ ] **AVE-02**: Guards до дорогой стадии: ffprobe duration-гард (паттерн audioduration.go, свой `AV_MAX_DURATION_SECONDS`) + resolution-probe против decode-bomb; `-protocol_whitelist file,crypto` на каждом вызове ffmpeg/ffprobe (SSRF/LFI через HLS/concat/subtitle-контент внутри контейнера)
- [ ] **AVE-03**: Отдельная av asynq-очередь + `cmd/av-worker` со своим retry schedule и unique-lock TTL из worst-case бюджета; stage-aware transient/terminal классификация выведена заново для видео (transcode-таймаут ≠ audio-прецедент «ffmpeg=terminal»); reconciler-роутинг по `jobs.engine='av'`
- [ ] **AVE-04**: av-worker контейнеризован (Debian ffmpeg с CVE-backport, версия пиненная); `AV_ENGINE_TIMEOUT` измерен RTF-матрицей worst-case (max разрешение × самый дорогой кодек × max длительность) по методологии Phase 32, с NO-GO-рычагами
- [ ] **AVE-05**: compose-сервис + Helm chart (Deployment с grace-периодами от измеренного таймаута) + KEDA ScaledObject с production-паритетом (WR-01 триада verbatim), env-parity (IN-02 паттерн), scale-from-zero live-proof

## v2 Requirements

Deferred to future release. Tracked but not in current roadmap.

### AV расширения

- **AVX-01**: Trim/crop как отдельные validated closed-opts фичи (пара start/end таймкодов) — только при подтверждённом спросе
- **AVX-02**: Registry cold-pull измерение для тяжёлых образов (перенос из v1.7 tech debt, актуально и для av-worker)

## Out of Scope

Explicitly excluded. Documented to prevent scope creep.

| Feature | Reason |
|---------|--------|
| Adaptive streaming (HLS/DASH) | Категорически другой продукт: multi-rendition + сегменты + манифесты + CDN; ломает single-input/single-output модель job'а |
| Редактирование видео (trim/crop/concat/filters/overlays) в этом милстоуне | Каждая операция — новая opts-поверхность без естественного потолка; raw `-vf` строки от клиента — injection/DoS вектор |
| Watermarking / branding overlay | Тот же filter-graph риск + связывает generic-сервис с brand-ассетами; место этому — в потребляющем сервисе поверх OctoConv |
| Свободные CRF/bitrate/preset/resolution opts | Прямо противоречит closed-allowlist дисциплине (OPTS-01/02); CRF 0 на max разрешении — гарантированный пробой таймаута |
| Live/real-time транскодинг, RTMP ingest | Несовместимо с async batch S3-in/S3-out архитектурой — другой продукт, не «поздняя фаза» |
| Multi-input jobs (concat) | Single-input/output assumption снимается только сразу для всех классов, не как видео-исключение |
| GPU/hwaccel энкодеры | В deployment-таргете нет GPU passthrough; кодируем только CPU |
| Внешние transcoding API | Воркеры строго офлайн — константа проекта |

## Traceability

Which phases cover which requirements. Updated during roadmap creation.

| Requirement | Phase | Status |
|-------------|-------|--------|
| AVC-01 | — | Pending |
| AVC-02 | — | Pending |
| AVC-03 | — | Pending |
| AVC-04 | — | Pending |
| AVC-05 | — | Pending |
| AVO-01 | — | Pending |
| AVO-02 | — | Pending |
| AVO-03 | — | Pending |
| AVT-01 | — | Pending |
| AVE-01 | — | Pending |
| AVE-02 | — | Pending |
| AVE-03 | — | Pending |
| AVE-04 | — | Pending |
| AVE-05 | — | Pending |

**Coverage:**
- v1 requirements: 14 total
- Mapped to phases: 0
- Unmapped: 14 ⚠️ (roadmap pending)

---
*Requirements defined: 2026-07-19*
*Last updated: 2026-07-19 after initial definition*
