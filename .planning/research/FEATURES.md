# Feature Research

**Domain:** Internal async file-conversion service — CI pipeline (GitHub Actions) + named conversion presets
**Researched:** 2026-07-12
**Confidence:** MEDIUM (CI concurrency/tiering mechanics are HIGH-confidence, sourced from GitHub's own docs; preset resolution/versioning semantics are a bespoke schema design question — recommendations are MEDIUM confidence, informed by analogous patterns in Cloudinary/CloudConvert/imgix and by OctoConv's own existing conventions, not by an external authority for this exact schema)

## Feature Landscape

### Table Stakes (Users Expect These)

| Feature | Why Expected | Complexity | Notes |
|---------|--------------|------------|-------|
| PR gate: gofmt/vet/build/unit-test on every push/PR | Baseline "does it compile and pass tests" signal; matches CLAUDE.md's own stated bar ("passes `go vet` cleanly") | LOW | Pure Go tooling, no external services, should run in well under a minute — this tier must be a **required** branch-protection check |
| `-race` unit-test tier | Codebase has async workers/queues/mutex-guarded singleton-sweeper (Phase 16) — data races are a real, already-encountered class of bug (`fakeEnqueuer` race debt item proves it) | LOW-MEDIUM | Blocked today by the `fakeEnqueuer` mutex debt fix (milestone dependency, already called out) — once fixed, this should also be a **required** PR check, not main-only, since it's still fast and has no external deps |
| Build all 5 Docker images | These images (`api`, `worker`, `document-worker`, `chromium-worker`, plus the `migrate`/build-adjacent image) are the actual deployable artifacts; a Dockerfile break (e.g. missing `libvips-tools`/`tini`/apt package) is a deploy-time failure today, caught in CI it's a PR-time failure | MEDIUM | Use `docker buildx build` with GH Actions layer cache (`cache-from/to: type=gha`) per image so 5 builds stay fast in parallel matrix jobs, not one serial job |
| Named preset resolvable via `preset=<name>` at job creation | Client asked to stop hand-rolling `target`+`opts` for every call; this is the entire point of the milestone feature | MEDIUM | Touches `handleCreateJob`'s multipart parsing (`internal/api/handlers.go`) and needs a new preset-lookup query; must route through the *same* validated-opts pipeline (`ParseDocOpts`/`ParseHTMLOpts` + `ValidateApplicability`) that explicit opts already use — no parallel/weaker validation path for presets |
| `cmd/manage-presets` CLI: create/list/show/deactivate | Direct mirror of `cmd/manage-clients` (already the codebase's established "operator manages state via CLI, not API" convention) | LOW | Same skeleton as `manage-clients/main.go`: `os.Args[1]` switch, `usage()` fallback, `fmt.Println` for anything meant to be captured/scripted, `log.Fatalf` for init errors |
| System presets usable by any client; client-scoped presets usable only by the owning client | DDL already encodes this via `scope`/`client_id`/`CHECK` constraint — the feature is "wire up what's already modeled," not new schema | LOW | Auth middleware already resolves `client.ID` before `handleCreateJob` runs; scoping the preset lookup by that id is a straightforward `WHERE (scope='system') OR (scope='user' AND client_id=$1)` |
| Job-record provenance: resolved preset name+version stored on the job | Needed for audit ("what config actually ran for job X") — this is a hard requirement for an internal service where "why did this job produce this output" must be answerable without guessing | LOW | `jobs.preset_name`/`jobs.preset_version` columns **already exist** in the DDL (lines 53-54) — this is populate-not-migrate work |

### Differentiators (Competitive Advantage)

| Feature | Value Proposition | Complexity | Notes |
|---------|--------------------|------------|-------|
| `preset show <name>` with full version history | Ops-friendly debugging: "what did v2 vs v3 of `thumbnail-webp` actually contain" without querying Postgres by hand | LOW | Cheap add on top of table-stakes `show`; just don't filter to latest-only |
| Scheduled (nightly/cron) live-E2E run, independent of PR traffic | Catches drift from external tool upgrades (libvips/LibreOffice/chromium base-image bumps) that no code change would trigger, and catches flakiness patterns invisible in low-volume PR traffic | LOW-MEDIUM | `schedule: cron` trigger; failures should open/update a tracking issue or post to a known channel — a scheduled job nobody is notified about is equivalent to no job at all |
| CI job names split 1:1 with logical checks (`lint`, `test-race`, `build / api`, `build / document-worker`, `e2e-live`) | Actionable failure UX — GitHub's PR checks list becomes a scannable dashboard instead of one opaque "CI" blob you have to click into | LOW | Matrix strategy for the 5-image build tier gives this almost for free |
| Preset `validate`/dry-run CLI verb (run `ValidateApplicability` against a hypothetical source/target without creating a job) | Lets an operator sanity-check a new preset before publishing it, without spending a real conversion | LOW | Pure reuse of existing `internal/convert` validation functions, no new logic |

### Anti-Features (Commonly Requested, Often Problematic)

| Feature | Why Requested | Why Problematic | Alternative |
|---------|---------------|------------------|-------------|
| Live E2E required-blocking on every PR commit | "Shift-left, catch everything before merge" | Docker-compose live E2E (real libvips/LibreOffice/chromium processes, service health-check races, network timing) is the single most flake-prone tier in this stack; making it a required PR gate trains engineers to spam-retry until green, which destroys the signal it's meant to provide | Advisory (non-blocking, visible) on every PR + **required, blocking** on push to `main`/merge-queue + scheduled nightly run with alerting |
| Self-service preset creation via a public API endpoint | "Let clients manage their own presets without waiting on an operator" | Explodes the trust/validation surface: arbitrary client-submitted opts would get stored server-side and silently reused across every future job under that name — contradicts the codebase's existing "internal clients, CLI-managed state" model (`clients` table has no self-service creation either) | Keep preset creation strictly `cmd/manage-presets`-only (operator/CLI), same trust boundary as `clients` |
| Deep-merge of preset baseline options with client-supplied partial overrides (Cloudinary/imgix-style "defaults + override") | Looks ergonomic — "use preset X but bump the quality a bit" | `opts` is a **closed, typed, allowlist struct** validated via `ParseDocOpts`/`ParseHTMLOpts` — merge semantics against a closed struct raise real ambiguity ("does the client's partial JSON replace or merge each key? what if a merged combination becomes syntactically invalid only in combination?") and reopen exactly the injection/allowlist surface Phase 14 closed | Ship XOR (preset **or** explicit target+opts, never both) for v1.4; revisit merge only if usage data shows real demand |
| Mutable preset versions (editing `options`/`target_format` of an existing version row in place) | "Just fix the preset, don't bump a version for a typo" | Breaks the audit trail's entire purpose: a job created last week recorded `preset_version=3`; if version 3's `options` are later mutated in place, that recorded version no longer describes what actually ran | Any semantic change (`options`, `target_format`) creates a **new** version row; only `description`/`is_active` are mutable in place (metadata, not behavior) |
| Hard `delete` CLI verb for presets | "Clean up old/unused presets" | No FK from `jobs` to `presets` (jobs denormalize `preset_name`+`preset_version`+`options` at creation time), so a hard delete wouldn't corrupt history — but it *would* make `preset show <name>` for an old version silently 404, surprising anyone auditing later, and breaks parity with the established `clients`/`webhook_deliveries` soft-state convention | `deactivate` only (`is_active=false`), mirroring `manage-clients revoke` — never a destructive delete verb |
| Full custom CI dashboard/status page | "Better visibility than the stock GitHub Actions UI" | Over-engineering for a small internal-tool team; GitHub's native Checks UI + branch protection required-checks already satisfies the actionability bar | Rely on native GitHub Checks + well-named jobs + artifact upload on failure |

## CI Pipeline Design — Recommendations

**Tiering (PR gate vs main-only vs scheduled):**

| Tier | Trigger | Required (blocking)? | Rationale |
|------|---------|----------------------|-----------|
| 1. gofmt/vet/build/test | every push + PR | **Required** on PR | Fast (<2 min), zero external deps, zero flake surface |
| 2. `-race` | every push + PR | **Required** on PR (once fakeEnqueuer debt item is fixed) | Still fast, no external services; async/queue/worker code is exactly where races hide — catching them post-merge is too late |
| 3. Build all 5 Docker images | every push + PR | **Required** on PR | These are the actual deploy artifacts; a broken Dockerfile is a deploy-blocking bug, should never reach `main` |
| 4. Live E2E on compose stack | every PR (advisory) + every push to `main` (**required**) + nightly `schedule` | **Advisory on PR, required on `main`** | Balances flake tolerance against catching real regressions before deploy; nightly catches infra/base-image drift independent of any code change |

**Failure UX (what makes results actionable):**
- One job per logical check, named so the GitHub PR checks list is scannable without opening logs (`lint`, `test-race`, `build (api)`, `build (worker)`, `build (document-worker)`, `build (chromium-worker)`, `e2e-live`) — use a build matrix for the 5-image tier rather than one serial job.
- Upload `docker compose logs` and worker stdout as build artifacts (`actions/upload-artifact`) on live-E2E failure — a red live-E2E run with no attached logs forces a full local re-run just to diagnose, which is the top complaint about flaky E2E tiers in practice.
- Route `gofmt -l`/`go vet` output through a step that fails with the diff/violation printed inline in the step log, not buried in a generic non-zero-exit failure.
- Scheduled (nightly) run failures must page/notify somewhere (tracking issue, chat webhook) — a cron job whose failure sits unseen in the Actions tab provides zero actual signal.

**Concurrency / cancellation of stale runs:**
- Use `concurrency: group: ${{ github.workflow }}-${{ github.ref }}` with `cancel-in-progress: true` **for PR-triggered runs** — cancel the previous run when a new commit lands on the same PR/branch; this is safe because nothing in the PR-gate tiers has side effects (it only builds/tests, never deploys).
- Do **not** cancel-in-progress for the `main`-branch/live-E2E-required run — GitHub explicitly disallows combining `queue: max` with `cancel-in-progress: true`, and semantically a main-branch gate run should never be interrupted mid-flight since it may be feeding a deploy decision. Let it queue instead.
- Scope the concurrency group to include `github.head_ref` (not just `github.workflow`) so unrelated PRs don't cancel each other's runs — only commits *within the same PR* should race/cancel.

**Dependencies already identified by the milestone (confirmed, not new):**
- `-race`-clean `fakeEnqueuer` must land before the `-race` CI tier is enabled (it would be permanently red otherwise).
- A dedicated image/libvips E2E test must exist before the live-E2E CI tier is enabled (it's the one gap in the current E2E matrix — document and HTML engines already have live-verified E2E per PROJECT.md, image doesn't yet).

## Preset Semantics — Recommended Defaults

Each open question from the milestone brief, with a recommended default and rationale. These are schema/behavior decisions specific to OctoConv's own `presets` DDL — confidence is MEDIUM (informed by analogous external patterns and by OctoConv's own established conventions, not verifiable against an external authority for this exact table).

**1. Name+scope resolution precedence — does a client preset shadow a system preset of the same name?**
Recommended: **yes, client (`scope='user'`) shadows system (`scope='system'`) for the same `name`**, for jobs authenticated as that client. Lookup order: `WHERE scope='user' AND client_id=$1 AND name=$2 AND is_active` first; if no row, fall back to `WHERE scope='system' AND name=$2 AND is_active`. This is the standard "more specific override wins" pattern used everywhere from CSS specificity to Terraform variable precedence to imgix's own defaults-then-per-request-override model — least surprising default, and it lets an operator give one client a customized variant of a shared preset name without renaming anything.

**2. Interaction between `preset=<name>` and explicit `target_format`/`opts`.**
Recommended: **XOR — reject with 422 if both a `preset` field and an explicit `target`/`opts` field are present in the same request.** `opts` is a closed, typed, validated struct (Phase 14's allowlist mechanism); Cloudinary/imgix's "defaults + override" model works because their parameters are large sets of independent, mostly-orthogonal knobs — merging a client-supplied partial JSON into a closed struct raises real ambiguity (replace-vs-merge per key, and whether a merged combination stays valid) and reopens exactly the validation surface Phase 14 closed. XOR is unambiguous, matches the "fail-closed, no silent precedence" philosophy already used throughout `handleCreateJob` (e.g., the mismatch/unrecognized-content 422s), and is trivially cheap to implement. `callback_url` and `file` remain orthogonal to this rule — a preset only ever substitutes for `target`+`opts`, never for delivery/upload fields.

**3. Versioning semantics — bump on update, or mutate in place?**
Recommended: **immutable versions, bump-on-update.** Any change to `options` or `target_format` creates a new row (`version = previous + 1`, same `name`+`scope`); `description` and `is_active` may be updated in place since they're metadata, not conversion behavior. Rationale: `jobs.preset_version` exists specifically so a job's provenance is reconstructable later — if version 3's `options` could be mutated after jobs already recorded `preset_version=3`, that recorded value would stop describing what actually ran. This mirrors the general principle behind immutable, pinned API versions (the same reasoning Stripe/Twilio apply to their own API version strings) and is a stronger guarantee than CloudConvert's presets appear to offer (their public docs don't describe any versioning at all for saved presets — a gap, not a pattern worth copying).

**4. `is_active` semantics — can multiple versions of the same name be active simultaneously?**
Recommended: **no — exactly one active version per `(scope, client_id, name)` at a time**, mirroring the two-slot key-rotation pattern OctoConv already uses for `clients` (`primary`/`secondary` key slots, `add-key`/`revoke`). When `manage-presets update` creates a new version, it deactivates the previous active version for that name in the same transaction. `preset=<name>` resolution is then a simple `WHERE name=$1 AND is_active` (unique per scope by construction) rather than a `MAX(version) WHERE is_active` scan across possibly-multiple active rows — deterministic and matches an existing, already-battle-tested codebase convention rather than inventing a new one.

**5. Job-record provenance.**
Already schema-ready: `jobs.preset_name`/`jobs.preset_version` columns exist. `handleCreateJob` must populate them at resolution time (alongside the already-existing pattern of persisting the *normalized*, not raw, opts — same "never trust/re-derive from client input downstream" discipline already applied to `opts`). No `preset_id` FK column exists on `jobs` and none is needed: `(name, version, scope)` is already unique-indexed on `presets` and sufficient to reconstruct exactly which preset row produced a given job.

**6. CLI verbs.**
Mirror `manage-clients`'s skeleton exactly (`os.Args[1]` switch, `usage()` fallback, `fmt.Println` for scriptable output, `log.Fatalf` for init errors):
- `create <scope> <client-id|-> <name> <operation> <target_format> <opts-json> [description]` — mints version 1, active.
- `update <name> <client-id|-> <opts-json> [target_format] [description]` — creates a new version, deactivates the prior one (transactional, matches recommendation 3).
- `list [--scope system|user] [--client <id>]` — table of active presets.
- `show <name> [--client <id>] [--version N]` — full detail incl. version history if `--version` omitted.
- `deactivate <name> [--client <id>] [--version N]` — soft-delete only, no destructive `delete` verb (matches anti-features table).

## Feature Dependencies

```
[fakeEnqueuer -race fix] ──requires-before──> [CI tier 2: -race]
[image/libvips E2E test] ──requires-before──> [CI tier 4: live E2E in CI]
[CI tier 1-3 green]      ──requires-before──> [CI tier 4: live E2E]  (no point running the slow tier if the fast ones are red)

[presets table populated + validated-opts reuse] ──requires──> [preset=<name> job-creation resolution]
[preset resolution]      ──requires──> [job-record provenance (preset_name/version columns)]
[single-active-version-per-name rule] ──enables──> [deterministic preset=<name> lookup without version disambiguation]

[XOR preset/explicit-opts rule] ──conflicts-with──> [merge-semantics differentiator] (cannot ship both in v1.4; merge is explicitly deferred)
```

### Dependency Notes

- **CI tier 2 requires the `-race` fix first:** enabling a required check that's already known-red blocks all merges immediately; the milestone brief already sequences this correctly.
- **CI tier 4 requires the image E2E test first:** without it, the live-E2E CI tier would have a real coverage gap (image/libvips — the original, longest-running engine — untested end-to-end) even though document/chromium engines are already covered.
- **Preset resolution requires the existing validated-opts pipeline, not a new one:** presets are just server-stored `(operation, target_format, options)` tuples that must pass through the identical `ParseDocOpts`/`ParseHTMLOpts` + `ValidateApplicability` gate that explicit opts already use — otherwise preset-sourced options become an unvalidated side door into the engine invocation path that Phase 14 was built specifically to close.
- **XOR conflicts with merge:** these are mutually exclusive design choices for v1.4; recommend shipping XOR now and treating merge semantics as an explicit, separately-scoped future differentiator if client feedback demands it.

## MVP Definition

### Launch With (v1.4)

- [ ] CI tiers 1-3 (gofmt/vet/build/test, `-race`, 5-image Docker build) as **required** PR checks — the actual "every push is checked" table-stakes promise
- [ ] CI tier 4 (live E2E) as advisory-on-PR / required-on-main / scheduled-nightly — full coverage without blocking velocity on a flake-prone tier
- [ ] `cmd/manage-presets` create/list/show/deactivate, mirroring `manage-clients`
- [ ] `preset=<name>` resolution in `handleCreateJob`, scoped by client, XOR against explicit `target`/`opts`, routed through existing validated-opts pipeline
- [ ] Single-active-version-per-name rule (mirrors client key-rotation pattern) + job-record provenance (`preset_name`/`preset_version` populated)
- [ ] Tech debt: dead webhook wiring removed from document/chromium workers, `fakeEnqueuer` race-safe, image/libvips E2E test added

### Add After Validation (v1.x)

- [ ] `preset show <name>` full version history (cheap add on top of table-stakes `show`)
- [ ] Preset `validate`/dry-run CLI verb
- [ ] Scheduled-run failure notification wiring (issue-bot or chat webhook) if not done in v1.4 itself

### Future Consideration (v2+)

- [ ] Deep-merge of preset baseline + partial client override opts — only if usage data shows clients frequently want "preset X but tweak one field"; requires deliberate merge-semantics design against the closed opts struct, deferred deliberately in this milestone
- [ ] Preset self-service creation via API — deliberately out of scope; would require a new trust/validation model this milestone does not need

## Feature Prioritization Matrix

| Feature | User Value | Implementation Cost | Priority |
|---------|------------|----------------------|----------|
| CI tiers 1-3 required on PR | HIGH | LOW-MEDIUM | P1 |
| CI tier 4 (live E2E) advisory/main-gate/nightly split | HIGH | MEDIUM | P1 |
| `manage-presets` CLI (create/list/show/deactivate) | HIGH | LOW | P1 |
| `preset=<name>` resolution + XOR rule | HIGH | MEDIUM | P1 |
| Single-active-version-per-name + provenance | HIGH | LOW-MEDIUM | P1 |
| Tech debt cleanup (dead webhook wiring, `-race`, image E2E) | MEDIUM (unblocks P1 CI items) | LOW | P1 |
| `preset show` version history | MEDIUM | LOW | P2 |
| Preset validate/dry-run CLI | LOW-MEDIUM | LOW | P3 |
| Scheduled-run alerting wiring | MEDIUM | LOW | P2 |
| Merge semantics (preset + partial override) | LOW (unvalidated demand) | HIGH | P3 (deferred) |

## Patterns from Mature Conversion APIs — What to Steal, What to Avoid

| Feature | Cloudinary | CloudConvert | imgix | Our Approach |
|---------|-------------|--------------|-------|---------------|
| Named preset/config reuse | Named transformations + upload presets, referenced by name in delivery URL or upload call | Presets created in web UI, referenced from API job/task calls (options set once, reused) | Per-Source default parameters, overridable per-request | Steal the core idea (name → server-stored config, reusable across API calls) — this is universal and proven |
| Preset + explicit param interaction | Merge/override: explicit URL params override named-transformation params where they overlap | Not documented publicly (real gap in their docs — not a pattern worth copying) | Explicit request params always override Source-level defaults (documented, clear precedence) | **Don't copy Cloudinary/imgix's merge model** for `opts` specifically — their params are large sets of independent orthogonal knobs; our `opts` is a small closed validated struct where merge ambiguity is a real security-adjacent risk. XOR instead, for v1.4 |
| Preset creation ownership | Named transformations/upload presets created via dashboard or admin API — not created ad hoc mid-request by end clients | Presets created via CloudConvert's own web UI, not by API callers | Source-level defaults configured by account admins, not per-request callers | Steal this: preset creation stays operator/CLI-only, never a client-facing creation endpoint — matches our existing `clients` table trust model |
| Versioning of presets | Named transformations can be updated in place (support article literally titled "How can I update a named transformation" — implies in-place mutation, with the caveat that cached/previously-rendered derivatives may not reflect the update) | No documented versioning | No documented versioning | **Do not copy this.** Cloudinary's own support docs flag exactly the problem immutable versioning avoids (stale cached derivatives after an in-place edit) — our jobs already durably record `preset_version`, so we should do what Cloudinary's own gap suggests they should: bump versions immutably rather than mutate in place |

## Sources

- [GitHub Docs — Control the concurrency of workflows and jobs](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/control-workflow-concurrency) — HIGH confidence, official docs
- [Blacksmith — Protect prod, cut costs: concurrency in GitHub Actions](https://www.blacksmith.sh/blog/protect-prod-cut-costs-concurrency-in-github-actions) — MEDIUM confidence, corroborates official docs on cancel-in-progress-for-PRs / never-cancel-deploys split
- [Exercism Docs — GitHub Actions Best Practices](https://exercism.org/docs/building/github/gha-best-practices) — MEDIUM confidence
- [Cloudinary — Named Transformations documentation](https://support.cloudinary.com/hc/en-us/articles/360018902952-Developing-and-Using-Named-Transformations-with-Cloudinary-Images-and-Videos) — HIGH confidence, official docs
- [Cloudinary — Upload Presets documentation](https://cloudinary.com/documentation/upload_presets) — HIGH confidence, official docs
- [Cloudinary — How Can I Update a Named Transformation?](https://support.cloudinary.com/hc/en-us/articles/202521272-How-can-I-update-a-named-transformation) — HIGH confidence, official docs (source of the "in-place edit vs cached derivative staleness" caveat)
- [CloudConvert — Jobs API v2](https://cloudconvert.com/api/v2/jobs) — HIGH confidence, official docs
- [CloudConvert — Presets blog post](https://cloudconvert.com/blog/presets) — MEDIUM confidence (marketing/blog, not full API reference; versioning/merge behavior explicitly not documented here — flagged as a gap, not a pattern)
- [imgix — Rendering API Overview](https://docs.imgix.com/en-US/apis/rendering/overview) — HIGH confidence, official docs
- [imgix — Advanced Source Settings (default parameters + override)](https://docs.imgix.com/en-US/getting-started/setup/creating-sources/advanced-settings) — HIGH confidence, official docs
- Internal: `.planning/PROJECT.md`, `internal/db/migrations/0001_init.sql`, `cmd/manage-clients/main.go`, `internal/api/handlers.go` — HIGH confidence, primary source for existing conventions this research builds on

---
*Feature research for: OctoConv v1.4 (CI pipeline, conversion presets, debt cleanup)*
*Researched: 2026-07-12*
