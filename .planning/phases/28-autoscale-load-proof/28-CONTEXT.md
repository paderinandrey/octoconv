# Phase 28: Autoscale Load-Proof - Context

**Gathered:** 2026-07-17
**Status:** Ready for planning
**Source:** Phase 27 artifacts (gate script, review, verification), user discussion

<domain>
## Phase Boundary

Флагманская live-приёмка милстоуна v1.6 (KEDA-03): таймстампированное доказательство 0→N→0 под залповой нагрузкой на image-классе и graceful-даунскейл долгой document-конверсии — на уже развёрнутой Phase 24/27 инфраструктуре. Плюс один точечный chart-фикс (WR-02 из 27-REVIEW), напрямую мешающий load-proof. НЕ входит: тюнинг триггеров под прод, WR-01 (api-outage семантика пустого PromQL), любые изменения приложения.

</domain>

<decisions>
## Implementation Decisions

### Таймстамп-доказательство (SC4 — hard deliverable)
- **D-01:** CSV-семплер: гейт каждые ~5s пишет строку (ISO-timestamp, queue_depth per state из PromQL, ready-реплики per Deployment из kubectl) на протяжении всего сценария steady→burst→drain→zero. CSV — первичное доказательство.
- **D-02:** После прогона из CSV рендерится PNG-график: queue depth и pod count двумя сериями на одной временной оси (gnuplot или python/matplotlib — что доступно локально; инструмент на усмотрение планировщика, факт PNG обязателен).
- **D-03:** Evidence-артефакты коммитятся в `.planning/phases/28-autoscale-load-proof/evidence/` (CSV, PNG, таймстампированный лог гейта) вместе с SUMMARY — это и есть «доказано с таймстампами», приёмка должна пережить сессию.

### Бёрст 0→N (SC1/SC2)
- **D-04:** Залп 20 одинаковых image-джобов (png→jpg, параллельные curl) на очередь при воркере на ИСТИННОМ нуле (пред-проверки: replicas=0, очередь пуста, external-метрика читается — как в Phase 27 гейте). При threshold=5/maxReplicas=4 HPA целится в 4 реплики; гейт ассертит SC1 буквально (≥2 реплик за 60s), достижение 4 фиксируется в evidence как факт, не ассерт.
- **D-05:** Бёрст только по image-классу — SC1 говорит только про image; doc/html 0→1 уже доказаны Phase 27; мульти-класс залп размывает тайминги на общей VM. Фикстура среднего размера, чтобы очередь не осушилась до срабатывания скейла (размер — деталь планировщика, калибруется).
- **D-06:** N→0 нога (SC2): после осушения очереди воркер возвращается к 0 в пределах cooldown-окна — тот же семплер фиксирует time-to-drain и time-to-zero.

### Механика SC3 (graceful-даунскейл долгого джоба)
- **D-07:** Долгий джоб = сгенерированный тяжёлый docx (сотни страниц/таблиц), целящийся в ~200s конверсии на LibreOffice этой VM; генератор калибруется одним пробным прогоном; итоговое время с запасом ОТ обоих концов: заметно > cooldown и < DOCUMENT_ENGINE_TIMEOUT 300s.
- **D-08:** Сценарий даунскейла: 2 document-джоба (короткий + долгий) скейлят document-worker 0→2 (threshold=1); короткий завершается, сигнал pending+active падает до 1 → KEDA/HPA даунскейлит 2→1. Детерминизм выбора жертвы: гейт ставит `controller.kubernetes.io/pod-deletion-cost` с НИЗКИМ значением на ЗАНЯТЫЙ под (низкий cost = удаляется первым) до срабатывания даунскейла — сам даунскейл остаётся штатным KEDA-событием, аннотация влияет только на выбор пода. НЕ использовать kubectl delete pod (это не KEDA-даунскейл, SC3 требует буквально «survives a KEDA downscale event»).
- **D-09:** Доказательство SC3 — тройная проверка: (1) джоб дошёл до done и результат скачивается; (2) `job_events` содержит ровно один переход queued→active (нет ложного ретрая); (3) таймстампы подтверждают: SIGTERM пода произошёл ДО завершения джоба, под завершился до истечения terminationGracePeriodSeconds (т.е. graceful, не SIGKILL). Опирается на per-class ShutdownTimeout 310s из 27-01.

### Скоуп hardening (из 27-REVIEW)
- **D-10:** WR-02 входит в Phase 28: не рендерить `spec.replicas` в Deployment'ах scaled-классов, когда `keda.enabled && prometheus.enabled` (иначе каждый helm upgrade сбрасывает scaled-to-zero класс на 1; гейт 27-03 уже костылит вокруг этого — костыль после фикса упростить/убрать). Фикс чарта + offline-проверка рендера + upgrade-идемпотентность.
- **D-11:** WR-01 (падение api → пустой PromQL-результат читается как 0 при ignoreNullValues=true → даунскейл с живым бэклогом) — НЕ в этой фазе: отдельная работа с трейдоффами семантики триггера, менять поведение прямо перед флагманской приёмкой рискованно. В бэклог/следующий милстоун.

### Дисциплина
- **D-12:** OrbStack-дисциплина Phase 24/27 без изменений: sequential pre-builds, compose и k8s никогда одновременно, гейт самодостаточен (ставит KEDA сам, teardown через EXIT-trap). Load-proof-гейт расширяет/переиспользует паттерн `scripts/keda-gate.sh` (отдельный скрипт или расширение — на усмотрение планировщика, но Phase 27 гейт должен остаться рабочим как есть).

### Claude's Discretion
- Шаг семплера (~5s), инструмент рендера PNG, точный размер image-фикстуры и параметры docx-генератора
- Отдельный `scripts/keda-load-proof.sh` vs режим в существующем гейте
- Формат evidence-лога; имена файлов в evidence/
- Как именно читать таймстампы SIGTERM/завершения пода (kubectl events vs pod status vs логи воркера)

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### База Phase 27 (инфраструктура, на которой доказываем)
- `scripts/keda-gate.sh` — рабочий live-гейт: install-flow, live external-metric discovery, teardown-trap, обход WR-02 (строки ~228-240 — после D-10 упростить)
- `.planning/phases/27-keda-autoscaling/27-03-SUMMARY.md` — evidence-паттерн и two Rule-1 deviations (KEDA HPA ownership settling — критично для понимания таймингов)
- `.planning/phases/27-keda-autoscaling/27-REVIEW.md` — WR-01 (в бэклог), WR-02 (в скоуп, D-10), WR-06 (cooldown vs retry backoff — учитывать при интерпретации таймлайна)
- `deploy/chart/octoconv/values.yaml` — залоченные триггер-значения (image threshold 5 / polling 5 / cooldown 60; document threshold 1 / polling 15 / cooldown 120), maxReplicas
- `deploy/chart/octoconv/templates/deployment-worker.yaml`, `deployment-document-worker.yaml` — spec.replicas рендер для D-10
- `cmd/document-worker/main.go`, `cmd/worker/main.go` — per-class ShutdownTimeout (310s/130s) из 27-01

### Roadmap/требования
- `.planning/ROADMAP.md` §Phase 28 — SC1-SC4 дословно (⩾2 реплик за 60s; ~200s джоб; таймстамп-график)
- `.planning/REQUIREMENTS.md` — KEDA-03

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `scripts/keda-gate.sh` — весь install/discovery/assert/teardown каркас переиспользуется; функции postJob/минт клиента уже написаны
- `internal/e2e` фикстуры (sample.png/docx) — стартовая точка; для D-05/D-07 нужны более тяжёлые
- Phase 27 evidence-практика (таймстампы в SUMMARY) — расширяется до CSV/PNG

### Established Patterns
- Live-гейт: самодостаточный bash, set -euo pipefail, EXIT-trap teardown, exit code = гейт
- Никогда не хардкодить external-metric имя — live discovery (Pitfall 5 Phase 27)
- KEDA берёт ownership Deployment только после первого cooldown — все пред-проверки «истинного 0» должны поллить, не ассертить мгновенно (Rule-1 fix 27-03)

### Integration Points
- `deploy/chart/octoconv/templates/deployment-{worker,document-worker,chromium-worker}.yaml` — условный рендер spec.replicas (D-10)
- `.planning/phases/28-autoscale-load-proof/evidence/` — новый артефакт-каталог (D-03)

</code_context>

<specifics>
## Specific Ideas

- График должен показывать все пять означенных на роудмапе отметок: steady state → burst → time-to-first-replica → time-to-drain → time-to-scale-to-zero
- pod-deletion-cost аннотация снимается/неважна после teardown — она ставится гейтом на конкретный под, в чарт не входит
- SC3-прогон и SC1/SC2-прогон — независимые сценарии одного гейта (document-очередь не участвует в image-бёрсте)

</specifics>

<deferred>
## Deferred Ideas

- WR-01: семантика пустого PromQL-результата при падении api (ignoreNullValues vs ложные срабатывания) — бэклог/следующий милстоун (D-11)
- Продакшен-тюнинг threshold/cooldown по результатам load-proof — вне фазы; наблюдения зафиксировать в SUMMARY как вход для будущего
- kube-state-metrics/полный observability-стек — по-прежнему непропорционально

</deferred>

---

*Phase: 28-autoscale-load-proof*
*Context gathered: 2026-07-17*
