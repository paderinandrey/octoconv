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

// LibreOfficeConverter converts office documents to PDF by shelling out to
// the `soffice` CLI in headless mode.
type LibreOfficeConverter struct{}

// Pairs returns one {format, "pdf"} pair per supported source document format.
func (LibreOfficeConverter) Pairs() []Pair {
	pairs := make([]Pair, 0, len(documentFormats))
	for _, f := range documentFormats {
		pairs = append(pairs, Pair{From: f, To: "pdf"})
	}
	return pairs
}

// Convert runs `soffice --headless --convert-to pdf:<filter>` against inPath,
// isolating this invocation's LibreOffice profile inside a subdirectory of
// filepath.Dir(outPath) (the caller's per-job workDir — already unique and
// already cleaned up) so concurrent jobs never share a profile/lock. ctx must
// carry the engine timeout.
func (LibreOfficeConverter) Convert(ctx context.Context, inPath, outPath string, _ map[string]any) error {
	workDir := filepath.Dir(outPath) // caller's per-job workDir; already unique, already cleaned up
	profileDir := filepath.Join(workDir, "lo-profile")
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		return fmt.Errorf("libreoffice: mkdir profile: %w", err)
	}

	filter, err := filterFor(filepath.Ext(inPath))
	if err != nil {
		return fmt.Errorf("libreoffice: %w", err)
	}

	args := []string{
		"--headless", "--invisible", "--nocrashreport", "--nodefault",
		"--nologo", "--nofirststartwizard", "--norestore",
		"-env:UserInstallation=file://" + profileDir,
		"--convert-to", "pdf:" + filter,
		"--outdir", workDir,
		inPath,
	}
	if err := runCommand(ctx, "soffice", args...); err != nil {
		return fmt.Errorf("libreoffice: %w", err)
	}

	// soffice writes <input-basename>.pdf into --outdir; the worker's inPath
	// convention (internal/worker/worker.go) is always "in.<sourceFormat>",
	// so the produced file is deterministically workDir/in.pdf. Rename it to
	// the caller's outPath.
	producedPath := filepath.Join(workDir, strings.TrimSuffix(filepath.Base(inPath), filepath.Ext(inPath))+".pdf")
	if err := os.Rename(producedPath, outPath); err != nil {
		return fmt.Errorf("libreoffice: rename output: %w", err)
	}

	return validatePDF(outPath)
}

// filterFor maps a source document extension to the LibreOffice PDF export
// filter that produces the correct output for that document's application
// (Writer/Calc/Impress).
func filterFor(sourceExt string) (string, error) {
	switch NormalizeFormat(sourceExt) {
	case "docx", "odt":
		return "writer_pdf_Export", nil
	case "xlsx", "ods":
		return "calc_pdf_Export", nil
	case "pptx", "odp":
		return "impress_pdf_Export", nil
	default:
		return "", fmt.Errorf("no pdf export filter for %q", sourceExt)
	}
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
