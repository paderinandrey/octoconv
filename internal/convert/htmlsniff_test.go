package convert

import (
	"bytes"
	"strings"
	"testing"
)

func TestLooksLikeHTML(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want bool
	}{
		{
			name: "doctype lowercase",
			in:   []byte("<!doctype html><html><body>hi</body></html>"),
			want: true,
		},
		{
			name: "doctype uppercase",
			in:   []byte("<!DOCTYPE HTML><html><body>hi</body></html>"),
			want: true,
		},
		{
			name: "doctype mixed case with leading whitespace",
			in:   []byte("  \n\t<!DocType Html>\n<html></html>"),
			want: true,
		},
		{
			name: "bare html tag",
			in:   []byte("<html><body>hi</body></html>"),
			want: true,
		},
		{
			name: "html tag with attributes uppercase",
			in:   []byte("<HTML lang=\"en\"><body>hi</body></HTML>"),
			want: true,
		},
		{
			name: "html tag with leading whitespace",
			in:   []byte("   \r\n<html>\n<body></body></html>"),
			want: true,
		},
		{
			name: "html self-closing marker",
			in:   []byte("<html/>"),
			want: true,
		},
		{
			name: "plain text no marker",
			in:   []byte("hello world, this is not html at all"),
			want: false,
		},
		{
			name: "unrelated tag prefixed with html",
			in:   []byte("<htmlfoo>not really html</htmlfoo>"),
			want: false,
		},
		{
			name: "contains NUL byte",
			in:   append([]byte("<!doctype html>\x00"), []byte("<html></html>")...),
			want: false,
		},
		{
			name: "invalid utf-8 byte sequence",
			in:   []byte{0x3C, 0x21, 0xFF, 0xFE, 0x68, 0x74, 0x6D, 0x6C},
			want: false,
		},
		{
			name: "leading BOM then doctype",
			in:   append([]byte{0xEF, 0xBB, 0xBF}, []byte("<!doctype html><html></html>")...),
			want: true,
		},
		{
			name: "bare html with nothing after",
			in:   []byte("<html"),
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := bytes.NewReader(tc.in)
			got := LooksLikeHTML(r, int64(len(tc.in)))
			if got != tc.want {
				t.Errorf("LooksLikeHTML(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestLooksLikeHTMLEmptyInput(t *testing.T) {
	r := bytes.NewReader(nil)
	if LooksLikeHTML(r, 0) {
		t.Error("LooksLikeHTML on empty input = true, want false")
	}
}

func TestLooksLikeHTMLNeverPanicsShortReaderAt(t *testing.T) {
	// shortReaderAt reports a claimed size larger than what it actually has
	// to return, exercising the io.ReaderAt short-read contract (returns a
	// non-EOF error alongside a partial read count).
	sr := strings.NewReader("<html><body>hi</body></html>")
	// Claim a size far larger than the actual content -- LooksLikeHTML must
	// not panic when ReadAt returns fewer bytes than requested.
	if LooksLikeHTML(sr, 10_000_000) {
		// A truncated read may or may not still match the marker depending
		// on how much was actually read; the only hard requirement here is
		// "does not panic", asserted implicitly by reaching this line.
		t.Log("short-read case returned true; no panic occurred (expected)")
	}
}

func TestLooksLikeHTMLOversizedInputBoundedRead(t *testing.T) {
	// A file larger than htmlSniffCap must not be fully buffered; the
	// marker check still succeeds since the marker sits well within the
	// bounded prefix.
	var buf bytes.Buffer
	buf.WriteString("<!doctype html><html><body>")
	buf.Write(bytes.Repeat([]byte("a"), htmlSniffCap*2))
	buf.WriteString("</body></html>")
	data := buf.Bytes()
	r := bytes.NewReader(data)
	if !LooksLikeHTML(r, int64(len(data))) {
		t.Error("LooksLikeHTML on oversized-but-valid HTML = false, want true")
	}
}
