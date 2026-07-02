# Roadmap: OctoConv

## Overview

This milestone hardens the existing image-conversion vertical slice into a production-ready internal service. The journey starts by landing the working slice on `main` and closing the biggest security gap (no auth), then layers on the guardrails that depend on client identity (rate limiting), the highest-value reliability win for callers (webhook delivery), the fix that makes retries and job recovery actually safe (retry-safety + reconciler), and finally the remaining hardening items that are independent of the auth/webhook/reconciler critical path (content validation, storage lifecycle, observability). By the end, internal clients can safely and reliably submit conversion jobs with authenticated access, get results pushed via webhook, trust that transient failures self-heal instead of silently failing, and operators can see and act on the system's real health.

## Phases

**Phase Numbering:**

- Integer phases (1, 2, 3): Planned milestone work
- Decimal phases (2.1, 2.2): Urgent insertions (marked with INSERTED)

Decimal phases appear between their surrounding integers in numeric order.

- [ ] **Phase 1: Merge, Auth & Rate Limiting** - The existing slice lands on `main`, and every request is authenticated per client and rate-limited against abuse.
- [ ] **Phase 2: Webhook Delivery** - Clients receive signed job-completion callbacks instead of relying on polling.
- [ ] **Phase 3: Retry-Safety & Reconciler** - The worker correctly retries transient failures, and stranded jobs are automatically and safely recovered.
- [ ] **Phase 4: Content Validation, Storage Lifecycle & Observability** - Uploads are verified by real content, storage doesn't grow unbounded, and operators can see true system health.

## Phase Details

### Phase 1: Merge, Auth & Rate Limiting

**Goal**: The working image-conversion vertical slice is on `main`, and every API request must present a valid, client-scoped API key — unauthenticated, invalid, or excessive traffic is rejected before it can affect production.
**Mode:** mvp
**Depends on**: Nothing (first phase)
**Requirements**: BASE-01, AUTH-01, AUTH-02, AUTH-03, AUTH-04, AUTH-05, RATE-01, RATE-02, RATE-03
**Success Criteria** (what must be TRUE):

  1. `main` runs the full existing image-conversion vertical slice (merged from `feat/scaffold-and-infra`), build and existing tests pass.
  2. A request to a protected endpoint with a missing, invalid, or revoked API key is rejected with 401.
  3. A client cannot see or reference another client's job — cross-client job lookup returns 404, never confirming the job exists.
  4. API keys are stored only as salted SHA-256 hashes (never plaintext), and a client can hold two simultaneously active keys to support rotation without downtime.
  5. A client exceeding their per-client request rate receives 429 with a `Retry-After` header; a coarse pre-auth IP-based limit throttles flood traffic before it reaches auth or the database.

**Plans**: 3 plans

Plans:
**Wave 1**

- [x] 01-01-PLAN.md — Auth issuance foundation: key-hash schema, salted hashing, clients repo, operator CLI

**Wave 2** *(blocked on Wave 1 completion)*

- [ ] 01-02-PLAN.md — Auth enforcement: resolver + middleware, client_id threading, 401/404 client scoping

**Wave 3** *(blocked on Wave 2 completion)*

- [ ] 01-03-PLAN.md — Rate limiting: coarse pre-auth IP guard + per-client 429 + Retry-After

### Phase 2: Webhook Delivery

**Goal**: Clients receive job completion results pushed via signed webhook callbacks, removing the need to poll for status.
**Mode:** mvp
**Depends on**: Phase 1 (requires authenticated `client_id`/`callback_url` attribution)
**Requirements**: WEBHOOK-01, WEBHOOK-02, WEBHOOK-03, WEBHOOK-04, WEBHOOK-05
**Success Criteria** (what must be TRUE):

  1. When a job with a non-empty `callback_url` completes (`done`/`failed`), the service delivers a webhook without the client needing to poll.
  2. The webhook payload is HMAC-SHA256 signed with a timestamp, so receivers can verify authenticity and reject replayed requests.
  3. A failing webhook endpoint receives retried delivery attempts with exponential backoff and jitter, up to a bounded number of attempts.
  4. Every delivery attempt (status, attempt number, HTTP response code) is recorded in `webhook_deliveries`.
  5. After retries are exhausted, a delivery is marked terminal (dead-letter) rather than silently dropped, and remains available for manual investigation.

**Plans**: TBD

Plans:

- [ ] 02-01: TBD

### Phase 3: Retry-Safety & Reconciler

**Goal**: The worker correctly distinguishes transient from terminal failures so asynq retry actually functions, and jobs stranded by infrastructure hiccups (lost enqueue, crashed worker) are automatically recovered without duplicating work.
**Mode:** mvp
**Depends on**: Phase 1 (baseline on `main`). Retry-safety must be implemented before reconciler work within this phase — a reconciler built on the current single-attempt state machine would cause duplicate job processing.
**Requirements**: RELY-01, RELY-02, RECON-01, RECON-02, RECON-03
**Success Criteria** (what must be TRUE):

  1. A transient conversion failure (network/timeout) triggers a real asynq retry rather than being marked terminally failed after one attempt.
  2. A terminal conversion failure (invalid input, unsupported format) marks the job failed immediately without wasted retries.
  3. A job stuck in `queued` with no corresponding task in the asynq queue is automatically re-enqueued exactly once — no duplicate tasks.
  4. A job stuck in `active` past a defined staleness threshold (worker crashed) is recovered without re-processing a job that is merely slow but healthy.
  5. Every reconciler action (job recovered, job terminal-failed) is recorded in `job_events`.

**Plans**: TBD

Plans:

- [ ] 03-01: TBD

### Phase 4: Content Validation, Storage Lifecycle & Observability

**Goal**: Uploaded files are verified by their actual content rather than trusted metadata, storage doesn't grow unbounded, and operators can see the real health and behavior of the system.
**Mode:** mvp
**Depends on**: Phase 1 (baseline on `main`)
**Requirements**: VALID-01, VALID-02, STOR-01, OBS-01, OBS-02, OBS-03
**Success Criteria** (what must be TRUE):

  1. A file whose actual content (magic bytes) doesn't match its declared format/extension is rejected with 422 before it's written to S3.
  2. Uploaded files and results are automatically deleted from S3/MinIO after their configured retention TTL, with no manual cleanup required.
  3. An operator can view Prometheus metrics for queue depth, job outcomes, and webhook delivery success/failure.
  4. The health endpoint reflects real dependency status (Postgres, Redis, S3/MinIO reachability), not a static `{"status":"ok"}`.
  5. An operator can visually inspect the asynq queue via the asynqmon dashboard.

**Plans**: TBD

Plans:

- [ ] 04-01: TBD

## Progress

**Execution Order:**
Phases execute in numeric order: 1 → 2 → 3 → 4

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. Merge, Auth & Rate Limiting | 1/3 | In Progress|  |
| 2. Webhook Delivery | 0/TBD | Not started | - |
| 3. Retry-Safety & Reconciler | 0/TBD | Not started | - |
| 4. Content Validation, Storage Lifecycle & Observability | 0/TBD | Not started | - |
