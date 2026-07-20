package convert

import (
	"bytes"
	"io"
)

// sniffLen is the number of leading bytes Sniff peeks at. 12 covers every
// signature in the table below, including WebP's "RIFF"+size+"WEBP" (offset
// 8-11) and HEIC's "ftyp"+brand (offset 4-11).
//
// Audio formats (mp3/wav/m4a/ogg) are NOT registered here -- mp3's ID3v2 tag
// has a variable, declared length that can run well past this fixed window,
// so audio content is detected via the separate SniffAudio (audiosniff.go).
const sniffLen = 12

// heicBrands are the ISOBMFF major/compatible brands that identify HEIF/HEIC
// content. A "ftyp" box alone is not sufficient — MP4, MOV, and other ISOBMFF
// containers share the same box structure and must NOT be misdetected as heic.
var heicBrands = map[string]bool{
	"heic": true,
	"heix": true,
	"hevc": true,
	"hevx": true,
	"mif1": true,
	"msf1": true,
}

// signature pairs a registered format with a matcher over the peeked prefix.
type signature struct {
	format string
	match  func(buf []byte) bool
}

// signatures is the hardcoded, closed detection table (D-03). It covers the
// image formats registered in convert.Default (imageFormats in libvips.go:
// png, jpg, webp, heic, tiff) PLUS the fixed-offset video containers mp4,
// mov and avi.
//
// The video entries are deliberately live AHEAD of engine registration
// (WR-11, 34-REVIEW.md): AVConverter is built and tested in Phase 34 but not
// registered into convert.Default until Phase 35, so an mp4 upload is
// detected here and then rejected one step later at pair validation. mkv and
// webm are NOT in this table -- they need a variable-offset EBML walk
// (SniffVideo, avsniff.go) rather than a fixed 12-byte window, and that
// matcher is likewise not wired into the upload path until Phase 35. Both
// halves are fail-closed in the interim; keep this comment in sync when
// Phase 35 registers the engine.
var signatures = []signature{
	{"png", matchPNG},
	{"jpg", matchJPEG},
	{"webp", matchWebP},
	{"heic", matchHEIC},
	{"tiff", matchTIFF},
	{"mp4", matchMP4},
	{"mov", matchMOV},
	{"avi", matchAVI},
}

func matchPNG(b []byte) bool {
	sig := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	return len(b) >= len(sig) && bytes.Equal(b[:len(sig)], sig)
}

func matchJPEG(b []byte) bool {
	sig := []byte{0xFF, 0xD8, 0xFF}
	return len(b) >= len(sig) && bytes.Equal(b[:len(sig)], sig)
}

func matchWebP(b []byte) bool {
	return len(b) >= 12 && bytes.Equal(b[0:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP"))
}

func matchTIFF(b []byte) bool {
	le := []byte{0x49, 0x49, 0x2A, 0x00}
	be := []byte{0x4D, 0x4D, 0x00, 0x2A}
	return len(b) >= 4 && (bytes.Equal(b[:4], le) || bytes.Equal(b[:4], be))
}

func matchHEIC(b []byte) bool {
	if len(b) < 12 || !bytes.Equal(b[4:8], []byte("ftyp")) {
		return false
	}
	return heicBrands[string(b[8:12])]
}

// Sniff peeks at up to sniffLen bytes of r to identify the actual content
// format by magic bytes, independent of any filename/extension/Content-Type
// header. It returns the normalized detected format (via NormalizeFormat), or
// "" when no known signature matches (D-02 — unrecognized content). rest is
// an io.Reader that yields the FULL original stream (the peeked prefix
// re-stitched onto the remainder via io.MultiReader) so callers can still
// upload the intact file — Sniff never buffers the whole upload into memory
// (D-07). A short input (fewer than sniffLen bytes) is not an error: it
// simply fails to match some or all signatures.
func Sniff(r io.Reader) (detected string, rest io.Reader, err error) {
	buf := make([]byte, sniffLen)
	n, readErr := io.ReadFull(r, buf)
	if readErr != nil && readErr != io.ErrUnexpectedEOF && readErr != io.EOF {
		return "", nil, readErr
	}
	buf = buf[:n]
	rest = io.MultiReader(bytes.NewReader(buf), r)

	for _, sig := range signatures {
		if sig.match(buf) {
			return NormalizeFormat(sig.format), rest, nil
		}
	}
	return "", rest, nil
}

// MIMEType returns the canonical MIME type for a registered format, or
// "application/octet-stream" for anything unrecognized. This is the single
// home for format->MIME mapping shared by internal/api (stored Content-Type,
// D-06) and internal/worker (output Content-Type). Covers image formats
// (libvips), document formats + their pdf conversion target (LibreOffice),
// html + its pdf conversion target (chromium), and the audio engine's four
// input formats plus its four transcription output targets (whisper,
// AUD-02) -- so every job type is served with the same Content-Type
// correctness guarantee.
func MIMEType(format string) string {
	switch NormalizeFormat(format) {
	case "png":
		return "image/png"
	case "jpg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	case "heic":
		return "image/heic"
	case "tiff":
		return "image/tiff"
	case "pdf":
		return "application/pdf"
	case "docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case "xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case "pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	case "odt":
		return "application/vnd.oasis.opendocument.text"
	case "ods":
		return "application/vnd.oasis.opendocument.spreadsheet"
	case "odp":
		return "application/vnd.oasis.opendocument.presentation"
	case "html":
		return "text/html"
	case "mp3":
		return "audio/mpeg"
	case "wav":
		return "audio/wav"
	case "m4a":
		return "audio/mp4"
	case "ogg":
		return "audio/ogg"
	case "txt":
		return "text/plain"
	case "srt":
		return "application/x-subrip"
	case "vtt":
		return "text/vtt"
	case "json":
		return "application/json"
	case "mp4":
		return "video/mp4"
	case "mov":
		return "video/quicktime"
	case "avi":
		return "video/x-msvideo"
	case "mkv":
		return "video/x-matroska"
	case "webm":
		return "video/webm"
	default:
		return "application/octet-stream"
	}
}
