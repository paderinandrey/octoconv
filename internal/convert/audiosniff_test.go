package convert

import (
	"bytes"
	"io"
	"os"
	"testing"
)

// --- matchWAV ---

func TestMatchWAV(t *testing.T) {
	data := []byte("RIFF")
	data = append(data, 0x00, 0x00, 0x00, 0x00) // arbitrary chunk size
	data = append(data, []byte("WAVE")...)
	if !matchWAV(data) {
		t.Fatal("matchWAV = false, want true for RIFF/WAVE")
	}
}

func TestMatchWAV_RejectsWebP(t *testing.T) {
	data := []byte("RIFF")
	data = append(data, 0x00, 0x00, 0x00, 0x00)
	data = append(data, []byte("WEBP")...)
	if matchWAV(data) {
		t.Fatal("matchWAV = true for RIFF/WEBP, want false")
	}
}

// --- matchOGG ---

func TestMatchOGG(t *testing.T) {
	data := []byte("OggS")
	data = append(data, 0x00, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00) // arbitrary page header padding
	if !matchOGG(data) {
		t.Fatal("matchOGG = false, want true for OggS-prefixed buffer")
	}
}

func TestMatchOGG_RejectsOther(t *testing.T) {
	if matchOGG([]byte("notanogg")) {
		t.Fatal("matchOGG = true for non-OggS content, want false")
	}
}

// --- matchM4A ---

func TestMatchM4A(t *testing.T) {
	for _, brand := range []string{"M4A ", "M4B ", "isom", "mp42"} {
		data := []byte{0x00, 0x00, 0x00, 0x1c}
		data = append(data, []byte("ftyp")...)
		data = append(data, []byte(brand)...)
		if !matchM4A(data) {
			t.Fatalf("matchM4A(brand=%q) = false, want true", brand)
		}
	}
}

func TestMatchM4A_ForeignBrandNotDetected(t *testing.T) {
	for _, brand := range []string{"qt  ", "mp41"} {
		data := []byte{0x00, 0x00, 0x00, 0x1c}
		data = append(data, []byte("ftyp")...)
		data = append(data, []byte(brand)...)
		if matchM4A(data) {
			t.Fatalf("matchM4A(brand=%q) = true, want false (not allowlisted)", brand)
		}
	}
}

func TestMatchM4A_BareFtypNoBrandBytes(t *testing.T) {
	// "ftyp" present at offset 4-8 but the buffer is truncated before any
	// brand bytes at offset 8-12 -- must not panic and must fail closed.
	data := []byte{0x00, 0x00, 0x00, 0x1c}
	data = append(data, []byte("ftyp")...)
	if matchM4A(data) {
		t.Fatal("matchM4A with no brand bytes = true, want false")
	}
}

// --- matchMP3 ---

// TestMatchMP3_ID3Tagged proves the common real-world case: an mp3 produced
// by ffmpeg's default ID3v2 tagging is detected via the synchsafe-size skip,
// not just a bare frame-sync-at-0 check.
func TestMatchMP3_ID3Tagged(t *testing.T) {
	data, err := os.ReadFile("testdata/audio/sample-id3.mp3")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if !matchMP3(data) {
		t.Fatal("matchMP3(sample-id3.mp3) = false, want true (ID3v2-tagged mp3 is the common case)")
	}
}

// TestMatchMP3_Bare proves the untagged case: frame-sync sits at offset 0
// with no ID3v2 header at all.
func TestMatchMP3_Bare(t *testing.T) {
	data := []byte{0xFF, 0xFB, 0x90, 0x00, 0x00, 0x00, 0x00, 0x00}
	if !matchMP3(data) {
		t.Fatal("matchMP3(bare frame-sync at 0) = false, want true")
	}
}

// TestMatchMP3_FooterFlag proves the ID3v2 footer-present flag (byte 5,
// bit 0x10) correctly adds 10 extra bytes before the frame sync. Without
// accounting for the footer, tagEnd would land 10 bytes short of the real
// frame-sync word and the same bytes would NOT be detected as mp3.
func TestMatchMP3_FooterFlag(t *testing.T) {
	const size = 20 // synchsafe-encodable in the low 7 bits of byte 9 alone
	data := make([]byte, 0, 64)
	data = append(data, []byte("ID3")...)
	data = append(data, 0x04, 0x00) // version 2.4.0
	data = append(data, 0x10)       // flags: footer-present (0x10) set
	data = append(data, 0x00, 0x00, 0x00, byte(size))
	data = append(data, bytes.Repeat([]byte{0xAA}, size)...) // tag data (10..30)
	data = append(data, bytes.Repeat([]byte{0xBB}, 10)...)   // 10-byte footer (30..40)
	data = append(data, 0xFF, 0xF0)                          // frame sync at tagEnd=10+20+10=40

	if !matchMP3(data) {
		t.Fatal("matchMP3(footer-flag fixture) = false, want true when +10 footer bytes are accounted for")
	}

	// Sanity: byte 30 (tagEnd WITHOUT the footer adjustment) is not a frame
	// sync -- proves this fixture actually exercises the footer-flag branch
	// rather than accidentally passing for an unrelated reason.
	if data[30] == 0xFF && data[31]&0xE0 == 0xE0 {
		t.Fatal("fixture invalid: frame sync accidentally present at the non-footer-adjusted offset")
	}
}

// TestMatchMP3_TruncatedID3Header_FailsClosed proves a buffer that starts
// with "ID3" but is shorter than the 10-byte fixed header is rejected, not
// indexed out of bounds.
func TestMatchMP3_TruncatedID3Header_FailsClosed(t *testing.T) {
	data := []byte("ID3\x04\x00\x00") // 6 bytes: "ID3" + ver + flags, no size field
	if matchMP3(data) {
		t.Fatal("matchMP3(truncated ID3 header) = true, want false (fail closed)")
	}
}

// TestMatchMP3_OversizedDeclaredSize_FailsClosed proves a crafted synchsafe
// size whose computed tagEnd pushes past the available buffer is rejected
// rather than indexed out of bounds or grown into.
func TestMatchMP3_OversizedDeclaredSize_FailsClosed(t *testing.T) {
	data := []byte{}
	data = append(data, []byte("ID3")...)
	data = append(data, 0x04, 0x00) // version
	data = append(data, 0x00)       // flags: no footer
	// Declare the maximum synchsafe size (0x0FFFFFFF), far beyond any
	// buffer this test supplies.
	data = append(data, 0x7F, 0x7F, 0x7F, 0x7F)
	data = append(data, 0xFF, 0xFB, 0x90, 0x00) // a handful of trailing bytes

	if matchMP3(data) {
		t.Fatal("matchMP3(oversized declared synchsafe size) = true, want false (fail closed, never grow/seek)")
	}
}

func TestMatchMP3_RejectsNonMP3(t *testing.T) {
	if matchMP3([]byte("not an mp3 at all")) {
		t.Fatal("matchMP3(garbage) = true, want false")
	}
}

// --- SniffAudio ---

func TestSniffAudio_RealFixtures(t *testing.T) {
	cases := map[string]string{
		"testdata/audio/sample.wav":     "wav",
		"testdata/audio/sample-id3.mp3": "mp3",
		"testdata/audio/sample.m4a":     "m4a",
	}
	for path, want := range cases {
		f, err := os.Open(path)
		if err != nil {
			t.Fatalf("open %s: %v", path, err)
		}
		detected, _, err := SniffAudio(f)
		f.Close()
		if err != nil {
			t.Fatalf("SniffAudio(%s) error: %v", path, err)
		}
		if detected != want {
			t.Fatalf("SniffAudio(%s) = %q, want %q", path, detected, want)
		}
	}
}

func TestSniffAudio_Bare_MP3(t *testing.T) {
	data := []byte{0xFF, 0xFB, 0x90, 0x00, 0x00, 0x00, 0x00, 0x00}
	detected, _, err := SniffAudio(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("SniffAudio error: %v", err)
	}
	if detected != "mp3" {
		t.Fatalf("SniffAudio(bare mp3) = %q, want mp3", detected)
	}
}

func TestSniffAudio_OGG(t *testing.T) {
	data := []byte("OggS")
	data = append(data, 0x00, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00)
	detected, _, err := SniffAudio(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("SniffAudio error: %v", err)
	}
	if detected != "ogg" {
		t.Fatalf("SniffAudio(OggS) = %q, want ogg", detected)
	}
}

func TestSniffAudio_ForeignFtypBrandNotDetected(t *testing.T) {
	data := []byte{0x00, 0x00, 0x00, 0x1c}
	data = append(data, []byte("ftyp")...)
	data = append(data, []byte("qt  ")...)
	detected, _, err := SniffAudio(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("SniffAudio error: %v", err)
	}
	if detected != "" {
		t.Fatalf("SniffAudio(qt brand) = %q, want \"\" (not m4a)", detected)
	}
}

func TestSniffAudio_Unrecognized(t *testing.T) {
	detected, _, err := SniffAudio(bytes.NewReader([]byte("not audio at all, just text")))
	if err != nil {
		t.Fatalf("SniffAudio error: %v", err)
	}
	if detected != "" {
		t.Fatalf("SniffAudio(garbage) = %q, want \"\"", detected)
	}
}

func TestSniffAudio_PreservesFullStream(t *testing.T) {
	data := []byte("RIFF")
	data = append(data, 0x00, 0x00, 0x00, 0x00)
	data = append(data, []byte("WAVE")...)
	data = append(data, bytes.Repeat([]byte{0x01, 0x02}, 100)...)

	detected, rest, err := SniffAudio(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("SniffAudio error: %v", err)
	}
	if detected != "wav" {
		t.Fatalf("detected = %q, want wav", detected)
	}
	got, err := io.ReadAll(rest)
	if err != nil {
		t.Fatalf("ReadAll(rest) error: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("rest stream does not preserve the full original bytes")
	}
}

func TestSniffAudio_ShortInputNoPanic(t *testing.T) {
	detected, rest, err := SniffAudio(bytes.NewReader([]byte{0x49, 0x44}))
	if err != nil {
		t.Fatalf("SniffAudio error: %v", err)
	}
	if detected != "" {
		t.Fatalf("detected = %q, want \"\" for short unmatched input", detected)
	}
	if rest == nil {
		t.Fatal("rest must not be nil even for short input")
	}
}

// TestSniffAudio_OversizedID3SizeFailsClosed exercises the fail-closed
// bound through the full SniffAudio path (not just matchMP3 directly): a
// declared synchsafe size larger than what SniffAudio actually peeked must
// not misdetect as mp3.
func TestSniffAudio_OversizedID3SizeFailsClosed(t *testing.T) {
	data := []byte{}
	data = append(data, []byte("ID3")...)
	data = append(data, 0x04, 0x00, 0x00)
	data = append(data, 0x7F, 0x7F, 0x7F, 0x7F) // max synchsafe size
	data = append(data, 0xFF, 0xFB, 0x90, 0x00)

	detected, _, err := SniffAudio(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("SniffAudio error: %v", err)
	}
	if detected != "" {
		t.Fatalf("SniffAudio(oversized declared ID3 size) = %q, want \"\" (fail closed)", detected)
	}
}
