# Phase 5: Webhook SSRF Private-IP Opt-Out - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-07-08
**Phase:** 5-Webhook SSRF Private-IP Opt-Out
**Areas discussed:** Точная граница отключения проверки, Видимость включённого флага

---

## Точная граница отключения проверки

| Option | Description | Selected |
|--------|-------------|----------|
| Только RFC1918 private | isPrivate() перестаёт блокироваться; loopback/link-local/unspecified остаются заблокированы всегда | ✓ |
| Вся группа целиком | loopback + private + link-local + unspecified отключаются флагом одновременно | |

**User's choice:** Только RFC1918 private
**Notes:** Явно исключили ослабление loopback/link-local (включая cloud metadata endpoint 169.254.169.254) и unspecified — для них нет легитимного сценария callback_url даже во внутренней сети.

---

## Видимость включённого флага

| Option | Description | Selected |
|--------|-------------|----------|
| Лог при старте сервиса | cmd/api/main.go пишет log.Printf при старте, если флаг включён | ✓ |
| Только документация в .env.example | Без лога при старте | |

**User's choice:** Лог при старте сервиса
**Notes:** Согласуется с конвенцией "только cmd/*/main.go логирует" — изменений в internal/* не требуется.

---

## Claude's Discretion

- Точная формулировка строки лога (эмодзи-префикс vs ⚠-префикс)
- Расположение проверки флага (inline в isBlockedIP vs параметр)

## Deferred Ideas

- Per-client opt-out для private-IP вебхуков
- Re-resolve/re-validate callback_url перед каждой попыткой доставки (DNS-rebinding защита)
