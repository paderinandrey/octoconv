# Phase 27: KEDA Autoscaling - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-07-16
**Phase:** 27-keda-autoscaling
**Areas discussed:** Перенос коллектора queue-depth, Дизайн триггеров ScaledObject, Глубина live-гейта Phase 27

(Area offered but not selected: Prometheus — где живёт и что скрейпит → resolved via research defaults under Claude's Discretion.)

---

## Перенос коллектора queue-depth

| Option | Description | Selected |
|--------|-------------|----------|
| Move — только api | Убрать регистрацию из всех 4 воркеров; api регистрирует все очереди; один источник истины, без дубль-серий | ✓ |
| Duplicate — api + воркеры | api регистрирует все, воркеры оставляют свою; дубли-серии в Prometheus | |

| Option | Description | Selected |
|--------|-------------|----------|
| Все 4 очереди, включая webhook | Единообразная наблюдаемость; соответствует SC1 роудмапа | ✓ |
| Только 3 скейлируемые | Минимум для KEDA, но противоречит SC1 и теряет webhook-метрику | |

| Option | Description | Selected |
|--------|-------------|----------|
| Unit + compose-E2E assertion | Unit-тест регистрации всех 4 очередей + E2E-проверка: api /metrics отдаёт метрику, воркеры — нет | ✓ |
| Только существующий E2E-прогон | Быстрее, но метрику не проверяет | |

**User's choice:** Move в api; все 4 очереди; unit + compose-E2E assertion.

---

## Дизайн триггеров ScaledObject

| Option | Description | Selected |
|--------|-------------|----------|
| pending + active | Сигнал не обнуляется пока длинная задача в полёте — нет даунскейла посреди джоба | ✓ |
| Только pending | Проще, но cooldown — единственная защита от преждевременного даунскейла | |
| pending + active + retry | retry с бэкоффом держит реплику впустую между попытками | |

| Option | Description | Selected |
|--------|-------------|----------|
| По классам: image 5 / doc 1 / html 2 | Стартовые демо-значения, обратно пропорционально длительности задач | ✓ |
| Единый threshold=1 везде | Наглядно, но pod churn на быстрых image-задачах | |
| На усмотрение планировщика | Зафиксировать только принцип | |

| Option | Description | Selected |
|--------|-------------|----------|
| maxReplicas: image 4 / doc 2 / html 2 | Потолки под OrbStack VM; значения в values | ✓ |
| Выше: image 8 / doc 3 / html 4 | Зрелищнее, но лимиты памяти превысят разумное для локальной VM | |

| Option | Description | Selected |
|--------|-------------|----------|
| fallback → 1 реплика | fallback.replicas=1 — при падении Prometheus задачи продолжают обрабатываться | ✓ |
| Без fallback | Реплики замирают; тихая остановка конвейера при 0 | |

**User's choice:** pending+active; thresholds image 5 / doc 1 / html 2; maxReplicas image 4 / doc 2 / html 2; fallback 1 реплика.

---

## Глубина live-гейта Phase 27

| Option | Description | Selected |
|--------|-------------|----------|
| Все 3 класса 0→1, полный цикл на image | SC2 буквально; полный →0 по cooldown ждём только на image; + SC1 через kubectl get --raw при 0 реплик; webhook-worker держит 2 | ✓ |
| Только image полный цикл | Быстрее, но doc/html-проблемы (Rosetta, shm) всплывут лишь в Phase 28 | |
| Все 3 класса полный 0→1→0 | Три ожидания cooldown — заметно дольше | |

| Option | Description | Selected |
|--------|-------------|----------|
| KEDA/Prometheus скриптовано в гейте | Идемпотентный helm install запиненной v2.20.x в keda-namespace; Prometheus в нашем чарте за флагом | ✓ |
| KEDA — ручной prerequisite | Гейт не самодостаточен, версия не зафиксирована | |

| Option | Description | Selected |
|--------|-------------|----------|
| keda.enabled=false в values.yaml, true в values-local | Базовый чарт ставится без KEDA CRD; helm template с flag off не рендерит ScaledObjects | ✓ |
| true везде | Чарт падает на кластере без KEDA CRD | |

**User's choice:** все 3 класса 0→1 + полный цикл на image; установка скриптом гейта; keda.enabled=false по умолчанию.

---

## Claude's Discretion

- Prometheus: размещение в чарте за флагом, скрейп api /metrics, обновление networkpolicy-metrics.yaml, scrape interval
- Точка регистрации коллектора в cmd/api/main.go (Inspector lifecycle)
- Раскладка ScaledObject-шаблонов, label-конвенции, failureThreshold у fallback
- Точные pollingInterval/cooldownPeriod по классам (в рамках D-08)
- Проверка семантики asynq Server.Shutdown() на research

## Deferred Ideas

- Залповый 0→N→0 с таймстампами + graceful scale-down soak длинного document-джоба — Phase 28 (KEDA-03)
- Продакшен-тюнинг threshold/cooldown по реальным распределениям — Phase 28
- KEDA/HPA для api/mcp-http — вне скоупа (нет queue-сигнала)
- kube-prometheus-stack — непропорционально для одного PromQL-эндпоинта
