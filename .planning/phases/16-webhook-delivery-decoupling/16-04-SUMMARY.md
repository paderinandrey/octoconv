---
phase: 16-webhook-delivery-decoupling
plan: 04
subsystem: infra
tags: [e2e, live-acceptance, webhook, advisory-lock, failover, asynq, docker-compose]

# Dependency graph
requires:
  - phase: 16-webhook-delivery-decoupling
    plan: "03"
    provides: "docker-compose topology with two symmetric webhook-worker services; image/document/chromium workers stripped of webhook env"
provides:
  - "Live-verified SC1: webhook delivery is independent of the image worker (document job's webhook delivered by a webhook-worker while octoconv-worker was stopped)"
  - "Live-verified SC2: killing one webhook-worker mid-delivery loses zero and duplicates zero delivered webhooks (asynq at-least-once + webhook_deliveries idempotency, no new tracking)"
  - "Live-verified SC3: exactly one fleet-wide advisory-lock sweeper (pg_locks count == 1) with auto-failover to a different backend pid within ~11s of killing the holder"
  - "Human sign-off on all three success criteria (checkpoint approved)"
affects: []

# Tech tracking
tech-stack:
  added: []
  patterns: []

key-files:
  created: []
  modified: []

key-decisions:
  - "SC1 delivery-by-webhook-worker evidence uses per-worker Prometheus metrics (octoconv_webhook_deliveries_total) instead of container logs, because HandleWebhookDeliver deliberately never logs (project convention: library code returns errors, only cmd/*/main.go logs)"
  - "SC2's mid-delivery kill was made reliably interceptable by pointing callback_url at a receiver that delays its HTTP response 5s; the resulting raw-endpoint double-POST is the documented at-least-once semantics of D-06, not a defect — exactly one delivered=true row existed at the system of record"
  - "Stack torn down (compose down, volumes retained per orchestrator instruction) after human approval; copied .env removed from worktree root, never committed"

patterns-established: []

requirements-completed: [WEBH-01]

# Metrics
duration: ~25min (stack-up to SC3 failover verified; checkpoint approval async)
completed: 2026-07-12
---

# Phase 16 Plan 04: Live E2E Acceptance of Decoupled Webhook Topology Summary

**All three Phase 16 success criteria live-verified against a freshly built two-webhook-worker stack and human-approved: webhook delivery survives image-worker absence (SC1), survives a mid-delivery consumer kill with zero lost/zero duplicated delivered webhooks (SC2), and exactly one fleet-wide advisory-lock sweeper with ~11s auto-failover (SC3). WEBH-01 satisfied.**

## Performance

- **Duration:** ~25 min of live drills (2026-07-12T05:13Z stack up → 05:32Z SC3 failover verified), plus async human checkpoint approval
- **Tasks:** 3 (2 auto acceptance drills + 1 blocking human-verify checkpoint, approved)
- **Files modified:** 0 (live acceptance drill — `files_modified: []` by design)

## Accomplishments

- Built and ran the full stack fresh (`docker compose -f docker-compose.yml -f docker-compose.e2e.yml up -d --build`): all 10 services (api, worker, document-worker, chromium-worker, webhook-worker-1/-2, postgres, redis, minio, asynqmon) reached running/healthy; `/healthz` reported `{"postgres":"ok","redis":"ok","s3":"ok","status":"ok"}`.
- Baseline: full E2E suite green on the new topology — `go test ./internal/e2e/ -count=1 -v` passed all tests in 46.98s, including the webhook-asserting `TestDocumentConversionE2E/sample.docx` subtest (signed webhook now delivered by a webhook-worker, no engine worker holds the webhook queue).
- SC1, SC2, SC3 all demonstrated live with captured evidence (below) and human-approved at the blocking checkpoint.

## Captured Live Evidence

### SC1 — webhook delivery independent of the image worker (D-03)

With `octoconv-worker` (image engine) stopped via `docker stop`:

- Submitted `sample.docx → pdf` (`job_id=3108f7eb-e226-4968-833f-be5acdca0580`) with `callback_url=http://host.docker.internal:8765/webhook`; job reached `status=done` while the image worker remained stopped (document-worker converted it).
- **(a) Receiver hit:** the host-bound receiver logged exactly one signed POST — headers `X-Octoconv-Signature: 37afb27c...aa22`, `X-Octoconv-Timestamp: 1783833405`, body carrying the correct `job_id`, `status: done`, and a fresh presigned `download_url`.
- **(b) Delivered-by-webhook-worker proof (deviation — metrics instead of logs):** the plan asked for "webhook-worker log shows the delivery", but `HandleWebhookDeliver` deliberately has zero log lines (project convention: `internal/*` never logs). Equivalent-strength evidence captured instead from each worker's own localhost `/metrics` endpoint: `octoconv_webhook_deliveries_total{result="success"}` = 1 on **each** of webhook-worker-1 and webhook-worker-2 (2 fleet-wide = 1 from the E2E baseline's webhook subtest + 1 from this SC1 delivery) — a webhook-worker, not the image worker, processed the delivery.
- **(c) System-of-record row:**

  ```
  SELECT job_id, delivered, attempt, status_code, dead_letter FROM webhook_deliveries
   WHERE job_id='3108f7eb-e226-4968-833f-be5acdca0580';
                  job_id                | delivered | attempt | status_code | dead_letter
   3108f7eb-e226-4968-833f-be5acdca0580 | t         |       1 |         200 | f
  ```

- Image worker restarted (`docker start octoconv-worker`) and confirmed running before Task 2.

### SC2 — kill one webhook-worker mid-delivery: zero loss, zero duplicated delivered webhooks (D-05/D-06)

- Setup: submitted a fast image job (`png → jpg`, `job_id=0931d45b-283c-4c0c-a0e0-a7493614ff0c`) with `callback_url` pointing at a receiver that delays its HTTP response by 5s — deliberately widening the in-flight window so the kill is reliably interceptable (the production receiver answers in ms).
- An automated watcher polled both webhook-worker containers' `/proc/net/tcp` for the established connection to the receiver port and killed the busy one: **`docker kill octoconv-webhook-worker-2` at 2026-07-12T05:25:54Z, mid-HTTP-call** (ExitCode 137 confirmed).
- Result: the surviving `octoconv-webhook-worker-1` completed delivery ~2 min later (consistent with asynq's 30s lease expiry + up-to-1-min recoverer poll):

  ```
  SELECT job_id, attempt, delivered, status_code, dead_letter, created_at FROM webhook_deliveries
   WHERE job_id='0931d45b-283c-4c0c-a0e0-a7493614ff0c' ORDER BY created_at;
                  job_id                | attempt | delivered | status_code | dead_letter |          created_at
   0931d45b-283c-4c0c-a0e0-a7493614ff0c |       2 | t         |         200 | f           | 2026-07-12 05:27:53.951634+00
  ```

  Exactly **one** row, `delivered=true` — no loss, no duplicated delivered record; the queue drained onto the survivor with no new tracking code (D-06: asynq at-least-once + `webhook_deliveries`/`asynq.Unique` idempotency reused verbatim).
- **At-least-once nuance (flagged at checkpoint, explicitly accepted by the human as matching D-06):** the receiver's raw HTTP log showed 2 physical POSTs. The killed worker's request had already been fully sent and read by the receiver before SIGKILL; Linux gracefully closed (FIN) the orphaned socket on process death, so the receiver's artificially-delayed 200 response was accepted at the TCP layer by a socket whose owning process was dead — no application code processed it and no `webhook_deliveries` row was written for that attempt. This is inherent at-least-once webhook semantics (clients must be idempotent by `job_id`, same class as Stripe/GitHub webhooks), surfaced only because the response was artificially delayed; it is D-06's documented guarantee, not a new exactly-once claim and not a defect.
- `octoconv-webhook-worker-2` restarted before SC3.

### SC3 — exactly one fleet-wide sweeper + auto-failover (D-01/D-02)

- Steady state, both workers live:

  ```
  SELECT count(*) FROM pg_locks WHERE locktype='advisory';  -- 1
  SELECT pid, granted FROM pg_locks WHERE locktype='advisory';
   pid | granted
   101 | t
  ```

  Holder correlated via `pg_stat_activity.client_addr = 192.168.147.6` → **octoconv-webhook-worker-1** (the reconciler is the only advisory-lock user in the codebase, so this count is the direct single-sweeper assertion).
- **Failover drill:** `docker kill octoconv-webhook-worker-1` at 2026-07-12T05:31:02Z. Advisory-lock count returned to exactly **1 at 05:31:13Z (~11s later — well within one 1m sweep interval)**, now held by a **different backend pid**:

  ```
  SELECT pid, granted FROM pg_locks WHERE locktype='advisory';
   pid  | granted
   1813 | t
  SELECT pid, client_addr, backend_start FROM pg_stat_activity WHERE pid=1813;
   1813 | 192.168.147.7 | 2026-07-12 05:30:03.409758+00
  ```

  `192.168.147.7` = **octoconv-webhook-worker-2**, and that backend's `backend_start` (05:30:03Z) predates the kill — it is the survivor's pre-existing dedicated advisory-lock session winning the next `TryAcquire` tick, i.e. genuine auto-failover via Postgres session-close lock release, not a reconnect of the killed container.
- Optional corroboration (induced webhook-gap → single `webhook_gap_recovered` event) was skipped — explicitly optional in the plan; `job_events` had 0 reconciler-action rows this run, consistent with no stale jobs and no gaps occurring.
- `octoconv-webhook-worker-1` restarted; final state 2/2 webhook-workers running, advisory count still exactly 1.

### Secret spot-check (T-16-13)

All captured evidence files and logs grepped for the dev `WEBHOOK_SIGNING_SECRET` value — zero matches. No signing secret appears in any captured output.

## Task Commits

No code/task commits — both auto tasks are live acceptance drills with `files_modified: []`; the working tree stayed clean throughout (verified via `git status --short`). The only commit for this plan is the SUMMARY.md metadata commit.

1. **Task 1: Build stack + SC1** — no commit (no repo changes)
2. **Task 2: SC2 + SC3** — no commit (no repo changes)
3. **Task 3: Human sign-off** — checkpoint returned, human replied "approved" (all three criteria accepted, including the SC2 at-least-once nuance)

## Files Created/Modified

None (evidence captured in this SUMMARY; scratch receivers/watchers lived outside the repo).

## Decisions Made

- SC1 evidence point (b) satisfied via per-worker Prometheus `octoconv_webhook_deliveries_total` metrics instead of container logs, because the delivery handler never logs by project convention — documented as a deviation, equivalent evidentiary strength.
- SC2's in-flight window was widened with a 5s-delayed receiver response to make the mid-delivery kill deterministic; the resulting raw double-POST was analyzed to root cause (orphaned-socket FIN after SIGKILL) and explicitly accepted by the human as D-06's at-least-once semantics.
- Stack torn down after approval with `docker compose down` (volumes retained per orchestrator instruction); the copied `.env` removed from the worktree root (gitignored, never committed).

## Deviations from Plan

**1. [Evidence-method deviation] SC1 "webhook-worker log shows the delivery" replaced with per-worker metrics**
- **Found during:** Task 1
- **Issue:** `HandleWebhookDeliver` has zero log lines (project convention: `internal/*` never logs), so no per-delivery container-log line can exist.
- **Fix:** Queried each webhook-worker's localhost-only `/metrics` (`octoconv_webhook_deliveries_total{result="success"}` incremented on each worker) as the delivered-by-webhook-worker proof.
- **Files modified:** none
- **Commit:** n/a

No other deviations — no auto-fixes, no architectural questions, no auth gates.

## Issues Encountered

- The stock Python `http.server.HTTPServer` receiver crashed on this host (`socket.getfqdn` idna failure on a Cyrillic reverse-DNS name); worked around in the scratch receiver by overriding `server_bind` — outside the repo, no code impact.
- SC2 raw-endpoint double-POST (detailed above) — analyzed, root-caused, and human-accepted as documented at-least-once behavior.

## User Setup Required

None.

## Next Phase Readiness

- Phase 16 acceptance bar met: all three ROADMAP success criteria live-verified and human-approved; WEBH-01 satisfied.
- The question "which binary delivers webhooks?" now has a single settled answer: `cmd/webhook-worker`, and only it — with real ≥2-consumer redundancy and exactly-one-sweeper auto-failover proven on live infrastructure.
- No blockers. Phase 16 (and milestone v1.3's final phase) ready for phase close-out by the orchestrator.

---
*Phase: 16-webhook-delivery-decoupling*
*Completed: 2026-07-12*

## Self-Check: PASSED

- FOUND: .planning/phases/16-webhook-delivery-decoupling/16-04-SUMMARY.md
- No task commits to verify (both auto tasks declared `files_modified: []`; working tree clean throughout)
- Evidence artifacts embedded above (receiver hits, webhook_deliveries rows, pg_locks counts/pids) match the raw captures verbatim
