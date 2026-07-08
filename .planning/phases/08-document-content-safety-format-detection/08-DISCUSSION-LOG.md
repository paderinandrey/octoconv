# Phase 8: Document Content Safety & Format Detection - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-07-09
**Phase:** 8-Document Content Safety & Format Detection
**Areas discussed:** Лимит zip-bomb защиты, Политика по макросам

---

## Лимит zip-bomb защиты

| Question | Option chosen |
|---|---|
| Метрика | Общий несжатый размер всех записей в ZIP (не compression-ratio на запись) |
| Значение по умолчанию | 500 MiB |

**Notes:** По аналогии с Phase 7's `MAX_IMAGE_PIXELS` (общий пиксельный бюджет вместо отдельных ограничений по измерению) — простая метрика на основе заявленных метаданных, без декомпрессии. 500 MiB даёт запас для легитимных крупных xlsx/pptx с множеством встроенных изображений, но всё равно отсекает классические zip-бомбы (обычно расширяются до ГБ/ТБ).

Рассматривался и отклонён вариант per-entry compression-ratio: точнее ловит крайние случаи, но сложнее подобрать порог без ложных срабатываний на легитимных документах.

## Политика по макросам

| Option | Selected |
|--------|----------|
| Жёсткий блок всегда, без опций | ✓ |
| Опциональный флаг отключения (как WEBHOOK_ALLOW_PRIVATE_IPS) | |

**Notes:** В отличие от Phase 5 (где приватные IP — легитимный сценарий для внутренних деплоев), макросы не нужны ни для одного легитимного кейса PDF-конвертации — код макроса не исполняется при генерации PDF. Опция отключения не нужна.

## Claude's Discretion

- Имя env var для zip-bomb лимита (напр. MAX_DOCUMENT_UNCOMPRESSED_SIZE)
- Файл/структура кода для OOXML/ODF container-inspection (напр. internal/convert/docsniff.go)
- Точный механизм HasDimensionLimit-guard для устранения регрессии с dimension-check
- Объединение macro-check и zip-bomb-check в один проход разбора central directory vs раздельные проходы

## Deferred Ideas

Нет — DOC-V2-02 (password-protected pre-flight) и DOC-V2-05 (active anti-DoS) уже зафиксированы в REQUIREMENTS.md на уровне milestone.
