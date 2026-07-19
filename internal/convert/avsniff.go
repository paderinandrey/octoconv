package convert

import (
	"bytes"
	"io"
)

// mp4VideoBrands is the closed major-brand allowlist for ordinary MP4
// *video* content (bytes 8-12 of the ftyp box). It MUST stay disjoint from
// m4aBrands (audiosniff.go) and heicBrands (sniff.go) -- all three share the
// identical ftyp+brand box shape, so an overlapping brand would make a
// single real-world file match more than one engine class (T-34-02).
// "qt  " is deliberately EXCLUDED here: it is QuickTime's own major brand,
// routed to matchMOV instead, never folded into this table.
var mp4VideoBrands = map[string]bool{
	"isom": true,
	"mp41": true, "mp42": true, "mp4v": true, "avc1": true,
	"iso2": true, "iso3": true, "iso4": true, "iso5": true,
	"iso6": true, "iso7": true, "iso8": true, "iso9": true,
	"3gp4": true, "3gp5": true, "3g2a": true, "dash": true,
}

// matchMP4 mirrors matchHEIC's exact ftyp+brand-table shape (sniff.go),
// checked against the closed mp4VideoBrands allowlist above. It returns
// false for anything shorter than the fixed 12-byte window, for a
// non-"ftyp" box, and for any brand not on the allowlist (including m4a's
// "M4A "/"M4B " and heic's brands, which use the identical box shape).
func matchMP4(b []byte) bool {
	if len(b) < 12 || !bytes.Equal(b[4:8], []byte("ftyp")) {
		return false
	}
	return mp4VideoBrands[string(b[8:12])]
}

// matchMOV checks for QuickTime's own major brand "qt  " (two trailing
// spaces) at the same ftyp+brand offset matchMP4/matchHEIC use. QuickTime
// content is never folded into mp4VideoBrands -- it gets its own detected
// format so callers can distinguish the two containers.
func matchMOV(b []byte) bool {
	if len(b) < 12 || !bytes.Equal(b[4:8], []byte("ftyp")) {
		return false
	}
	return string(b[8:12]) == "qt  "
}

// matchAVI mirrors matchWAV's exact RIFF-container shape (audiosniff.go),
// checking the form-type field ("AVI ") rather than just the "RIFF" prefix
// -- otherwise a WAV file (form-type "WAVE" at the identical offset) would
// misdetect as AVI.
func matchAVI(b []byte) bool {
	return len(b) >= 12 && bytes.Equal(b[0:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("AVI "))
}

// avPeekLen is the bounded prefix size SniffVideo/matchEBML read looking for
// the EBML header's DocType element. Live-verified real mkv/webm headers
// (EBMLVersion/EBMLReadVersion/EBMLMaxIDLength/EBMLMaxSizeLength/DocType/
// DocTypeVersion/DocTypeReadVersion) total under 60 bytes; 4 KiB gives
// >60x headroom while still bounding the walk -- mirrors dimPeekLen's
// generous-but-still-bounded discipline (dimensions.go). Any declared
// element size/offset that would run past this window fails closed rather
// than growing the buffer or seeking further (D-07).
const avPeekLen = 4 * 1024

// ebmlMagic is the fixed 4-byte magic shared by both mkv and webm -- it
// alone cannot disambiguate the two formats (see matchEBML).
var ebmlMagic = []byte{0x1A, 0x45, 0xDF, 0xA3}

// ebmlDocTypeID is the well-known EBML element ID for the DocType element
// (RFC 8794 §11.2.4), the only reliable disambiguator between mkv
// ("matroska") and webm ("webm") -- both share the identical EBML magic.
const ebmlDocTypeID = 0x4282

// vintLen returns the byte-length of an EBML variable-length integer given
// its first byte, determined by the position of the leading set bit
// (RFC 8794 §4). Returns 0 for 0x00, which cannot start any valid EBML
// vint -- callers must treat that as a fail-closed signal, not a length.
func vintLen(first byte) int {
	for i := 0; i < 8; i++ {
		if first&(0x80>>uint(i)) != 0 {
			return i + 1
		}
	}
	return 0
}

// readSizeVint decodes an EBML SIZE vint at buf[pos:], masking off the
// length-marker bit(s) in the first byte to produce the numeric value
// (RFC 8794 §4). Fails closed (ok=false) the instant the vint's declared
// length would run past the bounded buffer -- never grows the buffer or
// seeks further, mirroring matchMP3's fail-closed discipline
// (audiosniff.go).
func readSizeVint(buf []byte, pos int) (value uint64, length int, ok bool) {
	if pos >= len(buf) {
		return 0, 0, false
	}
	n := vintLen(buf[pos])
	if n == 0 || pos+n > len(buf) {
		return 0, 0, false
	}
	value = uint64(buf[pos]) &^ (0xFF << uint(8-n))
	for i := 1; i < n; i++ {
		value = value<<8 | uint64(buf[pos+i])
	}
	return value, n, true
}

// readIDVint decodes an EBML ELEMENT ID vint at buf[pos:] WITHOUT masking
// the length-marker bit(s) -- unlike a SIZE vint, the marker bits are part
// of an ID's own value (RFC 8794 §5; live-verified: bytes 0x42 0x86
// together equal the well-known EBMLVersion ID 0x4286). Fails closed the
// instant the declared length would run past the bounded buffer, or would
// exceed the 4-byte maximum EBML ID width.
func readIDVint(buf []byte, pos int) (id uint32, length int, ok bool) {
	if pos >= len(buf) {
		return 0, 0, false
	}
	n := vintLen(buf[pos])
	if n == 0 || n > 4 || pos+n > len(buf) {
		return 0, 0, false
	}
	var v uint32
	for i := 0; i < n; i++ {
		v = v<<8 | uint32(buf[pos+i])
	}
	return v, n, true
}

// matchEBML walks a bounded prefix of buf looking for the EBML header's
// DocType element and returns ("mkv", true) for DocType "matroska",
// ("webm", true) for DocType "webm", or ("", false) for anything else --
// including a non-EBML buffer, a truncated header, a declared element
// size/offset that would run past the bounded avPeekLen window, or a
// DocType value that is neither "matroska" nor "webm". This is a genuinely
// new algorithm (unlike matchMP4/matchMOV/matchAVI's fixed-offset shape):
// both mkv and webm share the identical 4-byte EBML magic, and the
// disambiguating DocType element sits at a variable offset determined by
// however many optional preceding elements a given encoder emits -- see
// Anti-Pattern 1 in 34-RESEARCH.md for why a fixed-offset matcher cannot
// work here. Every declared size/offset check fails closed the instant it
// would exceed what has actually been peeked; matchEBML never guesses.
func matchEBML(buf []byte) (format string, ok bool) {
	if len(buf) < 5 || !bytes.Equal(buf[:4], ebmlMagic) {
		return "", false
	}
	headerSize, sizeLen, ok := readSizeVint(buf, 4)
	if !ok {
		return "", false
	}
	pos := 4 + sizeLen
	end := pos + int(headerSize)
	if end > len(buf) {
		end = len(buf) // bounded peek: never trust a declared size past what we actually have
	}
	for pos < end {
		id, idLen, ok := readIDVint(buf, pos)
		if !ok {
			return "", false
		}
		pos += idLen
		size, sizeLen, ok := readSizeVint(buf, pos)
		if !ok {
			return "", false
		}
		pos += sizeLen
		if pos+int(size) > len(buf) {
			return "", false // declared element size runs past bounded window: fail closed
		}
		if id == ebmlDocTypeID {
			switch string(buf[pos : pos+int(size)]) {
			case "matroska":
				return "mkv", true
			case "webm":
				return "webm", true
			default:
				return "", false // unrecognized DocType value: fail closed, never guess
			}
		}
		pos += int(size)
	}
	return "", false // DocType not found within the bounded window: fail closed
}

// SniffVideo peeks at up to avPeekLen bytes of r to identify mkv/webm
// content via matchEBML, mirroring SniffAudio's io.ReadFull +
// io.MultiReader re-stitch shape (audiosniff.go). mp4/mov/avi are NOT
// re-checked here -- they fit sniff.go's existing fixed-12-byte-window
// Sniff()/signatures table exactly and are handled there instead. rest
// yields the FULL original stream so callers can still upload the intact
// file (D-07); unrecognized content returns ("", rest, nil).
func SniffVideo(r io.Reader) (detected string, rest io.Reader, err error) {
	buf := make([]byte, avPeekLen)
	n, readErr := io.ReadFull(r, buf)
	if readErr != nil && readErr != io.ErrUnexpectedEOF && readErr != io.EOF {
		return "", nil, readErr
	}
	buf = buf[:n]
	rest = io.MultiReader(bytes.NewReader(buf), r)

	if format, ok := matchEBML(buf); ok {
		return NormalizeFormat(format), rest, nil
	}
	return "", rest, nil
}
