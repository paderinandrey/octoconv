---
phase: 01-merge-auth-rate-limiting
reviewed: 2026-07-04T00:00:00Z
depth: standard
files_reviewed: 4
files_reviewed_list:
  - internal/api/routes.go
  - internal/api/routes_test.go
  - internal/jobs/repo_test.go
  - internal/ratelimit/ratelimit.go
findings:
  critical: 0
  warning: 1
  info: 1
  total: 2
status: issues_found
---

# Phase 01: Code Review Report (Gap Closure)

**Reviewed:** 2026-07-04T00:00:00Z
**Depth:** standard
**Files Reviewed:** 4
**Status:** issues_found

## Summary

Reviewed the two targeted gap-closure fixes for Phase 1:

1. **Gap 1 (jobs.client_id FK):** `internal/jobs/repo_test.go` now inserts a real `clients` row via
   `createTestClient` and threads its id into every `CreateParams.ClientID` used by the integration
   tests. Verified against the schema (`clients.api_key_hash` is a nullable `UNIQUE` column, so
   inserting `name`-only rows does not collide across repeated test runs) and confirmed empirically:
   ran the full `internal/jobs` integration suite against a live `octoconv-db` Postgres container
   (`DATABASE_URL=postgres://octo:...@localhost:5434/octo_db`) — `TestJobLifecycle`, `TestMarkFailed`,
   `TestGetNotFound` all pass, where previously an FK violation on `jobs.client_id` would have failed
   `Create`. The fix is correct.

2. **Gap 2 (RealIP spoofing / CR-01):** `internal/api/routes.go` replaces the deprecated
   `middleware.RealIP` with `middleware.ClientIPFromRemoteAddr`, and `internal/ratelimit/ratelimit.go`'s
   `ipKey` reads the unforgeable peer IP via `middleware.GetClientIP` (falling back to a raw
   `RemoteAddr` parse only when that context value is absent, e.g. direct unit tests of `ByIP` in
   isolation — never falling back to a client-supplied header). Verified the chi v5.3.0 API surface
   directly (`go doc`) to confirm `ClientIPFromRemoteAddr`/`GetClientIP` behave as the code comments
   claim, and confirmed the new `internal/api/routes_test.go` test
   (`TestByIP_NotEvadedByForwardedForSpoofing`) exercises the real router end-to-end: 5 requests
   share one `RemoteAddr` but carry distinct spoofed `X-Forwarded-For` values, and the coarse IP
   limiter (rpm=2) correctly rejects requests 3-5 with 429 regardless of the header. `go build`,
   `go vet`, and `go test` all pass for the affected packages.

Both fixes are functionally correct and directly close the previously identified gaps (RATE-03 /
ROADMAP SC5 / 01-REVIEW.md CR-01, and the jobs.client_id FK integration-test failure). No Critical
or blocking issues found. One test-hygiene Warning and one Info item below.

## Warnings

### WR-01: `createTestClient` never cleans up inserted `clients` rows — unbounded growth in the shared test database

**File:** `internal/jobs/repo_test.go:33-41`
**Issue:** `createTestClient` inserts a new `clients` row on every call (`TestJobLifecycle` and
`TestMarkFailed` each call it once) but never deletes it — there is no `t.Cleanup` doing a
corresponding `DELETE FROM clients WHERE id = $1`, unlike `newTestRepo`'s `t.Cleanup(pool.Close)` for
the pool itself. Since `clients.name` has no uniqueness constraint, repeated runs don't fail, but the
table grows without bound as tests are re-run against a long-lived dev/CI Postgres instance. This was
empirically confirmed: after only a handful of local test runs the live `octoconv-db` container
already had 12 accumulated rows with `name = 'jobs-test-client'`:
```
docker exec octoconv-db psql -U octo -d octo_db -c \
  "SELECT count(*) FROM clients WHERE name='jobs-test-client';"
 count
-------
    12
```
This is a new helper introduced specifically by this gap-closure change, so it's the right place to
fix it rather than let the pattern spread to future tests that call `createTestClient`. It doesn't
cause incorrect production behavior, but it does degrade the reliability/hygiene of the integration
test suite over time (a long-lived shared database accumulating orphaned rows indefinitely), and
`jobs.client_id` being `ON DELETE SET NULL` means these orphaned client rows will never be cleaned up
transitively by cascading job deletes either.

**Fix:** Register a cleanup alongside the insert, mirroring the existing `t.Cleanup(pool.Close)`
pattern:
```go
func createTestClient(t *testing.T, r *Repo) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := r.pool.QueryRow(context.Background(), "INSERT INTO clients (name) VALUES ($1) RETURNING id", "jobs-test-client").Scan(&id)
	if err != nil {
		t.Fatalf("create test client: %v", err)
	}
	t.Cleanup(func() {
		if _, err := r.pool.Exec(context.Background(), "DELETE FROM clients WHERE id = $1", id); err != nil {
			t.Logf("cleanup test client %s: %v", id, err)
		}
	})
	return id
}
```

## Info

### IN-01: `ipKey`'s `net.SplitHostPort` failure fallback returns the whole `RemoteAddr` (including port) as the rate-limit bucket key

**File:** `internal/ratelimit/ratelimit.go:80-83`
**Issue:** When `middleware.GetClientIP` returns `""` (middleware not run — only expected to happen
in isolated unit tests of `ByIP`, not in production where `Routes()` always installs
`ClientIPFromRemoteAddr` globally) and `r.RemoteAddr` is itself unparseable by
`net.SplitHostPort` (no `host:port` form), the fallback returns the raw `r.RemoteAddr` string
verbatim, which could include a port. In practice Go's `net/http` server always populates
`RemoteAddr` as `host:port` for real TCP connections, so this path is effectively unreachable in
production; it only matters for hand-constructed test requests with a malformed `RemoteAddr` (not
exercised by the current test suite, and not a regression — this is pre-existing fallback-path
shape, just now serving the same purpose for the new middleware). Flagged for completeness only;
no fix required unless a future test starts hitting this branch and gets an unexpectedly bucketed
key.

---

_Reviewed: 2026-07-04T00:00:00Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
