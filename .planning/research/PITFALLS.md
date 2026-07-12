# Pitfalls Research

**Domain:** Adding GitHub Actions CI (with a live docker-compose E2E tier) and a server-side presets mechanism to an existing production-hardened Go conversion service (OctoConv v1.4)
**Researched:** 2026-07-12
**Confidence:** MEDIUM-HIGH (GitHub Actions runner limits verified via current official sources; presets pitfalls derived directly from this repo's existing DDL/code conventions, HIGH confidence; general CI flakiness patterns MEDIUM — verified against official docs where cited)

This supersedes the previous milestone's PITFALLS.md (v1.3, Document Class v2 — LibreOffice cross-format, OLE-CFB, PDF/A, chromium HTML→PDF, webhook decoupling — all shipped and validated per `.planning/PROJECT.md`). This research is scoped entirely to v1.4's three feature areas: GitHub Actions CI (4 levels culminating in live E2E), a server-side presets mechanism resolved into the existing validated-opts pipeline, and the v1.3 tech-debt cleanup that gates the CI `-race` tier.

## Critical Pitfalls

### Pitfall 1: Runner disk exhaustion from 5 image builds — already happened locally, will happen in CI too

**What goes wrong:**
The stack builds 5 custom images (`Dockerfile.api`, `Dockerfile.worker`, `Dockerfile.document-worker`, `Dockerfile.webhook-worker`, `Dockerfile.chromium-worker`) on top of 5 pulled base images (`postgres:18`, `redis:8`, `minio/minio:latest`, `minio/mc:latest`, `hibiken/asynqmon:0.7.2`), where `document-worker` bundles LibreOffice and `chromium-worker` bundles a Chromium headless runtime — both multi-hundred-MB-to-1GB+ payloads on top of `golang:1.26-bookworm` build stages. A standard GitHub-hosted `ubuntu-latest` runner guarantees only **14GB of usable disk** (roughly 22GB free before any workflow step, per GitHub's own runner-images docs) — materially less than a typical developer's Docker Desktop VM disk. This project already hit "no space left on device" building this exact stack **locally** during Phase 13 Plan 03 (see `13-03-SUMMARY.md`, Docker Desktop VM disk exhaustion mid-build, resolved twice via `docker builder prune -a` / `docker image prune -a`). A CI runner with less headroom than a dev laptop will hit this reliably, not occasionally, unless mitigated up front.

**Why it happens:**
`docker compose build`/`up --build` keeps every intermediate layer and every base image resident; nothing is pruned between steps by default. Teams assume "it built fine on my machine" transfers to CI runners, which have less disk and no prior layer cache.

**How to avoid:**
- Add an explicit disk-space step before the E2E build tier: either `docker system prune -af --volumes` right before building (cheap, safe on an ephemeral runner with nothing else on it) or a marketplace action (e.g. `jlumbroso/free-disk-space`) to reclaim the ~10-15GB GitHub pre-installs (Android SDK, .NET, Haskell toolchains) that a Go+Docker job never uses.
- Build only the 5 images this project needs — do not `docker compose pull` unrelated tags, and pin `minio/minio:latest`/`minio/mc:latest` to specific tags (also closes a supply-chain gap the project already flags for `asynqmon` in `docker-compose.yml`'s own comment about pinned tags).
- Prefer `docker buildx build --load` with registry/gha cache per-image over `docker compose build`'s default local-only cache (see Pitfall 3) — this avoids re-downloading `golang:1.26-bookworm` + `debian:bookworm-slim` + LibreOffice/Chromium apt payloads on every run.
- Tear the stack down and prune between the "build all 5 images" step and any later step in the same job if disk is still tight (`docker compose down -v` frees the compose stack's own layers before a subsequent step needs headroom).

**Warning signs:**
`no space left on device` errors during `apt-get install` inside `document-worker`/`chromium-worker` build stages (this project's exact prior local failure mode) or during `docker buildx` metadata writes; CI jobs that pass on re-run with no code change (classic disk-pressure flake signature).

**Phase to address:**
The CI phase that introduces the "build all 5 Docker images" tier (CI level 3 in the milestone's 4-level design) — disk mitigation must ship in the same phase as the build step, not as a follow-up, since the failure is not hypothetical for this stack.

---

### Pitfall 2: Live E2E tier flakes from healthcheck-vs-readiness gap under slow/loaded runners

**What goes wrong:**
`docker-compose.yml`'s healthchecks (`interval: 5s, timeout: 3s, retries: 10` → ~50s worst-case before Compose considers a dependency healthy) gate `depends_on: condition: service_healthy` for `postgres`/`redis`/`minio`, and the E2E harness's own env-var contract (`E2E_S3_DIAL_ADDR`, `E2E_WEBHOOK_HOST`) further assumes the stack is fully up before the Go test process starts issuing HTTP calls. GitHub-hosted runners are frequently noisier/slower (shared CPU, cold image pulls, LibreOffice/Chromium first-boot cost inside `document-worker`/`chromium-worker`, which have no explicit healthcheck at all in `docker-compose.yml` today) than a developer's local machine. `service_healthy` only covers the 3 infra containers — `api`, `worker`, `document-worker`, `chromium-worker`, `webhook-worker-1/2` have no healthcheck and no guarantee that they've finished their own DB-connect/migrate/queue-bind startup sequence before the E2E suite's first `POST /v1/jobs`.

**Why it happens:**
Healthchecks were written and tuned against local Docker Desktop timing; nobody re-validates the same retry/interval budget against CI runner variance until the first randomly-failing run. Compose's `service_healthy` for infra containers is necessary but not sufficient — it says Postgres/Redis/MinIO are up, not that `api`/`worker`/`document-worker` have finished connecting to them.

**How to avoid:**
- Add lightweight healthchecks (or at minimum a startup-probe loop) to `api` (`GET /healthz`, which already pings Postgres/Redis/S3 per Phase 4's observability work) and gate the E2E test invocation on that endpoint returning `200` — not just on Compose's `service_healthy` for the 3 infra containers.
- Move stack-readiness polling into the test harness itself (or a pre-test shell step) with an explicit, generous timeout and visible progress (`curl --retry N --retry-connrefused` against `/healthz`) rather than trusting Compose's fixed `retries: 10` budget to be enough headroom on a loaded runner — this project's own E2E summaries already do a manual `curl -fsS http://localhost:8090/healthz` before running tests (see `13-03-SUMMARY.md`); formalize that as a CI step, don't rely on implicit timing.
- Do not shrink retries defensively to "make it pass faster" — widen them for CI headroom instead; CI minutes are cheaper than flaky-test debugging time.

**Warning signs:**
Intermittent `connection refused` on the first E2E HTTP call that passes on retry; E2E job failing only on CI, never locally; failures clustering on cold-cache runs (first run of the day, or after a base-image bump) rather than warm-cache runs.

**Phase to address:**
Same CI phase that wires the live E2E tier into GitHub Actions — the readiness-polling step should ship alongside the workflow file, not be discovered later via flaky-test triage.

---

### Pitfall 3: `docker compose build` silently bypasses the GitHub Actions cache backend

**What goes wrong:**
Teams wire up `docker/setup-buildx-action` and expect `docker compose build`/`up --build` to automatically use the `type=gha` cache backend. It does not — `docker compose build` uses the classic builder or a local buildx driver without cache-to/cache-from flags unless each service's build section is explicitly configured with `cache_from`/`cache_to`, or the images are built individually via `docker buildx bake`/`docker buildx build` before `docker compose up` (with matching image tags so Compose reuses the just-built image instead of rebuilding). Result: every CI run re-executes the full `apt-get install libreoffice...`/chromium download from scratch, burning the disk budget from Pitfall 1 *and* run time on every single push, with the GHA cache silently doing nothing.

**Why it happens:**
`docker buildx bake` (which does understand a compose file's `build:` sections and *does* support `--set *.cache-to=type=gha`) is easy to conflate with plain `docker compose build` — the two have overlapping syntax but different cache wiring, and the distinction is easy to skim past.

**How to avoid:**
- Use `docker buildx bake -f docker-compose.yml --set '*.cache-from=type=gha' --set '*.cache-to=type=gha,mode=max'` (or build each of the 5 images individually via `docker/build-push-action` with per-image `cache-from`/`cache-to: type=gha,scope=<service-name>`) instead of `docker compose build`.
- Give each of the 5 images its own `scope` in the gha cache key (`scope=api`, `scope=worker`, `scope=document-worker`, `scope=chromium-worker`, `scope=webhook-worker`) — without per-service scoping, 5 images sharing one cache scope will thrash each other out under the 10GB budget (see Pitfall 4).
- Verify the cache is actually hitting: a rebuild with no Dockerfile changes should show `CACHED` on nearly every layer in the buildx log; if it doesn't, the wiring is wrong, not the cache.

**Warning signs:**
CI build times for the "build all 5 images" step stay constant (~same as a cold local build) across consecutive runs with no dependency/Dockerfile changes; `docker buildx build` logs show no `CACHED` layers on a second run.

**Phase to address:**
The CI phase introducing the Docker-image-build tier — cache wiring should be validated (two consecutive runs, compare timings) before that phase is marked done, not left as a later optimization.

---

### Pitfall 4: 10GB GHA cache budget can't hold 5 images' worth of LibreOffice/Chromium layers, causing cache thrashing

**What goes wrong:**
GitHub's Actions cache has a **default 10GB-per-repository budget with LRU eviction** (GitHub added an option in late 2025 to raise this ceiling, but it is not the default — verify the repo's cache settings rather than assuming extra headroom). `mode=max` gha caching (needed to cache intermediate `apt-get install` layers, not just final image layers) can easily produce several GB per image once LibreOffice and Chromium's system dependencies are included. With 5 images sharing one repo-wide 10GB budget, the *first* eviction under pressure will be the least-recently-used image's cache — not necessarily the least important one — leading to cache misses that look random from a workflow-log perspective ("why did document-worker rebuild from scratch this time but not last time?").

**Why it happens:**
The 10GB limit is a repository-wide, not per-workflow or per-image, budget; teams size caching decisions per-image without accounting for the shared ceiling.

**How to avoid:**
- Use `mode=min` (only final-stage layers) for the base `debian:bookworm-slim` runtime stages of `document-worker`/`chromium-worker` where the heavy apt payload already lives in the final image anyway, and reserve `mode=max` for the Go builder stage (`golang:1.26-bookworm`, sharing an identical build pattern across all 5 images) — that's the layer most worth deduping.
- Consider registry-based caching (`cache-to=type=registry,ref=<ghcr>/octoconv-cache:<service>`) instead of `type=gha` if the 10GB ceiling proves too tight in practice — registry caching has no comparable size ceiling tied to the Actions cache quota.
- Monitor: GitHub's cache usage UI (Settings → Actions → Caches) shows current usage; check it after the first few CI runs post-launch rather than assuming 10GB is comfortable headroom for 5 images with LibreOffice/Chromium payloads.

**Warning signs:**
Build times for one or two of the 5 images regress unpredictably run-to-run while others stay fast; cache usage dashboard shows near-100% of the 10GB budget shortly after the CI tier ships.

**Phase to address:**
Same CI phase as Pitfall 3 — cache scoping strategy is a design decision made once, at the time the build-and-cache step is written, not a tuning pass done after the fact.

---

### Pitfall 5: Zombie compose stacks eating runner minutes and destroying diagnostics when a step fails mid-suite

**What goes wrong:**
If `docker compose up -d --build` succeeds and the E2E test step fails (assertion failure, timeout, or a genuine bug), and the workflow has no explicit teardown step, GitHub Actions will still terminate the runner VM at job end — so this project's specific residual risk isn't "the container runs forever," it's (a) the diagnostic record of a still-running stack being lost with no `docker compose logs` capture, and (b) any future move to self-hosted/persistent runners inheriting stale containers/volumes from the failed run, corrupting the next run's Postgres/MinIO state.

**Why it happens:**
Compose teardown steps are written for the happy path (`docker compose down` as the last step) without `if: always()`, so a failing test step causes the workflow to skip straight to job cleanup, silently dropping the teardown (and any diagnostic log dump that would have explained the failure).

**How to avoid:**
- Always pair the "bring the stack up" step with an `if: always()` teardown step (`docker compose -f docker-compose.yml -f docker-compose.e2e.yml down -v`) later in the same job.
- Add an `if: failure()` step that dumps `docker compose logs` (all 8 services: postgres, redis, minio, api, worker, document-worker, chromium-worker, webhook-worker-1/2) as a build artifact before teardown — this project's failure modes (LibreOffice filter mismatches, timeout-vs-retry classification bugs) are exactly the kind that are undiagnosable from the Go test's stdout alone.
- If self-hosted runners are ever introduced (not currently in scope, but worth flagging given `docker-compose.yml`'s heavy resource limits — `chromium-worker` at 2 CPU/2GB, `document-worker`/`worker` at 2 CPU/1GB each — which may exceed GitHub-hosted runners' default 2-core/7GB), always start such jobs with `docker system prune -af` as a hygiene step, since a self-hosted runner accumulates state across jobs in a way GitHub-hosted ephemeral runners do not.

**Warning signs:**
CI minutes consumption climbing without a corresponding increase in run count; failed E2E jobs with no useful log output beyond "test failed" and no compose logs artifact to explain why.

**Phase to address:**
The CI phase introducing the live E2E tier — teardown-with-`if: always()` and failure-log-capture should be in the first version of the workflow file, not retrofitted after the first opaque failure.

---

### Pitfall 6: Superseded runs pile up and starve concurrency-limited runners

**What goes wrong:**
Without a `concurrency` group, every push to a branch (including rapid-fire pushes during active development, which this project's git history shows happens often — multiple commits per day across `main`) queues a full new CI run, including the heaviest tier (5-image build + live E2E). Old runs for commits that are already superseded keep consuming runner slots/minutes even though their result is moot the instant a newer push lands.

**Why it happens:**
`concurrency:` groups are opt-in in GitHub Actions; a first CI pass often omits them since the workflow "works" without one — the cost only becomes visible under real push cadence.

**How to avoid:**
Add a `concurrency: { group: ci-${{ github.workflow }}-${{ github.ref }}, cancel-in-progress: true }` block at the workflow level (or per-job for the heavy E2E job specifically, if base-gate jobs should never be cancelled mid-run). Scope the group to `github.ref` so concurrent PRs against different branches don't cancel each other.

**Warning signs:**
GitHub Actions usage/minutes dashboard showing runs that never reached "complete" (cancelled manually or timed out) alongside runs for commits nobody cares about anymore; PR checks staying "in progress" long after a newer commit was pushed to the same branch.

**Phase to address:**
The CI phase that first introduces the workflow file — concurrency control is a one-line addition, cheapest to include from day one.

---

### Pitfall 7: `-race` + real-wall-clock reconciler soak test pushes past `go test`'s default 10-minute timeout

**What goes wrong:**
`go test`'s default per-package `-timeout` is 10 minutes if not overridden. `-race` instrumentation typically adds 2-10x wall-clock overhead. This project already has `internal/reconciler/reconciler_soak_test.go`, whose own doc comment states it verifies recovery "using REAL elapsed wall-clock time. No SQL backdating of created_at is used" (RECON-05) — a design choice made deliberately to prove production timing behavior, not a mock-clock unit test. While the current soak test's actual sleeps are short (100-150ms, confirmed in code) and the test is `DATABASE_URL`-gated (skips without a live Postgres), the *pattern* is real-time-based and one accidental configuration change (a bumped `RECONCILER_*_STALE_AFTER` test fixture, or a future soak-style test copied from this one) is one step away from silently exceeding the default timeout once `-race` overhead stacks on top — especially if such a test ever leaks into the plain `go test ./...` gate that runs without an explicit `-timeout` override (the existing E2E tier already sets `-timeout 30m` explicitly, per `13-03-SUMMARY.md`'s documented run command — the base/`-race` gate tiers do not appear to, based on this repo's existing test invocations).

**Why it happens:**
`-timeout` defaults are easy to forget because "it passed in under 10 minutes" during development on a fast local machine; `-race`'s overhead and CI runner CPU throttling both push the same suite closer to (or past) that ceiling without any code change.

**How to avoid:**
- Set an explicit `-timeout` flag (e.g. `-timeout 5m` for the fast unit-test/`-race` tier, sized with headroom above the tier's actual measured duration, and the already-established `-timeout 30m` for the live E2E tier) in every `go test` invocation in the CI workflow — never rely on the 10-minute default, since its adequacy is a moving target as `-race` gets enabled and test suites grow.
- Keep real-wall-clock tests (soak-style) `DATABASE_URL`-gated and confined to the live-E2E tier (as `reconciler_soak_test.go` already is) rather than letting them leak into the fast `-race` gate tier — this is already this project's existing pattern; preserve it explicitly as a rule when adding the CI workflow, don't let a future test accidentally add a `DATABASE_URL`-independent real-time wait to the fast tier.

**Warning signs:**
`panic: test timed out after 10m0s` appearing only in the `-race` CI job, never in a plain local `go test ./...` run; CI job duration for the `-race` tier creeping upward release over release without a corresponding growth in test count.

**Phase to address:**
The CI phase that enables `-race` (level 2) — set the explicit timeout at the same time the flag is added, as a preventive measure, not reactively after the first timeout. Note: this phase depends on the tech-debt phase fixing the `fakeEnqueuer` data race first (see Pitfall-to-Phase Mapping) — `-race` cannot be enabled cleanly while that race exists.

---

### Pitfall 8: Preset resolution TOCTOU — preset deactivated/edited between lookup and job-row insert

**What goes wrong:**
`presets` (per `internal/db/migrations/0001_init.sql`) has `is_active boolean` and `version int`, and `jobs` already carries its own `preset_name`/`preset_version`/`options jsonb` columns — meaning the schema's intended design is almost certainly "resolve the preset row into the job's own `options` snapshot at creation time," not "look the preset up again on every worker read." The gap: between `SELECT ... FROM presets WHERE name=$1 AND (client_id=$2 OR scope='system') AND is_active` and the subsequent `INSERT INTO jobs (..., preset_name, preset_version, options)`, an operator could deactivate that preset via `cmd/manage-presets` (presumably mirroring `cmd/manage-clients`' create/revoke pattern) or bump its version. A naive implementation either (a) races silently and sometimes creates a job against an already-deactivated preset, or (b) wraps the whole read+insert in a transaction with row-level locking the presets table was never designed to need (presets are read far more often than clients' API keys, and locking every preset read against every job-creation request would serialize job intake under load — a much worse regression than the race itself).

**Why it happens:**
The codebase's existing guarded-transition pattern (`Repo.transition`, `SELECT ... FOR UPDATE`) trains developers to reach for row-locking as the default answer to any read-then-write race; applying that same heavyweight pattern to a hot read path (every job creation) is a mismatch for a resource (presets) that changes rarely and is read on every request.

**How to avoid:**
- Accept the race as a documented, narrow, acceptable window (matching this project's existing precedent for other narrow, accepted residual risks — e.g. the `file://` residual-read risk accepted in Phase 15) — the practical consequence of "job created against a preset deactivated 50ms earlier" is: one job runs with settings an operator just decided to retire, materially the same outcome as if the deactivation had landed 50ms later. This does **not** need transactional protection; it needs the resolved options to be **snapshotted onto the job row** (already the schema's apparent intent) so a *later* preset edit/deactivation never retroactively changes an already-created job's behavior — that is the actual invariant worth protecting, not the split-second creation-time race.
- What must not be skipped: re-checking the preset's `is_active` flag with a plain (non-locking) read immediately before the `INSERT`, so an already-deactivated preset is rejected for *new* jobs (404, matching cross-client convention — see Pitfall 12) even if the race window itself is not closed with a lock.

**Warning signs:**
A job created with a `preset_name` that scans as inactive when audited after the fact — expected occasionally under concurrent deactivation, a *problem* only if it happens for presets that were already inactive well before the request (which would indicate the active-check was skipped entirely, not raced).

**Phase to address:**
The presets resolution phase (server-side `preset=<name>` → validated opts) — the snapshot-onto-job-row behavior is core design and must be correct from the first implementation; the "acceptable race window" decision should be an explicit, written Key Decision (matching this project's existing convention of recording accepted residual risks in `PROJECT.md`), not an implicit unstated assumption.

---

### Pitfall 9: Trusting stored preset options without re-running them through the current opts-validation allowlist

**What goes wrong:**
`presets.options` is `jsonb NOT NULL DEFAULT '{}'::jsonb` — a schema-flexible blob, by design, since presets must cover multiple `operation`/engine types over time. The project's existing opts pipeline (`ParseDocOpts`/`ParseHTMLOpts` in `internal/convert/opts.go`/`htmlopts.go`) is the single validated chokepoint guaranteeing "client bytes never reach engine argv/CSS unvalidated" (OPTS-01/02, HTML-03). If preset resolution reads `presets.options` and passes it directly into the job's `options` column (or worse, directly into the converter) **without** re-running it through the same `ParseXOpts` allowlist function used for ad-hoc client-submitted opts, two failure modes open up: (a) a preset created under an older, looser allowlist (e.g. before `htmlMarginMMMax` was tightened, or before a field was removed from `HTMLOpts`) silently carries forward now-invalid values that bypass current validation entirely because "it's a preset, not raw client input"; (b) a future allowlist tightening has no effect on already-stored presets, creating a two-tier trust model (ad-hoc opts are always current-validated, preset opts are validated-at-creation-time-only) that directly contradicts this project's own stated invariant that client bytes never carry unvalidated data past the parse boundary — a preset's stored JSON *is* client/operator-originated data (entered via `cmd/manage-presets`, which itself may predate a schema change).

**Why it happens:**
"It's already a preset, presumably validated when created" is an intuitive but false assumption — validation is a property of *when a value is checked*, not a permanent property of the value itself. Every other piece of durable state in this codebase (job status transitions, callback URLs) is re-checked at the point of use, not trusted from creation time; presets should follow the same rule but are new enough to not yet have that rule encoded.

**How to avoid:**
- At preset-resolution time (inside `handleCreateJob`, alongside the existing engine/format validation), run `presets.options` through the exact same `ParseDocOpts`/`ParseHTMLOpts` (or a shared dispatcher keyed by `operation`/engine, matching the existing `Registry.EngineFor` pattern) used for direct client-submitted opts — never branch preset-sourced opts around that call.
- If a stored preset now fails current validation (allowlist tightened since creation), fail the job creation with a clear, non-leaking error (see Pitfall 12) rather than silently coercing/dropping the invalid field — the operator who owns the preset needs to know it's stale, not have it silently misbehave.
- Consider a `cmd/manage-presets validate` (or `lint`) subcommand that re-runs every stored preset through current `ParseXOpts` and reports failures, so allowlist changes surface stale presets proactively instead of only at first job-creation failure.

**Warning signs:**
A preset that worked at creation time starts returning validation errors after an unrelated opts-schema change ships — this is actually the *correct* behavior if re-validation is wired in; its absence (a preset silently keeps "working" with fields the current allowlist would reject for ad-hoc input) is the actual warning sign that re-validation was skipped.

**Phase to address:**
The presets resolution phase — this must be designed in from the start since it's the one place where "presets reuse the existing validated-opts pipeline" (already the milestone's own stated design intent per `PROJECT.md`) can quietly diverge into "presets bypass the existing validated-opts pipeline" if the re-validation call is treated as optional.

---

### Pitfall 10: Scope-precedence bugs between system and per-client ("user") presets

**What goes wrong:**
The DDL's actual scope model is `scope IN ('system', 'user')` (not "client" as a scope label — the milestone description's "client presets" refers to `scope='user'` rows, which carry a non-null `client_id`), with independent uniqueness: `presets_system_uq (name, version) WHERE scope='system'` and `presets_user_uq (client_id, name, version) WHERE scope='user'`. **Nothing in the DDL prevents a `user`-scope preset from sharing a `name` with a `system`-scope preset** — this is presumably intentional (a client should be able to define `photo-thumbnail` locally even if a system preset of the same name exists, and have their local one take precedence when they request `preset=photo-thumbnail`), but it means preset resolution needs an explicit, tested precedence rule: "look up `(client_id, name)` in user-scope first; fall back to `(name)` in system-scope only if no user-scope match exists" — get the lookup order backwards (system-first) and a client's intentional override of a system default silently never takes effect, with no error to signal the shadowing failed.

**Why it happens:**
Two independent unique indexes with no cross-scope uniqueness constraint is easy to read as "these are just two separate namespaces" rather than "one namespace with an explicit override order" — the precedence logic lives entirely in application code, not the schema, so it's untestable by a DB constraint and must be covered by an explicit unit test.

**How to avoid:**
- Write the resolution query/function as two explicit steps in a fixed order (user-scope lookup by `(client_id, name)` → if found, use it and stop; else system-scope lookup by `(name)`), and unit-test the shadowing case directly: create a system preset and a same-named user preset for one client, assert the client's job resolves to the user preset's `options`/`target_format`, and assert a *different* client with no same-named preset still resolves to the system one.
- Also test the inverse-looking-but-different case: a client requesting a preset name that has **only** a system-scope entry (no user override) must still resolve successfully — a bug here (e.g., accidentally scoping the system lookup by `client_id IS NULL AND client_id = $1` instead of an `OR`) would make every system preset invisible to every authenticated request.
- Log/metric the resolution outcome (`resolved_scope=system|user`) at least during initial rollout so scope-precedence bugs are observable, not just silent.

**Warning signs:**
A client reports "my custom preset isn't being used" despite having created one with the same name as a system preset (classic shadowing-order bug); or, inversely, "system presets don't work for me at all" (classic client_id-filter-too-strict bug).

**Phase to address:**
The presets resolution phase — precedence order should be one of the phase's explicit test cases (both directions), not an implicit side effect of however the SQL happens to be written first.

---

### Pitfall 11: Version field on presets makes "which version resolves" an unstated but load-bearing decision

**What goes wrong:**
Both `presets_system_uq (name, version)` and `presets_user_uq (client_id, name, version)` include `version` in their uniqueness key, and `jobs.preset_version` exists as its own column — meaning the schema explicitly supports **multiple versions of the same preset name coexisting**. A naive `WHERE name = $1` lookup with no `version` filter and no `ORDER BY version DESC LIMIT 1` will either error (if more than one version row exists, depending on how the SQL is written) or silently resolve to whichever version Postgres happens to return first (not guaranteed to be the latest) — a correctness bug that only manifests once an operator creates a second version of an existing preset, which may not happen until well after the presets feature ships and looks "done."

**Why it happens:**
Early testing during the presets phase will likely only ever create one version per preset name (the common case), so a missing `ORDER BY version DESC LIMIT 1` (or missing explicit "at most one active version" semantics) won't surface as a bug until a real operator creates preset v2 in production.

**How to avoid:**
- Decide and document explicitly: does `preset=<name>` (no version specified) always resolve to the highest active `version`, or does versioning mean something else (e.g., only one row per name is ever "active" at a time, and `version` is purely an audit/history column bumped on every edit, with old versions kept for `job.preset_version` traceability but never independently resolvable)? The DDL's `is_active` column (singular per row, not "latest active") suggests the latter is more likely intended — but this must be a stated design decision, not inferred.
- If multiple simultaneously-active versions per name are genuinely supported, the resolution query needs an explicit `ORDER BY version DESC LIMIT 1` (or `WHERE is_active` combined with an application-level invariant that at most one version per `(scope, client_id, name)` is ever active) — write a test that creates two versions of the same preset and asserts deterministic resolution.

**Warning signs:**
Resolution behaves correctly in every manual/E2E test (because only one version ever exists in those tests) but the query itself has no explicit ordering — a code-review-only catch, since it won't fail any test until a second version is created.

**Phase to address:**
The presets resolution phase — the version-resolution rule should be a written decision (added to `PROJECT.md`'s Key Decisions table, matching this project's existing convention) before the resolution query is implemented, not left implicit in whatever `LIMIT 1` behavior Postgres happens to exhibit.

---

### Pitfall 12: Preset-existence leaks across clients via error message content or status code, breaking the established 404-not-403 convention

**What goes wrong:**
This project already has a hard-won, explicitly-documented convention: "cross-client доступ → 404 (никогда 403)" for API-key-authenticated resources (established in Phase 1, per `PROJECT.md`'s Validated section). Presets are the first *new* resource type added since that convention was set, and per-client (`user`-scope) presets are exactly the kind of resource where it's easy to regress: a naive implementation might return 403 ("this preset exists but isn't yours") instead of 404 ("no such preset"), or — more subtly — return a *different* error message/shape for "preset name doesn't exist at all" vs. "preset name exists but belongs to another client" (e.g., a validation error mentioning the preset's `target_format`/`operation` for the latter case but not the former), which leaks existence information through message content even while nominally returning the same status code.

**Why it happens:**
The 404-not-403 rule was established for one resource type (client auth) and its rationale doesn't automatically propagate to a newly-added resource unless someone explicitly re-derives it; and "same status code" is an easier bar to hit in code review than "byte-for-byte same response body for both cases," so message-content leaks are more likely to slip through.

**How to avoid:**
- Implement preset lookup so that "preset doesn't exist" and "preset exists but belongs to a different client" are **literally the same code path** returning the same fixed message (e.g., `writeError(w, http.StatusNotFound, "preset not found")`) — the SQL query itself should filter by `client_id` (or `scope='system'`) as part of the `WHERE` clause, not as a separate post-lookup ownership check with a distinguishable branch, mirroring how this project already treats cross-client job access (if `GET /v1/jobs/{id}` already does this for jobs, copy that exact pattern rather than re-deriving it for presets).
- Add a test explicitly asserting response-body equality (not just status-code equality) between "nonexistent preset name" and "preset belonging to another client" — a status-code-only test would pass even with a message-content leak.

**Warning signs:**
Any code path where preset lookup returns the row first and then checks `if preset.ClientID != authenticatedClientID` as a distinguishable branch (versus filtering by client in the query itself) — the review signal is "was the ownership check done in SQL or in Go," since the Go-branch version is the one that tempts a different error message per branch.

**Phase to address:**
The presets resolution phase (specifically, whichever plan wires `preset=<name>` into `POST /v1/jobs`) — this is a one-line SQL-filter decision made once at implementation time; retrofitting it later requires an audit of every place preset lookup happens (CLI included — `cmd/manage-presets` itself should presumably also enforce scope-appropriate visibility, though the CLI is likely operator-trusted and out of the client-facing 404 concern).

---

## Technical Debt Patterns

| Shortcut | Immediate Benefit | Long-term Cost | When Acceptable |
|----------|-------------------|-----------------|------------------|
| Skip disk-space mitigation in CI, rely on runner defaults | One less workflow step to write | Recurring flaky-build failures once LibreOffice/Chromium images are added to the build tier — this project has already hit this exact failure locally | Never — this project has direct evidence (13-03-SUMMARY.md) the stack exceeds comfortable disk headroom |
| Use `docker compose build` without buildx/gha cache wiring | Faster to write, "just works" | Every CI run pays full LibreOffice/Chromium build cost; burns both disk and minutes on every push | Only for a throwaway spike/prototype workflow, never the shipped CI pipeline |
| Lock `presets.options` acceptance to "validated once at preset-creation time only" | Simpler resolution code path (no re-validation call) | Silently diverges from the project's own "never trust stored client-originated bytes" invariant; allowlist tightening has no retroactive effect | Never — this directly contradicts an established project-wide security invariant (OPTS-01/02) |
| Return 403 for cross-client preset access "just for this one case, it's simpler" | Slightly more semantically "correct" HTTP status per REST purists | Breaks the existing, deliberate 404-only convention; leaks preset existence to unauthenticated-for-that-resource callers | Never — this project explicitly rejected 403 for this exact reason in Phase 1 |
| Enable `-race` in CI without an explicit `-timeout` override | One less flag to reason about | Silent flakiness once `-race` overhead + CI runner throttling push any future real-time-based test past the 10-minute default | Acceptable only until the first `-race`-tier test exceeds ~5 minutes; set the explicit flag proactively instead of waiting for the first timeout |

## Integration Gotchas

| Integration | Common Mistake | Correct Approach |
|-------------|-----------------|-------------------|
| GitHub Actions + Docker Buildx | Assuming `docker compose build` uses the `type=gha` cache automatically | Use `docker buildx bake` with explicit `cache-from`/`cache-to: type=gha,scope=<service>` per image, or build each image individually via `docker/build-push-action` |
| GitHub Actions + docker-compose healthchecks | Trusting `depends_on: condition: service_healthy` alone as "the stack is ready for tests" | Add an explicit stack-readiness poll against `GET /healthz` (already exists, already pings Postgres/Redis/S3) before starting the E2E test run |
| GitHub Actions runner host + compose network (webhook receiver reachability) | Forgetting to carry the existing `extra_hosts: host.docker.internal:host-gateway` pattern (already used for `api`, `webhook-worker-1/2`, `chromium-worker` in `docker-compose.e2e.yml`) into CI, where `host-gateway` resolution behavior can differ between GitHub-hosted Linux runners and local Docker Desktop | Explicitly verify `host.docker.internal:host-gateway` resolves correctly on `ubuntu-latest` runners in a smoke step before trusting it in the full E2E run — Linux Docker Engine (which GitHub-hosted Linux runners use, unlike Docker Desktop's VM) needs the same `--add-host` mechanism, which Compose's `extra_hosts` already provides, but this is worth a one-time explicit CI verification rather than an assumption carried over from local testing |
| MinIO presigned URLs + `E2E_S3_DIAL_ADDR` rewrite | Assuming the CI runner's `127.0.0.1:9100` port mapping matches what worked locally without re-verifying the compose port publish (`9100:9000`) survives unchanged in the CI compose invocation | Keep the exact same `docker-compose.yml`/`docker-compose.e2e.yml` port mappings between local and CI (already the case, since CI should invoke the same compose files) — do not introduce a CI-specific compose override that changes port numbers, since `E2E_S3_DIAL_ADDR=127.0.0.1:9100` is hardcoded in the E2E harness's documented env-var contract |
| `cmd/manage-presets` CLI + auth scoping | Building the CLI without the same client-scoping discipline as `cmd/manage-clients`, e.g. allowing an operator to accidentally create a `user`-scope preset for the wrong `client_id` with no confirmation/lookup-by-name safety net | Mirror `cmd/manage-clients`' existing UX conventions (create/add-key/revoke-style explicit subcommands, confirmation of which client a scoped action targets) rather than inventing a new CLI interaction pattern |

## Performance Traps

| Trap | Symptoms | Prevention | When It Breaks |
|------|----------|------------|-----------------|
| Preset resolution re-running full opts validation on every job creation | Slightly higher CPU per job-creation request | Acceptable — this is the deliberate trade-off Pitfall 9 requires (correctness over micro-optimization); the existing opts-validation call is already fast (in-memory struct parsing, no I/O) | Not expected to break at this project's internal-clients scale; would only matter at request volumes far beyond "internal services only" |
| Row-locking every preset read to close the TOCTOU window from Pitfall 8 | Job-creation throughput drops under concurrent load because a rarely-written, often-read table is serialized like a rarely-read, often-written one | Do not lock preset reads; accept the narrow race and protect the actual invariant (job-row snapshot) instead | Would manifest as job-creation latency climbing under load if implemented — a self-inflicted trap, not a scale threshold to actually reach |
| 5-image CI build tier run on every single push (not just PRs/main) | CI minutes consumption grows linearly with push frequency even for unrelated doc-only commits | Path-filter the heaviest tiers (Docker build + live E2E) to skip on changes that touch only non-code paths (`.planning/**`, `*.md`), while keeping gofmt/vet/build/test on every push | Becomes noticeable once the team's push cadence is high enough that CI minutes/cost become a visible line item — worth deciding proactively given this project's demonstrated multi-commit-per-day cadence |

## Security Mistakes

| Mistake | Risk | Prevention |
|---------|------|------------|
| Presets bypass the validated-opts pipeline (Pitfall 9) | Reintroduces the exact class of injection risk OPTS-01/02 was built to close — client/operator-influenced bytes (via CLI-entered preset) reaching engine argv/CSS without allowlist validation | Route `presets.options` through the same `ParseDocOpts`/`ParseHTMLOpts` call used for ad-hoc opts, with no bypass branch |
| Cross-client preset existence leak (Pitfall 12) | Minor information disclosure (confirms a named preset exists and is used by another internal client) — low severity given internal-only clients, but inconsistent with the project's established security posture | Filter by `client_id` in the SQL `WHERE` clause, not as a post-lookup Go-side branch; identical error body for "not found" and "not yours" |
| `WEBHOOK_ALLOW_PRIVATE_IPS`/`WEBHOOK_ALLOW_INSECURE_HTTP` relaxations in `docker-compose.e2e.yml` accidentally reused outside the E2E CI job | If a CI workflow file mistakenly builds/deploys using `docker-compose.e2e.yml` values outside the E2E job (e.g., a copy-paste into a future deploy step), the SSRF guard relaxation escapes the test-only context it was designed for | Keep the E2E-only compose override strictly confined to the E2E CI job's `docker compose -f docker-compose.yml -f docker-compose.e2e.yml` invocation; never let a later CI job (e.g., an image-publish step) reference the `.e2e.yml` override file at all |
| Secrets (e.g. `API_KEY_SALT`, `WEBHOOK_SIGNING_SECRET`) currently hardcoded as `dev-only-change-me-in-real-deploys` literals directly in `docker-compose.yml`/`docker-compose.e2e.yml` get echoed into CI logs verbatim if a workflow step dumps env/config | Low risk today (values are explicitly dev-only placeholders, not real secrets) but establishes a bad habit if CI workflows print environment dumps for debugging | Keep using the existing dev-only placeholder convention for CI's throwaway stack (no real secret material belongs in CI at all for this internal-only, non-deployed-from-CI service), but avoid workflow steps that dump full `env` or full compose config to logs as a matter of hygiene |

## "Looks Done But Isn't" Checklist

- [ ] **CI Docker image build tier:** Often missing disk-space mitigation — verify by running the exact same 5-image build sequence against a disk-constrained environment (or explicitly checking runner disk usage mid-build via `df -h`) before declaring the tier stable, not just confirming it passed once.
- [ ] **CI live E2E tier:** Often missing `if: always()` teardown and `if: failure()` log capture — verify by deliberately breaking one assertion and confirming the workflow still cleans up the stack and produces a diagnosable log artifact.
- [ ] **Presets resolution:** Often missing re-validation of stored `options` against the current allowlist — verify by manually inserting a preset row with a field the current `ParseXOpts` would reject (e.g., an out-of-range `margin_mm`) and confirming job creation rejects it, not silently accepts it.
- [ ] **Presets cross-client isolation:** Often missing response-body-identical 404s — verify with a test comparing full response bytes (not just status code) between "preset name doesn't exist" and "preset exists for a different client."
- [ ] **`-race` CI gate:** Often missing explicit `-timeout` override — verify the workflow file's `go test` invocation includes `-timeout` explicitly rather than relying on the 10-minute default, and that it was chosen with headroom above the tier's actual measured duration under `-race`.
- [ ] **GHA build cache:** Often "wired up" but never verified to actually hit — verify by running the same workflow twice in a row with no changes and confirming build time drops substantially and `CACHED` layers appear in the log on the second run.

## Recovery Strategies

| Pitfall | Recovery Cost | Recovery Steps |
|---------|----------------|------------------|
| Disk exhaustion discovered only after CI ships | LOW | Add a disk-cleanup step (`docker system prune -af` or a free-disk-space marketplace action) before the build tier; no code changes needed, just workflow-file edits |
| Cache backend not actually being used | LOW-MEDIUM | Switch `docker compose build` calls to `docker buildx bake`/`docker/build-push-action` with explicit `cache-from`/`cache-to`; re-run twice to confirm hit rate before considering it fixed |
| Preset resolution shipped without re-validation (Pitfall 9) | MEDIUM | Add the missing `ParseXOpts` call at the resolution chokepoint; audit any presets created in the interim for now-invalid fields via a one-off `cmd/manage-presets validate`-style pass before they're used by a real job |
| Scope-precedence bug shipped backwards (system-before-user) | MEDIUM | Fix the lookup order; audit `job_events`/job history for any jobs that silently resolved to the wrong scope's preset during the bug window, since this affects already-completed jobs' actual behavior retroactively, not just future ones |
| Cross-client preset existence leak shipped (403 or distinguishable message) | LOW | Fix the SQL filter/error-message unification; this is an information-disclosure issue with internal-only clients as the blast radius, not a data-loss issue — no data recovery needed, just a code fix and a follow-up audit of whether the leaked information was actually observed/logged anywhere |

## Pitfall-to-Phase Mapping

| Pitfall | Prevention Phase | Verification |
|---------|-------------------|----------------|
| fakeEnqueuer data race (v1.3 tech debt, prerequisite for `-race` CI gate) | Tech-debt cleanup phase (must land before the `-race` CI phase, per the milestone's own stated dependency ordering) | `go test -race ./internal/reconciler/...` passes clean with zero race detector reports |
| Runner disk exhaustion (5-image build) | CI phase: Docker image build tier | Build tier passes on a fresh/cold-cache CI run with disk-mitigation step present; re-run without the mitigation step once to confirm it actually reproduces the local failure mode from 13-03-SUMMARY.md, then keep it fixed |
| Healthcheck-vs-readiness gap | CI phase: live E2E tier | E2E tier passes on 5+ consecutive cold-start CI runs (not just warm/cached ones) with no `connection refused` flakes |
| `docker compose build` bypassing gha cache | CI phase: Docker image build tier | Two consecutive runs with no Dockerfile changes show substantially reduced build time and `CACHED` layers in logs on the second run |
| 10GB cache budget thrashing across 5 images | CI phase: Docker image build tier | Cache usage dashboard checked after first several CI runs; per-image cache scopes confirmed non-colliding |
| Zombie stacks / lost diagnostics on failure | CI phase: live E2E tier | Deliberately-failing test run still produces a teardown and a compose-logs artifact |
| Superseded runs piling up | CI phase: workflow-file introduction | Pushing two commits in quick succession to the same branch shows the first run auto-cancelled |
| `-race` + default 10-minute timeout | CI phase: `-race` gate | Workflow's `go test` invocations all carry an explicit `-timeout` flag; `-race` tier duration measured and confirmed with headroom below that timeout |
| Preset resolution TOCTOU | Presets phase: resolution + job creation | Explicit written decision recorded (accept the narrow race, protect the job-row snapshot instead) plus a test confirming already-created jobs are unaffected by later preset edits |
| Preset opts trust boundary (never trust stored opts) | Presets phase: resolution + job creation | Test inserting a preset with a since-invalidated field and confirming job creation rejects it via the same `ParseXOpts` path used for ad-hoc opts |
| Scope precedence (system vs. user) | Presets phase: resolution logic | Test covering both shadowing (user overrides system) and fallback (no user override, system resolves) directions |
| Version resolution ambiguity | Presets phase: resolution logic | Written decision on version-resolution semantics recorded before implementation; test with two versions of one preset confirming deterministic resolution |
| Cross-client preset existence leak | Presets phase: `preset=<name>` wiring into `POST /v1/jobs` | Test asserting byte-identical response bodies for "preset not found" vs. "preset belongs to another client" |

## Sources

- `.planning/milestones/v1.3-phases/13-cross-format-conversion-input-safety/13-03-SUMMARY.md` — direct project evidence of Docker Desktop VM disk exhaustion during this exact multi-image build sequence, resolved via `docker builder prune -a`/`docker image prune -a`. HIGH confidence (primary source, this repo's own history).
- `docker-compose.yml`, `docker-compose.e2e.yml` — healthcheck timing, resource limits (`chromium-worker` shm_size/2g, `document-worker`/`worker` 1g), existing `host.docker.internal:host-gateway` pattern, E2E-only SSRF-guard relaxation scope. HIGH confidence (primary source).
- `internal/db/migrations/0001_init.sql` — presets/jobs DDL: scope model (`system`/`user`), uniqueness constraints, `is_active`/`version` columns, `jobs.preset_name`/`preset_version`/`options` snapshot columns. HIGH confidence (primary source).
- `internal/reconciler/reconciler_soak_test.go`, `internal/reconciler/reconciler_test.go` (fakeEnqueuer) — real-wall-clock soak test pattern, `DATABASE_URL`-gated skip guard, existing unguarded-race tech debt referenced in `PROJECT.md`. HIGH confidence (primary source).
- [GitHub-hosted runners reference](https://docs.github.com/en/actions/reference/runners/github-hosted-runners) — 14GB usable disk / ~22GB free on standard `ubuntu-latest` runners. MEDIUM-HIGH confidence (official docs, current).
- [`ubuntu-latest` runner disk space reduced — discussion #9329](https://github.com/actions/runner-images/discussions/9329) — confirms disk headroom on standard runners is tighter than commonly assumed and has trended smaller over time. MEDIUM confidence (official repo discussion, not formal docs, but authoritative source).
- [Docker Docs — GitHub Actions cache backend](https://docs.docker.com/build/cache/backends/gha/) — `type=gha` cache backend behavior, `mode=max` vs `mode=min`, and that it must be wired explicitly (not automatic under `docker compose build`). HIGH confidence (official Docker docs).
- [GitHub Changelog — Actions cache size can now exceed 10GB per repository (2025-11-20)](https://github.blog/changelog/2025-11-20-github-actions-cache-size-can-now-exceed-10-gb-per-repository/) — confirms 10GB is the *default*, configurable but not automatically raised; teams must not assume extra headroom without checking repo settings. MEDIUM-HIGH confidence (official changelog).
- Go standard toolchain behavior: `go test`'s default `-timeout` is 10 minutes when unspecified — well-established stdlib/tooling behavior (`go help testflag`). HIGH confidence (documented Go tooling behavior).

---
*Pitfalls research for: OctoConv v1.4 (GitHub Actions CI + live E2E, presets mechanism)*
*Researched: 2026-07-12*
