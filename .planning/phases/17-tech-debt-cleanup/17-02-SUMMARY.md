---
phase: 17-tech-debt-cleanup
plan: 02
subsystem: testing
tags: [e2e, libvips, image, png, jpg, webhook, hmac, docker-compose]

# Dependency graph
requires:
  - phase: 11-e2e-foundation (or equivalent prior E2E work)
    provides: e2eSetup/provisionClient/postJob/pollUntilDone/startWebhookReceiver/assertSignedWebhook/downloadClient helpers in internal/e2e/e2e_test.go
provides:
  - internal/e2e/testdata/sample.png (real PNG fixture, stdlib-generated)
  - TestImageConversionE2E (image/libvips engine E2E coverage: upload -> convert png->jpg -> download -> HMAC-verified webhook)
  - assertDownloadIsImage helper (convert.Sniff-based image download assertion)
affects: [19-ci-pipeline]

# Tech tracking
tech-stack:
  added: []
  patterns: ["convert.Sniff-based download assertion for raster image formats (parallel to assertDownloadIsFormat's convert.SniffContainer for office containers)"]

key-files:
  created:
    - internal/e2e/testdata/sample.png
  modified:
    - internal/e2e/e2e_test.go

key-decisions:
  - "PNG fixture generated via a throwaway Go program using stdlib image/image/png (16x16 opaque RGB, 86 bytes) rather than hand-crafted bytes or a downloaded file — no supply-chain vector, trivially reviewable."
  - "TestImageConversionE2E exercises exactly one pair (png->jpg) with a webhook assertion, mirroring TestDocumentConversionE2E's single-webhook-pair pattern rather than a full format-pair table — the image engine's format matrix is already covered structurally by convert.Sniff unit tests; this plan's job was closing the missing *live* E2E gap, not re-deriving the pair table."
  - "Used -p octoconv (not the worktree-derived default project name) when bringing up docker compose, so the run shares octoconv-db/redis/minio with the main checkout per the plan's explicit note that the compose project is shared."

requirements-completed: [DEBT-08]

# Metrics
duration: 6min
completed: 2026-07-12
---

# Phase 17 Plan 02: Image-Engine E2E Test Summary

**Added TestImageConversionE2E (png->jpg via libvips) to internal/e2e, closing the last gap in the E2E format matrix — live-verified PASS against a real docker-compose stack with HMAC webhook confirmation.**

## Performance

- **Duration:** 6 min (task work) + live compose build/test cycle
- **Started:** 2026-07-12T20:15:00+03:00
- **Completed:** 2026-07-12T20:22:00+03:00
- **Tasks:** 2 completed
- **Files modified:** 2 (1 created, 1 modified)

## Accomplishments
- Committed a tiny, real, stdlib-generated PNG fixture (`internal/e2e/testdata/sample.png`, 86 bytes) that passes `convert.Sniff` and the IHDR dimension check.
- Added `TestImageConversionE2E`: multipart upload of the PNG -> poll to done -> presigned download sniffed via `convert.Sniff` as `jpg` -> HMAC-verified signed webhook, mirroring `TestDocumentConversionE2E`'s shape but for the image (libvips) engine.
- Added `assertDownloadIsImage` helper — the image-format counterpart to `assertDownloadIsFormat` (which uses `convert.SniffContainer` for ZIP/office containers); this one uses `convert.Sniff` (magic-byte detector) for raster images.
- Ran the mandatory LIVE COMPOSE-STACK HARD GATE: full stack up `--build` (project `octoconv`, shared with the main checkout), `/healthz` ready, and `TestImageConversionE2E` reported `--- PASS` (not `--- SKIP`) in 2.14s, proving the full upload -> convert -> download -> HMAC-webhook cycle against the real stack.

## Task Commits

Each task was committed atomically:

1. **Task 1: Generate the committed PNG fixture** - `a464558` (feat)
2. **Task 2: Author TestImageConversionE2E + assertDownloadIsImage** - `ce123a8` (feat)

**Plan metadata:** (this commit, docs)

## Files Created/Modified
- `internal/e2e/testdata/sample.png` - 16x16 opaque RGB PNG, generated via a throwaway `go run` program (stdlib `image`/`image/png`), then the generator was deleted; only the PNG is committed.
- `internal/e2e/e2e_test.go` - Added `TestImageConversionE2E` and `assertDownloadIsImage`.

## Decisions Made
- PNG fixture generated deterministically with stdlib `image/png`, not hand-crafted or downloaded — matches the threat model's T-17-03 mitigation (no supply-chain vector, reviewable as a plain PNG).
- Single png->jpg pair with webhook assertion (not a full pair table) — sufficient to close the identified E2E gap (DEBT-08) without duplicating the pair-matrix coverage already proven by `internal/convert`'s unit tests.
- `docker compose -p octoconv ...` used explicitly to share the existing `octoconv-db`/`octoconv-redis`/`octoconv-minio` containers with the main checkout, per the plan's parallel-execution note that the compose project is shared.

## Deviations from Plan

### Auto-fixed Issues (operational, not code)

**1. [Rule 3 - Blocking] Cleaned up a stray docker-compose project before the shared-project run**
- **Found during:** Task 2 live-gate execution
- **Issue:** An initial `docker compose -f docker-compose.yml -f docker-compose.e2e.yml up -d --build` (without `-p`) used the worktree-directory-derived default project name (`agent-a264218a390d1fd04`), which created `octoconv-minio`/`octoconv-redis` containers (fixed `container_name`s in the compose file) that then conflicted with the shared `octoconv` project's already-running `octoconv-db`. This is a container-naming collision, not a code change — no source files were touched.
- **Fix:** Ran `docker compose -p agent-a264218a390d1fd04 -f docker-compose.yml -f docker-compose.e2e.yml down` to remove the two stray (never-started, `created`-state only) containers, then re-ran the stack with the correct `-p octoconv` project name so it reused `octoconv-db` and correctly created `octoconv-minio`/`octoconv-redis` under the shared project.
- **Files modified:** None (docker/runtime only).
- **Verification:** Live gate then completed successfully with `--- PASS`.

---

**Total deviations:** 1 auto-fixed (operational docker-compose project-name collision, no source changes).
**Impact on plan:** No scope creep. All code changes match the plan exactly.

## Issues Encountered
None beyond the docker-compose project-name collision documented above.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- DEBT-08 fully closed: the image engine now has live E2E coverage, unblocking Phase 19's live-E2E CI tier requirement.
- `go test ./...` remains green offline (TestImageConversionE2E self-skips without `E2E_BASE_URL`).
- Stack left stopped (`docker compose -p octoconv ... stop`, not `down -v`) — volumes/data preserved for the main checkout.

---
*Phase: 17-tech-debt-cleanup*
*Completed: 2026-07-12*

## Self-Check: PASSED

- FOUND: internal/e2e/testdata/sample.png
- FOUND: internal/e2e/e2e_test.go
- FOUND commit: a464558
- FOUND commit: ce123a8
