---
phase: 01-merge-auth-rate-limiting
reviewed: 2026-07-03T12:00:00Z
depth: standard
files_reviewed: 19
files_reviewed_list:
  - cmd/api/main.go
  - cmd/manage-clients/main.go
  - internal/api/api.go
  - internal/api/handlers_test.go
  - internal/api/handlers.go
  - internal/api/routes.go
  - internal/auth/auth.go
  - internal/auth/context.go
  - internal/auth/hash_test.go
  - internal/auth/hash.go
  - internal/auth/middleware_test.go
  - internal/auth/middleware.go
  - internal/clients/clients.go
  - internal/clients/repo_test.go
  - internal/clients/repo.go
  - internal/db/migrations/0002_client_api_keys.sql
  - internal/jobs/jobs.go
  - internal/jobs/repo.go
  - internal/ratelimit/ratelimit_test.go
  - internal/ratelimit/ratelimit.go
findings:
  critical: 1
  warning: 3
  info: 2
  total: 6
status: issues_found
---

# Phase 01: Code Review Report

**Reviewed:** 2026-07-03T12:00:00Z
**Depth:** standard
**Files Reviewed:** 19
**Status:** issues_found

## Summary

Reviewed the API-key auth layer (`internal/auth`, `internal/clients`), the per-IP/per-client rate
limiter (`internal/ratelimit`), and their wiring into the HTTP layer (`internal/api`,
`cmd/api/main.go`, `cmd/manage-clients/main.go`). The core design is sound: keys are hashed with a
server-side salted SHA-256 digest before ever touching Postgres, key rotation uses independent
primary/secondary slots with per-slot revocation, guarded status transitions in `internal/jobs`
are unchanged and still correct, and cross-client job access correctly collapses to an identical
404 response.

The most significant finding is that the "coarse pre-auth IP flood guard" the code and its own
doc comments describe (`internal/api/routes.go`) is trivially bypassable: it is built on chi's
`middleware.RealIP`, which the chi maintainers themselves have marked **Deprecated** and
**vulnerable to IP spoofing** (see `github.com/go-chi/chi/v5/middleware/realip.go`), because it
blindly trusts client-supplied `X-Forwarded-For`/`X-Real-IP`/`True-Client-IP` headers and mutates
`r.RemoteAddr` from them before `ratelimit.ByIP` ever runs. Any caller can defeat the per-IP
throttle in front of the auth resolver's Postgres lookup by simply varying a request header. This
undermines the exact protection the middleware ordering comment claims to provide, so it is
classified as a blocker.

Three further robustness/quality issues were found: two ignored `ok` returns from
`auth.ClientFromContext` that create a latent nil-pointer-panic risk if the auth invariant is ever
violated, an orphaned-S3-object failure mode in `handleCreateJob` with no compensating cleanup
(asymmetric with the documented mitigation for enqueue failures), and a rate-limit config quirk
where `0` cannot be used to explicitly disable a limiter. Two minor info-level items are also
noted.

## Critical Issues

### CR-01: Per-IP rate limiter is bypassable via spoofed headers (defeats the pre-auth flood guard)

**File:** `internal/api/routes.go:22,28`
**Issue:**
`Routes()` installs `middleware.RealIP` globally (line 22) and then `ratelimit.ByIP(s.ipRateRPM)`
on the `/v1` group (line 28). `ratelimit.ByIP` keys on `httprate.KeyByIP`, which reads
`r.RemoteAddr` via `net.SplitHostPort`. But `chi/middleware.RealIP` (v5.3.0, the version pinned in
`go.mod`) unconditionally **overwrites** `r.RemoteAddr` with the first of `True-Client-IP`,
`X-Real-IP`, or the leftmost `X-Forwarded-For` value it finds — headers any HTTP client can set
freely. The chi package's own godoc for this exact function reads:

> Deprecated: RealIP is vulnerable to IP spoofing — it mutates r.RemoteAddr to the leftmost
> X-Forwarded-For value, or to True-Client-IP / X-Real-IP whether or not your infrastructure
> actually sets them. See GHSA-3fxj-6jh8-hvhx, GHSA-rjr7-jggh-pgcp, GHSA-9g5q-2w5x-hmxf.

As wired here, any unauthenticated caller can bypass the per-IP throttle entirely by sending a
different `X-Forwarded-For` value on every request (e.g. a random IP each time), making `ByIP`
key each request into its own empty bucket. This is precisely the "coarse pre-auth flood guard...
before any DB lookup" the `Routes()` doc comment says protects the auth resolver's Postgres lookup
from flood traffic (`internal/api/routes.go:14-18`) — the protection does not actually hold
against a hostile caller, only against accidental/naive flooding.

Whether this is externally reachable depends on deployment topology, but nothing in
`cmd/api/main.go` restricts trusted proxies, and the code/comments present `ByIP` as a real
security control rather than a best-effort one, so this should be fixed before relying on it.

**Fix:** Replace `middleware.RealIP` + `httprate.KeyByIP` with one of the non-deprecated
alternatives chi now ships (`middleware.ClientIPFromRemoteAddr` if the API is directly exposed with
no trusted reverse proxy, or `middleware.ClientIPFromXFFTrustedProxies` configured with the actual
proxy CIDR if one exists), and read the IP via `middleware.GetClientIP` in a custom `httprate` key
func instead of the header-trusting `RealIP`/`KeyByIP` combination. E.g.:

```go
// routes.go
r.Use(middleware.ClientIPFromRemoteAddr) // or ClientIPFromXFFTrustedProxies(trustedCIDRs) behind a known proxy

// ratelimit.go
func ByIP(rpm int) func(http.Handler) http.Handler {
	return httprate.Limit(
		rpm,
		window,
		httprate.WithKeyFuncs(func(r *http.Request) (string, error) {
			ip, _ := middleware.GetClientIP(r.Context())
			return ip, nil
		}),
		httprate.WithLimitHandler(limitHandler(window)),
	)
}
```

If there genuinely is no reverse proxy in front of the API in this deployment, dropping
`middleware.RealIP` entirely (and rate-limiting on the raw `RemoteAddr`, which the kernel/TCP
stack sets and callers cannot forge) is the simplest correct fix.

## Warnings

### WR-01: `auth.ClientFromContext` `ok` is discarded, risking a nil-pointer panic

**File:** `internal/api/handlers.go:85`, `internal/api/handlers.go:147`
**Issue:** Both `handleCreateJob` and `handleGetJob` do:
```go
client, _ := auth.ClientFromContext(ctx)
```
and then immediately dereference `client.ID`. The `internal/auth` package's own doc comment says
"Handlers downstream of Middleware can rely on ok being true" — true today given the fixed route
wiring in `routes.go`, but the code doesn't actually enforce or verify that invariant at the call
site. If a future refactor adds a route to the `/v1` group before `auth.Middleware` runs, removes
the middleware from one path, or a handler is invoked directly (as opposed to through
`Routes()`), this becomes a nil-pointer dereference. `middleware.Recoverer` would turn it into a
generic 500, but the failure would be silent (no log line identifying "auth invariant violated"),
making it harder to diagnose than a clear, intentional error.
**Fix:**
```go
client, ok := auth.ClientFromContext(ctx)
if !ok {
	writeError(w, http.StatusInternalServerError, "internal error")
	return
}
```
(mirrors the existing pattern of never trusting an unchecked assertion elsewhere in the codebase).

### WR-02: Orphaned S3 objects when job creation fails after upload succeeds

**File:** `internal/api/handlers.go:79-108`
**Issue:** `handleCreateJob` uploads the input file to storage (line 79) and only afterwards
inserts the job row via `s.repo.Create` (line 89). If `s.repo.Create` fails (DB error, connection
drop, etc.), the handler returns a 500 but the just-uploaded object at `key` is never deleted —
it is permanently orphaned in S3 with no job row to ever reference or clean it up. This is
asymmetric with the very next failure mode (enqueue failure, line 110), which is explicitly
handled and documented: "The row stays in 'queued'; a reconciler (next steps) will recover it."
No equivalent mitigation or comment exists for the upload-succeeds/create-fails case.
**Fix:** Either delete the uploaded object on `Create` failure (compensating action), e.g.:
```go
createdID, err := s.repo.Create(ctx, jobs.CreateParams{...})
if err != nil {
	_ = s.storage.Delete(ctx, key) // best-effort cleanup; log failures
	writeError(w, http.StatusInternalServerError, "failed to create job")
	return
}
```
or explicitly document (like the enqueue path) that orphaned objects are an accepted, tracked
tradeoff pending a garbage-collection job.

### WR-03: Rate limit thresholds cannot be explicitly disabled; `0` silently becomes the default

**File:** `internal/api/api.go:64-69`
**Issue:** `NewServer` treats `cfg.IPRateLimitRPM == 0` and `cfg.ClientRateLimitRPM == 0` as "not
configured" and substitutes the hardcoded defaults (60 / 120). Combined with
`cmd/api/main.go:58-59` (`envInt64("RATE_LIMIT_IP_RPM", 60)` etc.), an operator who explicitly sets
`RATE_LIMIT_IP_RPM=0` intending to disable the limiter instead silently gets the default of 60 —
with no log line indicating the override happened. This is a footgun: the config value the
operator set is silently ignored.
**Fix:** Distinguish "unset" from "explicitly zero" using a pointer/optional type, or document
(and log at startup) that `0` always means "use default" and cannot express "disabled". If
"disabled" needs to be expressible, use a sentinel like `-1` and validate/reject other negative
values explicitly at startup rather than passing them straight into `httprate.Limit` unchecked.

## Info

### IN-01: `RevokeKey` leaves the revoked key digest in the unique `api_key_hash*` column

**File:** `internal/clients/repo.go:95-121`
**Issue:** `RevokeKey` stamps `primary_revoked_at`/`secondary_revoked_at` but never clears the
corresponding `api_key_hash`/`api_key_hash_secondary` column. Because those columns carry a `UNIQUE`
constraint (`internal/db/migrations/0002_client_api_keys.sql:10-11`), a revoked digest can never be
reused as any client's key again — practically irrelevant given 256-bit random keys, but worth a
one-line comment (or nulling the column on revoke) so it's clear this is an intentional tradeoff
rather than an oversight.
**Fix:** Either add a short comment explaining the tradeoff, or null the hash column alongside
setting `*_revoked_at` in the `UPDATE` (`... SET primary_revoked_at = now(), api_key_hash = NULL ...`).

### IN-02: No log/telemetry when the auth resolver returns a non-`ErrInvalidKey` error

**File:** `internal/auth/middleware.go:31-39`
**Issue:** When `resolver.ResolveClient` fails with an error other than `ErrInvalidKey` (e.g. the
database is unreachable), `Middleware` returns a 500 but never logs anything — `internal/auth` is
library code and per project convention doesn't log directly (`CLAUDE.md` "Logging" convention),
but there's currently no hook (returned error, metrics counter, etc.) for `cmd/api/main.go` or an
error-handling middleware to observe this class of failure either. Every DB-unreachable request
silently becomes an opaque 500 with no operator-visible signal beyond the generic HTTP access log.
**Fix:** Not necessarily a code change here — but confirm there is a plan (e.g. chi's `Logger`
middleware plus response status, or a metrics middleware) for surfacing repeated 500s from this
path in an on-call/alerting sense, since it's the single chokepoint for all authenticated traffic.

---

_Reviewed: 2026-07-03T12:00:00Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
