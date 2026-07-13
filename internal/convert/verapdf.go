package convert

import (
	"context"
	"encoding/xml"
	"fmt"
	"time"
)

// verapdfTimeout bounds a single ValidatePDFA invocation (D-04). It is set
// once at process startup via SetVeraPDFTimeout from
// cmd/document-worker/main.go -- mirroring NewHandler's engineTimeout
// threading, this package never reads VERAPDF_TIMEOUT (or any env var)
// directly; env-only-in-main is the enforced convention (WARNING-3 fix).
// Zero means "never set" -- effectiveVeraPDFTimeout falls back to 60s so
// hermetic tests and any caller that never calls SetVeraPDFTimeout still get
// a sane bound.
var verapdfTimeout time.Duration

// SetVeraPDFTimeout stores the VERAPDF_TIMEOUT budget for every subsequent
// ValidatePDFA call. Call exactly once at process startup, BEFORE the asynq
// server starts consuming tasks (single write before any concurrent reader
// -- no mutex needed, mirroring how engineTimeout is threaded into
// NewHandler).
func SetVeraPDFTimeout(d time.Duration) {
	verapdfTimeout = d
}

// effectiveVeraPDFTimeout returns the configured timeout, defaulting to 60s
// when SetVeraPDFTimeout was never called (verapdfTimeout == 0).
func effectiveVeraPDFTimeout() time.Duration {
	if verapdfTimeout > 0 {
		return verapdfTimeout
	}
	return 60 * time.Second
}

// maxSummaryChars bounds the rule-violation summary that rides into the
// terminal error message itself (D-07): only a short diagnostic excerpt is
// ever embedded in error text; the full detail lands in job_events.detail
// via the worker's existing MarkFailed call, not here.
const maxSummaryChars = 500

// veraPDFReport is the subset of veraPDF's `--format xml` machine-readable
// report schema (verified live against the pinned verapdf/cli:v1.30.2 image,
// captured in 23-01-SUMMARY.md and internal/convert/testdata/verapdf_*.mrr.xml)
// this parser needs: the validationReport's isCompliant verdict (the
// authoritative compliance signal per D-09 -- exit code alone is NEVER
// trusted), its rule/check violations (for the bounded D-07 summary), and
// batchSummary's failure counters (failedToParse/outOfMemory/veraExceptions/
// failedJobs -- these, not just isCompliant, indicate the validator itself
// failed to produce a trustworthy verdict rather than a clean non-compliant
// one, D-06's "an unverifiable archival claim is a failed archival claim").
type veraPDFReport struct {
	XMLName xml.Name `xml:"report"`
	Jobs    struct {
		Job []struct {
			ValidationReport struct {
				JobEndStatus string `xml:"jobEndStatus,attr"`
				IsCompliant  bool   `xml:"isCompliant,attr"`
				Details      struct {
					Rule []struct {
						Clause string `xml:"clause,attr"`
						Check  []struct {
							Status       string `xml:"status,attr"`
							ErrorMessage string `xml:"errorMessage"`
						} `xml:"check"`
					} `xml:"rule"`
				} `xml:"details"`
			} `xml:"validationReport"`
		} `xml:"job"`
	} `xml:"jobs"`
	BatchSummary struct {
		FailedToParse     int `xml:"failedToParse,attr"`
		OutOfMemory       int `xml:"outOfMemory,attr"`
		VeraExceptions    int `xml:"veraExceptions,attr"`
		ValidationReports struct {
			FailedJobs int `xml:"failedJobs,attr"`
		} `xml:"validationReports"`
	} `xml:"batchSummary"`
}

// parseVeraPDFReport parses raw veraPDF machine-readable report bytes
// (D-08: unit-tested offline against the committed compliant/non-compliant
// fixtures, zero binary dependency) and returns the authoritative compliance
// verdict. err is non-nil ONLY when the report itself is unparseable, or
// batchSummary's failure counters indicate the validator failed to produce a
// trustworthy verdict at all (D-06/D-09) -- a clean non-compliant verdict is
// NOT an error: it is reported via compliant=false plus a bounded summary.
func parseVeraPDFReport(raw []byte) (compliant bool, summary string, err error) {
	var report veraPDFReport
	if uerr := xml.Unmarshal(raw, &report); uerr != nil {
		return false, "", fmt.Errorf("parse machine-readable report: %w", uerr)
	}
	bs := report.BatchSummary
	if bs.FailedToParse > 0 || bs.OutOfMemory > 0 || bs.VeraExceptions > 0 || bs.ValidationReports.FailedJobs > 0 {
		return false, "", fmt.Errorf(
			"batch summary reports a validator failure (failedToParse=%d outOfMemory=%d veraExceptions=%d failedJobs=%d)",
			bs.FailedToParse, bs.OutOfMemory, bs.VeraExceptions, bs.ValidationReports.FailedJobs,
		)
	}
	if len(report.Jobs.Job) == 0 {
		return false, "", fmt.Errorf("report contains no job entries")
	}
	vr := report.Jobs.Job[0].ValidationReport
	if vr.IsCompliant {
		return true, "", nil
	}

	var b []byte
	for _, rule := range vr.Details.Rule {
		for _, check := range rule.Check {
			if check.ErrorMessage == "" {
				continue
			}
			if len(b) > 0 {
				b = append(b, "; "...)
			}
			b = append(b, rule.Clause...)
			b = append(b, ": "...)
			b = append(b, check.ErrorMessage...)
		}
	}
	summary = string(b)
	if len(summary) > maxSummaryChars {
		summary = summary[:maxSummaryChars] + "...(truncated)"
	}
	return false, summary, nil
}

// validateReport turns a parsed veraPDF verdict into ValidatePDFA's
// fail-closed terminal error (D-06): "pdf/a non-compliant" for a clean
// non-compliant report, "pdf/a validation error" for anything the validator
// itself could not produce a trustworthy verdict for. Both substrings are
// coupled into terminalVeraPDFSignatures (internal/worker/worker.go) in this
// SAME commit -- a retry can never fix either outcome.
func validateReport(raw []byte) error {
	compliant, summary, err := parseVeraPDFReport(raw)
	if err != nil {
		return fmt.Errorf("verapdf: pdf/a validation error: %w", err)
	}
	if !compliant {
		return fmt.Errorf("verapdf: pdf/a non-compliant: %s", summary)
	}
	return nil
}

// ValidatePDFA runs real ISO 19005-2b validation (D-05: only ever called
// AFTER the cheap /GTS_PDFA marker pre-filter in validateDocumentOutput has
// passed) against the produced PDF at path, via the existing hardened
// runCommand (Setpgid + process-group kill, D-04) -- no new exec
// abstraction. The invocation is bounded by effectiveVeraPDFTimeout (its own
// VERAPDF_TIMEOUT budget, injected via SetVeraPDFTimeout, separate from the
// outer DOCUMENT_ENGINE_TIMEOUT the caller's ctx already carries --
// whichever fires first wins, since vctx is derived FROM ctx).
//
// veraPDF's machine-readable report rides on STDOUT (verified live in
// scripts/verapdf-measure.sh: `verapdf -f 2b --format xml file.pdf >
// report.xml`) -- runCommand's captured stdout is parsed regardless of the
// process's exit code (D-09: veraPDF exits 1 for a non-compliant report,
// which is a VALID report, not a process failure; exit code alone is never
// trusted). Only a run that produced NO stdout at all (process never
// started, or was killed by the timeout before writing a report) skips
// straight to the fail-closed "validation error" path without attempting to
// parse an empty buffer.
func ValidatePDFA(ctx context.Context, path string) error {
	vctx, cancel := context.WithTimeout(ctx, effectiveVeraPDFTimeout())
	defer cancel()

	stdout, runErr := runCommand(vctx, "verapdf", "-f", "2b", "--format", "xml", path)
	if runErr != nil && len(stdout) == 0 {
		// D-06: an unverifiable archival claim is a failed archival claim.
		// %w here preserves a wrapped context.DeadlineExceeded (a
		// VERAPDF_TIMEOUT expiry) all the way up to isDocumentTerminal,
		// which already classifies it terminal -- no new signature needed
		// for the timeout case.
		return fmt.Errorf("verapdf: pdf/a validation error: %w", runErr)
	}
	return validateReport(stdout)
}
