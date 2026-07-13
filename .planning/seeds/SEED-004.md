---
status: dormant
planted_during: "v1.5 milestone (MCP Access & Document Fidelity), Phase 22 execution, 2026-07-13"
trigger_when: "Planning the milestone after v1.5 ships — the compose stack is functionally complete (3 engines + webhooks + presets + MCP + CI), so the next infrastructure leap is validating the stated production future: Kubernetes + KEDA"
---

# SEED-004: Local k8s + KEDA full-stack validation

## The problem

PROJECT.md constraints have said «Kubernetes + KEDA — будущее» since v1.0, and the
engine-class architecture (отдельные asynq-очереди image/document/html/webhook)
была спроектирована именно под независимый автоскейл воркеров — но ни разу не
проверялась в k8s. Compose ≠ k8s: сеть, DNS, probes, Jobs, secrets. Пока порт не
сделан, «production future» — это неподтверждённая гипотеза.

## Decided design sketch (from the planting discussion)

- **Кластер:** OrbStack built-in Kubernetes (уже установлен у оператора, включается
  одной кнопкой) — ноль нового тулинга; kind/k3d как fallback.
- **Манифесты:** Helm-чарт или kustomize — Deployments для api/воркеров (probes из
  готовых compose-healthcheck'ов), StatefulSet/операторные Postgres+Redis+MinIO или
  внешние, Secrets/ConfigMaps из .env-контракта, `cmd/migrate` и createbucket как
  Jobs/initContainers.
- **KEDA:** ScaledObjects per engine-class на УЖЕ СУЩЕСТВУЮЩУЮ Prometheus-метрику
  глубины очереди (не redis-scaler по внутренностям asynq) — это целевая проверка
  всей engine-class архитектуры.
- **E2E:** адаптация live-набора под in-cluster прогон (ресивер вебхуков как под/Job).

## Known porting landmines (found by code inspection at planting time)

1. `METRICS_ADDR: 127.0.0.1:9090` — в поде метрики недостижимы для скрейпера;
   нужно `0.0.0.0` + NetworkPolicy вместо localhost-изоляции (и asynqmon так же).
2. `host.docker.internal:host-gateway` трюк E2E webhook-ресивера в k8s не существует
   — нужен in-cluster ресивер.
3. `createbucket` one-shot и migrate → k8s Jobs с правильным ordering (в compose это
   depends_on + healthcheck).
4. Compose-DNS имена в presigned URL (`minio:9000`) — проблема, решённая dial-redirect'ом
   в e2e и MCP-клиенте, в k8s встаёт по-новому (Services/ingress именование).

## Why this matters

Снимает последний «отложенный» констрейнт PROJECT.md честным путём и превращает
KEDA-довод engine-class архитектуры из дизайн-намерения в проверенный факт. Плюс
даёт операционный артефакт (чарт), с которого начинается любой реальный деплой.

## When to Surface

- При планировании милстоуна после v1.5 (v1.6).
- Если появится реальный k8s-таргет деплоя у компании.
- Если понадобится нагрузочная проверка автоскейла воркеров.
