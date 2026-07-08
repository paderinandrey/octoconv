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
// convert.Default: png, jpg, webp, heic, tiff. heic/tiff entries are added
// once tiffDimensions/heicDimensions are implemented below.
var dimensionParsers = map[string]dimensionParser{
	"png":  pngDimensions,
	"jpg":  jpegDimensions,
	"webp": webpDimensions,
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
