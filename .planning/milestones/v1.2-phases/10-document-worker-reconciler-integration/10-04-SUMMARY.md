---
phase: 10-document-worker-reconciler-integration
plan: 04
subsystem: infra
tags: [docker, docker-compose, libreoffice, tini, image-split, env-config]

# Dependency graph
requires:
  - phase: 10-document-worker-reconciler-integration
    plan: 03
    provides: "cmd/document-worker binary consuming only the document queue"
provides:
  - "Dockerfile.worker reverted to libvips-only (LibreOffice/tini/fonts genuinely removed from image worker attack surface)"
  - "Dockerfile.document-worker: standalone LibreOffice + tini-as-PID-1 runtime image building cmd/document-worker"
  - "docker-compose document-worker service with own DOCUMENT_WORKER_CONCURRENCY/DOCUMENT_ENGINE_TIMEOUT and identical D-02 resource limits"
  - "DOCUMENT_WORKER_CONCURRENCY / DOCUMENT_ENGINE_TIMEOUT / DOCUMENT_MAX_RETRY documented in .env.example"
affects: [11-document-api-routing-e2e]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Single-purpose runtime Dockerfile shape (Dockerfile.api's slim ca-certificates-only pattern) applied to both Dockerfile.worker (libvips-only) and Dockerfile.document-worker (LibreOffice-only)"

key-files:
  created:
    - Dockerfile.document-worker
  modified:
    - Dockerfile.worker
    - docker-compose.yml
    - .env.example

key-decisions:
  - "Dockerfile.worker's ENTRYPOINT reverted from tini-wrapped to a direct exec (['/usr/local/bin/worker']) since the image engine (libvips) is a single synchronous CLI invocation with no forking-daemon orphan risk, unlike LibreOffice's oosplash->soffice.bin topology — the tini requirement is genuinely engine-specific, not container-generic."
  - "docker-compose document-worker service omits WEBHOOK_SIGNING_SECRET entirely (D-06) — confirmed against cmd/document-worker/main.go, which never signs or delivers webhooks (cmd/worker remains the sole webhook consumer)."
  - "document-worker service given the SAME cpus:2.0/memory:1g resource limits as the image worker (D-02), not a differentiated envelope — accepted as a reasoned default per Phase 10 CONTEXT, not empirically measured."

requirements-completed: [DOC-07]

# Metrics
duration: 12min
completed: 2026-07-09
---

# Phase 10 Plan 04: Docker Image Split & Compose Integration Summary

**Reverted Dockerfile.worker to libvips-only and created Dockerfile.document-worker carrying LibreOffice + tini-as-PID-1, correcting Phase 9's pragmatic same-image compromise; added a document-worker compose service with its own concurrency/timeout and identical D-02 resource limits, and documented all three new env vars in .env.example.**

## Performance

- **Duration:** 12 min
- **Started:** 2026-07-09T19:19:00Z (approx)
- **Completed:** 2026-07-09T19:31:45Z
- **Tasks:** 2 completed
- **Files modified:** 4 (1 created, 3 modified)

## Accomplishments

- `Dockerfile.worker`'s runtime stage no longer installs `tini`, `libreoffice-writer-nogui`, `libreoffice-calc-nogui`, `libreoffice-impress-nogui`, `fonts-crosextra-carlito`, `fonts-crosextra-caladea`, or `fonts-liberation2` — only `ca-certificates` and `libvips-tools` remain. `ENTRYPOINT` reverted from the tini wrapper to a direct `["/usr/local/bin/worker"]` exec, with an inline comment explaining why no init/reaper is needed for a non-forking engine. `USER nobody` and the two-stage `CGO_ENABLED=0` build for `./cmd/worker` are unchanged.
- New `Dockerfile.document-worker`: mirrors `Dockerfile.api`'s two-stage build/runtime shape, builds `-o /out/document-worker ./cmd/document-worker`, and carries the exact LibreOffice + font + tini package set (verbatim, live-verified in Phase 9's 09-RESEARCH.md at `4:7.4.7-1+deb12u13`) that was removed from `Dockerfile.worker`. `ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/document-worker"]`, `USER nobody` preserved, and the tini-as-PID-1 rationale comment carried over verbatim from Phase 9's live-verified zombie-reaping fix.
- `Dockerfile.worker-test` was not touched (confirmed `git diff --exit-code` clean) — it is an independent Go+LibreOffice test-harness image, unrelated to either runtime Dockerfile.
- `docker-compose.yml` gained a `document-worker` service (inserted between `worker` and `asynqmon`): same DB/Redis/S3 environment wiring as `worker`, `DOCUMENT_WORKER_CONCURRENCY: "2"` and `DOCUMENT_ENGINE_TIMEOUT: "300s"` in place of `WORKER_CONCURRENCY`/`ENGINE_TIMEOUT`, no `WEBHOOK_SIGNING_SECRET` (document-worker never delivers webhooks), same `cpus: "2.0"`/`memory: 1g` resource limits as the image worker.
- `.env.example` gained a new `# Document worker` section documenting `DOCUMENT_WORKER_CONCURRENCY=2`, `DOCUMENT_ENGINE_TIMEOUT=300s`, and `DOCUMENT_MAX_RETRY=3` (matching the default already implemented in `internal/queue/client.go`), each with an inline `#` comment in the file's established style.

## Verification Evidence

```
$ grep -v '^#' Dockerfile.worker | grep -c 'libreoffice\|tini\|fonts-'
0
$ grep -v '^#' Dockerfile.document-worker | grep -c 'libreoffice-writer-nogui'
1
$ grep -c 'libvips-tools' Dockerfile.worker
1
$ grep 'ENTRYPOINT' Dockerfile.worker Dockerfile.document-worker
Dockerfile.worker:ENTRYPOINT ["/usr/local/bin/worker"]
Dockerfile.document-worker:ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/document-worker"]
$ git diff --exit-code Dockerfile.worker-test
(clean, no output)
$ docker compose config -q
(exit 0)
$ grep -c 'DOCUMENT_WORKER_CONCURRENCY\|DOCUMENT_ENGINE_TIMEOUT\|DOCUMENT_MAX_RETRY' .env.example
3
$ grep -n 'octoconv-document-worker\|dockerfile: Dockerfile.document-worker' docker-compose.yml
129:      dockerfile: Dockerfile.document-worker
130:    container_name: octoconv-document-worker
$ grep -n 'WEBHOOK_SIGNING_SECRET' docker-compose.yml
118:      WEBHOOK_SIGNING_SECRET: "dev-only-change-me-in-real-deploys"   (only in worker service block; document-worker has none)
$ go build ./...
(clean, exit 0)
```

## Task Commits

Each task was committed atomically:

1. **Task 1: Revert Dockerfile.worker to libvips-only and create Dockerfile.document-worker** - `368f861` (feat)
2. **Task 2: Add the document-worker compose service and document the env vars** - `f4c5f61` (feat)

## Files Created/Modified

- `Dockerfile.worker` - LibreOffice/tini/font packages removed; direct-exec ENTRYPOINT restored; stale runtime-stage comment corrected to reference only libvips.
- `Dockerfile.document-worker` (new) - two-stage build targeting `./cmd/document-worker`, LibreOffice + fonts + tini runtime, `ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/document-worker"]`.
- `docker-compose.yml` - new `document-worker` service block (build, env, resource limits) inserted after `worker` and before `asynqmon`.
- `.env.example` - new `# Document worker` section documenting `DOCUMENT_WORKER_CONCURRENCY`, `DOCUMENT_ENGINE_TIMEOUT`, `DOCUMENT_MAX_RETRY`.

## Decisions Made

- Followed the plan's exact package-set and entrypoint prescriptions verbatim (Phase 9's live-verified LibreOffice provisioning relocated, not re-derived or re-verified).
- Confirmed via `cmd/document-worker/main.go` (already implemented in Plan 03) that `WEBHOOK_SIGNING_SECRET` should be omitted from the document-worker compose service — the binary reads it but never uses it for signing/delivery (D-06), so a dev-placeholder value would be misleading.
- Confirmed via `internal/queue/client.go` that `DOCUMENT_MAX_RETRY`'s implemented default is `3` (not a placeholder `<N>` as `10-PATTERNS.md` had left it) — documented `.env.example` with the actual shipped default so the file stays truthful.

## Deviations from Plan

None - plan executed exactly as written. Both tasks matched their `read_first`/`action`/`verify`/`acceptance_criteria` blocks with no blocking issues, bugs, or missing functionality encountered.

## Threat Flags

None - this plan's threat model (T-10-08, T-10-09, T-10-SC) was already fully addressed by the actions specified in the plan; no new security-relevant surface was introduced beyond what the threat model already covers.

## Known Stubs

None - both Dockerfiles and the compose service are fully wired; no placeholder/mock data paths were introduced.

## Issues Encountered

None.

## User Setup Required

None - Docker was available in the execution environment (`docker compose config -q` succeeded); no external configuration needed. Full container image build + live document conversion smoke test is intentionally deferred to Phase 11's end-to-end verification per the plan's `<verification>` section.

## Next Phase Readiness

- Image worker (`Dockerfile.worker`) is libvips-only again; LibreOffice's larger attack surface lives exclusively in `Dockerfile.document-worker`, isolated per D-01/DOC-07.
- `docker-compose.yml` now defines both `worker` and `document-worker` as independently buildable/runnable services with matching resource envelopes.
- All document-worker env vars are documented in `.env.example`, ready for Phase 11's end-to-end verification against a real docker-compose stack.
- No blockers.

---
*Phase: 10-document-worker-reconciler-integration*
*Completed: 2026-07-09*

## Self-Check: PASSED

- FOUND: Dockerfile.document-worker
- FOUND: Dockerfile.worker (reverted, libvips-only, verified via grep)
- FOUND: docker-compose.yml document-worker service
- FOUND: .env.example Document worker section
- FOUND commit: 368f861 (Task 1)
- FOUND commit: f4c5f61 (Task 2)
