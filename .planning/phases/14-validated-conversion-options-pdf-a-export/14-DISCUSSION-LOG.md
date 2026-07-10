# Phase 14: Validated Conversion Options & PDF/A Export - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-07-10
**Phase:** 14-validated-conversion-options-pdf-a-export
**Areas discussed:** Форма opts в API, Применимость opts к задаче, Глубина PDF/A-проверки в worker, Персистенция и echo opts

---

## Форма opts в API

| Option | Description | Selected |
|--------|-------------|----------|
| Enum-профиль (Рекомендую) | `{"pdf_profile": "pdf/a-2b"}` — закрытый enum, единственное значение сейчас; -1b/-3b добавятся без ломки API | ✓ |
| Boolean-флаг | `{"pdf_a": true}` — минимальная поверхность, но расширение потребует второго поля/депрекации | |

**User's choice:** Enum-профиль

| Option | Description | Selected |
|--------|-------------|----------|
| JSON в одном поле (Рекомендую) | Одно form-поле `opts` с JSON-строкой, json.Decoder + DisallowUnknownFields, масштабируется на Phase 15 | ✓ |
| Отдельные form-поля | `opts.pdf_profile=...` — проще для curl, плоская структура, хуже масштабируется | |

**User's choice:** JSON в одном поле

---

## Применимость opts к задаче

| Option | Description | Selected |
|--------|-------------|----------|
| 422 fail-closed (Рекомендую) | Неприменимая к (engine, target) опция → 422 до записи в S3; молчаливый игнор скрывает ошибку клиента | ✓ |
| Молча игнорировать | Мягче для клиентов, но противоречит fail-closed философии проекта | |

**User's choice:** 422 fail-closed

| Option | Description | Selected |
|--------|-------------|----------|
| Вместе с format-pair (Рекомендую) | Два шага: синтаксис, затем применимость после format-pair валидации; логика применимости в internal/convert | ✓ |
| Вся валидация в API-слое | Проще сейчас, но Phase 15 придётся растаскивать | |
| You decide | На усмотрение планировщика | |

**User's choice:** Вместе с format-pair

---

## Глубина PDF/A-проверки в worker

| Option | Description | Selected |
|--------|-------------|----------|
| Worker-side terminal (Рекомендую) | validateDocumentOutput требует /GTS_PDFA-маркер при запрошенном профиле; отсутствие — terminal fail + синхронное обновление terminalLibreOfficeSignatures | ✓ |
| Только e2e-assertion | Меньше кода, но регрессия LO молча отдаёт обычный PDF под видом PDF/A | |

**User's choice:** Worker-side terminal

---

## Персистенция и echo opts

| Option | Description | Selected |
|--------|-------------|----------|
| Нормализованную структуру (Рекомендую) | В jobs.options — валидированная структура; сырые клиентские байты не переживают handleCreateJob | ✓ |
| Сырой клиентский JSON | Аудит-след оригинала, но каждый читатель обязан ре-валидировать | |

**User's choice:** Нормализованная структура

| Option | Description | Selected |
|--------|-------------|----------|
| Да, echo в обоих (Рекомендую) | opts (нормализованные) в POST 201 и GET /v1/jobs/{id}; omitempty для пустых | ✓ |
| Нет, не возвращаем | Меньше изменений, хуже наблюдаемость | |

**User's choice:** Да, echo в обоих

| Option | Description | Selected |
|--------|-------------|----------|
| Строгий парсинг, без дубля (Рекомендую) | Worker: DisallowUnknownFields в ту же структуру, мусор → terminal; бизнес-правила не дублируются | ✓ |
| Полная ре-валидация | Максимальная паранойя, но два места истины разъезжаются | |

**User's choice:** Строгий парсинг, без дубля

---

## Claude's Discretion

- Сигнатура/размещение opts-структуры и её путь через payload/Postgres
- Точный вид OutputIntent-грепа (/GTS_PDFA2 vs семейство /GTS_PDFA) — сверить с живым LO 7.4
- Формулировки 422-сообщений
- Форма injection-теста (unit + live состав)
- Лимит размера поля opts, семантика пустого `{}` vs отсутствующего поля

## Deferred Ideas

- PDF/A-1b / PDF/A-3b профили — когда появится реальный клиент
- veraPDF полная валидация — v2 (DOCV3-01)
- Print-опции HTML-движка — Phase 15 (HTML-03)
