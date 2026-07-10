package convert

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFilterFor(t *testing.T) {
	cases := map[[2]string]string{
		// The 6 ->pdf pairs (unchanged filter names).
		{"docx", "pdf"}: "writer_pdf_Export",
		{"odt", "pdf"}:  "writer_pdf_Export",
		{"xlsx", "pdf"}: "calc_pdf_Export",
		{"ods", "pdf"}:  "calc_pdf_Export",
		{"pptx", "pdf"}: "impress_pdf_Export",
		{"odp", "pdf"}:  "impress_pdf_Export",

		// The 6 cross pairs: forward ("8" family filters) and reverse
		// ("... 2007 XML" family filters).
		{"docx", "odt"}: "writer8",
		{"odt", "docx"}: "MS Word 2007 XML",
		{"xlsx", "ods"}: "calc8",
		{"ods", "xlsx"}: "Calc MS Excel 2007 XML",
		{"pptx", "odp"}: "impress8",
		{"odp", "pptx"}: "Impress MS PowerPoint 2007 XML",

		// Case/alias robustness.
		{".DOCX", "ODT"}: "writer8",
		{"PPTX", ".pdf"}: "impress_pdf_Export",
	}
	for in, want := range cases {
		got, err := filterFor(in[0], in[1])
		if err != nil {
			t.Errorf("filterFor(%q, %q) unexpected error: %v", in[0], in[1], err)
			continue
		}
		if got != want {
			t.Errorf("filterFor(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}

	if got, err := filterFor("docx", "mp3"); err == nil {
		t.Errorf("filterFor(\"docx\", \"mp3\") = %q, nil, want an error", got)
	} else if got != "" {
		t.Errorf("filterFor(\"docx\", \"mp3\") returned non-empty filter %q alongside error", got)
	} else if !strings.Contains(err.Error(), "no export filter for") {
		t.Errorf("filterFor(\"docx\", \"mp3\") error = %v, want substring \"no export filter for\"", err)
	}

	if got, err := filterFor(".txt", "pdf"); err == nil {
		t.Errorf("filterFor(\".txt\", \"pdf\") = %q, nil, want an error", got)
	} else if got != "" {
		t.Errorf("filterFor(\".txt\", \"pdf\") returned non-empty filter %q alongside error", got)
	}
}

func TestValidatePDF(t *testing.T) {
	dir := t.TempDir()

	validPath := filepath.Join(dir, "valid.pdf")
	if err := os.WriteFile(validPath, []byte("%PDF-1.6\n%rest of content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validatePDF(validPath); err != nil {
		t.Errorf("validatePDF(valid) = %v, want nil", err)
	}

	emptyPath := filepath.Join(dir, "empty.pdf")
	if err := os.WriteFile(emptyPath, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validatePDF(emptyPath); err == nil {
		t.Error("validatePDF(empty) = nil, want error")
	}

	wrongPath := filepath.Join(dir, "wrong.pdf")
	if err := os.WriteFile(wrongPath, []byte("not a pdf"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validatePDF(wrongPath); err == nil {
		t.Error("validatePDF(wrong magic) = nil, want error")
	}

	// A sub-magic-length file must surface as the terminal missing-magic
	// signature, not a transient-looking "unexpected EOF" (D-04).
	tinyPath := filepath.Join(dir, "tiny.pdf")
	if err := os.WriteFile(tinyPath, []byte("%PD"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := validatePDF(tinyPath)
	if err == nil {
		t.Fatal("validatePDF(tiny) = nil, want error")
	}
	if !strings.Contains(err.Error(), "output missing %PDF- magic bytes") {
		t.Errorf("validatePDF(tiny) = %q, want the terminal missing-magic signature", err)
	}
}

// TestValidateDocumentOutput mirrors TestValidatePDF's three-case shape for
// the non-pdf branch (D-03), plus proves validateDocumentOutput(path,"pdf")
// still delegates to the %PDF- check unchanged.
func TestValidateDocumentOutput(t *testing.T) {
	dir := t.TempDir()

	// (a) a synthesized valid ODT container whose SniffContainer.Format
	// matches the requested target -> nil.
	odtPath := filepath.Join(dir, "valid.odt")
	odtData := odfZipFixture(t, "application/vnd.oasis.opendocument.text")
	if err := os.WriteFile(odtPath, odtData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateDocumentOutput(odtPath, "odt", false); err != nil {
		t.Errorf("validateDocumentOutput(valid odt, %q) = %v, want nil", "odt", err)
	}

	// (b) an empty file -> error.
	emptyPath := filepath.Join(dir, "empty.odt")
	if err := os.WriteFile(emptyPath, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateDocumentOutput(emptyPath, "odt", false); err == nil {
		t.Error("validateDocumentOutput(empty, \"odt\") = nil, want error")
	} else if !strings.Contains(err.Error(), "output is empty") {
		t.Errorf("validateDocumentOutput(empty) error = %v, want substring \"output is empty\"", err)
	}

	// (c) a valid zip whose container format does NOT match the requested
	// target (a docx container validated against target "odt") -> error
	// containing "output does not match expected container format".
	wrongPath := filepath.Join(dir, "wrong.odt")
	docxData := ooxmlZipFixture(t, "word/document.xml")
	if err := os.WriteFile(wrongPath, docxData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateDocumentOutput(wrongPath, "odt", false); err == nil {
		t.Error("validateDocumentOutput(docx-as-odt) = nil, want error")
	} else if !strings.Contains(err.Error(), "output does not match expected container format") {
		t.Errorf("validateDocumentOutput(mismatch) error = %v, want substring \"output does not match expected container format\"", err)
	}

	// validateDocumentOutput(path, "pdf") still delegates to the %PDF- check.
	pdfValidPath := filepath.Join(dir, "valid.pdf")
	if err := os.WriteFile(pdfValidPath, []byte("%PDF-1.6\n%rest of content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateDocumentOutput(pdfValidPath, "pdf", false); err != nil {
		t.Errorf("validateDocumentOutput(valid pdf, \"pdf\") = %v, want nil", err)
	}
	pdfInvalidPath := filepath.Join(dir, "invalid.pdf")
	if err := os.WriteFile(pdfInvalidPath, []byte("not a pdf"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateDocumentOutput(pdfInvalidPath, "pdf", false); err == nil {
		t.Error("validateDocumentOutput(non-pdf content, \"pdf\") = nil, want error")
	}
}

// TestPDFAFilterOptions asserts the PDF/A-2b builder forces both properties
// Pitfall 7 requires (SelectPdfVersion + EmbedStandardFonts:true), and that
// empty opts produce no filter suffix at all.
func TestPDFAFilterOptions(t *testing.T) {
	suffix, isPDFA := PDFAFilterOptions(DocOpts{PDFProfile: "pdf/a-2b"})
	if !isPDFA {
		t.Fatal("PDFAFilterOptions(pdf/a-2b) isPDFA = false, want true")
	}
	if !strings.Contains(suffix, "SelectPdfVersion") {
		t.Errorf("PDFAFilterOptions(pdf/a-2b) suffix = %q, want it to contain SelectPdfVersion", suffix)
	}
	if !strings.Contains(suffix, "EmbedStandardFonts") || !strings.Contains(suffix, "true") {
		t.Errorf("PDFAFilterOptions(pdf/a-2b) suffix = %q, want it to force EmbedStandardFonts:true (Pitfall 7)", suffix)
	}

	suffix, isPDFA = PDFAFilterOptions(DocOpts{})
	if isPDFA || suffix != "" {
		t.Errorf("PDFAFilterOptions(empty opts) = (%q, %v), want (\"\", false)", suffix, isPDFA)
	}
}

// TestDocOptsInjectionResistance is the mandatory success-criterion-1
// artifact (D-07, Pitfall 9): it proves that no adversarial byte sequence in
// a client-supplied opts JSON value can ever reach the soffice filter
// argument. Every adversarial input is either rejected outright by
// ParseDocOpts (the common case, since the allow-list is a single exact
// string), or -- if it somehow parses -- must produce a filter suffix
// byte-for-byte identical to the clean pdf/a-2b case, with none of the
// adversarial tokens present.
func TestDocOptsInjectionResistance(t *testing.T) {
	cleanSuffix, cleanIsPDFA := PDFAFilterOptions(DocOpts{PDFProfile: "pdf/a-2b"})
	if !cleanIsPDFA || cleanSuffix == "" {
		t.Fatal("clean pdf/a-2b case must produce a non-empty PDF/A suffix; test setup is broken")
	}

	adversarial := []string{
		// Attempts to break out of the JSON string value and inject a
		// second filter property directly.
		`{"pdf_profile":"pdf/a-2b\",\"EncryptFile\":true,\"x\":\""}`,
		// Attempts to smuggle a different UNO export filter name onto the
		// end of the enum value.
		`{"pdf_profile":"pdf/a-2b:calc_pdf_Export"}`,
		// A structurally valid but wholly unknown key -- must be rejected
		// by DisallowUnknownFields, not silently ignored.
		`{"EncryptFile":true}`,
		// A valid enum value alongside an unknown key -- the whole decode
		// must fail, not just the unknown key.
		`{"pdf_profile":"pdf/a-2b","EncryptFile":true}`,
		// Case-variant of the only allowed value -- must NOT be treated as
		// equivalent to the exact allow-listed string.
		`{"pdf_profile":"PDF/A-2B"}`,
	}

	for _, raw := range adversarial {
		t.Run(raw, func(t *testing.T) {
			opts, err := ParseDocOpts([]byte(raw))
			if err != nil {
				// Rejected before ever reaching the builder: client bytes
				// never got a chance to influence the argv/filter string.
				return
			}
			suffix, isPDFA := PDFAFilterOptions(opts)
			if suffix != cleanSuffix || isPDFA != cleanIsPDFA {
				t.Errorf("adversarial opts %q parsed successfully and produced a DIFFERENT filter suffix (%q, %v) than the clean pdf/a-2b case (%q, %v) -- this proves client bytes reached the soffice filter argument", raw, suffix, isPDFA, cleanSuffix, cleanIsPDFA)
			}
			for _, token := range []string{"EncryptFile", "calc_pdf_Export"} {
				if strings.Contains(suffix, token) {
					t.Errorf("adversarial token %q leaked into filter suffix %q -- this proves client bytes reached the soffice filter argument", token, suffix)
				}
			}
		})
	}
}

// TestConvertRejectsPDFAOnNonPDFTarget proves the "PDF/A suffix only ever
// rides on a pdf export filter" invariant is enforced where the argv is
// built, not only at the API layer (review WR-03): a persisted pdf_profile
// on a non-pdf target (DB corruption, manual insert, a future write path)
// must fail with the terminal-classified error, never reach soffice, and
// never be reported "done" with the archival profile silently unhonored.
// The guard fires before runCommand, so no soffice binary is needed.
func TestConvertRejectsPDFAOnNonPDFTarget(t *testing.T) {
	dir := t.TempDir()
	inPath := filepath.Join(dir, "in.docx")
	if err := os.WriteFile(inPath, []byte("stub"), 0o600); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, "out.odt")

	err := LibreOfficeConverter{}.Convert(context.Background(), inPath, outPath, map[string]any{"pdf_profile": "pdf/a-2b"})
	if err == nil {
		t.Fatal("Convert(pdf_profile on docx->odt) = nil, want error")
	}
	// The exact substring is coupled into terminalLibreOfficeSignatures
	// (internal/worker/worker.go) -- a retry can never fix this.
	if !strings.Contains(err.Error(), "pdf_profile requested for non-pdf target") {
		t.Errorf("Convert error = %q, want it to contain the terminal signature %q", err, "pdf_profile requested for non-pdf target")
	}
	// The docx->pdf + pdf_profile positive path (guard must NOT fire) is
	// covered by the live acceptance run against a real soffice (Plan
	// 14-03); invoking Convert here would shell out when soffice is on
	// PATH, so it is deliberately not exercised in this hermetic test.
}

// TestParseDocOptsStrictness proves the parser rejects the laxities
// json.Decoder.Decode alone would accept (review WR-01): trailing data after
// the first JSON value (a smuggled second object or plain garbage), a
// non-object top-level value (`null` decodes as a valid zero struct), and
// duplicate keys (silent "last wins" would resurrect a rejected value) --
// while valid inputs keep parsing exactly as before.
func TestParseDocOptsStrictness(t *testing.T) {
	rejected := []string{
		// Trailing second object -- Decode would silently drop it.
		`{"pdf_profile":"pdf/a-2b"}{"EncryptFile":true}`,
		// Trailing garbage bytes.
		`{"pdf_profile":"pdf/a-2b"} garbage`,
		// Non-object top-level values -- `null` previously yielded a valid
		// zero DocOpts.
		`null`,
		`"pdf/a-2b"`,
		`[]`,
		// Duplicate key, both orderings -- "last wins" must never resurrect
		// a value the allow-list rejected.
		`{"pdf_profile":"evil","pdf_profile":"pdf/a-2b"}`,
		`{"pdf_profile":"pdf/a-2b","pdf_profile":"evil"}`,
	}
	for _, raw := range rejected {
		if _, err := ParseDocOpts([]byte(raw)); err == nil {
			t.Errorf("ParseDocOpts(%q) = nil error, want rejection", raw)
		}
	}

	// Valid inputs are unaffected by the strictness checks.
	o, err := ParseDocOpts([]byte(`{"pdf_profile":"pdf/a-2b"}`))
	if err != nil || o.PDFProfile != pdfProfileA2b {
		t.Errorf("ParseDocOpts(valid profile) = (%+v, %v), want pdf/a-2b, nil", o, err)
	}
	o, err = ParseDocOpts([]byte(`{}`))
	if err != nil || o.PDFProfile != "" {
		t.Errorf("ParseDocOpts({}) = (%+v, %v), want zero DocOpts, nil", o, err)
	}
	o, err = ParseDocOpts([]byte(" {\n  \"pdf_profile\": \"pdf/a-2b\"\n } \n"))
	if err != nil || o.PDFProfile != pdfProfileA2b {
		t.Errorf("ParseDocOpts(whitespace-padded valid profile) = (%+v, %v), want pdf/a-2b, nil", o, err)
	}
}

// TestValidatePDFAOutputIntent proves D-05/D-06: a produced PDF that carries
// the /GTS_PDFA OutputIntent marker passes validateDocumentOutput when PDF/A
// was requested; one that lacks it fails with an "OutputIntent"-substring
// error (the terminal signature worker.go matches on); and when PDF/A was
// NOT requested (wantPDFA=false), the marker's presence/absence is ignored
// entirely (regression safety for plain document->pdf conversions).
func TestValidatePDFAOutputIntent(t *testing.T) {
	dir := t.TempDir()

	withMarker := filepath.Join(dir, "with-marker.pdf")
	pdfWithMarker := []byte("%PDF-1.6\n%some pdf bytes /GTS_PDFA1 more bytes\n%%EOF")
	if err := os.WriteFile(withMarker, pdfWithMarker, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateDocumentOutput(withMarker, "pdf", true); err != nil {
		t.Errorf("validateDocumentOutput(marker present, wantPDFA=true) = %v, want nil", err)
	}

	withoutMarker := filepath.Join(dir, "without-marker.pdf")
	pdfWithoutMarker := []byte("%PDF-1.6\n%plain pdf bytes, no marker\n%%EOF")
	if err := os.WriteFile(withoutMarker, pdfWithoutMarker, 0o600); err != nil {
		t.Fatal(err)
	}
	err := validateDocumentOutput(withoutMarker, "pdf", true)
	if err == nil {
		t.Fatal("validateDocumentOutput(marker absent, wantPDFA=true) = nil, want error")
	}
	if !strings.Contains(err.Error(), "OutputIntent") {
		t.Errorf("validateDocumentOutput(marker absent, wantPDFA=true) error = %v, want substring \"OutputIntent\"", err)
	}

	if err := validateDocumentOutput(withoutMarker, "pdf", false); err != nil {
		t.Errorf("validateDocumentOutput(marker absent, wantPDFA=false) = %v, want nil (marker ignored)", err)
	}
}

func TestRegistryLibreOfficePairs(t *testing.T) {
	// Default registry is populated by converters.go init().
	if !Default.Supports("docx", "pdf") {
		t.Error("expected docx->pdf to be supported")
	}
	if !Default.Supports("DOCX", ".pdf") {
		t.Error("expected DOCX->.pdf (aliased/cased) to be supported")
	}
	for _, from := range []string{"odt", "xlsx", "ods", "pptx", "odp"} {
		if !Default.Supports(from, "pdf") {
			t.Errorf("expected %s->pdf to be supported", from)
		}
	}
	if Default.Supports("pdf", "docx") {
		t.Error("pdf->docx must not be supported (one-directional only)")
	}
	if Default.Supports("docx", "docx") {
		t.Error("identity pair docx->docx must not be registered")
	}
	if _, ok := Default.Lookup("odp", "pdf"); !ok {
		t.Error("expected a converter for odp->pdf")
	}

	// D-01: the 6 cross pairs are supported, including a cased/aliased
	// variant.
	crossCases := [][2]string{
		{"docx", "odt"}, {"odt", "docx"},
		{"xlsx", "ods"}, {"ods", "xlsx"},
		{"pptx", "odp"}, {"odp", "pptx"},
	}
	for _, p := range crossCases {
		if !Default.Supports(p[0], p[1]) {
			t.Errorf("expected %s->%s to be supported", p[0], p[1])
		}
	}
	if !Default.Supports(".DOCX", "ODT") {
		t.Error("expected .DOCX->ODT (aliased/cased) to be supported")
	}

	// No cross-family pairs are registered.
	forbidden := [][2]string{
		{"docx", "ods"}, {"docx", "odp"},
		{"xlsx", "odt"}, {"xlsx", "odp"},
		{"pptx", "odt"}, {"pptx", "ods"},
	}
	for _, p := range forbidden {
		if Default.Supports(p[0], p[1]) {
			t.Errorf("forbidden cross-family pair %s->%s must not be supported", p[0], p[1])
		}
	}

	// Identity pairs remain unregistered for every document format.
	for _, f := range documentFormats {
		if Default.Supports(f, f) {
			t.Errorf("identity pair %s->%s must not be registered", f, f)
		}
	}
}

// TestLibreOfficeConverter_TimeoutKillsRealProcess proves DOC-06: the
// existing hardened process-group-kill wrapper (runCommand, exec.go)
// terminates a real soffice/soffice.bin process tree on ctx timeout, with
// zero survivors.
//
// Two deliberate corrections vs. a naive version of this test:
//
//  1. This drives runCommand directly with the same soffice argv Convert
//     builds, rather than calling LibreOfficeConverter{}.Convert with a
//     .txt input. filterFor(".txt") returns an error and short-circuits
//     BEFORE soffice ever launches, so a Convert-based "zero survivors"
//     assertion would trivially pass without ever starting a real process
//     — a false pass that would defeat the whole point of DOC-06.
//  2. Rather than a blind flat deadline, this polls `ps` for soffice.bin's
//     running state before killing. A flat deadline risks firing the kill
//     before soffice.bin has even forked (also a false pass — nothing real
//     to kill yet). Polling ensures the kill genuinely lands mid-render.
func TestLibreOfficeConverter_TimeoutKillsRealProcess(t *testing.T) {
	if _, err := exec.LookPath("soffice"); err != nil {
		t.Skip("soffice not on PATH; run inside the worker test image")
	}

	dir := t.TempDir()
	inPath := filepath.Join(dir, "in.txt")
	var buf bytes.Buffer
	for i := 0; i < 80_000; i++ {
		buf.WriteString("the quick brown fox jumps over the lazy dog\n")
	}
	if err := os.WriteFile(inPath, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	profileDir := filepath.Join(dir, "lo-profile")
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatal(err)
	}

	args := []string{
		"--headless", "--invisible", "--nocrashreport", "--nodefault",
		"--nologo", "--nofirststartwizard", "--norestore",
		"-env:UserInstallation=file://" + profileDir,
		"--convert-to", "pdf:writer_pdf_Export",
		"--outdir", dir,
		inPath,
	}

	// Generous outer timeout: the launch itself must never get cut short
	// before soffice.bin actually forks.
	parentCtx, parentCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer parentCancel()

	ctx, killCancel := context.WithCancel(parentCtx)
	defer killCancel()

	runDone := make(chan error, 1)
	go func() { runDone <- runCommand(ctx, "soffice", args...) }()

	// Poll for soffice.bin actually running before triggering the kill —
	// this is the probe-and-kill methodology RESEARCH.md verified live
	// (Pitfall 2), avoiding a false pass where the kill fires before any
	// real process exists to kill.
	deadline := time.Now().Add(5 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		out, err := exec.Command("ps", "-eo", "pid,comm").CombinedOutput()
		if err == nil && strings.Contains(string(out), "soffice.bin") {
			found = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !found {
		t.Fatalf("soffice.bin never appeared within the poll bound; test setup is broken")
	}

	// soffice.bin is now running — trigger the SIGKILL-on-cancel path.
	killCancel()

	if err := <-runDone; err == nil {
		t.Fatal("expected runCommand to return an error after the kill, got nil")
	}

	out, err := exec.Command("ps", "-eo", "pid,comm").CombinedOutput()
	if err != nil {
		t.Fatalf("ps: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "soffice") || strings.Contains(line, "oosplash") {
			t.Fatalf("surviving LibreOffice process after timeout: %s", line)
		}
	}
}

// TestLibreOfficeConverter_ConvertProducesValidPDF proves DOC-04/DOC-05
// live: a real Convert() call against a real office document produces a
// valid, %PDF--prefixed output file.
func TestLibreOfficeConverter_ConvertProducesValidPDF(t *testing.T) {
	if _, err := exec.LookPath("soffice"); err != nil {
		t.Skip("soffice not on PATH; run inside the worker test image")
	}

	dir := t.TempDir()
	seedPath := filepath.Join(dir, "seed.txt")
	if err := os.WriteFile(seedPath, []byte("hello from octoconv\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	seedCtx, seedCancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer seedCancel()
	if err := runCommand(seedCtx, "soffice", "--headless", "--convert-to", "odt", "--outdir", dir, seedPath); err != nil {
		t.Fatalf("seed conversion (txt->odt) failed: %v", err)
	}

	producedODT := filepath.Join(dir, "seed.odt")
	inPath := filepath.Join(dir, "in.odt")
	if err := os.Rename(producedODT, inPath); err != nil {
		t.Fatalf("rename seed odt: %v", err)
	}

	outPath := filepath.Join(dir, "out.pdf")
	convertCtx, convertCancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer convertCancel()

	if err := (LibreOfficeConverter{}).Convert(convertCtx, inPath, outPath, nil); err != nil {
		t.Fatalf("Convert(in.odt, out.pdf) = %v, want nil", err)
	}

	fi, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("stat output: %v", err)
	}
	if fi.Size() == 0 {
		t.Fatal("output pdf is empty")
	}

	f, err := os.Open(outPath)
	if err != nil {
		t.Fatalf("open output: %v", err)
	}
	defer f.Close()
	buf := make([]byte, 5)
	if _, err := f.Read(buf); err != nil {
		t.Fatalf("read output header: %v", err)
	}
	if string(buf) != "%PDF-" {
		t.Fatalf("output header = %q, want %%PDF-", buf)
	}
}
