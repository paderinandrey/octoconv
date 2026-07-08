# Requirements: OctoConv

**Defined:** 2026-07-08
**Core Value:** Внутренние сервисы компании могут безопасно и надёжно поставить задачу конвертации изображения и получить результат — без риска для стабильности или безопасности продакшена.

## v1.1 Requirements

Milestone v1.1 "Tech Debt Cleanup" — закрытие tech debt, выявленного на аудите milestone v1.0 (`.planning/milestones/v1.0-MILESTONE-AUDIT.md`). Чисто закрывающий релиз, без новых возможностей.

### Webhooks

- [ ] **WEBHOOK-06**: Оператор может отключить SSRF-блокировку приватных IP (`WEBHOOK_ALLOW_PRIVATE_IPS`) для деплоев на внутренней сети компании, где `callback_url` легитимно указывает на RFC1918/loopback-адрес

### Reconciler

- [ ] **RECON-04**: Reconciler находит задачи в статусе `done`/`failed` с непустым `callback_url`, для которых нет ни одной записи в `webhook_deliveries`, и инициирует доставку вебхука (закрывает гонку потери вебхука при сбое Redis в момент завершения задачи)
- [ ] **RECON-05**: Восстановление зависших `queued`/`active` задач подтверждено реальным wall-clock soak-тестом (не только интеграционными тестами против живой БД)

### Content Validation

- [ ] **VALID-03**: API отклоняет загрузку, если заявленные размеры изображения (ширина × высота) превышают настраиваемый лимит, до запуска конвертации (защита от decompression bomb)

## Out of Scope

| Feature | Reason |
|---------|--------|
| Новые классы движков (document/av/archive) | Следующий этап роста продукта, не hardening-cleanup |
| CAD-движок | Открытый вопрос по SDK не решён |
| Per-client/per-error-code лейблы в метриках | Отложено в Phase 4 из-за риска unbounded cardinality; не тех. долг v1.0, отдельное решение |
| Basic-auth для asynqmon | Отложено в Phase 4 в пользу localhost-only биндинга; revisit только при смене модели деплоя |

## Traceability

Which phases cover which requirements. Updated during roadmap creation.

| Requirement | Phase | Status |
|-------------|-------|--------|
| WEBHOOK-06 | Phase 5 | Pending |
| RECON-04 | Phase 6 | Pending |
| RECON-05 | Phase 6 | Pending |
| VALID-03 | Phase 7 | Pending |

**Coverage:**
- v1.1 requirements: 4 total
- Mapped to phases: 4 (Phase 5: Webhook SSRF Private-IP Opt-Out; Phase 6: Reconciler Webhook-Gap Sweep & Staleness Soak Test; Phase 7: Image Dimension Limit)
- Unmapped: 0 ✓

---
*Requirements defined: 2026-07-08*
*Last updated: 2026-07-08 after v1.1 roadmap creation*
