# Phase 8: Document Content Safety & Format Detection - Context

**Gathered:** 2026-07-09
**Status:** Ready for planning

<domain>
## Phase Boundary

The API can structurally tell a genuine docx/xlsx/pptx/odt/ods/odp from a spoofed, malformed, or hostile one, and rejects anything unsafe before it touches S3 or the (not-yet-built) LibreOffice engine. This phase covers: extending content-format detection to disambiguate the 6 office formats (all ZIP-based, sharing the same `PK\x03\x04` magic bytes with each other and with a bare `.zip`), a zip-bomb declared-size guard, and macro-part rejection. It does NOT cover: the LibreOffice converter itself (Phase 9), the document worker/queue/reconciler wiring (Phase 10), or `handleCreateJob`'s engine-routing branch (Phase 11) — this phase's checks slot into the existing `Sniff` → pair-check sequence but the actual enqueue-to-document-queue routing is Phase 11's job. Also does NOT cover: the confirmed pre-existing regression where `handleCreateJob`'s unconditional `convert.Dimensions()` call would currently 422 every document upload — fixing that (a `HasDimensionLimit`-style predicate scoping the image-only dimension check) is in this phase's scope since it's part of making document uploads work at all through the existing pipeline, but it is a narrow bugfix, not new dimension-limiting behavior for documents.

</domain>

<decisions>
## Implementation Decisions

### Format disambiguation approach
- **D-01:** Structural container inspection, not extension trust — mirrors Phase 4's D-01 philosophy exactly. ODF (odt/ods/odp) disambiguates via the OASIS-mandated first-ZIP-entry `mimetype` file (uncompressed, fixed byte offset, per research `.planning/research/ARCHITECTURE.md`/`SUMMARY.md`). OOXML (docx/xlsx/pptx) disambiguates via reading the ZIP central directory (stdlib `archive/zip`, needs `io.ReaderAt` — `multipart.File` already satisfies this, confirmed in research) and inspecting `[Content_Types].xml` for the format-specific content-type string (`wordprocessingml.document` / `spreadsheetml.sheet` / `presentationml.presentation`). Zero new dependencies (stdlib `archive/zip`, `compress/flate` only) — consistent with the project's established zero-new-deps philosophy (Phase 4 D-03, Phase 7 D-01).
- **D-02:** This is architecturally a new, second detection path alongside the existing `sniff.go` prefix-signature table (`Sniff`) — not a drop-in extension of it, since OOXML disambiguation requires reading the ZIP central directory (needs a `ReaderAt`), not just a fixed byte prefix like every existing signature. Planner/researcher to decide exact code organization (e.g. a new file alongside `sniff.go`, or a variant entry point), but the two detection styles (prefix-match vs. container-inspection) are distinct enough to not force into the same function shape.

### Zip-bomb guard
- **D-03:** Limit is on total declared uncompressed size summed across all ZIP central-directory entries (`UncompressedSize64`), not a per-entry compression-ratio check. Matches the "declared metadata, reject before expensive work" philosophy already used for `MAX_IMAGE_PIXELS` (Phase 7 D-04) — simpler to implement and reason about than a per-entry ratio heuristic, and sufns to catch the classic zip-bomb pattern (a document expanding to GB/TB from a tiny file).
- **D-04:** Default limit is 500 MiB total declared uncompressed size across all entries. Configurable via a new env var (exact name left to Claude's Discretion, following the existing `MAX_IMAGE_PIXELS`/`MAX_UPLOAD_BYTES` naming convention — e.g. `MAX_DOCUMENT_UNCOMPRESSED_SIZE`). Chosen with headroom for legitimate large xlsx/pptx with many embedded images, while still catching zip bombs that typically expand to GB/TB scale from a near-empty input.

### Macro rejection
- **D-05:** Hard, unconditional rejection — no operator opt-out flag (unlike Phase 5's `WEBHOOK_ALLOW_PRIVATE_IPS` pattern, which was deliberately introduced for a legitimate opt-in use case). Macros serve no purpose in a PDF-conversion pipeline (macro code never executes as part of producing a PDF output), so there is no legitimate reason for any client to need macro-enabled documents accepted. Detection: presence of `vbaProject.bin` (OOXML macro-enabled variants — docm/xlsm/pptm and similar) or a Basic-script manifest entry (ODF's macro storage convention) among the ZIP entries, checked in the same container-inspection pass as D-01's format disambiguation and D-03's zip-bomb guard.

### Claude's Discretion
- Exact env var name for the zip-bomb limit (e.g. `MAX_DOCUMENT_UNCOMPRESSED_SIZE`) — follow existing naming convention (`MAX_UPLOAD_BYTES`, `MAX_IMAGE_PIXELS`).
- Exact file/function organization for the new OOXML/ODF container-inspection code (e.g. a new `internal/convert/docsniff.go` alongside `sniff.go`, or a differently-named file) — technical detail, follow the project's "one file per responsibility" convention.
- Exact mechanism for the `HasDimensionLimit`-style predicate fixing the confirmed dimension-check regression (research already identified the gap precisely — `internal/convert/dimensions.go`'s `dimensionParsers` map has no document-format entries, so `Dimensions()` fails closed with `ErrDimensionsUnknown` for every document today) — planner/researcher to decide the exact guard shape in `handleCreateJob`, but the behavior must be: documents skip the dimension check entirely (it's an image-only concept), not that documents get a document-specific dimension check (that's out of scope, see Phase Boundary).
- Whether the macro-detection check and the zip-bomb check both run inside the same single ZIP-central-directory read pass as the format-disambiguation check (D-01/D-02), or as separate passes — likely the same pass for efficiency (avoid re-parsing the central directory three times), but this is an implementation detail.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Project & Requirements
- `.planning/PROJECT.md` — Current Milestone v1.2 section
- `.planning/REQUIREMENTS.md` — `DOC-01`, `DOC-02`, `DOC-03` (locked v1.2 scope for this phase)
- `.planning/ROADMAP.md` — Phase 8 goal, success criteria

### Research (already resolved most of the technical "how" — read before researching further)
- `.planning/research/SUMMARY.md` — Executive summary, Phase 1 (mapped to this Phase 8) rationale, the confirmed dimension-check regression finding
- `.planning/research/ARCHITECTURE.md` — "Addendum: v1.2 Document Engine Class" section — ZIP disambiguation approach (OOXML central-directory `[Content_Types].xml` inspection vs. ODF fixed-offset `mimetype` check), confirmed dimension-check gap with exact file/line references
- `.planning/research/FEATURES.md` — Content validation table-stakes section — ODF/OOXML structural sniffing rationale, zip-bomb guard, macro-part rejection, sourced against the OASIS ODF spec and community OOXML detection conventions
- `.planning/research/PITFALLS.md` — Pitfall 6 (resource-exhaustion surface, accepted as residual risk per REQUIREMENTS.md's Out of Scope — this phase's zip-bomb guard is the declared-metadata-layer mitigation, not a complete defense)

### Prior Phase Context (the pattern this phase extends)
- `.planning/milestones/v1.1-phases/07-image-dimension-limit-decompression-bomb-protection/07-CONTEXT.md` — D-04/D-05 (total-pixel-count-ceiling philosophy for `MAX_IMAGE_PIXELS`) — direct precedent for this phase's D-03/D-04 (total-uncompressed-size ceiling for zip-bomb)
- `.planning/milestones/v1.0-phases/04-content-validation-storage-lifecycle-observability/04-CONTEXT.md` — D-01/D-03 (structural-content-over-extension-trust philosophy, zero-new-dependencies) — direct precedent for this phase's D-01

### Existing Codebase (reference patterns to follow)
- `internal/convert/sniff.go` — `Sniff(r io.Reader) (detected string, rest io.Reader, err error)`, the peek-and-restitch idiom; existing `signatures` table is prefix-match only and does NOT need to change for docx/xlsx/pptx (their shared `PK\x03\x04` ZIP prefix could be added as a low-confidence pre-check, but the real disambiguation happens in the new container-inspection code per D-02)
- `internal/convert/dimensions.go` — `dimensionParsers` map, `ErrDimensionsUnknown` — the exact site of the confirmed regression; `Dimensions()` dispatch needs a document-format-aware guard
- `internal/api/handlers.go` `handleCreateJob` — current sequence: Sniff → mismatch rejection → pair-check → dimension check (currently unconditional, the bug) → callback_url validation → upload. This phase's new checks (format disambiguation, zip-bomb, macro) land in the same "reject before any storage write" position as the existing Sniff-based checks, likely right after or merged into the existing Sniff step's rejection logic — exact insertion point is a planner decision informed by Phase 11 also needing this pipeline (Phase 11 owns the actual engine-routing branch, not this phase).
- `internal/convert/convert.go` — `NormalizeFormat`, `convert.Default` registry — the format list this phase's detection must match against `docx`/`xlsx`/`pptx`/`odt`/`ods`/`odp` once `converters.go` registers the LibreOffice converter in Phase 9 (this phase's detection code can and should be built/tested against the format strings independently of whether a converter is registered yet).

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `Sniff`'s peek-and-restitch idiom (`io.MultiReader(bytes.NewReader(buf), r)`) — reusable pattern for however this phase's container-inspection code re-stitches its read prefix/central-directory read back onto the stream for the eventual S3 upload.
- `multipart.File` already implements `io.ReaderAt` (confirmed in research) — no buffering needed to support `archive/zip`'s `zip.NewReader(r io.ReaderAt, size int64)` requirement.

### Established Patterns
- Zero-new-dependency, hand-written binary/container parsing (Phase 4's `sniff.go`, Phase 7's `dimensions.go`) — this phase's OOXML/ODF container inspection continues that pattern using stdlib `archive/zip`/`compress/flate` instead of external libraries.
- "Reject before any storage write" (Phase 4 VALID-01/02, Phase 7 D-06) — this phase's three checks (format match, zip-bomb, macro) are one more gate in that same sequence.
- Total-declared-size/count ceiling instead of per-item heuristics (Phase 7 D-04's total-pixel-count) — directly precedents this phase's D-03 (total uncompressed size, not per-entry ratio).

### Integration Points
- `internal/api/handlers.go` `handleCreateJob` — where the new checks land, and where the confirmed dimension-check regression gets fixed
- New file(s) in `internal/convert/` — where the OOXML/ODF container-inspection, zip-bomb, and macro-detection code live

</code_context>

<specifics>
## Specific Ideas

No UI/UX references — backend-only, pre-upload validation gate, same character as Phase 4/7. Concrete asks: structural disambiguation of all 6 office formats via ZIP-container inspection (not extension trust), a 500 MiB total-uncompressed-size zip-bomb ceiling (configurable), and unconditional macro-part rejection with no opt-out.

</specifics>

<deferred>
## Deferred Ideas

None raised this phase — REQUIREMENTS.md's v2/Out-of-Scope sections already capture the relevant future items (DOC-V2-02 password-protected pre-flight detection, DOC-V2-05 active complexity-based anti-DoS) from the milestone-level discussion.

</deferred>

---

*Phase: 8-Document Content Safety & Format Detection*
*Context gathered: 2026-07-09*
