package convert

import "bytes"

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
