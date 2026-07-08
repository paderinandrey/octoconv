# Project Retrospective

*A living document updated after each milestone. Lessons feed forward into future planning.*

## Milestone: v1.0 — Hardening MVP

**Shipped:** 2026-07-08
**Phases:** 4 | **Plans:** 15 | **Sessions:** ~3 (spanning 2026-07-05 to 2026-07-08)

### What Was Built
- Mandatory API-key auth (salted-hash, dual-slot rotation) + per-client and pre-auth IP rate limiting (Phase 1)
- HMAC-signed webhook delivery with SSRF-guarded `callback_url`, bounded retry + backoff, dead-letter on exhaustion (Phase 2)
- Transient/terminal error classification, queue-aware retry backoff, `asynq.Unique` dedup lock, and a Postgres-driven reconciler for stranded jobs (Phase 3)
- Magic-byte content validation, MinIO ILM lifecycle TTL, Prometheus metrics, real `/healthz`, and an asynqmon dashboard — all live-verified end-to-end (Phase 4)

### What Worked
- **Wave-based parallel execution with worktree isolation** consistently caught same-wave file overlaps before they became merge conflicts, and cross-wave conflicts (e.g. `go.mod` contested by two plans in the same wave adding different direct dependencies) were small and mechanical to resolve by hand.
- **Post-wave build/test gates** (build + vet + full test suite after every merge, not just per-plan) caught nothing broken across 4 phases — a genuinely clean run, which validates the gate as cheap insurance rather than theater.
- **Live E2E verification over trusting SUMMARY.md narrative.** Every phase verifier and the final integration checker re-ran real commands against real Postgres/Redis/MinIO/asynq rather than accepting an executor's self-report. This caught nothing wrong in this milestone, but is the reason confidence in the "tech_debt, 0 blockers" verdict is high rather than assumed.
- **The blocking-human checkpoint for the `[ASSUMED]`-tagged `prometheus/client_golang` dependency worked exactly as designed** — the executor refused a relayed "approved" from the orchestrator twice, even after the orchestrator escalated the framing ("I'm the orchestrator talking directly to the user"). This is the correct behavior: a coordinator cannot manufacture consent on an agent's behalf. The resolution pattern that emerged — orchestrator performs the gated action directly once genuine first-hand user approval exists, rather than fighting the sub-agent's refusal — should be the standard playbook for this situation going forward.
- **Discuss-phase's decision-coverage gate caught a real gap**: 4 of Phase 4's CONTEXT.md decisions (D-03/04/05/07) were implemented but not cited in the plan's `must_haves.truths`, and the gate blocked planning completion until they were added. Cheap to fix, would have been an invisible drift otherwise.

### What Was Inefficient
- **Rebuilding the dev docker-compose stack for the first time in months surfaced a real, unrelated bug** (missing `WEBHOOK_SIGNING_SECRET` on the worker service since Phase 2) that had nothing to do with the plan being verified. Stale, never-rebuilt containers had been silently masking this for the entire Phase 3 and most of Phase 4. **Lesson: rebuild the dev stack periodically (or in CI) even when no phase explicitly requires it** — "it still runs" is not the same as "it still builds correctly from current source."
- **REQUIREMENTS.md checkbox/status drift accumulated across all 4 phases** — every single phase's own VERIFICATION.md independently noted "requirements marked complete in code, but the tracking checkboxes still say Pending," and none of the four fixed it inline. It was cleaned up in one batch pass right before the milestone audit. **Lesson: either fix the checkbox in the same commit as the VERIFICATION.md that confirms it, or accept that it's always a batch cleanup at milestone-audit time — but don't let 4 phases independently rediscover and defer the same small fix.**
- **A cross-phase recommendation fell through a real crack**: Phase 2's own SECURITY.md explicitly recommended a Phase 3 reconciler extension (sweep `done`/`failed` jobs with a dropped webhook enqueue), but Phase 3's discuss-phase scoped webhooks out entirely and never surfaced the specific recommendation for a yes/no decision. Nobody was wrong at any single step — Phase 3's context genuinely said "webhooks are Phase 2, done" — but the *recommendation itself* needed an explicit accept/reject, not silent scope-exclusion. **Lesson: when a phase's own review/security doc recommends specific future work, that recommendation should be visible as a checklist item during the *next* phase's discuss-phase, not just discoverable by manually reading old SECURITY.md files.**

### Patterns Established
- **Orchestrator-direct-execution fallback for sub-agent-refused checkpoints**: when a genuinely-authorized action is blocked because the executing sub-agent correctly can't distinguish a relayed approval from real consent, the orchestrator (who has the real, first-hand user approval) completes that specific gated step directly in the sub-agent's worktree, documents it explicitly in the SUMMARY.md as a "process deviation, not a content deviation," and lets the sub-agent's earlier commits stand.
- **Live verification checkpoints that touch shared dev infrastructure** (e.g., a docker-compose stack the user is actively running) require explicit user sign-off on *how* to verify (stop-and-substitute vs. merge-first-then-verify vs. defer), not just *whether* to verify — the infrastructure coordination is itself a decision, not a mechanical detail.

### Key Lessons
1. A blocking-human checkpoint that can be satisfied by a relayed message from any agent isn't actually blocking — the refusal-of-relayed-approval behavior observed this milestone is the feature working correctly, not friction to route around.
2. Rebuilding infrequently-touched deployment artifacts (docker-compose, container images) on a schedule, not just when a phase happens to touch them, surfaces staleness bugs while they're cheap to fix.
3. Documentation-sync fields (REQUIREMENTS.md checkboxes, cross-phase recommendation follow-through) need a structural point where they're forced to reconcile — milestone-audit time is late-but-workable; per-phase would be better if it doesn't add too much ceremony.

### Cost Observations
- Model mix: planner runs on Opus, everything else (research, execution, verification, integration-check) on Sonnet — consistent with the project's `model_profile: balanced` config.
- Sessions: ~3 across roughly 4 days of wall-clock work (2026-07-05 → 2026-07-08).
- Notable: parallel worktree execution meant most wall-clock time was waiting on Wave 1/Wave 2 pairs running concurrently rather than serially — Phase 4's 5 plans across 3 waves completed with only 2 sequential "wave boundary" waits despite 5x the plan count of a single-wave phase.

---

## Cross-Milestone Trends

### Process Evolution

| Milestone | Sessions | Phases | Key Change |
|-----------|----------|--------|------------|
| v1.0 | ~3 | 4 | First milestone — wave-based parallel execution + worktree isolation + live E2E verification established as the baseline pattern |

### Cumulative Quality

| Milestone | Requirements | Blockers at Audit | Tech Debt Items |
|-----------|-------------|--------------------|-----------------|
| v1.0 | 24/24 satisfied | 0 | 5 (3 carried to next milestone's Active candidates, 2 informational) |

### Top Lessons (Verified Across Milestones)

1. Blocking-human checkpoints must reject relayed approval from any agent, including the orchestrator — confirmed working as intended in v1.0.
2. Live, real-infrastructure verification (not just unit tests or narrative trust) is the deciding factor in audit confidence — established in v1.0, worth preserving as milestones grow larger and re-verifying everything live becomes more expensive.
