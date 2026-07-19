package convert

import (
	"bytes"
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
