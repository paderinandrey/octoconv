---
phase: 01-merge-auth-rate-limiting
plan: 01
subsystem: auth
tags: [postgres, pgx, crypto/sha256, crypto/rand, api-keys]

# Dependency graph
requires: []
provides:
  - "0002_client_api_keys.sql migration: api_key_hash, api_key_hash_secondary, primary_revoked_at, secondary_revoked_at, updated_at columns + partial indexes + clients_set_updated trigger"
  - "internal/auth.GenerateKey / internal/auth.HashKey: pure salted-SHA-256 key generation/hashing helpers"
  - "internal/clients.Repo: Create, GetByKeyHash, AddSecondaryKey, RevokeKey"
  - "cmd/manage-clients operator CLI: create / add-key / revoke subcommands"
  - "API_KEY_SALT env var (documented in .env.example)"
affects: [01-02, request-path-auth-middleware, rate-limiting]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Salted SHA-256 digest (not bcrypt/argon2) for high-entropy API keys, pepper passed as a parameter (no os.Getenv inside internal/auth or internal/clients)"
    - "Two independent key slots per client, each with its own revoked_at timestamp, for zero-downtime rotation"
    - "Raw secret material only ever reaches fmt.Println at the CLI boundary, never log.*"

key-files:
  created:
    - internal/db/migrations/0002_client_api_keys.sql
    - internal/auth/hash.go
    - internal/auth/hash_test.go
    - internal/clients/clients.go
    - internal/clients/repo.go
    - internal/clients/repo_test.go
    - cmd/manage-clients/main.go
  modified:
    - .env.example

key-decisions:
  - "Column-level UNIQUE constraints on api_key_hash/api_key_hash_secondary provide global uniqueness; the two partial indexes (clients_api_key_hash_idx, clients_api_key_hash_secondary_idx) are plain (non-unique) partial indexes scoped to the non-revoked rows, mirroring the jobs_inflight_idx convention, since uniqueness is already guaranteed by the column constraint"
  - "RevokeKey uses two static UPDATE statements selected by a Go switch (not dynamic SQL column interpolation) to keep all queries as compile-time string literals"

patterns-established:
  - "Pure hashing/generation helpers stay free of os.Getenv; secrets are threaded through as explicit parameters by callers (cmd/manage-clients reads API_KEY_SALT once and passes it down)"

requirements-completed: [BASE-01, AUTH-01, AUTH-04, AUTH-05]

# Metrics
duration: 14min
completed: 2026-07-03
---

# Phase 01 Plan 01: API-Key Issuance Foundation Summary

**Salted-SHA-256 client API key issuance: `0002` migration with dual-slot key columns, `internal/auth` hash helpers, `internal/clients` repository, and a `manage-clients` operator CLI supporting create/add-key/revoke.**

## Performance

- **Duration:** 14 min
- **Started:** 2026-07-03T02:27:00+03:00
- **Completed:** 2026-07-03T02:41:58+03:00
- **Tasks:** 3
- **Files modified:** 8

## Accomplishments
- Confirmed BASE-01 baseline (`go build ./...` + `go test ./...` green) before adding new code
- `0002_client_api_keys.sql` ALTERs `clients` with two independent key slots, each with its own revoked-at timestamp — the structural precondition for AUTH-05 zero-downtime rotation
- `internal/auth` provides pure, dependency-free `GenerateKey`/`HashKey` (crypto/rand + crypto/sha256, no bcrypt/argon2), fully covered by table-driven TDD tests (RED then GREEN)
- `internal/clients.Repo` gives the per-request auth lookup (`GetByKeyHash`) and the operator-facing key lifecycle (`Create`, `AddSecondaryKey`, `RevokeKey`), mirroring `internal/jobs/repo.go` conventions
- `cmd/manage-clients` lets an operator mint a client + primary key, add a secondary key, and revoke either slot independently — the raw key is printed exactly once via `fmt.Println` and never reaches `log.*`

## Task Commits

Each task was committed atomically:

1. **Task 1: Confirm baseline green, add key-hash migration 0002** - `ad38a9c` (feat)
2. **Task 2: Salted-hash helper + clients repository** - `9a37c92` (test/RED) → `1f2bf8e` (feat/GREEN, auth.HashKey/GenerateKey) → `e9c7eee` (feat, clients repository)
3. **Task 3: Operator CLI cmd/manage-clients** - `7045b04` (feat)

_Task 2 followed TDD: hash_test.go committed first as a compile-failing RED commit, then hash.go implemented to turn it GREEN. The clients repository (no `<behavior>` block, integration-test-only) was committed as a single feat commit per plan intent._

**Plan metadata:** (to be added by orchestrator after merge)

## Files Created/Modified
- `internal/db/migrations/0002_client_api_keys.sql` - ALTERs `clients` with dual key-slot columns, partial indexes, `clients_set_updated` trigger
- `internal/auth/hash.go` - `GenerateKey` (32 crypto/rand bytes, base64url) + `HashKey` (salted SHA-256, deterministic)
- `internal/auth/hash_test.go` - table-driven tests: determinism, salt-sensitivity, raw-sensitivity, hex format, no raw-key leakage
- `internal/clients/clients.go` - `Client` domain type (id, name)
- `internal/clients/repo.go` - `Repo` with `Create`, `GetByKeyHash`, `AddSecondaryKey`, `RevokeKey`; `ErrNotFound` sentinel
- `internal/clients/repo_test.go` - DATABASE_URL-gated integration tests: create/lookup round-trip, unknown digest, secondary-key rotation, independent per-slot revocation
- `cmd/manage-clients/main.go` - operator CLI: `create <name>`, `add-key <client-id>`, `revoke <client-id> <primary|secondary>`
- `.env.example` - documents new required `API_KEY_SALT` variable

## Decisions Made
- Used plain (non-unique) partial indexes for the hot auth-lookup path since the column-level `UNIQUE` constraints already enforce global uniqueness on `api_key_hash`/`api_key_hash_secondary`; this mirrors the existing `jobs_inflight_idx` non-unique partial-index convention rather than introducing a second, redundant unique index.
- `RevokeKey` dispatches to one of two fully-static SQL strings via a Go `switch` (rather than building the column name into the query string) to avoid any dynamic SQL construction, even though the slot value itself is never attacker-controlled in this CLI-only usage.

## Deviations from Plan

None - plan executed exactly as written. The only interpretive choice (unique vs. plain partial indexes) is documented above as a Decision, not a deviation, since the plan text described them as "partial indexes... mirrors the existing jobs_inflight_idx partial-index convention" without specifying uniqueness, and jobs_inflight_idx is itself non-unique.

## Issues Encountered
- No live Postgres instance was available at the expected `DATABASE_URL` (port 5433) in this worktree's sandbox; a `docker compose up -d postgres` attempt conflicted with an existing stopped `octoconv-db` container from a sibling context, and — to avoid interfering with shared Docker state potentially used by parallel wave agents — no attempt was made to start or reuse it. The `internal/clients` integration tests and `go run ./cmd/migrate` were therefore not executed against a live database in this session; they follow the exact `DATABASE_URL`-gated skip pattern already proven in `internal/jobs/repo_test.go` and compile/vet cleanly. `go build ./...` and `go test ./...` (including the new `internal/auth` unit tests, which need no database) all pass.

## User Setup Required

None - no external service configuration required. Note: before this feature is exercised against a real database, an operator must set `API_KEY_SALT` in their `.env` (documented in `.env.example`) and run `go run ./cmd/migrate` to apply `0002_client_api_keys.sql`.

## Next Phase Readiness
- The API-key issuance foundation (schema, hashing, repo, CLI) is complete and ready for the next plan to wire `GetByKeyHash` into request-path auth middleware and add `jobs.client_id` association.
- Integration tests (`internal/clients/repo_test.go`) and the live migration run should be exercised against a real `DATABASE_URL` (e.g., via `docker compose up -d postgres`) before or during the next plan's work, to confirm the schema behaves as expected end-to-end.

---
*Phase: 01-merge-auth-rate-limiting*
*Completed: 2026-07-03*
