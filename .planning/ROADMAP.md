# Roadmap: OctoConv

## Milestones

- ✅ **v1.0 Hardening MVP** — Phases 1-4 (shipped 2026-07-08) — see `.planning/milestones/v1.0-ROADMAP.md`
- 🚧 **v1.1 Tech Debt Cleanup** — Phases 5-7 (in progress)

## Phases

<details>
<summary>✅ v1.0 Hardening MVP (Phases 1-4) — SHIPPED 2026-07-08</summary>

- [x] Phase 1: Merge, Auth & Rate Limiting (4/4 plans) — completed 2026-07-04
- [x] Phase 2: Webhook Delivery (3/3 plans) — completed 2026-07-04
- [x] Phase 3: Retry-Safety & Reconciler (3/3 plans) — completed 2026-07-06
- [x] Phase 4: Content Validation, Storage Lifecycle & Observability (5/5 plans) — completed 2026-07-07

Full details: `.planning/milestones/v1.0-ROADMAP.md`

</details>

### 🚧 v1.1 Tech Debt Cleanup (In Progress)

**Milestone Goal:** Close the tech debt surfaced by the v1.0 milestone audit — no new capabilities, purely hardening the hardening.

- [x] **Phase 5: Webhook SSRF Private-IP Opt-Out** - Operators on internal private networks can enable webhook delivery to RFC1918 `callback_url` targets via explicit opt-in (loopback/link-local stay blocked) (completed 2026-07-08)
- [ ] **Phase 6: Reconciler Webhook-Gap Sweep & Staleness Soak Test** - Reconciler also recovers done/failed jobs whose webhook silently never fired, and queued/active staleness recovery is proven under real wall-clock conditions
- [ ] **Phase 7: Image Dimension Limit (Decompression-Bomb Protection)** - API rejects uploads whose declared pixel dimensions exceed a configured limit, before conversion or storage

## Phase Details

### Phase 5: Webhook SSRF Private-IP Opt-Out
**Goal**: Operators running on internal private networks can enable webhook delivery to private-IP `callback_url` targets via an explicit config flag, without weakening the default-safe SSRF posture for everyone else.
**Depends on**: Phase 4 (webhook delivery + SSRF `callback_url` validation, v1.0)
**Requirements**: WEBHOOK-06
**Success Criteria** (what must be TRUE):
  1. With `WEBHOOK_ALLOW_PRIVATE_IPS` unset (default), a `callback_url` pointing at an RFC1918/loopback/link-local address is still rejected with 400 — unchanged from v1.0 behavior.
  2. With `WEBHOOK_ALLOW_PRIVATE_IPS=true`, job creation with a `callback_url` pointing at an RFC1918 private address succeeds, and the webhook is actually delivered to that address. Loopback and link-local addresses (including the `169.254.169.254` cloud metadata endpoint) remain rejected even with the flag enabled (D-01) — there is no legitimate `callback_url` use case for them.
  3. Even with the flag enabled, non-https schemes and syntactically invalid URLs are still rejected — the opt-out only relaxes the IP-range check, nothing else in validation.
  4. The new environment variable and its default (disabled) are documented in `.env.example` so operators discover it without reading source.
**Plans**: 1 plan
- [x] 05-01-PLAN.md — WEBHOOK_ALLOW_PRIVATE_IPS opt-out: conditional isBlockedIP RFC1918 relaxation, startup warning, .env.example docs

### Phase 6: Reconciler Webhook-Gap Sweep & Staleness Soak Test
**Goal**: Operators can trust the reconciler to recover both jobs stranded in `queued`/`active` and jobs whose completion webhook silently never fired, and that staleness recovery has been proven under real wall-clock conditions rather than only mocked-clock integration tests.
**Depends on**: Phase 4 (reconciler + webhook delivery, v1.0)
**Requirements**: RECON-04, RECON-05
**Success Criteria** (what must be TRUE):
  1. A job that reaches `done`/`failed` with a non-empty `callback_url` but zero rows in `webhook_deliveries` (e.g. a dropped enqueue during a Redis blip) is detected by the reconciler's sweep and a webhook delivery is triggered for it.
  2. The webhook-gap sweep does not re-trigger delivery for jobs that already have at least one `webhook_deliveries` row — including dead-lettered ones — so no job receives a duplicate delivery attempt from this sweep.
  3. A real wall-clock soak test demonstrates that a job left genuinely stranded in `queued`/`active` past the staleness threshold is requeued/recovered by a live, running reconciler process within the expected sweep interval, using real elapsed time (not a mocked or fast-forwarded clock).
  4. The same soak test demonstrates that a job exceeding `MaxRecoveries` under real elapsed time is terminally failed, with the failure recorded in `job_events`.
**Plans**: 4 plans (3 waves)
- [x] 06-01-PLAN.md — Webhook queue asynq.Unique lock + derived jitter-inflated WebhookUniqueTTL (D-01, D-02)
- [x] 06-02-PLAN.md — FindWebhookGaps NOT EXISTS anti-join + RecordWebhookGapRecovered + supporting index migration (RECON-04, D-04/D-05/D-06)
- [x] 06-03-PLAN.md — Reconciler second sweep loop (enqueue-first gap recovery) + unit tests + metric label (D-03, D-06)
- [ ] 06-04-PLAN.md — Real wall-clock soak test: stranded-job recovery + cap exhaustion (RECON-05, D-07/D-08)

### Phase 7: Image Dimension Limit (Decompression-Bomb Protection)
**Goal**: Operators are protected from decompression-bomb uploads — the API rejects images whose declared pixel dimensions exceed a configured limit before any conversion work begins.
**Depends on**: Phase 4 (magic-byte content validation, v1.0)
**Requirements**: VALID-03
**Success Criteria** (what must be TRUE):
  1. Uploading an image whose declared width × height exceeds the configured limit is rejected with 422 before the file is written to S3 and before a conversion job is enqueued.
  2. Uploading an image within the configured dimension limit succeeds and proceeds through the existing pipeline unaffected.
  3. The dimension limit is configurable via environment variable (not hardcoded), with a documented sane default.
  4. The check parses actual pixel dimensions from file headers across all currently-supported formats (png/jpg/webp/heic/tiff), rather than trusting magic-byte format detection alone.
**Plans**: TBD

## Progress

**Execution Order:**
Phases execute in numeric order: 5 → 6 → 7

| Phase | Milestone | Plans Complete | Status | Completed |
|-------|-----------|-----------------|--------|-----------|
| 1. Merge, Auth & Rate Limiting | v1.0 | 4/4 | Complete | 2026-07-04 |
| 2. Webhook Delivery | v1.0 | 3/3 | Complete | 2026-07-04 |
| 3. Retry-Safety & Reconciler | v1.0 | 3/3 | Complete | 2026-07-06 |
| 4. Content Validation, Storage Lifecycle & Observability | v1.0 | 5/5 | Complete | 2026-07-07 |
| 5. Webhook SSRF Private-IP Opt-Out | v1.1 | 1/1 | Complete   | 2026-07-08 |
| 6. Reconciler Webhook-Gap Sweep & Staleness Soak Test | v1.1 | 3/4 | In Progress|  |
| 7. Image Dimension Limit (Decompression-Bomb Protection) | v1.1 | 0/? | Not started | - |
</content>
