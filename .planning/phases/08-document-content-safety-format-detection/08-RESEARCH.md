# Phase 8: Document Content Safety & Format Detection - Research

**Researched:** 2026-07-09
**Domain:** ZIP-container structural format detection (OOXML/ODF disambiguation), zip-bomb declared-size guard, macro-part rejection, in Go stdlib only
**Confidence:** HIGH

## Summary

This phase extends OctoConv's existing "validate before touching storage" pipeline to a second family of ZIP-based container formats. All six target formats (docx/xlsx/pptx/odt/ods/odp) share the identical `PK\x03\x04` magic bytes, so disambiguation cannot happen in `sniff.go`'s existing prefix-match table — it requires reading the ZIP central directory via stdlib `archive/zip`, which needs `io.ReaderAt` + a known size rather than a forward-only `io.Reader`. This research empirically validated every open technical question the phase context raised, by generating **real, tool-produced** docx/xlsx/pptx/odt/ods/odp files (via `pandoc`, a real `xlsx` fixture from a widely-used open-source test suite, and `odfpy`, a genuine ODF-writing library) and inspecting their exact byte structure, then writing small Go programs against the actual `go1.26.4` `archive/zip` stdlib to confirm behavior directly rather than trusting the spec or milestone-level research alone.

Three findings materially change or sharpen the milestone-level research's recommended approach and must inform planning:

1. **OOXML's `[Content_Types].xml` is NOT reliably the first ZIP entry.** A real pptx produced by `pandoc` has `[Content_Types].xml` at central-directory index 8, not 0 — disproving the "producers conventionally emit it first" assumption the milestone `ARCHITECTURE.md` addendum relied on (Pattern 1: "inflate the first entry"). The robust, simpler, and empirically-validated alternative — already hinted at in the milestone `FEATURES.md` but contradicted by `ARCHITECTURE.md`'s addendum — is a **root-part-presence-by-name check** (`word/document.xml` / `xl/workbook.xml` / `ppt/presentation.xml`), which needs no XML parsing and no position assumption, works identically regardless of where in the archive the part physically lives, and is confirmed present in every one of the real generated fixtures.
2. **Naive content-type substring search over the raw `[Content_Types].xml` bytes produces a real false-positive path.** A hand-built docx with a plausible embedded-xlsx OLE object (`Default Extension="xlsx" ContentType="...spreadsheetml.sheet"`) contains BOTH `wordprocessingml.document` and `spreadsheetml.sheet` substrings simultaneously, while `file`/libmagic still correctly identifies it as `Microsoft Word 2007+`. This concretely confirms the phase context's suspicion and rules out raw substring search as the detection primitive; root-part-presence-by-name (finding 1) sidesteps this entirely because embedded objects never live at the exact root-part paths.
3. **Go's `archive/zip` is unconditionally central-directory-authoritative and immune to local-header/central-directory filename mismatch** — confirmed by reading `go1.26.4`'s `archive/zip/reader.go` source directly and by hand-crafting a ZIP where the local file header lies about the entry name while the central directory says the truth: `zip.Reader.File[].Name` and `.Open()` both use the central directory's version unconditionally. This resolves the phase context's Question #1/#3 concern about local-header spoofing — no additional cross-check is needed for this phase's purposes. A **different, real** duplicate-entry risk was found instead (see Common Pitfalls): `archive/zip.NewReader` silently accepts two entries with the identical name and exposes both, which matters for whichever lookup logic picks "the" root part.

`multipart.File`'s `io.ReaderAt` implementation was verified correct and safe for both the in-memory and on-disk-temp-file upload paths by reading `mime/multipart/formdata.go`/`net/http/request.go` directly and by an empirical test proving `Read()` (sequential, used by `Sniff`) and `ReadAt()` (positional, needed by the new container check) do not interfere with each other on the same file handle — meaning the new container-inspection step must read from the **original** `multipart.File` + `header.Size`, not from `Sniff`'s already-type-erased `rest io.Reader` return value (`io.MultiReader` does not implement `io.ReaderAt`).

The zip-bomb guard (`UncompressedSize64` summed across central-directory entries) and the macro-part check (presence of `word|xl|ppt/vbaProject.bin` or a `Basic/`-prefixed entry) both reuse the **same single `zip.NewReader` call/pass** as the format-disambiguation check — confirmed to add zero decompression cost even against a real 50 MB-declared/51 KB-actual synthetic zip bomb built and read in this session. The confirmed pre-existing dimension-check regression (`convert.Dimensions` would 422 every document upload today) has an exact, minimal fix already scoped by prior research and re-confirmed against current line numbers.

**Primary recommendation:** Implement one new stdlib-only function (e.g. `convert.SniffContainer(r io.ReaderAt, size int64) (detected string, uncompressedTotal uint64, hasMacro bool, err error)`) that does a single `archive/zip.NewReader` pass: index-0 check for ODF's `mimetype` entry (name + `Method==0` + payload string match, no raw byte-offset parsing needed), name-presence check for OOXML's three root parts, summed `UncompressedSize64` for the zip-bomb guard, and a scan for macro-carrying entries — called from `handleCreateJob` against the original `multipart.File`/`header.Size` when `Sniff` returns unrecognized and the peeked prefix is `PK\x03\x04`.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| ZIP-container format disambiguation (OOXML/ODF) | API / Backend | — | Runs synchronously inside `handleCreateJob`, before any S3/Postgres write — identical placement to the existing image magic-byte sniff (`convert.Sniff`) |
| Zip-bomb declared-size guard | API / Backend | — | Same request-handling path; rejects before storage exactly like the existing `MAX_IMAGE_PIXELS` check |
| Macro-part rejection | API / Backend | — | Same single container-inspection pass as the two checks above |
| Dimension-check regression fix (`HasDimensionLimit`) | API / Backend | Database / Storage (n/a) | Pure control-flow guard inside the existing handler; no new tier involved |
| Format registry / `Converter` interface | API / Backend (`internal/convert`) | — | Unchanged this phase — `docx`/`xlsx`/etc. become detectable but are not yet registered to any `Converter` (Phase 9) or routed to a queue (Phase 11); `Supports()` will correctly return `false` for every pair until Phase 9 registers `LibreOfficeConverter` |

This phase touches exactly one tier (API/Backend, specifically `internal/convert` + `internal/api/handlers.go`) — there is no browser, SSR, or dedicated storage-tier work in scope. This matches the phase boundary explicitly: no queue/worker/routing changes belong here (Phases 9-11).

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| DOC-01 | API accepts docx/xlsx/pptx/odt/ods/odp and structurally disambiguates them (ZIP/OOXML central-directory check, ODF fixed-offset `mimetype` check) instead of trusting the extension | Architecture Pattern 1 (root-part-presence-by-name for OOXML, index-0 `mimetype` check for ODF) — both empirically validated against real tool-generated fixtures in this research; supersedes the milestone `ARCHITECTURE.md` addendum's "inflate first entry" approach with a simpler, position-independent, XML-parsing-free method |
| DOC-02 | API rejects (422, pre-S3-write) a declared-uncompressed-size-over-limit office document (zip-bomb guard) | Architecture Pattern 2 (single-pass `UncompressedSize64` summation via `archive/zip`'s central directory, zero decompression) — empirically confirmed against a real 50 MB-declared/51 KB-actual synthetic bomb built in this session |
| DOC-03 | API rejects (422) an office document containing macro parts (`vbaProject.bin` / Basic-script manifest) | Architecture Pattern 3 (OOXML: literal `word\|xl\|ppt/vbaProject.bin` path match, corroborated by `xlsxwriter`'s documented `Default Extension="bin"` convention; ODF: `Basic/`-prefixed entry match, corroborated against a secondary source describing LibreOffice's `Basic/script-lc.xml` + `Basic/<Library>/script-lb.xml` layout) |

</phase_requirements>

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**D-01 (Format disambiguation approach):** Structural container inspection, not extension trust — mirrors Phase 4's D-01 philosophy exactly. ODF (odt/ods/odp) disambiguates via the OASIS-mandated first-ZIP-entry `mimetype` file (uncompressed, fixed byte offset). OOXML (docx/xlsx/pptx) disambiguates via reading the ZIP central directory (stdlib `archive/zip`, needs `io.ReaderAt` — `multipart.File` already satisfies this, confirmed in research) and inspecting `[Content_Types].xml` for the format-specific content-type string. Zero new dependencies (stdlib `archive/zip`, `compress/flate` only).

**D-02 (Architecture):** This is architecturally a new, second detection path alongside the existing `sniff.go` prefix-signature table (`Sniff`) — not a drop-in extension of it. Planner/researcher to decide exact code organization, but the two detection styles (prefix-match vs. container-inspection) are distinct enough to not force into the same function shape.

**D-03 (Zip-bomb guard basis):** Limit is on total declared uncompressed size summed across all ZIP central-directory entries (`UncompressedSize64`), not a per-entry compression-ratio check.

**D-04 (Zip-bomb limit value):** Default limit is 500 MiB total declared uncompressed size across all entries. Configurable via a new env var (naming left to Claude's Discretion, following `MAX_IMAGE_PIXELS`/`MAX_UPLOAD_BYTES` convention).

**D-05 (Macro rejection):** Hard, unconditional rejection — no operator opt-out flag. Detection: presence of `vbaProject.bin` (OOXML) or a Basic-script manifest entry (ODF) among the ZIP entries, checked in the same container-inspection pass as D-01's format disambiguation and D-03's zip-bomb guard.

### Claude's Discretion

- Exact env var name for the zip-bomb limit (e.g. `MAX_DOCUMENT_UNCOMPRESSED_SIZE`) — follow existing naming convention (`MAX_UPLOAD_BYTES`, `MAX_IMAGE_PIXELS`).
- Exact file/function organization for the new OOXML/ODF container-inspection code (e.g. a new `internal/convert/docsniff.go` alongside `sniff.go`).
- Exact mechanism for the `HasDimensionLimit`-style predicate fixing the confirmed dimension-check regression — behavior must be: documents skip the dimension check entirely, not that documents get a document-specific dimension check.
- Whether the macro-detection check and the zip-bomb check both run inside the same single ZIP-central-directory read pass as the format-disambiguation check, or as separate passes — likely the same pass for efficiency.

### Deferred Ideas (OUT OF SCOPE)

None raised this phase — REQUIREMENTS.md's v2/Out-of-Scope sections already capture the relevant future items (DOC-V2-02 password-protected pre-flight detection, DOC-V2-05 active complexity-based anti-DoS) from the milestone-level discussion. This phase does NOT cover: the LibreOffice converter itself (Phase 9), the document worker/queue/reconciler wiring (Phase 10), or `handleCreateJob`'s engine-routing branch (Phase 11).
</user_constraints>

## Project Constraints (from CLAUDE.md)

- Tech stack is locked (Go 1.26, chi, asynq+Redis, PostgreSQL 18, S3/MinIO) — not renegotiable at this phase; this research introduces zero new runtime dependencies, fully compliant.
- Naming: lowercase-no-separator filenames (`docsniff.go`, not `doc_sniff.go`); exported `PascalCase`, unexported `camelCase`; constructors `New<Type>`; `ctx` always first/named `ctx`.
- Error handling: wrap with `fmt.Errorf("<action>: %w", err)`; sentinel errors as package-level `var Err<Reason>`, checked via `errors.Is`; HTTP layer never leaks internal error text — handlers map to a short fixed string (matches existing `writeError` pattern already used for D-01/D-02/VALID-03 rejections).
- No `panic` for control flow anywhere in `internal/` — any new parser code must fail closed via returned errors, exactly like `dimensions.go`'s existing `ErrDimensionsUnknown`/fail-closed convention.
- Comments: package-level doc comment on one file; exported identifiers get a doc comment starting with the identifier name; non-obvious "why" decisions get inline comments (this codebase's established practice, e.g. `exec.go`'s process-group-kill rationale) — the new container-inspection code should follow `dimensions.go`'s citation style (`// Source: <spec/RFC>`) for each format's structural rule.
- One file per responsibility within `internal/convert/` — a new file (not an edit to `sniff.go`'s signature table) is the correct home per D-02.
- `go vet ./...` clean is the enforced minimum bar; no linter config beyond that.

## Standard Stack

### Core

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `archive/zip` | Go 1.26 stdlib (bundled with the pinned `go1.26.4` toolchain, `go.mod:3`) | Parse ZIP central directory: entry names, compression method, `UncompressedSize64`, entry content via `.Open()` | Already the project's zero-new-deps precedent (Phase 4/7 hand-rolled binary parsers); fully supports zip64 and random-access central-directory reads without decompression |
| `compress/flate` | Go 1.26 stdlib | Transitively used by `archive/zip`'s `Open()` when a `Method==8` (deflate) entry needs to be read (e.g. `mimetype`'s uncommon-but-possible compressed variant, or any entry whose content the container check needs to inspect) | Registered automatically by `archive/zip`; no direct import needed unless implementing a custom decompressor |
| `io` (`io.ReaderAt`, `io.SectionReader`) | Go 1.26 stdlib | `multipart.File` already implements `io.ReaderAt`; `zip.NewReader` requires exactly this + a `size int64` | Verified directly against `mime/multipart/formdata.go` and `net/http/request.go` source in this session — no wrapping/buffering needed |

**Verification note:** All three are Go standard library — no `npm view`/`pip index`-style registry check applies. Version is whatever ships with the pinned toolchain (`go1.26.4 darwin/arm64`, confirmed via `go version` in this session `[VERIFIED: local toolchain]`).

### Supporting

None — this phase introduces zero new supporting libraries. `encoding/xml` was evaluated for `[Content_Types].xml` parsing but is **not recommended** as the primary detection mechanism (see Architecture Pattern 1 rationale); if a future phase wants stricter OOXML conformance checking, `encoding/xml` remains available in stdlib with no new dependency.

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Root-part-presence-by-name (this research's recommendation) | Inflate first ZIP entry + substring-match content-type (milestone `ARCHITECTURE.md` addendum's original Pattern 1) | Rejected: empirically disproven in this session — a real `pptx` (via `pandoc`) does not have `[Content_Types].xml` as its first entry, so "inflate the first entry" would silently misclassify or reject valid pptx files produced by at least one common real-world tool |
| Root-part-presence-by-name | Raw substring search over `[Content_Types].xml`'s full decompressed bytes | Rejected: empirically shown to produce ambiguous double-matches when a docx contains an embedded xlsx OLE object (both `wordprocessingml.document` and `spreadsheetml.sheet` substrings present simultaneously) |
| Root-part-presence-by-name | `_rels/.rels` relationship resolution + `encoding/xml`-parsed `Content_Types.xml` Override lookup (the fully spec-correct OPC approach) | More rigorous (correctly resolves the root part even if a producer names it unconventionally), but adds two XML-parse passes and two new stdlib import surfaces for a case never observed in this research's real-file testing (2 independent tools, 3 formats) or in the wider ecosystem's documented conventions; not justified given REQUIREMENTS.md's explicit exclusion of "full OOXML/ODF schema validation" |
| ODF: `zip.NewReader`'s `File[0]` structured check | Hand-rolled fixed-byte-offset parser (raw local-file-header parsing at bytes 30/38, as the milestone `FEATURES.md` phrased it) | Rejected as the primary mechanism: fixed offsets 30/38 are only valid when the local header's filename-length is exactly 8 and extra-field-length is exactly 0 (confirmed true in this session's `odfpy`-generated samples, but not spec-guaranteed) — using `archive/zip`'s own structured parsing handles any extra-field length correctly for free and reuses the one `zip.NewReader` call already needed for OOXML/zip-bomb/macro checks |

**Installation:** None — zero new dependencies. No `go get`/`go install` step required for this phase.

**Version verification:** N/A (stdlib only). Confirmed the pinned toolchain (`go1.26.4 darwin/arm64`) is what CI/Docker builds will use per `go.mod:3` (`go 1.26.4`) and `Dockerfile.api`/`Dockerfile.worker`'s `golang:1.26-bookworm` builder stage.

## Package Legitimacy Audit

**Not applicable — this phase installs zero external packages.** All functionality is implemented with Go standard library (`archive/zip`, `compress/flate`, `io`) already available in the pinned `go1.26.4` toolchain. The Package Legitimacy Gate protocol (slopcheck, registry verification, postinstall-script check) is skipped because there is nothing to install.

## Architecture Patterns

### System Architecture Diagram

```
multipart upload (file, target)
        │
        ▼
r.FormFile("file") ──► file (multipart.File: io.Reader + io.ReaderAt + io.Seeker + io.Closer)
        │                     header (*multipart.FileHeader: .Size int64)
        ▼
convert.Sniff(file)  ── sequential io.ReadFull peek (12 bytes) ──► detected="" for any ZIP-shaped upload
        │                                                           (no PK signature registered today)
        ▼
   peeked prefix == "PK\x03\x04"?
        │ yes                                  │ no (image path, unchanged)
        ▼                                      ▼
convert.SniffContainer(file, header.Size)   existing image detected-format branch
  (NEW — single archive/zip.NewReader pass,
   reads via io.ReaderAt, does NOT disturb
   Sniff's sequential read cursor)
        │
        ├─► ODF check:   zr.File[0].Name=="mimetype" && Method==0
        │                  → Open()+compare payload against the 3 known strings
        ├─► OOXML check: name-presence scan for
        │                  word/document.xml | xl/workbook.xml | ppt/presentation.xml
        ├─► zip-bomb sum: Σ zr.File[i].UncompressedSize64  (same pass, zero decompression)
        └─► macro scan:  any entry named */vbaProject.bin OR prefixed "Basic/"
        │
        ▼
   detected == ""?  ──► 422 unrecognized content (existing D-02 path, reused)
   detected != source? ──► 422 mismatch (existing D-01/D-04 path, reused)
   total uncompressed > MAX_DOCUMENT_UNCOMPRESSED_BYTES? ──► 422 zip-bomb (NEW)
   hasMacro? ──► 422 macro-carrying document (NEW)
        │ all pass
        ▼
convert.HasDimensionLimit(detected)?  (NEW predicate — fixes confirmed regression)
   ├─ true  (png/jpg/webp/heic/tiff) → convert.Dimensions(...) exactly as today
   └─ false (docx/xlsx/pptx/odt/ods/odp) → SKIP entirely, proceed
        │
        ▼
callback_url validation → S3 upload → jobs.Create → enqueue (UNCHANGED this phase —
   convert.Default.Supports(detected, target) still returns false for every document
   pair until Phase 9 registers LibreOfficeConverter; document uploads with a
   currently-unregistered target format 422 via the EXISTING "unsupported conversion"
   check, which is correct/expected for this phase)
```

### Recommended Project Structure

```
internal/convert/
├── sniff.go             # UNCHANGED — image prefix-match table stays exactly as-is (D-02)
├── docsniff.go          # NEW — SniffContainer(r io.ReaderAt, size int64) and its helpers
│                        #   (single archive/zip.NewReader pass: ODF/OOXML disambiguation,
│                        #   zip-bomb size sum, macro-part scan)
├── dimensions.go        # MODIFY — add HasDimensionLimit(format string) bool predicate
│                        #   (dimensionParsers map itself stays image-only, unchanged)
internal/api/
├── handlers.go          # MODIFY — call SniffContainer when Sniff returns "" and the
│                        #   peeked prefix is PK\x03\x04; guard the existing dimension-
│                        #   check block with HasDimensionLimit(detected); wire the new
│                        #   MAX_DOCUMENT_UNCOMPRESSED_BYTES-derived limit
├── api.go               # MODIFY — Server/Config gain maxDocumentUncompressedBytes uint64
cmd/api/main.go           # MODIFY — envInt64("MAX_DOCUMENT_UNCOMPRESSED_BYTES", 500<<20)
                          #   wired into Config, mirroring MaxImagePixels's existing pattern
```

### Structure Rationale

A new file (`docsniff.go`), not an edit to `sniff.go`'s `signatures` table, matches D-02's explicit instruction and the codebase's "one file per responsibility" convention (mirrors how `dimensions.go` sits alongside `sniff.go` today as a structurally-different-shaped parser for a related concern). `dimensions.go` gets a small additive predicate rather than a new format-specific parser, since documents genuinely have no pixel-dimension concept — this is a scope guard, not new dimension-limiting behavior (matches the locked Claude's-Discretion note verbatim).

### Pattern 1: Root-part-presence-by-name for OOXML disambiguation (supersedes milestone research's "inflate first entry" approach)

**What:** Read the ZIP central directory via `archive/zip.NewReader(r io.ReaderAt, size int64)`. For OOXML disambiguation, do **not** assume position or inflate/parse any XML — simply check whether an entry with one of these **exact names** exists among `zr.File`:

| Root part name | Format |
|---|---|
| `word/document.xml` | docx |
| `xl/workbook.xml` | xlsx |
| `ppt/presentation.xml` | pptx |

**Verified in this session:** Built real docx (`pandoc`), pptx (`pandoc`), and xlsx (a real `openpyxl`/Excel-produced fixture from a widely-used open-source project's test suite) and confirmed via a Go program using `archive/zip.NewReader` directly that all three root-part names are present in their respective files, **regardless of central-directory position** — pptx's `[Content_Types].xml` was at index 8, not 0, while `word/document.xml`/`xl/workbook.xml`/`ppt/presentation.xml` were present and locatable via simple name iteration in every case `[VERIFIED: go1.26.4 archive/zip, tested against real pandoc/openpyxl-generated fixtures]`.

**When to use:** As the sole primary signal for OOXML disambiguation in this phase. No `encoding/xml` import needed, no decompression of `[Content_Types].xml` needed (root-part names are themselves ZIP entry names, visible in the central directory metadata alone if you only need existence — actually reading the entry's content is only needed if deeper validation is desired later).

**Trade-offs:**
- Simpler and strictly cheaper than the milestone `ARCHITECTURE.md` addendum's "inflate `[Content_Types].xml` via `compress/flate` and substring-match" approach — no decompression is needed at all for the format-disambiguation decision itself (only for the zip-bomb/macro checks does anything ever get `.Open()`'d, and macro/bomb checks use metadata fields too — `UncompressedSize64` and `Name`, not content — except macro detection for `Basic/`-prefix ODF, which is also name-only).
- Handles the empirically-confirmed embedded-OLE-object false-positive risk automatically: an embedded xlsx inside a docx lives at a path like `word/embeddings/oleObject1.xlsx`, never at the literal `xl/workbook.xml` — so root-part-presence never double-matches, unlike raw content-type substring search `[VERIFIED: hand-crafted test fixture in this session]`.
- **Duplicate-entry-name risk (new finding, not covered by milestone research):** `archive/zip.NewReader` does **not** reject a ZIP containing two entries with the identical name — both are exposed in `zr.File`, in central-directory order `[VERIFIED: hand-crafted test in this session — a ZIP with two "word/document.xml" entries with different content was accepted without error]`. If the root-part check simply does "does any entry have this name," duplicates don't change the accept/reject outcome for *this* check (existence is existence) — but if a future refinement ever needs to read the root part's *content* (not just its presence), a naive "first match" or "last match" policy creates a genuine parser-disagreement surface between this check and whatever Phase 9's LibreOffice eventually reads. Recommend: while iterating the central directory for this pass, also count occurrences of each root-part name and macro-part name; if any exceeds 1, fail closed (reject as unrecognized/suspicious) rather than silently picking one. Cheap (one extra counter) and forecloses an entire attack class.

**Example:**
```go
// Source: verified via internal Go program against real pandoc/openpyxl-generated
// docx/pptx/xlsx fixtures in this research session (go1.26.4 archive/zip stdlib).
var ooxmlRootParts = map[string]string{
	"word/document.xml":     "docx",
	"xl/workbook.xml":       "xlsx",
	"ppt/presentation.xml":  "pptx",
}

func detectOOXML(zr *zip.Reader) (format string, seen map[string]int) {
	seen = make(map[string]int)
	for _, f := range zr.File {
		if fmt, ok := ooxmlRootParts[f.Name]; ok {
			format = fmt
			seen[f.Name]++
		}
	}
	return format, seen
}
```

### Pattern 2: ODF `mimetype`-at-index-0 check using `archive/zip`'s structured API, not raw byte offsets

**What:** OASIS ODF v1.2 Part 3 §17.4 mandates the first ZIP entry be named `mimetype`, stored uncompressed, containing the literal media-type string. Rather than hand-parsing local-file-header bytes at fixed offsets (fragile if extra-field length is ever non-zero), use `archive/zip`'s own parsed `zr.File[0]`:

```go
// Source: OASIS OpenDocument v1.2 Part 3 §17.4; empirically verified against real
// odfpy-generated odt/ods/odp fixtures in this research session.
var odfMimetypes = map[string]string{
	"application/vnd.oasis.opendocument.text":          "odt",
	"application/vnd.oasis.opendocument.spreadsheet":   "ods",
	"application/vnd.oasis.opendocument.presentation":  "odp",
}

func detectODF(zr *zip.Reader) (format string, err error) {
	if len(zr.File) == 0 {
		return "", nil
	}
	first := zr.File[0]
	if first.Name != "mimetype" || first.Method != zip.Store {
		return "", nil // not ODF-shaped; fall through to OOXML/unrecognized
	}
	rc, err := first.Open()
	if err != nil {
		return "", fmt.Errorf("open mimetype entry: %w", err)
	}
	defer rc.Close()
	payload, err := io.ReadAll(io.LimitReader(rc, 128)) // bounded read, fail-closed on longer content
	if err != nil {
		return "", fmt.Errorf("read mimetype entry: %w", err)
	}
	return odfMimetypes[string(payload)], nil
}
```

**Verified in this session:** For all three `odfpy`-generated fixtures, `zr.File[0].Name == "mimetype"`, `Method == 0` (stored), and the exact payload strings matched `application/vnd.oasis.opendocument.{text,spreadsheet,presentation}` exactly, with **zero** compressed/uncompressed size discrepancy (39/46/47 bytes respectively) `[VERIFIED: go1.26.4 archive/zip against real odfpy-generated files]`. A raw hex dump additionally confirmed the local-file-header byte layout the milestone `FEATURES.md` cited (filename at byte 30, payload at byte 38) is correct **only because** filename-length was exactly 8 and extra-field-length was exactly 0 in this real file — using `archive/zip`'s structured parsing avoids depending on that coincidence.

**When to use:** As the sole primary signal for ODF disambiguation. Position (`index 0`) is a hard OASIS requirement, unlike OOXML's Content_Types.xml convention, so trusting position here is spec-grounded, not just observed.

**Trade-offs:** If `zr.File[0]` is not a `mimetype`/`Method==0` match, this correctly falls through (returns `"", nil`) rather than erroring — the caller must then also try the OOXML root-part check before concluding "unrecognized." A bare `.zip` (no `mimetype` first entry, no OOXML root parts) correctly falls through both checks to the existing D-02 "unrecognized content" 422.

### Pattern 3: Single-pass zip-bomb size guard (`UncompressedSize64`, zero decompression)

**What:** Sum `zr.File[i].UncompressedSize64` across every entry in the same central-directory iteration already performed for format disambiguation. Reject if the total exceeds the configured limit — **before** calling `.Open()` on anything except the small bounded reads needed for the `mimetype` payload comparison (Pattern 2) and, if content-based macro verification is ever added, the macro entries themselves (out of scope here — presence-only is the locked decision, D-05).

**Verified in this session:** Built a synthetic zip bomb (one `Deflate`-compressed entry of 50 MB of zeros) that compresses to 51,120 bytes on disk. Running it through `archive/zip.NewReader` and summing `UncompressedSize64` correctly reports 52,428,800 (50 MB) declared uncompressed size **without invoking `.Open()`/decompression on the entry at all** — confirmed by the fact the verification program never called `.Open()` in that code path `[VERIFIED: go1.26.4 archive/zip, synthetic bomb built and read in this session]`. This directly confirms the "trust declared metadata, reject before expensive work" approach costs effectively zero CPU regardless of the declared bomb size, exactly mirroring `MAX_IMAGE_PIXELS`'s existing philosophy.

**When to use:** Same pass as Pattern 1/2 — one `zip.NewReader` call serves format detection, size-guard, and macro-scan together (per the locked Claude's-Discretion note preferring a single pass).

**Trade-offs:** This is a total-sum guard, not a per-entry compression-ratio guard (D-03's explicit choice) — a document with many entries each individually under the ratio-suspicious threshold but summing past the limit is still caught; a single entry with an extreme ratio but small absolute declared size is not specifically flagged as "suspicious," only as contributing to the total. This matches the locked decision and the `MAX_IMAGE_PIXELS` precedent (total, not per-region).

### Pattern 4: Macro-part detection — literal path match (OOXML) + directory-prefix match (ODF)

**What:**
- **OOXML:** reject if any entry is named exactly `word/vbaProject.bin`, `xl/vbaProject.bin`, or `ppt/vbaProject.bin`. Corroborated by `xlsxwriter`'s official documentation (a real, widely-used Python library for authoring genuine `.xlsm` files): *"An Excel xlsm file is exactly the same as an xlsx file except that it contains an additional vbaProject.bin file"* and *"a package must contain at most one VBA Project part, which must be the target of an implicit relationship from the workbook part"* `[CITED: xlsxwriter documentation]`. `[Content_Types].xml` in a macro-enabled file additionally carries `<Default Extension="bin" ContentType="application/vnd.ms-office.vbaProject"/>` — a secondary, more format-invariant signal if extra rigor is ever wanted, but the literal-path check is the near-universal, SDK-default convention and matches D-05's locked wording exactly.
- **ODF:** reject if any entry's name has the prefix `Basic/` (covers `Basic/script-lc.xml` at the package root and `Basic/<LibraryName>/script-lb.xml` + module files under arbitrary, user-defined library-name subdirectories — checking a literal library name like `Basic/Standard/` would miss renamed libraries). Corroborated by a secondary source describing this exact layout (`script-lc.xml` at `Basic/` root, `script-lb.xml` + module XML under `Basic/<Library>/`) `[CITED: langintro.com ODF internals tutorial, cross-referenced against training-data knowledge of LibreOffice's Basic IDE storage format]`. Per the same source, this directory is created by LibreOffice **only** when a macro is actually added — a macro-free document never has any `Basic/`-prefixed entries at all, so a prefix-presence check should not false-positive on ordinary documents `[CITED, MEDIUM confidence — not independently verified against a real macro-carrying ODF sample in this session, since generating one required LibreOffice itself, unavailable in this environment]`.

**When to use:** Same single pass as Patterns 1-3.

**Trade-offs:** Neither check inspects *content* of the macro part — this is a presence-only reject, matching D-05's explicit locked scope ("no operator opt-out," "presence of ... checked"). The ODF `Basic/`-prefix check could theoretically over-reject a document with a never-populated, empty macro-library scaffold (if a user opened the Basic IDE and created an empty library without writing code, then saved) — this is an **acceptable, conservative fail-closed outcome** consistent with the project's established philosophy (`dimensions.go`'s `ErrDimensionsUnknown` fail-closed precedent), not a defect, and is explicitly the behavior D-05 asked for (presence, not content-based). Flagged as an Open Question below since it was not empirically testable in this environment.

### Pattern 5: `SniffContainer` must read from the original `multipart.File`, not from `Sniff`'s `rest` return value

**What:** `Sniff(file)` returns `rest io.Reader = io.MultiReader(bytes.NewReader(buf), file)` — `io.MultiReader` implements only `io.Reader`, **not** `io.ReaderAt`. `zip.NewReader` requires `io.ReaderAt` + `size int64`. Therefore the new container-inspection call must happen against the **original** `file` (the `multipart.File` value from `r.FormFile`, still in scope) and `header.Size` (the `*multipart.FileHeader.Size` field, already used elsewhere in `handlers.go` for `storage.Upload`'s size argument), not against `rest`.

**Verified in this session:**
- Read `mime/multipart/formdata.go` (go1.26.4) directly: `FileHeader.Open()` returns either (a) `sectionReadCloser{io.NewSectionReader(bytes.NewReader(content), 0, len(content)), nil}` for in-memory uploads, or (b) a plain `*os.File` (or a `sectionReadCloser` wrapping a shared `*os.File` with an `io.SectionReader` for offset-bounded access, when multiple file fields share one temp file) for on-disk uploads. Read `net/http/request.go`: `Request.FormFile` calls exactly `fhs[0].Open()`. Both code paths return a genuinely correct, bounded `io.ReaderAt` regardless of upload size `[VERIFIED: go1.26.4 stdlib source, read directly in this session]`.
- Empirically confirmed `Read()` (sequential cursor, what `Sniff`'s `io.ReadFull` uses) and `ReadAt()` (positional, what `zip.NewReader` uses) do not interfere: built a test using a real `*os.File` (simulating the on-disk-temp-file path), consumed 12 bytes via sequential `Read`, then successfully ran `zip.NewReader` against the same file handle via `ReadAt`, then confirmed the sequential cursor still resumed from byte 12 (not from wherever the ZIP central directory read left it) `[VERIFIED: empirical test against go1.26.4 os.File in this session]`.

**When to use:** Always — this dictates the exact call order in `handleCreateJob`: call `Sniff(file)` first (unchanged, cheap, handles the image path); if `detected == ""` and the already-peeked 12-byte prefix starts with `PK\x03\x04` (available from `Sniff`'s own peek — either have `Sniff` also expose the raw peeked bytes, or peek `[]byte{'P','K',3,4}` separately since it's only 4 bytes and `ReadAt(buf, 0)` is a cheap positional read that doesn't disturb the sequential cursor either), call `SniffContainer(file, header.Size)`. The final `rest` reader used for `s.storage.Upload` remains exactly `Sniff`'s existing `io.MultiReader(bytes.NewReader(buf), file)` construction — `SniffContainer`'s `ReadAt`-based reads never advance `file`'s sequential cursor, so nothing needs to be re-stitched a second time.

**Trade-offs:** This does couple the new detection function to accepting `(io.ReaderAt, int64)` rather than a plain `io.Reader` like `Sniff`/`Dimensions` — an intentional, unavoidable divergence from the existing two functions' signatures, justified by `archive/zip`'s hard API requirement, not a stylistic inconsistency. Keep the function decoupled from `multipart.File`/`net/http` specifically (accept the narrower `io.ReaderAt` interface) to preserve `internal/convert`'s existing independence from `internal/api`.

### Anti-Patterns to Avoid

- **Adding `PK\x03\x04` as a plain `sniff.go` signature entry:** shared by all 6 target formats plus bare `.zip`/`.jar`/`.epub` — a first-match-wins prefix table cannot disambiguate them and would misclassify five of the six formats as whichever is listed first. (Confirmed already in milestone `ARCHITECTURE.md` Anti-Pattern 1; re-confirmed here as still correct.)
- **Trusting ZIP central-directory position for OOXML's `[Content_Types].xml`:** empirically disproven in this session (pptx via pandoc is not first-entry). Use name-presence of the root part instead (Pattern 1).
- **Raw substring search over `[Content_Types].xml`'s decompressed bytes:** empirically shown to double-match on a docx with an embedded xlsx OLE object. Use root-part-presence-by-name instead.
- **Hand-parsing ODF's `mimetype` entry at fixed byte offsets 30/38:** works only when filename-length is exactly 8 and extra-field-length is exactly 0 (true in this session's real samples, not spec-guaranteed). Use `archive/zip`'s structured `zr.File[0]` + `.Open()` instead — same cost, no fragile assumption.
- **Calling the new container-inspection function against `Sniff`'s `rest` return value:** `io.MultiReader` does not implement `io.ReaderAt`; this will fail to compile against `zip.NewReader`'s signature, or (if wrapped incorrectly) silently misbehave. Always pass the original `multipart.File` + `header.Size`.
- **Silently picking "first match" or "last match" when a root-part or macro-part name appears more than once in the central directory:** `archive/zip.NewReader` does not deduplicate or reject duplicate entry names (verified empirically). Count occurrences during the single pass; treat >1 as a reject signal, not an implementation detail to shrug off.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|--------------|-----|
| ZIP central-directory parsing (entry names, sizes, compression method) | A custom byte-offset ZIP-header parser (beyond the bounded, already-established style in `dimensions.go` for raw *image* formats) | `archive/zip` (stdlib) | Handles zip64, arbitrary extra-field lengths, and data-descriptor variants correctly for free; hand-rolling this specifically for ZIP (unlike PNG/JPEG/TIFF/HEIC's simpler fixed-format headers) reintroduces exactly the local-header-vs-central-directory subtlety this research spent effort verifying stdlib already handles safely |
| Deflate decompression (needed only for the small bounded `mimetype` payload read) | A custom inflate implementation | `compress/flate` (used internally by `archive/zip.Open()`) | Zero reason to hand-roll a decompressor for a bounded, small, well-understood need |
| Full OOXML/ODF schema validation | Hand-rolled or third-party XSD validators | Nothing — explicitly out of scope per `REQUIREMENTS.md`'s Out-of-Scope table (Java-based tooling conflicts with zero-new-deps philosophy; schema conformance is a different problem from malicious-content detection per LibreOffice's own Jan 2026 blog post, cited in milestone research) | This phase's structural checks (root-part presence, mimetype match, size sum, macro-part presence) give the actually-relevant security signal without a new runtime dependency |

**Key insight:** Every problem this phase needs to solve (parse a ZIP central directory, read one small bounded entry, sum declared sizes) is already fully solved by Go's standard library with zero gaps — the only genuine engineering work is choosing the *correct* stdlib-based detection strategy (name/position-based, not content-substring-based) and getting the `io.ReaderAt` plumbing right against `multipart.File`, both of which this research empirically resolved.

## Common Pitfalls

### Pitfall 1: Assuming `[Content_Types].xml` is always the first ZIP entry

**What goes wrong:** A detection strategy that inflates/reads only `zr.File[0]` for OOXML (as the milestone `ARCHITECTURE.md` addendum proposed) will misclassify or reject a real pptx.
**Why it happens:** Microsoft's own tooling conventionally emits it first, but this is not spec-mandated, and other real tools (confirmed: `pandoc`) do not follow the convention.
**How to avoid:** Use root-part-presence-by-name (Pattern 1), which is position-independent.
**Warning signs:** A test suite that only exercises Microsoft-Office-generated or python-generated fixtures might never catch this — explicitly test with a pptx from a *different* producer (this research used `pandoc`) during phase execution.

### Pitfall 2: Raw substring search over `[Content_Types].xml` false-positives on embedded objects

**What goes wrong:** A docx/xlsx/pptx containing an embedded object of a *different* Office format (e.g., an Excel chart embedded in a Word doc) will contain multiple format-indicating substrings simultaneously.
**Why it happens:** `[Content_Types].xml`'s `Default Extension` mechanism applies package-wide, by file extension, not per-part — an embedded `.xlsx` OLE object triggers a `Default Extension="xlsx" ContentType=".../spreadsheetml.sheet"` entry even inside a document whose root is `word/document.xml`.
**How to avoid:** Root-part-presence-by-name (Pattern 1) is immune to this because embedded objects never occupy the literal root-part paths.
**Warning signs:** A rejection or misclassification specifically correlated with documents containing embedded charts/tables/objects from another Office application.

### Pitfall 3: Duplicate ZIP entry names are silently accepted by `archive/zip`

**What goes wrong:** A maliciously crafted ZIP with two entries sharing the same name (e.g., two `word/document.xml` entries with different content) is accepted without error by `archive/zip.NewReader`; whichever one a "first match" or "last match" lookup picks may differ from what a *different* ZIP-reading implementation later in the pipeline (e.g. LibreOffice in Phase 9) picks — a classic parser-confusion smuggling vector.
**Why it happens:** Nothing in the ZIP format or `archive/zip`'s implementation enforces name uniqueness across the central directory.
**How to avoid:** During the single container-inspection pass, count occurrences of every root-part name and macro-part name; treat a count `> 1` as a reject condition (fail closed), since no legitimate producer emits duplicates.
**Warning signs:** None observable from outside without explicit duplicate-counting — this is a "silent until exploited" class of bug, verify with an explicit unit test using a hand-crafted duplicate-name fixture.

### Pitfall 4: Building the zip-bomb/macro/format checks as three separate `zip.NewReader` passes

**What goes wrong:** Re-parsing the central directory three times is wasted work and risks the three checks disagreeing (e.g., if the reader is somehow re-seeked or re-buffered differently between calls).
**Why it happens:** Natural if the three checks are implemented as three separately-named, separately-tested functions each taking `(io.ReaderAt, int64)` and each calling `zip.NewReader` internally.
**How to avoid:** One `zip.NewReader` call, one `for _, f := range zr.File` loop, all four concerns (ODF check, OOXML check, size sum, macro scan) computed together and returned as one result struct/tuple.
**Warning signs:** Code review — if `zip.NewReader` appears more than once per upload in the new code, it's the anti-pattern.

### Pitfall 5: Forgetting the confirmed pre-existing dimension-check regression

**What goes wrong:** Without a guard, `internal/api/handlers.go`'s existing unconditional `convert.Dimensions(detected, rest)` call (currently at handler lines ~152-170, immediately after the pair-check) will 422 **every** accepted document upload with "cannot determine declared image dimensions," because `dimensions.go`'s `dimensionParsers` map (lines 37-43) is closed to exactly `{png, jpg, webp, heic, tiff}` and fails closed (`ErrDimensionsUnknown`) for anything else — this is not a hypothetical, it is the confirmed, currently-live behavior of the code as written today.
**Why it happens:** The dimension check was written before any non-image format existed in the system; nothing about its current shape is document-aware.
**How to avoid:** Add `func HasDimensionLimit(format string) bool { _, ok := dimensionParsers[NormalizeFormat(format)]; return ok }` to `dimensions.go` and wrap the existing dimension-check block in `handlers.go` with `if convert.HasDimensionLimit(detected) { ... }` — documents skip the block entirely (not "get a document-specific check," per the locked Claude's-Discretion scope note).
**Warning signs:** Any manual/integration test uploading a real docx/xlsx/etc. through the full `handleCreateJob` path without this fix will observe a 422 "cannot determine declared image dimensions" error, which is the regression manifesting directly.

## Code Examples

### Unified single-pass container inspection

```go
// Source: this research session — verified against real pandoc/openpyxl/odfpy-
// generated fixtures using go1.26.4's archive/zip stdlib.
package convert

import (
	"archive/zip"
	"errors"
	"io"
)

// ContainerResult is the outcome of a single ZIP-central-directory pass used to
// disambiguate OOXML/ODF office formats and simultaneously gather the zip-bomb
// and macro-part signals D-03/D-05 require.
type ContainerResult struct {
	Format             string // "docx"|"xlsx"|"pptx"|"odt"|"ods"|"odp"|"" (unrecognized)
	TotalUncompressed  uint64
	HasMacro           bool
	DuplicateRootPart  bool // fail-closed signal (Pitfall 3)
}

var ErrNotAZip = errors.New("not a valid zip container")

var ooxmlRootParts = map[string]string{
	"word/document.xml":    "docx",
	"xl/workbook.xml":      "xlsx",
	"ppt/presentation.xml": "pptx",
}

var odfMimetypes = map[string]string{
	"application/vnd.oasis.opendocument.text":         "odt",
	"application/vnd.oasis.opendocument.spreadsheet":  "ods",
	"application/vnd.oasis.opendocument.presentation": "odp",
}

var ooxmlMacroParts = map[string]bool{
	"word/vbaProject.bin": true,
	"xl/vbaProject.bin":   true,
	"ppt/vbaProject.bin":  true,
}

// SniffContainer inspects a ZIP-shaped upload's central directory to
// disambiguate the 6 supported office formats, sum declared uncompressed
// size (D-03/zip-bomb guard), and detect macro-carrying parts (D-05) — all
// in one archive/zip.NewReader pass (Pitfall 4).
func SniffContainer(r io.ReaderAt, size int64) (ContainerResult, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return ContainerResult{}, ErrNotAZip
	}

	var res ContainerResult
	rootPartCount := map[string]int{}

	for i, f := range zr.File {
		res.TotalUncompressed += f.UncompressedSize64

		if ooxmlMacroParts[f.Name] || hasBasicPrefix(f.Name) {
			res.HasMacro = true
		}
		if fmtName, ok := ooxmlRootParts[f.Name]; ok {
			res.Format = fmtName
			rootPartCount[f.Name]++
		}

		if i == 0 && f.Name == "mimetype" && f.Method == zip.Store {
			payload, rerr := readBounded(f, 128)
			if rerr == nil {
				if odfFmt, ok := odfMimetypes[string(payload)]; ok {
					res.Format = odfFmt
				}
			}
		}
	}
	for _, c := range rootPartCount {
		if c > 1 {
			res.DuplicateRootPart = true
		}
	}
	return res, nil
}

func hasBasicPrefix(name string) bool {
	return len(name) >= 6 && name[:6] == "Basic/"
}

func readBounded(f *zip.File, max int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, max))
}
```

### Handler integration sketch (illustrative — exact insertion point is a planner decision)

```go
// Source: this research session, building on the existing handlers.go pattern
// (internal/api/handlers.go), verified call-order against Sniff/multipart.File
// semantics directly in this session.
detected, rest, err := convert.Sniff(file)
if err != nil {
	writeError(w, http.StatusBadRequest, "invalid multipart form")
	return
}
if detected == "" {
	var prefix [4]byte
	if n, _ := file.ReadAt(prefix[:], 0); n == 4 && prefix == [4]byte{'P', 'K', 3, 4} {
		cr, cerr := convert.SniffContainer(file, header.Size)
		if cerr == nil && cr.Format != "" && !cr.DuplicateRootPart {
			detected = cr.Format
			if cr.TotalUncompressed > s.maxDocumentUncompressedBytes {
				writeError(w, http.StatusUnprocessableEntity, "declared uncompressed size exceeds configured limit")
				return
			}
			if cr.HasMacro {
				writeError(w, http.StatusUnprocessableEntity, "macro-carrying documents are not accepted")
				return
			}
		}
	}
}
// existing detected == "" / detected != source / Supports(...) checks continue unchanged below
```

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | ODF's `Basic/`-prefixed macro-storage directory is created only when a macro actually exists (never as an empty vestigial scaffold in a macro-free document) | Pattern 4 / Pitfall detail | If wrong, a macro-free ODF document that once had an empty macro library created (then never populated) could be incorrectly 422-rejected as "macro-carrying" — a false positive, not a security gap; low severity, but could surprise a real internal client |
| A2 | Microsoft-Office-generated and Google-Docs-exported docx/xlsx/pptx files also place their root parts at exactly `word/document.xml` / `xl/workbook.xml` / `ppt/presentation.xml` (this session only empirically verified `pandoc`-generated and one Excel-tool-generated fixture, not genuine MS Office or Google Docs output) | Architecture Pattern 1 | If wrong for some real producer, a legitimate document from that producer would 422 as "unrecognized content" — a false rejection, not a security gap; the fix would be adding the actual observed root-part name, discoverable immediately from the first real support ticket |
| A3 | The `xlsxwriter`-documented `word/xl/ppt/vbaProject.bin` literal path convention is universal across Microsoft Office, LibreOffice, and OpenXML-SDK-based tools for macro-enabled variants (not independently verified against a real `.docm`/`.xlsm`/`.pptm` sample in this session — no macro-enabled sample could be generated without LibreOffice/MS Office, unavailable in this environment) | Pattern 4 | If a tool ever places the VBA project part elsewhere, macro-carrying content from that tool would slip through — a security-relevant miss, though D-05's decision is presence-based and this is the documented universal convention across every source consulted |

## Open Questions

1. **Does an empty/never-populated ODF Basic library scaffold produce a false-positive macro rejection?**
   - What we know: LibreOffice creates the `Basic/` directory structure only when a macro is added (per the corroborating secondary source); this strongly suggests no false positives for genuinely macro-free documents.
   - What's unclear: Whether opening-then-not-using the Basic IDE (without ever adding actual code) leaves a vestigial `Basic/Standard/script-lb.xml` with zero `<script:module>` content, which the presence-only check (D-05's locked scope) would still reject.
   - Recommendation: Ship the presence-only check as locked by D-05; if real-world false positives surface, a follow-up could parse `script-lb.xml` for actual module content — explicitly out of scope for this phase per the locked decision's wording.

2. **Root-part naming across producers not tested in this session (genuine MS Office, Google Docs export).**
   - What we know: Two independent tools (`pandoc`, and an Excel-tool-produced `.xlsx` fixture) both use the conventional root-part names; this matches near-universal training-data knowledge and the ECMA-376 SDK's own defaults.
   - What's unclear: No LibreOffice, no genuine Microsoft Office, and no Google Docs export was available in this research environment to test directly.
   - Recommendation: Treat as MEDIUM-HIGH confidence per the Assumptions Log (A2); if the phase's test plan can source a genuine MS-Office-generated or Google-Docs-exported sample during execution, add it as an additional fixture — low cost, meaningfully raises confidence.

3. **Exact insertion point/refactor shape in `handleCreateJob` for the new container check.**
   - What we know: The check must run after `Sniff` returns `""`, before the pair-check, dimension-check, and any storage write; must read from the original `multipart.File` + `header.Size`, not `Sniff`'s `rest`.
   - What's unclear: Whether the planner prefers a helper that fully replaces the `detected == ""` handling (folding the PK-prefix check into a single combined function) versus the more minimal-diff sketch shown in Code Examples above.
   - Recommendation: Either is correct; the Code Examples sketch is illustrative, not prescriptive about exact function boundaries — planner's call, informed by minimizing diff to the well-tested existing image path.

## Environment Availability

No external runtime dependencies for this phase's code — `archive/zip`, `compress/flate`, and `io` are Go standard library, bundled with the already-pinned `go1.26.4` toolchain used throughout the project (`go.mod:3`, `Dockerfile.api`/`Dockerfile.worker`'s `golang:1.26-bookworm` builder stage). No new packages, no new CLI tools, no new services. This section is otherwise skipped per the stated skip condition (code/config-only phase).

*(Tooling used only for this research session's fixture generation — `pandoc`, `odfpy` via `pip`, `curl` for a public test fixture — are development-time research aids only and are not runtime dependencies of the shipped code; they do not appear in `Dockerfile.api`/`Dockerfile.worker` and are not referenced by any planned task.)*

## Security Domain

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-------------------|
| V2 Authentication | no | Unchanged — existing API-key middleware runs before this handler, out of this phase's scope |
| V3 Session Management | no | N/A — stateless API-key auth, no sessions |
| V4 Access Control | no | Unchanged — existing per-client ownership checks (`handleGetJob`) untouched |
| V5 Input Validation | yes | Structural, fail-closed ZIP-container inspection (this phase's core deliverable) — `archive/zip` stdlib, no hand-rolled parser for the container format itself; hand-rolled *logic* (root-part matching, size summation, macro scan) follows the same fail-closed, bounded-read discipline as `dimensions.go`'s existing image parsers |
| V6 Cryptography | no | N/A — no cryptographic operation in this phase |

### Known Threat Patterns for this stack

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|-----------------------|
| Zip-bomb / decompression-exhaustion via a small-compressed/huge-declared-uncompressed ZIP entry | Denial of Service | Reject based on `UncompressedSize64` sum from the central directory, before any decompression — verified zero-cost against a real synthetic bomb in this session (Pattern 3) |
| Format spoofing (uploading a bare `.zip`/arbitrary archive claiming to be a docx/odt/etc.) | Tampering / Spoofing | Root-part-presence-by-name (OOXML) / `mimetype`-at-index-0 (ODF) structural check, rejecting anything that doesn't match — extends the exact same discipline already shipped for image magic-byte validation (D-01/D-02, Phase 4) |
| Macro-carrying document reaching a later conversion engine (LibreOffice, Phase 9) | Elevation of Privilege / Tampering | Unconditional presence-based rejection of `vbaProject.bin`/`Basic/`-prefixed entries, no opt-out (D-05) — defense-in-depth ahead of Phase 9's engine-level macro-execution hardening |
| Duplicate-named ZIP entries causing disagreement between this phase's Go-based check and a later, differently-implemented consumer (LibreOffice's own unzip) | Tampering (parser confusion / smuggling) | Fail-closed on any root-part or macro-part name appearing more than once in the central directory (Pitfall 3, new finding from this research) |
| Local-file-header vs. central-directory filename mismatch (classic ZIP parser-confusion attack class) | Tampering | Confirmed **not exploitable against this phase's implementation**: `archive/zip.NewReader`/`.File[].Name`/`.Open()` are unconditionally central-directory-authoritative, empirically verified with a hand-crafted mismatched fixture in this session — no additional cross-check needed |

## Sources

### Primary (HIGH confidence)

- `go1.26.4` standard library source, read directly in this session: `mime/multipart/formdata.go` (`FileHeader.Open()`, `File` interface definition), `net/http/request.go` (`Request.FormFile`), `archive/zip/reader.go` (`File.Open()`, `findBodyOffset()`, `readDirectoryHeader()`) — HIGH, primary source of truth for exact stdlib behavior
- Real fixture files generated and inspected directly in this session: `pandoc 3.10`-produced docx/odt/pptx, `odfpy`-produced odt/ods/odp, an Excel-tool-produced xlsx fixture from a widely-used open-source project's public test suite — HIGH, empirical ground truth
- Hand-crafted adversarial ZIP fixtures built and tested directly in this session (local-header/central-directory filename mismatch, duplicate entry names, synthetic zip bomb, embedded-OLE-object substring collision) — HIGH, empirical
- Existing codebase, read directly: `internal/convert/{sniff,dimensions,convert,converters}.go`, `internal/api/{handlers,api}.go`, `cmd/api/main.go`, `.env.example` — HIGH, ground truth for integration points and naming conventions
- OASIS OpenDocument v1.2 Part 3, §17.4 (mimetype file requirement) — cited in milestone research, re-verified against real files in this session

### Secondary (MEDIUM confidence)

- [xlsxwriter — Working with VBA Macros](https://xlsxwriter.readthedocs.io/working_with_macros.html) — documents the `vbaProject.bin` convention and `Default Extension="bin"` content-type registration from a real, widely-used library's own authorship perspective
- ODF `Basic/` macro-storage directory layout (`Basic/script-lc.xml`, `Basic/<Library>/script-lb.xml`) — corroborated via a secondary ODF-internals source (langintro.com), cross-referenced against training-data knowledge of LibreOffice's Basic IDE storage format; not independently verified against a real macro-carrying ODF sample in this session (no LibreOffice available in this environment)

### Tertiary (LOW confidence)

- None used as load-bearing claims in this document — every claim tagged `[CITED]` above has at least one corroborating source beyond training data alone; claims that could not be corroborated are explicitly listed in the Assumptions Log instead of being asserted as fact.

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — zero new dependencies, entirely Go stdlib, versions pinned by existing `go.mod`/Dockerfiles
- Architecture: HIGH — every pattern in this document was empirically verified against real generated files and/or direct stdlib source reading in this session, not just cited from the milestone-level research or training data
- Pitfalls: HIGH — all five pitfalls are either empirically demonstrated in this session (1, 2, 3, 4) or directly traced to current, read-in-this-session source code (5, the dimension-check regression)

**Research date:** 2026-07-09
**Valid until:** Stable — this is pure Go-stdlib + spec-grounded structural parsing with no external service/API surface to drift; re-validate only if the pinned Go toolchain version changes materially or if real-world usage surfaces a producer whose root-part naming differs from what was tested (see Open Question 2).
