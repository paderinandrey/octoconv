---
phase: 12-tech-debt-cleanup
verified: 2026-07-10T00:00:00Z
status: passed
score: 5/5 must-haves verified
overrides_applied: 0
---

# Phase 12: Tech Debt Cleanup Verification Report

**Phase Goal:** Закрыть унаследованный advisory tech debt (v1.0 docker-compose audit + v1.2 11-REVIEW.md findings) перед новой движковой работой.
**Verified:** 2026-07-10
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | `docker-compose.e2e.yml` gives the `api` service `extra_hosts: host.docker.internal:host-gateway` | VERIFIED | `docker compose -f docker-compose.yml -f docker-compose.e2e.yml config --format json` → `.services.api.extra_hosts == ["host.docker.internal=host-gateway"]`. Merged config renders cleanly (exit 0). |
| 2 | Engine-class literals exist as exported constants in `internal/convert`; api/reconciler/worker code references constants; no raw literals outside `internal/convert` | VERIFIED | `grep -rn '"image"\|"document"' --include='*.go' internal/ cmd/ \| grep -v '_test.go' \| grep -v '^internal/convert/convert.go'` returns 0 lines. `convert.go` defines `EngineImage`/`EngineDocument`. `internal/api/handlers.go:265,267`, `internal/reconciler/reconciler.go:134,136`, `internal/queue/queue.go:31,33` all reference `convert.EngineImage`/`convert.EngineDocument`. The two `Engine()` doc comments (libvips.go:37, libreoffice.go:70) were reworded to drop the quoted literal, confirmed by direct read. |
| 3 | E2E HTTP clients carry explicit per-request timeouts; no `http.DefaultClient` remains in `internal/e2e` | VERIFIED | `grep -c 'http.DefaultClient' internal/e2e/e2e_test.go` = 0. Shared `e2eHTTP = &http.Client{Timeout: 30*time.Second}` (line 58) used by `postJob`/`pollUntilDone` (lines 150, 188); `downloadClient()` both branches carry `downloadClientTimeout = 60*time.Second` (lines 409, 412). |
| 4 | `gofmt -l .` returns zero files | VERIFIED | `gofmt -l .` produced empty output (exit 0). `go vet ./...` clean, `go build ./...` clean. |
| 5 | Every `.env.example` variable is wired into the corresponding docker-compose.yml service or has an explicit, accurate justification comment | VERIFIED | Walked all 30 vars in `.env.example` against `docker-compose.yml`: all consumed vars present in the correct service(s) (traced via `os.Getenv`/`envDuration` call sites in `cmd/*/main.go` and `internal/queue/client.go`). `STORAGE_TTL` correctly api-only (sole consumer `cmd/api/main.go:59`). `WEBHOOK_ALLOW_PRIVATE_IPS`/`WEBHOOK_ALLOW_INSECURE_HTTP` deliberately omitted from base compose with accurate inline comment (lines 101-105); confirmed `grep -E 'WEBHOOK_ALLOW_(PRIVATE_IPS\|INSECURE_HTTP):\s*"?true' docker-compose.yml` returns 0 matches (SSRF guard stays default-off in base, relaxed only in `docker-compose.e2e.yml`). Post-review fix commit `d6a6ad1` corrected the previously-false document-worker `WEBHOOK_SIGNING_SECRET`/`WEBHOOK_PRESIGN_TTL` comment (WR-01) — current text ("document-worker only PRODUCES webhook-delivery tasks; signing happens in cmd/worker's HandleWebhookDeliver... currently inert here -- wired for parity/explicitness") is now accurate: cross-checked against `cmd/document-worker/main.go:50-54` ("document-worker neither delivers nor signs webhooks... inert for HandleDocumentConvert") and `internal/worker/worker.go:339` (`webhook.SignPayload` only called from `HandleWebhookDeliver`, registered exclusively in `cmd/worker/main.go`). |

**Score:** 5/5 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/convert/convert.go` | Exported `EngineImage`/`EngineDocument` constants | VERIFIED | Lines 11-19: exported const block with doc comment naming it the single source of truth. |
| `docker-compose.e2e.yml` | api-service `extra_hosts` host-gateway alias | VERIFIED | Confirmed via merged `docker compose config`. |
| `internal/e2e/e2e_test.go` | Shared HTTP client(s) with explicit per-request `Timeout` | VERIFIED | `e2eHTTP` (30s) + `downloadClientTimeout` (60s, both branches). |
| `docker-compose.yml` | env wiring reconciled against `.env.example` with logged omissions | VERIFIED | `MAX_IMAGE_PIXELS` present under `api` (line 87); full var walk performed (see truth 5). |

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| `internal/api/handlers.go` | `convert.EngineImage` / `convert.EngineDocument` | engine routing switch | WIRED | Lines 265, 267: `case convert.EngineImage:` / `case convert.EngineDocument:` |
| `internal/reconciler/reconciler.go` | `convert.EngineImage` / `convert.EngineDocument` | recovery-routing switch | WIRED | Lines 134, 136: `case convert.EngineImage:` / `case convert.EngineDocument:` |
| `internal/queue/queue.go` | `convert.EngineImage` / `convert.EngineDocument` | queue-name constants | WIRED | Lines 31, 33: `QueueImage = convert.EngineImage`, `QueueDocument = convert.EngineDocument` |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Full build/vet/format clean | `gofmt -l . && go vet ./... && go build ./...` | all exit 0, no output from gofmt | PASS |
| Full test suite green | `go test ./...` | all packages `ok` (e2e passes offline, self-skipping design intact) | PASS |
| DEBT-02 literal-elimination gate | grep gate (see truth 2) | 0 lines | PASS |
| DEBT-01 api-scoped extra_hosts | `docker compose -f docker-compose.yml -f docker-compose.e2e.yml config --format json` + node JSON check | `["host.docker.internal=host-gateway"]` on `api` | PASS |
| Base compose SSRF guard not relaxed | `grep -E 'WEBHOOK_ALLOW_(PRIVATE_IPS\|INSECURE_HTTP):\s*"?true' docker-compose.yml` | 0 matches | PASS |
| `docker compose config` renders (base + merged) | both variants | exit 0 | PASS |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|--------------|-------------|-------------|--------|----------|
| DEBT-01 | 12-01-PLAN.md | E2E works on plain-Linux docker via api extra_hosts | SATISFIED | See truth 1 |
| DEBT-02 | 12-01-PLAN.md | Engine-class literals as exported constants | SATISFIED | See truth 2 |
| DEBT-03 | 12-01-PLAN.md | E2E HTTP clients have per-request timeouts | SATISFIED | See truth 3 |
| DEBT-04 | 12-01-PLAN.md | `gofmt -l .` clean | SATISFIED | See truth 4 |
| DEBT-05 | 12-01-PLAN.md | docker-compose.yml reconciled against .env.example | SATISFIED | See truth 5 |

No orphaned requirements — all 5 IDs declared in PLAN frontmatter match REQUIREMENTS.md traceability table for Phase 12 exactly.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| — | — | No TBD/FIXME/XXX/TODO/HACK/PLACEHOLDER found in any of the 10 phase-touched files | — | — |

**Note (informational, non-blocking):** `12-REVIEW.md` (code review of this phase's diff) raised two additional warnings — WR-02 (unroutable-engine fail-closed paths in `handlers.go`/`reconciler.go` can leave a permanently-`queued` zombie job row if a future engine class is registered without a matching queue case) and WR-03 (reconciler's exhausted-path swallows a `MarkFailed` error before recording a metric and firing a webhook, pre-existing in `reconciler.go`). Neither is part of DEBT-01..05's scope or REQUIREMENTS.md's Phase 12 traceability; WR-03 is explicitly pre-existing and WR-02 concerns a not-yet-existing future engine class. These do not block phase 12 goal achievement but are worth tracking as candidate follow-up debt in a later phase.

### Human Verification Required

None. All five DEBT truths are mechanically verifiable (grep/build/test/compose-config) and were verified directly against the current repository state; DEBT-01's live plain-Linux run is explicitly out of scope per the success criteria (config-level verification accepted, dev box is macOS).

### Gaps Summary

No gaps. All 5 roadmap/PLAN must-haves (DEBT-01 through DEBT-05) are verified against the current codebase state, including the post-review WR-01 fix (commit `d6a6ad1`) which was independently cross-checked against the actual signing code path in `internal/worker/worker.go` and `cmd/document-worker/main.go` and found accurate. `go build`, `go vet`, `go test ./...`, and `gofmt -l .` are all clean. No debt markers (TBD/FIXME/XXX) found in any file this phase touched.

---

_Verified: 2026-07-10_
_Verifier: Claude (gsd-verifier)_
