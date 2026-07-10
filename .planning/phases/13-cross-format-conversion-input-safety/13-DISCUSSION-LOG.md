# Phase 13: Cross-Format Conversion & Input Safety - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-07-10
**Phase:** 13-cross-format-conversion-input-safety
**Areas discussed:** Матрица пар, Валидация выхода, Текст 422 для CFB, Охват live E2E

---

## Матрица пар

| Option | Description | Selected |
|--------|-------------|----------|
| 6 симметричных пар | docx↔odt, xlsx↔ods, pptx↔odp — только внутри семейства | ✓ |
| Шире: + кросс-семейные | Межсемейные пары — fidelity непредсказуема, скоуп разбухает | |
| Меньше: только в ODF | Только OOXML→ODF без обратного направления | |

**User's choice:** 6 симметричных пар (Recommended)

| Option | Description | Selected |
|--------|-------------|----------|
| Явный фильтр на пару | filterFor → таблица (source, target)→фильтр, детерминированно | ✓ |
| Автовыбор по расширению | `--convert-to odt` без фильтра — меньше кода, зависит от эвристик LO | |
| You decide | Оставить планировщику/исполнителю | |

**User's choice:** Явный фильтр на пару (Recommended)

---

## Валидация выхода

| Option | Description | Selected |
|--------|-------------|----------|
| Полный SniffContainer | Тот же структурный снифф, что и на входе; детект = целевой формат | ✓ |
| Лёгкий zip-чек | Только «открывается как ZIP» — пропустит неверный формат экспорта | |

**User's choice:** Полный SniffContainer (Recommended)

| Option | Description | Selected |
|--------|-------------|----------|
| Terminal сразу | Как validatePDF; текст ошибки в terminalLibreOfficeSignatures тем же коммитом | ✓ |
| Один ретрай | На случай гонок профиля LO — не наблюдались в v1.2 | |

**User's choice:** Terminal сразу (Recommended)

---

## Текст 422 для CFB

| Option | Description | Selected |
|--------|-------------|----------|
| Оба случая в тексте | «legacy binary or password-protected… convert or remove password» | ✓ |
| Коротко, без подсказок | «unsupported file format» | |
| You decide | Формулировку подберёт исполнитель | |

**User's choice:** Оба случая в тексте (Recommended)

---

## Охват live E2E

| Option | Description | Selected |
|--------|-------------|----------|
| Все 6 пар + CFB-отказ | Расширить internal/e2e всеми парами + live 422 на CFB-фикстуре | ✓ |
| 2 пары + unit остальное | Репрезентативное подмножество живьём | |

**User's choice:** Все 6 пар + CFB-отказ (Recommended)

---

## Claude's Discretion

- Точные имена LibreOffice-фильтров (верифицировать против LO 7.4)
- CFB-фикстура для E2E (имя, способ создания)
- Финальная формулировка 422-текста
- Механика выходного файла/расширения при не-PDF целях

## Deferred Ideas

- Различение legacy vs encrypted CFB — v2 (DOCV3-02)
- Fidelity-метрики кросс-конвертации — вне планки v1.3
