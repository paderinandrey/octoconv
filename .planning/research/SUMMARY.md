# Project Research Summary

**Project:** OctoConv — v1.2 Document Engine Class (LibreOffice)
**Domain:** Adding a second, heavier `os/exec`-based conversion engine class (office documents → PDF via LibreOffice headless) to an already production-hardened internal async file-conversion service (Go / chi / asynq / Postgres / MinIO)
**Researched:** 2026-07-09
**Confidence:** MEDIUM-HIGH

## Executive Summary

This milestone is a bounded extension, not a new system: OctoConv's core hardening (auth, rate limiting, webhooks, reconciler, magic-byte validation, observability) shipped in v1.0/v1.1 and is not being touched. The only new capability is a `document` engine class - docx/xlsx/pptx/odt/ods/odp to pdf via `soffice --headless` - registered as one more `convert.Converter` in the existing `Registry`, exactly as `.planning/PROJECT.md` locked in. Every research file converges on the same conclusion: this fits the existing architecture cleanly (no new binary, no new core contract, reuse of the existing hardened `os/exec` wrapper, reuse of the existing per-job `os.MkdirTemp` workDir), but LibreOffice's operational behavior is categorically less trustworthy than libvips' in ways the current codebase has never had to defend against - exit code 0 on failure, a shared-profile lock-file collision under concurrency, a documented history of launcher/forked-process orphaning that could defeat the existing SIGKILL-on-timeout guarantee, and a much larger/spikier resource footprint that a wall-clock timeout alone does not bound.

The recommended approach: (1) extend content-type sniffing to disambiguate ZIP-based office containers (OOXML vs. ODF vs. plain zip) using stdlib-only techniques (`archive/zip`/`compress/flate`, zero new dependencies) and add zip-bomb/macro-payload rejection at the same container-inspection step; (2) implement `LibreOfficeConverter` with a per-job isolated `-env:UserInstallation` profile (derived from the already-existing per-job workDir - zero new lifecycle code), explicit output-file existence/size/magic-bytes validation (since `soffice` can exit 0 with no or corrupt output), and a rename step to satisfy the `Converter` interface's fixed-`outPath` contract; (3) extend the queue/worker layer with a `document` queue, `DOCUMENT_ENGINE_TIMEOUT` (recommended starting default 300s, needs empirical validation), and a properly derived `DocumentUniqueTTL`/retry schedule (not copy-pasted from image's); (4) critically, update the existing, already-running reconciler in the same phase that ships the document queue - it is currently hardcoded to re-enqueue every stranded job onto the image queue, and will silently misroute/terminally-fail stranded document jobs from day one if not fixed concurrently.

The main risks are operational, not architectural, and are well-covered by the pitfalls research: (a) LibreOffice's profile-lock mechanism, if not job-isolated, can let one stuck conversion poison the entire document queue; (b) the existing `runCommand` process-group-kill guarantee has never been verified against the actual `soffice`/`soffice.bin` process topology and must be empirically tested, not assumed; (c) running `USER nobody` with no writable `$HOME`/font cache - fine for libvips, untested for LibreOffice - needs explicit Docker provisioning (HOME, pre-built fontconfig cache, font packages); (d) a crafted document can exhaust CPU/RAM well inside `DOCUMENT_ENGINE_TIMEOUT`, and the roadmap must make an explicit, documented decision about whether to mitigate this (container memory ceiling, input-complexity pre-check) or accept it as residual risk for v1.2, the same way CAD and HTML->PDF were explicitly scoped out. None of these risks are reasons to change the architectural approach - they are launch-blocking implementation details to plan for explicitly rather than discover in production.

## Key Findings

### Recommended Stack

No new Go module dependencies. The only new runtime dependency is the LibreOffice headless CLI itself, installed via Debian's `-nogui` component packages (not the full `libreoffice` metapackage) plus font packages that are easy to silently omit given the existing `--no-install-recommends` flag in `Dockerfile.worker`.

**Core additions:**
- `libreoffice-writer-nogui` + `libreoffice-calc-nogui` + `libreoffice-impress-nogui` (Debian bookworm, `4:7.4.7-1+deb12u13`, security-patched) - the only realistic OSS engine for docx/xlsx/pptx/odt/ods/odp to pdf; pin package names, not exact patch suffix, so Debian's security backports keep applying
- `fonts-crosextra-carlito` + `fonts-crosextra-caladea` + `fonts-liberation2` - metric-compatible substitutes for Calibri/Cambria/Times New Roman/Arial; must be listed explicitly because `libreoffice-common` only Recommends them and the Dockerfile already uses `--no-install-recommends`; omitting them causes silent layout drift, not an error
- `os.MkdirTemp` (existing, no new dependency) - reused as the LibreOffice isolated-profile root, composing with the already-existing per-job workDir and its `defer os.RemoveAll`
- `internal/convert/exec.go`'s `runCommand` (existing, unchanged) - the hardened process-group-kill wrapper already anticipates `soffice.bin` by name in its doc comment; this milestone is the reason that comment exists, but its correctness against the actual chosen package/launcher has never been empirically verified

**Explicitly rejected:** full `libreoffice` metapackage (GTK/Qt/X11 bloat), `unoconv`/Python-UNO bridge (solves a listener-concurrency problem this per-invocation design doesn't have), any persistent/pooled `soffice --accept=socket` listener (reintroduces the exact shared-profile concurrency problem per-job isolation avoids, and conflicts with the "no core rework" decision).

### Expected Features

**Must have (table stakes for v1.2):**
- ZIP-container structural sniff (ODF fixed-offset `mimetype` check + OOXML central-directory root-part check via `[Content_Types].xml`) rejecting non-matching/spoofed content - the direct analog of the already-shipped image magic-byte validation
- Declared-uncompressed-size (zip-bomb) guard on office containers, reusing `archive/zip`'s central directory metadata - same "trust declared metadata, reject before expensive work" philosophy as `MAX_IMAGE_PIXELS`
- Macro-part-presence rejection (`vbaProject.bin`/Basic-script parts) - cheapest mitigation for the most concrete named risk, shares the same container-inspection pass as the two checks above
- Per-job isolated `-env:UserInstallation` LibreOffice profile - without it, the document queue cannot safely run at `WORKER_CONCURRENCY > 1`
- `DOCUMENT_ENGINE_TIMEOUT` wired through the existing hardened-exec path, with timeout classified as terminal (not transient) for this engine - the only reliable backstop against LibreOffice's documented hang-on-bad-input behavior
- Engine-level hardening baked into the Docker image (disable macro execution, disable remote-content/link fetching) - near-zero cost, defense in depth

**Should have (add after validation, v1.x):**
- Pre-flight OLE-CFB (password-protected legacy container) detection as a distinct 422 reason
- `opts`-driven PDF/A export (the `Convert(ctx, inPath, outPath, opts)` plumbing already exists, unused today)

**Defer (v2+, explicitly out of scope per PROJECT.md):**
- Cross-format conversion within the document class (docx<->odt etc.) - engine-cheap once the base ships, but deliberately deferred
- Page-range/partial-document conversion, watermarking, full OOXML/ODF schema validation (Java-based tools, conflicts with zero-new-deps philosophy), persistent/pooled LibreOffice listener (anti-feature - no measured latency problem justifies the added lifecycle-management complexity)

### Architecture Approach

Every addition is either a new file inside an existing package (`internal/convert/libreoffice.go` next to `libvips.go`) or a targeted extension of an existing per-format dispatch table (`sniff.go`'s signature table, `dimensions.go`'s parser map, `queue.go`'s retry-schedule constants) - no new top-level packages, no new binary, no core contract change. The Build Order in the architecture research is explicitly dependency-ordered and should directly inform phase/task sequencing.

**Major components (delta):**
1. `LibreOfficeConverter` (`internal/convert/libreoffice.go`, new) - implements `Converter` for the 6 format pairs to pdf; owns profile isolation, output validation, and the outdir-basename-to-outPath rename
2. ZIP-container sniffing extension (`internal/convert/sniff.go`, modified) - two-stage disambiguation (ODF fixed-offset check vs. OOXML central-directory inspection), architecturally distinct from the current pure-prefix-match shape
3. Document queue/task plumbing (`internal/queue/queue.go`, `client.go`, modified) - `TypeDocumentConvert`, `QueueDocument`, a genuinely derived `DocumentUniqueTTL`/retry schedule (not copy-pasted from image's)
4. Worker wiring (`internal/worker/worker.go`, `cmd/worker/main.go`, modified) - `HandleDocumentConvert`, widened `process()` timeout parameter, LibreOffice-specific terminal-error signatures, second queue registered on the same `asynq.Server`
5. Reconciler engine-awareness (`internal/reconciler/reconciler.go`, modified - not called out in the architecture research's build order but flagged as launch-blocking in pitfalls) - must route recovery by engine class instead of hardcoding the image queue
6. API routing (`internal/api/handlers.go`, modified) - branch enqueue call by resolved engine class; guard the existing unconditional dimension-check with a new `HasDimensionLimit` predicate (confirmed gap: as written today, every document upload would 422 against the image-only dimension check)
7. `Dockerfile.worker` (modified) - LibreOffice `-nogui` packages, fonts, `HOME`/fontconfig-cache provisioning for `USER nobody`

### Critical Pitfalls

1. **Reconciler is hardcoded to the image queue** - will misroute or wrongly terminally-fail every stranded document job from the moment the document queue exists, because it's an existing, continuously-running production component that predates this milestone. Fix: make `FindStale`/the `enqueuer` interface engine-aware and route recovery through the registry/persisted `job.Engine`, in the same phase that ships the document queue, not a follow-up.
2. **LibreOffice can exit 0 while producing no output or a corrupt/empty PDF** - the existing `isTerminal`/upload pipeline has zero output validation today (uploads whatever bytes exist at `outPath` unconditionally). Fix: add a generic, engine-agnostic output-sanity check (non-zero size + `%PDF-` magic bytes for pdf targets) in `process()`, treated as terminal, which also silently hardens the existing image path for free.
3. **The user-profile lock is a hard concurrency ceiling, and a SIGKILLed stuck instance can leave a stale lock that poisons all subsequent document conversions** - a single bad input file can cause a fleet-wide document-conversion outage if profiles aren't job-isolated. Fix: per-job `-env:UserInstallation` derived from the existing per-job workDir (already gets cleaned up by the existing `defer os.RemoveAll`).
4. **The hardened-exec process-group-kill guarantee has never been tested against real LibreOffice** - depending on package/launcher topology, a forking `soffice`/`oosplash` launcher could let the real `soffice.bin` escape `Setpgid`+SIGKILL, silently defeating the timeout safety net. Fix: an explicit integration test (spawn a hung conversion, let the timeout fire, assert zero surviving `soffice*` processes via `ps`) before this milestone is considered done.
5. **`USER nobody` has no writable `$HOME`/font cache**, which libvips never needed but LibreOffice does for profile creation and fontconfig cache building - failure mode is cryptic (hangs or crashes that don't obviously point at "HOME is unwritable"). Fix: set `HOME` per-invocation to the per-job workDir, pre-build the fontconfig cache as root at Docker build time, install real font packages.
6. **A crafted document can exhaust CPU/RAM well within `DOCUMENT_ENGINE_TIMEOUT`** - unlike libvips (pixel-limited via `MAX_IMAGE_PIXELS`, so timeout is a reasonably tight proxy for "something's wrong"), LibreOffice's resource profile under pathological ZIP/XML content is a genuine, separate DoS surface. This needs an explicit roadmap decision (ship a memory-ceiling/complexity pre-check, or formally document as accepted residual risk) - not a silent gap.

## Implications for Roadmap

Based on combined research (architecture's dependency-ordered Build Order + pitfalls' "must fix in the same phase" constraints), suggested phase structure:

### Phase 1: Document Content Safety & Format Detection
**Rationale:** Zero dependency on the LibreOffice engine itself - pure Go, stdlib-only (`archive/zip`, `compress/flate`), testable standalone against fixture files. Must exist before any office document reaches the engine, mirroring the "validate early, write late" principle already established for images. Doing this first also surfaces the confirmed dimension-check gap early (unconditional `convert.Dimensions()` call would 422-reject every document upload today).
**Delivers:** Extended `sniff.go` (ODF fixed-offset check + OOXML central-directory disambiguation), zip-bomb declared-size guard, macro-part-presence rejection, `HasDimensionLimit` predicate scoping the existing image-only dimension check.
**Addresses:** Table-stakes features - ZIP-container structural sniff, zip-bomb guard, macro rejection (FEATURES.md P1 items).
**Avoids:** Pitfall 6 (resource-exhaustion surface) at the declared-metadata layer; the "confirmed gap" in ARCHITECTURE.md where every document upload would currently 422 against the image-only dimension check.

### Phase 2: LibreOffice Converter Engine
**Rationale:** The core, highest-pitfall-density unit of work - independent of the queue/worker/API layers (can be built and unit/integration-tested against the `Converter` interface directly), but everything downstream depends on it existing and being trustworthy. This is where nearly all of the critical pitfalls live, so it needs explicit verification steps built into the plan, not just implementation.
**Delivers:** `LibreOfficeConverter` (Pairs + Convert) with per-job `-env:UserInstallation` profile isolation, output-file existence/size/magic-bytes validation, outdir-basename-to-outPath rename, registered in `converters.go`; `Dockerfile.worker` updated with `-nogui` packages, fonts, `HOME`/fontconfig-cache provisioning for `USER nobody`; an explicit integration test proving the process-group-kill actually terminates real `soffice`/`soffice.bin` processes on timeout.
**Uses:** Stack findings - exact package set, font packages, invocation flags (`--headless --invisible ... --convert-to pdf:writer_pdf_Export --outdir`), `-env:UserInstallation` mitigation.
**Implements:** Architecture Patterns 2/3/4 (profile isolation, exit-code-0 distrust, outdir rename).
**Avoids:** Pitfalls 2, 3, 4, 5, 7 (exit-0-but-corrupt output, profile-lock poisoning, escaped process-group kill, unwritable-HOME startup failures, font-substitution drift).

### Phase 3: Queue, Worker, and Reconciler Integration
**Rationale:** Depends on Phase 2's converter existing to be meaningfully end-to-end testable, but the queue/task-type plumbing itself is independent Go code that could be built in parallel. Bundling the reconciler update into this phase (rather than deferring it) is a direct, non-negotiable consequence of Pitfall 1 - the reconciler is a live production component that will act on document jobs the instant the document queue exists, whether or not it's been updated.
**Delivers:** `TypeDocumentConvert`/`QueueDocument`, a genuinely derived `DocumentUniqueTTL` and retry schedule (mirroring `ImageUniqueTTL`'s derivation, not copy-pasted), `HandleDocumentConvert` with `DOCUMENT_ENGINE_TIMEOUT`, LibreOffice-specific terminal-error signatures in `isTerminal`, reconciler made engine-aware (`EnqueueDocumentConvert` added to its enqueuer interface, `FindStale` extended to carry format/engine info), and an explicit, documented decision on worker resource isolation (shared container/queue weight vs. separate concurrency ceiling for `document`, given LibreOffice's much heavier per-conversion footprint).
**Uses:** Architecture Pattern 1 (engine-class queue routing, already an established codebase convention), asynq's `TaskID`-based idempotent enqueue (existing pattern).
**Avoids:** Pitfall 1 (reconciler misrouting) - explicitly launch-blocking in the same phase; the "shared worker container" technical-debt pattern flagged in PITFALLS.md as needing an explicit, dated decision rather than silent inheritance.

### Phase 4: API Routing & End-to-End Integration
**Rationale:** The final integration step - branching the enqueue call by engine class only needs Phase 1 (sniffing/predicate) and Phase 3 (`EnqueueDocumentConvert`) to exist; it is otherwise independent of the worker-side internals and can be tested against a stubbed `Enqueuer` per the existing interface-segregation pattern. This is also the natural point for full live end-to-end verification (upload -> convert -> download) across all 6 format pairs, mirroring the "live e2e verified" bar every prior phase in this project has held itself to.
**Delivers:** `handleCreateJob` branching enqueue by resolved engine class, dimension-check guarded by `HasDimensionLimit`, full docker-compose rebuild and live smoke test of all 6 document format pairs, an explicit roadmap-level decision recorded on Pitfall 6 (resource-exhaustion mitigation vs. accepted residual risk, following the same pattern used to scope out CAD/HTML->PDF).
**Addresses:** The complete v1.2 Active requirements list in PROJECT.md.
**Avoids:** Shipping with an undecided/silent resource-isolation or DoS-surface gap - PITFALLS.md is explicit that these must be recorded decisions, not omissions discovered later.

### Phase Ordering Rationale

- Content-safety/sniffing (Phase 1) has zero dependency on the engine and surfaces a real, already-confirmed regression (dimension-check would reject every document upload) - cheapest to fix first, blocks nothing else from proceeding in parallel.
- The converter engine (Phase 2) is the highest-risk, most pitfall-dense unit and has no hard dependency on the queue/API layers being finished first - it can and should be built and verified in near-isolation (unit tests against the `Converter` interface, a hung-process integration test) before wiring it into the live job pipeline.
- Queue/worker/reconciler (Phase 3) must ship as one unit specifically because the reconciler is already running in production continuously and will act on document jobs the instant the queue exists - this is not a "nice sequencing," it's a correctness requirement surfaced directly by pitfalls research (Pitfall 1).
- API routing (Phase 4) is deliberately last because it's the thinnest, lowest-risk layer and is the natural point to run full live end-to-end verification once every other piece exists - consistent with how prior phases in this project (v1.0/v1.1) closed with live e2e checks rather than unit tests alone.

### Research Flags

Phases likely needing deeper research/explicit-verification tasks during planning:
- **Phase 2 (LibreOffice Converter Engine):** Highest uncertainty in this research set - the exact process topology (`soffice` wrapper vs. forking launcher) on the actual chosen base image is unverified, cold-start latency under the recommended per-invocation-profile model is only MEDIUM-confidence benchmarked, and `DOCUMENT_ENGINE_TIMEOUT`'s exact value (300s recommended) is a reasoned default pending empirical validation against real internal documents. Plan explicit verification/benchmark tasks (not just implementation tasks) into this phase.
- **Phase 3 (resource isolation decision):** The exact memory footprint of concurrent `soffice` invocations and whether the existing `docker-compose.yml` limits (`cpus: 2.0`, `memory: 1g`) need adjustment is directionally known but not hard-numbered - worth a quick empirical check before locking `DOCUMENT_WORKER_CONCURRENCY`.

Phases with standard, well-documented patterns (skip research-phase):
- **Phase 1 (content safety/sniffing):** ODF's fixed-offset `mimetype` check and OOXML's `[Content_Types].xml`-first-entry convention are both spec-grounded (OASIS spec, corroborated Microsoft/libmagic sources) and implementable with stdlib only - no further research needed.
- **Phase 4 (API routing):** A direct, mechanical extension of the existing "Postgres-first, then enqueue" and interface-segregation conventions already used throughout the codebase - no new pattern to research.

## Confidence Assessment

| Area | Confidence | Notes |
|------|------------|-------|
| Stack | HIGH | Debian package/CVE facts verified directly against packages.debian.org and the Debian security tracker; LibreOffice CLI/concurrency behavior corroborated across multiple independent sources including Gotenberg's production implementation. Exact conversion-latency numbers for large documents are MEDIUM and flagged for empirical benchmarking. |
| Features | MEDIUM | LibreOffice headless behavior verified across official docs, bug tracker, Gotenberg, and the OpenDocument spec; some specifics (exact registry key names for macro-security config, current-version CVE status) are LOW confidence and flagged to spot-check against the pinned version at implementation time. |
| Architecture | MEDIUM-HIGH | Integration points (component placement, build order, confirmed dimension-check gap) are HIGH confidence - grounded directly in the current repo code. LibreOffice-specific operational behavior (process topology, exit-code semantics) is MEDIUM - WebSearch-verified against multiple independent community/bug-tracker sources, not official LibreOffice docs directly. Docker image size delta is LOW - directionally consistent but no hard number verified. |
| Pitfalls | MEDIUM-HIGH | LibreOffice headless failure modes are extremely well-documented across TDF bugzilla, Gotenberg's issue tracker, and multiple independent Docker/serverless LibreOffice wrapper projects (2+ independent sources per pitfall). Codebase-specific integration gaps (reconciler hardcoding, dimension-check gap) are HIGH confidence - read directly from source. |

**Overall confidence:** MEDIUM-HIGH - the architectural fit and integration points are solidly grounded in the actual codebase; the residual uncertainty is entirely in LibreOffice's own operational behavior under this specific deployment's exact conditions (base image, `USER nobody`, per-job profile churn), which every research file explicitly flags as needing empirical verification during implementation rather than being fully resolvable by research alone.

### Gaps to Address

- **`DOCUMENT_ENGINE_TIMEOUT` exact value:** 300s is a reasoned starting default (per STACK.md's derivation), not a verified number - load-test against real internal worst-case documents before locking it in during Phase 2/3 planning.
- **Process-group-kill verification against real `soffice`/`soffice.bin`:** Never empirically tested in this codebase (unlike libvips) - must be an explicit integration test in Phase 2, not an assumption inherited from `exec.go`'s doc comment.
- **Cold-start latency under the per-job-fresh-profile model:** Existing benchmarks (5-12s cold, 1-2s warm) come from setups that reuse a warm listener or warm OS page cache - they do not clearly establish behavior for a fresh `-env:UserInstallation` profile on every single invocation, which is the recommended model here. Benchmark this specifically during Phase 2.
- **Resource-isolation decision (Pitfall 6, shared vs. separate worker container/concurrency ceiling for `document`):** Explicitly undecided in research - PITFALLS.md and ARCHITECTURE.md both flag this as requiring a deliberate, recorded roadmap decision (ship a mitigation, or document as accepted residual risk like CAD/HTML->PDF) rather than silent inheritance of the current shared 2 CPU/1 GiB limits.
- **Exact Docker image size/build-time delta:** Directionally "several hundred MB" across community sources but not independently verified against an authoritative number - measure via an actual `docker build` during Phase 2 rather than trusting the estimate for capacity planning.
- **Exact LibreOffice macro-security registry keys/CLI flags:** LOW-MEDIUM confidence in FEATURES.md - re-verify against the actual pinned LibreOffice version in the Dockerfile during Phase 2 implementation, not from training-data key names.

## Sources

### Primary (HIGH confidence)
- Debian security tracker (libreoffice source package) - CVE-2024-12425/12426, CVE-2025-1080/0514 confirmed fixed at the pinned bookworm package version
- packages.debian.org (`libreoffice-nogui`, `libreoffice-writer/calc/impress-nogui`, `fonts-liberation2`) - exact versions, dependency lists, package roles
- LibreOffice Help - File Conversion Filter Names (official docs) - export filter names (`writer_pdf_Export` etc.)
- OASIS OpenDocument v1.2 Part 3 spec, Section 17.4 - ODF fixed-offset `mimetype` file requirement
- Existing codebase, read directly: `internal/convert/{exec,sniff,dimensions,convert,libvips,converters}.go`, `internal/worker/worker.go`, `internal/queue/{queue,client}.go`, `internal/api/handlers.go`, `internal/reconciler/reconciler.go`, `cmd/worker/main.go`, `Dockerfile.worker`, `.planning/PROJECT.md`

### Secondary (MEDIUM confidence)
- Gotenberg documentation, GitHub issues/discussions (#94, #1023, Configuration/Troubleshooting docs) - production reference implementation for LibreOffice-as-a-service concurrency/lifecycle tradeoffs
- LibreOffice bugzilla #52125, #82775, #95843, #106134 - exit-code-0-on-failure, concurrent-jobs crash, zombie-process history
- Ask LibreOffice community threads - `-env:UserInstallation` lock-file mitigation, password-protected-file silent failure, headless hang-on-corrupt-input
- Debian wiki - `SubstitutingCalibriAndCambriaFonts`; The Document Foundation blogs - font substitution and Microsoft-font-replacement guidance
- buer.haus - "A Tale of Exploitation in Spreadsheet File Conversions" - real-world conversion-pipeline exploitation precedent informing the macro/remote-fetch hardening recommendation

### Tertiary (LOW confidence)
- Community-reported cold-start benchmark figures (shelfio/libreoffice-lambda-layer, vladholubiev/serverless-libreoffice) - not reproduced against this project's exact fresh-profile-per-job pattern
- Docker image size delta ("several hundred MB") - directionally consistent across sources, no authoritative figure

---
*Research completed: 2026-07-09*
*Ready for roadmap: yes*
