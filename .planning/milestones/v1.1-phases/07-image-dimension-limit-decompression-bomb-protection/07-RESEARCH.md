# Phase 7: Image Dimension Limit (Decompression-Bomb Protection) - Research

**Researched:** 2026-07-09
**Domain:** Zero-dependency binary parsing of image container/header formats (PNG, JPEG, WebP, TIFF, HEIC/ISOBMFF) to extract declared pixel dimensions from a bounded, non-seekable stream prefix, wired into an existing Go HTTP upload handler.
**Confidence:** HIGH for PNG/JPEG/WebP/TIFF byte layouts (cross-verified against official specs); MEDIUM-HIGH for HEIC (box structure verified against spec + a real-world sample + an existing Go implementation, but worst-case box-walk depth across all real-world encoders was not exhaustively tested).

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Parsing approach**
- **D-01:** Zero-dependency, hand-written binary parsers — one per registered format — reading only the fixed-position header fields needed to extract declared width/height, never decoding any pixel data and never adding a new Go module dependency. Matches Phase 4's D-03 philosophy applied to dimension extraction instead of format detection.
- **D-02 (rejected alternatives, for the record):** `golang.org/x/image` (webp/tiff decoders) rejected — would need the same `[ASSUMED]`-package human-verify gate Phase 4 hit with `prometheus/client_golang`, for marginal code savings. Shelling out to the worker's existing `vipsheader` CLI also rejected — the API process currently never execs anything; this would be the first process-exec surface in the API's request path and would require adding `libvips-tools` to `Dockerfile.api`.
- **D-03 (HEIC included, not deferred):** Unlike Phase 4's D-09 (which deferred dimension-limiting entirely, for all formats), this phase writes a minimal ISOBMFF box-walker sufficient to reach the `ispe` box (nested under `meta` → `iprp` → `ipco`) and read its declared width/height. All 5 registered formats get equal protection.

**Limit shape and value**
- **D-04:** The limit is a single total-pixel-count ceiling (`declared_width * declared_height <= limit`), not independent per-dimension caps.
- **D-05:** Default limit is 100 megapixels (100,000,000 total pixels — e.g. 10000×10000). Configurable via a new env var (exact name left to Claude's Discretion — e.g. `MAX_IMAGE_PIXELS`).

**Pipeline placement**
- **D-06:** The dimension check runs in `handleCreateJob`, after `convert.Sniff`'s existing detected/source/pair-check sequence confirms the format is one of the 5 registered ones, and BEFORE `s.storage.Upload` — consistent with "reject before any storage write" (VALID-01/02, D-01 from Phase 4). On a limit violation: reject with 422, log via the same D-08-style `client_id`-tagged `log.Printf` pattern already established in `handleCreateJob`.
- **D-07:** The dimension parser reads from `rest` (the reader `Sniff` returns) — must NOT assume `rest` is seekable and must NOT fully buffer the upload. It reads a small, bounded additional prefix beyond Sniff's 12 bytes and re-stitches that prefix onto the remainder the same way `Sniff` does, so the full original file still reaches `s3.Upload` unmodified.

### Claude's Discretion
- Exact env var name for the pixel-count limit (e.g. `MAX_IMAGE_PIXELS`) — follow existing naming convention.
- Exact bounded-read sizes per format for the dimension parser.
- Exact package/file location for the new dimension-parsing code (e.g. `internal/convert/dimensions.go`).
- Behavior when a format's dimension fields cannot be located within the bounded read window — should lean toward fail-closed/conservative-reject given this is a security control.

### Deferred Ideas (OUT OF SCOPE)
None raised this phase — D-09 (Phase 4's deferral) is being closed here, not further deferred.
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| VALID-03 | API отклоняет загрузку, если заявленные размеры изображения превышают настраиваемый лимит, до запуска конвертации (защита от decompression bomb) | Verified byte-exact layouts for all 5 registered formats (PNG IHDR, JPEG SOF, WebP VP8/VP8L/VP8X, TIFF IFD, HEIC ispe box walk); a single 64 KiB bounded peek-and-restitch strategy that reuses `Sniff`'s exact idiom; exact `handleCreateJob` insertion point; fail-closed behavior for all "cannot locate declared dimensions" cases — see Architecture Patterns, Code Examples, Common Pitfalls. |
</phase_requirements>

## Summary

This phase adds one new file, `internal/convert/dimensions.go`, following the exact peek-and-restitch idiom `internal/convert/sniff.go` already established in Phase 4: `io.ReadFull` into a fixed buffer, then `io.MultiReader(bytes.NewReader(buf), r)` to hand back an unconsumed stream. The only difference is buffer size — Sniff needs 12 bytes to detect a signature; dimension extraction needs a much larger bounded prefix to reach each format's declared-dimension fields, especially HEIC's deeply-nested `ispe` box.

All five formats' dimension fields were independently verified against official/authoritative specs (W3C PNG spec, TIFF 6.0/RFC 2301, ITU T.81 JPEG spec, RFC 9649 WebP, ISOBMFF/HEIF box structure) rather than trusted from training-data recall alone, matching this project's Phase 4 research discipline. **PNG** needs only 24 bytes (signature + length + type + width + height, all fixed-offset). **WebP** needs ~30 bytes for any of its three sub-formats (VP8/VP8L/VP8X), all fixed-offset once the FourCC is known. **JPEG** requires a linear marker scan from byte 2 (after SOI) until a Start-Of-Frame marker is found — bounded by how much APPn/EXIF/ICC metadata precedes the frame header in a real file, which is occasionally large (multi-KB ICC profiles, camera EXIF with embedded thumbnails). **TIFF** requires reading a 4-byte IFD offset and then indexing directly into the already-captured in-memory buffer at that offset — critically, once bytes are peeked into a `[]byte`, that buffer is fully random-access even though the underlying `io.Reader` is not; the "non-seekable" constraint only bites if the needed offset falls *outside* the bounded buffer, which is a real (if rare) case for streamed-writer TIFFs that append the IFD near EOF. **HEIC** is the outlier: a real iPhone-produced HEIC sample (4032×3024, single item) was analyzed byte-by-byte, showing `ftyp` (24 bytes) → `meta` (0xF74 = 3956 bytes total) → within `meta`: `hdlr`(34B), `dinf`(36B), `pitm`(14B), `iinf`(1085B), `iref`(148B), then `iprp`(1779B) containing `ipco`→`ispe` boxes — the first `ispe` box became reachable at roughly byte offset ~1370. This gives strong evidence that a **64 KiB bounded peek buffer** — the figure CONTEXT.md itself proposed as an example — comfortably covers the vast majority of real-world HEIC files (roughly 45× the observed reach-depth for a single-item photo), with generous headroom for burst-mode/multi-item files with larger `iinf`/`iref` tables.

**Primary recommendation:** implement ONE unified bounded peek of 64 KiB (not per-format variable sizes) from `rest`, re-stitch it via the same `io.MultiReader` pattern as `Sniff`, and dispatch to a per-format parser function operating on the in-memory `[]byte` slice. Any format whose dimension fields cannot be located within that 64 KiB window is a hard reject (422, `ErrDimensionsUnknown`) — this is both simpler to reason about than per-format variable windows and consistent with CONTEXT.md's explicit "lean fail-closed" guidance for the residual edge cases (huge multi-segment JPEG ICC profiles, TIFF IFD placed past 64 KiB, pathological multi-item HEIC).

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Declared-dimension extraction (5 format parsers) | API / Backend (`internal/convert`, format-scoped package) | — | Same tier as Phase 4's `Sniff` — format-registry-scoped logic, not a separate service. |
| Pixel-count limit enforcement + rejection | API / Backend (`internal/api/handlers.go`, `handleCreateJob`) | — | Must run before `s.storage.Upload`, inside the same request-handling code path as the existing content-validation gates (VALID-01/02). |
| Pixel-count limit configuration | API / Backend (`cmd/api/main.go` env parsing → `api.Config`) | — | Follows the exact existing pattern for `MaxUploadBytes`/`IPRateLimitRPM` — `os.Getenv`-only, no config file. |

## Standard Stack

### Core

No new dependency. Everything below uses only the Go standard library (`encoding/binary`, `bytes`, `io`), matching D-01/D-02.

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `encoding/binary` (stdlib) | Go 1.26 stdlib | `binary.BigEndian`/`binary.LittleEndian` `Uint16`/`Uint32`/`Uint64` for fixed-width field extraction | Already the correct, idiomatic tool for exactly this kind of binary-header parsing; zero risk of a slopsquatted/hallucinated package since it ships with the toolchain. |

### Supporting

None — no new supporting libraries needed.

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Hand-written per-format parsers (D-01, locked) | `golang.org/x/image/tiff`, `golang.org/x/image/webp`, or a general HEIF library (e.g. `jdeng/goheif`, `strukturag/libheif` cgo bindings) | User explicitly locked out new dependencies for this (D-02); `x/image`'s decoders also *decode* rather than just read declared header dimensions, which is strictly more work/risk than needed for a size-limit gate. A cgo-based HEIF library would also break the project's `CGO_ENABLED=0` static-binary build convention (`Dockerfile.api`). |

**Installation:** None — no `go get` required.

**Version verification:** N/A — no new package.

## Package Legitimacy Audit

**No external packages are introduced by this phase.** Per the Package Legitimacy Gate protocol, this section is required only "whenever this phase installs external packages" — it does not. All parsing uses Go's standard library (`bytes`, `encoding/binary`, `io`, already imported project-wide). No `go.mod` changes, no slopcheck/registry verification needed.

**Packages removed due to slopcheck [SLOP] verdict:** none — no packages considered.
**Packages flagged as suspicious [SUS]:** none.

## Architecture Patterns

### System Architecture Diagram

```text
multipart POST /v1/jobs
        │
        ▼
┌─────────────────────────────────────────────────────────────────┐
│ handleCreateJob (internal/api/handlers.go)                       │
│                                                                   │
│  1. ParseMultipartForm (MAX_UPLOAD_BYTES cap)                    │
│  2. r.FormFile("file") ──► file (multipart.File)                 │
│  3. convert.Sniff(file) ──► detected, rest1                      │
│       (peeks 12B, re-stitches: rest1 = MultiReader(buf12, file)) │
│  4. reject if detected == "" (unrecognized)                      │
│  5. reject if detected != declared extension (mismatch)          │
│  6. reject if !convert.Default.Supports(detected, target)        │
│  7. convert.Dimensions(detected, rest1) ──► w, h, rest2   [NEW]  │
│       (peeks ≤64KiB from rest1, re-stitches:                     │
│        rest2 = MultiReader(buf64k, rest1))                       │
│  8. reject 422 if err == ErrDimensionsUnknown            [NEW]  │
│  9. reject 422 if uint64(w)*uint64(h) > MAX_IMAGE_PIXELS  [NEW]  │
│ 10. validate callback_url (if present)                           │
│ 11. s.storage.Upload(ctx, key, rest2, header.Size, contentType)  │◄── rest2 still yields the FULL original file
│ 12. repo.Create (Postgres, status=queued)                        │
│ 13. queue.EnqueueImageConvert                                    │
└─────────────────────────────────────────────────────────────────┘
                                                                     Nothing after step 9 ever touches S3/Postgres
                                                                     for a request rejected at steps 4/5/6/8/9.
```

### Recommended Project Structure

```
internal/convert/
├── convert.go          # existing: Converter interface + Registry
├── converters.go       # existing: Default registry init()
├── libvips.go          # existing: imageFormats = {png, jpg, webp, heic, tiff}
├── sniff.go            # existing: 12-byte magic-byte signature table + Sniff()
├── sniff_test.go       # existing
├── dimensions.go       # NEW: dimPeekLen const, per-format parsers, Dimensions() dispatch, ErrDimensionsUnknown
└── dimensions_test.go  # NEW: byte-fixture tests per format + edge cases
internal/api/
└── handlers.go         # MODIFIED: handleCreateJob calls convert.Dimensions() between pair-check and callback_url validation
cmd/api/main.go          # MODIFIED: parse MAX_IMAGE_PIXELS env var, thread into api.Config
internal/api/api.go       # MODIFIED: Config gains MaxImagePixels field, Server gains maxImagePixels field
.env.example              # MODIFIED: document MAX_IMAGE_PIXELS with default
```

### Pattern 1: Second bounded peek-and-restitch, chained after Sniff's

**What:** Exactly reuse Sniff's `io.ReadFull` + `io.MultiReader` idiom, but with a 64 KiB buffer instead of 12 bytes, operating on the `rest` reader Sniff already returned.

**When to use:** Any time content must be classified/measured from further into the stream than the initial magic-byte peek, while still preserving the full stream for a later consumer (here, `s3.Upload`).

**Example:**
```go
// Source: pattern directly extends internal/convert/sniff.go's existing
// Sniff() implementation (Phase 4) — same idiom, larger buffer, and a
// dispatch table instead of a signature-match loop.
package convert

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
)

// dimPeekLen is the bounded prefix size read to locate each format's
// declared-dimension fields. 64 KiB comfortably covers PNG (24B), JPEG
// (marker scan; real EXIF/ICC segments are occasionally several KB but
// rarely tens of KB), WebP (~30B), TIFF (IFD offset + entries, assuming the
// IFD is placed near the start of the file, the common case), and HEIC
// (real-world sample: ispe reachable at ~1.4KB; 64 KiB gives >40x headroom
// for larger multi-item files). Any format whose fields aren't found within
// this window fails closed (ErrDimensionsUnknown) rather than growing the
// buffer or seeking further — D-07's explicit fail-closed guidance.
const dimPeekLen = 64 * 1024

// ErrDimensionsUnknown is returned when a registered format's declared
// pixel dimensions could not be located within the bounded peek window —
// treated as a rejection (D-07), not a fallback accept, since this is a
// resource-exhaustion security control.
var ErrDimensionsUnknown = errors.New("cannot determine declared image dimensions")

type dimensionParser func(buf []byte) (width, height uint32, ok bool)

var dimensionParsers = map[string]dimensionParser{
	"png":  pngDimensions,
	"jpg":  jpegDimensions,
	"webp": webpDimensions,
	"heic": heicDimensions,
	"tiff": tiffDimensions,
}

// Dimensions peeks up to dimPeekLen bytes from r (the `rest` reader Sniff
// already returned) to parse format's own declared width/height without
// decoding any pixel data. rest re-stitches the peeked prefix onto the
// remaining stream (same pattern as Sniff), so the full original stream
// still reaches storage.Upload unmodified.
func Dimensions(format string, r io.Reader) (width, height uint32, rest io.Reader, err error) {
	buf := make([]byte, dimPeekLen)
	n, readErr := io.ReadFull(r, buf)
	if readErr != nil && readErr != io.ErrUnexpectedEOF && readErr != io.EOF {
		return 0, 0, nil, readErr
	}
	buf = buf[:n]
	rest = io.MultiReader(bytes.NewReader(buf), r)

	parser, ok := dimensionParsers[NormalizeFormat(format)]
	if !ok {
		return 0, 0, rest, ErrDimensionsUnknown // format already validated by Supports before this is called
	}
	w, h, found := parser(buf)
	if !found {
		return 0, 0, rest, ErrDimensionsUnknown
	}
	return w, h, rest, nil
}
```

### Pattern 2: PNG — fixed-offset IHDR read (verified against W3C PNG spec)

**Verified layout** [CITED: w3.org/TR/png-3/#11IHDR]: 8-byte signature, then chunk = 4-byte length + 4-byte type + N-byte data + 4-byte CRC. IHDR is mandated to be the first chunk; its 13-byte data is width(4)+height(4)+bitdepth(1)+colortype(1)+compression(1)+filter(1)+interlace(1), both width/height as **big-endian uint32**.

| Field | Byte offset (from file start) | Size |
|---|---|---|
| Signature | 0–7 | 8 |
| Chunk length (=13) | 8–11 | 4 |
| Chunk type ("IHDR") | 12–15 | 4 |
| Width | 16–19 | 4 (uint32 BE) |
| Height | 20–23 | 4 (uint32 BE) |

Total bytes needed from file start: **24**.

```go
// Source: https://www.w3.org/TR/png-3/#11IHDR (IHDR chunk layout)
func pngDimensions(b []byte) (uint32, uint32, bool) {
	if len(b) < 24 || !bytes.Equal(b[12:16], []byte("IHDR")) {
		return 0, 0, false
	}
	width := binary.BigEndian.Uint32(b[16:20])
	height := binary.BigEndian.Uint32(b[20:24])
	return width, height, true
}
```

### Pattern 3: JPEG — bounded marker scan to first SOF segment (verified against ITU T.81)

**Verified layout** [CITED: ITU-T Rec. T.81 §B.1.1.3, cross-checked via multiple JPEG-structure references]: after the 2-byte SOI (`0xFFD8`), the stream is a sequence of marker segments: `0xFF` + marker byte, then (for markers that carry a payload) a 2-byte big-endian length (length field *includes itself*, i.e. payload length = length−2). Standalone markers with **no** length field: `TEM` (`0xFF01`) and `RSTn` (`0xFFD0`–`0xFFD7`); these must be skipped as exactly 2 bytes, not length-prefixed. `0xFF` bytes can also appear as fill/padding before a real marker and must be skipped individually. The frame header (SOF) marker is one of `0xFFC0`–`0xFFCF` **excluding** `0xFFC4` (DHT — Define Huffman Table), `0xFFC8` (JPG — reserved), and `0xFFCC` (DAC — Define Arithmetic Coding conditioning) — these three are *not* SOF markers despite falling in the `C0–CF` range and must be explicitly excluded or the parser will misread DHT/DAC segment bytes as if they were height/width. Inside an SOF segment: byte0 = precision (1 byte, usually 8), bytes 1–2 = **height** (big-endian uint16), bytes 3–4 = **width** (big-endian uint16) — height comes *before* width, a common source of transposition bugs.

| Field (relative to the `0xFF` of the SOF marker, call it `i`) | Offset | Size |
|---|---|---|
| Marker (`0xFFC0`..`0xFFC3`, etc.) | i, i+1 | 2 |
| Segment length | i+2, i+3 | 2 (BE) |
| Precision | i+4 | 1 |
| Height | i+5, i+6 | 2 (BE uint16) |
| Width | i+7, i+8 | 2 (BE uint16) |

Scan-window size: bounded by `dimPeekLen` (64 KiB); a single legitimate APPn/EXIF/ICC segment is capped at 65533 bytes by the 16-bit length field itself, but multiple large segments (e.g. a multi-chunk ICC profile, or a large embedded EXIF thumbnail) can occasionally exceed 64 KiB in unusual files — such files fail closed (see Common Pitfalls).

```go
// Source: ITU-T T.81 Annex B (marker segment structure, SOF marker range)
func jpegDimensions(b []byte) (uint32, uint32, bool) {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return 0, 0, false
	}
	i := 2
	for i+1 < len(b) {
		if b[i] != 0xFF {
			i++ // resync on stray non-marker byte
			continue
		}
		marker := b[i+1]
		if marker == 0xFF {
			i++ // fill byte
			continue
		}
		// Standalone markers: TEM (0x01), RSTn (0xD0-0xD7) — no length field.
		if marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7) {
			i += 2
			continue
		}
		if marker == 0xD8 { // stray SOI, skip
			i += 2
			continue
		}
		if marker == 0xD9 || marker == 0xDA {
			return 0, 0, false // EOI or SOS reached before any SOF found
		}
		if i+4 > len(b) {
			return 0, 0, false
		}
		segLen := int(b[i+2])<<8 | int(b[i+3])
		isSOF := marker >= 0xC0 && marker <= 0xCF &&
			marker != 0xC4 && marker != 0xC8 && marker != 0xCC
		if isSOF {
			if i+9 > len(b) {
				return 0, 0, false
			}
			height := uint32(b[i+5])<<8 | uint32(b[i+6])
			width := uint32(b[i+7])<<8 | uint32(b[i+8])
			return width, height, true
		}
		if segLen < 2 || i+2+segLen > len(b) {
			return 0, 0, false
		}
		i += 2 + segLen
	}
	return 0, 0, false
}
```

### Pattern 4: WebP — three sub-format layouts (verified against RFC 9649 / Google WebP container spec)

**Verified structure** [CITED: datatracker.ietf.org/doc/rfc9649/, developers.google.com/speed/webp/docs/riff_container]: 12-byte RIFF header (`RIFF` + 4-byte size + `WEBP`), then one or more chunks, each `FourCC(4) + Size(4, LE) + payload`. The first chunk's FourCC tells you the sub-format:

- **`VP8X`** (extended format): payload is 10 bytes — 1-byte flags, 3-byte reserved, then **canvas width minus 1** as a 24-bit little-endian value (3 bytes), then **canvas height minus 1** as a 24-bit little-endian value (3 bytes). This is sufficient on its own — no need to walk into the ALPH/ANIM/VP8/VP8L sub-chunks.
- **`VP8 `** (note trailing ASCII space — exactly 4 bytes, "VP8" is only 3): simple lossy. Payload: 3-byte frame tag, 3-byte start code (`0x9D 0x01 0x2A`), then two little-endian 16-bit fields: width = `value & 0x3FFF` (low 14 bits), horizontal scale = `value >> 14`; same for height.
- **`VP8L`**: simple lossless. Payload: 1-byte signature `0x2F`, then a 32-bit little-endian packed bitfield: bits 0–13 = width−1, bits 14–27 = height−1, bit 28 = alpha flag, bits 29–31 = version.

| Sub-format | File offset of payload start | Width/height field offsets (from file start) |
|---|---|---|
| VP8X | 20 (12 RIFF hdr + 4 FourCC + 4 size) | flags@20, reserved@21-23, width@24-26 (24-bit LE, +1), height@27-29 (24-bit LE, +1) |
| VP8  | 20 | start code@23-25 must equal `9D 01 2A`; width@26-27 (16-bit LE, &0x3FFF), height@28-29 (16-bit LE, &0x3FFF) |
| VP8L | 20 | signature@20 must equal `0x2F`; packed bitfield@21-24 (32-bit LE) |

Total bytes needed: **≤30** for any sub-format.

```go
// Source: RFC 9649 §6 (VP8X), Google WebP Container spec (VP8/VP8L chunk
// layout), WebP Lossless Bitstream Specification (VP8L packed header)
func webpDimensions(b []byte) (uint32, uint32, bool) {
	if len(b) < 20 || !bytes.Equal(b[0:4], []byte("RIFF")) || !bytes.Equal(b[8:12], []byte("WEBP")) {
		return 0, 0, false
	}
	fourCC := string(b[12:16])
	switch fourCC {
	case "VP8X":
		if len(b) < 30 {
			return 0, 0, false
		}
		width := uint32(b[24]) | uint32(b[25])<<8 | uint32(b[26])<<16
		height := uint32(b[27]) | uint32(b[28])<<8 | uint32(b[29])<<16
		return width + 1, height + 1, true
	case "VP8 ":
		if len(b) < 30 || b[23] != 0x9D || b[24] != 0x01 || b[25] != 0x2A {
			return 0, 0, false
		}
		w16 := uint32(b[26]) | uint32(b[27])<<8
		h16 := uint32(b[28]) | uint32(b[29])<<8
		return w16 & 0x3FFF, h16 & 0x3FFF, true
	case "VP8L":
		if len(b) < 25 || b[20] != 0x2F {
			return 0, 0, false
		}
		bits := uint32(b[21]) | uint32(b[22])<<8 | uint32(b[23])<<16 | uint32(b[24])<<24
		width := (bits & 0x3FFF) + 1
		height := ((bits >> 14) & 0x3FFF) + 1
		return width, height, true
	default:
		return 0, 0, false
	}
}
```

### Pattern 5: TIFF — header + first-IFD entry scan, operating on the in-memory buffer (verified against TIFF 6.0 / RFC 2301)

**Verified structure** [CITED: RFC 2301, TIFF 6.0 spec via cool.culturalheritage.org/bytopic/imaging/std/tiff4.html]: 8-byte header — 2-byte byte-order marker (`II`=little-endian, `MM`=big-endian), 2-byte magic number (42, in file's byte order), 4-byte offset (in file's byte order) to the first IFD. An IFD is: 2-byte entry count, then N × 12-byte entries (2-byte tag, 2-byte type, 4-byte count, 4-byte value-or-offset). **Critical nuance verified from spec**: if a value's encoded size (per its type — SHORT=2 bytes, LONG=4 bytes) is ≤4 bytes, it is stored **left-justified directly in the 4-byte value/offset field itself** (not at a separate offset) — this applies for both byte orders, i.e. a SHORT value always occupies the *first* 2 bytes of that 4-byte field, read with the file's own byte order. Tag 256 = `ImageWidth`, tag 257 = `ImageLength` (height); both are typically type 3 (SHORT) or type 4 (LONG).

**Bounded-read implication (the important part for D-07):** because the entire `dimPeekLen`-byte prefix is already captured into a `[]byte` before parsing starts, indexing into it at the IFD offset is safe random access — the "rest is not seekable" constraint only matters for the underlying `io.Reader` beyond that buffer. If the 4-byte first-IFD offset value points **past** the end of the captured buffer, the parser cannot follow it without either seeking (impossible) or buffering further (against D-07) — this must be a hard fail (`ErrDimensionsUnknown`). This is a real, if uncommon, case: some TIFF writers (particularly streaming/scanning software that writes image strips before finalizing the directory) place the IFD near the end of the file. See Common Pitfalls.

| Field | Offset | Size |
|---|---|---|
| Byte order marker | 0–1 | 2 |
| Magic number (42) | 2–3 | 2 |
| First IFD offset | 4–7 | 4 (uint32, file's byte order) |
| IFD entry count | `ifdOffset`–`ifdOffset+1` | 2 |
| Each IFD entry | `ifdOffset+2 + 12*i` | 12 |
| — tag | entry+0 | 2 |
| — type | entry+2 | 2 |
| — count | entry+4 | 4 |
| — value (left-justified) | entry+8 | 2 (SHORT) or 4 (LONG) |

```go
// Source: TIFF 6.0 spec / RFC 2301 (IFD structure, tag value left-
// justification rule)
func tiffDimensions(b []byte) (uint32, uint32, bool) {
	if len(b) < 8 {
		return 0, 0, false
	}
	var bo binary.ByteOrder
	switch {
	case bytes.Equal(b[0:2], []byte{0x49, 0x49}):
		bo = binary.LittleEndian
	case bytes.Equal(b[0:2], []byte{0x4D, 0x4D}):
		bo = binary.BigEndian
	default:
		return 0, 0, false
	}
	ifdOffset := bo.Uint32(b[4:8])
	if uint64(ifdOffset)+2 > uint64(len(b)) {
		return 0, 0, false // IFD beyond bounded window: fail closed (D-07)
	}
	count := bo.Uint16(b[ifdOffset : ifdOffset+2])
	entriesStart := ifdOffset + 2
	var width, height uint32
	var foundW, foundH bool
	for i := uint32(0); i < uint32(count); i++ {
		off := entriesStart + i*12
		if uint64(off)+12 > uint64(len(b)) {
			return 0, 0, false
		}
		tag := bo.Uint16(b[off : off+2])
		if tag != 256 && tag != 257 {
			continue
		}
		typ := bo.Uint16(b[off+2 : off+4])
		var val uint32
		switch typ {
		case 3: // SHORT — left-justified in first 2 bytes of the value field
			val = uint32(bo.Uint16(b[off+8 : off+10]))
		case 4: // LONG
			val = bo.Uint32(b[off+8 : off+12])
		default:
			continue
		}
		if tag == 256 {
			width, foundW = val, true
		} else {
			height, foundH = val, true
		}
		if foundW && foundH {
			return width, height, true
		}
	}
	return 0, 0, false
}
```

### Pattern 6: HEIC — bounded ISOBMFF box walk to `ispe` (verified against ISOBMFF/HEIF spec + real sample + existing Go implementation)

**Verified structure** [CITED: ISO/IEC 14496-12 box format; HEIF technical docs (nokiatech.github.io/heif); cross-checked against `jdeng/goheif`'s `heif`/`bmff` package, an existing Go HEIF parser]: every ISOBMFF box is `size(4, BE) + type(4, ASCII)` — 8-byte header — followed by payload; `size` includes the header itself. If `size == 1`, an extended 64-bit size follows the type field (16-byte header total); `size == 0` means "extends to end of file/container" (not expected for the boxes this phase needs, and treated as a bail-out). The path to declared dimensions is: top-level `ftyp` (already detected by Sniff, D-03) then top-level `meta` (a **FullBox** — 4 bytes of version/flags precede its children) → within `meta`'s children, `iprp` → within `iprp`'s children, `ipco` → `ipco`'s children include one or more `ispe` boxes. An `ispe` box's payload (it too is a FullBox) is: 4-byte version/flags, then **width** (4-byte BE uint32), then **height** (4-byte BE uint32) — 12 bytes of payload total.

**Real-world evidence (independently verified, not assumed):** a byte-level analysis of an actual iPhone 8 Plus HEIC file (4032×3024, single image item) showed: `ftyp` = 24 bytes; `meta` = 3956 bytes total, containing (in file order) `hdlr`(34B) → `dinf`(36B) → `pitm`(14B) → `iinf`(1085B) → `iref`(148B) → `iprp`(1779B, itself containing `ipco`→multiple `ispe` boxes, each 20 bytes total including header). Computing forward: `ftyp`(24) + `meta` header+version(12) + `hdlr`+`dinf`+`pitm`+`iinf`+`iref`(1317) = **~1353 bytes** before `iprp` even starts — the first `ispe` box becomes reachable well under 1.4 KB into the file. [CITED: cheeky4n6monkey.blogspot.com/2017/10/monkey-takes-heic.html, box-by-box hex analysis]

**Box order is not guaranteed by spec** — real encoders commonly order `meta`'s children as shown above, but this is convention, not a hard requirement; the box-walk below does **not** assume a fixed order, it scans linearly and reacts to whichever box type it sees.

**Simplification recommended (flagged, not silently assumed):** a fully spec-correct implementation would resolve the *primary item's* `ispe` via `pitm` (primary item ID) → `ipma` (item-property-association, maps item ID → property indices) → `ipco[index]`. This phase recommends a simpler, still-safe alternative: scan **all** `ispe` boxes found under `ipco` and take the **maximum** `width*height` across them. Rationale: real encoders always make the primary/full-resolution image's `ispe` the largest (thumbnails and auxiliary items are smaller or equal) — taking the max can only be equally or more conservative than the "correct" primary-item lookup, never less, which is the right bias for a security control. This avoids needing to also parse `pitm` version-dependent (8-bit vs 32-bit item ID) and `ipma` version/flag-dependent (1-byte vs 2-byte association entries) formats, at essentially zero risk of under-protecting. This is documented as an explicit design choice for the planner, not hidden — see Assumptions Log A1.

```go
// Source: ISO/IEC 14496-12 box structure; box hierarchy cross-verified
// against jdeng/goheif's heif/bmff.go box-walk and a real HEIC hex dump
// (cheeky4n6monkey.blogspot.com/2017/10/monkey-takes-heic.html)
func heicDimensions(b []byte) (uint32, uint32, bool) {
	var maxW, maxH uint32
	found := false
	walkBoxes(b, func(boxType string, payload []byte) bool {
		if boxType != "meta" || len(payload) < 4 {
			return true // keep scanning top-level boxes
		}
		walkBoxes(payload[4:], func(t string, p []byte) bool { // skip meta's version/flags
			if t != "iprp" {
				return true
			}
			walkBoxes(p, func(t2 string, p2 []byte) bool {
				if t2 != "ipco" {
					return true
				}
				walkBoxes(p2, func(t3 string, p3 []byte) bool {
					if t3 == "ispe" && len(p3) >= 12 {
						w := binary.BigEndian.Uint32(p3[4:8])
						h := binary.BigEndian.Uint32(p3[8:12])
						if uint64(w)*uint64(h) > uint64(maxW)*uint64(maxH) {
							maxW, maxH = w, h
						}
						found = true
					}
					return true
				})
				return false
			})
			return false
		})
		return false // stop scanning top-level boxes after meta
	})
	return maxW, maxH, found
}

// walkBoxes iterates top-level ISOBMFF boxes in buf, calling fn(type,
// payload) for each; fn returns false to stop early. Truncated/malformed
// boxes at the tail of the bounded buffer are silently treated as "nothing
// more to see" — the caller's found==false + ErrDimensionsUnknown handles
// the fail-closed behavior.
func walkBoxes(buf []byte, fn func(boxType string, payload []byte) bool) {
	i := 0
	for i+8 <= len(buf) {
		size := int(binary.BigEndian.Uint32(buf[i : i+4]))
		boxType := string(buf[i+4 : i+8])
		headerLen := 8
		if size == 1 {
			if i+16 > len(buf) {
				return
			}
			size64 := binary.BigEndian.Uint64(buf[i+8 : i+16])
			if size64 > uint64(len(buf)) {
				return
			}
			size = int(size64)
			headerLen = 16
		} else if size == 0 {
			return // "extends to EOF" — not expected here, bail conservatively
		}
		if size < headerLen || i+size > len(buf) {
			return // truncated within bounded window
		}
		if !fn(boxType, buf[i+headerLen:i+size]) {
			return
		}
		i += size
	}
}
```

### Anti-Patterns to Avoid

- **Fully buffering the upload to walk boxes/IFDs "to be safe":** defeats the entire purpose of a decompression-bomb *pre*-check — a large file (even one within `MAX_UPLOAD_BYTES`) buffered fully before validation reintroduces the exact "hold the whole thing in memory" risk this phase is meant to bound early. Always operate within the fixed `dimPeekLen` window.
- **Treating JPEG markers `0xFFC4`/`0xFFC8`/`0xFFCC` as SOF:** they fall in the `0xC0`-`0xCF` numeric range but are DHT/JPG/DAC respectively — a naive `marker >= 0xC0 && marker <= 0xCF` check without excluding these three will misparse a Huffman-table or arithmetic-coding segment's bytes as if they were the frame header, producing wrong (and unpredictable) width/height.
- **Comparing WebP's lossy FourCC against `"VP8"` (3 bytes) instead of `"VP8 "` (4 bytes, trailing space):** RIFF FourCCs are always exactly 4 ASCII bytes; the trailing space is part of the literal chunk ID and must be included in the comparison.
- **Treating PNG's chunk length field (13) as if it were the total record size:** it is only the *data* length; only relevant if walking past IHDR, which this feature never needs to do — but a bug here would silently misindex subsequent PNG chunks in a hypothetical future extension.
- **Multiplying width×height as native `int`/`int32` without checking width for overflow headroom:** always use `uint64` (or `int64` after explicit non-negative casts) for the pixel-count product — see Common Pitfalls.
- **Attempting to "seek forward" on `rest` to reach a TIFF IFD or HEIC box beyond the bounded buffer:** `rest` is a plain `io.Reader`/`io.MultiReader` chain (per D-07), not seekable; the only two options when the needed offset falls beyond `dimPeekLen` are (a) read further and grow the buffer (rejected — unbounded, defeats the point) or (b) fail closed. Always choose (b).

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Full image decoding to get dimensions | Calling `image.DecodeConfig` from stdlib `image/png`, `image/jpeg` (note: stdlib has **no** built-in webp/heic/tiff decoders anyway) | The hand-written header-only parsers in this phase | `image.DecodeConfig` for png/jpeg *would* technically work and is stdlib-only, but doesn't cover webp/heic/tiff at all (no stdlib decoders for those), so a single custom code path across all 5 formats is simpler to maintain than "stdlib for 2, custom for 3." Also, `image.DecodeConfig` for some formats still allocates more state than a raw header peek needs. |
| Binary field extraction (`Uint16`/`Uint32` BE/LE) | Manual bit-shifting by hand at every call site | `encoding/binary`'s `BigEndian`/`LittleEndian` helpers | Already used correctly is less error-prone than repeating `int(b[0])<<8|int(b[1])` inline at a dozen call sites; stdlib, zero risk. |

**Key insight:** this phase is inherently "hand-rolled parsing" by design (D-01) — the "don't hand-roll" discipline here is about not reaching for `image.DecodeConfig`/third-party decoders for the 2 formats that happen to have them (png/jpeg) while hand-rolling the other 3, which would create two different code paths and two different bounded-read disciplines for what should be one uniform mechanism.

## Common Pitfalls

### Pitfall 1: Multiplying width × height with native `int` risks silent overflow behavior differences across platforms
**What goes wrong:** `width * height` computed with Go's platform-sized `int` is fine on 64-bit builds (this project's `CGO_ENABLED=0` static binaries target amd64/arm64 per `Dockerfile.api`), but is a latent bug if ever cross-compiled to a 32-bit target, and is easy to get wrong if a reviewer assumes `int32` semantics.
**Why it happens:** TIFF's LONG type and PNG's width/height are full 32-bit unsigned values; an adversarial file could declare `width = height = 0xFFFFFFFF`. `uint32(0xFFFFFFFF) * uint32(0xFFFFFFFF)` computed in `uint64` = 18,446,744,065,119,617,025, which fits within `uint64`'s max (~1.8446744e19) but would silently wrap/misbehave if computed in 32-bit arithmetic or signed types.
**How to avoid:** Always store parsed width/height as `uint32`, and compute the pixel-count product as `uint64(width) * uint64(height)`, compared against a `uint64`-typed configured limit.
**Warning signs:** A deliberately-crafted TIFF/PNG with max-uint32 declared dimensions passing the pixel-limit check when it should be rejected — cover with a unit test asserting the overflow case is correctly rejected.

### Pitfall 2: TIFF IFD offset can legitimately point past the bounded window
**What goes wrong:** Some TIFF writers (especially streaming scanners/encoders that write image strip data before finalizing the directory) place the first IFD near the end of the file rather than right after the 8-byte header. Since `rest` is non-seekable and this phase deliberately caps the peek at `dimPeekLen`, such a file's IFD will be unreachable, and the parser must fail closed.
**Why it happens:** The TIFF spec does not mandate the IFD's position; it's purely wherever the writer's offset field points.
**How to avoid:** Explicitly check `ifdOffset + 2 > len(buf)` (and each subsequent entry offset) and return "not found" rather than attempting any read outside the captured buffer's bounds.
**Warning signs:** A legitimate TIFF from an unusual encoder (e.g., some scanner software, some GIS/geospatial TIFF writers) being rejected with "cannot determine declared dimensions" — this is an accepted, documented tradeoff (Assumptions Log A2), not a bug, but worth a clear error message so operators can diagnose it quickly if it happens in practice.

### Pitfall 3: JPEG marker scan must exclude DHT(0xC4)/JPG(0xC8)/DAC(0xCC) from the SOF range
**What goes wrong:** A naive range check `0xC0 <= marker <= 0xCF` treats DHT (Define Huffman Table, extremely common in every JPEG) as if it were a Start-Of-Frame segment, reading its Huffman table bytes as precision/height/width — silently producing wrong dimensions rather than an obvious error.
**Why it happens:** The JPEG marker numbering groups SOF and non-SOF-but-adjacent markers (DHT, JPG, DAC) into the same `0xC0`-`0xCF` byte range; only cross-referencing the actual ITU T.81 marker table reveals which specific values are excluded.
**How to avoid:** Explicit exclusion list (`!= 0xC4 && != 0xC8 && != 0xCC`) alongside the range check; test with a real JPEG (which always has at least one DHT segment) to confirm the scan correctly skips past it to reach the real SOF.
**Warning signs:** Every single real-world test JPEG "succeeding" but returning implausible width/height values (e.g., width=huffman-table-byte-garbage) — a strong signal this exclusion was missed, since DHT appears in essentially all real JPEGs before SOF.

### Pitfall 4: HEIC's "take the max ispe" simplification could, in a contrived case, under- or over-estimate the primary item's size
**What goes wrong:** If a HEIC file's largest `ispe`-bearing item is NOT the primary image (e.g., an auxiliary depth map or a bracketed-exposure companion image larger than the primary), the max-based approach would use that larger value — which is still safe (more conservative, i.e. errs toward rejecting), but could theoretically reject a file whose primary/decoded image is actually well under the limit. The reverse (primary image larger than any aux item, which is the overwhelmingly common real case) is correctly protected either way.
**Why it happens:** The simplification (Pattern 6) intentionally skips the `pitm`/`ipma` primary-item resolution for implementation simplicity (Claude's Discretion, no locked decision requiring full correctness here).
**How to avoid:** Accept this as a documented, deliberate tradeoff (security-conservative, not silently wrong) — if per-item false-positive rejections become a real operational issue, a follow-up phase can add full `ipma`-based primary-item resolution.
**Warning signs:** An operator reports a legitimate HEIC upload rejected for exceeding the pixel limit despite the primary/visible image being reasonably sized — investigate whether an auxiliary item (thumbnail should be smaller, but a depth map or bracketed frame might not be) is inflating the max.

### Pitfall 5: Re-peeking from `rest` must not skip or duplicate Sniff's original 12 bytes
**What goes wrong:** A subtly wrong implementation might try to peek fresh bytes directly from the *original* multipart file (bypassing Sniff's `rest`), which would either duplicate the first 12 bytes (if `rest` is later also read again) or skip them (if the raw file handle's read position has already advanced past them from Sniff's own `io.ReadFull`).
**Why it happens:** `Sniff` already consumed 12 bytes from the underlying `multipart.File`'s read cursor internally; the only way to get "the first 12 bytes again" is through `Sniff`'s own returned `rest` (which is `io.MultiReader(bytes.NewReader(sniffedBuf), file)`), not by reading `file` directly a second time.
**How to avoid:** `Dimensions()` must always be called with Sniff's `rest` as its `r` argument, never the original `file`/`multipart.File` handle. All byte offsets computed above (all relative to file-start, e.g. PNG width at byte 16) remain correct specifically *because* `rest`'s first 12 bytes are guaranteed to equal the original file's first 12 bytes.
**Warning signs:** Unit/integration tests using a real multipart upload (not a synthetic byte slice starting fresh) would immediately catch this if botched — recommend at least one handler-level integration test exercising the real `Sniff` → `Dimensions` → `Upload` chain end-to-end, not just isolated parser unit tests.

### Pitfall 6: `io.ReadFull`'s short-read errors must be tolerated, mirroring Sniff's own handling
**What goes wrong:** For files shorter than `dimPeekLen` (the overwhelming majority of legitimate small images), `io.ReadFull` returns `io.ErrUnexpectedEOF` (or `io.EOF` if zero bytes remain) rather than a "full" 64 KiB read — if this is treated as a hard error instead of "read what we could, parse from that," every small image would spuriously 500 instead of being correctly parsed or correctly rejected as too-short-to-determine-dimensions.
**Why it happens:** `io.ReadFull`'s documented contract distinguishes "got fewer bytes than asked, stream ended" (tolerable) from "read failed for another reason" (should propagate as an actual error, e.g. mapped to 400).
**How to avoid:** Follow `Sniff`'s existing exact pattern: `if readErr != nil && readErr != io.ErrUnexpectedEOF && readErr != io.EOF { return err }`, then proceed to parse `buf[:n]` regardless.
**Warning signs:** Any test uploading a small (sub-64KB) but otherwise valid image failing with an unexpected 500/400 instead of succeeding or correctly 422-rejecting on dimension grounds.

## Code Examples

### Handler integration point (D-06 ordering)

```go
// Source: pattern extends internal/api/handlers.go's existing handleCreateJob
// (Phase 4 content-validation block), inserted after the pair-check and
// before callback_url validation — both are "is this request acceptable"
// gates that must run before any storage/Postgres write.
if !convert.Default.Supports(detected, target) {
	writeError(w, http.StatusUnprocessableEntity,
		"unsupported conversion: "+detected+" -> "+target)
	return
}

// VALID-03: reject a decompression-bomb-shaped upload (declared pixel
// dimensions exceeding the configured limit) before any storage write.
// convert.Dimensions re-stitches its own bounded peek onto rest, so the
// full original stream still reaches s.storage.Upload below unmodified.
width, height, dimRest, err := convert.Dimensions(detected, rest)
if err != nil {
	log.Printf("content validation rejected: client_id=%s filename=%q reason=dimensions_unknown", client.ID, filename)
	writeError(w, http.StatusUnprocessableEntity,
		"cannot determine declared image dimensions for "+filename)
	return
}
rest = dimRest
totalPixels := uint64(width) * uint64(height)
if totalPixels > s.maxImagePixels {
	log.Printf("content validation rejected: client_id=%s filename=%q reason=dimension_limit width=%d height=%d limit=%d", client.ID, filename, width, height, s.maxImagePixels)
	writeError(w, http.StatusUnprocessableEntity,
		"declared image dimensions exceed configured limit")
	return
}

// callback_url is optional (per-job, D-02 Phase 5)...
callbackURL := r.FormValue(formFieldCallbackURL)
```

### Config/env wiring (mirrors existing `MAX_UPLOAD_BYTES`/`envInt64` pattern)

```go
// Source: cmd/api/main.go's existing envInt64 helper, reused verbatim.
srv := api.NewServer(jobs.NewRepo(pool), store, qc, resolver, health, api.Config{
	MaxUploadBytes:     envInt64("MAX_UPLOAD_BYTES", 100<<20),
	MaxImagePixels:     envInt64("MAX_IMAGE_PIXELS", 100_000_000), // D-05: 100 megapixels default
	IPRateLimitRPM:     int(envInt64("RATE_LIMIT_IP_RPM", 60)),
	ClientRateLimitRPM: int(envInt64("RATE_LIMIT_CLIENT_RPM", 120)),
})
```

`.env.example` documentation line (matches existing comment style):
```
MAX_IMAGE_PIXELS=100000000   # decompression-bomb guard: max declared width*height (default 100 megapixels, e.g. 10000x10000)
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|---------------|--------|
| Trusting `MAX_UPLOAD_BYTES` (file byte size) as a sufficient DoS guard for image processing | Also validating *declared pixel dimensions* independent of file byte size | Long-standing best practice for any service that decodes untrusted images (the "decompression bomb" class of vulnerability, well documented since at least the early 2000s for zip bombs and extended to image codecs) | A small (few-KB) file can declare an enormous pixel count (e.g. a highly-compressible solid-color 50000×50000 PNG), which decodes to gigabytes of raw pixel memory in the worker — `MAX_UPLOAD_BYTES` alone does not catch this class of attack. |

**Deprecated/outdated:** None — the byte-level format specs used here (PNG, TIFF, JPEG, WebP, ISOBMFF/HEIF) are all long-stable, non-deprecated container formats; no version-currency concerns apply to hand-parsing fixed binary layouts the way they would to a fast-moving library API.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | HEIC "take the max of all `ispe` boxes under `ipco`" is an acceptable simplification vs. full `pitm`/`ipma`-based primary-item resolution | Pattern 6, Pitfall 4 | Low risk of under-protection (max is always ≥ the primary item's true size in virtually all real encoders); non-zero risk of over-rejecting a rare file where an auxiliary item's declared size exceeds the primary image's — acceptable for a security-first control, but should be called out explicitly to the planner/discuss-phase as a deliberate design choice, not hidden. |
| A2 | 64 KiB is a sufficient bounded peek window for all 5 formats in the overwhelming majority of real-world files | Summary, Pattern 1, Pitfalls 2/3 | Based on one real-world HEIC sample (ispe reachable at ~1.4KB, ~45x margin) plus spec-derived reasoning for the other 4 formats (all need far less than 64KB in the typical case); NOT exhaustively tested against burst-mode/multi-item HEIC, TIFFs with end-of-file IFDs, or JPEGs with unusually large multi-segment ICC profiles. If wrong in practice, the failure mode is a false-positive 422 rejection (fail-closed), not a security gap — but could surface as unexpected rejections for a class of legitimate files the planner should be aware of and consider surfacing in error messages/logs for diagnosis. |
| A3 | TIFF's "value left-justified in the 4-byte field regardless of byte order" rule (used for SHORT-typed tags 256/257) is correctly interpreted from the spec-derived web summary rather than the primary TIFF 6.0 PDF text itself | Pattern 5 | If misinterpreted, SHORT-typed width/height tags on big-endian TIFFs specifically could be misread; low risk since the interpretation was cross-checked against a second independent source (fileformat.info/docs.fileformat.com) and is consistent with widely-implemented TIFF readers, but the planner/implementer should validate against at least one real big-endian ("MM") SHORT-typed TIFF sample during implementation testing. |

## Open Questions (RESOLVED)

1. **Exact check ordering relative to `callback_url` validation. (RESOLVED)**
   - What we know: CONTEXT.md's canonical_refs say the dimension check "lands between the pair-check and `callback_url` validation (or immediately after Sniff's mismatch checks — planner to decide exact ordering...), MUST be before `s3.Upload`."
   - What's unclear: Whether there's any operational reason to prefer one order over another (there isn't a functional difference — both are 422/400 rejections before upload).
   - Recommendation: Place it exactly where this research's Code Examples section shows (right after the `Supports` pair-check, right before `callback_url` validation) — keeps all "is this file/format acceptable" checks grouped together before the more independent callback-URL check.
   - Resolution: Orchestrator accepted the recommendation directly. Plan 07-02 implements this exact ordering (verified by gsd-plan-checker).

2. **Should `dimPeekLen` (64 KiB) be operator-configurable, or a fixed internal constant? (RESOLVED)**
   - What we know: CONTEXT.md's Claude's-Discretion note only calls out the *pixel-count limit* (`MAX_IMAGE_PIXELS`) as needing an env var; the bounded-read window size is described as an implementation detail.
   - What's unclear: Whether a future operator might need to tune the peek window (e.g., if a legitimate but unusual TIFF/JPEG workflow starts getting rejected).
   - Recommendation: Keep `dimPeekLen` as an internal, non-configurable constant (`const dimPeekLen = 64 * 1024` in `dimensions.go`) for this phase — matches `sniffLen`'s existing precedent (also a hardcoded constant, not env-configurable) and keeps scope tight; revisit only if real operational rejections at this boundary occur.
   - Resolution: Orchestrator accepted the recommendation directly. Plan 07-01 implements `dimPeekLen` as a fixed constant (verified by gsd-plan-checker).

## Environment Availability

Not applicable — this phase has no external tool/service/runtime dependencies beyond the Go standard library already used throughout the codebase. No new binaries, no new Docker image layers, no new environment variables beyond the one new `MAX_IMAGE_PIXELS` config value (which requires no external tooling to support, just `os.Getenv` parsing already used everywhere in `cmd/api/main.go`).

## Security Domain

> `security_enforcement` is not present in `.planning/config.json` — treated as enabled per protocol default.

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-------------------|
| V5 Input Validation | Yes | This phase's entire purpose — validating declared file metadata (pixel dimensions) against a resource-consumption ceiling before any expensive operation, directly extending Phase 4's magic-byte input validation. |
| V12 File Handling | Yes | ASVS V12 explicitly covers resource-exhaustion protections for file uploads including "restricting the size of files accepted" — pixel-dimension limiting is the image-specific analog to a raw byte-size limit (`MAX_UPLOAD_BYTES`, already covered) and closes the gap that byte-size alone does not bound decoded memory. |
| V4 Access Control | No (unchanged this phase) | Not touched — same auth/ownership model as before. |
| V6 Cryptography | No | No cryptographic operations in this phase. |

### Known Threat Patterns for this stack

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|-----------------------|
| Decompression bomb / oversized declared-dimension image causing worker OOM or severe latency during libvips decode | Denial of Service (resource exhaustion) | This phase's entire scope: reject at upload time based on declared (not decoded) pixel dimensions, before any conversion work begins. Was explicitly deferred in Phase 4 as D-09; closed here. |
| Malformed/adversarial binary header crafted to exploit a parsing bug (e.g., integer overflow, out-of-bounds read) in the new hand-written parsers themselves | Tampering / Denial of Service | Every parser above performs explicit bounds checks (`len(b) < N`) before every slice access, uses `uint64` arithmetic for the pixel-count product to avoid overflow, and fails closed (`ErrDimensionsUnknown`) rather than panicking or reading out-of-bounds on truncated/malformed input — this is the primary implementation risk this research flags for careful unit testing (fuzz-style truncated-input tests are recommended, see Assumptions Log A2/A3 and Pitfalls 2/3). |

## Sources

### Primary (HIGH confidence)
- W3C PNG Specification (Third Edition), IHDR chunk: `https://www.w3.org/TR/png-3/#11IHDR` — exact byte offsets for width/height fields.
- ITU-T Recommendation T.81 (JPEG), Annex B — marker segment structure, SOF marker numbering and exclusions (DHT/JPG/DAC), cross-verified via multiple independent JPEG-structure references converging on the same byte layout.
- IETF RFC 2301 / TIFF 6.0 specification (via `cool.culturalheritage.org/bytopic/imaging/std/tiff4.html`, `fileformat.info`) — byte-order header, IFD entry format, value left-justification rule for sub-4-byte types.
- IETF RFC 9649 (WebP Image Format) and Google's official WebP Container Specification (`developers.google.com/speed/webp/docs/riff_container`) and WebP Lossless Bitstream Specification (`developers.google.com/speed/webp/docs/webp_lossless_bitstream_specification`) — VP8/VP8L/VP8X exact bit-packing.
- ISO/IEC 14496-12 (ISOBMFF) box structure (size/type/extended-size rules) — general box-header format, cross-verified via the `spacestation93/heif_howto` walkthrough and `jdeng/goheif`'s actual Go implementation (`heif/bmff.go`, `heif/heif.go` — `SpatialExtentsProperty`/`ImageWidth`/`ImageHeight` fields, `pitm`/`ipma`-based property association).
- Direct repository reads: `internal/convert/sniff.go`, `internal/convert/convert.go`, `internal/convert/sniff_test.go`, `internal/api/handlers.go`, `internal/api/api.go`, `internal/api/handlers_test.go`, `cmd/api/main.go`, `.env.example`, `.planning/config.json` — exact-current-state grounding for every integration-point and convention claim above.

### Secondary (MEDIUM confidence)
- `cheeky4n6monkey.blogspot.com/2017/10/monkey-takes-heic.html` — real byte-level box-size analysis of an actual iPhone 8 Plus HEIC file, used to derive the ~1.4KB "ispe reachable at" evidence supporting the 64 KiB bounded-window recommendation. A digital-forensics blog, not a formal spec, but independently corroborates the ISOBMFF box hierarchy from the primary spec sources above with real numbers.
- Nokia HEIF Technical Information (`nokiatech.github.io/heif/technical.html`) — HEIF box/property terminology (`iprp`/`ipco`/`ispe`), same source already cited as MEDIUM confidence in Phase 4's research for HEIC brand codes.

### Tertiary (LOW confidence)
- None retained as authoritative claims — all WebSearch-only findings were cross-verified against a primary/official spec source above before being used to derive a byte offset or field layout.

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — zero new dependencies; stdlib `encoding/binary`/`bytes`/`io` usage is uncontroversial.
- Architecture: HIGH — the peek-and-restitch integration pattern directly extends `Sniff`'s already-shipped, already-tested mechanism; the exact `handleCreateJob` insertion point was verified by reading the current file, not assumed.
- Byte layouts (PNG/JPEG/WebP/TIFF): HIGH — each cross-verified against an official/authoritative spec source, not training-data recall alone.
- Byte layout (HEIC): MEDIUM-HIGH — box hierarchy and `ispe` internal layout verified against spec + an independent real Go implementation + one real-world hex-level sample; the "64 KiB is enough" bound and the "max-of-all-ispe" simplification are both reasoned/evidence-based but not exhaustively tested against the full space of real-world HEIC producers (see Assumptions Log A1/A2).

**Research date:** 2026-07-09
**Valid until:** 2026-08-08 (30 days — all cited specs are long-stable, non-versioned binary formats; no dependency-version drift risk since no new packages are introduced. Re-verify only if a real-world rejection pattern at the 64KB boundary surfaces during/after implementation, per Assumptions Log A2.)
