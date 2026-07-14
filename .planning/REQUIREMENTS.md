# Requirements: OctoConv — Milestone v1.6 Kubernetes & KEDA

**Defined:** 2026-07-14
**Core Value:** Внутренние сервисы компании могут безопасно (через аутентификацию по API-ключу) и надёжно поставить задачу конвертации файла (изображения, офисные документы, HTML) и получить результат — без риска для стабильности или безопасности продакшена.

## v1 Requirements

Requirements for this milestone. Each maps to roadmap phases.

### Kubernetes / Helm

- [ ] **K8S-01**: `helm install` разворачивает полный стек на OrbStack k8s (api, worker, document-worker, chromium-worker, webhook-worker×2 (+mcp-http добавится в чарт в Phase 25) + Postgres/Redis/MinIO StatefulSets + createbucket hook Job + миграции через самомиграцию api при старте — существующий механизм); E2E-набор проходит внутри кластера как Job
- [ ] **K8S-02**: Все четыре SEED-004 мины закрыты: METRICS_ADDR → 0.0.0.0 (values-only, ноль изменений Go-кода) + NetworkPolicy; in-cluster E2E-Job получает свой pod-IP через Downward API (закрывает host-gateway и S3-dial-redirect разом); ordering: миграции через api self-migration, createbucket через post-install hook; FQDN `S3_ENDPOINT` (`minio.<ns>.svc.cluster.local:9000`) — presigned URL резолвится и из подов, и с OrbStack-хоста
- [ ] **K8S-03**: Probes на всех сервисах (api — /healthz; воркеры — metrics-порт после 0.0.0.0-бинда); per-class `terminationGracePeriodSeconds` ≥ engine-таймаута класса (document ≥ 300s, html ≥ 60s, image ≥ 120s)

### KEDA

- [ ] **KEDA-01**: `octoconv_queue_depth` экспонируется always-on api-процессом (перенос/дублирование asynq.Inspector-коллектора из воркеров — жёсткая предпосылка scale-from-zero; при 0 реплик воркера метрика обязана жить)
- [ ] **KEDA-02**: KEDA (helm-установка, актуальная v2.20.x) + ScaledObjects по prometheus-скейлеру для image/document/html воркеров (minReplicaCount 0); webhook-worker исключён из KEDA полностью — фиксированные 2 реплики (единственный хост advisory-lock sweeper'а)
- [ ] **KEDA-03**: Live load-proof: залповая заливка очереди через API → наблюдаемый скейл 0→N→0 с таймстампами (0→N leg проверяется отдельно); scale-down soak — длинная document-конвертация в полёте переживает даунскейл gracefully (asynq graceful shutdown, не SIGKILL mid-job)

### MCP HTTP

- [ ] **MCPH-01**: `cmd/mcp-http` — streamable-HTTP MCP-эндпоинт (go-sdk `NewStreamableHTTPHandler`, уже в запиненном v1.6.1): per-request caller-key pass-through (под НЕ хранит ключей — zero-privilege сохранён; общий ключ сломал бы per-client лимиты и preset-scoping), те же 5 инструментов из transport-agnostic internal/mcpserver; контейнер + Service в чарте
- [ ] **MCPH-02**: Результат `convert_file` в HTTP-режиме remote-пригоден: `local_path`-контракт решён (опции: omit в HTTP-режиме / presigned-only / download-proxy tool — выбор фиксируется на планировании фазы как Key Decision)

### Operator REST

- [ ] **OPER-01**: system-scope пресеты управляются через REST клиентами из `OPERATOR_CLIENT_IDS` env-allowlist (env-only config, ноль миграций); для не-операторов system-write — 404-no-leak (конвенция проекта с Phase 1)

## v2 Requirements

### Infrastructure
- **K8SV2-01**: k8s-валидация в CI (kind/k3d job) — семя следующего милстоуна
- **K8SV2-02**: multi-env values / real-cluster deploy story
- **K8SV2-03**: `is_operator` колонка вместо env-allowlist (если понадобится redeploy-free управление или per-operator аудит)

### Carried
- **MCPV2-02**: MCP resources (host support), **DOCV3-03**: fonts/CJK

## Out of Scope

| Feature | Reason |
|---------|--------|
| k8s в CI | Локальная валидация — этот милстоун; CI-интеграция — отдельное решение (runner-ресурсы) |
| CD / автодеплой | Нет продакшен-таргета |
| Ingress/TLS | Внутренняя локальная валидация; OrbStack даёт localhost-роутинг |
| bitnami-чарты для deps | Пейволл 2025; свои минимальные StatefulSets на уже запиненных образах |
| redis-lists KEDA-скейлер | Опирается на недокументированные внутренности asynq и видит только pending; prometheus-скейлер на своей метрике |

## Traceability

| Requirement | Phase | Status |
|-------------|-------|--------|
| K8S-01 | Phase 24 | Pending |
| K8S-02 | Phase 24 | Pending |
| K8S-03 | Phase 24 | Pending |
| KEDA-01 | Phase 27 | Pending |
| KEDA-02 | Phase 27 | Pending |
| KEDA-03 | Phase 28 | Pending |
| MCPH-01 | Phase 25 | Pending |
| MCPH-02 | Phase 25 | Pending |
| OPER-01 | Phase 26 | Pending |

**Coverage:**
- v1 requirements: 9 total
- Mapped to phases: 9 ✓
- Unmapped: 0

**Phase → requirement rollup:**
- Phase 24 (Helm Chart Core & Landmine Closure): K8S-01, K8S-02, K8S-03
- Phase 25 (MCP Streamable HTTP): MCPH-01, MCPH-02
- Phase 26 (Operator System-Presets REST): OPER-01
- Phase 27 (KEDA Autoscaling): KEDA-01, KEDA-02
- Phase 28 (Autoscale Load-Proof): KEDA-03

---
*Requirements defined: 2026-07-14*
*Last updated: 2026-07-14 after roadmap creation (9/9 mapped)*
