package convert

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

// pngFixture builds a minimal PNG signature + IHDR chunk declaring width x
// height.
func pngFixture(width, height uint32) []byte {
	data := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A} // signature
	data = append(data, 0x00, 0x00, 0x00, 0x0D)                   // chunk length = 13
	data = append(data, []byte("IHDR")...)
	data = append(data, byte(width>>24), byte(width>>16), byte(width>>8), byte(width))
	data = append(data, byte(height>>24), byte(height>>16), byte(height>>8), byte(height))
	data = append(data, 0x08, 0x02, 0x00, 0x00, 0x00) // bitdepth/colortype/compression/filter/interlace
	data = append(data, 0x00, 0x00, 0x00, 0x00)       // CRC (not validated)
	return data
}

func TestDimensionsPNG(t *testing.T) {
	data := pngFixture(100, 200)
	w, h, rest, err := Dimensions("png", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Dimensions error: %v", err)
	}
	if w != 100 || h != 200 {
		t.Fatalf("got (%d,%d), want (100,200)", w, h)
	}
	if rest == nil {
		t.Fatal("rest must not be nil")
	}
}

func TestDimensionsPNG_NotIHDR(t *testing.T) {
	data := pngFixture(100, 200)
	copy(data[12:16], []byte("IDAT"))
	_, _, _, err := Dimensions("png", bytes.NewReader(data))
	if !errors.Is(err, ErrDimensionsUnknown) {
		t.Fatalf("err = %v, want ErrDimensionsUnknown", err)
	}
}

func TestDimensionsPNG_TooShort(t *testing.T) {
	data := pngFixture(100, 200)[:20]
	_, _, _, err := Dimensions("png", bytes.NewReader(data))
	if !errors.Is(err, ErrDimensionsUnknown) {
		t.Fatalf("err = %v, want ErrDimensionsUnknown", err)
	}
}

// webpVP8XFixture builds a RIFF/WEBP/VP8X fixture declaring canvasWidth x
// canvasHeight (the actual values, not width-1/height-1 — the encoding is
// handled internally).
func webpVP8XFixture(canvasWidth, canvasHeight uint32) []byte {
	data := []byte("RIFF")
	data = append(data, 0x00, 0x00, 0x00, 0x00) // RIFF size, unused
	data = append(data, []byte("WEBP")...)
	data = append(data, []byte("VP8X")...)
	data = append(data, 0x0A, 0x00, 0x00, 0x00) // chunk size = 10
	data = append(data, 0x00, 0x00, 0x00, 0x00) // flags + reserved
	w1 := canvasWidth - 1
	h1 := canvasHeight - 1
	data = append(data, byte(w1), byte(w1>>8), byte(w1>>16))
	data = append(data, byte(h1), byte(h1>>8), byte(h1>>16))
	return data
}

func TestDimensionsWebP_VP8X(t *testing.T) {
	data := webpVP8XFixture(400, 300)
	w, h, _, err := Dimensions("webp", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Dimensions error: %v", err)
	}
	if w != 400 || h != 300 {
		t.Fatalf("got (%d,%d), want (400,300)", w, h)
	}
}

func webpVP8LFixture(width, height uint32) []byte {
	data := []byte("RIFF")
	data = append(data, 0x00, 0x00, 0x00, 0x00)
	data = append(data, []byte("WEBP")...)
	data = append(data, []byte("VP8L")...)
	data = append(data, 0x00, 0x00, 0x00, 0x00) // chunk size, unused
	data = append(data, 0x2F)                   // VP8L signature
	bits := ((width - 1) & 0x3FFF) | (((height - 1) & 0x3FFF) << 14)
	data = append(data, byte(bits), byte(bits>>8), byte(bits>>16), byte(bits>>24))
	return data
}

func TestDimensionsWebP_VP8L(t *testing.T) {
	data := webpVP8LFixture(1024, 768)
	w, h, _, err := Dimensions("webp", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Dimensions error: %v", err)
	}
	if w != 1024 || h != 768 {
		t.Fatalf("got (%d,%d), want (1024,768)", w, h)
	}
}

func webpVP8Fixture(width, height uint32) []byte {
	data := []byte("RIFF")
	data = append(data, 0x00, 0x00, 0x00, 0x00)
	data = append(data, []byte("WEBP")...)
	data = append(data, []byte("VP8 ")...)
	data = append(data, 0x00, 0x00, 0x00, 0x00) // chunk size, unused
	data = append(data, 0x00, 0x00, 0x00)       // 3-byte frame tag
	data = append(data, 0x9D, 0x01, 0x2A)       // start code
	data = append(data, byte(width), byte(width>>8))
	data = append(data, byte(height), byte(height>>8))
	return data
}

func TestDimensionsWebP_VP8(t *testing.T) {
	data := webpVP8Fixture(640, 480)
	w, h, _, err := Dimensions("webp", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Dimensions error: %v", err)
	}
	if w != 640 || h != 480 {
		t.Fatalf("got (%d,%d), want (640,480)", w, h)
	}
}

func TestDimensionsWebP_VP8_NoTrailingSpaceFourCCNotMatched(t *testing.T) {
	// A 3-byte "VP8" (no trailing space) FourCC must not be treated as the
	// simple-lossy sub-format; the 4th byte here is 'L' so bytes 12:16
	// spell "VP8L" only if intentionally sliced that way — construct a
	// buffer where bytes 12:15 are "VP8" but byte 15 is something else
	// entirely (not a valid FourCC at all) to prove no false-positive match.
	data := []byte("RIFF")
	data = append(data, 0x00, 0x00, 0x00, 0x00)
	data = append(data, []byte("WEBP")...)
	data = append(data, []byte("VP8?")...) // not VP8X, "VP8 ", nor VP8L
	data = append(data, 0x00, 0x00, 0x00, 0x00)
	data = append(data, 0x00, 0x00, 0x00, 0x9D, 0x01, 0x2A, 0x80, 0x02, 0xE0, 0x01)
	_, _, _, err := Dimensions("webp", bytes.NewReader(data))
	if !errors.Is(err, ErrDimensionsUnknown) {
		t.Fatalf("err = %v, want ErrDimensionsUnknown for non-matching FourCC", err)
	}
}

// jpegFixtureWithDHT builds SOI + DHT + APP0 + SOF0 declaring width x height,
// proving DHT/APPn segments are correctly skipped.
func jpegFixtureWithDHT(width, height uint16) []byte {
	data := []byte{0xFF, 0xD8} // SOI

	// DHT segment (0xFFC4): length covers itself + a small fake table.
	dht := []byte{0xFF, 0xC4, 0x00, 0x05, 0xAA, 0xBB, 0xCC}
	data = append(data, dht...)

	// APP0/JFIF segment.
	app0 := []byte{0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00, 0x01, 0x01, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00}
	data = append(data, app0...)

	// SOF0 segment (0xFFC0): length(2)=17, precision(1)=8, height(2), width(2), etc.
	sof := []byte{0xFF, 0xC0, 0x00, 0x11, 0x08}
	sof = append(sof, byte(height>>8), byte(height))
	sof = append(sof, byte(width>>8), byte(width))
	sof = append(sof, 0x03, 0x01, 0x11, 0x00, 0x02, 0x11, 0x01, 0x03, 0x11, 0x01) // component data padding to segLen
	data = append(data, sof...)

	return data
}

func TestDimensionsJPEG(t *testing.T) {
	data := jpegFixtureWithDHT(400, 300)
	w, h, _, err := Dimensions("jpg", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Dimensions error: %v", err)
	}
	if w != 400 || h != 300 {
		t.Fatalf("got (%d,%d), want (400,300) -- DHT bytes must not be misread as SOF", w, h)
	}
}

func TestDimensionsJPEG_DHTMarkersExcludedFromSOFRange(t *testing.T) {
	// Directly exercise jpegDimensions with markers 0xC4/0xC8/0xCC present
	// but no true SOF marker anywhere: must fail closed, not misparse DHT.
	data := []byte{0xFF, 0xD8}
	data = append(data, 0xFF, 0xC4, 0x00, 0x04, 0x00, 0x00) // DHT
	data = append(data, 0xFF, 0xC8, 0x00, 0x04, 0x00, 0x00) // JPG (reserved)
	data = append(data, 0xFF, 0xCC, 0x00, 0x04, 0x00, 0x00) // DAC
	data = append(data, 0xFF, 0xD9)                         // EOI, no SOF ever seen
	_, _, ok := (dimensionParser(jpegDimensions))(data)
	if ok {
		t.Fatal("jpegDimensions must not treat DHT/JPG/DAC as SOF")
	}
}

func TestDimensionsPreservesFullStream(t *testing.T) {
	data := pngFixture(50, 60)
	data = append(data, []byte("trailing IDAT payload bytes...")...)

	_, _, rest, err := Dimensions("png", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Dimensions error: %v", err)
	}
	got, err := io.ReadAll(rest)
	if err != nil {
		t.Fatalf("ReadAll(rest) error: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("rest stream mismatch: got %d bytes, want %d bytes", len(got), len(data))
	}
}

func TestDimensionsShortInputNoPanic(t *testing.T) {
	w, h, rest, err := Dimensions("png", bytes.NewReader([]byte{0x89, 0x50}))
	if !errors.Is(err, ErrDimensionsUnknown) {
		t.Fatalf("err = %v, want ErrDimensionsUnknown", err)
	}
	if w != 0 || h != 0 {
		t.Fatalf("got (%d,%d), want (0,0)", w, h)
	}
	if rest == nil {
		t.Fatal("rest must not be nil even for short input")
	}
}

func TestDimensionsUnregisteredFormat(t *testing.T) {
	_, _, _, err := Dimensions("bmp", bytes.NewReader([]byte("whatever")))
	if !errors.Is(err, ErrDimensionsUnknown) {
		t.Fatalf("err = %v, want ErrDimensionsUnknown for unregistered format", err)
	}
}
