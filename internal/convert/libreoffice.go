package convert

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// documentFormats are the office document formats LibreOffice converts to pdf.
var documentFormats = []string{"docx", "odt", "xlsx", "ods", "pptx", "odp"}

// crossPairs is the explicit, flat list of the 6 symmetric intra-family
// cross-format pairs (D-01): docx<->odt, xlsx<->ods, pptx<->odp. Deliberately
// NOT a cross-product of documentFormats -- that would also generate the
// forbidden cross-family pairs (e.g. docx->ods). Each pair is listed by hand
// so the supported surface is auditable at a glance.
var crossPairs = []Pair{
	{From: "docx", To: "odt"},
	{From: "odt", To: "docx"},
	{From: "xlsx", To: "ods"},
	{From: "ods", To: "xlsx"},
	{From: "pptx", To: "odp"},
	{From: "odp", To: "pptx"},
}

// LibreOfficeConverter converts office documents to PDF, and between odt/odt-
// family sibling formats, by shelling out to the `soffice` CLI in headless
// mode.
type LibreOfficeConverter struct{}

// Pairs returns one {format, "pdf"} pair per supported source document format,
// plus the 6 symmetric intra-family cross pairs in crossPairs (D-01).
func (LibreOfficeConverter) Pairs() []Pair {
	pairs := make([]Pair, 0, len(documentFormats)+len(crossPairs))
	for _, f := range documentFormats {
		pairs = append(pairs, Pair{From: f, To: "pdf"})
	}
	pairs = append(pairs, crossPairs...)
	return pairs
}

// Convert runs `soffice --headless --convert-to <target>:<filter>` against
// inPath, isolating this invocation's LibreOffice profile inside a
// subdirectory of filepath.Dir(outPath) (the caller's per-job workDir —
// already unique and already cleaned up) so concurrent jobs never share a
// profile/lock. The target format is derived from filepath.Ext(outPath) — the
// worker builds outPath as "out."+job.TargetFormat (internal/worker/worker.go)
// — so Convert is target-agnostic: it drives whichever pair Pairs()
// advertised. ctx must carry the engine timeout.
func (LibreOfficeConverter) Convert(ctx context.Context, inPath, outPath string, opts map[string]any) error {
	workDir := filepath.Dir(outPath) // caller's per-job workDir; already unique, already cleaned up
	profileDir := filepath.Join(workDir, "lo-profile")
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		return fmt.Errorf("libreoffice: mkdir profile: %w", err)
	}

	// Garbage opts (e.g. a corrupt jobs.options column) is a deterministic
	// failure, not a transient one -- DocOptsFromMap applies the same
	// strictness (DisallowUnknownFields + allow-list) here as at the API
	// write path (D-10).
	docOpts, err := DocOptsFromMap(opts)
	if err != nil {
		return fmt.Errorf("libreoffice: %w", err)
	}
	// suffix is a compile-time server constant selected only by the
	// validated pdf_profile enum -- never built from opts' raw bytes
	// (D-07, Pitfall 9).
	suffix, isPDFA := PDFAFilterOptions(docOpts)

	targetFormat := NormalizeFormat(filepath.Ext(outPath))
	filter, err := filterFor(filepath.Ext(inPath), filepath.Ext(outPath))
	if err != nil {
		return fmt.Errorf("libreoffice: %w", err)
	}

	convertTo := targetFormat + ":" + filter
	if isPDFA {
		// Appended onto the SAME argv element (no shell involved --
		// runCommand/exec.Command takes an argv array, exec.go:19-20), so no
		// new escaping mechanism is needed.
		convertTo += ":" + suffix
	}

	args := []string{
		"--headless", "--invisible", "--nocrashreport", "--nodefault",
		"--nologo", "--nofirststartwizard", "--norestore",
		"-env:UserInstallation=file://" + profileDir,
		"--convert-to", convertTo,
		"--outdir", workDir,
		inPath,
	}
	if err := runCommand(ctx, "soffice", args...); err != nil {
		return fmt.Errorf("libreoffice: %w", err)
	}

	// soffice writes <input-basename>.<targetFormat> into --outdir; the
	// worker's inPath convention (internal/worker/worker.go) is always
	// "in.<sourceFormat>", so the produced file is deterministically
	// workDir/in.<targetFormat>. Rename it to the caller's outPath.
	producedPath := filepath.Join(workDir, strings.TrimSuffix(filepath.Base(inPath), filepath.Ext(inPath))+"."+targetFormat)
	// soffice's documented failure mode includes exiting 0 WITHOUT producing
	// an output file (e.g. a refused/unknown export filter). That outcome is
	// deterministic, so it must surface as a terminal-classified error
	// ("produced no output file" is in terminalLibreOfficeSignatures), not
	// the transient-looking rename ENOENT it would otherwise become (D-04).
	if _, err := os.Stat(producedPath); err != nil {
		return fmt.Errorf("libreoffice: produced no output file for %q: %v", targetFormat, err)
	}
	if err := os.Rename(producedPath, outPath); err != nil {
		return fmt.Errorf("libreoffice: rename output: %w", err)
	}

	return validateDocumentOutput(outPath, targetFormat, isPDFA)
}

// Engine reports the document engine class (D-01).
func (LibreOfficeConverter) Engine() string { return EngineDocument }

// filterTable is the explicit (source, target) -> LibreOffice export filter
// name table (D-02). There is deliberately no auto-derivation from extension:
// every supported pair is listed here by hand, so an unsupported combination
// is a hard "unsupported" error rather than a guessed filter name. The
// researched LO 7.4 (bookworm) filter names below are the starting point,
// live-confirmed against a real container in Plan 13-03.
var filterTable = map[[2]string]string{
	{"docx", "pdf"}: "writer_pdf_Export",
	{"odt", "pdf"}:  "writer_pdf_Export",
	{"xlsx", "pdf"}: "calc_pdf_Export",
	{"ods", "pdf"}:  "calc_pdf_Export",
	{"pptx", "pdf"}: "impress_pdf_Export",
	{"odp", "pdf"}:  "impress_pdf_Export",

	{"docx", "odt"}: "writer8",
	{"odt", "docx"}: "MS Word 2007 XML",
	{"xlsx", "ods"}: "calc8",
	{"ods", "xlsx"}: "Calc MS Excel 2007 XML",
	{"pptx", "odp"}: "impress8",
	{"odp", "pptx"}: "Impress MS PowerPoint 2007 XML",
}

// filterFor maps a (source, target) document format pair to the LibreOffice
// export filter that produces the correct output for that pair's application
// (Writer/Calc/Impress). filterTable is the single source of truth (D-02); an
// unsupported (source, target) combination returns "" and an error whose
// message contains "no export filter for".
func filterFor(sourceExt, targetFormat string) (string, error) {
	key := [2]string{NormalizeFormat(sourceExt), NormalizeFormat(targetFormat)}
	if filter, ok := filterTable[key]; ok {
		return filter, nil
	}
	return "", fmt.Errorf("no export filter for %s -> %s", sourceExt, targetFormat)
}

// pdfMagic is the leading byte sequence every valid PDF file begins with.
var pdfMagic = []byte("%PDF-")

// validatePDF guards against LibreOffice's documented "exit 0 but empty/
// corrupt output" failure mode (D-02): it requires the output file to be
// non-zero size AND begin with the %PDF- magic bytes before a conversion is
// treated as successful.
func validatePDF(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("libreoffice: stat output: %w", err)
	}
	if fi.Size() == 0 {
		return fmt.Errorf("libreoffice: output is empty")
	}
	// A file shorter than the magic itself can never be a valid PDF; classify
	// it as the terminal missing-magic case up front so the ReadFull below
	// cannot surface it as a transient-looking "unexpected EOF" (D-04).
	if fi.Size() < int64(len(pdfMagic)) {
		return fmt.Errorf("libreoffice: output missing %%PDF- magic bytes")
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("libreoffice: open output: %w", err)
	}
	defer f.Close()
	buf := make([]byte, len(pdfMagic))
	if _, err := io.ReadFull(f, buf); err != nil {
		return fmt.Errorf("libreoffice: read output header: %w", err)
	}
	if !bytes.Equal(buf, pdfMagic) {
		return fmt.Errorf("libreoffice: output missing %%PDF- magic bytes")
	}
	return nil
}

// gtsPDFAMarker is the OutputIntent identifier every ISO 19005 (PDF/A)
// conforming PDF is required to embed. This is a family match (not the
// stricter "/GTS_PDFA2" per-part variant) -- a deliberate, explicitly
// NON-authoritative sanity check (Pitfall 8): it proves LibreOffice at least
// attempted PDF/A tagging, not full ISO 19005 conformance. Full veraPDF
// validation is accepted residual risk (DOCV3-01).
var gtsPDFAMarker = []byte("/GTS_PDFA")

// validateDocumentOutput dispatches to the correct structural validator for
// targetFormat (D-03): validatePDF (unchanged %PDF- magic check) for pdf
// targets, or -- symmetric to the input-side guarantee -- output validated by
// the same sniff (SniffContainer) that validates input, for every non-pdf
// document target. A mismatched/wrong-container output (e.g. a docx handed
// as target "odt") is treated identically to an empty/corrupt output: a
// deterministic, unrecoverable failure that must never be marked "done"
// (T-13-01). When wantPDFA is set (a pdf_profile was requested, D-05), a pdf
// target additionally requires the /GTS_PDFA OutputIntent marker to be
// present -- a regressed LibreOffice that silently returns a plain PDF under
// a PDF/A request must never be reported as a successful archival export.
func validateDocumentOutput(path, targetFormat string, wantPDFA bool) error {
	target := NormalizeFormat(targetFormat)
	if target == "pdf" {
		if err := validatePDF(path); err != nil {
			return err
		}
		if !wantPDFA {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("libreoffice: read output for PDF/A check: %w", err)
		}
		if !bytes.Contains(data, gtsPDFAMarker) {
			return fmt.Errorf("libreoffice: output missing PDF/A OutputIntent marker")
		}
		return nil
	}

	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("libreoffice: stat output: %w", err)
	}
	if fi.Size() == 0 {
		return fmt.Errorf("libreoffice: output is empty")
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("libreoffice: open output: %w", err)
	}
	defer f.Close()

	cr, serr := SniffContainer(f, fi.Size())
	if serr != nil || cr.Format != target {
		return fmt.Errorf("libreoffice: output does not match expected container format %s", targetFormat)
	}
	return nil
}
