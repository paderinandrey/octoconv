# Pitfalls Research

**Domain:** Document Class v2 — cross-format LibreOffice conversion, OLE-CFB pre-flight detection, opts-driven PDF/A export, chromium-based HTML→PDF engine, decoupled webhook delivery (OctoConv milestone v1.3)
**Researched:** 2026-07-10
**Confidence:** HIGH (grounded in current codebase — `internal/convert/libreoffice.go`, `docsniff.go`, `sniff.go`, `internal/queue/queue.go`, `internal/reconciler/reconciler.go`, `internal/webhook/deliver.go`, `internal/api/callbackurl.go` — plus official LibreOffice docs and verified security research on OOXML/CFB encryption). Two pitfalls (LibreOffice cross-format fidelity specifics) rely on community bug-tracker reports rather than official docs — flagged MEDIUM inline.

This supersedes the previous milestone's PITFALLS.md (which covered v1.2's document-engine addition — LibreOffice→PDF only, reconciler engine-routing, worker-topology split — all now shipped and validated per `.planning/PROJECT.md`). This research is scoped entirely to v1.3's five features: cross-format document conversion, OLE-CFB pre-flight detection, PDF/A export, HTML→PDF via chromium, and webhook-delivery decoupling.

## Critical Pitfalls

### Pitfall 1: Hardcoded `.pdf` assumptions break the moment output isn't PDF

**What goes wrong:**
`internal/convert/libreoffice.go`'s `Convert` currently hardcodes the produced-file extension and the validator: `producedPath := ... + ".pdf"` and the function unconditionally ends with `return validatePDF(outPath)`. DOC-V2-01 (docx↔odt, xlsx↔ods, pptx↔odp) makes the target format variable for the first time. If the naive change is "just call `filterFor` with the target too," these two hardcoded assumptions silently survive: soffice writes `in.odt` (say), the code looks for `in.pdf`, the rename fails or — worse, if a stale `in.pdf` from a previous PDF-export test happens to exist in the reused workDir pattern — succeeds by picking up the wrong file. Then `validatePDF` checks for `%PDF-` magic bytes against a file that was never meant to be a PDF, and correctly errors — but for the wrong reason, making the failure look like a soffice bug instead of an output-handling bug.

**Why it happens:**
The v1.2 implementation only ever had one target format (PDF), so "the output is always `.pdf`" was baked in as an implicit invariant rather than a parameter. Cross-format conversion invalidates that invariant everywhere it was assumed, not just in `filterFor`.

**How to avoid:**
Make the produced-file extension and the output validator both functions of the *target* format, not literals. `filterFor(sourceExt, targetExt)` should return the correct LibreOffice export filter for the pair (see Pitfall 3 for the filter-name matrix), and `producedPath`/output validation must derive the extension from the same target format the filter was chosen for — never from a literal `.pdf`.

**Warning signs:**
Any grep for `".pdf"` as a string literal inside `internal/convert/libreoffice.go` after this milestone lands is a red flag — the file should have zero hardcoded target extensions once cross-format is wired.

**Phase to address:**
The phase implementing DOC-V2-01 (cross-format conversion), before opts/PDF/A work lands on top of it.

---

### Pitfall 2: LibreOffice cross-format conversion is not lossless — expect silent content/fidelity degradation, not hard failures

**What goes wrong:** *(MEDIUM confidence — community bug reports, not official LO guarantees)*
docx→odt and odt→docx round-trips are documented to silently drop or corrupt: alt-text on images, tab-stop spacing, tracked-changes state, and internal hyperlinks to slides/bookmarks. On the spreadsheet side, xlsx↔ods conversion is lossy for anything beyond ~250 of Excel's ~420 built-in functions, and PivotTables-with-slicers, complex conditional formatting, Power Query, and VBA macros have no ODS equivalent at all. Critically, none of this raises an error — `soffice --convert-to` exits 0 and produces a structurally valid file. The existing `validatePDF`-style approach ("did the process exit cleanly and produce a well-formed file") cannot catch fidelity loss, because fidelity loss is not a validity failure.

**Why it happens:**
LibreOffice's import/export filters are best-effort translators between two independently-evolved format families (Microsoft OOXML vs OASIS ODF); there is no cross-format feature parity guarantee, and LibreOffice's own philosophy is "convert what you can, drop what you can't" rather than "fail if anything is lost."

**How to avoid:**
Do not try to detect fidelity loss programmatically (out of scope, no cheap signal exists). Instead: (1) document explicitly in the API contract that docx↔odt / xlsx↔ods / pptx↔odp is "structural best-effort conversion, not guaranteed round-trip fidelity" so internal clients don't assume archival-grade equivalence; (2) the macro-rejection guard already in `internal/api/handlers.go` (`cr.HasMacro` → 422) is a correctly-scoped mitigation, since macros are the single highest-value silent-loss case (a macro-enabled workbook converted to `.ods` loses all VBA silently) — keep it, and confirm it applies to the new cross-format pairs (not just the existing →PDF pairs, since PDF never carried macros in the first place but odt/ods/odp export could).

**Warning signs:**
Support requests along the lines of "the converted file looks different from the original" with no error in `job_events` — this is expected behavior, not a bug, but needs to be documented as such before it surprises an internal consumer.

**Phase to address:**
The phase implementing DOC-V2-01 — as a documentation/scope decision, not a code fix.

---

### Pitfall 3: LibreOffice's PDF-export filter names don't work for non-PDF cross-format targets — need a real filter matrix

**What goes wrong:**
`filterFor` today only ever returns `writer_pdf_Export` / `calc_pdf_Export` / `impress_pdf_Export` — filters that are meaningless for a docx→odt or xlsx→ods conversion. The correct ODF export filters are `writer8`, `calc8`, `impress8`; the correct reverse (odt→docx etc.) filters are the "MS ... 2007 XML" family (e.g. `MS Word 2007 XML`, `Calc MS Excel 2007 XML`, `Impress MS PowerPoint 2007 XML`). These are different literal strings from the PDF-export ones, keyed by (source app family, target format), not just source format.

**Why it happens:**
It's tempting to extend the existing `switch` by source extension alone, since that's the current pattern — but the current pattern only ever needed the source's app family (Writer/Calc/Impress) because the target was always PDF. Now the filter depends on both source and target.

**How to avoid:**
Build an explicit `(sourceFormat, targetFormat) → filterName` table (mirroring the existing `Pair{From, To}` shape already used in `Registry`), verified once against real LibreOffice output per pair — don't assume the "8" ODF filters or "2007 XML" OOXML filters based on training-data recall alone; confirm against the target LibreOffice version's own `--convert-to :help`/filter list, since exact filter-name strings have shifted across LibreOffice major versions.

**Warning signs:**
`soffice` exits non-zero with an "Error: no export filter" message, or (more dangerously) exits 0 but writes zero bytes / an empty shell document, if a stale/incorrect filter name is silently accepted for a different app family than intended.

**Phase to address:**
The phase implementing DOC-V2-01.

---

### Pitfall 4: There's no cheap `%PDF-`-equivalent for ZIP-based outputs — but the codebase already has the right tool for it

**What goes wrong:**
Once targets include odt/ods/odp/docx/xlsx/pptx, `validatePDF`'s "%PDF- magic bytes + non-zero size" check no longer applies. The naive replacement — "check it's a non-empty file starting with `PK\x03\x04`" — is *weaker* than the existing input-side validation this project already applies to uploads: it would accept literally any valid ZIP as a "successful" conversion, including a soffice-produced empty/near-empty archive that technically parses as a zip but contains the wrong root part (i.e., LibreOffice's documented "exit 0 but garbage output" failure mode, the same D-02 risk `validatePDF` was built to guard against for PDF).

**How to avoid:**
Reuse `convert.SniffContainer` — already built for input-side format disambiguation — as the *output*-side validator too: after conversion, run it against the produced file and assert `cr.Format == <expected target format>` (not just "some non-empty zip"). This is a natural, already-available cheap validity check that's stronger than a bare magic-bytes check, consistent with the project's existing single-pass-central-directory convention, and requires no new code beyond calling an existing function with the target format as an expectation.

**Warning signs:**
A conversion "succeeds" (job marked `done`, webhook fired) but the presigned download, when opened, is corrupt or is a different document type than requested — the exact failure mode this check is meant to catch before it reaches the client.

**Phase to address:**
The phase implementing DOC-V2-01, as part of generalizing `validatePDF` into a target-format-aware `validateOutput`.

---

### Pitfall 5: OLE-CFB magic bytes cannot distinguish "legacy binary .doc/.xls/.ppt" from "password-protected modern OOXML" — both share the identical 8-byte header

**What goes wrong:**
Both a legacy binary `.doc`/`.xls`/`.ppt` file *and* a password-protected/encrypted modern `.docx`/`.xlsx`/`.pptx` (ECMA-376 Agile or Standard encryption) begin with the exact same Compound File Binary (OLE2/MS-CFB) magic: `D0 CF 11 E0 A1 B1 1A E1`. A pre-flight check that stops at "does this look like CFB → reject" cannot tell an internal client *why* their upload was rejected: "this is an old binary format we don't support" and "this is a modern format we do support, but it's encrypted and we can't open it without a password" are different problems requiring different client-side remediation, and the milestone's goal is explicitly a "clear 422," not a generic one.

**Why it happens:**
Encrypted OOXML is, by design, a CFB *container* wrapping the real (encrypted) ZIP/OOXML package inside two internal streams (`EncryptedPackage` holding the ciphertext, `EncryptionInfo` holding the key-derivation parameters) — so at the outer-container level it is byte-for-byte indistinguishable from a genuinely legacy binary document, which uses entirely different internal stream names (`WordDocument`/`1Table`/`0Table` for legacy .doc, `Workbook`/`Book` for legacy .xls, `PowerPoint Document` for legacy .ppt).

**How to avoid:**
Distinguishing the two cases requires walking *inside* the CFB container's directory structure (not just its outer magic) and checking for the presence of an `EncryptedPackage`/`EncryptionInfo` stream pair (→ "password-protected modern format") versus the legacy-format stream names above (→ "legacy binary format, never supported"). Return a distinct, specific error message for each case rather than one generic "OLE-CFB detected, rejected" message.

**Warning signs:**
Internal client teams filing tickets asking "is my file corrupted?" when it's actually just password-protected — a sign the error message collapsed a genuinely useful distinction into one bucket.

**Phase to address:**
The phase implementing DOC-V2-02.

---

### Pitfall 6: The project's "zero new dependencies" conviction hits a real wall at CFB parsing — Go's stdlib has no OLE2/CFB reader

**What goes wrong:**
Every prior format-sniffing addition in this codebase (`sniff.go` for magic-byte image formats, `docsniff.go` for ZIP-based office formats) either needed a handful of byte comparisons or could lean on Go's stdlib `archive/zip`. There is no stdlib equivalent for OLE2/CFB — walking the CFB directory sector chain (and, for files above the CFB "mini stream" cutoff, the FAT sector chain too) to enumerate top-level stream names is a real, non-trivial parser to hand-roll correctly, unlike the ZIP central-directory read that `SniffContainer` already does in ~15 lines via stdlib.

**Why it happens:**
CFB predates ZIP-based formats by over a decade and was never given first-class Go stdlib support the way ZIP was; the "structural sniffing, zero new deps" pattern established for Phase 7/document formats does not automatically generalize to every container format.

**How to avoid:**
Treat this as an explicit build-vs-depend decision, not a drop-in extension of `docsniff.go`. Options, in order of fit with existing conventions: (1) hand-roll a *minimal* CFB directory-stream-name reader that only needs to enumerate top-level stream names (not full content extraction) — feasible since the detection only needs stream *names*, not decrypted content, and keeps the zero-new-deps convention intact, but budget real implementation/testing time for it, it is meaningfully more code than `docsniff.go`; (2) take a small, audited third-party Go CFB library, explicitly breaking the established convention — acceptable only if hand-rolling proves too costly, and should be logged as a Key Decision in PROJECT.md the way the "zero-dependency parsers" decision was for Phase 7; (3) do **not** shell out to an external tool (e.g. `file`, `olevba`) for this — that reintroduces the exact untrusted-input-to-exec attack surface the project has otherwise been careful to bound only to conversion engines, for a step that runs *before* the format is even confirmed convertible.

**Warning signs:**
Scope creep — if implementing CFB detection starts pulling in a general-purpose OLE2 reader/writer library "just in case," that's a sign the minimal stream-name-only reader wasn't scoped tightly enough.

**Phase to address:**
The phase implementing DOC-V2-02 — flag explicitly as needing its own implementation-complexity estimate, don't fold it into the same estimate as the ZIP-based sniffing work it superficially resembles.

---

### Pitfall 7: `EmbedStandardFonts` defaults to `false`, and PDF/A requires *all* fonts embedded — a PDF/A export can "succeed" while being non-conformant

**What goes wrong:**
LibreOffice's PDF export filter has an `EmbedStandardFonts` boolean parameter (default `false`) controlling whether the 14 base PDF fonts are embedded. PDF/A conformance (any of the -1/-2/-3 flavors) requires **every** font used in the document to be embedded — including ones a normal PDF viewer would assume are always available. If the opts-driven PDF/A path only sets `SelectPdfVersion` and doesn't also force font embedding on, the produced file can still exit 0, pass the existing `%PDF- magic bytes` check, and even superficially look like a PDF/A (correct version byte, maybe even correct XMP metadata block) while failing true conformance the moment a validator (or a downstream archival system) checks for embedded fonts.

**Why it happens:**
`SelectPdfVersion` and `EmbedStandardFonts` are independent filter parameters; setting one does not imply the other. It's an easy trap to reach for "the one PDF/A flag" and assume it's sufficient.

**How to avoid:**
When building the opts→filter-options JSON for a PDF/A request, force `EmbedStandardFonts: true` (and `UseTaggedPDF`/`PDFUACompliance` only if accessibility is separately required — don't conflate PDF/A with PDF/UA, they're different specs with overlapping-but-distinct requirements) alongside the version selector, as a hardcoded pairing whenever `pdf_a` is requested — never let these be independently client-settable (see Pitfall 9).

**Warning signs:**
A produced "PDF/A" file that renders fine in a normal PDF viewer (which silently substitutes system fonts for anything unembedded) but fails when opened in a strict PDF/A viewer or long-term archival tool — the exact failure mode PDF/A exists to prevent.

**Phase to address:**
The phase implementing DOC-V2-03.

---

### Pitfall 8: `%PDF-` magic bytes (the existing `validatePDF` check) proves nothing about PDF/A conformance — full conformance validation (veraPDF) is a real dependency the project likely can't justify yet

**What goes wrong:**
The existing `validatePDF` was correctly scoped for its original job — catching LibreOffice's documented "exit 0, empty/corrupt output" failure — but it says nothing about whether a PDF/A-flagged export actually conforms to ISO 19005. A PDF's own internal metadata can *claim* PDF/A-1b conformance (via XMP `pdfaid:part`/`pdfaid:conformance` and a `/GTS_PDFA1` output-intent identifier) without the document structure actually satisfying every PDF/A structural rule — the claim and the reality can diverge, and only a purpose-built conformance checker (veraPDF is the industry-standard one) can authoritatively tell them apart.

**Why it happens:**
Full ISO 19005 conformance checking is a large, Java-based dependency (veraPDF) — a poor fit for this project's established "zero new deps, small Go binaries" convention, and arguably disproportionate engineering effort for an internal tool whose PDF/A feature is "archival export," not "guaranteed regulator-grade conformance."

**How to avoid:**
Pick a deliberate, documented middle ground rather than silently shipping the weakest option: extend the output validator to also grep the produced PDF bytes for the expected `/GTS_PDFA1` (or `/GTS_PDFA2`/`/GTS_PDFA3`) OutputIntent identifier string matching the requested `SelectPdfVersion`, as a cheap (no new dependency) but explicitly *non-authoritative* sanity check — it confirms LibreOffice *attempted* to tag the file correctly, not that the file is truly conformant. Document this limitation as an accepted residual risk in PROJECT.md (mirroring the existing DOC-V2-05 resource-exhaustion residual-risk entry) rather than letting "we validate PDF/A" become an implicit, false claim. Defer full veraPDF integration explicitly rather than silently never revisiting it.

**Warning signs:**
Any internal documentation or client-facing claim that says "PDF/A output is validated" without qualifying *what* is validated (structural tag presence) versus what isn't (full ISO 19005 conformance).

**Phase to address:**
The phase implementing DOC-V2-03 — as an explicit scope/residual-risk decision, logged in PROJECT.md's Key Decisions table the way prior accepted risks have been.

---

### Pitfall 9: `opts` is a brand-new, currently-unguarded plumbing path — the injection risk is UNO filter-property injection, not shell injection

**What goes wrong:**
`Convert(ctx, inPath, outPath string, _ map[string]any)` already has an `opts` parameter in its signature today, but it is completely ignored (`_ map[string]any`) — there is no `opts` column in the jobs schema, no API parameter parsing, no validation, nothing. DOC-V2-03 is the *first* feature to actually route data through this path end-to-end, so there's no existing precedent or guardrail to lean on. Because `runCommand`/`exec.Command` passes arguments as an argv array (not through a shell), classic shell-metacharacter injection isn't the risk here — but if client-supplied `opts` values get marshaled directly into the LibreOffice filter-options JSON blob (the single argv token after `pdf:writer_pdf_Export:`), the attacker isn't escaping a shell, they're supplying arbitrary **UNO API filter properties** LibreOffice's own option parser will honor: e.g. setting an encryption password on the output (`EncryptFile`/`DocumentOpenPassword`), altering export quality/compression in ways that degrade output, or otherwise reaching properties well outside "which PDF/A version" that the API was ever meant to expose.

**Why it happens:**
`map[string]any` is a natural-looking shape for "generic options," and it's tempting to serialize it close to verbatim into the filter-options JSON for convenience — especially since the JSON-based filter-options syntax itself looks like a natural pass-through target.

**How to avoid:**
Never marshal client-supplied `opts` directly into the filter-options string. Define a small, closed, strictly-typed Go struct (e.g. `type DocOpts struct { PDFA bool }`, extended only as new options are deliberately added) that the API parses and validates client input into with an allow-list of known keys — reject unknown keys/values with 422 rather than silently ignoring or passing them through. Build the actual LibreOffice filter-options JSON purely from server-side constants keyed off the validated struct fields (e.g. `PDFA: true` → the hardcoded `SelectPdfVersion`+`EmbedStandardFonts` pair from Pitfall 7) — the client-controlled bytes should never appear inside the string handed to `soffice`'s argv.

**Warning signs:**
Any code path where `json.Marshal(opts)` (the raw client map) or similar directly produces (part of) the string passed to `runCommand`/`exec.Command` — that's the injection surface, full stop, regardless of how well-intentioned the allow-list checking elsewhere looks.

**Phase to address:**
The phase implementing DOC-V2-03, as a security-review gate before merge — this is the single highest-severity net-new attack surface in this milestone.

---

### Pitfall 10: Headless chromium's sandbox model conflicts with this project's own "run as unprivileged `nobody`" container convention

**What goes wrong:**
Every existing OctoConv worker container runs as `USER nobody` per `Dockerfile.worker`/`Dockerfile.document-worker`. Chrome/Chromium's internal OS-level sandbox (the mechanism `--no-sandbox` disables) normally relies on a setuid-root sandbox helper binary or user/PID namespace privileges that are typically unavailable to a non-root, non-`CAP_SYS_ADMIN` process in a stock Docker container — meaning `--no-sandbox` isn't an optional performance flag here, it's close to mandatory given the existing security convention, and it removes Chromium's own internal defense against a hostile page exploiting the renderer process.

**Why it happens:**
Generic "run headless Chrome in Docker" guidance treats `--no-sandbox` as a convenience flag ("your container is already a sandbox"), which undersells the tradeoff for *this* project specifically: the input to this engine is by definition attacker-influenced HTML (an internal client's document, potentially embedding untrusted content), and the container-level isolation this project already relies on (unprivileged user, resource caps) was designed around engines whose only real threat model was "hangs/crashes/orphaned processes" (LibreOffice, libvips), not "arbitrary renderer-process code execution against a malicious page."

**How to avoid:**
Layer container-level hardening *on top of* `--no-sandbox` rather than treating either alone as sufficient: keep the container as its own isolation boundary (already true — separate container per engine class is an established pattern here), drop all Linux capabilities beyond what's strictly needed, consider a seccomp profile, and — most importantly for this domain — enforce network egress restrictions at the container/process level (see Pitfall 11), since without Chrome's own sandbox, network access is the highest-value remaining attack surface a hostile page can exploit.

**Warning signs:**
Treating "we set `--no-sandbox`, it works" as the finish line during implementation, without a follow-up conversation about what compensating controls replace the sandbox's removed protection.

**Phase to address:**
The phase implementing DOC-V2-04 (HTML→PDF engine) — flagged in PROJECT.md's own milestone context as "the riskiest item, needs its own safety model," which this pitfall directly substantiates.

---

### Pitfall 11: URL-string SSRF validation (the existing `callback_url` pattern) does not transfer to chromium — a rendered page can reach internal/metadata endpoints via a raw IP literal, and DNS-rebinding-style TOCTOU is worse here because the "attacker" (page content) runs repeatedly, not once

**What goes wrong:**
The existing webhook SSRF guard (`internal/api/callbackurl.go`) validates a single client-supplied URL string once, at job-creation time, by resolving its hostname and checking the resolved IP against a blocklist. That pattern does not generalize to HTML→PDF: the "URL" being rendered isn't one client-supplied string, it's an entire HTML document that can contain arbitrarily many `<img src=...>`/`<iframe src=...>`/`fetch()`/`XMLHttpRequest` targets, discovered only at render time, potentially referencing bare IP literals (e.g. `http://169.254.169.254/`) that never go through hostname resolution at all — so a `net.LookupHost`-based check (even if one were bolted onto every discovered URL) wouldn't even apply to the most classic SSRF target, the cloud metadata endpoint, when addressed by IP literal directly.

**Why it happens:**
It's natural to reach for "the SSRF guard we already have" as a starting point, but that guard was built for a single, statically-known-at-creation-time URL (`callback_url`), not for content whose network-reaching behavior is fully attacker-authored and only observable at render time.

**How to avoid:**
Don't try to validate URLs discovered inside the HTML at all — block network access at the protocol layer instead, so the check applies uniformly regardless of hostname vs. IP literal, redirect chains, or JS-initiated fetches. The most robust mechanism is to drive Chromium via the DevTools Protocol (e.g. `chromedp`) rather than a pure one-shot `--headless --print-to-pdf` CLI invocation, specifically so the Go code can enable the `Fetch`/`Network` domain and deny every request that isn't a `file://` reference to the job's own already-downloaded input — a genuine fail-closed network guard, not a best-effort URL check. This is a real architectural divergence from the existing exec-only pattern (`runCommand`) used for LibreOffice/libvips, and should be called out explicitly as a design decision rather than discovered mid-implementation. The existing process-group-kill-on-timeout mechanism in `exec.go` still applies to whatever OS process ultimately runs the browser — that part of the hardened-exec convention transfers cleanly even if the invocation shape (long-lived CDP session vs. one-shot argv call) doesn't.

**Warning signs:**
A design that only validates `<img src>`/`<a href>` URLs found via a naive string scan of the HTML before rendering — trivially bypassed by anything constructed at runtime via JavaScript (`fetch(atob(...))`, dynamically-built URLs, etc.), since the scan happens before the page's own script has run.

**Phase to address:**
The phase implementing DOC-V2-04 — this is the core of the "new safety model" the milestone context already flags as needed; it should be a named design decision (CDP-driven network blocking vs. CLI-only) before implementation starts, not an afterthought.

---

### Pitfall 12: Chromium spawns multiple sub-processes and needs an init reaper — the same class of problem `Dockerfile.document-worker`'s `tini` already solves for `soffice.bin`

**What goes wrong:**
Headless Chromium isn't a single process — it forks a zygote, GPU process (even headless), and per-tab renderer processes. If the container's PID 1 is the Go worker binary itself (not an init system), crashed/killed Chromium children can be left as unreaped zombies, and — separately — `/dev/shm`'s default Docker size (64MB) is too small for Chromium's shared-memory needs, causing renderer crashes that look like unrelated flakiness rather than a resourcing problem.

**Why it happens:**
This is a well-known Docker+Chromium gotcha in general, but it's specifically relevant here because the project already solved the *identical* problem for LibreOffice's `soffice.bin` child process by making `tini` PID 1 in `Dockerfile.document-worker` — it would be easy to build the new html-worker container without carrying that same fix forward, since Chromium's need for it isn't obvious until it's already causing intermittent failures in production.

**How to avoid:**
Reuse the exact `tini`-as-PID-1 pattern from `Dockerfile.document-worker` in the new engine's Dockerfile, and explicitly set `--shm-size` (or pass `--disable-dev-shm-usage` to force Chromium to use `/tmp` instead of `/dev/shm`) in both the Dockerfile/compose config and the Chromium invocation flags — don't rely on only one of the two.

**Warning signs:**
Intermittent renderer crashes under concurrent load that don't reproduce reliably in local single-request testing — the classic signature of an undersized `/dev/shm` only manifesting under real container resource limits.

**Phase to address:**
The phase implementing DOC-V2-04, as part of the new Dockerfile/compose wiring.

---

### Pitfall 13: Decoupling webhook delivery from the image worker can silently recreate the exact single-point-of-failure it's meant to fix

**What goes wrong:**
Today, `cmd/worker/main.go` is the *sole* consumer of `queue.QueueWebhook` (weighted `{image: 2, webhook: 1}` in one `asynq.Server`), and `cmd/document-worker/main.go` explicitly and deliberately does **not** register `HandleWebhookDeliver` or include the webhook queue in its `Queues` map — the code comment there says so outright: *"document-worker neither delivers nor signs webhooks (D-06 — cmd/worker remains the sole webhook consumer)."* The milestone's stated goal (SEED-002) is that "deploying any subset of engine-workers doesn't silently lose webhooks" — but the most literal reading of "decouple webhook delivery" (spin up one new dedicated `cmd/webhook-worker` binary/container, remove webhook consumption from `cmd/worker` entirely) just *moves* the single point of failure from "the image worker" to "the one webhook-worker" — deploying that one new process now becomes the new outage window for webhook delivery, which is the exact problem being solved, just relocated.

**Why it happens:**
"Decoupling" is naturally read as "give it its own dedicated process," but a *single* dedicated process is still a single point of failure; what actually satisfies "survives deploy of any subset of workers" is *redundancy* — multiple independent processes capable of consuming the webhook queue, such that a rolling deploy of any one of them still leaves at least one other consumer running.

**How to avoid:**
Design the webhook-queue consumer as a horizontally-scalable role from the start (asynq safely supports N independent servers consuming the same queue — this is native, not a workaround), and/or have every engine-class worker (image, document, and future engines) continue to consume the webhook queue redundantly alongside their own engine queue, so that no single deploy event ever drops webhook consumption to zero as long as at least one engine worker is up. A single dedicated webhook-worker with only one replica does *not* satisfy the milestone's own stated goal; make the redundancy requirement explicit in the phase's success criteria, not just "webhook delivery moved to its own binary."

**Warning signs:**
A design/PR that introduces exactly one new webhook-consuming process and removes webhook consumption from every existing worker — that's a topology change, not a resilience improvement, unless that one process is explicitly run with ≥2 replicas.

**Phase to address:**
The phase implementing SEED-002 — this should be an explicit topology decision recorded in PROJECT.md's Key Decisions table before implementation, given how easy it is to satisfy the letter of "decouple it" while missing the actual resilience goal.

---

### Pitfall 14: The reconciler's webhook-gap sweep is a documented singleton — multi-consumer webhook topology must not duplicate it

**What goes wrong:**
`internal/reconciler/reconciler.go`'s `Sweeper` handles *both* stale-job recovery and webhook-gap recovery (RECON-04/RECON-05) in one loop, and it runs today as a single goroutine inside `cmd/worker/main.go` only — `cmd/document-worker/main.go` explicitly does not run any sweeper, with a code comment calling out exactly why: *"avoiding a double-sweep race between two independent sweep loops recovering the same stranded job."* If webhook delivery is decoupled into N redundant consumer processes (per Pitfall 13), naively running the existing `Sweeper` in *every* one of those processes reintroduces the double-sweep race this project has already identified and avoided once — now specifically for the webhook-gap sweep, which fires `EnqueueWebhookDeliver` for jobs whose completion webhook was never enqueued.

**Why it happens:**
It's natural to assume "the process that owns webhook delivery should also own webhook-gap recovery," but the sweeper's webhook-gap-recovery half only needs to run *once* across the whole fleet, regardless of how many processes are consuming the webhook delivery queue — ownership of "consuming tasks" and "the singleton sweep loop that creates tasks" are two different concerns that happened to live in the same binary before this milestone only because there was exactly one webhook-consuming process.

**How to avoid:**
Decide explicitly where the singleton sweeper lives once webhook consumption is redundant/multi-process: either (a) keep it in exactly one of the N webhook-consumer replicas via some form of leader election / "only replica 0 runs it" convention, or (b) extract the sweeper into its own always-single-instance process entirely separate from the horizontally-scaled webhook queue consumers (cleanest fit with `asynq.Unique`'s existing idempotency guarantees, since the sweeper already relies on enqueue-first + `asynq.ErrDuplicateTask` for safety against *accidental* double-enqueue, but was never designed to tolerate *N sweeper loops racing each other* on the same stranded-job/gap detection queries). Whichever is chosen, it needs to be a named decision, not an implicit side effect of wherever the new webhook-worker code happens to get written.

**Warning signs:**
Two (or more) `reconciler.NewSweeper(...).Run(ctx)` calls active simultaneously in production — check this explicitly during the phase's integration testing, since the failure mode (duplicate recovery actions on the same stranded job, mitigated only by `asynq.Unique`'s TTL-bounded lock, not by the sweeper's own logic) may not surface in a low-concurrency local test.

**Phase to address:**
The phase implementing SEED-002, as a direct consequence of resolving Pitfall 13's topology decision.

---

## Technical Debt Patterns

| Shortcut | Immediate Benefit | Long-term Cost | When Acceptable |
|----------|--------------------|-----------------|------------------|
| Skip the CFB-internal stream-name distinction; reject all CFB-shaped uploads with one generic message | Ships DOC-V2-02 faster, avoids hand-rolling a CFB parser | Internal clients can't tell "unsupported legacy format" from "just needs a password removed," generating avoidable support tickets | Only as a documented interim step, if the CFB parser (Pitfall 6) is scoped as a separate follow-up, not silently dropped |
| Validate PDF/A only via `%PDF-` magic bytes + a hardcoded `SelectPdfVersion`, skip the OutputIntent identifier grep (Pitfall 8) | Zero extra code | Silently ships a feature that claims "PDF/A support" without any signal distinguishing "attempted" from "actually tagged correctly" | Never — the OutputIntent grep is cheap enough that skipping it isn't a real time saving |
| Use a one-shot `chromium --headless --print-to-pdf` CLI call (matching the existing `runCommand` pattern) instead of a CDP/`chromedp` session | Reuses the existing hardened-exec pattern verbatim, less new code | Cannot enforce fail-closed network blocking at the protocol layer (Pitfall 11) — falls back to weaker, bypassable URL-string filtering | Only if network egress from the chromium process is independently blocked at the OS/container level (e.g. a network namespace with no route out) rather than relied upon in application code |
| Give the webhook queue a single new dedicated consumer process instead of redundant multi-consumer topology | Simpler initial implementation, matches "one engine, one binary" convention | Recreates the single point of failure SEED-002 is meant to eliminate (Pitfall 13) | Never, given the milestone's explicit stated goal |

## Integration Gotchas

| Integration | Common Mistake | Correct Approach |
|-------------|-----------------|-------------------|
| LibreOffice cross-format filters | Assuming the PDF-export filter names (`writer_pdf_Export` etc.) generalize to non-PDF targets | Use the distinct ODF (`writer8`/`calc8`/`impress8`) and OOXML (`MS Word 2007 XML` etc.) filter names, verified per-pair against the actual deployed LibreOffice version |
| LibreOffice PDF/A filter options | Setting only `SelectPdfVersion`, assuming it implies font embedding | Always pair `SelectPdfVersion` with `EmbedStandardFonts: true` for any PDF/A request |
| Chromium in Docker | Copying generic "add `--no-sandbox --disable-dev-shm-usage`" advice without addressing what replaces the sandbox's removed protection | Layer container-level isolation (capabilities, network egress blocking) as compensating controls, not just flag-copying |
| asynq multi-consumer webhook queue | Assuming "decoupled" means "exactly one new process" | Design for N-replica redundancy from the start; verify with an explicit rolling-deploy test that kills one replica while traffic is in flight |
| Reconciler sweeper + multi-process webhook consumers | Running the existing `Sweeper` in every webhook-consuming replica | Keep exactly one active sweeper instance fleet-wide; make this an explicit, tested invariant |

## Performance Traps

| Trap | Symptoms | Prevention | When It Breaks |
|------|----------|------------|-----------------|
| Chromium `/dev/shm` starvation under concurrent conversion load | Intermittent renderer crashes that don't reproduce in low-concurrency local testing | Set `--shm-size` explicitly in compose/Dockerfile and pass `--disable-dev-shm-usage` | Breaks specifically under concurrent HTML→PDF jobs on the default Docker 64MB `/dev/shm`, not under single-job local testing |
| Complex documents (large spreadsheets, deeply nested HTML/CSS) exhausting `DOCUMENT_ENGINE_TIMEOUT`/the new HTML engine's timeout | Jobs marked failed/terminal on timeout rather than a clean error; accepted as residual risk already for DOC-V2-05 | Existing `ENGINE_TIMEOUT`-style bound + concurrency cap is the only mitigation currently planned; treat as pre-existing accepted risk, don't attempt content-complexity analysis in this milestone | Scales with per-job complexity, not request volume — a single sufficiently complex document can consume the full timeout budget regardless of overall load |
| Weighted asynq queue starvation if new queue weights are copy-pasted without re-deriving them for the new topology | Webhook or document jobs experience longer tail latency than their retry schedule assumes | Explicitly re-derive per-queue weights (not copy `cmd/worker`'s `{image: 2, webhook: 1}` verbatim) for whatever new multi-process topology SEED-002 lands on | Becomes visible only under simultaneous load on multiple queues sharing a process — won't show up in single-queue testing |

## Security Mistakes

| Mistake | Risk | Prevention |
|---------|------|------------|
| Marshaling client-supplied `opts` map directly into the LibreOffice filter-options JSON | Attacker-controlled UNO filter properties (e.g. output encryption password, arbitrary export properties) reach `soffice`, not classic shell injection since `exec.Command` uses argv arrays, but a real property-injection surface | Parse `opts` into a small closed Go struct with an allow-list of known keys server-side; build the filter-options JSON purely from server-side constants keyed off validated fields — never pass client bytes through |
| Relying on URL-string SSRF validation (the existing `callback_url` pattern) for HTML→PDF content | A rendered page can reach internal/metadata endpoints via raw IP literals or JS-constructed URLs never present as a static string, bypassing any pre-render scan | Block network access at the protocol layer (CDP `Fetch`/`Network` domain interception, deny-all-except-`file://`) rather than validating URLs found in the HTML |
| Treating `--no-sandbox` as a pure performance flag | Removes Chromium's internal defense against a hostile-page renderer-process exploit, with no compensating control | Layer container-level hardening (dropped capabilities, network egress blocking) explicitly as the replacement isolation boundary |
| Rejecting CFB-shaped uploads without distinguishing legacy-binary from encrypted-modern | Not itself a security hole, but a diagnostic-quality gap that can mask genuinely malicious crafted-CFB uploads behind the same generic message as an ordinary password-protected file | Parse the CFB directory stream names to give an accurate, specific rejection reason (also surfaces malformed/anomalous CFB structures as a distinct case worth logging) |

## UX Pitfalls

| Pitfall | User Impact | Better Approach |
|---------|--------------|-------------------|
| Generic "unrecognized file content" 422 for both legacy-binary and password-protected uploads | Internal client teams can't tell whether their upload needs "resave in a supported format" vs. "remove the password" — leads to avoidable support tickets | Distinct, specific error messages for each case (Pitfall 5) |
| Cross-format conversion presented as "supported" with no fidelity caveat | Internal consumers may assume archival-grade round-trip equivalence and be surprised by dropped tracked-changes, alt-text, or unsupported spreadsheet functions | Document explicitly (in API docs / response, not just internal docs) that cross-format conversion is best-effort structural translation |
| PDF/A output silently not-actually-conformant (Pitfall 7/8) | A client relying on the PDF/A flag for genuine long-term archival compliance gets a document that looks right but fails real conformance checking downstream | Document precisely what the PDF/A validation does and doesn't guarantee; don't imply full ISO 19005 conformance if only doing a magic-bytes/OutputIntent sanity check |

## "Looks Done But Isn't" Checklist

- [ ] **Cross-format conversion (DOC-V2-01):** Often missing — target-format-aware output extension and validator (not hardcoded `.pdf`); verify by grepping `internal/convert/libreoffice.go` for any remaining literal `".pdf"` string.
- [ ] **OLE-CFB pre-flight (DOC-V2-02):** Often missing — distinguishing legacy-binary from encrypted-modern via internal stream names, not just the outer CFB magic; verify by testing both a genuine legacy `.doc` and a password-protected `.docx` and confirming distinct error messages.
- [ ] **PDF/A export (DOC-V2-03):** Often missing — `EmbedStandardFonts` forced alongside `SelectPdfVersion`, and a validator beyond bare `%PDF-` magic bytes; verify by opening a produced PDF/A file and checking font embedding, and grepping for the `/GTS_PDFA*` OutputIntent identifier.
- [ ] **HTML→PDF engine (DOC-V2-04):** Often missing — protocol-level network blocking (not URL-string filtering), `tini`-as-PID-1, and explicit `/dev/shm` sizing; verify by attempting to render an HTML page containing `<img src="http://169.254.169.254/">` and a JS `fetch()` to an arbitrary internal address, and confirming both fail closed.
- [ ] **Webhook decoupling (SEED-002):** Often missing — actual redundancy (≥2 independent consumers of the webhook queue) rather than a single relocated consumer, and a single surviving sweeper instance; verify with a rolling-deploy test that kills one webhook-consuming replica mid-traffic and confirms delivery continues, plus a check that only one sweeper instance is active fleet-wide.

## Recovery Strategies

| Pitfall | Recovery Cost | Recovery Steps |
|---------|-----------------|------------------|
| Hardcoded `.pdf` assumptions shipped and broke non-PDF targets | LOW | Generalize `producedPath`/`validatePDF` to be target-format-aware; existing test suite (`libreoffice_test.go`) already has infrastructure to extend per-pair |
| PDF/A export shipped without font embedding forced | LOW | Add `EmbedStandardFonts: true` to the hardcoded PDF/A filter-options constant; no schema/API change needed since this is a server-side-only constant, not client-controlled |
| `opts` naively pass-through into filter-options JSON, discovered post-merge | MEDIUM | Requires a follow-up PR introducing the closed struct + allow-list validation, plus an audit of any already-issued jobs that may have exercised the unguarded path |
| Webhook decoupling shipped as a single non-redundant consumer | MEDIUM | Add replica count / a second consumer path without needing to re-architect the queue itself, since asynq already supports N consumers natively — this is a deployment-topology fix, not a code rewrite |
| Double-sweeper race discovered in production after webhook decoupling | HIGH | Requires coordinated fix across whichever processes run the sweeper (removing it from N-1 of them, or extracting to a dedicated singleton process) plus an audit of `job_events`/`webhook_deliveries` for any duplicate recovery actions that occurred in the interim |

## Pitfall-to-Phase Mapping

| Pitfall | Prevention Phase | Verification |
|---------|--------------------|----------------|
| Hardcoded `.pdf` output assumptions | DOC-V2-01 phase | Grep for literal `.pdf` in `internal/convert/libreoffice.go`; test all 6 cross-format pairs end-to-end |
| LibreOffice fidelity loss (silent, not a bug) | DOC-V2-01 phase | Documentation review, not code — confirm API docs state best-effort conversion |
| Filter-name matrix correctness | DOC-V2-01 phase | Live E2E test of all cross-format pairs against the actual deployed LibreOffice version |
| Cheap ZIP-based output validity check | DOC-V2-01 phase | Confirm `SniffContainer` (or equivalent) is called on the *output*, asserting expected target format |
| CFB legacy-vs-encrypted distinction | DOC-V2-02 phase | Test both a genuine legacy `.doc` and a password-protected `.docx`, confirm distinct 422 messages |
| Zero-new-deps vs. hand-rolled CFB parser decision | DOC-V2-02 phase | Explicit Key Decision logged in PROJECT.md before implementation, not discovered mid-PR |
| Font embedding for PDF/A | DOC-V2-03 phase | Inspect produced PDF/A file's embedded font list |
| PDF/A conformance validation honesty | DOC-V2-03 phase | Confirm validator scope (OutputIntent grep, not full veraPDF) is documented as a residual-risk decision |
| `opts` injection surface | DOC-V2-03 phase | Security review gate before merge; confirm no client bytes reach the filter-options string directly |
| Chromium sandbox/container hardening | DOC-V2-04 phase | Container capability/network audit as part of phase review |
| Chromium network blocking (SSRF-equivalent) | DOC-V2-04 phase | Test rendering HTML with an `<img src>` to a private/metadata IP literal and a JS `fetch()` to an internal address; confirm both fail closed |
| Chromium zombie processes / `/dev/shm` sizing | DOC-V2-04 phase | Load test with concurrent conversions; confirm no orphaned processes accumulate and no shm-related crashes occur |
| Webhook decoupling redundancy | SEED-002 phase | Rolling-deploy test killing one webhook-consuming replica mid-traffic; confirm delivery continues |
| Reconciler sweeper singleton constraint | SEED-002 phase | Confirm exactly one active sweeper instance fleet-wide after the new topology lands |

## Sources

- `internal/convert/libreoffice.go`, `internal/convert/docsniff.go`, `internal/convert/sniff.go`, `internal/convert/exec.go` (this codebase — current LibreOffice converter, ZIP-based office-format sniffing, hardened process exec)
- `internal/api/handlers.go`, `internal/api/callbackurl.go` (this codebase — existing content-detection dispatch flow and SSRF-guard pattern for `callback_url`)
- `internal/queue/queue.go`, `internal/reconciler/reconciler.go`, `internal/webhook/deliver.go` (this codebase — asynq queue/task topology, reconciler sweeper singleton behavior, webhook delivery HTTP client)
- `cmd/worker/main.go`, `cmd/document-worker/main.go` (this codebase — current webhook-queue consumer wiring and the explicit "sole webhook consumer" / "no sweeper here" comments)
- `.planning/PROJECT.md` (milestone v1.3 scope, prior accepted-risk decisions, DOC-V2-05 residual risk pattern)
- [LibreOffice Help: PDF Export Command Line Parameters](https://help.libreoffice.org/latest/en-US/text/shared/guide/pdf_params.html) — `SelectPdfVersion` value mapping to PDF/A-1b/2b/3b, `EmbedStandardFonts` default `false`
- [LibreOffice Help: File Conversion Filter Names](https://help.libreoffice.org/latest/en-US/text/shared/guide/convertfilters.html) — `writer8`/`calc8`/`impress8` and "MS ... 2007 XML" filter name families
- [Didier Stevens: Encrypted OOXML Documents](https://blog.didierstevens.com/2018/06/07/encrypted-ooxml-documents/) — CFB header shared between legacy binary and encrypted OOXML; `EncryptedPackage`/`EncryptionInfo` stream structure
- [SANS ISC: Encrypted Office Documents](https://isc.sans.edu/diary/Encrypted+Office+Documents/23774) — CFB `D0 CF 11 E0 A1 B1 1A E1` header vs. ZIP `50 4B 03 04` header
- [Aspose: Detect File Format of Encrypted OOXML](https://docs.aspose.com/cells/java/detect-file-format-of-encrypted-office-open-xml-ooxml-files/) — corroborating CFB-wrapped-encrypted-OOXML detection approach
- [veraPDF](https://verapdf.org/) and [veraPDF CLI Validation docs](https://docs.verapdf.org/cli/validation/) — authoritative PDF/A conformance checking vs. metadata-claim-only checks
- [chromedp/docker-headless-shell](https://github.com/chromedp/docker-headless-shell), [Baeldung: Run Google Chrome headless in Docker](https://www.baeldung.com/ops/docker-google-chrome-headless) — `--disable-dev-shm-usage`, `--no-sandbox`, zombie-process/`tini` guidance for headless Chromium in Docker
- [chromedp/chromedp GitHub issue #207](https://github.com/chromedp/chromedp/issues/207) — Docker sandboxing considerations for chromedp specifically
- [hibiken/asynq Wiki: Queue Priority](https://github.com/hibiken/asynq/wiki/Queue-Priority) — weighted queue semantics, `StrictPriority`, multi-server same-queue consumption behavior (community-verified, matches this project's existing weighted-queue usage in `cmd/worker/main.go`)
- Community bug reports (GitHub `Euro-Office/DocumentServer#114`, bugs.documentfoundation.org, ask.libreoffice.org, forum.openoffice.org threads) — LibreOffice docx↔odt and xlsx↔ods fidelity-loss specifics (MEDIUM confidence, not official LO guarantees)

---
*Pitfalls research for: OctoConv v1.3 Document Class v2 (cross-format LibreOffice conversion, OLE-CFB detection, PDF/A export, chromium HTML→PDF engine, decoupled webhook delivery)*
*Researched: 2026-07-10*
