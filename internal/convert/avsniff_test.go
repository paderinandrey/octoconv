package convert

import (
	"bytes"
	"io"
	"testing"
)

// --- matchMP4 ---

func TestMatchMP4(t *testing.T) {
	for _, brand := range []string{"isom", "mp41", "mp42", "mp4v", "avc1", "dash"} {
		data := []byte{0x00, 0x00, 0x00, 0x20}
		data = append(data, []byte("ftyp")...)
		data = append(data, []byte(brand)...)
		if !matchMP4(data) {
			t.Fatalf("matchMP4(brand=%q) = false, want true", brand)
		}
	}
}

func TestMatchMP4_RejectsShort(t *testing.T) {
	if matchMP4([]byte{0x00, 0x00, 0x00, 0x20, 'f', 't', 'y', 'p'}) {
		t.Fatal("matchMP4(short buffer, no brand bytes) = true, want false")
	}
}

func TestMatchMP4_RejectsWAV(t *testing.T) {
	data := []byte("RIFF")
	data = append(data, 0x00, 0x00, 0x00, 0x00)
	data = append(data, []byte("WAVE")...)
	if matchMP4(data) {
		t.Fatal("matchMP4(RIFF/WAVE) = true, want false")
	}
}

func TestMatchMP4_RejectsM4ABrand(t *testing.T) {
	data := []byte{0x00, 0x00, 0x00, 0x1c}
	data = append(data, []byte("ftyp")...)
	data = append(data, []byte("M4A ")...)
	if matchMP4(data) {
		t.Fatal("matchMP4(M4A brand) = true, want false (belongs to audio engine)")
	}
}

func TestMatchMP4_RejectsHEICBrand(t *testing.T) {
	data := []byte{0x00, 0x00, 0x00, 0x1c}
	data = append(data, []byte("ftyp")...)
	data = append(data, []byte("heic")...)
	if matchMP4(data) {
		t.Fatal("matchMP4(heic brand) = true, want false (belongs to image engine)")
	}
}

func TestMatchMP4_RejectsQuickTimeBrand(t *testing.T) {
	data := []byte{0x00, 0x00, 0x00, 0x14}
	data = append(data, []byte("ftyp")...)
	data = append(data, []byte("qt  ")...)
	if matchMP4(data) {
		t.Fatal("matchMP4(qt brand) = true, want false (routed to matchMOV instead)")
	}
}

// --- matchMOV ---

func TestMatchMOV(t *testing.T) {
	data := []byte{0x00, 0x00, 0x00, 0x14}
	data = append(data, []byte("ftyp")...)
	data = append(data, []byte("qt  ")...)
	if !matchMOV(data) {
		t.Fatal("matchMOV(qt brand) = false, want true")
	}
}

func TestMatchMOV_RejectsMP4Brand(t *testing.T) {
	data := []byte{0x00, 0x00, 0x00, 0x20}
	data = append(data, []byte("ftyp")...)
	data = append(data, []byte("isom")...)
	if matchMOV(data) {
		t.Fatal("matchMOV(isom brand) = true, want false")
	}
}

func TestMatchMOV_RejectsShort(t *testing.T) {
	if matchMOV([]byte{0x00, 0x00, 0x00, 0x14, 'f', 't', 'y', 'p'}) {
		t.Fatal("matchMOV(short buffer) = true, want false")
	}
}

// --- matchAVI ---

func TestMatchAVI(t *testing.T) {
	data := []byte("RIFF")
	data = append(data, 0xce, 0x99, 0x00, 0x00)
	data = append(data, []byte("AVI ")...)
	if !matchAVI(data) {
		t.Fatal("matchAVI(RIFF/AVI ) = false, want true")
	}
}

func TestMatchAVI_RejectsWAV(t *testing.T) {
	data := []byte("RIFF")
	data = append(data, 0x00, 0x00, 0x00, 0x00)
	data = append(data, []byte("WAVE")...)
	if matchAVI(data) {
		t.Fatal("matchAVI(RIFF/WAVE) = true, want false (WAV form-type, not AVI)")
	}
}

func TestMatchAVI_RejectsShort(t *testing.T) {
	if matchAVI([]byte("RIFF")) {
		t.Fatal("matchAVI(short buffer) = true, want false")
	}
}

// --- Cross-engine brand disjointness (T-34-02) ---

// TestVideoBrandDisjointness asserts mp4VideoBrands (avsniff.go), m4aBrands
// (audiosniff.go), and heicBrands (sniff.go) are pairwise disjoint. All
// three tables key an identical ftyp+brand box shape (bytes 4-8 "ftyp",
// bytes 8-12 the major brand); an overlapping entry would make a single
// real-world file match more than one engine class's sniffer.
func TestVideoBrandDisjointness(t *testing.T) {
	tables := map[string]map[string]bool{
		"mp4VideoBrands": mp4VideoBrands,
		"m4aBrands":      m4aBrands,
		"heicBrands":     heicBrands,
	}
	names := []string{"mp4VideoBrands", "m4aBrands", "heicBrands"}
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			a, b := names[i], names[j]
			for brand := range tables[a] {
				if tables[b][brand] {
					t.Fatalf("brand %q present in both %s and %s, want disjoint", brand, a, b)
				}
			}
		}
	}
}

// --- Sniff() integration: mp4/mov/avi routed through the signatures table ---

func TestSniff_MP4(t *testing.T) {
	data := []byte{0x00, 0x00, 0x00, 0x20}
	data = append(data, []byte("ftyp")...)
	data = append(data, []byte("isom")...)
	data = append(data, 0x00, 0x00, 0x02, 0x00)
	detected, _, err := Sniff(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Sniff error: %v", err)
	}
	if detected != "mp4" {
		t.Fatalf("Sniff(mp4 fixture) = %q, want mp4", detected)
	}
}

func TestSniff_MOV(t *testing.T) {
	data := []byte{0x00, 0x00, 0x00, 0x14}
	data = append(data, []byte("ftyp")...)
	data = append(data, []byte("qt  ")...)
	data = append(data, 0x00, 0x00, 0x02, 0x00)
	detected, _, err := Sniff(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Sniff error: %v", err)
	}
	if detected != "mov" {
		t.Fatalf("Sniff(mov fixture) = %q, want mov", detected)
	}
}

func TestSniff_AVI(t *testing.T) {
	data := []byte("RIFF")
	data = append(data, 0xce, 0x99, 0x00, 0x00)
	data = append(data, []byte("AVI ")...)
	detected, _, err := Sniff(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Sniff error: %v", err)
	}
	if detected != "avi" {
		t.Fatalf("Sniff(avi fixture) = %q, want avi", detected)
	}
}

// --- MIMEType video/* cases ---

func TestMIMEType_Video(t *testing.T) {
	cases := map[string]string{
		"mp4":  "video/mp4",
		"mov":  "video/quicktime",
		"avi":  "video/x-msvideo",
		"mkv":  "video/x-matroska",
		"webm": "video/webm",
	}
	for format, want := range cases {
		if got := MIMEType(format); got != want {
			t.Fatalf("MIMEType(%q) = %q, want %q", format, got, want)
		}
	}
}

// --- EBML/DocType bounded-peek parser (matchEBML) ---

// buildEBMLHeader constructs a byte-exact EBML header fixture matching the
// live-verified layout in 34-RESEARCH.md Pattern 3: magic, a 1-byte master
// SIZE vint, four fixed 4-byte elements (EBMLVersion/EBMLReadVersion/
// EBMLMaxIDLength/EBMLMaxSizeLength), the DocType element (variable-length
// value), then two more fixed 4-byte elements (DocTypeVersion/
// DocTypeReadVersion). docType must be shorter than 16 bytes so its SIZE
// vint fits in a single byte (0x80|len).
func buildEBMLHeader(docType string) []byte {
	body := []byte{}
	body = append(body, 0x42, 0x86, 0x81, 0x01) // EBMLVersion = 1
	body = append(body, 0x42, 0xf7, 0x81, 0x01) // EBMLReadVersion = 1
	body = append(body, 0x42, 0xf2, 0x81, 0x04) // EBMLMaxIDLength = 4
	body = append(body, 0x42, 0xf3, 0x81, 0x08) // EBMLMaxSizeLength = 8
	body = append(body, 0x42, 0x82, byte(0x80|len(docType)))
	body = append(body, []byte(docType)...)
	body = append(body, 0x42, 0x87, 0x81, 0x04) // DocTypeVersion = 4
	body = append(body, 0x42, 0x85, 0x81, 0x02) // DocTypeReadVersion = 2

	header := append([]byte{}, ebmlMagic...)
	header = append(header, byte(0x80|len(body))) // master-element SIZE vint
	header = append(header, body...)
	return header
}

func TestMatchEBML_MKV(t *testing.T) {
	buf := buildEBMLHeader("matroska")
	format, ok := matchEBML(buf)
	if !ok || format != "mkv" {
		t.Fatalf("matchEBML(matroska fixture) = (%q, %v), want (mkv, true)", format, ok)
	}
}

func TestMatchEBML_WebM(t *testing.T) {
	buf := buildEBMLHeader("webm")
	format, ok := matchEBML(buf)
	if !ok || format != "webm" {
		t.Fatalf("matchEBML(webm fixture) = (%q, %v), want (webm, true)", format, ok)
	}
}

func TestMatchEBML_RejectsNonEBML(t *testing.T) {
	if _, ok := matchEBML([]byte("not an ebml file at all")); ok {
		t.Fatal("matchEBML(non-EBML bytes) = true, want false")
	}
}

func TestMatchEBML_RejectsTruncated(t *testing.T) {
	full := buildEBMLHeader("matroska")
	// Truncate mid-way through the DocType element's value bytes -- past the
	// magic+size vint, but before the declared header size worth of body.
	truncated := full[:20]
	if _, ok := matchEBML(truncated); ok {
		t.Fatal("matchEBML(truncated header) = true, want false (fail closed)")
	}
}

func TestMatchEBML_RejectsUnknownDocType(t *testing.T) {
	buf := buildEBMLHeader("notaformat")
	if format, ok := matchEBML(buf); ok {
		t.Fatalf("matchEBML(unrecognized DocType) = (%q, true), want (_, false)", format)
	}
}

// TestMatchEBML_RejectsOversizedElementSize proves a declared element size
// that would run past the bounded window fails closed rather than being
// grown into or seeked past -- the DoS-defense half of T-34-03/T-34-04.
func TestMatchEBML_RejectsOversizedElementSize(t *testing.T) {
	buf := append([]byte{}, ebmlMagic...)
	buf = append(buf, 0x80|0x08) // master SIZE = 8 (small, valid header)
	// DocType element declaring a SIZE vint far larger than any bytes present.
	buf = append(buf, 0x42, 0x82, 0xBF) // SIZE = 0xBF & 0x7F = 0x3F = 63
	buf = append(buf, []byte("short")...)
	if _, ok := matchEBML(buf); ok {
		t.Fatal("matchEBML(oversized declared element size) = true, want false (fail closed)")
	}
}

// TestMatchEBML_RejectsHugeSizeVint is WR-03's regression pin: an 8-byte SIZE
// vint declaring a value >= 2^31 must fail closed and must never panic. The
// bounds checks compare in uint64 space precisely because narrowing to int
// first is implementation-defined truncation on a 32-bit build -- 0x100000000
// truncates to 0 (the check passes against an empty slice) and 0x80000000
// truncates to a NEGATIVE int (the check passes, then the slice expression
// panics on attacker-controlled input). On a 64-bit build these already
// failed closed; this pins the intent so a future edit cannot quietly
// reinstate the narrowing. audioduration.go:21-31 documents the same
// platform-dependent numeric-conversion hazard class.
func TestMatchEBML_RejectsHugeSizeVint(t *testing.T) {
	for _, size := range []uint64{0x80000000, 0x100000000, 0x00FFFFFFFFFFFFFF} {
		// 8-byte SIZE vint: length marker 0x01, then 7 value bytes.
		sizeVint := []byte{0x01}
		for shift := 48; shift >= 0; shift -= 8 {
			sizeVint = append(sizeVint, byte(size>>uint(shift)))
		}

		// A DocType element declaring the huge size.
		buf := append([]byte{}, ebmlMagic...)
		buf = append(buf, 0x80|0x20) // master SIZE = 32
		buf = append(buf, 0x42, 0x82)
		buf = append(buf, sizeVint...)
		buf = append(buf, []byte("matroska")...)
		if format, ok := matchEBML(buf); ok {
			t.Errorf("matchEBML(element size 0x%X) = (%q, true), want (_, false)", size, format)
		}

		// The master HEADER element declaring the huge size is a different
		// case: an over-large header size is clamped to the peeked window
		// (bounded scan) rather than rejected, so a DocType that genuinely
		// sits inside the peeked bytes must still be found. What must never
		// happen is a truncated `int(headerSize)` producing a zero or
		// negative scan end, which would silently abandon the walk and
		// misreport a real mkv as unrecognized.
		hdr := append([]byte{}, ebmlMagic...)
		hdr = append(hdr, sizeVint...)
		hdr = append(hdr, 0x42, 0x82, 0x88)
		hdr = append(hdr, []byte("matroska")...)
		format, ok := matchEBML(hdr)
		if !ok || format != "mkv" {
			t.Errorf("matchEBML(header size 0x%X) = (%q, %v), want (\"mkv\", true) via a clamped bounded scan", size, format, ok)
		}
	}
}

func TestVintLen(t *testing.T) {
	cases := []struct {
		first byte
		want  int
	}{
		{0x80, 1}, {0xFF, 1},
		{0x40, 2}, {0x7F, 2},
		{0x20, 3}, {0x3F, 3},
		{0x10, 4}, {0x1F, 4},
		{0x00, 0},
	}
	for _, c := range cases {
		if got := vintLen(c.first); got != c.want {
			t.Fatalf("vintLen(0x%02X) = %d, want %d", c.first, got, c.want)
		}
	}
}

// --- SniffVideo ---

func TestSniffVideo_EBML(t *testing.T) {
	cases := map[string]string{
		"matroska": "mkv",
		"webm":     "webm",
	}
	for docType, want := range cases {
		buf := buildEBMLHeader(docType)
		detected, rest, err := SniffVideo(bytes.NewReader(buf))
		if err != nil {
			t.Fatalf("SniffVideo(%s) error: %v", docType, err)
		}
		if detected != want {
			t.Fatalf("SniffVideo(%s) = %q, want %q", docType, detected, want)
		}
		got, err := io.ReadAll(rest)
		if err != nil {
			t.Fatalf("ReadAll(rest) error: %v", err)
		}
		if !bytes.Equal(got, buf) {
			t.Fatalf("SniffVideo(%s) rest stream does not preserve full original bytes", docType)
		}
	}
}

func TestSniffVideo_Unrecognized(t *testing.T) {
	detected, rest, err := SniffVideo(bytes.NewReader([]byte("not video at all, just text")))
	if err != nil {
		t.Fatalf("SniffVideo error: %v", err)
	}
	if detected != "" {
		t.Fatalf("SniffVideo(garbage) = %q, want \"\"", detected)
	}
	if rest == nil {
		t.Fatal("rest must not be nil even for unrecognized input")
	}
}
