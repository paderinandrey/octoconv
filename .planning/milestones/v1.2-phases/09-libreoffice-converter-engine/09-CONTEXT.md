# Phase 9: LibreOffice Converter Engine - Context

**Gathered:** 2026-07-09
**Status:** Ready for planning

<domain>
## Phase Boundary

The worker can turn a document accepted by Phase 8's safety gate into a trustworthy PDF via LibreOffice headless, and never leaves an orphaned `soffice` process behind. This phase covers: `LibreOfficeConverter` implementing the existing `Converter` interface (`Pairs()`/`Convert()`), per-job LibreOffice profile isolation, output-file validation before treating a conversion as successful, the outdir-basename-to-outPath rename LibreOffice's CLI requires, `Dockerfile.worker` provisioning (LibreOffice packages, fonts, `HOME`/fontconfig-cache for `USER nobody`), and an explicit integration test proving the existing hardened process-group-kill wrapper (`internal/convert/exec.go`) actually terminates a real `soffice`/`soffice.bin` process on timeout. It does NOT cover: registering the converter in the queue/worker pipeline as a reachable, routed engine (Phase 10's `cmd/document-worker` wiring), the reconciler becoming engine-aware (Phase 10), or `handleCreateJob` routing accepted documents to any queue (Phase 11) — this phase builds and proves the converter in isolation against the `Converter` interface and a live Docker environment, it does not make documents reachable end-to-end through the API yet.

</domain>

<decisions>
## Implementation Decisions

### Timeout
- **D-01:** `DOCUMENT_ENGINE_TIMEOUT` defaults to 300 seconds (2.5x the existing `ENGINE_TIMEOUT` default of 120s). This is milestone-research's reasoned starting default (STACK.md), not empirically validated against real internal worst-case documents — no representative large internal docx/xlsx/pptx corpus is available in this environment to benchmark against. Accepted as the locked default for v1.2; revisit if real production documents demonstrate it's too tight or unnecessarily generous. Configurable via env var, following the existing `ENGINE_TIMEOUT` pattern exactly.

### Output validation
- **D-02:** Before marking a document conversion successful, `LibreOfficeConverter.Convert` must verify the output file is non-zero size AND begins with the `%PDF-` magic bytes — reusing the project's existing magic-byte-validation philosophy (Phase 4's `sniff.go`) applied to the engine's own output instead of client input. This directly defends against LibreOffice's documented "exit 0 but empty/corrupt output" failure mode (RESEARCH.md Pitfall 2 from milestone research). A more thorough check (trailing `%%EOF` presence, structural PDF validation) was considered and explicitly rejected as over-engineering for this phase — the size+magic-bytes check catches the specific, well-documented LibreOffice failure mode this defends against; deeper PDF structural validation is not currently a known/observed failure mode and would add complexity without a demonstrated need.

### Process-group-kill verification
- **D-03:** This phase's plan MUST include a live integration test against a real Docker environment with LibreOffice actually installed — not a unit test with a mocked/stubbed process. Rationale: `internal/convert/exec.go`'s hardened `Setpgid`+SIGKILL-on-timeout wrapper has only ever been proven against libvips; milestone research flagged that LibreOffice's launcher may fork rather than exec, potentially escaping the process group and silently defeating the timeout safety net (a launch-blocking pitfall, not a nice-to-have). The test must: rebuild `Dockerfile.worker` (or a Phase-9-scoped variant) with LibreOffice installed, trigger a conversion that hangs/exceeds `DOCUMENT_ENGINE_TIMEOUT`, let the hardened exec wrapper's timeout fire, and assert via `ps`/process inspection that zero `soffice`/`soffice.bin` processes survive. This mirrors the project's established live-verification discipline (v1.0/v1.1's docker-compose smoke tests) rather than relying solely on unit tests, since no pure-Go unit test can validate real OS process-group behavior against a real LibreOffice binary.

### Claude's Discretion
- Exact package set and font packages for `Dockerfile.worker` — already well-specified by milestone research (STACK.md: `libreoffice-writer-nogui`/`libreoffice-calc-nogui`/`libreoffice-impress-nogui`, `fonts-crosextra-carlito`/`caladea`, `fonts-liberation2`), but researcher/planner should re-verify exact current package availability against the pinned Debian bookworm release at implementation time.
- Exact `HOME`/fontconfig-cache provisioning mechanics for `USER nobody` (build-time fontconfig cache pre-build vs. runtime) — technical implementation detail per milestone research's Pitfall 5.
- Whether the live process-group-kill integration test (D-03) lives as a Go test gated behind an environment flag (mirroring Phase 6's `DATABASE_URL`-gated soak tests) or as a separate shell-script/docker-compose-driven verification step — planner's call, informed by what's mechanically testable within a `go test` process versus what genuinely requires shelling out to `docker` / `ps` from outside Go's process tree.
- Exact profile-isolation directory derivation (`-env:UserInstallation=file://<path>`) — milestone research already established this reuses the existing per-job `os.MkdirTemp` workDir; planner to confirm the exact derivation point in `internal/worker/worker.go`'s job lifecycle once Phase 10 wires the document worker (this phase can build `LibreOfficeConverter` to accept the isolation path as a parameter without needing Phase 10's worker wiring to exist yet).

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Project & Requirements
- `.planning/PROJECT.md` — Current Milestone v1.2 section
- `.planning/REQUIREMENTS.md` — `DOC-04`, `DOC-05`, `DOC-06` (locked v1.2 scope for this phase)
- `.planning/ROADMAP.md` — Phase 9 goal, success criteria

### Research (milestone-level — already resolved most of the "how")
- `.planning/research/SUMMARY.md` — Phase 2 (mapped to this Phase 9) rationale, critical pitfalls 2/3/4/5/7
- `.planning/research/STACK.md` — Exact `soffice` invocation syntax, package set, font packages, concurrency/lock mitigation (`-env:UserInstallation`), timeout guidance derivation
- `.planning/research/PITFALLS.md` — Profile-lock concurrency bug, exit-0-on-failure, process-group-kill verification gap, unprivileged-user environment provisioning, font/version drift (accepted limitation)

### Prior Phase Context (the pattern this phase extends)
- `.planning/phases/08-document-content-safety-format-detection/08-CONTEXT.md` / `08-RESEARCH.md` — the safety gate this phase's converter receives already-validated input from (documents reaching `LibreOfficeConverter` have passed `SniffContainer`'s format/zip-bomb/macro checks — Phase 9 does not need to re-validate content safety, only produce a trustworthy PDF from already-trusted input)
- `.planning/milestones/v1.0-phases/04-content-validation-storage-lifecycle-observability/04-CONTEXT.md` — magic-byte validation philosophy this phase's D-02 output-check directly extends (applied to engine output instead of client input)

### Existing Codebase (reference patterns to follow)
- `internal/convert/convert.go` — `Converter` interface (`Pairs() []Pair`, `Convert(ctx, inPath, outPath, opts) error`), `Registry`/`convert.Default` — the interface `LibreOfficeConverter` must implement
- `internal/convert/libvips.go` — the only existing `Converter` implementation; direct structural analog for `LibreOfficeConverter`
- `internal/convert/exec.go` — hardened `runCommand` (Setpgid + process-group SIGKILL on context timeout) — reused unchanged for LibreOffice invocation; this phase's D-03 integration test is the first real proof this wrapper works against a non-libvips process
- `internal/convert/converters.go` — `init()` registration pattern; `LibreOfficeConverter` gets registered here (though Phase 9 doesn't need it reachable via the live pipeline yet — Phase 10/11 do the routing)
- `Dockerfile.worker` — current `debian:bookworm-slim` base + `libvips-tools`; this phase adds the LibreOffice/font packages and `HOME`/fontconfig provisioning per D-03's live test needing a real installed binary

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `internal/convert/exec.go`'s `runCommand` — unchanged, reused directly for the `soffice` invocation
- The existing per-job `os.MkdirTemp` workDir pattern in `internal/worker/worker.go` — the natural root for the `-env:UserInstallation` profile-isolation directory (Phase 10 wires this; Phase 9's `LibreOfficeConverter` accepts the path as a parameter)
- `internal/convert/sniff.go`'s magic-byte philosophy — direct precedent for D-02's output-validation approach

### Established Patterns
- One-file-per-converter-implementation (`libvips.go`) — `libreoffice.go` follows the same shape
- Hardened external process execution via `runCommand` — no new process-exec code needed, just a new caller

### Integration Points
- `internal/convert/libreoffice.go` (new) — the `LibreOfficeConverter` implementation
- `internal/convert/converters.go` — registration (inert until Phase 10/11 route documents to it)
- `Dockerfile.worker` — LibreOffice packages, fonts, `HOME`/fontconfig-cache provisioning
- A live integration test (mechanism per Claude's Discretion above) proving process-group-kill against real `soffice`

</code_context>

<specifics>
## Specific Ideas

No UI/UX references — backend-only, engine-implementation phase, same character as the libvips converter build-out (originally part of the pre-v1.0 vertical slice). Concrete asks: `LibreOfficeConverter` for all 6 document formats → pdf, per-job profile isolation, output validation (size + magic bytes), a 300s default timeout, and a live Docker-based integration test proving the timeout-kill guarantee against a real `soffice` process.

</specifics>

<deferred>
## Deferred Ideas

None raised this phase — REQUIREMENTS.md's v2/Out-of-Scope sections already capture relevant future items (DOC-V2-03 PDF/A export via `opts`, DOC-V2-05 active complexity-based anti-DoS) from the milestone-level discussion.

</deferred>

---

*Phase: 9-LibreOffice Converter Engine*
*Context gathered: 2026-07-09*
