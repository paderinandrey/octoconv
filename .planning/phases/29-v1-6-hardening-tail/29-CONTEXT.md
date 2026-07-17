# Phase 29: v1.6 Hardening Tail - Context

**Gathered:** 2026-07-18
**Status:** Ready for planning
**Source:** v1.6 milestone audit + 27-REVIEW/28-REVIEW findings, user discussion

<domain>
## Phase Boundary

Закрытие четырёх pre-diagnosed hardening-находок аудита v1.6 на чистом chart-субстрате ДО того, как в Phase 33 пишется audio ScaledObject. Чистый Go/chart/bash — ноль audio-зависимостей. НЕ входит: любая audio-работа, k8s-в-CI (K8SV2-01), is_operator-колонка (K8SV2-03).

</domain>

<decisions>
## Implementation Decisions

### HARD-01 + соседние KEDA-робастность (WR-01/WR-02/WR-06)
- **D-01:** WR-01 fix = flip `ignoreNullValues: "true"` → `"false"` на всех трёх ScaledObject-шаблонах (image/document/html). Сустайнед absence метрики (api недоступна) становится scaler error → `fallback.replicas: 1` держит по одной реплике на класс вместо ложного scale-to-zero с живым бэклогом. Цена — безобидный fallback-blip на fresh install / коротком рестарте api до первого enqueue (принимаем, fail-safe в сторону доступности, как webhook-worker fail-closed). Обновить in-template комментарий: удалить рассуждение про «genuinely empty queue» как основание для true.
- **D-02:** WR-02 (взят в scope — один корень с WR-01): добавить `checksum/config` аннотацию в pod-template Prometheus Deployment (`sha256sum` над prometheus.yaml template) — helm upgrade при смене scrape-config катит под, иначе stale scrape вырождается в тот же пустой-результат WR-01. Стандартный Helm-паттерн.
- **D-03:** WR-06 (взят в scope): изменить PromQL-триггер всех трёх ScaledObject на `state=~"pending|active|retry"` (было pending+active) — retry-таски это неизбежная имминентная работа; убирает blind spot, где воркер на 0 реплик не поднимается на retry-бэклог до reconciler-свипа (~12.6 мин worst case). Обновить D-04-комментарий про выбор состояний. МЕНЯЕТ поведение скейлинга — перепроверить живым гейтом (HARD-04 прогон).

### HARD-02 — operator live acceptance (OPER-01)
- **D-04:** Расширить существующий `scripts/presets-rest-acceptance.sh` секцией system-scope (не новый скрипт) — он уже минтит клиентов, держит base-url и cleanup. Секция: operator-клиент делает CRUD `/v1/system/presets`; non-operator получает byte-identical no-leak 404; system-пресет юзабелен в джобе любым клиентом (по образцу Phase 26 acceptance-намерения, которое так и не материализовалось в скрипт).
- **D-05:** Пробросить `OPERATOR_CLIENT_IDS` в compose api-сервис: `OPERATOR_CLIENT_IDS: "${OPERATOR_CLIENT_IDS:-}"` в docker-compose.yml (сейчас переменной там нет — WR-03). Скрипт минтит двух клиентов (operator + regular), экспортит UUID оператора в `OPERATOR_CLIENT_IDS`, пересоздаёт/рестартит api-сервис чтобы подхватить env, затем гоняет матрицу. Против COMPOSE-стека (дешевле k8s; фаза не трогает k8s-специфику API).

### HARD-03 — gate-tooling warnings (шесть из 28-REVIEW)
- **D-06:** Закрыть все шесть, каждый с диффом и re-run соответствующего гейта: (1) falsy-`0` в ScaledObject stabilization-шаблоне (`if hasKey`/explicit-nil вместо truthy-чека, чтобы значение 0 не терялось); (2) SC3 stale-pod гонка в keda-load-proof.sh (не брать earliest creationTimestamp — исключать Terminating); (3) false-PASS download-чек (`curl -f`/http_code); (4) orphaned watcher-процесс (kill child pipeline, не только subshell); (5) pin интерпретатора в render_evidence.py (`uv run --python 3.11` или shebang-guard); (6) CWD-независимая SAMPLE_IMAGE в gen_heavy_docx.py (resolve относительно __file__).

### HARD-04 — presigned direct-dial recheck (K8S-02)
- **D-07:** Шаг в `scripts/keda-gate.sh` (не отдельный install-цикл): (1) пред-проверка здоровья OrbStack-демона/прокси (loud-fail если клин — не маскировать обходом); (2) прямой `curl` по presigned FQDN-URL с OrbStack-хоста БЕЗ `--connect-to`/port-forward; bounded retry. Если демон клинит — гейт честно падает с диагностикой, а не проходит через workaround. Закрывает degraded-transport оговорку из 24-VERIFICATION.

### Структура фазы (планировщику)
- **D-08:** Группировка в 3 плана: План A — chart-робастность оффлайн (HARD-01 D-01/02/03 + HARD-03 gate-шаблон-фикс подпункт 1 — всё трогает ScaledObject/prometheus/values, один владелец шаблонов, оффлайн helm template/lint проверки). План B — HARD-02 compose acceptance (Go-код не трогается, только скрипт+compose). План C — HARD-03 остальные скрипт-фиксы (2-6) + HARD-04 live-гейт присоединение. Причина группировки: HARD-01 и gate-шаблон-фикс HARD-03 оба трогают ScaledObject-шаблон — нельзя параллельно (конфликт), поэтому оба в План A.

### Claude's Discretion
- Точная форма checksum-аннотации (весь prometheus.yaml vs только config-блок)
- Порядок волн (A и B независимы — могут параллельно; C зависит от A по ScaledObject-состоянию для гейт-прогона)
- Как именно скрипт рестартит compose api (`docker compose up -d --force-recreate api` vs down/up)
- Точные диффы шести gate-tooling фиксов в рамках описанного намерения

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### KEDA robustness (HARD-01)
- `deploy/chart/octoconv/templates/scaledobject-image.yaml` (ignoreNullValues:41, PromQL query, fallback block — identical shape in scaledobject-document.yaml / scaledobject-html.yaml)
- `deploy/chart/octoconv/templates/prometheus.yaml` (pod-template — checksum annotation target for WR-02)
- `deploy/chart/octoconv/values.yaml` (keda.* cooldownPeriod values — WR-06 invariant context)
- `internal/queue/queue.go` (retry backoff schedules — the WR-06 invariant these must relate to)
- `.planning/milestones/v1.6-phases/27-keda-autoscaling/27-REVIEW.md` (WR-01/WR-02/WR-06 full text + suggested fixes)

### Operator acceptance (HARD-02)
- `scripts/presets-rest-acceptance.sh` (the script to extend with a system-scope section)
- `docker-compose.yml` (api service env block ~line 77 — add OPERATOR_CLIENT_IDS passthrough)
- `cmd/manage-clients` / `cmd/manage-presets` (how the script mints clients + system presets)
- `internal/api/system_presets_handlers.go` (requireOperator semantics — the no-leak 404 to assert)
- `.planning/milestones/v1.6-phases/26-operator-presets-rest/26-REVIEW.md` (WR-03 compose-passthrough gap)

### Gate tooling + live gate (HARD-03, HARD-04)
- `scripts/keda-load-proof.sh` (SC3 stale-pod, download-check, orphaned watcher, stabilization falsy-0 template consumer)
- `scripts/fixtures/render_evidence.py` (interpreter pin), `scripts/fixtures/gen_heavy_docx.py` (CWD-relative SAMPLE_IMAGE)
- `scripts/keda-gate.sh` (presigned-from-host step target for HARD-04; currently port-forward based)
- `.planning/milestones/v1.6-phases/28-autoscale-load-proof/28-REVIEW.md` (six gate-tooling warnings full text)
- `.planning/milestones/v1.6-phases/24-helm-chart-core/24-VERIFICATION.md` (K8S-02 degraded-transport caveat being closed)

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `scripts/presets-rest-acceptance.sh` — client-mint + base-url + cleanup harness reused for the operator section (D-04)
- `scripts/keda-gate.sh` — self-installing gate with EXIT-trap teardown; HARD-04 direct-dial is a new step inside it
- Phase 28 chart overlays (values-loadproof.yaml) — the stabilization template that HARD-03 fix #1 corrects

### Established Patterns
- ScaledObject templates are values-driven, three near-identical files — a WR-01/WR-06 fix touches all three consistently
- `${VAR:-}` compose env passthrough (existing pattern for optional envs)
- Gate scripts: bash `set -euo pipefail`, loud-fail, EXIT-trap teardown, no silent workarounds

### Integration Points
- `deploy/chart/octoconv/templates/scaledobject-*.yaml` + `prometheus.yaml` (single owner — plan A serializes ScaledObject edits)
- `docker-compose.yml` api service (HARD-02 env passthrough)
- `scripts/keda-gate.sh` STEP for presigned-from-host (HARD-04)

</code_context>

<specifics>
## Specific Ideas

- WR-06 query change is behavior-changing — the HARD-04 live-gate run doubles as its re-verification (retry-backlog scale-up)
- The operator acceptance run needs TWO client keys (operator + regular) minted via manage-clients, operator UUID exported into OPERATOR_CLIENT_IDS via compose env override
- HARD-04 must distinguish "daemon wedged" (loud-fail, investigate) from "presign genuinely broken" (real failure) — the pre-flight health check is what separates them

</specifics>

<deferred>
## Deferred Ideas

- WR-01 `absent()` Prometheus alerting pipeline (the alternative to the flip) — not needed once ignoreNullValues:false lands
- kube-state-metrics / full observability — still disproportionate
- k8s-в-CI (K8SV2-01), is_operator column (K8SV2-03) — separate future work

</deferred>

---

*Phase: 29-v1-6-hardening-tail*
*Context gathered: 2026-07-18*
