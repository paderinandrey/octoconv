# Project Research Summary

**Project:** OctoConv
**Domain:** Async internal file-conversion service (Go) — v1.3 milestone extends the existing document engine class and adds a third (chromium HTML→PDF) engine class, plus a cross-cutting webhook-delivery reliability fix
**Researched:** 2026-07-10
**Confidence:** MEDIUM-HIGH

## Executive Summary

OctoConv v1.3 "Document Class v2" is not a new product but a well-scoped extension of an already-shipped, production-hardened async conversion pipeline (chi API → Postgres-first double write → asynq engine-class queues → worker → converter registry → S3). Research across all four dimensions converges on the same conclusion: every one of the five target features (cross-format LibreOffice conversion, OLE-CFB pre-flight rejection, opts-driven PDF/A export, a new chromium-based HTML→PDF engine class, and webhook-delivery decoupling) fits cleanly into existing abstractions (`Converter`/`Registry`, `runCommand`, engine-class-per-binary/container, `jobs.options jsonb`) with **zero new Go dependencies** — this milestone is CLI-integration and stdlib work, consistent with the project's established philosophy. The stack, feature, and architecture research all independently arrive at the same recommended shape: extend `LibreOfficeConverter` to be target-format-aware rather than PDF-hardcoded, reuse the existing `SniffContainer` primitive for both CFB pre-flight rejection and output validation, validate `opts` against a closed allow-list rather than passthrough, and treat the new `chromium-headless-shell` engine as a fourth "engine class" following the v1.2 document-engine template (own binary, own container, own queue, `Engine()`/`EngineFor` routing).

The single highest-risk item, flagged consistently by all four researchers, is the HTML→PDF engine: unlike libvips/LibreOffice, headless Chromium is an **active** network actor — it will fetch every `<img src>`/`<link>`/`fetch()` target referenced inside client-submitted HTML at render time, which is a materially larger and qualitatively different SSRF surface than the URL-string validation already built for `callback_url`. Pitfalls research goes further than architecture research here: it argues the safest fail-closed control is protocol-level network interception (driving Chromium via CDP/`chromedp` and denying all non-`file://` requests), which is a real architectural divergence from the project's one-shot `runCommand` pattern used by every other engine — this needs to be a named design decision made early, not discovered mid-implementation, and network-level container/compose egress restriction should be layered on top regardless of which application-level control is chosen. A second cross-cutting risk, independently surfaced by feature and pitfalls research, is that "decouple webhook delivery" can be satisfied in name while recreating the exact single-point-of-failure it's meant to fix (a lone new `webhook-worker` process is still one process) — the milestone's success criteria should explicitly require redundancy (≥N consumers) and a single surviving reconciler-sweeper instance, not just a topology relocation.

Overall confidence is MEDIUM-HIGH: the LibreOffice/CFB/webhook-topology findings are HIGH confidence (grounded directly in the current codebase plus official LibreOffice docs and corroborated OOXML-encryption research), while the chromium/HTML-PDF safety model and a couple of narrow LibreOffice filter-behavior details (whether `SelectPdfVersion` behaves identically across `writer_pdf_Export`/`calc_pdf_Export`/`impress_pdf_Export`) are MEDIUM confidence and flagged for smoke-testing/design-decision during phase execution rather than treated as settled.

## Key Findings

### Recommended Stack

No new Go modules are required for any of the four v1.3 stack changes. The new pieces are all OS-package/CLI additions layered onto the existing hardened-exec pattern (`internal/convert/exec.go`'s `runCommand`):

**Core technologies:**
- `chromium-headless-shell` (Debian bookworm package, ~150.x) — third engine class (HTML→PDF); the "old headless" implementation split out of `chromium` at Chrome 132 specifically to keep a CLI-only `--print-to-pdf` flag with no DevTools driving required — exactly the shape this project's `runCommand` pattern needs. Reject the full `chromium` package (5x larger, pulls in GUI-stack deps) and reject `wkhtmltopdf` (unmaintained, weak modern CSS support — the exact gap the milestone cites for rejecting LibreOffice here).
- LibreOffice 7.4.7 (already pinned, no version bump) — already supports both the JSON `FilterData` CLI syntax for PDF/A (`SelectPdfVersion` enum 0/1/2/3/14-17) and native ODF/OOXML cross-format export filters (`writer8`/`calc8`/`impress8` and "MS ... 2007 XML" family) — zero new package, same `soffice` binary already shelled out to.
- stdlib `bytes.Equal` magic-byte check for the OLE-CFB signature (`D0 CF 11 E0 A1 B1 1A E1`) — a one-line check, same shape as existing `sniff.go` signature matching. Notable finding: this single check catches both legacy binary Office files *and* password-protected modern OOXML at once, since encrypted OOXML is itself CFB-wrapped per MS-OFFCRYPTO.
- Standalone `cmd/webhook-worker` binary + `Dockerfile.webhook-worker` — reuses `internal/webhook`, `internal/queue`, `internal/jobs` verbatim; structurally a trimmed copy of `cmd/worker/main.go` with the engine/storage wiring dropped.
- `tini` as PID 1 in the new HTML-worker container — reuse the exact pattern already proven for LibreOffice's `oosplash`→`soffice.bin` fork chain in `Dockerfile.document-worker`.

**Explicitly rejected:** any Go CDP-driver library (`chromedp`/`rod`/`playwright-go`) at the stack-recommendation level (introduces a persistent-process lifecycle model foreign to this codebase) — though pitfalls research flags this as the *only* robust way to get protocol-level network blocking, so it may need to be revisited as a security-driven exception during planning (see Critical Pitfalls below); a Go CFB-parsing library (a single 8-byte comparison suffices for reject-only detection); and registering `TypeWebhookDeliver` in every new engine worker "just in case" (recreates the exact coupling bug being fixed).

### Expected Features

**Must have (table stakes, P1 — all five map to milestone requirements):**
- Cross-format pairs: docx↔odt, xlsx↔ods, pptx↔odp via the existing LibreOffice engine (DOC-V2-01) — mechanically identical to the existing →PDF path, just more `Pairs()` entries.
- CFB pre-flight rejection (422) covering both encrypted OOXML and legacy binary doc/xls/ppt (DOC-V2-02) — prevents silent `soffice` timeouts across *all* document engine inputs, not just the new pairs.
- PDF/A-1b export via `opts` (DOC-V2-03) — first real consumer of the already-existing, currently-unused `jobs.options jsonb` column.
- HTML→PDF: single self-contained HTML file input with page size/margins/landscape/printBackground/bounded `waitDelay` options (DOC-V2-04) — the option set every Chromium-based competitor (Gotenberg, Puppeteer/Playwright PDF) treats as its minimum surface.
- Webhook delivery decoupled from any single engine worker (SEED-002) — closes a verified, already-documented gap (`cmd/document-worker/main.go`'s own comment: "cmd/worker remains the sole webhook consumer").

**Should have (competitive, P2):**
- PDF/A-2b and PDF/A-3b as additional selectable conformance levels — trivial extension of the 1b plumbing (same `SelectPdfVersion` enum), add once a real client needs transparency/JPEG2000 or attachment embedding.
- Dedicated `webhook-worker` binary matching the project's own engine-class-per-binary convention.

**Defer (v2+, P3 or anti-features):**
- HTML→PDF via URL fetch — explicitly an anti-feature: reopens the SSRF surface in a worse form (a full browser fetching and executing JS against arbitrary internal targets), needs its own from-scratch allow/deny model, not a quick add.
- HTML+external-assets as a zip bundle — would relax the single-input/single-output architectural constraint; ship self-contained single-HTML-file input first.
- `waitForExpression` (arbitrary JS-condition wait before printing) — meaningfully larger sandbox-escape/DoS surface than a capped `waitDelay`; not needed for this audience yet.
- Fidelity/diff reporting on cross-format conversions — large open-ended problem; no surveyed competitor (Gotenberg, CloudConvert, ConvertAPI, Zamzar) attempts this either; document known gaps in prose instead.

### Architecture Approach

The five features graft onto four distinct integration points, all verified against the current `main` codebase. (a) Worker output-naming/Content-Type logic is **already fully generalized** (derives from `job.TargetFormat`, not hardcoded `"pdf"`) — the entire cross-format gap lives inside `LibreOfficeConverter.Convert` (`Pairs()`, `filterFor`, the `--convert-to` invocation, and `validatePDF` all currently hardcode PDF). (b) `opts` plumbing requires five small, mechanical, already-scoped changes across `jobs.go`/`repo.go`/`handlers.go`/`worker.go`/`libreoffice.go` — the DB column already exists and is inert. (c) OLE-CFB detection slots in as a third detection branch in `handleCreateJob`'s existing sniff chain (alongside the magic-byte and ZIP branches), deliberately *not* registered through `Converter`/`Registry` since it's an unconditional reject, not a supported format. (d) The `jobs.engine` Postgres CHECK constraint is a closed list that does not include `html`/`chromium` — a migration is a hard prerequisite for any chromium-engine work, and must land in the same wave as new `internal/queue` scaffolding (task type, queue, retry schedule) before the converter/container work.

**Major components (new/changed):**
1. `internal/convert/libreoffice.go` — becomes target-format-aware (filter selection, produced-file extension, output validation) instead of PDF-only.
2. `internal/convert/olecfb.go` (new) — CFB magic-byte pre-flight check, called from `internal/api/handlers.go`, never registered in the `Converter` registry.
3. `internal/jobs`/`internal/api`/`internal/worker` — coordinated `opts` plumbing (API parse+validate → DB column → `Converter.Convert` parameter, replacing today's hardcoded `nil`).
4. `internal/convert/chromium.go` + `cmd/chromium-worker` + `Dockerfile.chromium-worker` (new) — fourth engine class, fits the `Converter` interface with zero interface changes; requires the `jobs.engine` CHECK-constraint migration and new queue/task-type scaffolding first.
5. `cmd/webhook-worker` + `Dockerfile.webhook-worker` (new) — dedicated, decoupled webhook-queue consumer, migrated in safely (asynq is pull-based — old and new consumers can run simultaneously with zero double-delivery risk) before removing registration from `cmd/worker`.

### Critical Pitfalls

1. **Hardcoded `.pdf` assumptions silently survive a naive cross-format implementation** — `producedPath` and `validatePDF` must both become functions of the *target* format, not literals; grep for remaining `.pdf` literals in `libreoffice.go` as a completion check. Must land in the same change as the first cross-format pair registration (registering the pair before generalizing validation makes every non-PDF-target job guaranteed-fail).
2. **CFB magic bytes alone can't distinguish "legacy binary" from "password-protected modern OOXML"** — both share the identical 8-byte header (encrypted OOXML is itself CFB-wrapped). A generic reject message is an acceptable interim step only if explicitly documented as such; the more precise fix (parsing CFB directory stream names) is real, non-trivial new parsing work with no Go stdlib support — this should get its own build-vs-depend decision logged in PROJECT.md's Key Decisions, not be folded into the same estimate as the ZIP-based sniffing work it superficially resembles.
3. **`opts` is a brand-new, currently-completely-unguarded plumbing path — the injection risk is UNO filter-property injection, not shell injection.** Since `exec.Command` uses argv arrays, shell metacharacters aren't the threat; the threat is a client supplying arbitrary LibreOffice filter properties (e.g., an output encryption password) if `opts` is marshaled anywhere close to verbatim into the filter-options JSON. Must be parsed into a small closed Go struct with an allow-list before reaching the CLI invocation — flagged as the single highest-severity net-new attack surface in this milestone, requiring an explicit security-review gate before merge.
4. **Headless Chromium's default network behavior directly contradicts "offline rendering"** — vanilla `--print-to-pdf` will fetch every absolute-URL resource referenced by the HTML/CSS/JS at render time (images, stylesheets, scripts, iframes), including raw IP literals to internal/metadata endpoints that a URL-string validation approach (like the existing `callback_url` SSRF guard) cannot catch, since it only applies to one static, pre-render string. This must be treated as a named design decision (protocol-level network blocking, e.g. via CDP interception, and/or container-level network egress restriction) made before the Dockerfile/compose topology is finalized, not discovered mid-implementation.
5. **"Decoupling" webhook delivery into exactly one new dedicated process just relocates the single point of failure it's meant to fix** — and naively running the existing reconciler `Sweeper` in every redundant webhook-consumer replica reintroduces a double-sweep race the project already identified and avoided once for stale-job recovery. The milestone's success criteria must explicitly require ≥2 independent webhook-queue consumers (asynq natively supports N consumers on one queue — this is free) and exactly one active sweeper instance fleet-wide, verified with a rolling-deploy test that kills one replica mid-traffic.

## Implications for Roadmap

Based on combined research, suggested phase structure (five feature phases plus tech-debt-first, per PROJECT.md's stated milestone plan):

### Phase 0: Tech debt cleanup (WR-02/03/04, gofmt, docker-compose audit)
**Rationale:** Explicitly called out in PROJECT.md as "tech-debt фаза первой" — carries no dependency on the new features and clears the runway before the riskier engine work.
**Delivers:** E2E `extra_hosts` fix, engine-name constants, E2E client timeouts, formatting/compose hygiene.
**Research flags:** None — standard cleanup, no research needed.

### Phase 1: Cross-format document conversion (DOC-V2-01) + output validation generalization
**Rationale:** Lowest architectural risk of the five features (reuses the existing LibreOffice engine and `Converter` interface with zero interface changes), and its side effect — generalizing `validatePDF`/`filterFor`/produced-path logic to be target-format-aware — is a **hard prerequisite** for PDF/A (Phase 2), since both touch the same code path.
**Delivers:** docx↔odt, xlsx↔ods, pptx↔odp round-trip conversion; target-aware output validation reusing `SniffContainer`.
**Addresses:** DOC-V2-01 (FEATURES.md P1).
**Avoids:** Pitfall 1 (hardcoded `.pdf`), Pitfall 3 (filter-name matrix), Pitfall 4 (cheap output validity check for ZIP-based targets).
**Research flags:** Needs a build-time verification item — LibreOffice filter-name matrix (`(sourceFormat, targetFormat) → filterName`) should be smoke-tested per-pair against the actual deployed LibreOffice version rather than assumed from training data.

### Phase 2: OLE-CFB pre-flight rejection (DOC-V2-02)
**Rationale:** Independent of cross-format work (pure API-layer addition, no DB/queue/worker changes) but should land early since it protects *all* document engine inputs, including the existing →PDF path and the new cross-format pairs from Phase 1 — feature research explicitly frames this as "should really be scoped as a fix for all document engine inputs."
**Delivers:** A new `internal/convert/olecfb.go` detection branch in `handleCreateJob`'s sniff chain, with a distinct 422 message from the generic "unrecognized content" case.
**Addresses:** DOC-V2-02 (FEATURES.md P1).
**Avoids:** Pitfall 5 (CFB can't distinguish legacy vs. encrypted without deeper parsing), Pitfall 6 (zero-new-deps wall — needs an explicit build-vs-depend decision, not silent scope creep into a full OLE2 library).
**Research flags:** Needs a phase-planning decision, logged as a Key Decision in PROJECT.md — minimal stream-name-only CFB reader (hand-rolled, keeps zero-deps) vs. a generic-message-only interim (deferring the legacy/encrypted distinction) vs. a small audited third-party CFB library.

### Phase 3: PDF/A opts-driven export (DOC-V2-03)
**Rationale:** Depends on Phase 1's target-format-aware output-validation plumbing and is the first real consumer of `jobs.options jsonb` — should design the `opts` closed-allowlist validation pattern here (smaller surface, one engine) before HTML→PDF (Phase 4) needs a larger option set and would otherwise have to retrofit it.
**Delivers:** Full `opts` chain (API parse+validate → DB → worker → `Converter.Convert`), PDF/A-1b export via `SelectPdfVersion`, with `EmbedStandardFonts` forced alongside it.
**Addresses:** DOC-V2-03 (FEATURES.md P1); sets up PDF/A-2b/3b (P2) as a near-free follow-on.
**Avoids:** Pitfall 7 (font embedding not implied by `SelectPdfVersion` alone), Pitfall 8 (magic-bytes validation proves nothing about true PDF/A conformance — document this as an accepted residual-risk limitation), Pitfall 9 (opts as a UNO-filter-property injection surface — the milestone's single highest-severity item, needs an explicit security-review gate).
**Research flags:** Needs deeper research/security review — the closed-allowlist `opts` design and its injection-surface implications should get explicit scrutiny before merge, not just standard code review.

### Phase 4: Chromium HTML→PDF engine (DOC-V2-04)
**Rationale:** Explicitly the riskiest item per the milestone's own framing; requires the `jobs.engine` CHECK-constraint migration and new queue/task-type scaffolding as hard prerequisites, and needs the safety-model decision (network blocking mechanism) resolved before the container/compose topology is finalized. Sequenced last among the feature phases so the `opts` plumbing (Phase 3) and engine-class conventions are already proven.
**Delivers:** `jobs.engine` migration adding `html`, new queue/task-type scaffolding, `internal/convert/chromium.go`, `cmd/chromium-worker` + `Dockerfile.chromium-worker`, page/margin/background/`waitDelay` opts.
**Uses:** `chromium-headless-shell` (STACK.md), `tini`-as-PID-1 pattern, `runCommand` hardened exec (unchanged).
**Implements:** Fourth engine-class following the v1.2 document-engine template (own binary, container, queue, `Engine()`/`EngineFor` routing).
**Research flags:** Needs its own dedicated research/design pass during phase planning — specifically the network-egress/SSRF-equivalent control (CDP-driven interception vs. CLI flags + container/network-level egress restriction) is a genuine architectural fork not resolved by this research round; also verify live whether Chromium's `--print-to-pdf` mode actually needs `tini` (unlike LibreOffice's confirmed fork chain, this is flagged as "should be confirmed live during implementation," not assumed).

### Phase 5: Webhook delivery decoupling (SEED-002)
**Rationale:** Orthogonal to all four engine-feature phases (conflicts with nothing, blocks nothing) — feature research recommends landing it early "since it's flagged as a standalone reliability gap, not tied to the new engine work," and architecture/pitfalls research agrees it should resolve *before* a third/fourth engine binary exists, so the "does this binary deliver webhooks?" question has a single settled answer going forward. Sequenced here (or could be moved earlier/parallel) mainly to keep the roadmap narrative aligned with PROJECT.md's stated ordering, but has no hard dependency forcing it last.
**Delivers:** New `cmd/webhook-worker` + `Dockerfile.webhook-worker`, deployed redundantly (≥2 consumers) alongside the existing `cmd/worker` registration, then old registration removed once proven live (safe zero-downtime migration since asynq consumers pull rather than push).
**Addresses:** SEED-002 (FEATURES.md P1, a verified existing gap).
**Avoids:** Pitfall 13 (single relocated consumer = same failure mode, just moved), Pitfall 14 (reconciler sweeper singleton must not be duplicated across redundant consumers).
**Research flags:** Needs an explicit topology decision recorded as a Key Decision before implementation — where the singleton sweeper lives once webhook consumption is multi-process (leader-election-style single-replica-runs-it convention, vs. extracting the sweeper into its own always-single-instance process).

### Phase Ordering Rationale

- Cross-format conversion (Phase 1) must precede PDF/A (Phase 3) because both touch the same `libreoffice.go` output-validation code path — generalizing it once, correctly, avoids the "hardcoded .pdf" pitfall biting twice.
- CFB pre-flight (Phase 2) has zero code dependency on the other phases and should land early so cross-format pairs (Phase 1) don't ship with the same latent silent-timeout gap the existing →PDF path already has.
- The `opts` validation pattern (Phase 3) should exist before HTML→PDF (Phase 4) needs its own, larger opts schema (page size/margins/background) — designing it once against the smaller PDF/A surface avoids a retrofit.
- The chromium engine (Phase 4) is sequenced last among feature work because it has the most open architectural question (network-blocking mechanism) and the largest new-container surface — everything else de-risks incrementally first.
- Webhook decoupling (Phase 5) is architecturally independent but should resolve before a fourth engine binary exists, so no new binary is ever tempted to "just also consume the webhook queue."

### Research Flags

Needs research (deeper technical/design research during `/gsd:plan-phase --research-phase <N>`):
- **Phase 2 (CFB detection):** zero-new-deps vs. hand-rolled CFB parser vs. small third-party library — a real build-vs-depend decision, not a standard pattern.
- **Phase 3 (PDF/A + opts):** closed-allowlist `opts` design and its injection-surface security review — the milestone's single highest-severity net-new attack surface.
- **Phase 4 (chromium HTML→PDF):** the network-blocking/SSRF-equivalent safety model (CDP interception vs. CLI-flag + container egress restriction) — explicitly flagged by architecture and pitfalls research as unresolved and needing a named design decision, plus live verification of Chromium's process-tree/PID-1 behavior.

Phases with standard patterns (skip research-phase, straightforward given existing precedent):
- **Phase 0 (tech debt):** standard cleanup work.
- **Phase 1 (cross-format conversion):** mechanically identical to the existing v1.2 LibreOffice→PDF path; only needs a smoke-test verification pass on filter names, not fresh research.
- **Phase 5 (webhook decoupling):** asynq's pull-based multi-consumer semantics are well-documented and already understood from this research round; the main work is a topology decision (recorded as a Key Decision) rather than open technical research.

## Confidence Assessment

| Area | Confidence | Notes |
|------|------------|-------|
| Stack | MEDIUM-HIGH | All four stack areas verified against official/Debian sources; the LibreOffice `SelectPdfVersion` behavior across `calc_pdf_Export`/`impress_pdf_Export` (vs. only `writer_pdf_Export`) is flagged as MEDIUM confidence, not independently smoke-tested — recommend a one-time smoke test per source format during phase execution. |
| Features | MEDIUM-HIGH | HIGH confidence for LibreOffice/CFB mechanics and current-codebase gaps (verified by direct source reads); MEDIUM confidence for competitor-API conventions (Gotenberg, CloudConvert, ConvertAPI, Zamzar) verified via WebSearch/WebFetch, not Context7 library coverage — no formal library docs exist for this domain shape. |
| Architecture | HIGH | All findings grounded in direct reads of the current `main` codebase; two load-bearing external claims (LibreOffice CLI PDF/A filter syntax, headless Chromium Docker sandboxing) are MEDIUM confidence but corroborated across multiple independent sources. |
| Pitfalls | HIGH | Grounded in direct codebase reads plus official LibreOffice docs and verified OOXML/CFB-encryption security research; two pitfalls (LibreOffice cross-format fidelity specifics) rely on community bug-tracker reports rather than official guarantees — explicitly flagged MEDIUM inline in that research. |

**Overall confidence:** MEDIUM-HIGH

### Gaps to Address

- **Chromium network-blocking mechanism (Phase 4):** research surfaces two options (CDP-driven request interception via `chromedp`, vs. CLI-flag + container/network-level egress restriction) but does not resolve which one OctoConv should adopt — this directly conflicts with the stack recommendation's "zero new Go deps" preference (CDP driving requires `chromedp`) and needs an explicit, documented trade-off decision during Phase 4 planning, not an assumption either way.
- **LibreOffice PDF/A filter-parameter behavior across source app families:** `SelectPdfVersion`'s behavior is verified for `writer_pdf_Export` but only inferred (not independently tested) for `calc_pdf_Export`/`impress_pdf_Export` — a one-time smoke test per source format (docx/xlsx/pptx → PDF/A-2b) should happen early in Phase 3/1 execution before this is treated as load-bearing.
- **CFB legacy-vs-encrypted distinction depth:** whether Phase 2 ships the simpler "one generic reject message" (interim, needs to be explicitly documented as such) or the more precise CFB-directory-stream-name parsing (real new parsing code, no Go stdlib support) is an open scope decision, not resolved by this research — should be a named Key Decision in PROJECT.md before implementation.
- **Whether `tini` is actually required for the chromium container:** unlike LibreOffice's confirmed `oosplash`→`soffice.bin` fork chain, headless Chromium's `--print-to-pdf` process-tree behavior in this exact invocation shape has not been confirmed live — treat the "no tini needed" architecture-research finding as a hypothesis to verify, not a settled fact, mirroring how the project confirmed `tini`'s necessity live for LibreOffice (per `Dockerfile.document-worker`'s own "confirmed live (09-02)" comment).
- **Webhook-consumer redundancy topology:** the exact mechanism for "≥2 consumers, exactly 1 sweeper" (fixed replica count vs. leader election vs. sweeper extracted to its own singleton process) is left as an implementation-time decision by all four researchers — this should be resolved and logged as a Key Decision early in Phase 5, since it shapes the new binary's structure.

## Sources

### Primary (HIGH confidence)
- Direct reads of current `main` branch: `internal/convert/{convert,converters,libreoffice,docsniff,sniff,dimensions,exec}.go`, `internal/api/{handlers,api,callbackurl}.go`, `internal/worker/worker.go`, `internal/jobs/{jobs,repo}.go`, `internal/queue/{queue,client}.go`, `internal/reconciler/reconciler.go`, `internal/webhook/deliver.go`, `internal/db/migrations/0001_init.sql`, `cmd/{api,worker,document-worker}/main.go`, `Dockerfile.{worker,document-worker}`, `docker-compose.yml`, `.planning/PROJECT.md`
- `packages.debian.org/bookworm/chromium-headless-shell`, `packages.debian.org/bookworm/chromium` — official Debian archive package data
- `developer.chrome.com/docs/chromium/headless` — official Chrome for Developers docs on the old/new headless split at Chrome 132
- `help.libreoffice.org/latest/en-US/text/shared/guide/pdf_params.html` and `.../convertfilters.html` — official LibreOffice CLI filter-parameter and filter-name reference
- MS-OFFCRYPTO / Didier Stevens' "Encrypted OOXML Documents", SANS ISC diary, Aspose docs — corroborated CFB/OOXML-encryption container structure

### Secondary (MEDIUM confidence)
- `vmiklos.hu/blog/pdf-convert-to.html` — LibreOffice core contributor's account of the CLI JSON filter-parameter feature landing in the 7.4 release cycle
- Gotenberg official docs (PDF/A, HTML-to-PDF, URL-to-PDF) — competitor API convention modeling for `opts` schema and HTML input model
- ConvertAPI/api2convert response-code docs — competitor "password protected" error-code conventions
- oneuptime.com, hexdocs ChromicPDF, docker-html-to-pdf reference project — Docker/headless-Chrome container hardening conventions (sandbox, `/dev/shm`, fonts)
- Hookdeck webhook-architecture writeups — decoupled ingestion/delivery worker pattern (industry practice, not formal spec)
- hibiken/asynq wiki — weighted queue / multi-consumer-same-queue semantics (community-verified, matches existing project usage)

### Tertiary (LOW confidence)
- Community bug-tracker reports (bugs.documentfoundation.org, ask.libreoffice.org, forum.openoffice.org threads) on LibreOffice docx↔odt / xlsx↔ods fidelity-loss specifics — explicitly flagged as not official LO guarantees, needs no further validation (documented as an accepted, expected limitation rather than a bug to fix)

---
*Research completed: 2026-07-10*
*Ready for roadmap: yes*
