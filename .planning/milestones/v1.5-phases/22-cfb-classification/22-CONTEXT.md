# Phase 22: CFB Encrypted-vs-Legacy Classification - Context

**Gathered:** 2026-07-13
**Status:** Ready for planning
**Source:** v1.5 research (FEATURES/ARCHITECTURE/PITFALLS + synthesis Key Decision), user-confirmed

<domain>
## Phase Boundary

Split the single OLE-CFB 422 ("legacy binary or password-protected Office format...", internal/api/handlers.go:248) into two distinct, accurate 422s — «файл запаролен» vs «устаревший бинарный формат» — via a hand-rolled, bounded, fuzz-hardened CFB directory parser in internal/convert. Never decrypt; never accept a CFB file.

</domain>

<decisions>
## Implementation Decisions

### Parser (synthesis Key Decision — hand-rolled, zero new deps)
- D-01: New `ClassifyCFB(r io.ReaderAt, size int64) CFBClass` in internal/convert (new file cfb.go alongside olecfb.go; IsOLECFB stays untouched as the cheap pre-filter). CFBClass ∈ {CFBEncrypted, CFBLegacy, CFBUnknown}
- D-02: Scope: header (sector size, first directory sector, FAT) → walk the directory sector chain via FAT → read directory entry NAMES only (UTF-16LE, 64-byte name field per entry, 128-byte entries). NO mini-FAT, NO stream content reads, NO decompression
- D-03: Classification by root-storage stream names: encrypted if `EncryptionInfo` OR `EncryptedPackage` present (covers Standard+Agile OOXML crypto); legacy if `WordDocument` OR `Workbook` OR `Book` OR `PowerPoint Document` present; encrypted markers WIN if both appear. Anything else → CFBUnknown
- D-04: DoS hardening (openmcdf GHSA-jxpf-xq2m-q525 class): visited-set cycle guard on the FAT sector chain; hard caps — max 4096 directory sectors walked, all reads bounds-checked against size; any violation/short read/parse error → CFBUnknown (fail-closed, never panic, never loop)
- D-05: FuzzClassifyCFB (Go native fuzzing) with seed corpus = real fixtures (internal/e2e/testdata/legacy.doc, encrypted.docx) + crafted headers; exit-gate: a bounded fuzz run (e.g. -fuzztime 30s locally) crash-free; the seed corpus runs in normal `go test` (CI tier 1/2 coverage automatically)

### API integration
- D-06: handleCreateJob's existing IsOLECFB branch calls ClassifyCFB: CFBEncrypted → 422 "password-protected Office file is not supported; remove the password and re-upload"; CFBLegacy → 422 "legacy binary Office format (.doc/.xls/.ppt) is not supported; convert to docx/xlsx/pptx"; CFBUnknown → the EXISTING combined 422 text unchanged (fail-closed compat). Log reason= values: encrypted_document / legacy_document / legacy_or_encrypted_document respectively
- D-07: internal/e2e's TestOLECFBRejectionE2E updated: legacy.doc expects the legacy message, encrypted.docx expects the encrypted message — live-proven against the compose stack (UNCONDITIONAL hard gate per project discipline)

### Claude's Discretion
- Exact struct/helper layout in cfb.go; whether ClassifyCFB takes the already-read header bytes
- Additional unit fixtures (tiny hand-crafted CFB byte slices in tests)

</decisions>

<canonical_refs>
## Canonical References

- `internal/convert/olecfb.go` — existing magic detector (pre-filter, unchanged)
- `internal/api/handlers.go` (~lines 238-249) — the branch being split; writeError conventions
- `internal/e2e/e2e_test.go` — TestOLECFBRejectionE2E + fixtures testdata/legacy.doc, encrypted.docx
- `internal/worker/worker.go` — NOT touched (rejection is pre-queue; no terminal-signature coupling needed)
- `.planning/research/FEATURES.md` (CFB stream names, two independent sources; validate Workbook vs Book against the real .xls fixture)
- `.planning/research/PITFALLS.md` (P-CFB: directory-cycle DoS, false-positive risk, fail-closed default)
- `.planning/research/SUMMARY.md` (Key Decision: hand-rolled + mandatory fuzz gate)

</canonical_refs>

<specifics>
## Specific Ideas

- Research flagged: validate the legacy Excel stream name (`Workbook` vs `Book`) against a real .xls during implementation — include both in the legacy set and confirm via the fixture
- MS-CFB spec facts: 512-byte header, sector size 2^SectorShift (512/4096), directory entry = 128 bytes, name = UTF-16LE with length field; FAT chain terminates with ENDOFCHAIN 0xFFFFFFFE

</specifics>

<deferred>
## Deferred Ideas

- CFB-содержимое (извлечение, конвертация legacy) — никогда в скоупе
- mscfb-библиотека — отклонена решением синтеза
</deferred>

---

*Phase: 22-cfb-classification*
*Context gathered: 2026-07-13*
