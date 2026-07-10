package convert

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
// PDFProfile against the closed allow-list. Strict means exactly one
// top-level JSON object (a bare `null` or any other non-object value is
// rejected), no duplicate top-level keys (no silent "last wins"), no bytes
// trailing the closing brace (checkStrictObject), and unknown keys are
// rejected (DisallowUnknownFields) so a client cannot smuggle an option this
// struct doesn't know about; an out-of-allow-list pdf_profile value is
// rejected the same way. An empty/absent pdf_profile ("{}" or missing key)
// is valid and yields a zero DocOpts (opts are optional).
func ParseDocOpts(raw []byte) (DocOpts, error) {
	if err := checkStrictObject(raw); err != nil {
		return DocOpts{}, err
	}
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

// checkStrictObject enforces the strictness properties json.Decoder.Decode
// alone does not (WR-01): Decode reads only the FIRST JSON value and ignores
// everything after it, accepts a top-level `null` as a valid zero struct, and
// resolves duplicate keys as silent "last wins". This walk requires the input
// to be exactly one top-level JSON object with unique top-level keys and
// nothing but EOF after the closing brace, so a smuggled second object,
// trailing garbage, or a rejected value resurrected by a duplicate key can
// never slip past the parse boundary.
func checkStrictObject(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("parse opts: %w", err)
	}
	if tok != json.Delim('{') {
		return fmt.Errorf("parse opts: top-level value must be a JSON object")
	}
	seen := make(map[string]struct{})
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("parse opts: %w", err)
		}
		key, ok := keyTok.(string)
		if !ok {
			return fmt.Errorf("parse opts: object key is not a string")
		}
		if _, dup := seen[key]; dup {
			return fmt.Errorf("parse opts: duplicate key %q", key)
		}
		seen[key] = struct{}{}
		// Consume the value (Decode reads exactly one value mid-object,
		// including nested composites); the actual field decoding happens in
		// ParseDocOpts, this walk only validates shape.
		var discard json.RawMessage
		if err := dec.Decode(&discard); err != nil {
			return fmt.Errorf("parse opts: %w", err)
		}
	}
	if _, err := dec.Token(); err != nil { // consume the closing '}'
		return fmt.Errorf("parse opts: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("parse opts: trailing data after JSON object")
	}
	return nil
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
