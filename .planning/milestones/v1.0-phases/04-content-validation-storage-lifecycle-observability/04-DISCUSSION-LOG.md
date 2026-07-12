# Phase 4: Content Validation, Storage Lifecycle & Observability - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-07-07
**Phase:** 4-Content Validation, Storage Lifecycle & Observability
**Areas discussed:** Строгость валидации содержимого, Сроки хранения в S3/MinIO, Состав Prometheus-метрик, Health-эндпоинт и exposure asynqmon

---

## Строгость валидации содержимого

| Question | Selected |
|---|---|
| Detected format differs from declared extension (both supported) | Всегда 422 |
| Magic bytes match no known signature | 422 |
| Detection approach | Свой жёсткий список сигнатур |
| 422 error message detail | Детальное сообщение |
| Check order: magic bytes vs pair-check | Сначала magic bytes, потом pair-check |
| S3 Content-Type metadata | Перезаписать на detected |
| Peek buffer size | Маленький peek под сигнатуры |
| Log rejection on API side | Да, log.Printf с client_id |
| Declared-dimension / decompression-bomb limit | Нет, отложить (deferred) |

**Notes:** User picked the recommended option on every question in this area. The dimension-limit question was explicitly deferred rather than built.

---

## Сроки хранения в S3/MinIO

| Question | Selected |
|---|---|
| TTL scope (single vs separate for uploads/results) | Один TTL на оба, конфигурируем |
| Default duration | 7 дней |
| Mechanism | MinIO ILM правило при старте сервиса |

**Notes:** All recommended options selected. No follow-up questions raised.

---

## Состав Prometheus-метрик

| Question | Selected |
|---|---|
| Job-outcome metric labels | engine + status только |
| Job-duration histogram (beyond ROADMAP minimum) | Да, добавить |
| Reconciler recovery/exhausted counter (not named in ROADMAP) | Да, добавить |

**Notes:** All recommended options selected.

---

## Health-эндпоинт и exposure asynqmon

| Question | Selected |
|---|---|
| Health-check depth | Лёгкий ping с таймаутом |
| Degraded response shape | 503 + JSON с деталями по каждой зависимости |
| asynqmon deployment/access | Отдельный сервис в docker-compose, порт только localhost |

**Notes:** All recommended options selected.

---

## Claude's Discretion

- Exact magic-byte signature bytes/offsets per currently-registered format
- Exact Prometheus metric names/types and histogram bucket boundaries
- Package location for the magic-byte detector/signature table
- Exact minio-go ILM API call sequence and startup idempotency handling
- asynqmon image tag and docker-compose port value

## Deferred Ideas

- Declared-image-dimension / decompression-bomb protection (separate hardening item, not this phase)
- Per-client or per-error-code labels on job-outcome metrics (cardinality concern, revisit later)
- Basic-auth or other access control on asynqmon (deferred in favor of localhost-only binding)
