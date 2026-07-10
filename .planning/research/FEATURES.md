# Feature Research

**Domain:** Async file-conversion service — document-class v2 (cross-format office conversion, legacy/encrypted rejection, PDF/A export, HTML→PDF, webhook delivery decoupling)
**Researched:** 2026-07-10
**Confidence:** MEDIUM-HIGH (mixed: HIGH for LibreOffice/CFB mechanics and current-codebase gaps verified by reading source; MEDIUM for competitor API conventions verified via WebSearch/WebFetch, no Context7 library coverage for this domain)

## Feature Landscape

### Table Stakes (Users Expect These)

| Feature | Why Expected | Complexity | Notes |
|---------|--------------|------------|-------|
| Cross-format pairs: docx↔odt, xlsx↔ods, pptx↔odp | Internal clients already convert *to* PDF one-way; office-suite migration/interop workflows need round-trip between MS and OpenDocument formats — this is the single most common "document conversion" ask beyond →PDF (CloudConvert, Zamzar, ConvertAPI all list these as core pairs) | LOW | Mechanically identical to the existing →PDF path: same `soffice --headless --convert-to <ext>` invocation, just swap the target extension/filter (`odt`, `ods`, `odp`, `docx`, `xlsx`, `pptx` are all native LibreOffice import/export filters — no new binary, no new container). Registers as more `Pairs()` entries on the existing `LibreOfficeConverter`. |
| Pre-flight rejection of encrypted/password-protected docs with 422 | Every conversion API surveyed (CloudConvert, ConvertAPI) treats "password protected" as a *distinct, named* error rather than a generic failure — ConvertAPI documents error code 5003 "File is password protected"; api2convert returns a dedicated password-required error. Silently timing out inside `soffice` (current behavior) is a worse UX and wastes a full `DOCUMENT_ENGINE_TIMEOUT` window per rejected job | LOW-MEDIUM | **Verified mechanism (HIGH confidence):** password-protected OOXML files (.docx/.xlsx/.pptx) are, at the byte level, re-encoded as an **OLE2 Compound File Binary (CFB)** container — signature `D0 CF 11 E0 A1 B1 1A E1` — wrapping `EncryptionInfo`/`EncryptedPackage` streams, instead of the normal ZIP (`PK\x03\x04`) signature a real docx/xlsx/pptx has. So detection needs **no OLE parsing at all**: if content-sniffing for a `docx`/`xlsx`/`pptx` target detects the CFB signature instead of the ZIP signature, reject immediately. This is a single new magic-byte check, consistent with the project's existing zero-dependency magic-bytes validation phase (Phase 4, VALID). |
| Pre-flight rejection of legacy binary doc/xls/ppt (pre-2007) | Same CFB signature as above is *also* the native, unencrypted format of legacy `.doc`/`.xls`/`.ppt` — LibreOffice can technically still open many of these but they carry disproportionate risk (old binary parsers are the most fragile/most CVE'd part of any office suite) | LOW | Because both "encrypted OOXML" and "legacy binary Office" collapse to the *same* CFB magic-byte signature, one check covers both requirements DOC-V2-02 asks for: **any CFB-signature file submitted for conversion → 422**, worded generically ("password-protected or legacy binary format not supported") rather than trying to distinguish the two sub-cases. |
| PDF/A-1b export via `opts` | PDF/A-1b is the most restrictive/most compatible archival level and the one every archival-conversion tool defaults to or supports first (based on PDF 1.4, no transparency/JPEG2000 dependency, works with the oldest validators) | LOW-MEDIUM | `jobs.options jsonb` column already exists in the schema and is currently unused ("первое реальное использование поля `opts`" per PROJECT.md) — this feature is the first consumer. Maps directly to LibreOffice's documented `SelectPdfVersion` filter parameter: `1` = PDF/A-1b, `2` = PDF/A-2b, `3` = PDF/A-3b (`0`/`15`-`17` = plain PDF 1.4-1.7). Confirmed via LibreOffice's own CLI docs (`help.libreoffice.org/.../pdf_params.html`) and Gotenberg's public API which exposes exactly this as a closed enum: `pdfa=PDF/A-1b|PDF/A-2b|PDF/A-3b`. |
| HTML→PDF: single self-contained HTML file input | Every HTML→PDF service (Gotenberg's `/forms/chromium/convert/html`, wkhtmltopdf-based tools) treats "one HTML document" as the base case; internal clients producing reports/invoices server-side will usually inline CSS/base64 images already | LOW-MEDIUM | Fits the existing single-input/single-output job model with **zero schema change** — the uploaded file is just HTML instead of an office doc or image. This should be the v1.3 baseline; assumes client pre-inlines assets. |
| HTML→PDF: page size, margins, print background, landscape | These are the option set every Chromium-based PDF tool (Gotenberg, Puppeteer/Playwright PDF, wkhtmltopdf) exposes as its *minimum* API surface — without `printBackground` most CSS-styled reports render with missing background colors/images by default (Chromium's own default omits print backgrounds) | LOW-MEDIUM | Verified Gotenberg's exact param set: `paperWidth`/`paperHeight` (or standard size presets), `marginTop/Bottom/Left/Right`, `landscape` (bool), `printBackground` (bool), `scale`. These map 1:1 onto Chromium DevTools Protocol's `Page.printToPDF` params — same params Puppeteer/Playwright expose. Treat as the `opts` schema for the new engine. |
| HTML→PDF: bounded render wait (`waitDelay`) | JS-rendered content (charts, web fonts) needs a settle window before printing or it captures a half-rendered page — this is universally supported (Gotenberg `waitDelay`, Puppeteer `page.waitForTimeout` equivalent) | LOW | A simple fixed/bounded delay (e.g. `wait_ms`, capped server-side) is enough; must be capped well under `ENGINE_TIMEOUT`-equivalent for the chromium engine so a client can't stall a worker slot indefinitely. |
| Webhook delivery independent of any single engine worker | An internal ops team routinely deploys a subset of engine workers (e.g. only `document-worker` during a partial rollout or an image-worker incident) — webhooks must still fire | MEDIUM | **Verified as a real, already-present gap (HIGH confidence, read from source):** `cmd/worker/main.go` is currently the **sole consumer** of `queue.QueueWebhook` (`mux.HandleFunc(queue.TypeWebhookDeliver, h.HandleWebhookDeliver)` only registered there; `cmd/document-worker/main.go` has an explicit comment "`document-worker` neither delivers nor signs webhooks... `cmd/worker` remains the sole webhook consumer"). If ops deploys `document-worker` (or a future `chromium-worker`) without also deploying the image `worker` binary, every `webhook:deliver` task queues forever — this is exactly SEED-002. |

### Differentiators (Competitive Advantage)

| Feature | Value Proposition | Complexity | Notes |
|---------|-------------------|------------|-------|
| PDF/A-2b and PDF/A-3b as selectable levels (not just 1b) | PDF/A-2b is the level most archival/compliance guidance now recommends by default for *new* archival projects (PDF 1.7 base, transparency + JPEG2000 support, smaller files); PDF/A-3b adds arbitrary file-attachment embedding (e.g. source XLSX alongside a PDF/A invoice — the ZUGFeRD/Factur-X pattern) | LOW (once 1b is wired, it's the same `SelectPdfVersion` enum: `2`, `3`) | Cheap to add once the `opts`-driven plumbing for 1b exists — same filter parameter, just a different accepted enum value. Good "do it while you're in there" scope addition. |
| Zero-dependency CFB detection reusing existing magic-bytes philosophy | Competing approach (oletools/olefile-style full OLE directory-stream parsing to distinguish "just legacy" vs "actually encrypted") adds a real parsing attack surface for untrusted input and a new dependency — a single 8-byte signature check gets the same practical outcome (reject both) with none of that risk | LOW | This is a genuine differentiator vs. how Python-ecosystem tools (`msoffcrypto-tool`, `oletools`) solve this — those fully parse the CFB directory/FAT structure to read `EncryptionInfo`; OctoConv doesn't need to because it isn't trying to *decrypt*, only to *reject*. |
| Dedicated `webhook-worker` binary, consuming only `queue.QueueWebhook` | Matches the project's own established engine-class-per-binary convention (`worker`, `document-worker`, future `chromium-worker`) — webhook delivery becomes its own independent "class" rather than a side-responsibility bolted onto whichever engine binary happened to be first | MEDIUM | This is the standard industry pattern for webhook reliability at any scale: separate event/task ingestion from delivery workers so a delivery-side slowdown or a partial-fleet deploy never blocks (or silently drops) delivery (Hookdeck's "Building a Reliable Service for Sending Webhooks" and general webhook-architecture writeups converge on exactly this: dedicated delivery worker pool, decoupled queue). For OctoConv concretely: move `mux.HandleFunc(queue.TypeWebhookDeliver, ...)` + the asynq `Queues: {QueueWebhook: N}` registration out of `cmd/worker` into a new small binary/container that has no image/document/chromium engine dependencies at all. |

### Anti-Features (Commonly Requested, Often Problematic)

| Feature | Why Requested | Why Problematic | Alternative |
|---------|---------------|-----------------|-------------|
| HTML→PDF via URL fetch (`convert this URL`) | Feels natural — Gotenberg itself offers a `/forms/chromium/convert/url` route, and clients may ask "just give it a link" | Directly re-opens the exact SSRF surface `callback_url` already required a dedicated guard for (RFC1918/loopback/link-local/metadata-endpoint blocking, `WEBHOOK_ALLOW_PRIVATE_IPS` opt-out) — except here the "URL" is fetched *by the chromium engine itself*, rendering arbitrary internal pages, following redirects, executing arbitrary JS on whatever the URL resolves to. For an internal service where clients are other internal services, this is materially worse than the webhook case because it also executes a full browser engine against the fetched target | Require clients to upload rendered/pre-fetched HTML (+ inlined assets) instead of a URL. If URL-fetch is ever revisited, it needs the *same* SSRF allow/deny model as webhooks, not a fresh one, plus explicit opt-in per client. |
| HTML+external-assets as a zip bundle in v1.3 scope | Clients with existing CSS/image/webfont pipelines will ask for this immediately after single-file HTML ships | Requires either (a) relaxing the single-input/single-output job assumption documented as an explicit architectural constraint, or (b) in-worker zip extraction + path traversal hardening (zip-slip) — a second content-validation surface on top of the CFB/magic-bytes one just being added for documents. Meaningful scope for a feature not explicitly requested in the v1.3 target list | Ship self-contained single-HTML-file input first (inline CSS/base64 assets); revisit zip-bundle support as its own follow-up once real client demand appears, reusing the existing zip-based content-validation patterns from image/document formats rather than inventing new ones ad hoc. |
| `waitForExpression` (arbitrary JS condition evaluated in the page before printing) | Power users converting JS-heavy dashboards will want "wait until this JS expression is true" (Gotenberg supports it) | Means evaluating a client-supplied JavaScript string inside the browser context on every conversion — a meaningfully larger sandbox-escape/DoS surface (infinite-looping expressions, `eval`-adjacent semantics) than a bounded `waitDelay`, for an internal-service audience that doesn't need it yet | Ship a capped `waitDelay` (e.g. max a few seconds, well under the engine timeout) for v1.3; defer expression-based waiting until a concrete internal client need justifies the added attack surface. |
| Combining PDF/A export with encryption/password-protecting the *output* | Seems like a reasonable "make it both archival and locked" request | PDF/A and PDF encryption are specified as mutually exclusive by the PDF/A standard itself (Gotenberg's docs explicitly reject requesting both: 400 Bad Request) — building support for the combination means building something the spec forbids | Reject the combination outright with a clear 422 if both are ever requested via `opts`; don't attempt to support it. |
| Fidelity/diff reporting on cross-format conversions ("tell me what changed") | Cross-format office conversion (docx↔odt etc.) has well-known, real fidelity gaps (custom numbering/multi-level lists, array formulas, chart types, animations, VBA/Basic macros never transfer) and a cautious client might want a warning | Building any kind of structural diff/fidelity-scoring between source and converted output is a large, open-ended problem (semantic document comparison) far outside what LibreOffice's engine or any of the surveyed competitor APIs (CloudConvert, Zamzar, ConvertAPI) attempt — none of them return fidelity warnings, they document known gaps in prose instead | Document known fidelity caveats in API docs/README (macros, multi-level lists, complex charts, animations) rather than trying to detect/report them at conversion time; this matches how every competitor surveyed handles it. |

## Feature Dependencies

```
Cross-format conversion (docx<->odt, xlsx<->ods, pptx<->odp)
    └──requires──> existing LibreOffice document-worker + Converter/Registry pattern (already shipped, v1.2)
    └──requires──> pre-flight CFB rejection (should land first: otherwise cross-format
                    pairs are the first place a password-protected/legacy binary file
                    would previously have silently timed out soffice instead of 422ing)

PDF/A opts-driven export
    └──requires──> jobs.options jsonb column (already exists, unused — first real consumer)
    └──requires──> opts schema validation (closed enum: "1b"|"2b"|"3b", not raw filter passthrough)
    └──conflicts──> encrypted/password-protected output request (mutually exclusive per PDF/A spec)

HTML->PDF chromium engine (third engine class)
    └──requires──> new engine-class scaffolding, following the v1.2 image/document pattern
                    (Engine()/EngineFor, separate binary+container, fail-closed routing)
    └──requires──> jobs.engine CHECK constraint migration — current constraint only allows
                    ('image','document','av','cad','archive','probe'); no 'html'/'chromium'
                    value exists yet, this blocks routing until a migration adds one
    └──enhances──> opts-driven page/margin/background options (reuses the same opts
                    plumbing being built for PDF/A)

Webhook delivery decoupling (SEED-002)
    └──requires──> extracting queue.TypeWebhookDeliver consumption out of cmd/worker
                    into its own binary/container (currently the SOLE consumer per
                    an explicit comment in cmd/document-worker/main.go)
    └──independent-of──> the other four v1.3 features (can ship in any order relative
                    to them, but should land early since it's flagged as a standalone
                    reliability gap, not tied to the new engine work)
```

### Dependency Notes

- **Cross-format conversion requires the CFB pre-flight check to land first (or at worst, simultaneously):** without it, the *new* cross-format pairs are exactly where an encrypted/legacy file would previously have silently exhausted `DOCUMENT_ENGINE_TIMEOUT` inside `soffice` instead of getting a fast 422 — the existing →PDF path has this same latent gap today, so DOC-V2-02 should really be scoped as "fix for all document engine inputs," not "fix scoped to the new pairs."
- **HTML→PDF requires a schema migration before any routing work is meaningful:** `jobs.engine` is a Postgres `CHECK` constraint currently enumerating exactly `('image','document','av','cad','archive','probe')`. There is no slot for the new engine class yet — this needs to be added in an early phase or the whole engine-class scaffolding (mirroring v1.2's `Engine()`/`EngineFor`/reconciler-routing pattern) has nowhere to attach.
- **PDF/A and HTML→PDF share the same `opts` plumbing:** whichever lands first should design the `opts` JSON shape (closed, validated keys — not raw LibreOffice/Chromium filter-string passthrough) so the second doesn't have to retrofit it. Recommend PDF/A first (smaller surface, reuses only the document engine) to shake out the `opts` validation pattern before HTML→PDF's larger option set arrives.
- **Webhook decoupling conflicts with nothing and blocks nothing** — it's an orthogonal reliability fix to existing infrastructure (webhook queue/table already exist since v1.0/v1.2) and can be sequenced independently, but shipping it *before* the new chromium engine avoids adding a third engine binary that would also need the "does this binary deliver webhooks?" question answered.

## MVP Definition

### Launch With (v1.3)

- [ ] docx↔odt, xlsx↔ods, pptx↔odp cross-conversion via existing LibreOffice engine — directly requested (DOC-V2-01), mechanically cheap
- [ ] CFB magic-byte pre-flight rejection (422) covering both encrypted OOXML and legacy binary doc/xls/ppt — directly requested (DOC-V2-02), prevents silent timeouts across *all* document engine inputs including existing →PDF pairs
- [ ] PDF/A-1b export via `opts` (minimum: one conformance level, closed enum) — directly requested (DOC-V2-03), first real use of `jobs.options`
- [ ] HTML→PDF: single self-contained HTML file input, with page size/margins/landscape/printBackground/bounded waitDelay options, new engine class — directly requested (DOC-V2-04), highest-risk item per project's own milestone notes
- [ ] Webhook delivery decoupled into its own consumer, independent of any engine worker binary — directly requested (SEED-002), closes a verified existing gap

### Add After Validation (v1.x)

- [ ] PDF/A-2b and PDF/A-3b as additional selectable conformance levels — trivial extension of the 1b plumbing once opts validation exists; add once a real client asks for transparency/JPEG2000 support or attachment embedding
- [ ] HTML→PDF with a zip-bundle (HTML + external CSS/image/font assets) input mode — add once a concrete internal client's HTML actually needs external assets rather than inlined ones
- [ ] `waitForExpression`-style JS-condition wait for HTML→PDF — add only if a specific JS-heavy client workload demonstrably needs more than a bounded delay

### Future Consideration (v2+)

- [ ] URL-fetch HTML→PDF input — explicitly deferred; would need its own from-scratch SSRF allow/deny model mirroring the webhook one, not a quick add
- [ ] Fidelity/diff reporting on cross-format conversions — explicitly out of scope; no surveyed competitor does this either

## Feature Prioritization Matrix

| Feature | User Value | Implementation Cost | Priority |
|---------|------------|---------------------|----------|
| Cross-format docx/xlsx/pptx ↔ odt/ods/odp | HIGH | LOW | P1 |
| CFB pre-flight rejection (encrypted + legacy) | HIGH | LOW-MEDIUM | P1 |
| PDF/A-1b export via `opts` | MEDIUM | LOW-MEDIUM | P1 |
| PDF/A-2b/3b additional levels | LOW-MEDIUM | LOW | P2 |
| HTML→PDF (single-file input, core options) | HIGH | MEDIUM-HIGH | P1 |
| HTML→PDF zip-bundle assets | MEDIUM | MEDIUM | P2 |
| HTML→PDF waitForExpression | LOW | MEDIUM-HIGH | P3 |
| Webhook delivery decoupling | HIGH | MEDIUM | P1 |
| HTML→PDF URL-fetch input | LOW (for this audience) | HIGH (+ new SSRF model) | P3 / anti-feature |

**Priority key:**
- P1: Must have for v1.3
- P2: Should have, add when possible in a later minor
- P3: Nice to have, future consideration (or explicitly rejected, see Anti-Features)

## Competitor Feature Analysis

| Feature | Gotenberg (self-hosted, closest architectural analog) | CloudConvert / ConvertAPI / Zamzar (hosted SaaS) | Our Approach |
|---------|--------------------------------------------------------|---------------------------------------------------|--------------|
| Cross-format office conversion | Routes everything through the same LibreOffice module regardless of target (`/forms/libreoffice/convert`), just varies output filter | CloudConvert task graph specifies `input_format`/`output_format` per task; both directions supported for docx/odt, xlsx/ods, pptx/odp | Same pattern as Gotenberg: reuse the existing `LibreOfficeConverter`, add pairs to its `Pairs()` list — no new engine needed |
| Password-protected detection | Not a first-class documented feature (Gotenberg assumes trusted input in its threat model — it's typically deployed behind your own app) | ConvertAPI: dedicated error code 5003 "File is password protected"; api2convert: dedicated password-required error/401 | Detect via CFB magic bytes pre-flight (own capability, not delegated to the engine failing) — stronger than Gotenberg's approach, matches competitor SaaS practice of a named error |
| PDF/A conformance levels | `pdfa` form field accepts `PDF/A-1b`/`PDF/A-2b`/`PDF/A-3b` as a closed enum on `/forms/pdfengines/convert`, `/merge`, and the Chromium routes — implemented as a LibreOffice post-processing pass | Not consistently surfaced as a top-level, named option across the surveyed SaaS APIs (more often it's an implicit "PDF/A" output format choice) | Adopt Gotenberg's exact modeling: closed `opts` enum mirroring `SelectPdfVersion`, starting with `1b` and adding `2b`/`3b` cheaply |
| HTML→PDF input model | Two separate routes: file-based (`index.html` + `files` assets, flat directory) vs `convert/url` (fetch a URL) — clearly modeled as distinct capabilities with different trust assumptions | Hosted SaaS tools generally support both HTML upload and URL fetch since they already operate as a trusted third party fetching public URLs | Only the file-based model — no URL-fetch route at all, given the internal-service SSRF posture already established for webhooks |
| Webhook delivery architecture | N/A — Gotenberg is synchronous/stateless per request, no webhook concept | ConvertAPI uses async task + webhook-on-completion; none of the surveyed docs describe internal decoupling of delivery workers from processing workers (not publicly documented, likely irrelevant to their scale/ops model) | Standard event-driven pattern (separate delivery queue + dedicated delivery worker) per general webhook-architecture guidance (Hookdeck et al.), applied to fix OctoConv's specific verified gap: `cmd/worker` currently the sole webhook consumer |

## Sources

- [PDF/A & PDF/UA — Gotenberg docs](https://gotenberg.dev/docs/manipulate-pdfs/pdfa-pdfua) — `pdfa=PDF/A-1b|2b|3b` closed enum, PDF/A + encryption mutual exclusivity (MEDIUM confidence, WebSearch-derived summary of official docs, not directly fetched)
- [Convert HTML to PDF — Gotenberg docs](https://gotenberg.dev/docs/convert-with-chromium/convert-html-to-pdf) (fetched directly — MEDIUM-HIGH confidence) — `index.html` + `files` asset model, full option set (paperWidth/Height, margins, landscape, scale, printBackground, omitBackground, waitDelay, waitForExpression, preferCssPageSize)
- [Convert URL to PDF — Gotenberg docs](https://gotenberg.dev/docs/convert-with-chromium/convert-url-to-pdf) — confirms URL-fetch exists as a *separate* route/capability from file upload (MEDIUM confidence)
- [PDF Export Command Line Parameters — LibreOffice Help](https://help.libreoffice.org/latest/en-US/text/shared/guide/pdf_params.html) (fetched directly — HIGH confidence) — `SelectPdfVersion` values: `1`=PDF/A-1b, `2`=PDF/A-2b, `3`=PDF/A-3b, `0`/`15-17`=plain PDF
- [LibreOffice Command Line: Convert Multiple Files DOCX to ODT](https://www.ubuntubuzz.com/2016/08/libreoffice-command-line-convert-multiple-files-docx-to-odt.html) — confirms `--convert-to odt` mechanics apply symmetrically to any supported target filter (MEDIUM confidence)
- [Encrypted OOXML Documents — Didier Stevens](https://blog.didierstevens.com/2018/06/07/encrypted-ooxml-documents/) and [Overview of Protected Office Open XML Documents — Microsoft Learn](https://learn.microsoft.com/en-us/archive/blogs/openspecification/overview-of-protected-office-open-xml-documents) — confirm encrypted OOXML files are re-encoded as OLE2/CFB containers with `EncryptionInfo`/`EncryptedPackage` streams (HIGH confidence — MS-authored spec background + independent forensic write-up agree)
- [ConvertAPI Response Codes](https://docs.convertapi.com/docs/response-codes) and api2convert error docs — dedicated "password protected" error codes (MEDIUM confidence, WebSearch-derived)
- [What Are the Different Versions of PDF/A? — Apryse](https://apryse.com/blog/pdfa-format/what-are-the-different-types-of-pdfa) and [How to Pick the Right Version of PDF/A — Apryse](https://apryse.com/blog/pdfa-format/how-to-pick-right-version-of-pdfa) — conformance-level tradeoffs and default recommendations (MEDIUM confidence, single-vendor source but consistent with Wikipedia/PDFlib coverage seen in the same search)
- [TDF Community Blog — resolving ODF compatibility issues](https://blog.documentfoundation.org/blog/2025/09/12/how-to-resolve-odf-compatibility-issues/) — cross-format fidelity gaps (multi-level lists, array formulas, animations, macros) (MEDIUM confidence)
- [Building a Reliable Service for Sending Webhooks — Hookdeck](https://hookdeck.com/blog/building-reliable-outbound-webhooks) and [Webhook Infrastructure Components — Hookdeck](https://hookdeck.com/webhooks/guides/webhook-infrastructure-components-and-their-functions) — decoupled ingestion/delivery worker pattern (MEDIUM confidence, industry-practice writeups not a formal spec)
- **Direct codebase reads (HIGH confidence, primary source):** `internal/convert/convert.go` (Converter interface `opts map[string]any`), `internal/db/migrations/0001_init.sql` (`jobs.options jsonb` already present and unused; `jobs.engine` CHECK constraint has no `html`/`chromium` value yet), `cmd/worker/main.go` + `cmd/document-worker/main.go` (confirms `cmd/worker` is currently the sole consumer of `queue.QueueWebhook`/`TypeWebhookDeliver` — the concrete SEED-002 gap), `internal/queue/queue.go` (queue/task-type constants)

---
*Feature research for: OctoConv v1.3 Document Class v2*
*Researched: 2026-07-10*
