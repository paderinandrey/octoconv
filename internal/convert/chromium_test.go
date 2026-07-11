package convert

import (
	"os"
	"strings"
	"testing"
)

// readSourceFile reads a .go source file in this package by name, relative
// to the package directory (the test's working directory) -- used only to
// grep the assembled argv literal without launching a live chromium binary.
func readSourceFile(name string) (string, error) {
	b, err := os.ReadFile(name)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func TestChromiumConverterPairs(t *testing.T) {
	got := ChromiumConverter{}.Pairs()
	want := []Pair{{From: "html", To: "pdf"}}
	if len(got) != len(want) || got[0] != want[0] {
		t.Errorf("Pairs() = %+v, want %+v", got, want)
	}
}

func TestChromiumConverterEngine(t *testing.T) {
	if got := (ChromiumConverter{}).Engine(); got != EngineHTML {
		t.Errorf("Engine() = %q, want %q", got, EngineHTML)
	}
}

func TestChromiumRegisteredInDefault(t *testing.T) {
	engine, ok := Default.EngineFor("html", "pdf")
	if !ok {
		t.Fatal("EngineFor(html, pdf) ok=false, want true after ChromiumConverter registration")
	}
	if engine != EngineHTML {
		t.Errorf("EngineFor(html, pdf) engine = %q, want %q", engine, EngineHTML)
	}
}

func TestInjectPrintCSSWithHeadClose(t *testing.T) {
	original := []byte("<!doctype html>\n<html>\n<head>\n<title>x</title>\n</head>\n<body>hi</body>\n</html>")
	originalCopy := append([]byte(nil), original...)
	css := "<style>@page { size: A4 !important; }</style>"

	got := injectPrintCSS(original, css)

	if !strings.Contains(string(got), css) {
		t.Fatalf("injectPrintCSS output does not contain the CSS block: %s", got)
	}
	headCloseIdx := strings.Index(string(got), "</head>")
	cssIdx := strings.Index(string(got), css)
	if cssIdx < 0 || headCloseIdx < 0 || cssIdx > headCloseIdx {
		t.Errorf("CSS block not placed immediately before </head>: css at %d, </head> at %d", cssIdx, headCloseIdx)
	}
	// inPath content must never be mutated.
	if string(original) != string(originalCopy) {
		t.Error("injectPrintCSS mutated its input html slice in place")
	}
}

func TestInjectPrintCSSCaseInsensitiveHeadClose(t *testing.T) {
	original := []byte("<!DOCTYPE HTML><HTML><HEAD><TITLE>x</TITLE></HEAD><BODY>hi</BODY></HTML>")
	css := "<style>marker</style>"
	got := injectPrintCSS(original, css)
	if !strings.Contains(string(got), css) {
		t.Fatalf("injectPrintCSS output does not contain the CSS block: %s", got)
	}
	// The css must land before the (case-insensitive) closing head tag.
	lower := strings.ToLower(string(got))
	headCloseIdx := strings.Index(lower, "</head>")
	cssIdx := strings.Index(string(got), css)
	if cssIdx < 0 || headCloseIdx < 0 || cssIdx > headCloseIdx {
		t.Errorf("CSS block not placed before case-insensitive </head>: css at %d, </head> at %d", cssIdx, headCloseIdx)
	}
}

func TestInjectPrintCSSNoHeadCloseFallsBackToAfterHTMLOpen(t *testing.T) {
	original := []byte("<!doctype html>\n<html lang=\"en\">\n<body>hi</body>\n</html>")
	originalCopy := append([]byte(nil), original...)
	css := "<style>marker</style>"

	got := injectPrintCSS(original, css)

	if !strings.Contains(string(got), css) {
		t.Fatalf("injectPrintCSS output does not contain the CSS block: %s", got)
	}
	htmlOpenEnd := strings.Index(string(got), "<html lang=\"en\">") + len("<html lang=\"en\">")
	cssIdx := strings.Index(string(got), css)
	if cssIdx != htmlOpenEnd {
		t.Errorf("CSS block not placed immediately after opening <html> tag: css at %d, want %d", cssIdx, htmlOpenEnd)
	}
	if string(original) != string(originalCopy) {
		t.Error("injectPrintCSS mutated its input html slice in place")
	}
}

func TestInjectPrintCSSNoMarkerAtAllPrependsAtStart(t *testing.T) {
	original := []byte("not really html content")
	css := "<style>marker</style>"
	got := injectPrintCSS(original, css)
	if !strings.HasPrefix(string(got), css) {
		t.Errorf("injectPrintCSS with no marker should prepend css, got: %s", got)
	}
}

func TestChromiumArgvContainsRequiredFlags(t *testing.T) {
	// This mirrors the exact argv Convert assembles (RESEARCH.md Code
	// Examples), asserted directly against the source so no live chromium
	// invocation is required for this check.
	data, err := readSourceFile("chromium.go")
	if err != nil {
		t.Fatalf("read chromium.go: %v", err)
	}
	required := []string{
		`"--proxy-server=127.0.0.1:9"`,
		`"--proxy-bypass-list=<-loopback>"`,
		`"--host-resolver-rules=MAP * ~NOTFOUND"`,
		`"--no-sandbox"`,
		`"--disable-dev-shm-usage"`,
		`"--no-pdf-header-footer"`,
		`"file://" + renderedPath`,
	}
	for _, want := range required {
		if !strings.Contains(data, want) {
			t.Errorf("chromium.go argv missing required flag/expression: %s", want)
		}
	}
	// --blink-settings=scriptEnabled=false must NOT be in argv (Plan 04 live
	// finding: it makes the one-shot command handler silently produce no
	// output at all in chromium-headless-shell 150.0.7871.100). JS-disable
	// moved to the injected CSP meta tag (cspNoScriptMeta) instead.
	if strings.Contains(data, `"--blink-settings=scriptEnabled=false"`) {
		t.Error("chromium.go argv still contains --blink-settings=scriptEnabled=false, live-tested to break --print-to-pdf entirely (Plan 04)")
	}
	// --disable-javascript must NOT be relied on either -- live-tested no-op
	// (does not actually disable script execution in this build).
	if strings.Contains(data, `"--disable-javascript"`) {
		t.Error("chromium.go argv contains --disable-javascript, live-tested as a no-op that does not disable JS (Plan 04)")
	}
}

func TestChromiumInjectsCSPNoScriptMeta(t *testing.T) {
	data, err := readSourceFile("chromium.go")
	if err != nil {
		t.Fatalf("read chromium.go: %v", err)
	}
	if !strings.Contains(data, "cspNoScriptMeta") {
		t.Error("chromium.go no longer references cspNoScriptMeta -- D-05's JS-disable mechanism must be injected via the same CSS injection point")
	}
	if !strings.Contains(cspNoScriptMeta, `script-src 'none'`) {
		t.Errorf("cspNoScriptMeta = %q, want a Content-Security-Policy with script-src 'none'", cspNoScriptMeta)
	}
}

func TestChromiumDoesNotReimplementValidatePDF(t *testing.T) {
	data, err := readSourceFile("chromium.go")
	if err != nil {
		t.Fatalf("read chromium.go: %v", err)
	}
	if strings.Contains(data, "func validatePDF") {
		t.Error("chromium.go re-implements validatePDF; it must reuse libreoffice.go's implementation verbatim")
	}
	if !strings.Contains(data, "validatePDF(outPath)") {
		t.Error("chromium.go does not call validatePDF(outPath)")
	}
}
