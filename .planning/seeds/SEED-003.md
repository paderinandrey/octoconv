---
status: dormant
planted_during: "v1.4 milestone (CI, Presets & Debt Cleanup) planning session, 2026-07-12"
trigger_when: "Planning the milestone after v1.4 ships — presets (Phase 18) are the natural prerequisite: an MCP convert tool is dramatically cleaner when it can reference named presets instead of exposing the raw opts allowlist"
---

# SEED-003: MCP-сервер для OctoConv

## The problem

Внутренние AI-агенты и ассистенты (Claude Code сессии разработчиков, внутренние
агентские пайплайны) не могут использовать OctoConv естественным образом — им
нужно вручную собирать multipart-запросы, поллить статус и скачивать результат
по presigned URL. Model Context Protocol — стандартный способ отдать сервис
агентам как набор инструментов.

## Decided design sketch (from the planting discussion)

- **Тонкий MCP-сервер поверх существующего HTTP API** (`cmd/mcp-server`), НЕ
  прямой доступ к Postgres/S3/очереди: auth, rate limiting, валидация контента
  и fail-closed логика остаются в одном месте. MCP-сервер держит обычный
  API-ключ клиента в env и ходит в `POST /v1/jobs` / `GET /v1/jobs/{id}`.
- **Инструменты:** `convert_file(path, target_format|preset, opts?)` —
  блокирующий вызов: multipart-upload, внутренний поллинг до done/failed,
  скачивание результата (для агентского UX один вызов лучше, чем job_id);
  плюс `get_job_status(job_id)` / `download_result(job_id)` для длинных задач,
  `list_supported_formats` (из реестра convert.Default), `list_presets`
  (после Phase 18).
- **SDK/транспорт:** официальный `modelcontextprotocol/go-sdk` (или зрелый
  `mark3labs/mcp-go`); stdio для локального использования разработчиками;
  streamable HTTP — если понадобится общий внутренний MCP-эндпоинт.
- Объём — примерно одна фаза: один бинарник, тонкая обёртка, минимум
  зависимостей.

## Why this matters

Пресеты (v1.4 Phase 18) + MCP = внутренние сервисы и агенты получают
конвертацию как «одну кнопку»: `convert_file(path, preset="archive-pdf")`.
Это прямое продолжение Core Value — безопасная и надёжная конвертация для
внутренних потребителей, теперь включая агентских.

## When to Surface

- При планировании милстоуна после v1.4 (v1.5) — пресеты уже отгружены.
- Если внутренняя команда попросит агентский/программный доступ «попроще, чем
  сырой HTTP API».
- Если появится внутренний каталог MCP-серверов компании, куда OctoConv
  должен быть подключён.
