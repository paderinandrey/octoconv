# Phase 29: v1.6 Hardening Tail - Discussion Log

> **Audit trail only.** Not consumed by downstream agents — decisions live in CONTEXT.md.

**Date:** 2026-07-18
**Phase:** 29-v1-6-hardening-tail
**Areas discussed:** HARD-01 + KEDA-warning scope, HARD-02 acceptance script, HARD-04 direct-dial, phase structure

---

## HARD-01: подход к WR-01 + scope соседних KEDA-ворнингов

| Option | Selected |
|--------|----------|
| flip ignoreNullValues: false (fallback держит 1 реплику) | ✓ |
| Оставить true + absent()-алерт | |

| Option | Selected |
|--------|----------|
| Оба соседа (WR-02 checksum + WR-06 retry) | ✓ |
| Только WR-01 + WR-02 | |
| Только WR-01 | |

| Option (WR-06 approach) | Selected |
|--------|----------|
| Добавить retry в PromQL-триггер (state=~pending\|active\|retry) | ✓ |
| Только задокументировать invariant | |

**User's choice:** flip ignoreNullValues:false; взять WR-02 (checksum) и WR-06 (retry-в-query) в scope.

---

## HARD-02: форма acceptance-скрипта

| Option | Selected |
|--------|----------|
| Расширить presets-rest-acceptance.sh (system-scope секция) | ✓ |
| Отдельный скрипт | |

**User's choice:** расширить существующий; OPERATOR_CLIENT_IDS пробросить в compose api.

---

## HARD-04: доказательство direct-dial

| Option | Selected |
|--------|----------|
| Пред-проверка демона + прямой curl в keda-gate.sh | ✓ |
| Отдельный мини-гейт-скрипт | |

**User's choice:** шаг в keda-gate.sh с health pre-flight + прямой curl, loud-fail при клине.

---

## Структура фазы

| Option | Selected |
|--------|----------|
| Группировка: chart (A) / compose (B) / live (C) | ✓ |
| Один план, 4 таска | |
| На усмотрение планировщика | |

**User's choice:** 3 плана; HARD-01+gate-template-fix вместе в План A (общий ScaledObject-владелец, нельзя параллельно).

---

## Claude's Discretion
- Форма checksum-аннотации, порядок волн, механика рестарта compose api, точные диффы шести gate-tooling фиксов

## Deferred Ideas
- absent()-алертинг (альтернатива flip, не нужна), kube-state-metrics, k8s-в-CI, is_operator-колонка
