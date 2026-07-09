// Package convert defines the Converter abstraction, a registry of supported
// format pairs, and the concrete engine implementations (libvips for images).
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

// dimensionParser extracts declared width/height from a bounded, in-memory
// prefix of a file's bytes. ok is false when the fields cannot be located
// (truncated/malformed/unrecognized input) — callers must fail closed.
type dimensionParser func(buf []byte) (width, height uint32, ok bool)

// dimensionParsers is the hardcoded, closed dispatch table (mirrors
// sniff.go's signatures table) scoped to exactly the formats registered in
// convert.Default: png, jpg, webp, heic, tiff.
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

// HasDimensionLimit reports whether format has a registered dimension
// parser (i.e. is an image format subject to the declared-pixel-dimension
// ceiling). Document formats (docx/xlsx/pptx/odt/ods/odp) have no pixel-
// dimension concept and must skip the check entirely — this predicate is
// the scope guard for the confirmed regression (RESEARCH.md Pitfall 5),
// not a new document-specific dimension check.
func HasDimensionLimit(format string) bool {
	_, ok := dimensionParsers[NormalizeFormat(format)]
	return ok
}

// pngDimensions reads the IHDR chunk's width/height fields.
// Source: https://www.w3.org/TR/png-3/#11IHDR (IHDR chunk layout)
func pngDimensions(b []byte) (uint32, uint32, bool) {
	if len(b) < 24 || !bytes.Equal(b[12:16], []byte("IHDR")) {
		return 0, 0, false
	}
	width := binary.BigEndian.Uint32(b[16:20])
	height := binary.BigEndian.Uint32(b[20:24])
	return width, height, true
}

// webpDimensions reads the declared canvas/frame dimensions from any of
// WebP's three sub-formats (VP8X extended, "VP8 " simple lossy, VP8L simple
// lossless).
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

// jpegDimensions scans the marker segments following SOI for the first
// Start-Of-Frame marker and reads its declared height/width. DHT(0xC4),
// JPG(0xC8), and DAC(0xCC) fall in the same 0xC0-0xCF numeric range as SOF
// markers but are NOT SOF and must be explicitly excluded.
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

// tiffDimensions reads the first IFD's ImageWidth (tag 256) and ImageLength
// (tag 257) entries. Because the entire dimPeekLen-byte prefix is already
// captured into b, indexing into it at the IFD offset is safe random
// access — but if the offset (or any entry) points past the captured
// buffer, the parser fails closed rather than seeking or growing the
// buffer (D-07/Pitfall 2).
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

// heicDimensions walks the ftyp -> meta -> iprp -> ipco box chain and reads
// every ispe box's declared width/height, returning the maximum width*height
// across them. Rationale (Assumptions Log A1): resolving the *primary*
// item's ispe would require also parsing the primary-item and
// item-property-association boxes (version-dependent item-id/association
// encodings); taking the max is a documented, security-conservative
// simplification — it can only reject more, never less, than full
// primary-item resolution. Deliberately does NOT parse those boxes.
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
