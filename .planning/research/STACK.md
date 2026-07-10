# Stack Research

**Domain:** Async file-conversion service — document-class expansion (cross-format, PDF/A, OLE-CFB pre-flight reject) + new HTML→PDF (chromium) engine class + webhook-worker decoupling
**Researched:** 2026-07-10
**Confidence:** MEDIUM-HIGH (all four areas verified against official/Debian sources; one narrow gap flagged below)

This is an **additive** stack note for OctoConv v1.3 "Document Class v2". It does not revisit the fixed core stack (Go 1.26, chi, asynq/Redis, PostgreSQL 18, MinIO — Notion spec, out of scope) or the LibreOffice-engine addition from v1.2 (superseded; see `.planning/milestones/v1.2-ROADMAP.md` for that history — this file replaces the previous milestone's STACK.md content). Every recommendation below follows the project's established philosophy: **zero new Go dependencies**, CLI shell-out through the existing hardened `runCommand` (`internal/convert/exec.go`), and one dedicated container per engine class (the pattern already proven by `Dockerfile.document-worker` in v1.2).

## Recommended Stack

### Core Technologies (new, for v1.3)

| Technology | Version | Purpose | Why Recommended |
|------------|---------|---------|-----------------|
| `chromium-headless-shell` (Debian package) | 150.0.7871.100-1~deb12u1 (bookworm) | Third engine class: HTML→PDF | Since Chrome 132 the classic "old Headless" implementation — the one that supports `--print-to-pdf` as a plain CLI flag with no DevTools driving — was split out of the main `chromium` binary into this standalone package, `chrome-headless-shell` upstream / `chromium-headless-shell` in Debian. It is exactly the "CLI shell-out, no driver library" shape the project requires, and it is security-patched by the Debian security team on the same cadence as the rest of the `debian:bookworm-slim` base images already in use. |
| LibreOffice `writer_pdf_Export` / `calc_pdf_Export` / `impress_pdf_Export` JSON filter options | Already present — LibreOffice 7.4.7 (bookworm's `libreoffice-*-nogui` packages, already pinned in `Dockerfile.document-worker`) | `opts`-driven PDF/A export (DOC-V2-03) | No new package. LibreOffice 7.4+ accepts a JSON `FilterData` blob appended to the filter name in `--convert-to 'pdf:writer_pdf_Export:{...}'`; bookworm's 7.4.7 is inside this window. `SelectPdfVersion` selects the PDF/A profile. Zero new dependency — same `soffice` CLI already shelled out to for docx/xlsx/pptx→PDF. |
| LibreOffice cross-format targets (`odt`/`ods`/`odp`/`docx`/`xlsx`/`pptx` as **output**, not just PDF) | Already present — no new package | Cross-format conversion within document class (DOC-V2-01) | The `libreoffice-writer-nogui` / `-calc-nogui` / `-impress-nogui` packages already installed for docx/xlsx/pptx→PDF also ship the ODF export filters (`writer8`, `calc8`, `impress8`) and the reverse OOXML import filters. `soffice --convert-to odt <input>.docx` works from the target extension alone — LibreOffice resolves the correct filter automatically, exactly the way libvips already infers codecs from `outPath`'s extension in `internal/convert/libvips.go`. |
| stdlib `bytes` magic-byte check for the OLE-CFB signature | stdlib only | Pre-flight OLE-CFB detection (DOC-V2-02) | The Compound File Binary signature is a fixed 8-byte constant (`D0 CF 11 E0 A1 B1 1A E1`) at offset 0. Detecting it is a one-line `bytes.Equal` check — identical shape to the existing `matchPNG`/`matchTIFF` functions in `internal/convert/sniff.go`. No parsing library needed at all; this is a reject-fast pre-check, not a CFB reader. |
| Standalone `cmd/webhook-worker` binary + `Dockerfile.webhook-worker` | N/A (new `cmd/`, reuses existing packages) | Decouple webhook delivery from the image worker (SEED-002) | Reuses `internal/webhook`, `internal/queue`, `internal/jobs` verbatim — zero new Go code beyond a `main.go` that is structurally a trimmed copy of `cmd/worker/main.go` (drop `convert.Default`, `ENGINE_TIMEOUT`, and the storage download/upload wiring; keep `jobs.Repo`, `webhook.Repo`, `webhook.Deliverer`, and an `asynq.Server` bound to `QueueWebhook` only). Exactly the same "extract a lean, single-purpose worker binary/container" move already made for `cmd/document-worker` in v1.2 Phase 10. |

### Supporting Libraries

None. No new Go module is required for any of the four v1.3 stack changes — this milestone is CLI-integration and stdlib-only, consistent with the "zero new Go deps" preference in the research question.

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| — | — | — | (intentionally empty — see "What NOT to Use" for the libraries that were considered and rejected) |

### Development Tools (container-level, not Go)

| Tool | Purpose | Notes |
|------|---------|-------|
| `tini` | PID 1 reaper in the new HTML-worker container | Already a proven pattern from `Dockerfile.document-worker` — Chromium spawns a zygote/child process tree analogous to LibreOffice's `oosplash`→`soffice.bin`; without an init/reaper, a group-SIGKILLed Chromium subtree risks the same PID-1-reparent zombie failure mode already documented and fixed for LibreOffice (09-02 finding). Reuse the identical `ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/html-worker"]` shape. |
| Debian `fonts-liberation2` (+ optionally `fonts-noto-color-emoji`/`fonts-noto-cjk` later) | Font coverage for HTML→PDF fidelity | Reuse the same font package already installed for `Dockerfile.document-worker`. Chromium renders with whatever fonts exist in the container — start with `fonts-liberation2` for baseline Latin coverage (keeps visual/metric consistency with the LibreOffice engine's font set), extend only if a real client HTML sample needs more. Do not pre-install the full `fonts-noto` mega-metapackage — unnecessary image bloat for an internal service. |

## Installation

```dockerfile
# New: Dockerfile.html-worker (third engine class, mirrors Dockerfile.document-worker)
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/html-worker ./cmd/html-worker

FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates \
      tini \
      chromium-headless-shell \
      fonts-liberation2 \
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/html-worker /usr/local/bin/html-worker
USER nobody
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/html-worker"]
```

```dockerfile
# New: Dockerfile.webhook-worker — lean, no external engine CLI at all
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/webhook-worker ./cmd/webhook-worker

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/webhook-worker /usr/local/bin/webhook-worker
USER nobody
ENTRYPOINT ["/usr/local/bin/webhook-worker"]
```

```bash
# No go.mod changes for this milestone.
go build ./...   # unchanged
```

### Exact CLI invocation syntax

**HTML→PDF (via `runCommand`, mirroring `LibvipsConverter.Convert`/`LibreOfficeConverter.Convert`):**
```bash
chromium-headless-shell \
  --headless --disable-gpu --no-sandbox --disable-dev-shm-usage \
  --no-pdf-header-footer \
  --timeout=<ms bounded by HTML_ENGINE_TIMEOUT> \
  --print-to-pdf=<outPath> \
  file://<inPath>
```
- `--no-sandbox` is required because the container already runs as unprivileged `nobody` (Chromium's setuid sandbox needs either root or `CAP_SYS_ADMIN`/user namespaces it won't have here); the residual risk is accepted the same way the project already accepts running LibreOffice/libvips unsandboxed inside a resource-limited, unprivileged, single-purpose container.
- `--disable-dev-shm-usage` avoids Chromium crashing against Docker's default 64 MB `/dev/shm` — also set `shm_size` for the `html-worker` service in `docker-compose.yml` as defense-in-depth regardless.
- `--print-to-pdf=<path>` is the direct, driver-free equivalent of libvips's `vips copy` / LibreOffice's `--convert-to` — no DevTools Protocol, no Puppeteer/chromedp/rod dependency.
- **SSRF surface note (stack-relevant, not just a pitfall):** unlike libvips/LibreOffice, Chromium will actively fetch network resources referenced *inside* the HTML (`<img src>`, `<link>`, `<iframe>`) while rendering. This is a materially different attack surface from the existing engines, and from the already-mitigated webhook-callback SSRF guard — it needs its own network-level containment (e.g., no route from the `html-worker` container's Docker network to internal service CIDRs, or a `--host-resolver-rules` denylist) decided at phase-planning level. Flagging here because it changes the container's `docker-compose.yml` network topology, not just its Dockerfile.

**Cross-format document conversion (extends `LibreOfficeConverter`, no new filter syntax beyond what's already used for →pdf):**
```bash
soffice --headless --invisible --nocrashreport --nodefault --nologo \
  --nofirststartwizard --norestore \
  -env:UserInstallation=file://<profileDir> \
  --convert-to odt \
  --outdir <workDir> <inPath>.docx
```
(swap `odt`→`ods`/`odp` and the input extension accordingly for xlsx↔ods, pptx↔odp; LibreOffice resolves the writer8/calc8/impress8 export filter from the target extension automatically, same as the existing `pdf:<filter>` calls already resolve via `filterFor`.)

**PDF/A export (`opts`-driven, extends the existing `pdf:<filter>` call with a JSON `FilterData` suffix):**
```bash
soffice --headless ... --convert-to \
  'pdf:writer_pdf_Export:{"SelectPdfVersion":{"type":"long","value":"2"}}' \
  --outdir <workDir> <inPath>.docx
```
`SelectPdfVersion` values (from official `help.libreoffice.org`, verified current): `0`=PDF 1.7 (default), `1`=PDF/A-1b, `2`=PDF/A-2b, `3`=PDF/A-3b, `14`-`17`=plain PDF 1.4-1.7. Substitute `writer_pdf_Export`→`calc_pdf_Export`/`impress_pdf_Export` for xlsx/pptx sources, same as the existing `filterFor` switch in `internal/convert/libreoffice.go`.

**OLE-CFB pre-flight signature (stdlib only, same shape as `internal/convert/sniff.go`):**
```go
var oleCFBMagic = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

func matchOLECFB(b []byte) bool {
    return len(b) >= len(oleCFBMagic) && bytes.Equal(b[:len(oleCFBMagic)], oleCFBMagic)
}
```
Notable, non-obvious finding: this single check catches **two** distinct rejection cases at once. Legacy binary `.doc`/`.xls`/`.ppt` files are natively OLE-CFB. But so are *password-protected modern* `.docx`/`.xlsx`/`.pptx` files — per MS-OFFCRYPTO, an encrypted OOXML package is itself wrapped in an OLE Compound File containing `EncryptionInfo`/`EncryptedPackage` streams. So "sniff CFB magic bytes on a file whose extension/declared format is docx/xlsx/pptx/doc/xls/ppt" cleanly covers both DOC-V2-02 scenarios ("password-protected/legacy binary doc/xls/ppt") with zero extra logic.

## Alternatives Considered

| Recommended | Alternative | When to Use Alternative |
|-------------|-------------|-------------------------|
| `chromium-headless-shell` CLI shell-out via `runCommand` | `chromedp` (Go, drives Chrome via CDP over the wire) | If the project ever needs fine-grained per-page JS evaluation, cookie injection, or a long-lived browser process reused across many renders for throughput — none of which DOC-V2-04 requires. Adds a Go dependency and a persistent-process supervision model the current one-shot-per-job `runCommand` pattern doesn't have. |
| `chromium-headless-shell` CLI shell-out via `runCommand` | `rod` (Go, higher-level CDP wrapper) | Same tradeoff as chromedp — richer browser-automation API at the cost of a new dependency and a different lifecycle model (persistent browser + tabs) than every other engine in this codebase. |
| `chromium-headless-shell` CLI shell-out via `runCommand` | `wkhtmltopdf` | Older, simpler, smaller footprint, but its WebKit fork is unmaintained upstream (project archived) with materially weaker modern CSS/flexbox/grid support — the exact gap the milestone rationale cites for rejecting LibreOffice as the HTML→PDF engine ("LibreOffice слабо рендерит современный CSS/JS"). Do not use for new work. |
| Debian `chromium-headless-shell` package | Google's redistributable Chrome-for-Testing binaries (as fetched by Puppeteer's `@puppeteer/browsers`) | Only if a specific Chrome version newer than what bookworm ships is required (e.g., a CSS feature landed after 150.x). Otherwise the Debian package keeps the same "OS-managed, security-patched, apt-installed" supply chain as every other engine dependency in this project (libvips-tools, LibreOffice) — don't introduce a second, differently-managed binary distribution channel. |
| stdlib `bytes.Equal` CFB magic check | A CFB-parsing library (e.g., a Go port of `libolecf`) | Only if a future feature needs to actually read *inside* the compound file (extract streams, detect the specific encryption algorithm, etc.) rather than just reject it. DOC-V2-02 only needs "is this CFB" — parsing the FAT/directory structure is out of scope and would be the first non-stdlib dependency in the validation layer, breaking the "zero-dependency parsers" precedent set in Phase 7 (decompression-bomb dimension parsers). |
| Separate `cmd/webhook-worker` binary/container | A second `asynq.Server` + queue registered inside every engine worker binary (image, document, html) | Never — this is exactly the topology SEED-002 exists to eliminate: today, deploying only the document/html workers silently drops webhook delivery, because only `cmd/worker` (image) registers `TypeWebhookDeliver`. Duplicating the webhook consumer into N worker binaries instead of extracting it once would multiply the coupling bug rather than fix it. |

## What NOT to Use

| Avoid | Why | Use Instead |
|-------|-----|-------------|
| `chromedp`, `rod`, `playwright-go`, or any other Go CDP-driver library | Introduces the project's first browser-automation Go dependency, a persistent-process lifecycle model that doesn't match the one-shot `runCommand`-per-job pattern every other engine uses, and a much larger transitive dependency surface (WebSocket/CDP protocol libs) for a feature that only needs "render this HTML to a PDF file" | `chromium-headless-shell --print-to-pdf`, shelled out via the existing `runCommand` hardening |
| `wkhtmltopdf` | Archived/unmaintained upstream project; WebKit-fork rendering engine years behind on modern CSS (flexbox/grid/custom properties) — the milestone explicitly calls out this exact gap as the reason LibreOffice was rejected for HTML→PDF | `chromium-headless-shell` |
| Full `chromium` package (not `chromium-headless-shell`) | ~273 MB installed vs. ~51-59 MB for `chromium-headless-shell`; pulls in GTK3, X11 client libs, PulseAudio, and other GUI-stack dependencies the worker container never touches | `chromium-headless-shell` |
| A Go CFB-parsing library | Adds a dependency to solve a problem (reject legacy/encrypted binary formats) that a single 8-byte comparison already solves completely | stdlib `bytes.Equal` magic-byte check, same shape as existing `internal/convert/sniff.go` |
| `golang.org/x/crypto/...` or any MS-OFFCRYPTO decryption library, to *decrypt and inspect* password-protected files instead of rejecting them | Out of scope for DOC-V2-02 (a clean 422 reject, not decryption support) and would reopen exactly the "new process-exec/parsing surface for untrusted input" risk Phase 7's decision explicitly avoided for image dimension parsing | Reject at the CFB-signature pre-flight check; do not attempt to decrypt |
| Registering `TypeWebhookDeliver` in the new `html-worker` and `document-worker` binaries "just in case" | Reintroduces the same silent-coupling bug SEED-002 targets, just spread across three binaries instead of one | Exactly one `cmd/webhook-worker` consumes `QueueWebhook`; image/document/html workers only ever consume their own engine-class queue |

## Stack Patterns by Variant

**If a client needs a PDF/A profile stricter than PDF/A-2b (e.g., PDF/A-3 for embedded XML invoices, or PDF/A-4 for long-term archival):**
- PDF/A-3b is available today via `SelectPdfVersion` value `3` on the same LibreOffice 7.4.7 already in the container — no stack change needed, just accept the value through the `opts` API surface.
- PDF/A-4 is not exposed by this `SelectPdfVersion` enum in 7.4.7; would require a LibreOffice version bump (out of scope for v1.3) — flag as a known ceiling, not a blocker.

**If HTML→PDF needs to render pages requiring authentication or session cookies:**
- Out of scope for this milestone (HTML is uploaded/submitted directly, not fetched from an authenticated origin) — do not add cookie-jar/auth plumbing to the `chromium-headless-shell` invocation now; that would be exactly the kind of feature creep that would eventually justify moving to `chromedp`, but isn't needed for DOC-V2-04 as scoped.

## Version Compatibility

| Package A | Compatible With | Notes |
|-----------|------------------|-------|
| `chromium-headless-shell` 150.0.7871.100-1~deb12u1 | `debian:bookworm-slim` base image (already used by `Dockerfile.worker`/`Dockerfile.document-worker`) | Same base-image family as every other worker container — no new base-image variant to maintain. |
| LibreOffice 7.4.7 (bookworm, already pinned) | JSON `FilterData` CLI syntax (`pdf:writer_pdf_Export:{...}`) | The JSON-parameter CLI feature was contributed for the 7.4 release cycle (per LibreOffice core developer Miklós Vajna's writeup); bookworm's 7.4.7 sits inside this window. Official `help.libreoffice.org` (version-agnostic "latest" docs) states the feature works from "7.3 and higher" — the two sources agree it is present, they diverge only on the exact minor version it landed in. Treat as verified-present, not verified-exact-introduction-version. |
| LibreOffice 7.4.7 `calc_pdf_Export`/`impress_pdf_Export` filters | `SelectPdfVersion` property | **MEDIUM confidence, not independently smoke-tested.** All three `*_pdf_Export` filters implement the same generic PDF-export UNO service, and the official docs page is titled generically ("PDF Export Command Line Parameters", not "Writer PDF Export…"), so `SelectPdfVersion` is expected to behave identically for xlsx/pptx sources. **Recommend a one-time smoke test per source format (docx/xlsx/pptx → PDF/A-2b) during phase execution** before treating this as load-bearing, per the project's existing discipline of verifying LibreOffice's documented "exit 0 but empty/corrupt output" failure mode (`validatePDF` in `libreoffice.go`). |
| `chromium-headless-shell --print-to-pdf` | Old-style headless CLI flags (`--headless`, `--disable-gpu`, `--no-pdf-header-footer`, `--timeout`, `--virtual-time-budget`) | These flags belong to the "old Headless" implementation, which is precisely what `chromium-headless-shell` *is* (it was split out of the unified `chromium` binary specifically to keep this flag surface alive after Chrome 132 changed the main binary's default `--headless` to the new, GUI-code-sharing implementation that does not support the identical flag set). Do not test against the plain `chromium` package's `--headless` and assume identical behavior post-Chrome 132. |

## Sources

- `packages.debian.org/bookworm/chromium-headless-shell` — version 150.0.7871.100-1~deb12u1, package purpose, size, dependencies (HIGH confidence, official Debian archive)
- `packages.debian.org/bookworm/chromium` — full-package size (~273 MB) comparison (HIGH confidence, official Debian archive)
- `developer.chrome.com/docs/chromium/headless` — old vs. new headless mode split at Chrome 132; `chrome-headless-shell` supports `--print-to-pdf` directly with no DevTools driving required (HIGH confidence, official Chrome for Developers docs)
- `help.libreoffice.org/latest/en-US/text/shared/guide/pdf_params.html` — `SelectPdfVersion` value table (0/1/2/3/14-17), JSON `FilterData` syntax, "7.3 and higher" version claim (HIGH confidence, official LibreOffice docs)
- `vmiklos.hu/blog/pdf-convert-to.html` — LibreOffice core developer's account of the CLI JSON filter-parameter work landing for the 7.4 release cycle (MEDIUM confidence, single named-author source, but the author is a LibreOffice core contributor and corroborates the official docs' "7.3+" window)
- Existing codebase: `internal/convert/libreoffice.go`, `internal/convert/sniff.go`, `internal/convert/docsniff.go`, `internal/convert/exec.go`, `internal/queue/queue.go`, `cmd/worker/main.go`, `Dockerfile.document-worker` — verified current implementation patterns this research extends (HIGH confidence, first-party source)
- MS-OFFCRYPTO / `msoffcrypto-tool` documentation and Didier Stevens' "Encrypted OOXML Documents" writeup — confirms password-protected modern OOXML (.docx/.xlsx/.pptx) files are themselves stored as an OLE Compound File wrapping an `EncryptedPackage` stream, so the single CFB-magic-byte pre-flight check catches **both** legacy binary formats **and** password-protected modern formats with one signature (MEDIUM-HIGH confidence, corroborated across two independent sources)
- WebSearch (general ecosystem/SSRF context, cross-checked across multiple results): headless-browser SSRF write-ups (Black Hills InfoSec, Intigriti, Neodyme) — informs the network-containment note above (MEDIUM confidence, WebSearch-only, flagged as needing phase-level architectural follow-up rather than a stack-library answer)

---
*Stack research for: OctoConv v1.3 "Document Class v2" (chromium HTML→PDF engine, LibreOffice cross-format + PDF/A, OLE-CFB pre-flight, webhook-worker decoupling)*
*Researched: 2026-07-10*
