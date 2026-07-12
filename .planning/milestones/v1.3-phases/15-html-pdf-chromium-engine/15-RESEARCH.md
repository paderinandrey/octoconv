# Phase 15: HTML→PDF Chromium Engine - Research

**Researched:** 2026-07-11
**Domain:** Headless Chromium CLI invocation for HTML→PDF conversion, network-level SSRF containment, container process-reaping, Postgres CHECK-constraint migration for a 4th engine class
**Confidence:** MEDIUM — see per-question breakdown below. This phase's own CONTEXT.md correctly anticipated the two highest-uncertainty items (exact print-option CLI syntax, tini necessity); this research resolves one of them from LOW to a documented CRITICAL finding (print options have **no CLI equivalent** — must go through CSS injection, not chromium flags) and narrows the other with corroborating evidence, but still requires live confirmation during execution.

## Summary

This is a refinement pass on top of an already-locked architecture (CONTEXT.md D-01..D-09). Nothing here contradicts a locked decision. The single most important finding is that **D-06's literal text ("print options → chromium flags from a server-side table, as PDFAFilterOptions in Phase 14") is not achievable as written**: Chromium's official `headless_command_switches.cc` source lists exactly nine CLI switches for `chromium-headless-shell`'s one-shot print-to-pdf mode, and none of them control page size, margins, landscape orientation, or background-graphics printing. Those four options (HTML-03's entire surface) can only be controlled from inside the HTML/CSS itself (`@page` rules + `print-color-adjust`), not from argv. This does **not** force an escalation to CDP/chromedp (D-02's fallback trigger is a *network*-leak finding, not a print-options gap) — it means the mechanism for HTML-03 changes from "validated enum → CLI flag" to "validated enum → server-constant CSS block injected into a worker-generated copy of the HTML," while preserving the exact same security invariant Phase 14 established (client bytes never reach the engine invocation; only pre-validated enum values select from a fixed, compile-time set of strings).

The network-block layering (D-03) is sound and the two flags are real, correctly-named Chromium switches — but there is one concrete, well-documented gap: Chrome bypasses `--proxy-server` for loopback/localhost addresses by default (since Chrome 72), so `--proxy-bypass-list=<-loopback>` must be added as a third flag or a page that fetches `http://127.0.0.1:<port>` or `http://localhost:<port>` will attempt a **direct** (unproxied) connection instead of dying at the dead proxy port. This is additive to D-03, not a contradiction of it. `file://` resource references are a second, separate residual risk this research surfaces (Priority-2 finding below) — outside D-03's network-flag scope entirely, since `file://` never touches the network stack the proxy/resolver flags govern.

Package name, binary name, and CLI switch names are all confirmed HIGH confidence via the official Debian package archive and Chromium's own source tree. tini-necessity and the exact process-group behavior of a one-shot `--print-to-pdf` invocation remain genuinely MEDIUM/LOW confidence — general Docker+Chromium guidance strongly favors keeping tini, but no source specifically confirms or denies it for this exact non-CDP, one-shot, `--print-to-pdf`-only invocation shape. This is correctly flagged in CONTEXT.md as "verify live, don't assume," and this research adds one concrete new lever to test: `--no-zygote`, which some containerized-Chromium configs use specifically to avoid forking a zygote process at all (trading process-pool warm-start performance neither this project nor a one-shot CLI invocation needs).

**Primary recommendation:** Keep every locked decision (D-01 through D-09) as-is. Correct D-06's mechanism from "CLI flags" to "CSS injection into a worker-generated HTML copy, built from server-side constants keyed by the same validated-opts pattern as Phase 14." Add `--proxy-bypass-list=<-loopback>` as a required third network-block flag. Treat tini as "keep it" (matches D-09's own bias) but budget one live experiment with `--no-zygote` as an alternative/complement, and one live experiment confirming or denying whether `file://` resource references can read files outside the job's own input.

## Architectural Responsibility Map

OctoConv is a backend-only Go service (no browser/CDN/SSR tiers); the standard web-tier table doesn't map cleanly, so this uses the project's own established component vocabulary (API / Worker / Queue / Storage / DB) instead.

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| HTML content detection (fail-closed) | API (`internal/api/handlers.go`) | — | Same sniff-chain discipline as every other format; must run before any storage write (HTML-01) |
| HTML→PDF rendering | Worker (`internal/convert/chromium.go`, `cmd/chromium-worker`) | — | New 4th engine class; owns the actual `chromium-headless-shell` invocation |
| Print-option validation & CSS-constant selection | Worker (`internal/convert/chromium.go` or new `htmlopts.go`) | API (structural validation only) | Mirrors Phase 14: API validates+persists the closed enum, the engine-side code selects the compile-time CSS string — never the API layer building CSS |
| Network egress denial | Worker process (CLI flags) | Container/compose (network layer, defense-in-depth) | D-03's own layering — CLI is primary, container network is secondary |
| Job routing / engine-class dispatch | API + Queue (`EngineFor`, `internal/queue`) | Reconciler | Existing `Converter.Engine()` pattern, zero interface change |
| `jobs.engine` CHECK constraint | DB (Postgres migration) | — | Hard prerequisite gate — no `html` job can be created before this lands |
| Output validation | Worker (`validatePDF`, reused verbatim) | — | `target` is always `"pdf"` for this engine; zero new validator code needed |

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| HTML-01 | Third engine class (own queue/worker/binary/container, terminal-classified timeout) for HTML→PDF via chromium | Confirmed package/binary names, exact migration text, exact queue/routing integration points (file:line), terminal-classification recommendation mirroring `isDocumentTerminal` |
| HTML-02 | Offline rendering — engine cannot fetch external network resources; no URL-fetch input mode | Confirmed `--proxy-server`+`--host-resolver-rules` are real, correctly-named flags; found and must-fix loopback-bypass gap (`--proxy-bypass-list=<-loopback>`); documented `file://` residual risk as a separate, must-verify-live item; concrete canary-test mechanism reusing existing E2E infra |
| HTML-03 | page_size / margin_mm / landscape / print_background via validated-opts | **Critical finding**: no CLI equivalent exists for any of these four options in one-shot `--print-to-pdf` mode (confirmed against Chromium's own switch-definition source); corrected mechanism is CSS injection from server-side constants, preserving Phase 14's security invariant |
</phase_requirements>

## Standard Stack

### Core
| Package | Version (verify live) | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `chromium-headless-shell` (Debian bookworm apt package) | `150.0.7871.100-1~deb12u1` per current Debian archive snapshot [CITED: packages.debian.org] — a WebSearch pass separately surfaced `145.0.7632.116-1~deb12u1`; the two do not agree, treat the exact pinned version as **[ASSUMED — verify live]** via `apt-cache policy chromium-headless-shell` at build time, since bookworm security/backports channels roll | one-shot, no-DevTools-required headless rendering binary, split out of full `chromium` specifically at Chrome 132 to keep a pure-CLI `--print-to-pdf` mode [CITED: developer.chrome.com/docs/chromium/headless] | Exactly the shape `runCommand` needs (D-01); rejects full `chromium` (5x heavier, GUI deps) |
| `tini` | already used, pinned by Debian bookworm (same as `Dockerfile.document-worker`) | PID-1 init/reaper | Not a new dependency — already proven in this codebase for LibreOffice's fork chain (D-09) |

No new Go module, npm, or PyPI/pip package is introduced by this phase — confirms CLAUDE.md's zero-new-Go-dependencies value. The only new external artifact is the OS-level apt package above.

### Binary details (verified via Debian package filelist) [CITED: packages.debian.org/bookworm/amd64/chromium-headless-shell/filelist]
- `/usr/bin/chromium-headless-shell` — the executable to invoke (this is the Debian-renamed binary; upstream/Chrome-for-Testing calls the equivalent binary `chrome-headless-shell` — do not confuse the two names when reading upstream docs vs. writing the Dockerfile `RUN`/exec command)
- `/usr/lib/chromium/chromium-headless-shell` — underlying binary the above wraps/symlinks to
- Depends on `chromium-common` (same version) plus the standard headless-browser shared-lib set (`libnss3`, `libgbm1`, `libasound2`, `libxcomposite1`, `libharfbuzz0b`, `libfreetype6`, `libfontconfig1`, etc. — apt resolves these automatically, no manual listing needed in the Dockerfile beyond the package name itself)
- **No fonts are bundled.** Mirror `Dockerfile.document-worker`'s font-install pattern: at minimum `fonts-liberation2` (already used there) for Latin text; if any internal HTML corpus needs non-Latin scripts, additional font packages (e.g. `fonts-noto-cjk`) inflate the image non-trivially — flag as a scope question for the planner, don't assume [CITED: architecture research, corroborated by oneuptime.com/hexdocs ChromicPDF Docker guides, MEDIUM confidence]

### Package Legitimacy Audit

The standard slopcheck/npm-registry protocol does not apply here — `chromium-headless-shell` is an OS-level apt package from the official Debian archive, not a language-ecosystem package (npm/PyPI/crates) subject to typosquatting/slopsquatting risk in the same way. Its provenance is the Debian archive itself (`packages.debian.org`), which is authoritative.

| Package | Registry | Age | Source | Disposition |
|---------|----------|-----|--------|-------------|
| `chromium-headless-shell` | Debian bookworm apt archive | Long-lived Debian-maintained package, tracks upstream Chromium security releases | Official Debian archive, upstream Chromium project | Approved — official OS package, not a language-ecosystem registry; slopcheck N/A |
| `tini` | Debian bookworm apt archive | Already in production use (`Dockerfile.document-worker`) | Official Debian archive | Approved — pre-existing, unchanged |

**Packages removed due to slopcheck verdict:** none (not applicable — no npm/PyPI/crates packages in this phase).
**Go modules added:** none — zero new entries in `go.mod`/`go.sum`.

## Architecture Patterns

### System Architecture Diagram (HTML→PDF path only — see `.planning/research/ARCHITECTURE.md` for the full milestone map)

```
Client                    API                          Redis/asynq        chromium-worker                  S3/Postgres
  |  multipart POST         |                                |                    |                             |
  |------------------------>| Sniff()->"" (no magic bytes)   |                    |                             |
  |                         | source=="html"/"htm" (from ext)|                    |                             |
  |                         | + LooksLikeHTML(file) content  |                    |                             |
  |                         |   check (UTF-8, no NUL,        |                    |                             |
  |                         |   <!doctype html>/<html> marker)                    |                             |
  |                         | -> detected="html"              |                   |                             |
  |                         | EngineFor("html","pdf")->"html" |                   |                             |
  |                         | validate opts (page_size/       |                   |                             |
  |                         |   margin_mm/landscape/          |                   |                             |
  |                         |   print_background) closed enum |                   |                             |
  |                         |------------------ Upload(S3, uploads/{id}/0-file.html) ------------------------->|
  |                         |------------------ Repo.Create(engine="html", opts) ------------------------------>|
  |                         |----------- EnqueueHTMLConvert -->|                   |                             |
  |                         |                                  |--- dequeue ------>|                             |
  |                         |                                  |                   |<--- download input.html ---|
  |                         |                                  |                   | build rendered.html:       |
  |                         |                                  |                   |   copy input +             |
  |                         |                                  |                   |   inject <style>@page{     |
  |                         |                                  |                   |     size/margin} +         |
  |                         |                                  |                   |     print-color-adjust     |
  |                         |                                  |                   |   as LAST <head> child      |
  |                         |                                  |                   | chromium-headless-shell     |
  |                         |                                  |                   |   --headless                |
  |                         |                                  |                   |   --disable-gpu              |
  |                         |                                  |                   |   --no-sandbox                |
  |                         |                                  |                   |   --disable-dev-shm-usage      |
  |                         |                                  |                   |   --blink-settings=            |
  |                         |                                  |                   |     scriptEnabled=false          |
  |                         |                                  |                   |   --proxy-server=127.0.0.1:9       |
  |                         |                                  |                   |   --proxy-bypass-list=<-loopback>   |
  |                         |                                  |                   |   --host-resolver-rules=            |
  |                         |                                  |                   |     "MAP * ~NOTFOUND"                 |
  |                         |                                  |                   |   --print-to-pdf=out.pdf                |
  |                         |                                  |                   |   file:///workDir/rendered.html          |
  |                         |                                  |                   | validatePDF(out.pdf) (reused verbatim)     |
  |                         |                                  |                   |----- upload out.pdf, MarkDone, webhook --->|
```

### Recommended Project Structure (new files, mirroring `document-worker`'s shape)
```
internal/convert/
├── chromium.go        # ChromiumConverter{Pairs, Convert, Engine} — mirrors libreoffice.go's shape
├── htmlopts.go         # HTMLOpts struct + ParseHTMLOpts/ValidateApplicability, mirrors opts.go's DocOpts pattern
├── htmlsniff.go         # LooksLikeHTML(r io.ReaderAt, size int64) bool — mirrors olecfb.go's shape
cmd/chromium-worker/
├── main.go               # near-identical skeleton to cmd/document-worker/main.go
Dockerfile.chromium-worker
internal/db/migrations/
└── 0005_html_engine.sql   # jobs.engine CHECK constraint, hard prerequisite
```

### Pattern 1: Print options as CSS injection, not CLI flags (CRITICAL — corrects D-06's literal mechanism)

**What:** `chromium-headless-shell`'s one-shot print-to-pdf mode has **exactly nine** CLI switches, verified directly against Chromium's own switch-definition source [VERIFIED: source.chromium.org — `components/headless/command_handler/headless_command_switches.cc`]:

```
--default-background-color
--dump-dom
--print-to-pdf[=<path>]
--no-pdf-header-footer
--disable-pdf-tagging
--generate-pdf-document-outline
--screenshot
--timeout=<ms>
--virtual-time-budget=<ms>
```

None of these control page size, margins, landscape orientation, or background-graphics printing. Multiple independent sources confirm this is a deliberate, known limitation of one-shot CLI mode (full `Page.printToPDF` DevTools-protocol options — `paperWidth`/`paperHeight`/`marginTop`/etc. — are only reachable via CDP) [CITED: developer.chrome.com/docs/chromium/headless; MEDIUM confidence, corroborated by andre.arko.net and a Debian-package-adjacent WebSearch result independently stating "you'll need to include these settings in your HTML content"].

**When to use:** Applies directly to HTML-03. D-06's "server-side table → chromium flags" instruction must become "server-side table → CSS block injected into a worker-built copy of the input HTML." This is NOT a violation of D-01/D-02 (no CDP, no chromedp, no new Go dependency) — the argv stays exactly `chromium-headless-shell <flags> --print-to-pdf=<path> file://<renderedPath>`; only the *mechanism* for HTML-03 changes from argv-based to HTML-text-based.

**Example (server-side constant table, mirrors `PDFAFilterOptions`'s "compile-time string, selected only by a validated enum" shape from `internal/convert/opts.go:148-153`):**
```go
// htmlPageSizeCSS maps the closed page_size enum to its CSS @page `size`
// keyword (CSS Paged Media spec keywords, not Chrome-specific).
var htmlPageSizeCSS = map[string]string{
    "a4":     "A4",
    "letter": "letter",
    "legal":  "legal",
    "a3":     "A3",
    "a5":     "A5",
}

// buildPrintCSS renders the fixed @page rule + background-forcing rule from
// ALREADY-VALIDATED HTMLOpts fields only -- never from raw client bytes
// (same invariant as PDFAFilterOptions, Pitfall 9's lesson applied here).
func buildPrintCSS(o HTMLOpts) string {
    size := htmlPageSizeCSS[o.PageSize] // o.PageSize already validated against the closed enum
    if o.Landscape {
        size += " landscape"
    }
    css := fmt.Sprintf("@page { size: %s !important; margin: %dmm !important; }\n", size, o.MarginMM)
    if o.PrintBackground {
        css += "*, *::before, *::after { -webkit-print-color-adjust: exact !important; print-color-adjust: exact !important; }\n"
    } else {
        css += "*, *::before, *::after { -webkit-print-color-adjust: economy !important; print-color-adjust: economy !important; }\n"
    }
    return "<style>" + css + "</style>"
}
```
`!important` on every injected property is deliberate: it must win the cascade regardless of any `@page`/print CSS the (untrusted) client HTML itself tries to set, and regardless of exactly where the block is injected relative to other `<style>`/`<link>` tags.

**Injection point (no new HTML-parsing dependency, consistent with D-07's own rejection of `x/net/html`):** case-insensitive search for `</head>` in the downloaded input and insert the `<style>` block immediately before it (last child of `<head>`, so it has cascade priority over anything else in `<head>`); if no `</head>` is found, insert immediately after the opening `<html ...>` tag (or at position 0 as a last-resort fallback — D-07's own content check already guarantees the file starts with `<!doctype html`/`<html` after whitespace/BOM, so a `</head>`-less-but-`<html>`-present case is the realistic fallback, not the position-0 case). Write the result to a **new** file (e.g. `workDir/rendered.html`) — never mutate or re-upload the S3-stored original. This mirrors the existing pattern where `LibreOfficeConverter.Convert` never touches `inPath` in place either.

**Verify live:** confirm CSS `@page` `size`/`margin` and `print-color-adjust` are actually honored by `chromium-headless-shell`'s print-to-pdf renderer (not just graphical Chrome's print dialog — multiple sources note headless and graphical Chrome print pipelines diverge) [CITED: andre.arko.net, MEDIUM confidence — "headless Chrome will silently refuse to fetch any resources referenced in your @page CSS rules" was one specific gotcha the author hit, relevant if page_size ever needs a background-image reference, which D-06's scope does not require].

### Pattern 2: Network-block flags — layered CLI, with one required addition

**What:** D-03's two flags are real, correctly-spelled Chromium switches (`--proxy-server`, `--host-resolver-rules`) — both are standard, long-standing Chromium networking switches, not headless-specific [CITED: multiple corroborating sources on the general "DNS sinkhole + dead proxy port" headless-Chrome network-blackholing technique]. **One required addition surfaced by this research:** Chrome has bypassed the configured proxy for loopback/localhost destinations by default since Chrome 72 — `http://127.0.0.1:<port>` or `http://localhost:<port>` referenced inside the HTML would attempt a **direct**, unproxied connection instead of failing at the dead `127.0.0.1:9` proxy port [CITED: zzz.buzz/2019/12/12/proxy-localhost-and-loopback-in-chrome, corroborated by a Chromium-adjacent selenium-wire GitHub issue and a Whitesmith engineering blog — MEDIUM-HIGH confidence, multiple independent sources agree on the same mechanism and remediation].

**How to avoid:** add a third flag, `--proxy-bypass-list=<-loopback>` — the documented negation syntax that removes the implicit loopback bypass, forcing loopback destinations through the (dead) proxy too:
```
--proxy-server=127.0.0.1:9 --proxy-bypass-list=<-loopback> --host-resolver-rules="MAP * ~NOTFOUND"
```
Practical blast radius today is low (nothing else listens on `chromium-worker`'s own loopback), but it is a genuine fail-open gap in the stated "fail closed against IP literals" threat model (Pitfall 11) and costs one flag to close — recommend closing it rather than accepting it as residual risk.

**Anti-Pattern to avoid:** relying on `--host-resolver-rules="MAP * ~NOTFOUND"` alone without `--proxy-server` — the resolver rule only affects *hostname* resolution; a raw IP literal (`http://169.254.169.254/`, `http://10.0.0.5/`) never goes through the resolver at all, so `--proxy-server` pointed at a dead port is the layer that actually stops IP-literal targets (the connection attempt to the dead proxy port itself fails, regardless of what final destination was requested) — this is *why* D-03 already specifies both flags together; this research just adds the loopback caveat to that existing correct design.

### Pattern 3: `file://` input, `file://` residual risk (new finding — Priority-2 research question)

**What:** The engine's only legitimate network-equivalent input is `file://<workDir>/rendered.html` (the job's own downloaded-and-CSS-injected copy). `data:` URIs referenced inside the HTML perform no network fetch at all regardless of the proxy/resolver flags — not a bypass, just inherently inert against this threat model (data is already inline). **The real open question is whether the untrusted HTML can use `file://` to read *other* files on the container filesystem** — e.g. `<img src="file:///etc/hostname">` or a sibling job's temp directory. Chromium's default `file://` access policy is permissive for resource loads (images/stylesheets/fonts) referenced from a `file://` origin; the proxy/resolver flags do nothing here since `file://` never touches the network stack they govern.

**Why this matters specifically for this container (not a generic Chromium fact):** `os.MkdirTemp` (used by `internal/worker/worker.go:441` to create each job's `workDir`) creates directories with mode `0700` — but every job in the same `chromium-worker` container runs as the **same** `nobody` UID, so `0700` does not isolate job A's `workDir` from job B's chromium invocation the way it would if jobs ran under distinct UIDs. This is a **pre-existing** property of the current container architecture (not new to this phase), but chromium is the first engine where `file://` is an *active, attacker-directed* read primitive inside the untrusted content itself, unlike LibreOffice/libvips where the input format has no equivalent "reference an arbitrary local path" construct.

**How to avoid / verify:** MUST smoke-test live during execution: render a fixture containing `<img src="file:///etc/hostname">` (a small, universally-world-readable file every Linux container has) and confirm whether the resulting PDF actually contains that content. Two outcomes:
- If Chromium's default file-access restrictions block it (some cross-origin file:// restrictions do exist for navigation, less so for subresource loads) — document that as the actual verified behavior, no further action.
- If it succeeds — this is a real, low-severity (internal-only-clients trust model, matches the project's other accepted-residual-risk items like DOC-V2-05) but real finding that should be logged as an explicit accepted residual risk in PROJECT.md's Key Decisions table, the same way Pitfall 8's PDF/A conformance gap was — not silently discovered later. A cheap mitigation if the finding is unacceptable: bind-mount only the job's own `rendered.html` (and any same-job assets) into a scratch directory chromium is launched with as its working context, rather than relying on ambient container filesystem visibility — flagged as an implementation option, not a requirement, since it adds real complexity for a residual risk the project's existing trust model (internal-only clients) may already accept.

### Anti-Patterns to Avoid
- **Assuming `chrome`/`google-chrome` docs transfer 1:1 to `chromium-headless-shell`:** most search results and blog posts document the full `chrome --headless=new/old` binary, not the standalone `chrome-headless-shell`/`chromium-headless-shell` binary. The standalone binary IS headless by construction (it has no other mode) — recommend testing live whether it still accepts (and ignores harmlessly) a redundant `--headless` flag, or whether it should be omitted; either way is low-risk, but don't assume examples using `chrome --headless=new --print-to-pdf ...` prove the standalone shell binary's own flag set.
- **Trusting `EmbedStandardFonts`-style "one flag does it all" thinking for print options:** unlike Phase 14's PDF/A case (where one CLI-passable FilterOptions JSON blob covered everything), HTML-03's four options split across two entirely different mechanisms (none are CLI flags) — don't assume there's a hidden fifth flag that "does the rest."
- **Re-litigating D-01/D-02 because of the CSS-injection finding:** the finding is about HTML-03's *mechanism*, not about network-blocking capability — it does not touch D-02's chromedp-fallback trigger condition (a live network-leak test failure), and does not require a new Go dependency.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| HTML→PDF page-size/margin control | A custom PDF post-processing step (e.g. re-paginating an already-rendered PDF) | CSS `@page` injected before render (Pattern 1) | Native browser layout engine already implements CSS Paged Media correctly; a post-process re-page step would need its own PDF-manipulation library — real new dependency, unlike CSS injection |
| HTML structural sniffing | Full HTML5 parser (`x/net/html`) for input detection | Extension/declared-source gate + bounded prefix marker + full-stream NUL/UTF-8 scan (mirrors D-07's own reasoning) | Already explicitly rejected in D-07 for the same reason SniffContainer avoids full ZIP extraction — strictness here is illusory (HTML "parses however it likes") |
| Network request interception | A CDP/chromedp driver *just* for network denial | Layered CLI flags (Pattern 2) + container egress restriction | D-02 already reserves CDP as the fallback ONLY if a live leak test fails — this research found no evidence the CLI-layer approach is insufficient, only one missing flag to close a documented gap |

**Key insight:** Every apparent gap this research found (print options, loopback bypass) has a **CLI/HTML-level** fix, not a "we need chromedp after all" conclusion. Keep escalating to D-02's fallback reserved for an actual live-test network leak, not for these findings.

## Common Pitfalls

### Pitfall A: Assuming D-06's "chromium flags from a server table" literally works for print options
**What goes wrong:** A planner writes a task like "add `--paper-width`/`--margin-top` flags built from the validated enum" — these flags do not exist in `chromium-headless-shell`'s switch table.
**Why it happens:** Phase 14's PDF/A pattern (validated enum → one CLI-passable JSON blob) is the closest prior art, and it's natural to assume the same shape transfers.
**How to avoid:** Use Pattern 1 (CSS injection) instead. This should be a task-writing decision made now, not discovered mid-implementation.
**Warning signs:** Any task acceptance criterion phrased as "chromium invoked with `--page-size=...`" — no such flag exists.

### Pitfall B: `--proxy-server` alone believed sufficient for "no exceptions"
**What goes wrong:** The canary test (D-04) passes because it targets a compose-network hostname, but a loopback-targeting payload silently bypasses the proxy.
**How to avoid:** Add `--proxy-bypass-list=<-loopback>` (Pattern 2) as a required third flag, not optional hardening.
**Warning signs:** A live test that only exercises `<img src="http://canary-hostname/...">` and never `http://127.0.0.1:.../` or `http://localhost:.../` will not catch this — recommend adding at least one loopback-targeting fixture reference even though its *observable* effect (nothing listens there) will be "job completes, no visible signal" rather than a positive-fail assertion; document this as a known test-coverage gap if not fully closed.

### Pitfall C: `--disable-dev-shm-usage` treated as fully sufficient
**What goes wrong:** Some Chromium bug reports indicate `--disable-dev-shm-usage` does not eliminate `/dev/shm` usage in every internal code path [CITED: issues.chromium.org/issues/40135361, MEDIUM confidence — a documented but not universally-reproduced report].
**How to avoid:** D-09 already specifies setting `--shm-size`/compose `shm_size` in ADDITION to the CLI flag — this research corroborates that as the right call, not redundant belt-and-suspenders.

### Pitfall D: `NormalizeFormat` doesn't fold `.htm` → `html`
**What goes wrong:** A client uploads `report.htm`; `source` is computed as `"htm"` (not `"html"`); if `ChromiumConverter.Pairs()` only registers `{"html","pdf"}`, `EngineFor("htm","pdf")` fails lookup even though the content is genuinely HTML.
**Why it happens:** `internal/convert/convert.go:45-55`'s `NormalizeFormat` already folds `jpeg→jpg` and `tif→tiff` as precedent, but has no `htm→html` case today.
**How to avoid:** Add a `case "htm": return "html"` arm to `NormalizeFormat`, mirroring the existing two aliases exactly (one-line, same file, same function) — this is the correct integration point, not a special case inside the new HTML-detection branch.
**Warning signs:** A `.htm`-extension upload 422's with "unsupported conversion: htm -> pdf" instead of succeeding.

## Code Examples

### Exact print-to-pdf invocation shape (verified flags only — Pattern 1/2 combined)
```go
// Source: this research (Chromium switch names VERIFIED against
// source.chromium.org's headless_command_switches.cc; --no-sandbox/
// --disable-dev-shm-usage/--disable-gpu per architecture research,
// MEDIUM confidence, Docker+Chromium community convention)
args := []string{
    "--headless", // verify live: may be redundant/no-op for the standalone shell binary
    "--disable-gpu",
    "--no-sandbox",
    "--disable-dev-shm-usage",
    "--blink-settings=scriptEnabled=false", // D-05; syntax pattern confirmed via
                                             // the sibling --blink-settings=imagesEnabled=false
                                             // example [CITED, MEDIUM confidence — verify live]
    "--proxy-server=127.0.0.1:9",
    "--proxy-bypass-list=<-loopback>", // closes the default loopback-bypass gap (Pattern 2)
    "--host-resolver-rules=MAP * ~NOTFOUND",
    "--print-to-pdf=" + outPath,
    "file://" + renderedPath, // NOT inPath -- renderedPath is inPath + injected <style> (Pattern 1)
}
if err := runCommand(ctx, "chromium-headless-shell", args...); err != nil {
    return fmt.Errorf("chromium: %w", err)
}
return validatePDF(outPath) // reused verbatim from internal/convert/libreoffice.go -- same package,
                             // target is always "pdf" for this engine, zero new validator code
```

### `jobs.engine` CHECK-constraint migration (exact current constraint, `internal/db/migrations/0001_init.sql:47-48`)
```sql
-- Source: this research, reading internal/db/migrations/0001_init.sql directly.
-- Current: CHECK (engine IN ('image', 'document', 'av', 'cad', 'archive', 'probe'))
-- Postgres auto-names an inline, unnamed column CHECK as <table>_<column>_check,
-- so the existing constraint's name is jobs_engine_check -- verify with
-- \d+ jobs (or information_schema.check_constraints) against the live dev DB
-- before writing the DROP, since an inline CHECK's auto-generated name is a
-- documented Postgres convention but not something this research directly
-- queried against a running instance.
ALTER TABLE jobs DROP CONSTRAINT jobs_engine_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_engine_check
    CHECK (engine IN ('image', 'document', 'av', 'cad', 'archive', 'probe', 'html'));
```
File name: `internal/db/migrations/0005_html_engine.sql` (0001-0004 already exist; migrations run in filename-sorted order per `internal/db/db.go`'s embedded-migration runner).

### Queue/routing scaffolding — exact file:line integration points (all mechanical, mirror the document engine 1:1)
| New symbol | Mirrors | File |
|---|---|---|
| `EngineHTML = "html"` | `EngineDocument` | `internal/convert/convert.go:17-20` |
| `TypeHTMLConvert = "html:convert"` | `TypeDocumentConvert` | `internal/queue/queue.go:19-23` |
| `QueueHTML = convert.EngineHTML` | `QueueDocument` | `internal/queue/queue.go:30-34` |
| `NewHTMLConvertTask` | `NewDocumentConvertTask` | `internal/queue/queue.go:79-99` |
| `htmlRetrySchedule` / `HTMLRetryDelay` | `documentRetrySchedule` / `DocumentRetryDelay` | `internal/queue/queue.go:197-224` |
| `HTMLUniqueTTL` | `DocumentUniqueTTL` | `internal/queue/queue.go:315-334` |
| `RetryDelayFunc` new `case TypeHTMLConvert:` | existing `case TypeDocumentConvert:` | `internal/queue/queue.go:233-244` |
| `EnqueueHTMLConvert` | `EnqueueDocumentConvert` | `internal/queue/client.go:98-` |
| `Enqueuer` interface new method | `EnqueueDocumentConvert` | `internal/api/api.go:31-32` |
| `handleCreateJob` engine switch new `case convert.EngineHTML:` | `case convert.EngineDocument:` | `internal/api/handlers.go:322-334` |
| reconciler engine switch new `case convert.EngineHTML:` | `case convert.EngineDocument:` | `internal/reconciler/reconciler.go:133-150` (currently `default:` fails closed and skips — must add an explicit case, not rely on fallthrough) |

### Opts dispatch — the one non-mechanical integration point
`internal/api/handlers.go:257` currently calls `convert.ParseDocOpts` unconditionally inside the `rawOpts != ""` branch. This must become an engine-keyed dispatch (`if engine == convert.EngineDocument { ... ParseDocOpts ... } else if engine == convert.EngineHTML { ... ParseHTMLOpts ... }`), since `HTMLOpts` is a structurally different closed type (`page_size`/`margin_mm`/`landscape`/`print_background`, not `pdf_profile`). `ValidateApplicability`-equivalent logic for HTML should mirror `internal/convert/opts.go:130-138`'s shape (reject opts that don't apply to the given engine/pair) but as its own function scoped to `EngineHTML`, not a shared one — keeps the same "opts only apply to the engine that defines them" invariant Phase 14 established.

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|---------------|--------|
| Full `chrome --headless=old` for one-shot PDF generation | Standalone `chrome-headless-shell`/`chromium-headless-shell` binary | Chrome 132.0.6793.0 [CITED: developer.chrome.com] | Smaller image, same one-shot CLI surface — confirms D-01's choice was correctly timed against the current ecosystem, not a stale pattern |
| Assuming `--print-to-pdf` exposes the full `Page.printToPDF` CDP option set via flags | It exposes only header/footer/tagging/outline/timeout/background-color/screenshot/dump-dom — page geometry is CSS-only | Long-standing limitation, not a recent regression — multiple 2024-2025-dated sources independently confirm the same gap | Directly drives Pattern 1's correction to D-06 |

**Deprecated/outdated:** `--print-to-pdf-no-header` (older flag name) — current flag is `--no-pdf-header-footer` [CITED: source.chromium.org]. Not used by this phase's option set, noted only because it appears in some older blog posts a planner might otherwise copy from.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | Exact pinned `chromium-headless-shell` version differs between two searches (150.x vs 145.x) | Standard Stack | Low — either way it's the current bookworm-channel build; only matters if a specific CVE/behavior is version-gated. Verify via `apt-cache policy` at build time, not a blocker |
| A2 | `--blink-settings=scriptEnabled=false` disables JS in headless-shell's print-to-pdf path specifically (not just full-chrome DevTools sessions) | Code Examples, D-05 | Medium — if it does NOT work as expected in this exact binary, D-05's entire "JS disabled removes a class of attacks" reasoning weakens; MUST be the first live smoke-test item (render a fixture with a `<script>document.write('INJECTED')</script>` and confirm it does NOT appear in the output PDF) |
| A3 | `chromium-headless-shell` accepts a bare `--headless` flag harmlessly even though the standalone binary is headless-only by construction | Code Examples | Low — worst case is a benign unknown-flag warning or error; trivial to drop if live testing shows an error |
| A4 | CSS `@page`/`print-color-adjust` rules are honored the same way in `chromium-headless-shell`'s print-to-pdf renderer as in graphical Chrome's print-preview pipeline | Pattern 1 | Medium — if the headless renderer diverges (as andre.arko.net documented for a *different* CSS feature, `@page` background-image loading), HTML-03's entire mechanism needs a fallback; MUST be an early live smoke-test |
| A5 | `os.MkdirTemp`'s `0700` workDir does not isolate same-UID sibling job directories from a `file://`-capable engine | Pattern 3 | Medium-Low — if wrong (e.g. some other isolation already exists this research didn't find, like per-container ephemeral storage with no persistent overlap), the residual-risk framing is overly cautious; if right and unaddressed, a crafted HTML could read another concurrently-running job's temp files within the same container |

## Open Questions

1. **Does `--headless` need to be passed to the standalone `chromium-headless-shell` binary, or is it implicit/rejected?**
   - What we know: the binary's entire purpose is being the headless-only build; official examples for it were not found in this research (only full-`chrome`-binary examples use `--headless=new/old`).
   - What's unclear: whether passing it anyway causes an error or is silently accepted.
   - Recommendation: Verify-Live Smoke Checklist item 1 (below) — trivial to resolve empirically before writing any task acceptance criteria around it.

2. **What exact stderr text does `chromium-headless-shell` emit for a terminal, unrecoverable failure (analogous to `terminalVipsSignatures`/`terminalLibreOfficeSignatures`)?**
   - What we know: the project's established convention is to populate this list from live-tested output (the `vips` list is commented "Verified live-tested"), not guessed.
   - What's unclear: this research cannot produce real stderr text without running the binary.
   - Recommendation: budget an early execution task (mirrors how `terminalVipsSignatures` was originally populated) — render a few deliberately-bad fixtures (though D-07's content-gate should reject most garbage before it ever reaches the worker; the remaining terminal cases are likely engine-internal failures like a render timeout being classified terminal via the `isDocumentTerminal`-style timeout-is-terminal rule, or a `file://` path chromium refuses to open) and capture actual output before finalizing the terminal-signature list.

3. **Does the current Debian bookworm `chromium-headless-shell` build honor `--blink-settings=scriptEnabled=false` inside its non-CDP print-to-pdf code path specifically?**
   - What we know: `--blink-settings=<key>=<value>` is a real, documented general Chromium switch mechanism (`imagesEnabled=false` is a confirmed sibling example).
   - What's unclear: whether the print-to-pdf command-handler path (a comparatively newer, narrower code path than full interactive browsing) applies Blink settings identically.
   - Recommendation: Verify-Live Smoke Checklist item 2.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| `chromium-headless-shell` (apt, inside `Dockerfile.chromium-worker`) | HTML-01/02/03 | Not yet installed in any current image — new Dockerfile required | ~145-150.x bookworm channel, verify live | None — this is the engine itself, no fallback; if unavailable in the target Debian channel at build time, that blocks the phase entirely (low risk — package is confirmed present in the official bookworm archive as of this research) |
| `tini` (apt) | Container PID-1 reaping (D-09) | Already used in `Dockerfile.document-worker` | Same pinned Debian version | N/A — already proven |
| Existing `runCommand` (`internal/convert/exec.go`) | Hardened process exec | Yes, in-repo, unchanged | N/A | N/A |
| Docker/docker-compose | Building/running the new service | Assumed available (existing project baseline) | N/A | N/A |

**Missing dependencies with no fallback:** `chromium-headless-shell` itself must be added to a new Dockerfile — this is expected, tracked work, not a surprise gap.

## Verify-Live Smoke Checklist

*The planner should turn this into an early (Wave 0/Wave 1) execution task, before any print-option or network-block task is marked complete. Each item is a single, fast, scriptable check.*

1. **Binary invocation shape** — `docker run --rm <image> chromium-headless-shell --version` and `chromium-headless-shell --headless --print-to-pdf=/tmp/out.pdf file:///tmp/test.html` (a trivial `<html><body>hi</body></html>` fixture) both succeed; confirm whether `--headless` is required, optional-but-harmless, or rejected (Open Question 1).
2. **JS actually disabled** — render a fixture containing `<script>document.write('<h1>INJECTED</h1>')</script>` with `--blink-settings=scriptEnabled=false`; confirm "INJECTED" does NOT appear in the output PDF text (Assumption A2 / Open Question 3).
3. **CSS `@page`/`print-color-adjust` honored** — render a fixture with a colored `<div style="background:red">` under each `print_background` true/false CSS variant (Pattern 1) and visually/structurally confirm the PDF differs correctly (Assumption A4).
4. **Network block — canary hit count** — reuse/generalize `startWebhookReceiver` (`internal/e2e/e2e_test.go:293-332`) into a canary receiver; render a fixture with `<img src="http://{host}:{port}/canary-img">` + `<script>fetch('.../canary-fetch')</script>` (inert since JS is off, kept as defense-in-depth per D-05) + references to `169.254.169.254` and internal compose hostnames (`redis`/`postgres`); assert zero hits AND job completes successfully within the engine timeout (not hung). Add `chromium-worker` to `docker-compose.e2e.yml`'s `extra_hosts: host.docker.internal:host-gateway` block, mirroring `worker`/`document-worker`.
5. **Loopback-bypass flag confirmed necessary** — with `--proxy-bypass-list=<-loopback>` OMITTED, confirm (via chromium's own verbose/net-log output, or simply that the job still completes without hanging either way given nothing listens on loopback) that the flag is inert-but-correct to include; this is a low-value empirical confirmation but cheap, given the flag's necessity is a documented Chrome behavior, not conjecture (Pattern 2).
6. **`file://` residual-read risk** — render `<img src="file:///etc/hostname">`; confirm whether the file's content appears in the output PDF (Pattern 3 / Assumption A5). Log the outcome as an explicit PROJECT.md residual-risk decision either way.
7. **tini necessity, one-shot invocation** — with tini as PID 1, deliberately trigger a `runCommand` context-timeout kill mid-render (a fixture with a large/slow-to-lay-out but still-static page, or a short `ENGINE_TIMEOUT` for the test) and confirm no defunct/zombie processes remain in the container after the SIGKILL. Repeat WITHOUT tini as a control to confirm the difference is real, not incidental. Optionally repeat both with `--no-zygote` added, to test whether it meaningfully simplifies the process tree for this specific one-shot invocation shape (D-09's own "verify live, don't assume" instruction, with one concrete new lever this research surfaced).
8. **`.htm` extension handling** — upload a `report.htm` fixture end-to-end; confirm `NormalizeFormat` needs the `htm→html` alias (Pitfall D) before this is treated as working.

## Sources

### Primary (HIGH confidence)
- Direct reads of current `main` branch: `internal/convert/{exec,libreoffice,opts,convert,sniff,docsniff}.go`, `internal/api/handlers.go`, `internal/worker/worker.go`, `internal/queue/{queue,client}.go`, `internal/reconciler/reconciler.go`, `internal/db/migrations/0001_init.sql`, `cmd/document-worker/main.go`, `Dockerfile.document-worker`, `docker-compose.yml`, `docker-compose.e2e.yml`, `internal/e2e/e2e_test.go`
- [source.chromium.org — `components/headless/command_handler/headless_command_switches.cc`](https://source.chromium.org/chromium/chromium/src/+/main:components/headless/command_handler/headless_command_switches.cc) — VERIFIED, authoritative, exhaustive list of the exact 9 print-to-pdf-related CLI switches
- [Debian — chromium-headless-shell in bookworm](https://packages.debian.org/bookworm/chromium-headless-shell) — package/version/dependency confirmation
- [Debian — chromium-headless-shell amd64 filelist](https://packages.debian.org/bookworm/amd64/chromium-headless-shell/filelist) — exact binary paths

### Secondary (MEDIUM confidence)
- [Chrome for Developers — Chrome Headless mode](https://developer.chrome.com/docs/chromium/headless) — standalone `chrome-headless-shell` binary split-out at Chrome 132, `--no-pdf-header-footer`/`--timeout`/`--virtual-time-budget` examples
- [andre.arko.net — Chrome "Print to PDF" and headless --print-to-pdf aren't the same!](https://andre.arko.net/2025/05/25/chrome-headless-print-to-pdf/) — headless vs. graphical print-pipeline divergence, `@page` CSS resource-loading gotcha
- [zzz.buzz — Proxy localhost and loopback addresses in Chrome](https://zzz.buzz/2019/12/12/proxy-localhost-and-loopback-in-chrome/) — the loopback-bypass-by-default behavior and `--proxy-bypass-list=<-loopback>` remediation
- [Whitesmith — A note about Chrome and proxying requests to localhost](https://www.whitesmith.co/blog/a-note-about-chrome-and-proxying-requests-to-localhost/) — corroborates the loopback-bypass finding independently
- [issues.chromium.org/issues/40135361 — --disable-dev-shm-usage not working in docker](https://issues.chromium.org/issues/40135361) — corroborates D-09's belt-and-suspenders `shm_size` recommendation
- [homedutech.com — How to change paper size in headless Chrome --print-to-pdf](https://www.homedutech.com/program-example/css--how-can-i-change-paper-size-in-headless-chrome-printtopdf.html) — independently states "you'll need to include these settings in your HTML content" (corroborates Pattern 1's core finding)

### Tertiary (LOW confidence)
- General WebSearch aggregation on `puppeteer`/Docker Chromium zombie-process issues (`github.com/puppeteer/puppeteer/issues/12854`, `monzim.com/projects/docpipe`) — corroborates tini-necessity general guidance, but none specifically test the exact one-shot, non-CDP, `--print-to-pdf`-only invocation shape this phase uses
- `--no-zygote` appearing in aggregated Puppeteer-in-Docker arg lists — a real, named flag, but not independently verified against this specific invocation shape; flagged as an experiment, not a recommendation

## Metadata

**Confidence breakdown:**
- Print-option CLI-flag absence (Pattern 1): HIGH — verified directly against Chromium's own switch-definition source, the most authoritative possible source short of running the binary
- Loopback-bypass gap (Pattern 2): MEDIUM-HIGH — multiple independent, specific, named sources agree on the exact mechanism and remediation
- tini necessity for this exact invocation shape: LOW-MEDIUM — strong general-purpose corroboration, zero source specifically tests one-shot `--print-to-pdf` (not a long-lived CDP session) with `--no-zygote` as a variable
- Package/binary naming: HIGH — official Debian archive, direct filelist read
- `file://` residual risk: MEDIUM — general Chromium file-access behavior is documented, but this research did not find a source specifically testing it against a `--no-sandbox`, `USER nobody`, one-shot print-to-pdf invocation

**Research date:** 2026-07-11
**Valid until:** ~30 days for the architectural/mechanism findings (Patterns 1-3 are unlikely to change); ~7 days is more appropriate for the exact pinned `chromium-headless-shell` version if the phase's build is delayed, since Debian's bookworm-security channel rolls Chromium versions frequently (security-driven releases are common for this specific package)
