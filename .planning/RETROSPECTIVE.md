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

## Milestone: v1.2 — Document Engine Class

**Shipped:** 2026-07-10
**Phases:** 4 (8–11) | **Plans:** 12 (incl. gap-closure 11-04) | **Timeline:** ~2 дня (2026-07-09 → 2026-07-10), 71 коммит, +2754 строк Go (без .planning)

*(Note: v1.1 shipped 2026-07-08 without a retrospective section — a short same-day tech-debt milestone; its audit closed 4/4 requirements with zero carry-over.)*

### What Was Built
- Structural office-format safety gate: one-pass ZIP central-directory sniff (OOXML/ODF disambiguation), zip-bomb size guard (`MAX_DOCUMENT_UNCOMPRESSED_BYTES`), macro rejection — all 422 before any S3 write (Phase 8)
- `LibreOfficeConverter` with per-job `-env:UserInstallation` isolation, `%PDF-` output validation, and a live-proven zero-survivors process-group-kill guarantee (Phase 9)
- Dedicated `document` asynq queue + standalone `cmd/document-worker` container (LibreOffice + tini as PID 1), engine-scoped terminal timeout classification, engine-aware reconciler recovery (Phase 10)
- Engine-aware API routing via `Converter.Engine()`/`Registry.EngineFor`, first true live E2E suite (`internal/e2e`, all 6 pairs + HMAC-verified webhook), Content-Type parity gap-closure (Phase 11)

### What Worked
- **The Converter/Registry abstraction survived its second engine untouched** — the v1.2 bet ("extend, don't refactor to a Handler/Capability contract") paid off: LibreOffice slotted in with two new methods (`Engine()`, `EngineFor`) and zero changes to the worker pipeline contract.
- **Live integration testing keeps finding real bugs unit tests can't**: Phase 9's live process-kill proof discovered an actual zombie-reaping gap (fixed with tini as PID 1) that a mocked test would have declared safe.
- **Phase-level verifier caught a real shipped defect the same day**: the gaps_found verdict on Content-Type parity (flagged by code review as WR-01 but not fixed inline) forced gap-closure plan 11-04 before the milestone could close — the verify → gap-plan → re-verify loop worked exactly as designed.
- **Deferred-item bookkeeping paid off**: Phase 10 explicitly deferred its live smoke test to Phase 11's E2E with a written evidence trail, and the milestone audit could confirm closure mechanically instead of re-litigating.

### What Was Inefficient
- **Code review findings that map 1:1 to verification criteria should be fixed before verification runs** — WR-01 was known (review ran first), yet the phase went to the verifier unfixed, costing a full gaps_found → plan → execute → re-verify cycle for a ~20-line change.
- **Untracked `.planning/` bit twice**: a worktree executor couldn't see the untracked 11-04-PLAN.md (worktrees only carry tracked files) and had to reconstruct the plan from VERIFICATION.md; the gap-closure planner accidentally emptied ROADMAP.md and had to restore it from a pre-untrack git baseline. **Lesson: either commit planning artifacts (`git add -f` consistently, as SUMMARYs already are) or pass their content inline to worktree agents — never assume untracked files are visible in a worktree.**
- **The E2E compose override was only validated on Docker Desktop** — WR-02 (missing `extra_hosts` on the `api` service) means the suite's webhook pair will 400 on plain-Linux docker/CI; caught by review, deferred as debt, but it narrows where the milestone's flagship test can actually run.

### Patterns Established
- **Engine-class as a first-class routing dimension**: converter self-describes its engine (`Engine()`), registry resolves it from content-detected formats (`EngineFor(detected, target)`), every dispatch point (API, reconciler) switches on it with a fail-closed default — the template for every future engine class (CAD, AV, chromium-based HTML→PDF).
- **Write-then-run E2E split**: commit the env-gated suite (self-skips offline, keeps `go test ./...` green) in one plan, run it live with a human-verify checkpoint in the next — keeps checkpoints out of implementation plans.
- **E2E-only compose override file** (`docker-compose.e2e.yml`) for security relaxations (SSRF opt-out, host-gateway) so production compose defaults never carry test-only settings.

### Key Lessons
1. Fix review findings that overlap verification criteria *before* spawning the verifier — the review report is a free pre-verification; ignoring it converts a cheap inline fix into a full gap-closure cycle.
2. Worktree-isolated agents see only tracked files; untracked planning artifacts must be passed inline or force-committed before dispatch.
3. Live-run acceptance gates (freshly built stack + real conversions + human approval) remain the highest-confidence close signal — v1.2's audit could cite a PASS matrix instead of narrative claims.

### Cost Observations
- Model mix: planner on Opus, research/execution/verification/integration on Sonnet (balanced profile) — unchanged from v1.0/v1.1.
- Sessions: ~2 (planning+execution in one background session, close in the next).
- Notable: all three phases' worth of infrastructure (queue, worker, engine) integrated with zero post-merge test failures across 4 waves — the wave-gate streak from v1.0 continues.

---

## Milestone: v1.3 — Document Class v2

**Shipped:** 2026-07-12
**Phases:** 5 (12–16) | **Plans:** 17 (incl. gap-closure 16-05) | **Timeline:** ~2 дня (2026-07-10 → 2026-07-12), 147 коммитов, +4773/−145 строк (без .planning)

### What Was Built
- Tech-debt-first opening phase: all 5 inherited advisory items (v1.0 compose audit + v1.2 WR-02/03/04 + gofmt nit) closed with zero new features (Phase 12)
- Cross-format document conversion (docx↔odt, xlsx↔ods, pptx↔odp) via an explicit (source,target) filter table, output validated by the same `SniffContainer` that guards input; OLE-CFB legacy/encrypted inputs rejected with a single fail-closed 422 (Phase 13)
- Validated `opts` mechanism (closed allowlist, injection-proof by unit test) + PDF/A-2b export with worker-side OutputIntent verification, live-confirmed on LibreOffice 7.4 (Phase 14)
- Third engine class: HTML→PDF via chromium-headless-shell with layered network blocking (live canary: zero connections across external/loopback/compose-host/file:// vectors), CSP-injected JS disable, print opts through the same opts pipeline (Phase 15)
- Webhook delivery decoupled from engine workers: dedicated `cmd/webhook-worker` ×2 replicas as the sole webhook-queue consumer, fleet-wide singleton reconciler-sweeper via Postgres advisory lock (~11s failover), conn-lifecycle gaps (CR-01/WR-01) closed in gap-plan 16-05 with a mutex + `-race` regression test (Phase 16)

### What Worked
- **The v1.2 lesson was applied and paid off twice**: Phase 13 and Phase 14 fixed review findings that overlapped verification criteria *before* spawning the verifier — both phases passed verification on the first run, avoiding the gap-closure cycle v1.2 paid for.
- **The engine-class template scaled to its third engine with no contract changes**: Phase 15 reproduced the v1.2 pattern (own queue, own binary/container, terminal-classified timeout, fail-closed routing) mechanically; the riskiest milestone item shipped with a 14/14 security audit.
- **Live testing again invalidated "obvious" assumptions**: two load-bearing RESEARCH.md claims for chromium (JS-disable launch flag, print_background CSS hint) were proven broken against the real binary and corrected in place — a mocked test would have shipped both bugs.
- **Plan-checker caught a real concurrency bug in a plan, not just style**: the 16-05 gap-closure plan originally forbade a mutex while introducing a cross-goroutine Close(); the checker's warning forced a revision (mutex + race test) that matched exactly what the parallel quick-fix independently implemented.

### What Was Inefficient
- **Parallel sessions duplicated gap-closure work**: a quick-task session fixed CR-01/WR-01 while this session was planning the same fix as 16-05. The executor reconciled cleanly (verify-not-reimplement + add the one missing race test), but coordination cost a planner+checker cycle for work that was ~90% done. Lesson: check `git log` for concurrent landings before planning gap closures.
- **Stale SDK caches misreported audit state** (`audit-open` kept showing a completed quick task as missing after the metadata fix; `roadmap.update-plan-progress` hung once) — cross-checking against the underlying files/library directly resolved both, but cost debugging time.
- **The `.planning/`-is-gitignored friction from v1.2 persists**: every tracking commit needs `git add -f`; one commit failed mid-flow because the sdk refused ignored paths.

### Patterns Established
- **Gap-closure plans get the same rigor as feature plans**: planner → checker → revision → re-check, then executor verifies pre-existing work criterion-by-criterion instead of re-implementing ("pre-satisfied by X" evidence trail in SUMMARY).
- **Advisory-lock singleton as the fleet-coordination primitive**: `pg_try_advisory_lock` on a dedicated pooled conn with mutex-guarded lifecycle — reusable for any future exactly-one-per-fleet role.
- **Terminal-error signatures coupled same-commit with validators** (D-04 discipline from Phase 13): any new validator error string ships together with its `terminalLibreOfficeSignatures` entry, preventing retry storms on permanent failures.

### Key Lessons
1. A review-fixes-before-verifier discipline converts gap-closure cycles into first-pass verifications — now proven across two consecutive phases.
2. Concurrent sessions on one repo need a git-log check before planning: the cheapest reconciliation is discovering the work is already merged.
3. When SDK/tooling output contradicts the filesystem, trust the filesystem and verify with the underlying library directly.

### Cost Observations
- Model mix: planner on Opus, research/execution/verification/integration on Sonnet — unchanged (balanced profile).
- Sessions: ~4 (planning+execution spread over background sessions; phases 14-15 ran in parallel sessions; close in this session).
- Notable: 5 phases with zero post-merge test failures across all waves; the wave-gate streak now spans four milestones.

---

## Milestone: v1.4 — CI, Presets & Debt Cleanup

**Shipped:** 2026-07-13
**Phases:** 3 (17–19) | **Plans:** 8 | **Timeline:** ~2 дня (2026-07-12 → 2026-07-13), 54 коммита, +2261/−60 строк (без .planning)

### What Was Built
- Debt-first opening: dead webhook wiring removed, fakeEnqueuer race-safe, image E2E test — plus a bonus terminal-Close fix (DEFER-17-01) discovered BY a phase hard gate hanging on a Phase 16 test (Phase 17)
- Named presets end-to-end: internal/presets (SQL-level shadowing/no-leak), manage-presets CLI, preset= in POST /v1/jobs with XOR/re-validation/TOCTOU re-check, provenance into dormant DDL columns — zero migrations, zero new deps (Phase 18)
- 4-tier GitHub Actions CI live on a public repo: gate → -race → 6-target bake with per-scope gha cache → full compose E2E; first-ever run failed exactly one tier and thereby proved the failure-path machinery (logs artifact, if:always teardown) while exposing a real E2E-env config bug (429 rate-limit cascade) (Phase 19)

### What Worked
- **Live hard gates as plan requirements (v1.3 lesson institutionalized)**: every phase's checker demanded unconditional live proofs; Phase 17's gate hang surfaced a real latent bug (lazy re-acquire after Close) that three prior green runs had missed by timing luck.
- **The pipeline's first failure was its first success**: run 1's e2e failure exercised artifact upload + teardown exactly as designed and handed over a 5.6KB compose-log that pinpointed the 429 cascade in minutes (fetched via nightly.link despite no gh auth).
- **Empirical validation of plan self-checks**: the checker executed the plans' own verify commands against mocks and caught two would-never-pass gates (broken yq pipeline, wrong grep count); the orchestrator then caught a third (PyYAML absent) the same way. Plans whose gates are themselves tested don't wedge executors.
- **Anonymous-evidence improvisation**: public-repo API polling → rate-limited → un-quota'd badge endpoint → nightly.link artifacts. The observation plan degraded gracefully three times without losing evidentiary rigor.

### What Was Inefficient
- **Docker daemon (OrbStack) wedged during the first 18-04 attempt** — 10-minute executor stall, then a misdiagnosed restart (Docker Desktop isn't installed; OrbStack is). Lesson recorded: identify the actual container runtime before restarting it; give executors a kill-after-120s rule for docker commands.
- **GitHub anonymous API quota (60/hr) burned by 30s polling** — switch to badge/nightly.link earlier, or get gh auth up front for CI-heavy phases.
- **Production rate limits as E2E blocker was foreseeable**: PITFALLS.md flagged healthcheck timing but nobody modeled the suite's polling RPM against RATE_LIMIT_IP_RPM=60 on a faster runner. Local greenness is not CI greenness.

### Patterns Established
- **checkpoint:human-verify with graceful downgrade**: plan encodes gh-authenticated watch → anonymous API → badge → human, in that order; the operator only sees the checkpoint when automation is truly exhausted.
- **Test-only compose override as the single home for E2E relaxations** (SSRF opt-outs since v1.2, now rate limits) — production compose is never touched by test needs.
- **Orchestrator-inline execution for push-main plans**: worktree isolation is structurally wrong for a plan whose action IS pushing main; executed inline with the same gates and SUMMARY discipline.

### Key Lessons
1. Hard gates don't just verify — they discover: two of this milestone's three real bugs (terminal-Close, 429 cascade) were found by gates, not by planning or review.
2. Plan self-verification commands must themselves be executed before execution (against mocks) — three broken gates in one phase proved this is cheap and always worth it.
3. CI environments differ from dev in speed, not just OS: rate limits, quotas and timing assumptions all need a "×10 faster machine" sanity pass.

### Cost Observations
- Model mix: planner on Opus, checker/executor/verifier/integration on Sonnet — unchanged.
- Sessions: ~1 background session end-to-end (plus the operator's checkpoint replies).
- Notable: zero post-merge test failures across all waves — five consecutive milestones now; and the milestone closed within ~26 часов календарного времени.

---

## Cross-Milestone Trends

### Process Evolution

| Milestone | Sessions | Phases | Key Change |
|-----------|----------|--------|------------|
| v1.0 | ~3 | 4 | First milestone — wave-based parallel execution + worktree isolation + live E2E verification established as the baseline pattern |
| v1.1 | ~1 | 3 | Same-day tech-debt close; first zero-carry-over audit (no retro section written) |
| v1.2 | ~2 | 4 | First multi-engine milestone; verify → gap-plan → re-verify loop exercised for real (Content-Type parity); first committed live E2E suite |
| v1.3 | ~4 | 5 | Third engine class; review-fixes-before-verifier discipline adopted; first parallel-session reconciliation (quick-task vs gap-plan) |
| v1.4 | ~1 | 3 | First live CI on GitHub; unconditional live hard gates institutionalized; plan self-checks empirically validated pre-execution |

### Cumulative Quality

| Milestone | Requirements | Blockers at Audit | Tech Debt Items |
|-----------|-------------|--------------------|-----------------|
| v1.0 | 24/24 satisfied | 0 | 5 (3 carried to next milestone's Active candidates, 2 informational) |
| v1.1 | 4/4 satisfied | 0 | 0 — first zero-carry-over close |
| v1.2 | 10/10 satisfied | 0 | 4 advisory (WR-02/03/04 + gofmt nit), all documented in 11-REVIEW.md and STATE.md |
| v1.3 | 14/14 satisfied | 0 | 3 advisory (dead webhook wiring in document/chromium workers, fakeEnqueuer -race, no image E2E), documented in v1.3-MILESTONE-AUDIT.md |
| v1.4 | 11/11 satisfied | 0 | 4 advisory (CACHED-residual, branch-protection manual step, D-04 invariant, manual acceptance script), documented in v1.4-MILESTONE-AUDIT.md |

### Top Lessons (Verified Across Milestones)

1. Blocking-human checkpoints must reject relayed approval from any agent, including the orchestrator — confirmed working as intended in v1.0.
2. Live, real-infrastructure verification (not just unit tests or narrative trust) is the deciding factor in audit confidence — established in v1.0, worth preserving as milestones grow larger and re-verifying everything live becomes more expensive.
3. Fix code-review findings that overlap verification criteria before spawning the verifier — learned in v1.2 (paid a gap cycle), applied in v1.3 (two first-pass verifications).
4. Live hard gates discover bugs planning cannot — confirmed across v1.3 (chromium research corrections) and v1.4 (terminal-Close hang, CI 429 cascade); make them unconditional in every plan that touches infrastructure.
