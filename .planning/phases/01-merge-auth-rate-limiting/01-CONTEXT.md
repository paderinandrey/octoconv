# Phase 1: Merge, Auth & Rate Limiting - Context

**Gathered:** 2026-07-03
**Status:** Ready for planning

<domain>
## Phase Boundary

The existing image-conversion vertical slice (currently on `feat/scaffold-and-infra`) lands on `main`. Every API request must present a valid, client-scoped API key — unauthenticated, invalid, or excessive traffic is rejected before it can affect production. This phase covers: merge, API-key auth (issuance, verification, revocation), and per-client + coarse IP-based rate limiting. It does NOT cover webhooks, reconciler/retry-safety, content validation, storage lifecycle, or observability — those are Phases 2-4.

</domain>

<decisions>
## Implementation Decisions

### Merge Strategy
- **D-01:** Merge `feat/scaffold-and-infra` into `main` via a merge commit (not squash) — preserves the 7-commit history showing how the vertical slice was built in stages.
- **D-02:** Delete the `feat/scaffold-and-infra` branch after a successful merge — `main` becomes the single source of truth going forward.

### API Key Provisioning
- **D-03:** Clients are provisioned via an operator-run CLI command (e.g. `go run ./cmd/manage-clients create <name>`), following the existing `cmd/migrate`-style one-shot-binary pattern already in the codebase. The CLI creates the `clients` row, generates a random high-entropy key, hashes it, and prints the raw key exactly once — it is never stored or logged in plaintext.
- **D-04:** `clients` rows store only `name` at creation (plus id, key hash(es), timestamps) — no team/contact metadata field for this phase. Matches the minimal `clients` schema already in the Notion DDL.
- **D-05:** Key revocation is a CLI command (e.g. `manage-clients revoke <key-id>`) that marks the stored hash inactive — does NOT delete the `clients` row (jobs already reference `client_id` via FK, and history should survive revocation).
- **D-06:** No automatic time-based key rotation/expiry in this phase. The `clients` schema supports two simultaneously active key hashes (primary + secondary) so an operator CAN rotate without downtime, but rotation itself is a manual CLI-triggered operation, not a TTL/cron job.
- **D-07:** Keys are stored as salted **hashes** (SHA-256, per research STACK.md — fast, high-entropy tokens don't need bcrypt/argon2), never encrypted/reversible, never plaintext, never logged.

### Auth Rollout
- **D-08:** Hard cutover — the moment auth middleware is deployed, every request without a valid, active API key gets 401 immediately. No warn-only/grace period: the service is not yet in production and has no real clients to break.
- **D-09:** `/healthz` and the future `/metrics` endpoint (Phase 4) stay outside the auth middleware chain — unauthenticated, so orchestrators/scrapers don't need a key.

### Rate Limiting
- **Claude's Discretion:** Specific numeric limits (requests/min per client, burst size, coarse pre-auth IP threshold) were not discussed — user deferred this to Claude's judgment. Use research findings (FEATURES.md, STACK.md) as the basis: per-client token bucket keyed on `client_id` (not IP), a coarse cheap pre-auth IP-based flood guard before auth/DB lookup, 429 + `Retry-After` header on exceed. Pick conservative defaults suitable for internal batch + interactive usage patterns; make them configurable via env var following the existing `os.Getenv`-only configuration convention (no config file).

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Project & Requirements
- `.planning/PROJECT.md` — Core value, locked scope (auth first, internal clients only, API keys via `clients` table, no mTLS/OAuth2), Key Decisions table
- `.planning/REQUIREMENTS.md` — v1 requirements BASE-01, AUTH-01..05, RATE-01..03 mapped to this phase; Out of Scope table
- `.planning/ROADMAP.md` — Phase 1 goal, success criteria, dependencies

### Research (production-hardening milestone)
- `.planning/research/SUMMARY.md` — Executive synthesis; sequencing rationale (auth before rate limiting/webhooks)
- `.planning/research/STACK.md` — Library choices: `crypto/sha256` + `crypto/subtle.ConstantTimeCompare` for key hashing (not bcrypt/argon2); `github.com/go-chi/httprate` v0.15.0 for rate limiting (in-process now, `go-redis/redis_rate/v10` when horizontally scaled)
- `.planning/research/ARCHITECTURE.md` — Middleware ordering pattern (Pattern 3: coarse pre-auth IP limit → auth → per-client rate limit); recommended package layout (`internal/auth/`, `internal/ratelimit/`, `internal/clients/`)
- `.planning/research/PITFALLS.md` — Pitfall 5 (auth trusting network position instead of the key — explicitly must NOT happen), Pitfall 6 (plaintext key storage / no rotation support), Pitfall 9 (rate limiting on wrong identity — must key on `client_id`, not IP)

### Existing Codebase (reference patterns to follow)
- `.planning/codebase/ARCHITECTURE.md` — Existing `internal/api/api.go` narrow-interface pattern (`Repo`, `Storage`, `Enqueuer`) to mirror for a new `auth.ClientResolver` interface; `internal/api/routes.go` chi middleware chain (RequestID, RealIP, Logger, Recoverer) where new middleware slots in
- `.planning/codebase/STACK.md` — Confirms Go 1.26, chi v5.3.0, no config-file support (env-var only) — new rate-limit thresholds must follow this convention

### Notion (source-of-truth schema)
- "OctoConv — стек и модель данных" (Notion) — full DDL for `clients`, `jobs.client_id`; confirms `clients` table has no existing key-hash columns, will need a migration to add them (e.g. `api_key_hash`, `api_key_hash_secondary`, `revoked_at` or `is_active`)

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `internal/api/api.go` — narrow local interfaces (`Repo`, `Storage`, `Enqueuer`) at package boundaries; the new auth resolver should follow the same interface-segregation pattern rather than a concrete struct dependency
- `internal/api/routes.go` — existing chi middleware chain (`RequestID`, `RealIP`, `Logger`, `Recoverer`); new auth/rate-limit middleware groups slot in per research's Pattern 3 (coarse IP limit → auth → per-client limit), while `/healthz` stays outside the group
- `cmd/migrate/main.go` — existing one-shot CLI binary pattern (connect to Postgres, do a job, exit) to mirror for the new `cmd/manage-clients` (or similar) provisioning CLI

### Established Patterns
- Postgres-first, guarded-transition pattern (`internal/jobs/repo.go` `Repo.transition`) — not directly reused for auth, but sets the code-style precedent (explicit repo methods, row locking, wrapped errors) that a new `internal/clients` repo should follow
- Environment-variable-only configuration (`os.Getenv` in `cmd/*/main.go`) — new rate-limit thresholds and any auth-related tunables must follow this, no config file introduced

### Integration Points
- `internal/api/routes.go` — where the new middleware chain attaches
- `internal/jobs` — `jobs.client_id` column already exists in the Notion DDL; job creation/lookup handlers need to thread the resolved `client_id` through and enforce ownership on `GET /v1/jobs/{id}` (404 for cross-client access, not 403)
- New `internal/clients/` package (per research's recommended structure) for the `clients` repo (CRUD, key hash lookup) — separate from `internal/jobs`

</code_context>

<specifics>
## Specific Ideas

No specific UI/UX references — this is a backend/API phase. The concrete asks captured above (merge commit + branch deletion, CLI-based provisioning/revocation, hard cutover, `/healthz` stays public) are the specifics.

</specifics>

<deferred>
## Deferred Ideas

None — discussion stayed within phase scope. No scope creep occurred; rate-limit numeric values were explicitly delegated to Claude's discretion rather than deferred to a future phase.

</deferred>

---

*Phase: 1-Merge, Auth & Rate Limiting*
*Context gathered: 2026-07-03*
