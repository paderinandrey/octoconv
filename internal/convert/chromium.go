package convert

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// ChromiumConverter renders HTML to PDF via chromium-headless-shell's
// one-shot --print-to-pdf mode (D-01). It is the third engine class
// (EngineHTML), after image (libvips) and document (LibreOffice).
type ChromiumConverter struct{}

// Pairs returns the single supported {html, pdf} pair -- no cross-pairs
// exist for this engine (D-06/HTML-03 scope).
func (ChromiumConverter) Pairs() []Pair {
	return []Pair{{From: "html", To: "pdf"}}
}

// Engine reports the html engine class (D-01).
func (ChromiumConverter) Engine() string { return EngineHTML }

// headCloseTag and htmlOpenPrefix are the case-insensitive markers
// injectPrintCSS searches for to place the print CSS block (RESEARCH.md
// Pattern 1).
var (
	headCloseTag   = []byte("</head>")
	htmlOpenPrefix = []byte("<html")
)

// injectPrintCSS returns a NEW copy of html with css inserted immediately
// before the last child position of <head> -- i.e. right before the first
// case-insensitive "</head>" -- so the injected rule has cascade priority
// over anything else already in <head> (RESEARCH.md Pattern 1). If no
// "</head>" is found, css is inserted immediately after the end of the
// opening "<html ...>" tag; if neither marker is found (should not happen
// once LooksLikeHTML has already gated the upload, D-07), css is prepended
// at position 0 as a last resort. html is never mutated in place -- the
// caller writes the result to a new file (rendered.html), never
// re-uploading/overwriting the original input.
func injectPrintCSS(html []byte, css string) []byte {
	lower := bytes.ToLower(html)

	if idx := bytes.Index(lower, headCloseTag); idx >= 0 {
		return spliceAt(html, idx, css)
	}
	if idx := bytes.Index(lower, htmlOpenPrefix); idx >= 0 {
		if end := bytes.IndexByte(html[idx:], '>'); end >= 0 {
			return spliceAt(html, idx+end+1, css)
		}
	}
	return spliceAt(html, 0, css)
}

// spliceAt returns a new byte slice with css inserted into html at byte
// offset at, without mutating html's underlying array.
func spliceAt(html []byte, at int, css string) []byte {
	out := make([]byte, 0, len(html)+len(css))
	out = append(out, html[:at]...)
	out = append(out, css...)
	out = append(out, html[at:]...)
	return out
}

// Convert builds a CSS-injected copy of inPath (never mutating inPath
// itself, mirroring LibreOfficeConverter's never-touch-inPath discipline)
// and invokes chromium-headless-shell's one-shot print-to-pdf mode against
// that copy, with the layered network-block flags (D-03) and JS disabled
// (D-05). ctx must carry the engine timeout.
func (ChromiumConverter) Convert(ctx context.Context, inPath, outPath string, opts map[string]any) error {
	workDir := filepath.Dir(outPath) // caller's per-job workDir; already unique, already cleaned up

	// Garbage opts (e.g. a corrupt jobs.options column) is a deterministic
	// failure, not a transient one -- HTMLOptsFromMap applies the same
	// strictness (DisallowUnknownFields + allow-list) here as at the API
	// write path (D-10), mirroring DocOptsFromMap's use in Convert.
	o, err := HTMLOptsFromMap(opts)
	if err != nil {
		return fmt.Errorf("chromium: %w", err)
	}

	input, err := os.ReadFile(inPath)
	if err != nil {
		return fmt.Errorf("chromium: read input: %w", err)
	}
	// buildPrintCSS is a server-constant string selected only by the
	// already-validated HTMLOpts fields -- never built from raw client
	// bytes (RESEARCH.md Pattern 1, Pitfall 9's lesson applied to CSS).
	rendered := injectPrintCSS(input, buildPrintCSS(o))
	renderedPath := filepath.Join(workDir, "rendered.html")
	if err := os.WriteFile(renderedPath, rendered, 0o600); err != nil {
		return fmt.Errorf("chromium: write rendered html: %w", err)
	}

	// flags verify-live-confirmed in Plan 04 smoke checklist
	args := []string{
		"--headless",
		"--disable-gpu",
		"--no-sandbox",
		"--disable-dev-shm-usage",
		"--blink-settings=scriptEnabled=false",
		"--proxy-server=127.0.0.1:9",
		"--proxy-bypass-list=<-loopback>",
		"--host-resolver-rules=MAP * ~NOTFOUND",
		"--print-to-pdf=" + outPath,
		"file://" + renderedPath, // NOT inPath -- renderedPath carries the injected print CSS
	}
	if err := runCommand(ctx, "chromium-headless-shell", args...); err != nil {
		return fmt.Errorf("chromium: %w", err)
	}

	// --print-to-pdf=<path> writes directly to outPath -- no rename step is
	// needed (unlike soffice's --outdir convention in libreoffice.go).
	// validatePDF is reused verbatim from libreoffice.go; target is always
	// "pdf" for this engine, so no new validator is needed.
	return validatePDF(outPath)
}
