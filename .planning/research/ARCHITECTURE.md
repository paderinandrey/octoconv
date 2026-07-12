# Architecture Research

**Domain:** Integration architecture for OctoConv v1.4 (CI pipeline, presets, debt cleanup)
**Researched:** 2026-07-12
**Confidence:** HIGH (all findings verified directly against current repo source — no external ecosystem claims in this milestone)

This is an **integration** research doc, not a greenfield architecture doc: v1.4 adds three
independent, mostly-non-interacting feature groups on top of an already-hardened system
(v1.0–v1.3 shipped). Each section below identifies exactly what's NEW vs MODIFIED, with file
paths, and where each piece plugs into the existing dependency graph
(`cmd/* → internal/{api,worker} → internal/{jobs,storage,queue,convert} → internal/db`).

## Standard Architecture

### System Overview (v1.4 deltas highlighted)

```
┌──────────────────────────────────────────────────────────────────────────┐
│  cmd/ (entry points)                                                       │
│  ┌────────┐ ┌────────┐ ┌──────────┐ ┌─────────┐ ┌───────────────┐         │
│  │  api   │ │ worker │ │ document │ │chromium │ │ webhook-worker│         │
│  │        │ │(image) │ │ -worker  │ │-worker  │ │               │         │
│  └───┬────┘ └───┬────┘ └────┬─────┘ └────┬────┘ └──────┬────────┘         │
│      │          │           │            │              │                 │
│  ┌───▼──────────────────────▼────────────▼──────────────▼──────┐          │
│  │ NEW: cmd/manage-presets  (mirrors cmd/manage-clients)         │          │
│  │      cmd/migrate — unchanged (no new migration expected)      │          │
│  └────────────────────────────────────────────────────────────────┘        │
├──────────────────────────────────────────────────────────────────────────┤
│  internal/api  (MODIFIED: api.go +PresetRepo iface, handlers.go +preset   │
│                 resolution step in handleCreateJob)                       │
│  internal/jobs (MODIFIED: Repo.Create persists preset_name/preset_version │
│                 — columns already exist in DDL, unused until now)         │
│  internal/presets (NEW package, mirrors internal/clients' Repo shape)     │
│  internal/worker, internal/queue, internal/convert, internal/storage,     │
│  internal/webhook — UNCHANGED                                             │
│  internal/reconciler — MODIFIED (test-only): fakeEnqueuer gets a mutex    │
│    (debt fix, -race prerequisite)                                        │
│  cmd/document-worker, cmd/chromium-worker — MODIFIED (debt): dead         │
│    webhook.NewRepo/NewDeliverer wiring removed                           │
│  internal/e2e — MODIFIED: new TestImageConversionE2E (debt fix, mirrors  │
│                 existing TestDocumentConversionE2E/TestHTMLConversionE2E) │
├──────────────────────────────────────────────────────────────────────────┤
│  internal/db — UNCHANGED. presets table + jobs.preset_name/preset_version │
│                already exist in 0001_init.sql (dormant since Phase 1) —   │
│                v1.4 activates existing schema, no new migration needed.   │
├──────────────────────────────────────────────────────────────────────────┤
│  .github/workflows/  — NEW, entirely outside internal/cmd. gate → race →  │
│                 docker-build → e2e-live, reusing docker-compose.yml +     │
│                 docker-compose.e2e.yml unchanged.                          │
└──────────────────────────────────────────────────────────────────────────┘
```

### Component Responsibilities (NEW / MODIFIED only)

| Component | New or Modified | Responsibility | File |
|-----------|-----------------|-----------------|------|
| `internal/presets` | **NEW package** | Postgres-backed presets repository: resolve an active preset by name with client→system fallback; CRUD for the CLI | `internal/presets/repo.go` (to be created), mirrors `internal/clients/repo.go` |
| `cmd/manage-presets` | **NEW binary** | Operator CLI: create system/client presets, list, activate/deactivate | `cmd/manage-presets/main.go` (to be created), mirrors `cmd/manage-clients/main.go` |
| `internal/api/api.go` | **MODIFIED** | Add `PresetRepo` interface (interface-segregated, like `Repo`/`Storage`/`Enqueuer`); add `presets PresetRepo` field + constructor param to `Server`/`NewServer` | `internal/api/api.go:16-34, 52-120` |
| `internal/api/handlers.go` | **MODIFIED** | `handleCreateJob` gains a preset-resolution branch that runs BEFORE format-pair validation and feeds the SAME `ParseDocOpts`/`ParseHTMLOpts` validation path | `internal/api/handlers.go:78-391` |
| `internal/jobs` | **MODIFIED** | `CreateParams` gains `PresetName string` / `PresetVersion int`; `Repo.Create`'s INSERT gains 2 columns (already exist in DDL) | `internal/jobs/jobs.go`, `internal/jobs/repo.go:63-131` |
| `cmd/document-worker/main.go`, `cmd/chromium-worker/main.go` | **MODIFIED (debt)** | Remove dead `webhook.NewRepo`/`webhook.NewDeliverer`/signing-secret wiring passed into `worker.NewHandler` — webhook delivery lives solely in `cmd/webhook-worker` since Phase 16 | `cmd/document-worker/main.go:26,55,70-71`, `cmd/chromium-worker/main.go` (same lines) |
| `internal/reconciler/reconciler_test.go` | **MODIFIED (debt)** | Add a mutex around `fakeEnqueuer`'s call-recording slices/fields — currently a data race under `go test -race` | `internal/reconciler/reconciler_test.go:81-111` |
| `internal/e2e` | **MODIFIED (debt)** | Add `TestImageConversionE2E`, mirroring `TestDocumentConversionE2E` (`e2e_test.go:342`) / `TestHTMLConversionE2E` (`:800`) — closes the last gap in the E2E matrix before CI's live-e2e tier depends on full coverage | `internal/e2e/e2e_test.go` |
| `.github/workflows/ci.yml` | **NEW file** | 4-tier CI: gate → race → docker-build → e2e-live | `.github/workflows/ci.yml` (to be created) |

## Recommended Project Structure

```
internal/
  presets/                  # NEW — mirrors internal/clients exactly
    presets.go               # Preset struct, scope/operation string consts (mirror jobs.go's status consts)
    repo.go                  # Repo{pool}, NewRepo, GetActiveByNameAndClient, Create, List, SetActive
    repo_test.go              # unit tests against a real pgx pool (mirrors clients/jobs test style)
cmd/
  manage-presets/
    main.go                  # subcommand CLI: create-system / create-client / list / activate / deactivate
.github/
  workflows/
    ci.yml                   # single workflow, 4 jobs (gate, race, docker-build, e2e-live) with `needs:`
```

### Structure Rationale

- **`internal/presets/` as its own package, not folded into `internal/jobs`:** presets are a
  distinct entity with their own table, own repo, own CLI, and their own lifecycle
  (system/user scope, versioning, activation) — this exactly matches why `internal/clients`
  is its own package instead of living inside `internal/jobs` (auth/client identity vs. job
  lifecycle are different concerns owned by different repos). `internal/jobs` should gain
  only the two new columns on the existing `Job`/`CreateParams` structs — it must NOT import
  `internal/presets` (jobs stays the single source of truth for job state; presets stay a
  read-only, resolved-once-at-creation-time input to that state, same relationship storage
  and queue already have to jobs).
- **`cmd/manage-presets` mirrors `cmd/manage-clients` file-for-file:** same `os.Args`-based
  subcommand dispatch, same "connect to Postgres directly, no HTTP/auth layer" pattern (an
  operator CLI, not a client-facing surface), same fail-fast `log.Fatalf` on `db.Connect`
  error. This is a deliberate case where copying the existing pattern's shape (not the
  literal keys code) is lower-risk than inventing a new CLI idiom.
- **Single `.github/workflows/ci.yml`, not four files:** the four tiers are strictly
  sequential/dependent (see Build Order below), not independently-triggered workflows. A
  single file with `needs:` job dependencies gives one place to read the whole pipeline,
  and branch-protection required-checks configuration only has to reference job names inside
  one workflow rather than coordinating `workflow_run` triggers across files (which is more
  fragile — cross-workflow triggers only fire off the DEFAULT branch's workflow definition,
  a common gotcha with the multi-file approach on PRs from branches).

## Architectural Patterns

### Pattern 1: Preset resolution as a pre-validation stage (extends the existing validated-opts pipeline)

**What:** `preset=<name>` is a THIRD form field alongside `target`/`opts`, mutually exclusive
with them. When present, it resolves — via a narrow `PresetRepo` interface — to a
`(target_format, options)` pair which is then fed into the *exact same* downstream code paths
(`convert.Default.EngineFor`, `convert.ParseDocOpts`/`ParseHTMLOpts`,
`ValidateApplicability`/`ValidateHTMLApplicability`) that already exist for manual
`target`+`opts` requests. No new validation logic is invented — the preset is just an
alternate SOURCE of the same two already-validated fields.

**When to use:** Any time a client wants server-stored, named defaults instead of repeating
`target`+`opts` on every request. This is the standard "template/preset resolves to concrete
parameters, then re-enters the normal validation pipeline" shape — the same shape
`DocOptsFromMap` (`internal/convert/opts.go:113-122`) already uses to re-validate
already-persisted `jobs.options` on the worker's read path. Preset resolution is that same
"never trust a persisted value without re-parsing it through the strict parser" discipline,
applied to `presets.options` instead of `jobs.options`.

**Trade-offs:** Slightly more branching in `handleCreateJob`, but it reuses 100% of the
existing fail-closed opts machinery — zero new attack surface for opts injection, because
`ParseDocOpts`/`ParseHTMLOpts` never trust the caller regardless of whether the raw JSON came
from the multipart form or from a `jsonb` column.

**Where it plugs in (exact insertion point in `internal/api/handlers.go`):**

```
1. ParseMultipartForm                                    (unchanged)
2. read `target` AND `preset` form fields                (MODIFIED: was target-only)
3. IF both target and preset are set        → 400 (ambiguous, fail closed, no silent precedence)
   IF neither is set                        → 400 "missing target format" (unchanged message)
4. read file, header, declared source-from-extension       (unchanged)
5. resolve client from context                              (unchanged — already before content
                                                              detection, needed for preset lookup too)
6. IF preset is set:
     row, err := s.presets.GetActiveByNameAndClient(ctx, client.ID, presetName)
     - not found / inactive / operation != "convert"       → 422 "unknown or inactive preset"
     - target = convert.NormalizeFormat(row.TargetFormat)
     - presetOptsMap = row.Options   (map[string]any, already unmarshaled from jsonb)
     IF the raw `opts` form field is ALSO non-empty         → 400 (ambiguous, fail closed —
                                                              preset governs opts exclusively,
                                                              no merge)
7. Sniff / SniffContainer / LooksLikeHTML / IsOLECFB content detection    (UNCHANGED —
                                                              preset never supplies source
                                                              format; detected content is
                                                              still the sole source of truth)
8. engine, ok := convert.Default.EngineFor(detected, target)              (UNCHANGED CALL —
                                                              target is now preset-resolved
                                                              when applicable; this IS the
                                                              "format-pair validation" the
                                                              preset must resolve before)
9. dimension check                                                        (unchanged)
10. callback_url validation                                               (unchanged)
11. opts parsing:
      IF preset was used: rawOpts source = json.Marshal(presetOptsMap)
      ELSE:                rawOpts source = r.FormValue(formFieldOpts)     (existing)
      → same engine-keyed switch: ParseDocOpts/ParseHTMLOpts + ValidateApplicability/
        ValidateHTMLApplicability                                          (UNCHANGED CALLS)
12. storage.Upload, repo.Create (now also persists PresetName/PresetVersion),
    engine-queue enqueue                                                   (repo.Create only
                                                              MODIFIED to add 2 columns)
```

This ordering satisfies the requirement precisely: preset resolves to `target_format`+`opts`
BEFORE the `EngineFor` format-pair check (step 8), and the resolved opts flow through the
identical `ParseDocOpts`/`ParseHTMLOpts` fail-closed validators (step 11) — never a
preset-specific bypass.

**Example (Go, illustrative — not exact final code):**
```go
target := convert.NormalizeFormat(r.FormValue(formFieldTarget))
presetName := r.FormValue(formFieldPreset)
if target != "" && presetName != "" {
    writeError(w, http.StatusBadRequest, "specify either target or preset, not both")
    return
}
// ... file, source, client resolution unchanged ...

var presetOpts map[string]any
if presetName != "" {
    p, err := s.presets.GetActiveByNameAndClient(ctx, client.ID, presetName)
    if err != nil || p == nil || p.Operation != operationConv || p.TargetFormat == "" {
        writeError(w, http.StatusUnprocessableEntity, "unknown or inactive preset: "+presetName)
        return
    }
    if r.FormValue(formFieldOpts) != "" {
        writeError(w, http.StatusBadRequest, "opts is not allowed together with preset")
        return
    }
    target = convert.NormalizeFormat(p.TargetFormat)
    presetOpts = p.Options
}
if target == "" {
    writeError(w, http.StatusBadRequest, "missing target format or preset")
    return
}
// ... content detection, EngineFor(detected, target) unchanged ...
// opts stage: if presetName != "", marshal presetOpts and feed it through the
// SAME ParseDocOpts/ParseHTMLOpts + ValidateApplicability calls that already
// exist for the manual-opts branch.
```

### Pattern 2: Repo interface shape — `GetActiveByNameAndClient` with system-fallback query

**What:** A single SQL query resolves preset lookup with client-scope taking priority over
system-scope, using an ORDER BY trick rather than two round-trips:

```sql
SELECT id, name, version, scope, client_id, operation, target_format, options, description
FROM presets
WHERE name = $1
  AND is_active = true
  AND ((scope = 'user' AND client_id = $2) OR scope = 'system')
ORDER BY (scope = 'user') DESC, version DESC
LIMIT 1
```

`ORDER BY (scope = 'user') DESC` sorts true-before-false in Postgres, so a matching
user-scoped preset always outranks a same-named system-scoped one in a single query; `version
DESC` is the tiebreaker if more than one row is (accidentally) `is_active` for the same
scope/name — a defensive ORDER BY, not a substitute for the CLI enforcing single-active-version
per name (see Anti-Patterns below).

**When to use:** Exactly this one lookup — the per-request hot path inside `handleCreateJob`.
Any other preset access (CLI listing, admin views) can use simpler, unindexed queries since
they aren't per-request.

**Trade-offs:** The boolean-in-ORDER-BY idiom is a well-known Postgres pattern but reads as
"clever" to anyone unfamiliar with it — worth a one-line comment at the call site (matches
the codebase's convention of explaining non-obvious "why" decisions inline, per
`internal/convert/exec.go`'s process-group-kill comment style).

**Example (Go):**
```go
// Preset mirrors a subset of the presets table row.
type Preset struct {
    ID           uuid.UUID
    Name         string
    Version      int
    Scope        string // "system" | "user"
    ClientID     uuid.UUID
    Operation    string
    TargetFormat string
    Options      map[string]any
    Description  string
}

// Repo is the presets repository backed by a pgx pool.
type Repo struct{ pool *pgxpool.Pool }

func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

// GetActiveByNameAndClient resolves an active preset by name, preferring a
// client-scoped preset over a same-named system-scoped one (client-scope wins).
func (r *Repo) GetActiveByNameAndClient(ctx context.Context, clientID uuid.UUID, name string) (*Preset, error) {
    // ... query above, Scan into Preset, json.Unmarshal options jsonb ...
}
```

### Pattern 3: CI as a single tiered workflow (`needs:` job graph, not multiple workflow files)

**What:** One `.github/workflows/ci.yml`, triggered on `push` and `pull_request`, with four
jobs chained by `needs:`:

```yaml
jobs:
  gate:
    # gofmt -l ., go vet ./..., go build ./..., go test ./...
    # internal/e2e self-skips (E2E_BASE_URL unset) — this tier stays fast & offline-safe.
  race:
    needs: gate
    # go test -race ./...
  docker-build:
    needs: race
    # docker build for Dockerfile.api / .worker / .document-worker / .chromium-worker /
    # .webhook-worker (5 images — Dockerfile.worker-test is a dev/CI-support image for a
    # separate soffice-gated local test flow, NOT one of the 5 deployment images)
  e2e-live:
    needs: docker-build
    # docker compose -f docker-compose.yml -f docker-compose.e2e.yml up -d --build
    # wait for healthchecks, then go test ./internal/e2e/... with the documented env
    # contract; docker compose down -v in an always()-guarded teardown step
```

**When to use:** Any project whose CI tiers are strictly escalating in cost/duration and
strictly dependent (a later tier only makes sense if the earlier one passed) — exactly this
project's stated tier order (gate → race → docker-build → e2e). Independent/parallel checks
(e.g., a separate lint-only job that doesn't gate anything else) would instead be siblings
with no `needs:`, but none of the four tiers here are independent of each other.

**Trade-offs:** A single workflow file means one YAML file grows to ~80-120 lines instead of
four ~20-line files — acceptable given the project's existing "no fragmented tooling"
convention (no Makefile, no separate lint config, one `docker-compose.yml`). The main
downside — a change to `docker-build` triggers `gate`+`race` to (harmlessly) re-run even if
Go code didn't change — is not a real cost here since ALL pushes/PRs must pass ALL tiers
anyway per the milestone's own requirement ("Каждый push проверяется автоматически вплоть до
живого E2E").

## Data Flow

### Preset resolution flow (new)

```
POST /v1/jobs  (multipart: file, preset=<name>, [callback_url])
    ↓
handleCreateJob: parse form → read target XOR preset
    ↓ (preset branch)
s.presets.GetActiveByNameAndClient(ctx, client.ID, name)
    ↓ (found, active, operation=="convert")
target = preset.TargetFormat ; presetOpts = preset.Options
    ↓
[UNCHANGED] content sniff → detected format
    ↓
[UNCHANGED] convert.Default.EngineFor(detected, target)  — format-pair validation
    ↓
[UNCHANGED, fed from presetOpts instead of r.FormValue] ParseDocOpts/ParseHTMLOpts
    + ValidateApplicability/ValidateHTMLApplicability
    ↓
[UNCHANGED] storage.Upload
    ↓
[MODIFIED] repo.Create(ctx, jobs.CreateParams{ ..., PresetName: name, PresetVersion: preset.Version })
    → INSERT INTO jobs (..., preset_name, preset_version, ...) — columns already exist,
      unused since Phase 1 (0001_init.sql:53-54)
    ↓
[UNCHANGED] engine-class enqueue (image/document/html queue)
```

### CI flow (new, per push/PR)

```
git push / PR opened
    ↓
GitHub Actions: ci.yml triggered
    ↓
gate  (gofmt, vet, build, unit test — offline, ~1-2 min)
    ↓ (needs: gate)
race  (go test -race ./... — requires fakeEnqueuer mutex fix, else flaky/red)
    ↓ (needs: race)
docker-build  (5 images: api, worker, document-worker, chromium-worker, webhook-worker)
    ↓ (needs: docker-build)
e2e-live  (docker compose up -d --build using docker-compose.yml + docker-compose.e2e.yml,
           wait for healthchecks, go test ./internal/e2e/... — requires the new
           TestImageConversionE2E to exist for full-matrix coverage, else the tier is green
           but blind to the image/libvips pipeline)
    ↓
docker compose down -v  (always()-guarded teardown)
```

### Key Data Flows

1. **Preset provenance on the job row:** `jobs.preset_name`/`jobs.preset_version` are written
   at creation time from the RESOLVED preset row, not re-looked-up later — mirrors the
   existing "resolve once at creation, never re-resolve mid-flight" discipline already used
   for `engine`/`source_format`/`target_format` (the worker re-reads these from Postgres, not
   from the presets table, so a preset later being deactivated/edited never retroactively
   changes an in-flight or historical job's behavior — the job's `options` column already
   holds the fully-resolved, validated opts snapshot, same as today).
2. **CI env contract is 100% inherited, not reinvented:** the `e2e-live` job sets no new
   environment variables beyond what `docker-compose.yml`'s `api`/`webhook-worker-*` services
   and `internal/e2e/e2e_test.go`'s documented contract already define — `E2E_BASE_URL`,
   `DATABASE_URL`, `API_KEY_SALT` (must equal the `api` service's `API_KEY_SALT` value,
   `"dev-only-change-me-in-real-deploys"`), and `E2E_S3_DIAL_ADDR=127.0.0.1:9100` (since the
   presigned URL host `minio:9000` isn't resolvable from the GitHub Actions runner, same
   reason local dev needs it). `WEBHOOK_SIGNING_SECRET` should also be set to match
   `webhook-worker-1/-2`'s value so the HMAC signature gets verified, not just
   asserted-non-empty (stronger CI coverage than the minimum required by the test file).

## Scaling Considerations

Not meaningfully relevant to this milestone — CI/presets/debt-cleanup don't change runtime
scaling characteristics. One CI-specific note:

| Concern | Now (v1.4) | Later |
|---------|-----------|-------|
| CI job duration | 4 sequential tiers on every push; e2e-live (`docker compose up --build` + healthcheck wait + live conversions) is the dominant cost, likely 3-6 min | If this becomes a bottleneck, cache Docker layers (`actions/cache` or GHA's built-in Docker layer caching) before splitting anything — do not prematurely parallelize docker-build's 5 images into a matrix until build time is actually measured |
| Preset table read load | One extra indexed `SELECT` per job creation when `preset` is used — negligible; `presets_system_uq`/`presets_user_uq` indexes already exist for exact-match lookups, though the fallback query above doesn't hit them directly (it does `(scope='user' AND client_id=$2) OR scope='system'`, not an indexed equality on both branches) | If preset-based job creation becomes a large fraction of traffic, add a targeted index e.g. `CREATE INDEX presets_active_name_idx ON presets (name) WHERE is_active` to speed the fallback query — not needed at internal-client-service load levels |

## Anti-Patterns

### Anti-Pattern 1: Re-implementing opts validation for preset-sourced options

**What people do:** Write a preset-specific opts validator (or worse, trust
`preset.Options` as already-safe because "an operator created it") instead of routing it
through `ParseDocOpts`/`ParseHTMLOpts`.
**Why it's wrong:** Breaks the project's single-validation-authority invariant
(`internal/convert` owns ALL opts validation, D-04/D-10 per existing handler comments); a
preset could be created before a `DocOpts`/`HTMLOpts` schema change and become stale/invalid,
or a future preset-editing surface could let a compromised operator credential smuggle
unvalidated JSON straight to the engine's argv (the exact injection class Phase 14 closed).
**Do this instead:** Feed `json.Marshal(preset.Options)` through the identical
`ParseDocOpts`/`ParseHTMLOpts` + `ValidateApplicability`/`ValidateHTMLApplicability` call
sites already in `handleCreateJob` — the same re-validate-on-every-read discipline
`DocOptsFromMap` already applies to `jobs.options` on the worker side.

### Anti-Pattern 2: Silently merging `target`/`opts` form fields with a `preset`

**What people do:** Let `preset` supply defaults and let an explicit `opts`/`target` field
"override" or "merge with" the preset, to be permissive.
**Why it's wrong:** Ambiguous precedence is exactly the shape of bug this codebase
consistently rejects elsewhere (e.g., the declared-extension-vs-detected-content mismatch is
a hard 422, never an auto-correction, per D-01/D-04 in `handlers.go`). A silent-merge preset
would make it impossible to reason about what a job actually ran with from the API contract
alone.
**Do this instead:** Treat `preset` and `target`/`opts` as mutually exclusive request shapes;
supplying both is a 400, full stop — mirrors the fail-closed-on-ambiguous-input pattern
already used throughout `handleCreateJob`.

### Anti-Pattern 3: Adding a new migration for preset provenance on `jobs`

**What people do:** Add `0002_add_preset_columns.sql` to store which preset produced a job.
**Why it's wrong:** Unnecessary — `jobs.preset_name text` and `jobs.preset_version int` already
exist in `0001_init.sql:53-54`, dormant since the schema was first written (`PROJECT.md`
explicitly notes `presets` "остаются неиспользуемыми" — this milestone activates existing
schema, it doesn't extend it). A new migration here would be pure debt.
**Do this instead:** Extend `jobs.CreateParams` with `PresetName`/`PresetVersion` fields and
add the two columns to `Repo.Create`'s existing `INSERT` statement
(`internal/jobs/repo.go:102-108`) — a Go-only change, zero new SQL migrations.

### Anti-Pattern 4: Assuming CI workflow YAML alone gates merges

**What people do:** Merge `.github/workflows/ci.yml` and consider "CI blocks bad merges" done.
**Why it's wrong:** A workflow running is not the same as a workflow being REQUIRED — without
a branch protection rule on `main` naming the job(s) as required status checks, a red CI run
does not block a merge button click; this is a GitHub repo-settings action, not something
expressible inside the workflow file itself.
**Do this instead:** After the workflow first lands and goes green once, configure branch
protection on `main` (via GitHub UI or `gh api repos/{owner}/{repo}/branches/main/protection`)
to require at least the `gate` job (and ideally `race`) as a required status check before
merge. This is an explicit manual/operational follow-up step, not a code-phase deliverable —
flag it so the roadmap doesn't silently assume the workflow file alone is sufficient.

### Anti-Pattern 5: Two application-level active-preset invariants with no DB enforcement

**What people do:** Rely purely on `cmd/manage-presets` application logic to keep "at most one
`is_active=true` row per (scope, client, name)" true, with no DB constraint, and assume it'll
always hold.
**Why it's wrong:** The existing unique indexes (`presets_system_uq`, `presets_user_uq`) are on
`(name, version)`/`(client_id, name, version)`, NOT on `is_active` — nothing in the DDL
prevents two different versions of the same preset both being `is_active=true`
simultaneously. This is the same class of risk the codebase already accepts elsewhere (e.g.
`clients.AddSecondaryKey`'s two-active-key-slot cap is enforced in Go, not a DB constraint) —
consistent with project convention, but worth stating explicitly rather than assuming the
`ORDER BY version DESC LIMIT 1` fallback in `GetActiveByNameAndClient` is a correctness
guarantee rather than a defensive tiebreaker.
**Do this instead:** `cmd/manage-presets`'s "activate" subcommand should transactionally
deactivate any other `is_active=true` row for the same `(scope, client_id, name)` before
setting the new one active — mirrors `AddSecondaryKey`'s guarded, transaction-wrapped,
lock-then-check-then-write shape (`internal/clients/repo.go:64-90`). This keeps the invariant
true by construction in the one place that mutates it, without needing a new partial unique
index migration.

## Integration Points

### External Services

| Service | Integration Pattern | Notes |
|---------|---------------------|-------|
| GitHub Actions (ubuntu-latest runner) | Standard hosted runner; Docker Engine + `docker compose` v2 built in | `host.docker.internal:host-gateway` (used by `docker-compose.e2e.yml`'s `extra_hosts`) works unchanged on GitHub-hosted Linux runners — same Docker Engine `--add-host` mechanism as local Docker, not a Docker-Desktop-only feature |
| No new external services | — | CI needs no cloud secrets: Postgres/Redis/MinIO/API-key-salt/webhook-signing-secret are all dev-only literal values already committed in `docker-compose.yml`/`docker-compose.e2e.yml` — reused as-is in CI, no GitHub encrypted secrets required for this milestone |

### Internal Boundaries

| Boundary | Communication | Notes |
|----------|---------------|-------|
| `internal/api` ↔ `internal/presets` | Narrow `PresetRepo` interface (interface segregation, matching existing `Repo`/`Storage`/`Enqueuer`) | `internal/api` must never import the concrete `*presets.Repo` type — same discipline as its existing `Repo`/`Storage`/`Enqueuer` interfaces |
| `internal/presets` ↔ `internal/jobs` | **None** — no import either direction | Presets are resolved ONCE at job-creation time in the API layer; the resolved `target_format`+`options` are copied into `jobs.CreateParams` as plain fields. `internal/jobs` never needs to know presets exist |
| `cmd/manage-presets` ↔ `internal/presets`, `internal/db` | Direct (operator CLI, no HTTP/auth layer) | Mirrors `cmd/manage-clients` exactly — connects to Postgres directly, no `internal/auth` dependency |
| `.github/workflows/ci.yml` ↔ `docker-compose.yml` + `docker-compose.e2e.yml` | Shell steps (`docker compose -f ... -f ... up -d --build`), not a Go/API boundary | CI adds no new compose files or service definitions — it is purely a consumer of the existing two files, unmodified |
| `cmd/document-worker`/`cmd/chromium-worker` ↔ `internal/webhook` | **REMOVED** (debt fix) | These two binaries currently construct `webhook.NewRepo`/`webhook.NewDeliverer` and pass them into `worker.NewHandler` even though they're inert (webhook delivery lives solely in `cmd/webhook-worker` since Phase 16) — the fix deletes this dead wiring; verify whether `worker.NewHandler`'s constructor signature still needs the webhook-repo/deliverer params for the image/document/HTML handlers it also builds, or whether they can become optional/nil-safe as part of this cleanup, before deleting the call sites |

## Suggested Build Order

Given the milestone's own stated dependency constraints (`-race`-fix before the `-race` CI
tier; image E2E before the live-E2E CI tier) plus the fact that presets and CI are otherwise
fully independent of each other:

**1. Debt cleanup first** (small, isolated, and two of its three items are hard prerequisites
   for parts of tier 3 below):
   - 1a. Remove dead webhook wiring from `cmd/document-worker/main.go` +
     `cmd/chromium-worker/main.go` — zero dependencies on anything else in this milestone,
     do it any time, lowest risk item.
   - 1b. Add the mutex to `fakeEnqueuer` in `internal/reconciler/reconciler_test.go` — **must
     land before the CI workflow's `race` job is added/enabled**, otherwise the first CI run
     is red (or worse, silently flaky) on tier 2.
   - 1c. Add `TestImageConversionE2E` to `internal/e2e` — **must land before the CI workflow's
     `e2e-live` job is added/enabled**, otherwise the first live-E2E CI run is green but blind
     to the image/libvips pipeline (a false sense of full coverage).

**2. Presets second** (fully independent of CI and of the debt items — no code-level
   dependency in either direction):
   - `internal/presets` package → `cmd/manage-presets` CLI → `internal/api` wiring
     (`PresetRepo` interface, `handleCreateJob` preset-resolution branch, `jobs.CreateParams`
     extension). This can technically be built in parallel with step 1, but sequencing it
     after debt cleanup keeps the diff surface on `cmd/document-worker`/`chromium-worker`/
     `internal/reconciler`/`internal/e2e` small and isolated from unrelated preset changes.

**3. CI workflow last:**
   - Build `.github/workflows/ci.yml`'s four tiers (`gate`, `race`, `docker-build`,
     `e2e-live`) only after steps 1b and 1c have merged, so the workflow is green on its very
     first run instead of needing an immediate follow-up fix. Building CI last also means its
     `docker-build`/`e2e-live` tiers exercise the FINAL v1.4 codebase (presets included) from
     day one, rather than validating only a partial slice.
   - Within this step, the four jobs are naturally tiered via `needs:` as described in
     Pattern 3 — that internal ordering is a single-workflow implementation detail, not a
     cross-feature build-order concern.

## Sources

- Direct repository inspection (all findings HIGH confidence, verified against current
  source — no external/ecosystem claims in this research):
  - `/Users/apaderin/dev/octoconv/internal/api/handlers.go` (handleCreateJob flow, exact line
    numbers for each validation stage)
  - `/Users/apaderin/dev/octoconv/internal/api/api.go` (interface segregation pattern:
    `Repo`/`Storage`/`Enqueuer`/`Pinger`)
  - `/Users/apaderin/dev/octoconv/internal/jobs/repo.go` (repo pattern, guarded transitions,
    `Create`'s INSERT shape)
  - `/Users/apaderin/dev/octoconv/internal/clients/repo.go` (repo pattern to mirror for
    presets; `AddSecondaryKey`'s guarded single-active-slot enforcement)
  - `/Users/apaderin/dev/octoconv/cmd/manage-clients/main.go` (CLI pattern to mirror for
    `cmd/manage-presets`)
  - `/Users/apaderin/dev/octoconv/internal/db/migrations/0001_init.sql` (confirms `presets`
    table and `jobs.preset_name`/`jobs.preset_version` columns already exist, unused)
  - `/Users/apaderin/dev/octoconv/internal/convert/opts.go` (`ParseDocOpts`,
    `DocOptsFromMap`'s re-validate-on-read pattern — the template for preset opts
    re-validation)
  - `/Users/apaderin/dev/octoconv/internal/queue/queue.go` (`Enqueuer` shape, per-engine-class
    queue routing pattern referenced for context)
  - `/Users/apaderin/dev/octoconv/cmd/document-worker/main.go` (confirmed dead webhook wiring
    at lines 26, 50-55, 70-71; identical shape in `cmd/chromium-worker/main.go`)
  - `/Users/apaderin/dev/octoconv/internal/reconciler/reconciler_test.go` (confirmed
    unsynchronized `fakeEnqueuer` shared-state fields, lines 81-111)
  - `/Users/apaderin/dev/octoconv/internal/e2e/e2e_test.go` (confirmed env-gated E2E harness
    contract; confirmed no `TestImageConversionE2E` exists yet — only Document/Cross-Format/
    OLE-CFB/PDF-A/Opts-Rejection/HTML tests)
  - `/Users/apaderin/dev/octoconv/docker-compose.yml` +
    `/Users/apaderin/dev/octoconv/docker-compose.e2e.yml` (confirmed 5 deployment Dockerfiles
    vs. `Dockerfile.worker-test`'s separate dev/CI-support role; confirmed dev-only secrets
    already committed, no new CI secret store needed)
  - `/Users/apaderin/dev/octoconv/.planning/PROJECT.md` (milestone scope, prior context on
    `presets` being dormant schema, explicit `-race`-before-CI and image-E2E-before-live-E2E
    ordering constraints already stated by the project owner)

---
*Architecture research for: OctoConv v1.4 (CI pipeline, presets, debt cleanup)*
*Researched: 2026-07-12*
