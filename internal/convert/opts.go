package convert

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// pdfProfileA2b is the single accepted pdf_profile value today (D-01). Future
// profiles (pdf/a-1b, pdf/a-3b) are near-free additions to this allow-list --
// same SelectPdfVersion enum, a new hardcoded suffix in PDFAFilterOptions --
// deferred until a real client needs them.
const pdfProfileA2b = "pdf/a-2b"

// pdfaFilterOptionsSuffix is the fixed, server-constant LibreOffice
// FilterOptions JSON suffix for PDF/A-2b export (D-07): SelectPdfVersion=2
// forces PDF/A output, and EmbedStandardFonts=true is hardwired alongside it
// because SelectPdfVersion alone does NOT imply font embedding -- an
// unembedded-font PDF/A-2b is not conformant (Pitfall 7). This is a
// compile-time string literal, never assembled from client input (Pitfall 9).
const pdfaFilterOptionsSuffix = `{"SelectPdfVersion":{"type":"long","value":"2"},"EmbedStandardFonts":{"type":"boolean","value":true}}`

// DocOpts is the closed, strictly-parsed set of client-requested document
// conversion options (D-01). Every field is validated against a fixed
// allow-list by ParseDocOpts -- no field here ever carries raw, unvalidated
// client bytes past the parse boundary. Currently a single enum-profile
// field; boolean flags were deliberately rejected (D-01) in favor of an
// explicit, extensible profile string.
type DocOpts struct {
	PDFProfile string `json:"pdf_profile,omitempty"`
}

// ParseDocOpts strict-decodes raw JSON into a DocOpts and validates
// PDFProfile against the closed allow-list. Unknown keys are rejected
// (DisallowUnknownFields) so a client cannot smuggle an option this struct
// doesn't know about; an out-of-allow-list pdf_profile value is rejected the
// same way. An empty/absent pdf_profile ("{}" or missing key) is valid and
// yields a zero DocOpts (opts are optional).
func ParseDocOpts(raw []byte) (DocOpts, error) {
	var o DocOpts
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&o); err != nil {
		return DocOpts{}, fmt.Errorf("parse opts: %w", err)
	}
	if o.PDFProfile != "" && o.PDFProfile != pdfProfileA2b {
		return DocOpts{}, fmt.Errorf("unsupported pdf_profile %q", o.PDFProfile)
	}
	return o, nil
}

// DocOptsFromMap round-trips a persisted map[string]any (job.Opts, already
// unmarshaled from the jobs.options jsonb column by internal/jobs) through
// ParseDocOpts -- the same strictness (DisallowUnknownFields + allow-list)
// applied on the worker/converter read path as on the API write path (D-10).
// A nil/empty map yields a zero DocOpts, no error.
func DocOptsFromMap(m map[string]any) (DocOpts, error) {
	if len(m) == 0 {
		return DocOpts{}, nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return DocOpts{}, fmt.Errorf("marshal opts: %w", err)
	}
	return ParseDocOpts(raw)
}

// ValidateApplicability rejects opts that do not apply to the given
// (engine, source, target) conversion. Empty opts always apply (nothing was
// requested). A non-empty PDFProfile applies only to document-engine
// conversions targeting pdf -- this logic lives here in internal/convert,
// next to the format-pair table, rather than in the API layer (D-04); the
// API layer only calls it once engine/source/target are known.
func ValidateApplicability(engine, source, target string, o DocOpts) error {
	if o.PDFProfile == "" {
		return nil
	}
	if engine != EngineDocument || NormalizeFormat(target) != "pdf" {
		return fmt.Errorf("pdf_profile is only valid for document -> pdf conversions")
	}
	return nil
}

// PDFAFilterOptions returns the LibreOffice FilterOptions JSON suffix for the
// requested PDF/A profile, and whether PDF/A was requested at all. The
// returned string is ALWAYS the compile-time pdfaFilterOptionsSuffix
// constant, selected purely by switching on the already-validated
// o.PDFProfile enum value -- it is never built from o's raw bytes, so no
// client-controlled content can ever reach the soffice filter argument
// (Pitfall 9). Callers MUST only pass a DocOpts that has already been through
// ParseDocOpts/DocOptsFromMap.
func PDFAFilterOptions(o DocOpts) (suffix string, isPDFA bool) {
	if o.PDFProfile != pdfProfileA2b {
		return "", false
	}
	return pdfaFilterOptionsSuffix, true
}
