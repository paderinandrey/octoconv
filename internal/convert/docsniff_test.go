package convert

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"testing"
)

// mustWriteEntry adds a single deflate-compressed entry with the given
// content to zw, failing the test on any write error.
func mustWriteEntry(t *testing.T, zw *zip.Writer, name, content string) {
	t.Helper()
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
	if err != nil {
		t.Fatalf("CreateHeader(%q): %v", name, err)
	}
	if _, err := w.Write([]byte(content)); err != nil {
		t.Fatalf("Write(%q): %v", name, err)
	}
}

// ooxmlZipFixture builds a zip containing the given OOXML root part, but
// preceded by 8 filler entries (mirroring the real pandoc-produced pptx
// where [Content_Types].xml sits at central-directory index 8, not 0) --
// proving SniffContainer's root-part-presence-by-name check is
// position-independent.
func ooxmlZipFixture(t *testing.T, rootPart string) []byte {
	t.Helper()
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	mustWriteEntry(t, zw, "[Content_Types].xml", "<Types/>")
	for i := 0; i < 7; i++ {
		mustWriteEntry(t, zw, fmt.Sprintf("filler/part%d.xml", i), "filler")
	}
	mustWriteEntry(t, zw, rootPart, "root part content")
	if err := zw.Close(); err != nil {
		t.Fatalf("zw.Close: %v", err)
	}
	return buf.Bytes()
}

// odfZipFixture builds a zip whose first entry is "mimetype" (stored,
// uncompressed) carrying the given payload, per OASIS ODF v1.2 Part 3
// §17.4.
func odfZipFixture(t *testing.T, mimetype string) []byte {
	t.Helper()
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	fw, err := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		t.Fatalf("CreateHeader(mimetype): %v", err)
	}
	if _, err := fw.Write([]byte(mimetype)); err != nil {
		t.Fatalf("Write(mimetype): %v", err)
	}
	mustWriteEntry(t, zw, "META-INF/manifest.xml", "<manifest/>")
	if err := zw.Close(); err != nil {
		t.Fatalf("zw.Close: %v", err)
	}
	return buf.Bytes()
}

// odfWithBasicFixture builds an ODF-shaped zip (mimetype-at-index-0, per
// odfZipFixture) plus an additional macro-storage entry -- covers ODF's
// Basic/-prefixed macro detection (a directory-prefix match, distinct from
// OOXML's literal vbaProject.bin path match).
func odfWithBasicFixture(t *testing.T, mimetype, basicEntry string) []byte {
	t.Helper()
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	fw, err := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		t.Fatalf("CreateHeader(mimetype): %v", err)
	}
	if _, err := fw.Write([]byte(mimetype)); err != nil {
		t.Fatalf("Write(mimetype): %v", err)
	}
	mustWriteEntry(t, zw, basicEntry, "Sub Foo\nEnd Sub\n")
	if err := zw.Close(); err != nil {
		t.Fatalf("zw.Close: %v", err)
	}
	return buf.Bytes()
}

// macroZipFixture builds a minimal, valid-shaped docx (word/document.xml
// root part present) plus the given macro-part entry -- covers OOXML's
// literal vbaProject.bin path match.
func macroZipFixture(t *testing.T, macroPart string) []byte {
	t.Helper()
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	mustWriteEntry(t, zw, "word/document.xml", "root part content")
	mustWriteEntry(t, zw, macroPart, "macro content")
	if err := zw.Close(); err != nil {
		t.Fatalf("zw.Close: %v", err)
	}
	return buf.Bytes()
}

// duplicateNameZipFixture builds a zip with two entries sharing the exact
// same name -- reused for both the duplicate-root-part and the
// duplicate-macro-part scenarios, since SniffContainer's fail-closed guard
// (RESEARCH.md Pitfall 3) covers both name classes via one shared counter.
func duplicateNameZipFixture(t *testing.T, name string) []byte {
	t.Helper()
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	mustWriteEntry(t, zw, name, "first copy")
	mustWriteEntry(t, zw, name, "second copy, different content")
	if err := zw.Close(); err != nil {
		t.Fatalf("zw.Close: %v", err)
	}
	return buf.Bytes()
}

// zipBombFixture builds a single deflate-compressed entry whose declared
// UncompressedSize64 equals declaredSize, written from real (highly
// compressible, all-zero) content so archive/zip.Writer computes the size
// itself rather than trusting a caller-spoofed header field.
func zipBombFixture(t *testing.T, declaredSize uint64) []byte {
	t.Helper()
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "word/document.xml", Method: zip.Deflate})
	if err != nil {
		t.Fatalf("CreateHeader: %v", err)
	}
	chunk := make([]byte, 1<<20) // 1 MiB of zeros, reused per write
	var written uint64
	for written < declaredSize {
		n := declaredSize - written
		if n > uint64(len(chunk)) {
			n = uint64(len(chunk))
		}
		if _, err := w.Write(chunk[:n]); err != nil {
			t.Fatalf("Write: %v", err)
		}
		written += n
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zw.Close: %v", err)
	}
	return buf.Bytes()
}

func TestSniffContainer_DOCX(t *testing.T) {
	data := ooxmlZipFixture(t, "word/document.xml")
	res, err := SniffContainer(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("SniffContainer error: %v", err)
	}
	if res.Format != "docx" {
		t.Fatalf("Format = %q, want docx", res.Format)
	}
}

func TestSniffContainer_XLSX(t *testing.T) {
	data := ooxmlZipFixture(t, "xl/workbook.xml")
	res, err := SniffContainer(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("SniffContainer error: %v", err)
	}
	if res.Format != "xlsx" {
		t.Fatalf("Format = %q, want xlsx", res.Format)
	}
}

func TestSniffContainer_PPTX(t *testing.T) {
	data := ooxmlZipFixture(t, "ppt/presentation.xml")
	res, err := SniffContainer(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("SniffContainer error: %v", err)
	}
	if res.Format != "pptx" {
		t.Fatalf("Format = %q, want pptx", res.Format)
	}
}

func TestSniffContainer_ODT(t *testing.T) {
	cases := map[string]string{
		"application/vnd.oasis.opendocument.text":         "odt",
		"application/vnd.oasis.opendocument.spreadsheet":  "ods",
		"application/vnd.oasis.opendocument.presentation": "odp",
	}
	for mimetype, want := range cases {
		data := odfZipFixture(t, mimetype)
		res, err := SniffContainer(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatalf("SniffContainer error for %q: %v", mimetype, err)
		}
		if res.Format != want {
			t.Fatalf("mimetype %q: Format = %q, want %q", mimetype, res.Format, want)
		}
	}
}

func TestSniffContainer_BareZipUnrecognized(t *testing.T) {
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	mustWriteEntry(t, zw, "readme.txt", "hello")
	if err := zw.Close(); err != nil {
		t.Fatalf("zw.Close: %v", err)
	}
	data := buf.Bytes()

	res, err := SniffContainer(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("SniffContainer error: %v", err)
	}
	if res.Format != "" {
		t.Fatalf("Format = %q, want \"\" for unrecognized zip", res.Format)
	}
}

func TestSniffContainer_ZipBombDeclaredSize(t *testing.T) {
	const declared uint64 = 20 * 1024 * 1024 // 20 MiB
	data := zipBombFixture(t, declared)

	res, err := SniffContainer(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("SniffContainer error: %v", err)
	}
	if res.TotalUncompressed != declared {
		t.Fatalf("TotalUncompressed = %d, want %d", res.TotalUncompressed, declared)
	}
}

func TestSniffContainer_MacroDetectedOOXML(t *testing.T) {
	data := macroZipFixture(t, "word/vbaProject.bin")
	res, err := SniffContainer(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("SniffContainer error: %v", err)
	}
	if !res.HasMacro {
		t.Fatal("HasMacro = false, want true for OOXML vbaProject.bin")
	}
}

func TestSniffContainer_MacroDetectedODF(t *testing.T) {
	data := odfWithBasicFixture(t, "application/vnd.oasis.opendocument.text", "Basic/Standard/script-lb.xml")
	res, err := SniffContainer(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("SniffContainer error: %v", err)
	}
	if !res.HasMacro {
		t.Fatal("HasMacro = false, want true for ODF Basic/ entry")
	}
}

func TestSniffContainer_DuplicateRootPartRejected(t *testing.T) {
	data := duplicateNameZipFixture(t, "word/document.xml")
	res, err := SniffContainer(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("SniffContainer error: %v", err)
	}
	if !res.DuplicateRootPart {
		t.Fatal("DuplicateRootPart = false, want true for duplicated word/document.xml")
	}
}

func TestSniffContainer_DuplicateMacroPartRejected(t *testing.T) {
	data := duplicateNameZipFixture(t, "word/vbaProject.bin")
	res, err := SniffContainer(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("SniffContainer error: %v", err)
	}
	if !res.DuplicateRootPart {
		t.Fatal("DuplicateRootPart = false, want true for duplicated word/vbaProject.bin")
	}
}

func TestSniffContainer_NotAZip(t *testing.T) {
	data := []byte("this is definitely not a zip file, no PK signature here")
	_, err := SniffContainer(bytes.NewReader(data), int64(len(data)))
	if !errors.Is(err, ErrNotAZip) {
		t.Fatalf("err = %v, want ErrNotAZip", err)
	}
}
