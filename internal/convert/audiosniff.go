package convert

import (
	"bytes"
	"io"
)

// mp3PeekLen must comfortably exceed any real-world ID3v2 tag including
// embedded album art (APIC frames commonly run tens to low hundreds of KB) --
// reuse the same bounded, fail-closed discipline as dimPeekLen (dimensions.go).
// A declared ID3v2 size that pushes tagEnd beyond this bound is REJECTED, not
// grown into -- this is a resource-exhaustion control, not just a detector
// (AUD-01/T-30-01).
const mp3PeekLen = 512 * 1024

// m4aBrands is the closed ISOBMFF major/compatible-brand allowlist that
// identifies m4a audio content. A bare "ftyp" box is not sufficient -- MP4,
// MOV, and other ISOBMFF containers share the identical box structure and
// must NOT be misdetected as m4a (T-30-04).
var m4aBrands = map[string]bool{
	"M4A ": true, // primary brand, trailing space is part of the 4-byte code
	"M4B ": true, // audiobook variant
	"isom": true, // generic ISO base media, seen as a compatible-brand entry
	"mp42": true, // MP4 v2 compatible brand, common in real-world m4a encoders
}

// matchWAV mirrors matchWebP's exact RIFF-container shape (sniff.go), just
// with the "WAVE" fourCC instead of "WEBP" at the same offset.
func matchWAV(b []byte) bool {
	return len(b) >= 12 && bytes.Equal(b[0:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WAVE"))
}

// matchOGG checks for the fixed 4-byte Ogg page-header capture pattern
// present at the start of every valid Ogg-container file (RFC 3533 §6),
// regardless of the codec carried inside (Vorbis, Opus, ...).
func matchOGG(b []byte) bool {
	sig := []byte("OggS")
	return len(b) >= len(sig) && bytes.Equal(b[:len(sig)], sig)
}

// matchM4A mirrors matchHEIC's exact ftyp+brand-table shape (sniff.go),
// checked against the closed m4aBrands allowlist above.
func matchM4A(b []byte) bool {
	if len(b) < 12 || !bytes.Equal(b[4:8], []byte("ftyp")) {
		return false
	}
	return m4aBrands[string(b[8:12])]
}

// matchMP3 detects an mp3 stream that may be preceded by an ID3v2 tag of
// variable, declared length -- the one format that cannot fit the fixed
// sniffLen=12 window sniff.go's signatures table assumes (see the doc
// comment on SniffAudio below).
//
// Algorithm (empirically verified against a real ffmpeg-produced ID3v2.4
// tag, see 30-RESEARCH.md "MP3 ID3v2 Detection"):
//  1. If the buffer starts with "ID3", decode the synchsafe size field at
//     offset 6-9 (each byte uses only its low 7 bits, by design, so the
//     size field itself can never contain a byte pattern that could be
//     confused with the frame-sync word), add 10 more bytes if the
//     footer-present flag (byte 5, bit 0x10) is set, and check for the
//     MPEG frame-sync word (0xFF + top 3 bits of the next byte all 1) at
//     the computed tagEnd. A tagEnd that pushes past the available buffer
//     fails closed -- mirrors dimensions.go's ErrDimensionsUnknown
//     philosophy for a declared value that can't be verified within the
//     bound (D-07): never grow the buffer or seek further, just reject.
//  2. Otherwise, the frame-sync word must sit at offset 0 (untagged mp3).
func matchMP3(b []byte) bool {
	if len(b) >= 3 && bytes.Equal(b[:3], []byte("ID3")) {
		if len(b) < 10 {
			return false // truncated ID3v2 fixed header: fail closed
		}
		size := int(b[6])<<21 | int(b[7])<<14 | int(b[8])<<7 | int(b[9])
		tagEnd := 10 + size
		if b[5]&0x10 != 0 { // footer-present flag
			tagEnd += 10
		}
		if tagEnd < 0 || tagEnd+1 >= len(b) {
			return false // beyond bounded peek window: fail closed, do not grow/seek
		}
		return b[tagEnd] == 0xFF && (b[tagEnd+1]&0xE0) == 0xE0
	}
	// No ID3v2 tag: frame sync must be at offset 0.
	return len(b) >= 2 && b[0] == 0xFF && (b[1]&0xE0) == 0xE0
}

// SniffAudio peeks at up to mp3PeekLen bytes of r to identify mp3/wav/m4a/ogg
// content by magic bytes, mirroring Sniff's io.ReadFull + io.MultiReader
// re-stitch shape (sniff.go) but with a much larger peek window than
// sniffLen=12, because matchMP3's ID3v2 tag can legitimately run to tens or
// low hundreds of KB (embedded album art). MP3 is deliberately NOT added to
// sniff.go's fixed-window signatures table -- that table assumes every
// matcher only needs the same small fixed prefix, which does not hold for a
// variable-offset, declared-length tag (30-RESEARCH.md "Design implication
// for sniff.go"). rest yields the FULL original stream so callers can still
// upload the intact file (D-07); unknown content returns ("", rest, nil).
func SniffAudio(r io.Reader) (detected string, rest io.Reader, err error) {
	buf := make([]byte, mp3PeekLen)
	n, readErr := io.ReadFull(r, buf)
	if readErr != nil && readErr != io.ErrUnexpectedEOF && readErr != io.EOF {
		return "", nil, readErr
	}
	buf = buf[:n]
	rest = io.MultiReader(bytes.NewReader(buf), r)

	switch {
	case matchWAV(buf):
		return NormalizeFormat("wav"), rest, nil
	case matchOGG(buf):
		return NormalizeFormat("ogg"), rest, nil
	case matchM4A(buf):
		return NormalizeFormat("m4a"), rest, nil
	case matchMP3(buf):
		return NormalizeFormat("mp3"), rest, nil
	default:
		return "", rest, nil
	}
}
