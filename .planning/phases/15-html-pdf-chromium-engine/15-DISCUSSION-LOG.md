# Phase 15: HTML→PDF Chromium Engine - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-07-11
**Phase:** 15-html-pdf-chromium-engine
**Areas discussed:** Механизм сетевой изоляции, JavaScript в рендере, Набор print-опций, Детект HTML на входе

---

## Механизм сетевой изоляции

| Option | Description | Selected |
|--------|-------------|----------|
| CLI + слои (Рекомендую) | one-shot chromium-headless-shell + dead-proxy (127.0.0.1:9) + host-resolver-rules MAP*~NOTFOUND + живой adversarial-тест; ноль новых Go-зависимостей; chromedp как эскалация при утечке | ✓ |
| CDP через chromedp | Протокольная интерцепция через DevTools, deny-all-except-file://; самый fail-closed, но +зависимость и отход от one-shot паттерна | |
| OS-уровень: netns | chromium в пустом network namespace; самая жёсткая гарантия, но требует привилегий под USER nobody, реализуемость не подтверждена | |

**User's choice:** CLI + слои

| Option | Description | Selected |
|--------|-------------|----------|
| Канареечный листенер (Рекомендую) | HTTP-листенер в compose-сети, HTML ссылается через img+JS-fetch; ассерт — ноль входящих соединений + PDF создан; прямое доказательство | ✓ |
| Только метаданные-IP | HTML → 169.254.169.254; ассерт только «рендер завершился»; не доказывает отсутствие запроса | |

**User's choice:** Канареечный листенер

---

## JavaScript в рендере

| Option | Description | Selected |
|--------|-------------|----------|
| JS выключен (Рекомендую) | --blink-settings=scriptEnabled=false; убирает JS-fetch/rebinding/renderer-эксплойты (критично при --no-sandbox), детерминированный рендер, waitDelay не нужен | ✓ |
| JS включён | Полный рендер под сетевой блокировкой; поддержка JS-документов, но JS-движок как attack surface + нужен waitDelay | |

**User's choice:** JS выключен

---

## Набор print-опций

| Option | Description | Selected |
|--------|-------------|----------|
| Минимум + landscape (Рекомендую) | page_size enum (a4/letter/legal/a3/a5), margin_mm 0–50, landscape, print_background; без waitDelay/произвольных размеров | ✓ |
| Строго минимум HTML-03 | Только page_size+margins+print_background, landscape отложить | |
| Расширенный | + раздельные поля, произвольный размер, scale; больше поверхности валидации без запроса | |

**User's choice:** Минимум + landscape

---

## Детект HTML на входе

| Option | Description | Selected |
|--------|-------------|----------|
| Расширение + контент-чек (Рекомендую) | source_format=html/.html/.htm + fail-closed контент-чек (UTF-8 без NUL + HTML-маркер в начале); бинарь под .html → 422 до S3 | ✓ |
| Только расширение | Доверяем .html без контент-проверки; проще, но ломает «вход валидируется структурно» и даёт мусорные «успешные» конвертации | |
| Строгий HTML-парсинг | Полный x/net/html до приёма; +зависимость, строгость иллюзорна (HTML парсится «как угодно») | |

**User's choice:** Расширение + контент-чек

---

## Claude's Discretion

- Точные chromium-флаги (verify live: scriptEnabled, размерные флаги, --print-to-pdf в headless-shell)
- HTML_ENGINE_TIMEOUT/HTML_WORKER_CONCURRENCY и terminal-классификация таймаута
- Структура opts для html и диспатч opts по движкам
- Форма миграции jobs.engine CHECK и волновая координация со скаффолдингом
- Лимит размера HTML-файла
- Точный вид canary-листенера в e2e

## Deferred Ideas

- JS-рендеринг — отдельным решением при реальном клиенте
- CDP/chromedp — запасной путь при утечке CLI-блокировки
- Расширенные print-опции (раздельные поля, произвольный размер, scale)
- Кастомные шрифты / CJK-RTL — v2 (DOCV3-03)
- URL-fetch вход — явно Out of Scope
