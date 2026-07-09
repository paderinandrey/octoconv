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
	cases := map[string]string{
		"docx":  "writer_pdf_Export",
		"odt":   "writer_pdf_Export",
		"xlsx":  "calc_pdf_Export",
		"ods":   "calc_pdf_Export",
		"pptx":  "impress_pdf_Export",
		"odp":   "impress_pdf_Export",
		".DOCX": "writer_pdf_Export",
		"PPTX":  "impress_pdf_Export",
	}
	for in, want := range cases {
		got, err := filterFor(in)
		if err != nil {
			t.Errorf("filterFor(%q) unexpected error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("filterFor(%q) = %q, want %q", in, got, want)
		}
	}

	if got, err := filterFor(".txt"); err == nil {
		t.Errorf("filterFor(\".txt\") = %q, nil, want an error", got)
	} else if got != "" {
		t.Errorf("filterFor(\".txt\") returned non-empty filter %q alongside error", got)
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
