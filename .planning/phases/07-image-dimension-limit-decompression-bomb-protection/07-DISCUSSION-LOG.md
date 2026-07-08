# Phase 7: Image Dimension Limit (Decompression-Bomb Protection) - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-07-09
**Phase:** 7-Image Dimension Limit (Decompression-Bomb Protection)
**Areas discussed:** Подход к парсингу размеров, Обработка HEIC, Конкретное значение лимита

---

## Подход к парсингу размеров

User initially asked for clarification on what "writing your own image parser" meant — clarified that this is reading a handful of fixed-position header bytes per format (not decoding pixels), with per-format byte-offset explanations (PNG IHDR, TIFF IFD tags 256/257, JPEG SOF marker scan, WebP VP8/VP8L/VP8X variants, HEIC ispe box).

| Option | Description | Selected |
|--------|-------------|----------|
| Свой mini-parser без зависимостей | Несколько десятков байт заголовка на формат, zero-dep | ✓ |
| golang.org/x/image | Меньше кода, но новая внешняя зависимость + [ASSUMED]-гейт | |
| Shell-out в vipsheader | Унифицировано для всех форматов, но новый process-exec в API | |

**User's choice:** Свой мини-парсер
**Notes:** Согласуется с философией D-03 из Phase 4 (zero new dependencies для content validation).

---

## Обработка HEIC

| Option | Description | Selected |
|--------|-------------|----------|
| Мини-парсер ispe-бокса | Полная защита всех 5 форматов, без исключений | ✓ |
| Пропустить HEIC, принять остаточный риск | Меньше кода, но дыра в защите | |

**User's choice:** Писать ispe-парсер
**Notes:** В отличие от Phase 4's D-09 (который отложил decompression-bomb защиту целиком), здесь HEIC не выделяется как исключение.

---

## Конкретное значение лимита

| Question | Selected |
|---|---|
| Тип ограничения | Общее число пикселей (width×height) |
| Значение по умолчанию | 100 мегапикселей |

**Notes:** 100 МП с запасом покрывает крупные сканы/фото с профессиональных камер, но всё равно отсекает классические decompression bomb (напр. PNG с заявленными 65535×65535 ≈ 4.3 млрд пикселей).

---

## Claude's Discretion

- Точное имя env var для лимита (напр. MAX_IMAGE_PIXELS)
- Точные размеры bounded-read окна на формат (JPEG marker scan, HEIC box walk и т.д.)
- Расположение файла с парсерами (напр. internal/convert/dimensions.go)
- Поведение при невозможности определить размеры в пределах bounded-read окна (склоняться к fail-closed reject)

## Deferred Ideas

Нет — Phase 7 закрывает D-09 из Phase 4, а не откладывает что-то новое.
