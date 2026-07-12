# Phase 10: Document Worker & Reconciler Integration - Context

**Gathered:** 2026-07-09
**Status:** Ready for planning

<domain>
## Phase Boundary

Document conversions run in their own resource-isolated process, respect their own timeout budget, and are recovered correctly if they get stranded. This phase covers: a dedicated `cmd/document-worker` binary + its own `Dockerfile.document-worker`, correcting the milestone-level Docker split that Phase 9 pragmatically violated; `TypeDocumentConvert`/`QueueDocument` queue plumbing with a genuinely derived `DocumentUniqueTTL`/retry schedule (mirroring `ImageUniqueTTL`'s derivation, not copy-pasted); `DOCUMENT_ENGINE_TIMEOUT` and `DOCUMENT_WORKER_CONCURRENCY` env wiring; making the reconciler engine-aware (it currently hardcodes every recovery onto the image queue — a launch-blocking gap identified in milestone research, since the reconciler is a live, continuously-running production component). It does NOT cover: `handleCreateJob`'s actual routing of accepted documents to the document queue (Phase 11) — this phase builds the queue/worker/reconciler infrastructure for documents, but the API still only calls `EnqueueImageConvert` today (Phase 11 adds the branch).

</domain>

<decisions>
## Implementation Decisions

### Docker image split (correcting Phase 9's pragmatic violation)
- **D-01:** Revert `Dockerfile.worker` back to libvips-only (drop `tini`, `libreoffice-writer/calc/impress-nogui`, and the three font packages Phase 9 added) — it builds `cmd/worker` (the image engine) and should stay lean, per the milestone-level locked decision (a dedicated `cmd/document-worker` + its own Docker image, specifically so LibreOffice's footprint never touches the image worker's container). Create `Dockerfile.document-worker` (LibreOffice + fonts + `tini`, mirroring Phase 9's provisioning work) that builds the new `cmd/document-worker` binary. Phase 9's `Dockerfile.worker-test` (a separate, already-committed test harness) is the natural home for the D-03 process-group-kill live test going forward — it doesn't need to move, since it already exists independently of both runtime Dockerfiles; confirm/adjust as needed during planning.
- **D-02:** New `document-worker` docker-compose service gets the SAME resource limits as the existing `image`-engine `worker` service: `cpus: "2.0"`, `memory: 1g`. Not empirically validated against real document workloads (no representative corpus available) — starting point, adjustable later based on real usage, matching the same "reasoned default, not measured" posture already accepted for `DOCUMENT_ENGINE_TIMEOUT`'s 300s (Phase 9's D-01).

### Concurrency
- **D-03:** `DOCUMENT_WORKER_CONCURRENCY` is a separate env var from `WORKER_CONCURRENCY` (not shared), defaulting lower than the image engine's default of 4 (exact default left to Claude's Discretion — milestone research suggested something like 2, given `soffice.bin`'s heavier per-conversion memory footprint relative to libvips within the same `cpus: "2.0"` / `memory: 1g` ceiling as D-02).

### Reconciler engine-awareness
- **D-04:** The reconciler's `enqueuer` interface gains `EnqueueDocumentConvert(ctx, id) error` alongside the existing `EnqueueImageConvert`. Recovery routing reads the already-existing `jobs.engine` column (confirmed present in the schema since `0001_init.sql`, `CHECK (engine IN ('image', 'document', 'av', 'cad', 'archive', 'probe'))` — no migration needed) to pick which `Enqueue*Convert` method to call for a given stranded job, rather than hardcoding `EnqueueImageConvert` for everything as it does today.

### Process topology (resolved during pattern-mapping)
- **D-05:** The reconciler sweep runs ONLY in `cmd/worker` — not duplicated into `cmd/document-worker`. `cmd/worker`'s `Sweeper` becomes engine-aware (D-04) and recovers stranded jobs of BOTH engine classes by reading `jobs.engine` and calling the matching `Enqueue*Convert` method. `cmd/document-worker` is scoped narrowly to consuming the `document` queue and converting — no sweep loop of its own. Avoids a double-DB-scan / redundant-recovery-attempt race between two independent sweepers (the existing enqueue-first + `asynq.ErrDuplicateTask`-guard pattern would make concurrent sweeps safe but wastefully redundant — cleaner to keep exactly one sweeper).
- **D-06:** `cmd/document-worker` does NOT register a `TypeWebhookDeliver` handler or consume the `webhook` queue — it only PRODUCES webhook-delivery tasks (via the existing shared `queue.Client.EnqueueWebhookDeliver`, called from `HandleDocumentConvert`'s completion path exactly like `HandleImageConvert` already does). `cmd/worker` remains the sole consumer of the `webhook` queue, unchanged from today. asynq tasks are queue-based, not producer-affinitized, so a task enqueued by `cmd/document-worker` is picked up transparently by `cmd/worker`'s already-subscribed handler — no duplicated handler registration needed.
- **Deferred (explicitly out of this phase's scope):** extracting webhook delivery into its own `cmd/webhook-worker` binary/container was raised during discussion and correctly identified as an orthogonal concern (applies equally to image and document jobs, not document-specific) — already noted as a "cheap, non-breaking future migration" in Phase 2's (v1.0) original CONTEXT.md. Doing it now would expand this phase well beyond DOC-07/08/09. Captured as a seed for a future milestone (`.planning/seeds/SEED-002.md`), not implemented here.

### Claude's Discretion
- Exact `DOCUMENT_WORKER_CONCURRENCY` default value (research-suggested ballpark: 2, lower than `WORKER_CONCURRENCY`'s 4) — not pinned by the user, planner/researcher to finalize with rationale.
- Exact derivation of `DocumentUniqueTTL` and the document-queue retry schedule (`DocumentRetryDelay`, `documentBackoffSum`, etc.) — must mirror `ImageUniqueTTL`'s derivation pattern (`internal/queue/queue.go`) using `DOCUMENT_ENGINE_TIMEOUT` (300s, Phase 9 D-01) instead of `ENGINE_TIMEOUT`, and a `DOCUMENT_MAX_RETRY` bounded retry budget (exact number left to research/planner, following `IMAGE_MAX_RETRY`'s precedent).
- Exact home for the D-03 process-group-kill live test relative to the new `Dockerfile.document-worker` (whether `Dockerfile.worker-test` from Phase 9 is reused as-is, renamed, or a new dedicated test harness is created) — technical detail, not user-specified.
- Whether `Dockerfile.document-worker`'s package list is copy-pasted verbatim from Phase 9's now-reverted `Dockerfile.worker` additions, or re-verified fresh against current Debian bookworm — Phase 9's research already live-verified this exact package set (`4:7.4.7-1+deb12u13`, all CVEs fixed), so re-verification is likely unnecessary, but planner's call.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Project & Requirements
- `.planning/PROJECT.md` — Current Milestone v1.2 section
- `.planning/REQUIREMENTS.md` — `DOC-07`, `DOC-08`, `DOC-09` (locked v1.2 scope for this phase)
- `.planning/ROADMAP.md` — Phase 10 goal, success criteria

### Prior Phase Context (what this phase builds on and corrects)
- `.planning/phases/09-libreoffice-converter-engine/09-CONTEXT.md` / `09-RESEARCH.md` / `09-02-SUMMARY.md` — the `LibreOfficeConverter` this phase wires into a real worker; the exact LibreOffice/font package set and the `tini`-as-PID-1 fix (D-03's process-group-kill live proof) that D-01 above relocates from `Dockerfile.worker` to `Dockerfile.document-worker`
- `.planning/research/SUMMARY.md` / `STACK.md` — milestone-level research on `DOCUMENT_WORKER_CONCURRENCY` sizing rationale (heavier per-conversion soffice footprint) and the original "separate binary, separate image" architectural intent this phase now realizes

### Existing Codebase (reference patterns to follow)
- `internal/queue/queue.go` — `ImageUniqueTTL`, `ImageRetryDelay`, `imageBackoffSum` — the exact derivation pattern `DocumentUniqueTTL`/`DocumentRetryDelay` must mirror (not copy-paste; must use `DOCUMENT_ENGINE_TIMEOUT`'s 300s, not `ENGINE_TIMEOUT`'s 120s)
- `internal/queue/client.go` — `Client.imageUniqueTTL` field pattern, `EnqueueImageConvert` — the analog for a new `EnqueueDocumentConvert`
- `internal/reconciler/reconciler.go` — `enqueuer` interface (currently only `EnqueueImageConvert`), `jobStore` interface, the sweep loop that must become engine-aware via the existing `jobs.engine` column
- `internal/worker/worker.go`, `cmd/worker/main.go` — `Handler`/`HandleImageConvert` structural pattern for the new `cmd/document-worker`'s `HandleDocumentConvert`
- `Dockerfile.worker` — current state (has Phase 9's LibreOffice additions, to be reverted per D-01) and `Dockerfile.api` — comparison for shared build-stage conventions
- `Dockerfile.worker-test` (Phase 9) — the existing D-03 live-test harness, to be evaluated for reuse/relocation
- `docker-compose.yml` — existing `worker` service definition (`cpus: "2.0"`, `memory: 1g`, `WORKER_CONCURRENCY`, `ENGINE_TIMEOUT` env) — the exact analog for the new `document-worker` service (D-02)
- `.env.example` — existing `WORKER_CONCURRENCY`/`ENGINE_TIMEOUT`/`IMAGE_MAX_RETRY` entries — the naming convention `DOCUMENT_WORKER_CONCURRENCY`/`DOCUMENT_ENGINE_TIMEOUT`/`DOCUMENT_MAX_RETRY` must follow

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `internal/queue/queue.go`'s engine-class queue routing pattern (`TypeImageConvert`/`QueueImage` constants, `ImageUniqueTTL` derivation) — directly mirrored for `TypeDocumentConvert`/`QueueDocument`/`DocumentUniqueTTL`
- `jobs.engine` column (already exists, already includes `'document'` in its CHECK constraint) — zero schema change needed for reconciler engine-awareness

### Established Patterns
- Second-queue-in-the-same-`asynq.Server`-multi-queue-config precedent was set when the `webhook` queue was added in Phase 2 (v1.0) — but THIS phase deliberately breaks from that precedent by using a SEPARATE binary/process (`cmd/document-worker`) rather than adding a second queue to the existing `cmd/worker`'s `asynq.Server`, per the milestone-level locked decision (resource isolation was the explicit reason for choosing separate binaries over a shared process)
- Genuinely-derived-not-copy-pasted TTL/retry-schedule discipline (established in Phase 6's `WebhookUniqueTTL` correction of a jitter-related bug) — applies directly to `DocumentUniqueTTL`

### Integration Points
- `Dockerfile.worker` (MODIFY — revert to libvips-only) and new `Dockerfile.document-worker`
- `cmd/document-worker/main.go` (new) — mirrors `cmd/worker/main.go`'s wiring but for the document engine only
- `internal/queue/queue.go`, `internal/queue/client.go` (MODIFY — document queue/task/TTL additions)
- `internal/reconciler/reconciler.go` (MODIFY — engine-aware enqueuer interface + routing)
- `docker-compose.yml` (MODIFY — new `document-worker` service)
- `.env.example` (MODIFY — new env vars)

</code_context>

<specifics>
## Specific Ideas

No UI/UX references — backend-only, infrastructure/queue-wiring phase. Concrete asks: correct the Docker image split so LibreOffice never touches the image worker's container, give document conversions their own resource envelope and concurrency knob, and make the reconciler recover document jobs through the right queue instead of silently misrouting them.

</specifics>

<deferred>
## Deferred Ideas

None raised this phase — REQUIREMENTS.md's v2/Out-of-Scope sections already capture relevant future items. `handleCreateJob`'s actual routing to the document queue is explicitly Phase 11, not deferred scope creep — it's the next phase in sequence.

</deferred>

---

*Phase: 10-Document Worker & Reconciler Integration*
*Context gathered: 2026-07-09*
