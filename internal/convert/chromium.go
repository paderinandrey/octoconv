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

// cspNoScriptMeta is a server-constant meta tag (never client-controlled)
// that disables script execution via a strict Content-Security-Policy
// (D-05). Plan 04's live smoke checklist found that chromium-headless-shell
// 150.0.7871.100's launch-time --blink-settings=scriptEnabled=false flag
// makes the one-shot --print-to-pdf/--dump-dom command handler silently
// produce NO output at all (exit 0, no file written) -- confirmed
// reproducible on both a script-bearing and a script-free fixture, so this
// is not "JS-heavy pages fail," it is a hard incompatibility between that
// flag and the one-shot command handler in this build. The documented
// alternative flag, --disable-javascript, was also live-tested and found to
// be a no-op (a <script>document.write(...)</script> fixture still executed
// and its output appeared in --dump-dom's DOM). This CSP meta tag, injected
// into the SAME worker-built copy of the HTML that already carries the
// print CSS (Pattern 1's injection point), was live-verified to block
// script execution (document.write's effect was absent from --dump-dom
// output) while leaving --print-to-pdf fully functional -- it is the
// mechanism this converter uses to satisfy D-05, in place of the
// launch-time flag RESEARCH.md originally specified.
const cspNoScriptMeta = `<meta http-equiv="Content-Security-Policy" content="script-src 'none'; object-src 'none'">`

// headCloseTag and htmlOpenPrefix are the case-insensitive markers
// injectPrintCSS searches for to place the print CSS block (RESEARCH.md
// Pattern 1).
var (
	headCloseTag   = []byte("</head>")
	headOpenPrefix = []byte("<head")
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

// injectCSPFirst returns a NEW copy of html with csp inserted as the FIRST
// child of <head> -- immediately after the opening "<head ...>" tag -- so
// the CSP is parsed before any <script> that also lives in <head> (CR-01).
// A <meta> CSP only governs content that appears AFTER it in source order;
// injecting it right before </head> (as injectPrintCSS does for the print
// style) would leave a head-preceding inline <script> to execute before the
// policy is installed, defeating D-05's JS-disable. The print <style> stays
// at end-of-head for cascade priority; only the CSP must lead. Fallbacks
// mirror injectPrintCSS: after the opening "<html ...>" tag, else position 0
// (LooksLikeHTML has already gated that one of these markers exists, D-07).
func injectCSPFirst(html []byte, csp string) []byte {
	lower := bytes.ToLower(html)

	if idx := bytes.Index(lower, headOpenPrefix); idx >= 0 {
		if end := bytes.IndexByte(html[idx:], '>'); end >= 0 {
			return spliceAt(html, idx+end+1, csp)
		}
	}
	if idx := bytes.Index(lower, htmlOpenPrefix); idx >= 0 {
		if end := bytes.IndexByte(html[idx:], '>'); end >= 0 {
			return spliceAt(html, idx+end+1, csp)
		}
	}
	return spliceAt(html, 0, csp)
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

// Convert builds a CSS+CSP-injected copy of inPath (never mutating inPath
// itself, mirroring LibreOfficeConverter's never-touch-inPath discipline)
// and invokes chromium-headless-shell's one-shot print-to-pdf mode against
// that copy, with the layered network-block flags (D-03) and JS disabled
// via the injected CSP meta tag (D-05, see cspNoScriptMeta). ctx must carry
// the engine timeout.
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
	// Two separate injections with distinct anchors (CR-01): the CSP meta
	// must be the FIRST child of <head> so it precedes any in-head <script>
	// (a <meta> CSP only governs content parsed after it); the print <style>
	// goes just before </head> for cascade priority. Both are server-constant
	// strings selected only by already-validated HTMLOpts fields -- never
	// built from raw client bytes (RESEARCH.md Pattern 1, Pitfall 9's lesson
	// applied to CSS). See cspNoScriptMeta's doc comment for why JS-disable
	// moved from a launch flag to this injection point (Plan 04 live finding).
	rendered := injectCSPFirst(input, cspNoScriptMeta)
	rendered = injectPrintCSS(rendered, buildPrintCSS(o))
	renderedPath := filepath.Join(workDir, "rendered.html")
	if err := os.WriteFile(renderedPath, rendered, 0o600); err != nil {
		return fmt.Errorf("chromium: write rendered html: %w", err)
	}

	// flags verify-live-confirmed in Plan 04 smoke checklist. Notably
	// ABSENT: --blink-settings=scriptEnabled=false (live-tested to make the
	// one-shot command handler silently produce no output at all -- see
	// cspNoScriptMeta) and --disable-javascript (live-tested no-op, does not
	// actually disable script execution in this build). JS-disable is
	// instead enforced via the injected CSP meta tag above. --headless is
	// live-confirmed optional-but-harmless for the standalone shell binary
	// (kept for explicitness); --no-sandbox is live-confirmed REQUIRED under
	// USER nobody (D-08) -- omitting it fails with "No usable sandbox!".
	// --no-pdf-header-footer suppresses chromium's default print
	// header/footer, which otherwise leaks the internal
	// file:///workDir/rendered.html path and generation timestamp into
	// every produced PDF (live-observed default-on behavior, not requested
	// by any HTML-03 option).
	args := []string{
		"--headless",
		"--disable-gpu",
		"--no-sandbox",
		"--disable-dev-shm-usage",
		"--no-pdf-header-footer",
		"--proxy-server=127.0.0.1:9",
		"--proxy-bypass-list=<-loopback>",
		"--host-resolver-rules=MAP * ~NOTFOUND",
		"--print-to-pdf=" + outPath,
		"file://" + renderedPath, // NOT inPath -- renderedPath carries the injected print CSS + CSP
	}
	if err := runCommand(ctx, "chromium-headless-shell", args...); err != nil {
		return fmt.Errorf("chromium: %w", err)
	}

	// --print-to-pdf=<path> writes directly to outPath -- no rename step is
	// needed (unlike soffice's --outdir convention in libreoffice.go).
	// validatePDF is reused verbatim from libreoffice.go; target is always
	// "pdf" for this engine, so no new validator is needed. Wrap in a
	// chromium: context so a failure here doesn't surface with a misleading
	// bare "libreoffice:" prefix (CR-02); the terminal-signature substrings
	// it emits ("output is empty", "output missing %PDF- magic bytes") are
	// preserved inside the wrapped message, so isHTMLTerminal still classifies
	// correctly via the shared terminalLibreOfficeSignatures list.
	if err := validatePDF(outPath); err != nil {
		return fmt.Errorf("chromium: %w", err)
	}
	return nil
}
