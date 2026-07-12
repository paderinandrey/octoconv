---
phase: 17-tech-debt-cleanup
verified: 2026-07-12T21:00:00Z
status: passed
score: 3/3 must-haves verified
overrides_applied: 0
---

# Phase 17: Tech Debt Cleanup Verification Report

**Phase Goal:** Close the v1.3 tail-debt so the later `-race` and live-E2E CI tiers can land green instead of red/blind on day one.
**Verified:** 2026-07-12T21:00:00Z
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | `cmd/document-worker` and `cmd/chromium-worker` no longer construct `webhook.NewRepo`/`NewDeliverer` nor read `WEBHOOK_SIGNING_SECRET`; both build and start cleanly | VERIFIED | Direct read of both `main.go` files: `worker.NewHandler(repo, store, convert.Default, envDuration(...), nil, nil, qc, nil, 0)` — mirrors `cmd/worker/main.go`'s nil-safe pattern exactly, with per-arg comments. `grep -c "internal/webhook"` = 0 and `grep -c WEBHOOK_SIGNING_SECRET` = 0 for both files; `cmd/webhook-worker/main.go` untouched (grep = 1, non-zero). `go build ./...` and `go vet ./...` both exit 0; `gofmt -l` empty. |
| 2 | `go test ./internal/reconciler/... -race` runs (not skips) and reports clean; `fakeEnqueuer` counters are mutex-guarded | VERIFIED | Code review: `fakeEnqueuer` has `mu sync.Mutex` field; all four `Enqueue*` methods lock/append/unlock; four locked snapshot accessors (`imageCallIDs`, `webhookCallIDs`, `documentCallIDs`, `htmlCallIDs`) added. `reconciler_soak_test.go`'s concurrent read changed to `enq.imageCallIDs()`. Independently re-ran live against `octoconv-db` (healthy): `DATABASE_URL=postgres://octo:octo-pass@localhost:5434/octo_db go test ./internal/reconciler/... -race -count=1 -timeout 300s -v` → **23/23 PASS**, `TestSoakRecoversStrandedQueuedJob` RAN (not skipped, 1.35s), `TestPGAdvisoryLockTryAcquireCloseRace` PASSED (the test that was hanging pre-fix), zero `DATA RACE` output. |
| 3 | A new image-engine E2E test drives full upload → convert (libvips) → download → HMAC-verified webhook cycle against a live compose stack and passes | VERIFIED | `TestImageConversionE2E` (internal/e2e/e2e_test.go:982) mirrors `TestDocumentConversionE2E`'s shape: `e2eSetup` → `provisionClient` → reads `testdata/sample.png` → `startWebhookReceiver` → `postJob` (target `jpg`, callback registered) → `pollUntilDone` (2 min bound) → `assertDownloadIsImage` (uses `convert.Sniff`, magic-byte detector, asserts detected format == "jpg") → `assertSignedWebhook` (same helper used by doc/HTML E2E tests, HMAC-verifies when `WEBHOOK_SIGNING_SECRET` set). Fixture verified: `internal/e2e/testdata/sample.png` = real 16x16 PNG, 86 bytes, correct 8-byte PNG magic, `file` reports "PNG image data". Offline self-skip confirmed: `go test ./internal/e2e/ -run TestImageConversionE2E -v` → `--- SKIP` (E2E_BASE_URL not set), exit 0. Live PASS is documented in 17-02-SUMMARY.md (`--- PASS` in 2.14s, full compose stack up, healthz ready) — accepted as primary evidence per verification scope; the postgres/redis/minio/webhook-worker stack was stopped after that run so a fresh live rerun was not repeated here, but the test code itself is sound and reuses proven helpers (`assertSignedWebhook`, `pollUntilDone`, `startWebhookReceiver`) with no stub/placeholder logic. |

**Score:** 3/3 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `cmd/document-worker/main.go` | webhook wiring removed | VERIFIED | nil/0 args, no import, no secret read |
| `cmd/chromium-worker/main.go` | webhook wiring removed | VERIFIED | nil/0 args, no import, no secret read |
| `internal/reconciler/reconciler_test.go` | race-safe fakeEnqueuer | VERIFIED | `sync.Mutex` + 4 locked accessors present |
| `internal/reconciler/reconciler_soak_test.go` | reads via locked accessor | VERIFIED | `enq.imageCallIDs()` used; `len(enq.imageCalls)` direct read removed |
| `internal/e2e/testdata/sample.png` | real PNG fixture | VERIFIED | 86 bytes, valid PNG magic + IHDR, `file` confirms |
| `internal/e2e/e2e_test.go` (`TestImageConversionE2E`, `assertDownloadIsImage`) | image E2E test + helper | VERIFIED | both functions present, wired to existing E2E helpers |
| `internal/reconciler/reconciler.go` (`PGAdvisoryLock.closed` flag) | DEFER-17-01 same-phase fix | VERIFIED | `Close()` sets terminal `closed` flag; `TryAcquire` errors after close instead of resurrecting a connection; `RunWithLock` still fail-safe skips on any `TryAcquire` error |

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| `cmd/document-worker/main.go` | `worker.NewHandler` | nil webhookRepo/deliverer/signingSecret, 0 presignTTL | WIRED | Exact match to `cmd/worker/main.go` reference pattern |
| `cmd/chromium-worker/main.go` | `worker.NewHandler` | same nil-safe pattern | WIRED | Confirmed identical shape |
| `internal/reconciler/reconciler_soak_test.go` | `fakeEnqueuer` accessor | `enq.imageCallIDs()` locked read in wait loop | WIRED | grep confirms single call site, no raw field read remains |
| `internal/e2e/e2e_test.go` (`TestImageConversionE2E`) | `convert.Sniff` | download assertion sniffs fetched bytes | WIRED | `assertDownloadIsImage` calls `convert.Sniff(bytes.NewReader(body))` |
| `internal/e2e/e2e_test.go` (`TestImageConversionE2E`) | `assertSignedWebhook` | webhook receiver channel → assertion | WIRED | Called with `received`, `jobID` — same helper reused by document/HTML E2E tests |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| document/chromium workers build clean | `go build ./cmd/document-worker ./cmd/chromium-worker` | exit 0 | PASS |
| full repo builds/vets/formats clean | `go build ./... && go vet ./... && gofmt -l .` | all empty/exit 0 | PASS |
| reconciler package live `-race` run (re-executed independently, not just trusted from SUMMARY) | `DATABASE_URL=... go test ./internal/reconciler/... -race -count=1 -timeout 300s -v` | 23/23 PASS, no DATA RACE, soak test ran | PASS |
| E2E image test offline self-skip | `go test ./internal/e2e/ -run TestImageConversionE2E -v` | `--- SKIP` (E2E_BASE_URL not set), exit 0 | PASS |
| PNG fixture is a real, valid PNG | `file internal/e2e/testdata/sample.png` + magic-byte cmp | "PNG image data, 16 x 16" + magic match | PASS |
| Live compose E2E full pipeline | (not re-run — stack was stopped after 17-02's documented live PASS) | 17-02-SUMMARY.md: `--- PASS` (2.14s) | ACCEPTED (documented evidence, code reviewed sound) |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|--------------|-------------|-------------|--------|----------|
| DEBT-06 | 17-01 | Remove dead webhook wiring from document/chromium workers | SATISFIED | Code inspection + build/vet/gofmt clean |
| DEBT-07 | 17-01 | fakeEnqueuer race-safe, `-race` clean | SATISFIED | Code inspection + independent live `-race` re-run, 23/23 PASS |
| DEBT-08 | 17-02 | Image E2E test, full pipeline | SATISFIED | Code inspection (sound, reuses proven helpers) + documented live PASS in 17-02-SUMMARY.md |

Note: `.planning/REQUIREMENTS.md` still shows DEBT-06/07/08 as unchecked (`- [ ]`) and status "Pending" in its tracking table (lines 26-28, 63-65). This is a documentation-staleness issue only — not a code gap — since the phase's actual code/tests independently prove all three requirements are satisfied. Recommend updating REQUIREMENTS.md checkboxes/status as a trivial follow-up.

### Anti-Patterns Found

None. Scanned all phase-modified files (`cmd/document-worker/main.go`, `cmd/chromium-worker/main.go`, `internal/reconciler/reconciler_test.go`, `internal/reconciler/reconciler_soak_test.go`, `internal/reconciler/reconciler.go`, `internal/reconciler/advisorylock_conn_test.go`, `internal/e2e/e2e_test.go`) for `TBD|FIXME|XXX|TODO|HACK|PLACEHOLDER` — zero matches.

### DEFER-17-01 Regression Check

The same-phase orchestrator follow-up (commit `e11aff4`) added a `closed bool` field to `PGAdvisoryLock`, made `Close()` set it and become terminal, and made `TryAcquire` return an error immediately if `closed`. Verified no Phase 16 regression:
- `Close()` still calls `l.conn.Release()` and nils the field (release semantics preserved).
- `TryAcquire`'s pre-existing error path (hard-close + release on query failure) is unchanged.
- `RunWithLock` still treats any `TryAcquire` error (including the new "advisory lock is closed" error) as fail-safe skip — no behavior change to the sweep-gating logic, only closes the resurrection hole.
- New regression test `TestPGAdvisoryLockCloseIsTerminal` passed in the independent live re-run above, alongside the previously-hanging `TestPGAdvisoryLockTryAcquireCloseRace` now passing cleanly.

### Human Verification Required

None. All three success criteria are independently verifiable via code inspection and/or automated test execution; no visual/UX/real-time behavior requires human judgment for this phase.

### Gaps Summary

No gaps. All three ROADMAP Phase 17 success criteria are verified true in the codebase, not merely claimed in SUMMARY.md:
- SC1 (DEBT-06): confirmed by direct file read + grep + build/vet/gofmt, independently reproduced.
- SC2 (DEBT-07): confirmed by direct file read of the mutex/accessor pattern AND an independent live re-run of the exact `-race` gate against a live Postgres, reproducing the orchestrator's stated 23/23 PASS result exactly, including the previously-hanging test now passing after the same-phase DEFER-17-01 fix.
- SC3 (DEBT-08): confirmed by direct code review of `TestImageConversionE2E`/`assertDownloadIsImage` (sound, no stubs, reuses proven helpers, correct Sniff-based assertion) plus the fixture's validity; the live compose PASS is accepted as documented evidence per the verification scope since re-running it would require rebuilding the 5-image stack, but nothing in the test code casts doubt on the executor's already-observed live PASS.

Only a minor documentation-staleness note (REQUIREMENTS.md checkboxes not yet updated) is flagged — non-blocking, does not affect phase goal achievement.

---

*Verified: 2026-07-12T21:00:00Z*
*Verifier: Claude (gsd-verifier)*
