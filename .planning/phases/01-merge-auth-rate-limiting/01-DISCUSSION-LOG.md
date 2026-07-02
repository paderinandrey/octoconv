# Phase 1: Merge, Auth & Rate Limiting - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-07-03
**Phase:** 1-Merge, Auth & Rate Limiting
**Areas discussed:** Провижининг API-ключей, Rollout auth, Стратегия слияния ветки

---

## Провижининг API-ключей

| Option | Description | Selected |
|--------|-------------|----------|
| CLI-скрипт оператора | Отдельная команда создаёт запись в `clients`, генерирует ключ, печатает raw-значение один раз | ✓ |
| Прямой SQL/вручную | Оператор вручную вставляет запись и хеш через psql | |
| Защищённый admin HTTP-эндпоинт | `POST /v1/admin/clients` под отдельным admin-ключом | |

**User's choice:** CLI-скрипт оператора
**Notes:** Соответствует hash-only хранению — raw-ключ показывается один раз и не сохраняется.

| Option | Description | Selected |
|--------|-------------|----------|
| Только name | Минимально: id, name, ключи, timestamps | ✓ |
| Name + владеющая команда/контакт | Доп. поле team/contact | |

**User's choice:** Только name
**Notes:** Соответствует уже существующей минимальной схеме `clients` из Notion DDL.

| Option | Description | Selected |
|--------|-------------|----------|
| CLI-команда revoke | Тот же инструмент оператора, помечает хеш неактивным | ✓ |
| Удаление записи из clients | Жёстче, но рвёт FK на jobs.client_id | |

**User's choice:** CLI-команда revoke
**Notes:** Запись в `clients` не удаляется — сохраняется история чьи это были задачи.

| Option | Description | Selected |
|--------|-------------|----------|
| Нет, только ручная | Схема поддерживает два активных ключа, ротация запускается оператором вручную | ✓ |
| Да, с max-age на ключ | Требует фоновой задачи/cron и решения, что делать при просрочке | |

**User's choice:** Нет, только ручная

Дополнительный уточняющий вопрос от пользователя: "Ключ будет храниться в зашифрованном виде?" — разъяснено различие hashing (необратимо) vs encryption (обратимо); пользователь подтвердил hashing (salted SHA-256), не encryption.

---

## Rollout auth

| Option | Description | Selected |
|--------|-------------|----------|
| Жёсткий cutover | Как только auth-мидлвара выкачена — все запросы без валидного ключа получают 401 сразу | ✓ |
| Переходный период (warn-only) | Сначала логируется отсутствие ключа, запрос пропускается | |

**User's choice:** Жёсткий cutover
**Notes:** Сервис ещё не в проде, реальных клиентов нет — переходный период не нужен.

| Option | Description | Selected |
|--------|-------------|----------|
| Остаются без auth | `/healthz` и будущий `/metrics` — вне auth-цепочки | ✓ |
| Тоже требуют ключ | Единый auth-мидлвара на весь router, без исключений | |

**User's choice:** Остаются без auth

---

## Стратегия слияния ветки

| Option | Description | Selected |
|--------|-------------|----------|
| Merge commit | Сохраняет все 7 коммитов в истории отдельно | ✓ |
| Squash | Все 7 коммитов в один на main | |

**User's choice:** Merge commit

| Option | Description | Selected |
|--------|-------------|----------|
| Удалить | После успешного merge ветка больше не нужна | ✓ |
| Оставить как архив | На случай если понадобится сверка с исходным состоянием среза | |

**User's choice:** Удалить

---

## Claude's Discretion

- Конкретные числовые лимиты rate limiting (запросов/мин на клиента, размер burst, порог coarse pre-auth IP-лимита) — пользователь явно делегировал это решение Claude, опираясь на research (FEATURES.md, STACK.md): per-client token bucket по `client_id`, грубый pre-auth IP-guard, 429 + `Retry-After`.

## Deferred Ideas

None — discussion stayed within phase scope.
