package convert

import (
	"bytes"
	"io"
	"unicode/utf8"
)

// htmlSniffCap bounds how much of an uploaded file LooksLikeHTML reads into
// memory for its UTF-8/NUL/marker check (D-07). HTML uploads in this
// project's use case are small text documents; a generous cap keeps the
// check bounded (mirrors SniffContainer's "never buffer the whole upload"
// discipline, docsniff.go) while still covering any realistic HTML document
// in full. r.ReadAt is used throughout -- never a disturbed sequential
// cursor -- so an oversized upload cannot force an unbounded read here.
const htmlSniffCap = 1 << 20 // 1 MiB

// htmlDoctypeMarker and htmlTagMarker are the two case-insensitive prefixes
// (checked after stripping a leading UTF-8 BOM and whitespace) that
// identify genuine HTML text (D-07). "<html" alone is not sufficient -- it
// must be immediately followed by '>', '/', or whitespace so an unrelated
// tag name that merely starts with the same four letters (e.g. "<htmlfoo>")
// does not false-match.
var (
	htmlDoctypeMarker = []byte("<!doctype html")
	htmlTagMarker     = []byte("<html")
)

// LooksLikeHTML reports whether r's content is valid UTF-8, contains no NUL
// bytes, and begins (after leading whitespace/BOM) with an HTML marker
// (<!doctype html> or <html>, case-insensitive) -- D-07's fail-closed
// content check, run by the caller AFTER the client's declared
// source/extension already claims html (source == "html", post
// NormalizeFormat's htm alias). It never panics on empty, short, or
// oversized input.
//
// This is deliberately NOT a full HTML parser (D-07): a full DOM-tree
// parsing dependency is rejected for the same reason SniffContainer avoids
// full ZIP extraction (docsniff.go) -- strictness here would be illusory,
// since HTML famously "parses however it likes" even for malformed input,
// so a full parse buys no additional safety over a bounded marker+encoding
// check, only a new dependency.
func LooksLikeHTML(r io.ReaderAt, size int64) bool {
	if size <= 0 {
		return false
	}
	n := size
	if n > htmlSniffCap {
		n = htmlSniffCap
	}
	buf := make([]byte, n)
	read, err := r.ReadAt(buf, 0)
	if err != nil && err != io.EOF {
		return false
	}
	buf = buf[:read]
	if len(buf) == 0 {
		return false
	}

	if bytes.IndexByte(buf, 0) != -1 {
		return false
	}
	if !utf8.Valid(buf) {
		return false
	}

	buf = bytes.TrimPrefix(buf, []byte{0xEF, 0xBB, 0xBF}) // leading UTF-8 BOM
	buf = bytes.TrimLeft(buf, " \t\r\n\f\v")

	lower := bytes.ToLower(buf)
	if bytes.HasPrefix(lower, htmlDoctypeMarker) {
		return true
	}
	if bytes.HasPrefix(lower, htmlTagMarker) {
		rest := lower[len(htmlTagMarker):]
		if len(rest) == 0 {
			return false // bare "<html" with nothing after it -- not a complete tag
		}
		switch rest[0] {
		case '>', '/', ' ', '\t', '\r', '\n':
			return true
		}
	}
	return false
}
