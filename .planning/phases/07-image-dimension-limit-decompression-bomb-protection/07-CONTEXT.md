# Phase 7: Image Dimension Limit (Decompression-Bomb Protection) - Context

**Gathered:** 2026-07-09
**Status:** Ready for planning

<domain>
## Phase Boundary

`handleCreateJob` rejects an upload whose *declared* pixel dimensions (parsed from the format's own header fields, not decoded pixel data) exceed a configured total-pixel-count limit, before conversion or storage — closing the decompression-bomb gap explicitly deferred as D-09 in Phase 4. This phase covers: a zero-dependency dimension parser for each of the 5 formats registered in `convert.Default` (png/jpg/webp/heic/tiff), a configurable pixel-count limit, and wiring the check into `handleCreateJob` between content-format detection and the pair-check/upload. It does NOT cover: any change to `convert.Sniff`'s existing magic-byte detection (Phase 4, unchanged — the dimension parser is a new, separate pass over the same reader), any change to the actual conversion/decode step in the worker (still bounded only by `ENGINE_TIMEOUT` + process-group kill, unchanged), or general request-body size limits (already covered by `MAX_UPLOAD_BYTES`/`http.MaxBytesReader`, Phase 1, unrelated — this phase protects against a *small* file whose *declared* dimensions would explode in decoded memory, not a large file).

</domain>

<decisions>
## Implementation Decisions

### Parsing approach
- **D-01:** Zero-dependency, hand-written binary parsers — one per registered format — reading only the fixed-position header fields needed to extract declared width/height, never decoding any pixel data and never adding a new Go module dependency. Matches Phase 4's D-03 philosophy (self-contained, no external image-parsing library, full control over exactly what's trusted) applied to dimension extraction instead of format detection.
- **D-02 (rejected alternatives, for the record):** `golang.org/x/image` (webp/tiff decoders) was considered and rejected — it would need the same `[ASSUMED]`-package human-verify gate Phase 4 hit with `prometheus/client_golang`, for marginal code savings. Shelling out to the worker's existing `vipsheader` CLI was also considered and rejected — the API process currently never execs anything; this would be the first process-exec surface in the API's request path (a meaningfully different threat profile than the worker, which only execs on already-validated jobs) and would require adding `libvips-tools` to `Dockerfile.api`.
- **D-03 (HEIC included, not deferred):** Unlike Phase 4's D-09 (which deferred dimension-limiting entirely, for all formats), this phase writes a minimal ISOBMFF box-walker sufficient to reach the `ispe` box (nested under `meta` → `iprp` → `ipco`) and read its declared width/height. All 5 registered formats get equal protection — HEIC is not carved out as an accepted residual risk.

### Limit shape and value
- **D-04:** The limit is a single total-pixel-count ceiling (`declared_width * declared_height <= limit`), not independent per-dimension caps. A total-pixel budget matches the actual decoded-memory cost (the thing being protected against) more directly than bounding width and height separately, and doesn't reject legitimately long/thin images that stay under the pixel budget.
- **D-05:** Default limit is 100 megapixels (100,000,000 total pixels — e.g. 10000×10000). Configurable via a new env var (exact name left to Claude's Discretion, following the existing `os.Getenv`-only convention — e.g. `MAX_IMAGE_PIXELS`).

### Pipeline placement
- **D-06:** The dimension check runs in `handleCreateJob`, after `convert.Sniff`'s existing detected/source/pair-check sequence confirms the format is one of the 5 registered ones, and BEFORE `s.storage.Upload` — consistent with the existing "reject before any storage write" discipline (VALID-01/02, D-01 from Phase 4). On a limit violation: reject with 422 (same status class as the existing content-validation rejections), and log via the same D-08-style `client_id`-tagged `log.Printf` pattern already established in `handleCreateJob` for Sniff rejections.
- **D-07:** The dimension parser reads from `rest` (the reader `Sniff` returns, which already re-stitches the 12-byte sniff peek onto the remaining stream) — it must NOT assume `rest` is seekable (it's a plain `io.Reader`/`io.MultiReader` chain, not a `multipart.File` at that point) and must NOT fully buffer the upload. It reads a small, bounded additional prefix beyond Sniff's 12 bytes (exact size is per-format and left to research/planner — e.g. enough to cover a PNG IHDR chunk, a bounded JPEG marker scan, or a bounded HEIC box walk) and re-stitches that prefix onto the remainder the same way `Sniff` does, so the full original file still reaches `s3.Upload` unmodified.

### Claude's Discretion
- Exact env var name for the pixel-count limit (e.g. `MAX_IMAGE_PIXELS`) — follow existing naming convention.
- Exact bounded-read sizes per format for the dimension parser (how many bytes to peek for JPEG's marker scan, HEIC's box walk, etc.) — this is a case where research is strongly recommended before planning, mirroring Phase 4's approach of independently verifying exact byte-level signatures rather than trusting general knowledge (HEIC's box nesting in particular has real pitfall potential, similar to Phase 4 research's HEIC brand-list finding).
- Exact package/file location for the new dimension-parsing code (e.g. a new `internal/convert/dimensions.go` alongside `sniff.go`, following the same package/file-per-responsibility convention) — technical detail.
- Behavior when a format's dimension fields cannot be located within the bounded read window (e.g. a truncated or unusually-structured file, or a TIFF whose IFD offset points beyond the bounded prefix) — planner/researcher to decide whether this is a conservative reject (422, "cannot determine declared dimensions") or an accept-with-unknown-dimensions fallback; should lean toward the fail-closed/conservative-reject side given this is explicitly a security control, but the exact behavior is not user-specified.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Project & Requirements
- `.planning/PROJECT.md` — Current Milestone v1.1 section
- `.planning/REQUIREMENTS.md` — `VALID-03` (locked v1.1 scope for this phase)
- `.planning/ROADMAP.md` — Phase 7 goal, success criteria

### Prior Phase Context (the decision being extended)
- `.planning/milestones/v1.0-phases/04-content-validation-storage-lifecycle-observability/04-CONTEXT.md` — D-09: the original decompression-bomb deferral this phase now implements. D-03 (zero-new-dependencies philosophy for content validation) is the direct precedent D-01 in this phase's decisions extends.
- `.planning/milestones/v1.0-phases/04-content-validation-storage-lifecycle-observability/04-RESEARCH.md` — HEIC brand-list / ISOBMFF box-structure research from Phase 4's `Sniff` work; this phase's HEIC `ispe`-box walker builds on the same box-format understanding.

### Existing Codebase (reference patterns to follow)
- `internal/convert/sniff.go` — `Sniff(r io.Reader) (detected string, rest io.Reader, err error)`, the exact peek-and-restitch pattern (D-07 in this phase) to replicate for the dimension parser's own bounded read; `signatures`/`heicBrands` show the existing per-format table and box-detection style to extend.
- `internal/api/handlers.go` `handleCreateJob` (~lines 95-172) — exact current sequence: `Sniff` → unrecognized/mismatch rejection → pair-check → `callback_url` validation → `s3.Upload`. The dimension check (D-06) lands between the pair-check and `callback_url` validation (or immediately after Sniff's mismatch checks — planner to decide exact ordering relative to the pair-check, but MUST be before `s3.Upload`), using the same `client.ID`-tagged `log.Printf` + `writeError(w, http.StatusUnprocessableEntity, ...)` idiom already used for Sniff rejections.
- `internal/convert/convert.go` — `NormalizeFormat`, `convert.Default` registry — the 5-format closed list (png/jpg/webp/heic/tiff) this phase's parser table must match exactly, same as `sniff.go`'s `signatures` table.

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `convert.Sniff`'s peek-and-restitch idiom (`io.ReadFull` into a fixed buffer, then `io.MultiReader(bytes.NewReader(buf), r)`) — the exact mechanical pattern the new dimension parser reuses, just with a larger/per-format-variable bounded buffer instead of the fixed 12-byte `sniffLen`.
- `convert.NormalizeFormat` — the detected format string from `Sniff` is already normalized; the dimension parser's format-to-parser dispatch can key directly off it.

### Established Patterns
- Hardcoded, zero-dependency format tables (`sniff.go`'s `signatures`) — this phase adds a second such table (format → dimension-parsing function) in the same spirit.
- "Reject before any storage write" (Phase 4 VALID-01/02) — the dimension check is one more gate in that same sequence, not a new architectural pattern.
- `client.ID`-tagged `log.Printf` for content-validation rejections (Phase 4 D-08) — reused verbatim for dimension-limit rejections.

### Integration Points
- `internal/api/handlers.go` `handleCreateJob` — where the new dimension check call lands, between `Sniff`'s validation and `s3.Upload`
- New file (likely `internal/convert/dimensions.go`) — where the 5 per-format dimension parsers and the dispatch function live

</code_context>

<specifics>
## Specific Ideas

No UI/UX references — backend-only, single-request-path phase, same character as Phase 4/5. Concrete asks: zero-dependency parsers for all 5 formats including HEIC (no format carved out as an accepted risk this time), a single total-pixel-count ceiling defaulting to 100 megapixels, and placement in the existing pre-upload validation gate in `handleCreateJob`.

</specifics>

<deferred>
## Deferred Ideas

None raised this phase beyond what Phase 4 already deferred (D-09 itself is being closed here, not further deferred).

</deferred>

---

*Phase: 7-Image Dimension Limit (Decompression-Bomb Protection)*
*Context gathered: 2026-07-09*
