# Stack Research

**Domain:** GitHub Actions CI pipeline (4-tier: gate → race → Docker build → live E2E) + presets CLI dependency confirmation
**Researched:** 2026-07-12
**Confidence:** HIGH (all GitHub Action versions verified live via GitHub's Releases API on 2026-07-12; runner specs verified via official GitHub docs and `actions/runner-images`)

This is an **additive** stack note for OctoConv v1.4 "CI, Presets & Debt Cleanup". It supersedes the previous milestone's STACK.md content (v1.3 "Document Class v2" — chromium/LibreOffice/webhook-worker research, now shipped and validated). It does not revisit the fixed core stack (Go 1.26, chi, asynq/Redis, PostgreSQL 18, MinIO — Notion spec, out of scope) or any existing engine/worker code. Everything below is either (a) net-new GitHub Actions tooling for the CI pipeline, or (b) an explicit confirmation that presets need **zero new Go dependencies**.

## Recommended Stack

### Core Technologies (GitHub Actions)

| Technology | Version | Purpose | Why Recommended |
|------------|---------|---------|-----------------|
| `actions/checkout` | `v7` (v7.0.0, released 2026-06-18) | Clone repo into runner workspace | Current major release as of research date. `v6.0.3` (2026-06-02) is also actively maintained as a fallback. |
| `actions/setup-go` | `v6` (v6.5.0, released 2026-06-24) | Install Go 1.26 toolchain; built-in module/build cache | Use `go-version-file: go.mod` so CI always tracks whatever `go.mod`'s `go 1.26.4` directive says, with no second version string to keep in sync. Built-in caching (`cache: true`, the default) removes the need for a hand-rolled `actions/cache` step for Go — one less moving part. |
| `docker/setup-buildx-action` | `v4` (v4.2.0, released 2026-07-02) | Provision a BuildKit builder | Required before `docker/bake-action` — bake does **not** set up Buildx itself (verified against the action's own README/example workflow). Without this, Buildx falls back to a builder that can't use the `type=gha` cache backend. |
| `docker/bake-action` | `v7` (v7.3.0, released 2026-07-01) | Build multiple images from one definition in one invocation | OctoConv's `docker-compose.yml` **already declares all 5 build targets** (`api`, `worker`, `document-worker`, `chromium-worker`, and `webhook-worker-1`/`-2` sharing `Dockerfile.webhook-worker`). Bake can consume a compose file directly as its target definition (`files: docker-compose.yml`) — no parallel Dockerfile list needs to be maintained inside the workflow YAML. Materially less duplication than 5 separate `docker/build-push-action` steps. |
| `actions/upload-artifact` | `v7` (v7.0.1, released 2026-04-10) | Upload compose logs on E2E failure | Standard mechanism for post-mortem debugging of a failed live-infra run. `v3.x` is legacy/EOL — do not use. |

### Supporting Actions

| Action | Version | Purpose | When to Use |
|--------|---------|---------|-------------|
| `actions/cache` | `v6` (v6.1.0, released 2026-06-26) | Generic key/value cache | **Not needed here.** `actions/setup-go`'s built-in cache covers Go, and the `type=gha` Buildx backend covers Docker layers. Keep in reserve only for something neither of those covers (e.g. a separately-downloaded CLI tool). |
| `docker/metadata-action` | `v6` (v6.2.0, released 2026-07-02) | Derive image tags/labels for a registry push | **Not needed.** This pipeline never pushes OctoConv's images anywhere (see "What NOT to Add") — compose's default `{project}-{service}` image naming is sufficient. |
| `docker/login-action` | n/a (not adopted) | Authenticate to a registry | **Not needed for OctoConv's own images.** Narrow, deferred use only: if anonymous Docker Hub pulls of `postgres:18`/`redis:8`/`minio/minio:latest` start hitting 429 rate limits on shared GitHub runner IP ranges (a real, documented phenomenon — see Pitfalls), add a free Docker Hub account + `docker/login-action` at that point. Don't add it preemptively. |

### Development Tools (confirms zero new Go dependencies for presets)

| Tool | Purpose | Notes |
|------|---------|-------|
| Go stdlib (`flag`, `os`) + existing `internal/db` (pgx) | `cmd/manage-presets` CLI | **Confirmed: no new Go module needed.** The `presets` table already exists in the DDL (documented as unused since the original schema design — see `.planning/PROJECT.md` Context). `cmd/manage-presets` follows the exact structural pattern of the existing `cmd/manage-clients` (stdlib flag parsing, direct pgx pool access, no framework). The `preset` field on `POST /v1/jobs` is a server-side name→stored-`opts` lookup that reuses the validated-`opts` allowlist mechanism already shipped in Phase 14 (`OPTS-01/02`) — a preset *is* a named, server-stored `opts` value, subject to the identical fail-closed validation client-supplied `opts` already go through. `go.mod`/`go.sum` require no edits. |
| `gofmt`, `go vet`, `go test` (`-race` in Tier 2) | Existing enforced quality bar (per `CLAUDE.md`) | CI Tier 1/2 automate what's already the project's manual bar — no new linter, no `.golangci.yml`, consistent with current conventions (none exists today). |

## Installation / Workflow Skeleton

No package manifest to install from — this is GitHub Actions YAML. Recommended shape for `.github/workflows/ci.yml` (new file — the project currently has **no CI workflow at all**, per `CLAUDE.md`'s "No Makefile or CI workflow ... rely on go build, go vet, go test run manually"): four jobs chained by `needs:`, mirroring the milestone's "4 tiers" framing and giving each tier its own GitHub status check (useful later for branch protection).

```yaml
name: ci
on: [push, pull_request]

jobs:
  # Tier 1: fast gate
  gate:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
          cache: true
          cache-dependency-path: go.sum
      - run: test -z "$(gofmt -l .)"
      - run: go vet ./...
      - run: go build ./...
      - run: go test ./...   # no -race here; that's Tier 2

  # Tier 2: race detector (blocked on the fakeEnqueuer mutex debt fix landing first)
  race:
    needs: gate
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
          cache: true
          cache-dependency-path: go.sum
      - run: go test -race ./...

  # Tier 3: build all 5 images, verify they build, warm the GHA layer cache
  docker-build:
    needs: race
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - name: Free disk space   # see "Runner Sizing" below — cheap insurance for 5 images incl. LibreOffice + Chromium
        run: |
          sudo rm -rf /usr/share/dotnet /opt/ghc /usr/local/lib/android /opt/hostedtoolcache/CodeQL || true
          docker image prune -af || true
      - uses: docker/setup-buildx-action@v4
      - uses: docker/bake-action@v7
        with:
          files: docker-compose.yml
          load: false   # verification only — no need to keep images in this job's daemon
          set: |
            api.cache-to=type=gha,mode=max,scope=api
            api.cache-from=type=gha,scope=api
            worker.cache-to=type=gha,mode=max,scope=worker
            worker.cache-from=type=gha,scope=worker
            document-worker.cache-to=type=gha,mode=max,scope=document-worker
            document-worker.cache-from=type=gha,scope=document-worker
            chromium-worker.cache-to=type=gha,mode=max,scope=chromium-worker
            chromium-worker.cache-from=type=gha,scope=chromium-worker
            webhook-worker-1.cache-to=type=gha,mode=max,scope=webhook-worker
            webhook-worker-1.cache-from=type=gha,scope=webhook-worker
            webhook-worker-2.cache-to=type=gha,mode=max,scope=webhook-worker
            webhook-worker-2.cache-from=type=gha,scope=webhook-worker

  # Tier 4: live E2E against the real compose stack
  e2e:
    needs: docker-build
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - name: Free disk space
        run: sudo rm -rf /usr/share/dotnet /opt/ghc /usr/local/lib/android /opt/hostedtoolcache/CodeQL || true
      - uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
          cache: true
          cache-dependency-path: go.sum
      - uses: docker/setup-buildx-action@v4
      # Reload images from the Tier-3-warmed GHA cache (near-instant: same scopes as above)
      - uses: docker/bake-action@v7
        with:
          files: docker-compose.yml
          load: true
          set: |
            api.cache-from=type=gha,scope=api
            worker.cache-from=type=gha,scope=worker
            document-worker.cache-from=type=gha,scope=document-worker
            chromium-worker.cache-from=type=gha,scope=chromium-worker
            webhook-worker-1.cache-from=type=gha,scope=webhook-worker
            webhook-worker-2.cache-from=type=gha,scope=webhook-worker
      - name: Bring up compose stack
        run: docker compose -f docker-compose.yml -f docker-compose.e2e.yml up -d
        # deliberately NOT --build here: images are already loaded and tagged
        # by bake above under the same {project}-{service} names compose
        # expects. If tag-matching between bake's compose-derived naming and
        # what `docker compose up` looks for ever proves unreliable, fall
        # back to `up -d --build` — with the GHA cache warm it's still
        # near-instant, so this is a low-risk safety net, not a real tradeoff.
      - run: go run ./cmd/migrate
        env:
          DATABASE_URL: postgres://octo:octo-pass@localhost:5434/octo_db
      - name: Run E2E suite
        run: go test ./internal/e2e/...
        env:
          E2E_BASE_URL: http://localhost:8090
          DATABASE_URL: postgres://octo:octo-pass@localhost:5434/octo_db
          API_KEY_SALT: dev-only-change-me-in-real-deploys
          WEBHOOK_SIGNING_SECRET: dev-only-change-me-in-real-deploys
          # E2E_WEBHOOK_HOST defaults to host.docker.internal inside
          # e2e_test.go — already correct on a Linux GH runner because
          # docker-compose.e2e.yml adds extra_hosts: host-gateway on
          # api/webhook-worker-1/-2/chromium-worker. No CI-specific change
          # needed (see "host.docker.internal" section below).
      - name: Dump compose logs on failure
        if: failure()
        run: docker compose -f docker-compose.yml -f docker-compose.e2e.yml logs --no-color > compose-logs.txt
      - uses: actions/upload-artifact@v7
        if: failure()
        with:
          name: compose-logs
          path: compose-logs.txt
          retention-days: 7
      - name: Tear down
        if: always()
        run: docker compose -f docker-compose.yml -f docker-compose.e2e.yml down -v
```

## Alternatives Considered

| Recommended | Alternative | When to Use Alternative |
|-------------|-------------|-------------------------|
| `docker/bake-action` reading `docker-compose.yml` directly | 5× `docker/build-push-action` steps, one per Dockerfile | If the images ever need genuinely different build args/platforms per image such that a shared compose-derived bake definition becomes awkward. For 5 uniform-shape builds sharing a compose file already in the repo, bake is strictly less duplication. |
| Separate `docker-build` (cache-warm, no compose stack) and `e2e` (cache-reload + full stack) jobs | Single job doing build-then-up | If total pipeline time is short enough that the extra job overhead (re-checkout, re-setup-buildx) isn't worth a separate pass/fail signal. The recommended split gives a clean "images build" status check independent of live-E2E flakiness — a flaky webhook-timing assertion shouldn't obscure a real Dockerfile break, and vice versa. |
| `up -d` (no `--build`), relying on bake's tags matching compose's expected names | `up -d --build` unconditionally | Use `--build` unconditionally the moment tag-matching between bake's compose-derived naming and `docker compose up`'s lookup proves unreliable in practice — the explicit fallback noted in the skeleton above. |
| GHA cache backend (`type=gha`), scoped per-service | Registry cache backend (`type=registry`) | Only if OctoConv gets a private registry for other reasons — it doesn't today. Registry cache has no 10 GB cap, but needs push credentials and an actual registry, both out of scope (Constraints: Docker Compose for local dev; K8s/KEDA explicitly future/out-of-scope). |
| `actions/setup-go`'s built-in cache | Hand-rolled `actions/cache` step for `~/go/pkg/mod` + `~/.cache/go-build` | Only if a future need arises to cache something setup-go doesn't cover. Redundant for plain Go module/build caching today. |

## What NOT to Add

| Avoid | Why | Use Instead |
|-------|-----|-------------|
| Any registry push step (GHCR/Docker Hub) for OctoConv's own images | Milestone scope is "build all 5 images in CI," not "publish" them; the project has no registry anywhere in its stack (Constraints: Docker Compose for local dev, K8s/KEDA explicitly future/out-of-scope) | Build with `load:`/no-`load` as appropriate per job; keep images ephemeral within each CI job |
| A third-party CI system (CircleCI, Jenkins, GitLab CI, etc.) | Milestone explicitly specifies GitHub Actions; repo is already hosted on GitHub | GitHub Actions, as above |
| Kubernetes-based CI runners / self-hosted K8s executors | K8s+KEDA is explicitly future/out-of-scope per project Constraints; standard `ubuntu-latest` hosted runners are sufficient for a docker-compose stack of this size | GitHub-hosted `ubuntu-latest` runners |
| `docker-compose` v1 (standalone Python binary, hyphenated command) | Deprecated upstream, no longer receives updates, and is actually absent from current runner images, which ship only the v2 CLI plugin | `docker compose` (v2 CLI plugin, space not hyphen) — already what the project's own README and compose files assume |
| A hand-written matrix of 5 parallel `docker/build-push-action` jobs, each pushing/pulling through a registry to share results with the E2E job | Unnecessary registry round-trip for images that are never published; adds push-credential management for zero benefit | Single bake invocation per job, GHA-cache-backed, as in the skeleton above |
| `actions/cache` for Docker layers | Historically used before the `type=gha` Buildx cache backend existed; strictly worse for Docker builds (no BuildKit-aware layer/blob dedup, coarser granularity, manual key management) | `type=gha` cache backend wired through Buildx/bake |
| New Go dependencies for presets (e.g. a validation/config library) | The `presets` table and validated-`opts` mechanism (Phase 14) already provide everything needed; `cmd/manage-clients` is the established CLI pattern to copy | stdlib `flag` + existing `internal/db`/repo-style pattern |

## Runner Sizing — Read Before Wiring the `e2e` Job

**Verified specs (docs.github.com, checked 2026-07-12):** standard `ubuntu-latest` is **4 vCPU / 16 GB RAM / 14 GB SSD for public repositories**, but only **2 vCPU / 8 GB RAM / 14 GB SSD for private repositories** on GitHub's included free tier. OctoConv is internal — confirm whether its GitHub repo is private and, if so, what plan applies (Team/Enterprise plans can grant larger default included runners; Free-tier private repos get the smaller spec).

Why this matters concretely: the compose stack Tier 4 brings up simultaneously is `postgres:18` + `redis:8` + `minio` + `api` + `worker` (2 CPU/1 GiB limit) + `document-worker` (2 CPU/1 GiB limit) + `chromium-worker` (2 CPU/2 GiB limit, plus 256m `/dev/shm`) + `webhook-worker-1`/`-2` + `asynqmon`. Those are Docker resource **limits**, not reservations — actual usage during a short E2E run will typically be far below the ceilings — but on a 2 vCPU/8 GB private-repo runner, several conversions running concurrently across engine classes could plausibly approach the ceiling. **MEDIUM confidence** this is fine for the current one-format-pair-at-a-time E2E test shape; flag it as the first thing to check if the `e2e` job is ever flaky/OOM-killed rather than test-logic-flaky. If it becomes a real bottleneck, GitHub-hosted larger runners (paid, e.g. `ubuntu-latest-4-cores`) are the next lever — self-hosted runners are a bigger step, not recommended as a first response given the project's explicit "no extra infra focus this phase" constraint.

**14 GB nominal disk vs. reality:** GitHub's own docs list "14 GB SSD" for standard Linux runners, but community reports (`actions/runner-images` discussions, cross-checked 2026-07-12) indicate the underlying filesystem is larger in practice (~72 GB total, with fresh runners starting around ~22 GB free after preinstalled tooling) — the 14 GB figure reads as a conservative documented guarantee rather than literal available space. Either way, 5 images including LibreOffice (`document-worker`) and Chromium (`chromium-worker`) plus 3 pulled base service images (Postgres 18, Redis 8, MinIO) is a realistic candidate for `ENOSPC` on a cold cache. **Mitigation included in the workflow skeleton above:** a "Free disk space" step removing preinstalled toolcache (`/usr/share/dotnet`, `/opt/ghc`, `/usr/local/lib/android`, `/opt/hostedtoolcache/CodeQL`) before the build/E2E jobs — a plain shell step, no third-party action, consistent with the project's existing bias toward stdlib/shell over new dependencies (e.g. the zero-dependency decompression-bomb size parsers from Phase 7).

## Docker Compose v2 and `host.docker.internal` — Confirmed Preinstalled, No Change Needed

`ubuntu-latest` (Ubuntu 24.04 runner image) ships Docker Compose v2 as the `docker compose` CLI plugin out of the box — confirmed 2.38.2 in the image's own README, and GitHub bumped Docker/Compose versions again across all Linux/Windows runner images on 2026-02-09 per the Actions changelog, so it stays current without any setup action. Docker Engine ships at 28.x, which natively supports the `host-gateway` special string used in `extra_hosts` (Docker Engine has supported it since 20.10) — so `docker-compose.e2e.yml`'s existing `extra_hosts: ["host.docker.internal:host-gateway"]` pattern (already relied on for the E2E webhook receiver and the chromium-worker network-block canary) works unmodified inside a GitHub Actions job exactly as it does in local dev.

**No new strategy is needed here** — this is a "verify, don't reinvent" finding: the existing compose contract (`E2E_BASE_URL`, `E2E_WEBHOOK_HOST` defaulting to `host.docker.internal`, `API_KEY_SALT` must match the running API's value, optional `E2E_S3_DIAL_ADDR`) transfers to CI as-is, per `internal/e2e/e2e_test.go`'s documented environment contract.

## GHA Docker Layer Cache — Sizing Note

The GitHub Actions cache backend for Buildx (`type=gha`) shares the same **10 GB per-repository cache quota** as every other `actions/cache`-based cache in the repo (Go module/build cache included), with LRU eviction (oldest-unaccessed-first) once the quota is exceeded, and rate limits of up to 1,500 cache reads / 200 writes per minute. With `mode=max` across 5 images — 2 of which (`document-worker` with LibreOffice, `chromium-worker` with headless Chromium) carry non-trivial base-layer weight — it's plausible the combined per-scope cache set periodically exceeds 10 GB and some scopes get evicted before others. **This is graceful degradation, not a failure mode:** an evicted scope just rebuilds that image's layers from scratch on the next run (slower, not broken). No action required unless build times become a real pain point, at which point trimming `mode=max` to `mode=min` for the least-frequently-changed images (the base-OS + LibreOffice/Chromium *install* layers, which rarely change) is the first lever to pull.

## Version Compatibility

| Package A | Compatible With | Notes |
|-----------|-----------------|-------|
| `actions/setup-go@v6` | Go 1.26 (`go.mod` directive `go 1.26.4`) | Use `go-version-file: go.mod`, not a hardcoded `go-version: '1.26'` input, so CI always matches whatever `go.mod` declares without a second place to update. |
| `docker/bake-action@v7` | `docker/setup-buildx-action@v4` | Bake does **not** provision Buildx itself — `setup-buildx-action` must run first in the same job (confirmed via the action's own README/example workflow). |
| `docker/bake-action` reading `docker-compose.yml` | Compose Build Specification (`build:` blocks already present for all 5 image services) | No separate `docker-bake.hcl` file is needed — the existing compose file is a valid bake definition source as-is. |
| GHA cache backend (`type=gha`) | Buildx bundled with Docker 28.x on `ubuntu-latest` | GitHub's Cache Service API v2 is the only supported API version since April 2025; irrelevant risk here since current-generation actions (`bake-action@v7`, `setup-buildx-action@v4`) only speak v2. |
| `postgres:18` / `redis:8` / `minio/minio:latest` (existing, unchanged) | Anonymous Docker Hub pulls from GitHub-hosted runner IP ranges | Not a version-compatibility issue, but a **known rate-limit risk**: Docker Hub's anonymous per-IP pull limits are shared across all GitHub-hosted runners globally, and busy shared IP ranges occasionally hit 429s. No CI exists yet for this project so it hasn't been observed here — if 429s appear in practice, the fix is a free Docker Hub account + `docker/login-action` (raises the limit), not a stack change. Not recommended preemptively. |

## Sources

- `actions/checkout` releases (`api.github.com/repos/actions/checkout/releases`, live query 2026-07-12) — v7.0.0 published 2026-06-18, v6.0.3 published 2026-06-02
- `actions/setup-go` releases (same method) — v6.5.0 published 2026-06-24; `actions/setup-go` README (raw, main branch) — verified `cache-dependency-path` input and default `go.mod`-keyed cache behavior
- `docker/build-push-action` releases — v7.3.0 published 2026-07-01 (evaluated as an alternative, see "Alternatives Considered")
- `actions/cache` releases — v6.1.0 (2026-06-26), with a concurrently maintained v5.1.0 line
- `docker/setup-buildx-action` releases — v4.2.0 published 2026-07-02
- `docker/bake-action` releases — v7.3.0 published 2026-07-01; `docker/bake-action` README (raw, master branch) — verified setup-buildx dependency and the compose-file/wildcard-cache-`set` workflow example
- `actions/upload-artifact` releases — v7.0.1 published 2026-04-10
- `docker/metadata-action` releases — v6.2.0 published 2026-07-02 (evaluated, not adopted)
- docs.github.com, "GitHub-hosted runners reference" — public-vs-private-repo `ubuntu-latest` hardware spec table (4 vCPU/16 GB/14 GB SSD public; 2 vCPU/8 GB/14 GB SSD private)
- `actions/runner-images` Ubuntu 24.04 README — Docker 28.0.4 / Docker Compose 2.38.2 preinstalled
- GitHub Changelog, "Docker and Docker Compose version upgrades on hosted runners" (2026-01-30) — confirms periodic version bumps (e.g. 2026-02-09) keep Compose current on `ubuntu-latest`
- Docker Docs, "GitHub Actions cache" (`docs.docker.com/build/cache/backends/gha/`) — `type=gha` cache backend usage; per-target `scope=` requirement to avoid cache overwrite when multiple bake targets share one invocation
- docs.github.com, "Caching dependencies to speed up workflows" — 10 GB per-repo cache quota, LRU eviction policy, 200 writes/1,500 reads per minute rate limits
- `actions/runner-images` community discussions (#9329, #13719, #10386) and independent blog write-ups (Carlos Becker, Chris Dzombak) — MEDIUM confidence, cross-referenced across multiple sources — nominal 14 GB documented disk vs. empirically larger (~72 GB total / ~22 GB free) actual filesystem, and the standard mitigation (removing preinstalled toolcache dirs, `docker image prune`) for `ENOSPC` on multi-image Docker builds
- Repo files read for integration context: `docker-compose.yml`, `docker-compose.e2e.yml`, `internal/e2e/e2e_test.go` (env-var contract, first ~80 lines), `.env.example`, `.planning/PROJECT.md`

---
*Stack research for: OctoConv v1.4 "CI, Presets & Debt Cleanup" (GitHub Actions 4-tier CI pipeline; presets CLI zero-new-deps confirmation)*
*Researched: 2026-07-12*
