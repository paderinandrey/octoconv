# Phase 9: LibreOffice Converter Engine - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-07-09
**Phase:** 9-LibreOffice Converter Engine
**Areas discussed:** DOCUMENT_ENGINE_TIMEOUT значение, Проверка process-group-kill против реального soffice, Критерии валидации выходного PDF

---

## DOCUMENT_ENGINE_TIMEOUT значение

| Option | Selected |
|--------|----------|
| 300с (из milestone research) | ✓ |
| Своё значение | |

**Notes:** Не эмпирически проверено (нет реальных внутренних документов для бенчмарка в этой среде), но разумная стартовая точка — 2.5x общего ENGINE_TIMEOUT (120с).

## Проверка process-group-kill

| Option | Selected |
|--------|----------|
| Живой docker-тест в рамках фазы | ✓ |
| Только unit-тесты + skip без soffice | |

**Notes:** internal/convert/exec.go никогда не проверялся против реального LibreOffice-процесса. Milestone research отметил риск: launcher LibreOffice может форкаться, а не exec'аться, потенциально избегая process-group kill. Это launch-blocking риск, не nice-to-have — нужен реальный docker-тест с пересобранным Dockerfile.worker.

## Критерии валидации выходного PDF

| Option | Selected |
|--------|----------|
| Ненулевой размер + valid %PDF- magic bytes | ✓ |
| Дополнительно проверять %%EOF | |

**Notes:** Согласуется с существующей magic-byte философией (Phase 4's sniff.go), применённой к выводу движка вместо входа клиента. Более глубокая структурная валидация PDF признана избыточной для этой фазы — не наблюдаемый failure mode, добавляет сложность без доказанной необходимости.

## Claude's Discretion

- Точный набор пакетов/шрифтов для Dockerfile.worker (уже специфицирован milestone research, но перепроверить актуальность против bookworm)
- Механика HOME/fontconfig-cache provisioning для USER nobody
- Механизм live-теста process-group-kill (Go test за env-флагом vs отдельный shell/docker-compose шаг)
- Точное место деривации -env:UserInstallation пути (Phase 10 подключит воркер; Phase 9 строит конвертер, принимающий путь параметром)

## Deferred Ideas

Нет — DOC-V2-03 (PDF/A export) и DOC-V2-05 (active anti-DoS) уже зафиксированы в REQUIREMENTS.md на уровне milestone.
