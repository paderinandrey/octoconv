package convert

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// verapdfFixture reads one of Plan 01's committed machine-readable report
// fixtures (D-08) -- these are REAL captured veraPDF output against the
// pinned verapdf/cli:v1.30.2 image, not hand-crafted synthetic XML.
func verapdfFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

// TestParseVeraPDFReportCompliant proves the parser reads isCompliant="true"
// off the committed real compliant report and returns no error/no summary.
func TestParseVeraPDFReportCompliant(t *testing.T) {
	raw := verapdfFixture(t, "verapdf_compliant.mrr.xml")
	compliant, summary, err := parseVeraPDFReport(raw)
	if err != nil {
		t.Fatalf("parseVeraPDFReport(compliant) error = %v, want nil", err)
	}
	if !compliant {
		t.Error("parseVeraPDFReport(compliant) compliant = false, want true")
	}
	if summary != "" {
		t.Errorf("parseVeraPDFReport(compliant) summary = %q, want empty", summary)
	}
}

// TestParseVeraPDFReportNonCompliant proves the parser reads
// isCompliant="false" off the committed real non-compliant report and
// surfaces a non-empty, bounded rule-violation summary (D-07).
func TestParseVeraPDFReportNonCompliant(t *testing.T) {
	raw := verapdfFixture(t, "verapdf_noncompliant.mrr.xml")
	compliant, summary, err := parseVeraPDFReport(raw)
	if err != nil {
		t.Fatalf("parseVeraPDFReport(noncompliant) error = %v, want nil", err)
	}
	if compliant {
		t.Error("parseVeraPDFReport(noncompliant) compliant = true, want false")
	}
	if summary == "" {
		t.Fatal("parseVeraPDFReport(noncompliant) summary is empty, want the rule-violation excerpt")
	}
	// The fixture's two failed rules mention the missing metadata stream and
	// the DeviceRGB-without-OutputIntent violation -- assert at least one
	// concrete detail actually made it into the summary, not just a generic
	// placeholder.
	if !strings.Contains(summary, "DeviceRGB") && !strings.Contains(summary, "metadata") {
		t.Errorf("parseVeraPDFReport(noncompliant) summary = %q, want it to contain a concrete rule violation detail", summary)
	}
	if len(summary) > maxSummaryChars+len("...(truncated)") {
		t.Errorf("parseVeraPDFReport(noncompliant) summary length = %d, want it bounded by maxSummaryChars (%d)", len(summary), maxSummaryChars)
	}
}

// TestParseVeraPDFReportUnparseable proves garbage/non-XML bytes (the
// unparseable-report case D-06/D-09 requires fail-closed handling for)
// return a non-nil error, entirely offline -- no veraPDF binary involved.
func TestParseVeraPDFReportUnparseable(t *testing.T) {
	if _, _, err := parseVeraPDFReport([]byte("not xml at all")); err == nil {
		t.Fatal("parseVeraPDFReport(garbage) error = nil, want an error")
	}
}

// TestParseVeraPDFReportBatchSummaryFailure proves that even a
// well-formed-XML report is treated as a validator failure (not trusted at
// face value) when batchSummary's failure counters are non-zero -- the
// signal 23-01-SUMMARY.md calls out as more authoritative than isCompliant
// alone for detecting a validator-side problem (D-06/D-09).
func TestParseVeraPDFReportBatchSummaryFailure(t *testing.T) {
	synthetic := `<?xml version="1.0" encoding="utf-8"?>
<report>
  <jobs>
    <job>
      <validationReport jobEndStatus="exception" isCompliant="false"></validationReport>
    </job>
  </jobs>
  <batchSummary totalJobs="1" failedToParse="0" encrypted="0" outOfMemory="0" veraExceptions="1">
    <validationReports compliant="0" nonCompliant="0" failedJobs="1">0</validationReports>
  </batchSummary>
</report>`
	if _, _, err := parseVeraPDFReport([]byte(synthetic)); err == nil {
		t.Fatal("parseVeraPDFReport(batchSummary failure) error = nil, want an error (D-06 fail-closed)")
	}
}

// TestValidateReportCompliant/NonCompliant/Unparseable prove ValidatePDFA's
// terminal-error SHAPE (the exact substrings terminalVeraPDFSignatures
// matches on, internal/worker/worker.go) against the same fixtures/inputs,
// entirely offline (validateReport never shells out).
func TestValidateReportCompliant(t *testing.T) {
	if err := validateReport(verapdfFixture(t, "verapdf_compliant.mrr.xml")); err != nil {
		t.Errorf("validateReport(compliant) = %v, want nil", err)
	}
}

func TestValidateReportNonCompliant(t *testing.T) {
	err := validateReport(verapdfFixture(t, "verapdf_noncompliant.mrr.xml"))
	if err == nil {
		t.Fatal("validateReport(noncompliant) = nil, want error")
	}
	if !strings.Contains(err.Error(), "pdf/a non-compliant") {
		t.Errorf("validateReport(noncompliant) = %q, want it to contain the terminal signature %q", err, "pdf/a non-compliant")
	}
}

func TestValidateReportUnparseable(t *testing.T) {
	err := validateReport([]byte("not xml at all"))
	if err == nil {
		t.Fatal("validateReport(garbage) = nil, want error")
	}
	if !strings.Contains(err.Error(), "pdf/a validation error") {
		t.Errorf("validateReport(garbage) = %q, want it to contain the terminal signature %q", err, "pdf/a validation error")
	}
}

// TestSetVeraPDFTimeout proves the env-only-in-main injection seam (WARNING-3
// fix): SetVeraPDFTimeout stores the configured duration, and
// effectiveVeraPDFTimeout falls back to 60s when it was never set (or reset
// to zero), mirroring NewHandler's engineTimeout default.
func TestSetVeraPDFTimeout(t *testing.T) {
	orig := verapdfTimeout
	defer func() { verapdfTimeout = orig }()

	verapdfTimeout = 0
	if got := effectiveVeraPDFTimeout(); got != 60*time.Second {
		t.Errorf("effectiveVeraPDFTimeout() with unset verapdfTimeout = %v, want 60s default", got)
	}

	SetVeraPDFTimeout(5 * time.Second)
	if got := effectiveVeraPDFTimeout(); got != 5*time.Second {
		t.Errorf("effectiveVeraPDFTimeout() after SetVeraPDFTimeout(5s) = %v, want 5s", got)
	}
}
