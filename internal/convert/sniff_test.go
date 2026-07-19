package convert

import (
	"bytes"
	"io"
	"testing"
)

func TestSniffPNG(t *testing.T) {
	data := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52}
	detected, _, err := Sniff(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Sniff error: %v", err)
	}
	if detected != "png" {
		t.Fatalf("detected = %q, want png", detected)
	}
}

func TestSniffJPEG(t *testing.T) {
	data := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46, 0x00, 0x01}
	detected, _, err := Sniff(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Sniff error: %v", err)
	}
	if detected != "jpg" {
		t.Fatalf("detected = %q, want jpg", detected)
	}
}

func TestSniffWebP(t *testing.T) {
	data := []byte("RIFF")
	data = append(data, 0x00, 0x00, 0x00, 0x00) // arbitrary chunk size
	data = append(data, []byte("WEBP")...)
	detected, _, err := Sniff(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Sniff error: %v", err)
	}
	if detected != "webp" {
		t.Fatalf("detected = %q, want webp", detected)
	}
}

func TestSniffTIFFLittleEndian(t *testing.T) {
	data := []byte{0x49, 0x49, 0x2A, 0x00, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	detected, _, err := Sniff(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Sniff error: %v", err)
	}
	if detected != "tiff" {
		t.Fatalf("detected = %q, want tiff", detected)
	}
}

func TestSniffTIFFBigEndian(t *testing.T) {
	data := []byte{0x4D, 0x4D, 0x00, 0x2A, 0x00, 0x00, 0x00, 0x08, 0x00, 0x00, 0x00, 0x00}
	detected, _, err := Sniff(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Sniff error: %v", err)
	}
	if detected != "tiff" {
		t.Fatalf("detected = %q, want tiff", detected)
	}
}

func TestSniffHEIC(t *testing.T) {
	brands := []string{"heic", "heix", "hevc", "hevx", "mif1", "msf1"}
	for _, brand := range brands {
		data := []byte{0x00, 0x00, 0x00, 0x18}
		data = append(data, []byte("ftyp")...)
		data = append(data, []byte(brand)...)
		detected, _, err := Sniff(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("Sniff error for brand %q: %v", brand, err)
		}
		if detected != "heic" {
			t.Fatalf("brand %q: detected = %q, want heic", brand, detected)
		}
	}
}

// TestSniffHEIC_ForeignBrandNotDetected proves an ordinary MP4 video brand
// ("mp42") is never misdetected as heic, even though both formats share the
// identical ftyp+brand box shape. Since Phase 34 (34-01) added mp4/mov/avi
// to the signatures table, "mp42" is now correctly classified as mp4
// instead of going undetected -- the assertion here narrowed from "detected
// nothing at all" to "did not misdetect as heic", which is what this test
// has always actually been proving (T-34-02).
func TestSniffHEIC_ForeignBrandNotDetected(t *testing.T) {
	data := []byte{0x00, 0x00, 0x00, 0x18}
	data = append(data, []byte("ftyp")...)
	data = append(data, []byte("mp42")...) // MP4 brand, not HEIF
	detected, _, err := Sniff(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Sniff error: %v", err)
	}
	if detected == "heic" {
		t.Fatalf("detected = %q, want anything but heic for non-HEIF ftyp brand", detected)
	}
	if detected != "mp4" {
		t.Fatalf("detected = %q, want mp4 (mp42 is a registered mp4VideoBrands entry)", detected)
	}
}

func TestSniffUnrecognized(t *testing.T) {
	detected, _, err := Sniff(bytes.NewReader([]byte("notanimage")))
	if err != nil {
		t.Fatalf("Sniff error: %v", err)
	}
	if detected != "" {
		t.Fatalf("detected = %q, want \"\" for unrecognized content", detected)
	}
}

func TestSniffShortInputNoPanic(t *testing.T) {
	detected, rest, err := Sniff(bytes.NewReader([]byte{0x89, 0x50}))
	if err != nil {
		t.Fatalf("Sniff error: %v", err)
	}
	if detected != "" {
		t.Fatalf("detected = %q, want \"\" for short unmatched input", detected)
	}
	if rest == nil {
		t.Fatal("rest must not be nil even for short input")
	}
}

func TestSniffPreservesFullStream(t *testing.T) {
	data := []byte("RIFF")
	data = append(data, 0x00, 0x00, 0x00, 0x00)
	data = append(data, []byte("WEBP")...)
	data = append(data, []byte("VP8 extra payload bytes after the header")...)

	detected, rest, err := Sniff(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Sniff error: %v", err)
	}
	if detected != "webp" {
		t.Fatalf("detected = %q, want webp", detected)
	}

	got, err := io.ReadAll(rest)
	if err != nil {
		t.Fatalf("ReadAll(rest) error: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("rest stream = %q, want full original bytes %q", got, data)
	}
}

func TestMIMEType(t *testing.T) {
	cases := map[string]string{
		"png":     "image/png",
		"jpg":     "image/jpeg",
		"jpeg":    "image/jpeg",
		"webp":    "image/webp",
		"heic":    "image/heic",
		"tiff":    "image/tiff",
		"pdf":     "application/pdf",
		"docx":    "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"xlsx":    "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		"pptx":    "application/vnd.openxmlformats-officedocument.presentationml.presentation",
		"odt":     "application/vnd.oasis.opendocument.text",
		"ods":     "application/vnd.oasis.opendocument.spreadsheet",
		"odp":     "application/vnd.oasis.opendocument.presentation",
		"html":    "text/html",
		"unknown": "application/octet-stream",
	}
	for format, want := range cases {
		if got := MIMEType(format); got != want {
			t.Errorf("MIMEType(%q) = %q, want %q", format, got, want)
		}
	}
}
