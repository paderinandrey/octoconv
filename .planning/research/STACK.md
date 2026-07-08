# Stack Research

**Domain:** Adding a LibreOffice-headless document-to-PDF engine class to an existing Go async conversion worker (shelled-out CLI engine, per-job `os/exec`, Docker/Debian bookworm-slim runtime)
**Researched:** 2026-07-09
**Confidence:** HIGH (Debian package/CVE facts verified against packages.debian.org and the Debian security tracker; LibreOffice CLI/concurrency behavior corroborated across multiple independent sources including the battle-tested Gotenberg project; a few numeric defaults — exact conversion latency for "large but realistic" documents — are MEDIUM confidence and flagged as needing an empirical benchmark before locking `DOCUMENT_ENGINE_TIMEOUT`)

This research is scoped to the *document engine* addition only. Go 1.26, chi, asynq/Redis, pgx/Postgres 18, minio-go/S3 are locked (`.planning/codebase/STACK.md`) and untouched. No new Go module dependencies are introduced — the LibreOffice engine is a second concrete `convert.Converter` shelling out to a CLI, exactly like `LibvipsConverter`, reusing `internal/convert/exec.go`'s hardened `runCommand` unchanged.

## Recommended Stack

### Core Technologies

| Technology | Version | Purpose | Why Recommended |
|------------|---------|---------|-----------------|
| `soffice` (LibreOffice headless CLI) via `libreoffice-writer-nogui` + `libreoffice-calc-nogui` + `libreoffice-impress-nogui` | `4:7.4.7-1+deb12u13` (Debian bookworm, apt `stable`, security-track updated) | Converts docx/odt → pdf (writer), xlsx/ods → pdf (calc), pptx/odp → pdf (impress, pulls `libreoffice-draw-nogui` + `libreoffice-core-nogui` transitively) | This is the only realistic OSS engine for this format set (LibreOffice's own export filters). The `-nogui` variants are Debian's server/scripting-oriented builds — same conversion engine and export filters as the full `libreoffice` package, minus GTK/Qt UI toolkits, the Start Center, and X11 UI deps that a headless worker never touches. Debian's security team actively backports LibreOffice CVE fixes into this exact package line — verified via the Debian security tracker (see Sources) that CVE-2024-12425/12426 (path traversal via embedded font handling in headless/server-side processing — directly relevant to this milestone) and CVE-2025-1080/0514 are all marked **fixed** at `4:7.4.7-1+deb12u13`, so the "old-looking" `7.4.x` upstream version number does NOT mean unpatched; it means Debian backports fixes rather than rebasing to a newer upstream release. HIGH confidence. |
| `fonts-crosextra-carlito` + `fonts-crosextra-caladea` | latest in bookworm (OFL-1.1, small, <5MB combined) | Metric-compatible substitutes for Calibri/Cambria — the default fonts in every Office 2007+ docx/pptx/xlsx | **Load-bearing, easy to miss.** Without these, LibreOffice silently substitutes Liberation fonts for Calibri/Cambria, which are metrically *different* and will reflow text, shift page breaks, and overflow table cells in the PDF output — a correctness bug, not a crash, so it won't show up in tests unless someone visually diffs output PDFs. HIGH confidence (Debian wiki `SubstitutingCalibriAndCambriaFonts` explicitly documents this substitution behavior). |
| `fonts-liberation2` | `2.1.5-1` (bookworm) | Metric-compatible substitutes for Times New Roman/Arial/Courier New | Same class of problem as Carlito/Caladea, for the older Office-2003-era default fonts still common in legacy docx/xlsx. **Explicit install required**: `libreoffice-common` only *Recommends* this package, and `Dockerfile.worker` already builds with `apt-get install --no-install-recommends` (to keep the image lean for the image engine) — so this font package will silently NOT be installed unless listed explicitly alongside the `-nogui` packages. HIGH confidence (verified package relationship + existing Dockerfile flag). |

### Supporting Libraries

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `os.MkdirTemp` (stdlib, already used) | Go 1.26 stdlib | Per-job scratch directory, reused as the LibreOffice isolated profile root | `internal/worker/worker.go:270` already creates `workDir := os.MkdirTemp("", "octoconv-"+job.ID.String()+"-")` per job and `defer os.RemoveAll(workDir)`s it. The LibreOffice converter needs **no new dependency** for unique-profile-per-invocation isolation (see Question 2 below) — it can derive the profile path as a subdirectory of the same `workDir` (e.g. `filepath.Join(filepath.Dir(outPath), "lo-profile")`) that's already passed through via `outPath`, and it's cleaned up for free by the existing `defer os.RemoveAll(workDir)`. Do NOT hand-roll a `google/uuid`-based temp-dir name for this — `os.MkdirTemp`'s `O_EXCL`-based uniqueness is exactly the same guarantee, with no extra import. |
| `internal/convert/exec.go`'s `runCommand` (existing, unchanged) | n/a | Runs `soffice` with `Setpgid` + process-group `SIGKILL` on context timeout | **No changes needed.** This is a direct, and important, validation: `/usr/bin/soffice` on Debian is a wrapper script that `exec`s (replaces its own process image, not fork+exec) into `soffice.bin`, so the PID Go's `os/exec` tracks stays valid for the real LibreOffice process; and even in the (theoretically possible, version-dependent) case where it forks instead, `runCommand`'s `Setpgid: true` + `syscall.Kill(-pid, SIGKILL)` kills the whole process group, not just the tracked PID — which is precisely the documented fix for LibreOffice's well-known "leaves `soffice.bin` zombie/orphan processes behind on timeout/hang" failure mode (see Pitfalls sources). The existing exec.go doc comment already anticipates this ("LibreOffice's soffice.bin do not get orphaned/left hanging") — this new engine is the reason that comment exists. |

### Development Tools

| Tool | Purpose | Notes |
|------|---------|-------|
| `apt-get update && apt-get install ... libreoffice-writer-nogui libreoffice-calc-nogui libreoffice-impress-nogui fonts-crosextra-carlito fonts-crosextra-caladea fonts-liberation2` in `Dockerfile.worker` | Install the document engine at build time, always pulling the current `+deb12uNN` security patch level rather than pinning an exact patch suffix | Pin the **package names**, not the exact `+deb12u13` suffix, in the Dockerfile — Debian's bookworm-slim base image tracks `stable` + `stable-security`, so a fresh `docker build` automatically gets the latest backported CVE fixes for the same `4:7.4.x` line without any Dockerfile change. Do not add `bookworm-backports` to get a newer upstream LibreOffice version (24.8.x/25.2.x) — unnecessary complexity and a second apt source for a purely internal service where the security-patched stable-branch package already receives the relevant CVE fixes (verified above). |

## Installation

```dockerfile
# Runtime stage (Dockerfile.worker) — additive to the existing libvips-tools install
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates \
      libvips-tools \
      libreoffice-writer-nogui \
      libreoffice-calc-nogui \
      libreoffice-impress-nogui \
      fonts-crosextra-carlito \
      fonts-crosextra-caladea \
      fonts-liberation2 \
 && rm -rf /var/lib/apt/lists/*
```

```go
// go.mod: no new entries. Reuse os.MkdirTemp, os/exec, syscall (all stdlib,
// already imported by internal/convert/exec.go), and google/uuid (already a
// dependency, already used for job IDs) if a job-id-derived profile dir name
// is preferred over os.MkdirTemp's own randomness.
```

## Question-by-Question Findings

### 1. `soffice --convert-to` invocation syntax

```
soffice --headless --invisible --nocrashreport --nodefault --nologo \
  --nofirststartwizard --norestore \
  -env:UserInstallation=file:///<per-job-tmp-dir> \
  --convert-to pdf:writer_pdf_Export \
  --outdir <outdir> \
  <inpath>
```

- `--convert-to OutputExt[:OutputFilterName]` selects the export filter. **Explicitly pin the filter name** (`writer_pdf_Export` for docx/odt, `calc_pdf_Export` for xlsx/ods, `impress_pdf_Export` for pptx/odp) rather than the bare `pdf` extension — LibreOffice's automatic filter detection is reliable for these formats today, but pinning removes an entire class of "which module opened this file" ambiguity and documents intent in the command itself. Confirmed via the official LibreOffice "File Conversion Filter Names" help page. HIGH confidence.
- `--outdir <dir>` sets the destination directory. **Filename convention (important integration detail):** `soffice` names the output using the **input file's basename** with the target extension swapped in — e.g. `report.docx` → `report.pdf` — it does **not** accept an explicit output filename. The existing worker convention (`internal/worker/worker.go:270-278`) writes the input to `workDir/in.<sourceFormat>` and expects the converter to produce exactly `workDir/out.<targetFormat>`. **The LibreOffice `Converter.Convert` implementation must rename the file `soffice` actually produces** (`workDir/in.pdf`) to the `outPath` the caller passed (`workDir/out.pdf`) via `os.Rename` after `runCommand` succeeds — this is a required adaptation, not optional, or the worker will report "output not found" on every successful conversion. Confirmed via `man soffice` and multiple independent walkthroughs. HIGH confidence.
- The full hardening flag set (`--invisible --nocrashreport --nodefault --nologo --nofirststartwizard --norestore`) is the flag combination used by Gotenberg (the most widely deployed production Go-adjacent LibreOffice-conversion service) specifically to guarantee no dialog/prompt/UI element can ever block headless conversion in a server context. MEDIUM-HIGH confidence (WebSearch-derived, but consistent across Gotenberg's own docs and community write-ups; not independently re-verified against LibreOffice's own source, so treat as "strongly recommended defaults" rather than a documented contract).

### 2. Concurrent invocation / user-profile-lock problem

**The problem is real and confirmed:** `soffice` writes a lock file into its user profile directory; a second invocation that finds an existing lock either refuses to start ("another instance is already running") or — worse — silently attaches to the already-running instance's UNO session instead of starting independently. The default profile path (`~/.config/libreoffice`) is shared by every invocation on the same `$HOME`, so naive concurrent `os/exec` calls from multiple `WORKER_CONCURRENCY` goroutines **will** collide.

**Recommended mitigation: per-invocation isolated profile via `-env:UserInstallation=file:///<unique-tmp-dir>`, not a long-lived listener.**

| Approach | Verdict for this worker | Why |
|---|---|---|
| **Per-invocation `-env:UserInstallation`** (recommended) | ✅ Adopt | Fits the existing per-job `os/exec` model exactly (same shape as `LibvipsConverter.Convert`): no daemon lifecycle, no health-checking a persistent process, no shared mutable state between jobs, and a hung/crashed conversion for one job cannot corrupt or wedge a shared profile that other concurrent jobs depend on. This directly matches the project's existing failure-isolation philosophy (guarded per-job transitions, per-job engine timeout that only ever kills that job's process group). |
| Long-lived `soffice --accept=socket,...` listener + UNO/`unoconv`/`jodconverter`-style pooling | ❌ Reject for this milestone | A single `soffice` listener instance can only process **one conversion at a time** (LibreOffice's own internal lock) — it does not give you `WORKER_CONCURRENCY`-wide parallelism by itself. Gotenberg's own docs confirm this: in their default "stateful" mode, concurrent requests literally queue behind the one shared instance (their own worked example: 10 concurrent 2s conversions take ~20s serialized). To get real concurrency with a listener architecture you need a *pool* of listeners plus routing/health-check/restart logic — a meaningful new subsystem (this is exactly what `unoconv`, `jodconverter`, and Gotenberg's "stateless" mode exist to solve) that is not justified when per-invocation `os/exec` already gives genuine concurrency for free. It would also introduce a persistent background process the worker binary must supervise, at odds with the "worker shells out per job" architecture used everywhere else in this codebase. |
| `unoconv` (Python bridge to a running `soffice` listener) | ❌ Do not add | Adds a Python runtime + `python3-uno` dependency to a Go/Debian-slim image for no capability the plain `soffice --convert-to` CLI doesn't already provide for this milestone's scope (single format → pdf, no batching/API needs). `unoconv` exists to work around exactly the listener-concurrency problem above by talking to one already-running instance — irrelevant once you've chosen the per-invocation model. |

Concretely: generate a unique temp directory per job (reuse the job's existing `workDir` from `internal/worker/worker.go`, e.g. `filepath.Join(workDir, "lo-profile")`), pass it as `-env:UserInstallation=file://` + that path, and let the existing `defer os.RemoveAll(workDir)` clean it up — no new uniqueness mechanism, no new dependency. HIGH confidence this specific flag/approach is the standard mitigation (corroborated by LibreOffice's own community docs, multiple independent server-side-conversion write-ups, and Gotenberg's "stateless mode" design, which is exactly this pattern generalized).

### 3. Debian package footprint

Use the **`-nogui` component packages**, not the full `libreoffice` metapackage:

| Package | Role | Approx. footprint |
|---|---|---|
| `libreoffice-core-nogui` (pulled in transitively) | Shared engine, UNO runtime, VCL headless (`svp`) backend | ~27.4 MB compressed download (verified via packages.debian.org) |
| `libreoffice-writer-nogui` | docx/odt → pdf | small, mostly shared-lib linkage |
| `libreoffice-calc-nogui` | xlsx/ods → pdf | pulls `liborcus`/`liborcus-parser` (spreadsheet parsing), `lp-solve` |
| `libreoffice-impress-nogui` | pptx/odp → pdf | pulls `libreoffice-draw-nogui`, `libetonyek`/`libmwaw` (legacy format import filters), `libbox2d2` |

Do **not** install: `libreoffice-base-nogui` (database/Base component — not used), `libreoffice-math-nogui` (formula editor — not used), `libreoffice-report-builder-bin-nogui`, `python3-uno` (only needed for scripting/`unoconv`, rejected above), or the full `libreoffice` metapackage (pulls GTK3/Qt5 UI toolkits, X11 client libs, and — depending on Recommends — a JRE for Base/report features; none of that is reachable in a `--headless --convert-to` invocation and it meaningfully bloats both image size and `apt-get install` time). Confirmed via direct inspection of each `-nogui` package's dependency list on packages.debian.org. HIGH confidence.

**No Xvfb/virtual framebuffer needed.** LibreOffice has used a headless VCL backend (`svp`, "SVP" = the non-X11 rendering plugin) automatically selected when `--headless` is passed and no `$DISPLAY` is set, since long before the version shipped in bookworm — none of the current server-side-conversion guides (Gotenberg, the Debian wiki, community write-ups) mention needing Xvfb for basic `--convert-to`. MEDIUM-HIGH confidence (consistent absence across sources rather than one authoritative statement).

### 4. Realistic conversion timeout / `DOCUMENT_ENGINE_TIMEOUT`

No single authoritative benchmark exists for "docx/xlsx/pptx with embedded images/many sheets/slides," so treat the following as a reasoned starting default to validate empirically against real internal documents before locking it in (MEDIUM confidence on the exact number, HIGH confidence on the *shape* of the recommendation):

- Warm-process conversions of ordinary-sized office documents (a few pages/sheets/slides, few embedded images) reported across multiple sources: roughly 1-10 seconds of actual engine work.
- Cold-start tax on top of that (see Question 5) adds several more seconds per invocation under the recommended per-invocation-profile model.
- Pathological-but-realistic documents (large embedded images, dozens of spreadsheet formulas/sheets with cross-references, video-embedded pptx) are the long tail that can push total conversion time into the tens of seconds to low minutes — LibreOffice has open, unresolved performance issues specifically around large/complex Calc and Impress files.
- **Recommendation:** set `DOCUMENT_ENGINE_TIMEOUT` default to **300s (5 minutes)** — roughly 2.5x the sum of a generous cold-start allowance (~15s) plus a generous "large realistic document" conversion budget (~90-120s), leaving headroom rather than a tight bound, mirroring how `ENGINE_TIMEOUT` (120s) already generously exceeds libvips' typical sub-second image conversions. Load-test with the internal team's actual worst-case documents before shipping and adjust; do not treat 300s as verified, only as a defensible starting point distinct from and larger than `ENGINE_TIMEOUT`.
- This timeout choice has a direct downstream dependency: once `DOCUMENT_ENGINE_TIMEOUT` and a `DOCUMENT_MAX_RETRY` are chosen, mirror the existing `ImageUniqueTTL(maxRetry, engineTimeout)` derivation pattern (`internal/queue/queue.go:235`) for a `DocumentUniqueTTL`, rather than hand-picking a constant — the existing code already documents exactly why the TTL must be *derived* from these two values.

### 5. Cold-start latency tax

**Confirmed real and non-trivial.** Multiple independent benchmarks report the *first* `soffice --headless` invocation in a fresh environment taking roughly 5-12 seconds before conversion even begins, dropping to roughly 1-2 seconds for later invocations in the same benchmark setup. The important nuance for this architecture: those "later invocation is fast" numbers generally come from benchmarks that either (a) reused the same warm `soffice` listener process, or (b) ran on a machine where OS page cache already held LibreOffice's shared libraries/fonts — **they do not clearly establish that a fresh `-env:UserInstallation` profile on every single invocation (the recommended per-job-isolation model) stays fast after the first call.** Treat the honest expectation as: a fixed per-job process-startup cost, plausibly single-digit seconds even when the underlying `.so` files are OS-page-cache-warm (since profile initialization — first-run config extraction into the fresh profile dir — is itself part of that cost, not just binary loading), on top of actual document-rendering time.

**Practical implication:** this cold-start-per-job tax is a real per-job cost under the recommended per-invocation model, not a one-time warmup that amortizes away — plan `DOCUMENT_ENGINE_TIMEOUT` and throughput expectations accordingly (see Question 4), and do not assume the "1-2s after the first call" figure applies here without benchmarking against this exact fresh-profile-per-job pattern in the actual Docker image. MEDIUM confidence — this is the most uncertain finding in this research and should be empirically re-verified during implementation with a quick throwaway benchmark (`for i in 1..20; do time soffice --headless -env:UserInstallation=file:///tmp/lo-$i --convert-to pdf ... ; done`).

## Alternatives Considered

| Recommended | Alternative | When to Use Alternative |
|-------------|-------------|--------------------------|
| Per-invocation `soffice` with isolated `-env:UserInstallation`, driven by `os/exec` (this milestone's choice) | Long-lived `soffice --accept=socket` listener pool (Gotenberg "stateful"/JODConverter style) | Only if conversion throughput at high concurrency becomes a measured bottleneck *and* the per-invocation cold-start tax (Question 5) is shown to dominate total latency — this is a meaningfully larger subsystem (pool management, health checks, restart-on-crash) that should not be built speculatively. |
| `libreoffice-{writer,calc,impress}-nogui` (Debian `stable` package line) | `libreoffice` from `bookworm-backports` (newer upstream, e.g. 24.8.x/25.2.x) | If a specific bug fixed only upstream (not backported by Debian security) blocks a real conversion; verify against the Debian security tracker first since the CVEs checked here are already fixed in the stable line. |
| `os.MkdirTemp`-derived per-job profile dir | `google/uuid`-named profile dir | Only if profile directory names need to be predictable/loggable independent of the OS temp-dir naming scheme — not a functional requirement here. |
| Explicit `--convert-to pdf:writer_pdf_Export` (module-specific filter) | Bare `--convert-to pdf` (auto-detected filter) | Auto-detection is reliable for the 6 formats in scope; only pin per-module filters if a future format is added where auto-detection has been observed to pick the wrong export filter. |

## What NOT to Use

| Avoid | Why | Use Instead |
|-------|-----|--------------|
| Full `libreoffice` metapackage | Pulls GTK3/Qt5 UI toolkits, X11 client libraries, Start Center, and (via Recommends) Base/report-builder Java dependencies — none reachable via `--headless --convert-to`; needlessly bloats image size and build time | `libreoffice-{writer,calc,impress}-nogui` + `libreoffice-core-nogui` (transitive) |
| `unoconv` / Python-UNO bridge | Solves a listener-concurrency problem this design doesn't have (per-invocation `os/exec` already gives real concurrency); adds a Python runtime dependency to a Go/Debian-slim image for no net-new capability at this milestone's scope | Plain `soffice --headless --convert-to` per job |
| Shared default `soffice` profile (`~/.config/libreoffice` / no `-env:UserInstallation`) across concurrent `os/exec` calls | The confirmed lock-file collision — concurrent jobs either fail with "another instance is already running" or silently attach to another job's session | Unique `-env:UserInstallation=file://<per-job-tmp-dir>` per invocation, reusing the job's existing `os.MkdirTemp` workDir |
| Pinning an exact `4:7.4.7-1+deb12uNN` patch suffix in the Dockerfile | Locks out future Debian security backports for the same package line (verified this line receives real CVE fixes) | Pin the package **name** only; let `apt-get update` at build time pick up the current `stable-security` patch level |
| Assuming `--no-install-recommends` (already used in `Dockerfile.worker`) still pulls font packages | It does not — `fonts-liberation2`/Carlito/Caladea are Recommends, not Depends, of LibreOffice packages, and this flag suppresses Recommends; silent font substitution corrupts layout without any error | Explicitly list `fonts-crosextra-carlito fonts-crosextra-caladea fonts-liberation2` alongside the `-nogui` packages |
| Bundling the document queue's concurrency into the same undifferentiated `WORKER_CONCURRENCY` pool as the image queue with no cap | Each concurrent `soffice.bin` invocation can plausibly consume 200-500MB RAM plus real CPU during rendering — running `WORKER_CONCURRENCY` (default 4) simultaneous LibreOffice conversions inside the existing worker container's resource limits (`docker-compose.yml`: `cpus: "2.0"`, `memory: 1g`) risks OOM-killing the whole worker process, taking the image queue down with it | Give the `document` queue its own concurrency ceiling — either a second `asynq.Server` instance inside `cmd/worker/main.go` scoped only to `queue.QueueDocument` with a dedicated (lower, e.g. default 2) `DOCUMENT_WORKER_CONCURRENCY`, or a fully separate worker deployment/container — and raise that container's memory limit accordingly. This is a deployment/architecture decision, not a library choice, but it is a direct, load-bearing consequence of the "no new library, extend `os/exec`" stack choice above and should not be deferred silently. |

## Stack Patterns by Variant

**If document-queue concurrency needs an independent cap from the image queue (recommended given LibreOffice's memory footprint):**
- Instantiate a second `*asynq.Server` inside `cmd/worker/main.go`, bound to `queue.QueueDocument` only, with its own `Concurrency: envInt("DOCUMENT_WORKER_CONCURRENCY", 2)`, run alongside the existing image/webhook server via a second `srv.Start(mux)` goroutine.
- Because: asynq's `Queues` weight map (used today for `image`/`webhook`) only affects *priority* when multiple tasks are ready within one server's shared `Concurrency` pool — it cannot express an independent hard concurrency ceiling per queue. A hard, low ceiling on `document` is necessary given each `soffice` invocation's RAM/CPU footprint is an order of magnitude heavier than a `vips copy` invocation.

**If a future format needs cross-conversion within the document class (e.g. docx→odt) — explicitly out of scope for v1.2 per `.planning/PROJECT.md`:**
- Register additional `Pair`s on the same `LibreOfficeConverter` (LibreOffice supports many non-PDF export targets via `--convert-to <ext>:<filter>`) rather than introducing a second document-class converter — no new package needed, just more entries in `Pairs()`.
- Because: it's the same `soffice` binary and the same isolated-profile/rename-output pattern; only the filter name and output extension change.

## Version Compatibility

| Package A | Compatible With | Notes |
|-----------|------------------|-------|
| `libreoffice-{writer,calc,impress}-nogui` `4:7.4.7-1+deb12u13` | `debian:bookworm-slim` base image (already used by `Dockerfile.worker`) | Same Debian release as the already-installed `libvips-tools` — no new base image or multi-arch concern. |
| `fonts-crosextra-{carlito,caladea}`, `fonts-liberation2` | Any LibreOffice bookworm package version | Pure font packages, no version coupling to LibreOffice itself. |
| `internal/convert/exec.go`'s `runCommand` (Setpgid + process-group kill) | `soffice`'s wrapper-script → `soffice.bin` exec chain | No changes required; the existing hardened-exec pattern already anticipates and correctly handles LibreOffice's subprocess shape (see Supporting Libraries above). |
| `os.MkdirTemp`-based per-job `workDir` (`internal/worker/worker.go`) | `-env:UserInstallation=file://<workDir subpath>` | No changes to the temp-dir creation/cleanup pattern — only the LibreOffice converter needs to construct one additional path inside the already-created, already-cleaned-up `workDir`. |

## Sources

- [Debian security tracker — libreoffice source package](https://security-tracker.debian.org/tracker/source-package/libreoffice) — confirmed CVE-2024-12425, CVE-2024-12426, CVE-2025-1080, CVE-2025-0514 all fixed at `4:7.4.7-1+deb12u13` in bookworm (HIGH confidence, authoritative)
- [packages.debian.org/bookworm/libreoffice-nogui](https://packages.debian.org/bookworm/libreoffice-nogui), [.../libreoffice-writer-nogui](https://packages.debian.org/bookworm/libreoffice-writer-nogui), [.../libreoffice-calc-nogui](https://packages.debian.org/bookworm/libreoffice-calc-nogui), [.../libreoffice-impress-nogui](https://packages.debian.org/bookworm/libreoffice-impress-nogui) — exact version, dependency lists, package roles (HIGH confidence, authoritative)
- [packages.debian.org/bookworm/amd64/libreoffice-core-nogui/download](https://packages.debian.org/bookworm/amd64/libreoffice-core-nogui/download) — verified compressed package size (~27.4MB) for footprint discussion (HIGH confidence)
- [packages.debian.org/bookworm/fonts-liberation2](https://packages.debian.org/bookworm/fonts-liberation2) — version 2.1.5-1, Times/Arial/Courier metric-compatible fonts (HIGH confidence)
- [Debian wiki — SubstitutingCalibriAndCambriaFonts](https://wiki.debian.org/SubstitutingCalibriAndCambriaFonts) — Carlito/Caladea as Calibri/Cambria metric-compatible substitutes, available since Debian Jessie (MEDIUM-HIGH, WebSearch-derived summary of an official Debian wiki page)
- `man soffice` (via [SysTutorials](https://www.systutorials.com/docs/linux/man/1-soffice/) and [Binghamton University mirror](https://bingweb.binghamton.edu/man-soffice.html)) — `--convert-to`, `--outdir`, `-env:UserInstallation` syntax (HIGH confidence, canonical man page content)
- [LibreOffice Help — File Conversion Filter Names](https://help.libreoffice.org/latest/en-US/text/shared/guide/convertfilters.html) — `writer_pdf_Export`/`calc_pdf_Export`/`impress_pdf_Export` filter names (HIGH confidence, official docs)
- [Ask LibreOffice — "How can i run multiple instances of soffice.bin at a time"](https://ask.libreoffice.org/en/question/42975/how-can-i-run-multiple-instances-of-sofficebin-at-a-time/) and [multiple-user-profiles thread](https://ask.libreoffice.org/t/multiple-user-profiles-for-parallel-processing-with-custom-configuration-changes-in-user-profiles/110834) — confirmed lock-file collision problem and `-env:UserInstallation` mitigation (MEDIUM-HIGH, official community support forum, corroborated across multiple independent threads)
- [Gotenberg documentation — Configuration](https://gotenberg.dev/docs/configuration) and [Gotenberg GitHub issue #94 "Libreoffice Concurrence"](https://github.com/thecodingmachine/gotenberg/issues/94) and [Discussion #893](https://github.com/gotenberg/gotenberg/discussions/893) — stateful (single-listener, serialized) vs. stateless (per-request instance) tradeoff, confirming per-invocation isolation is the correct concurrency-scaling model, and the exact hardened CLI flag set Gotenberg passes to `soffice` (MEDIUM-HIGH, most widely deployed comparable production system, but WebSearch-summarized rather than directly reading Gotenberg's Go source)
- [shelfio/libreoffice-lambda-layer issue #30](https://github.com/shelfio/libreoffice-lambda-layer/issues/30) and [vladholubiev/serverless-libreoffice STEP_BY_STEP.md](https://github.com/vladholubiev/serverless-libreoffice/blob/master/STEP_BY_STEP.md) — cold-start (~5-12s) vs. warm (~1-2s) conversion latency data points (MEDIUM confidence, community-reported benchmarks, not independently reproduced against the fresh-profile-per-job pattern recommended here)
- [The Register — LibreOffice macro vulnerability](https://www.theregister.com/2019/07/30/libreoffice_macro_vulnerability/) and [LibreOffice Help — Macro Security](https://help.libreoffice.org/latest/en-US/text/shared/optionen/macrosecurity.html) — background on macro-execution risk in office documents; relevant context for treating document inputs as untrusted content in the same way image inputs already are, even though this file focuses on stack/package choices rather than the full pitfalls analysis (MEDIUM confidence, general background)
- Existing codebase: `internal/convert/exec.go`, `internal/convert/libvips.go`, `internal/convert/convert.go`, `internal/worker/worker.go`, `cmd/worker/main.go`, `internal/queue/queue.go`, `Dockerfile.worker` — read directly to ground every recommendation in the actual current implementation (HIGH confidence, primary source)

---
*Stack research for: LibreOffice-headless document-to-PDF engine class for OctoConv v1.2*
*Researched: 2026-07-09*
