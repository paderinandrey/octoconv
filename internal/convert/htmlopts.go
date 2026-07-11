package convert

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// htmlPageSizeCSS maps the closed page_size enum (D-06) to its CSS @page
// `size` keyword (CSS Paged Media spec keywords, not Chrome-specific).
// Selecting only from this map (never from raw client bytes) is what keeps
// buildPrintCSS's output a server-constant string -- the same invariant
// PDFAFilterOptions establishes for the document engine's FilterOptions
// suffix (opts.go), applied here to CSS text instead of an argv suffix
// (RESEARCH.md Pattern 1: no chromium CLI flag exists for any of these four
// print options).
var htmlPageSizeCSS = map[string]string{
	"a4":     "A4",
	"letter": "letter",
	"legal":  "legal",
	"a3":     "A3",
	"a5":     "A5",
}

// htmlMarginMMMin/htmlMarginMMMax bound the accepted margin_mm range (D-06).
const (
	htmlMarginMMMin = 0
	htmlMarginMMMax = 50
)

// HTMLOpts is the closed, strictly-parsed set of client-requested HTML->PDF
// print options (D-06/HTML-03). Every field is validated against a fixed
// allow-list/range by ParseHTMLOpts -- no field here ever carries raw,
// unvalidated client bytes past the parse boundary. Mirrors DocOpts's
// closed-struct shape (opts.go), but selects a server-constant CSS block
// (buildPrintCSS) rather than a CLI argv suffix, since chromium's one-shot
// print-to-pdf mode exposes no CLI flags for page geometry (RESEARCH.md
// Pattern 1).
type HTMLOpts struct {
	PageSize string `json:"page_size,omitempty"`
	// MarginMM is a *int, not an int, so an unset margin (nil) is
	// distinguishable from an explicit margin_mm:0. A nil MarginMM means the
	// client requested no page margin at all, and buildPrintCSS omits the
	// `margin` declaration entirely -- chromium's default print margin (and
	// the client HTML's own `@page` margin) then applies, instead of being
	// forced edge-to-edge. A non-nil pointer (including &0) is an explicit
	// client request and is honored as `margin: Nmm !important` (WR-02/CR-03).
	MarginMM        *int `json:"margin_mm,omitempty"`
	Landscape       bool `json:"landscape,omitempty"`
	PrintBackground bool `json:"print_background,omitempty"`
}

// ParseHTMLOpts strict-decodes raw JSON into an HTMLOpts and validates
// PageSize/MarginMM against their closed allow-list/range. Strictness
// (exactly one top-level JSON object, no duplicate keys, no trailing bytes,
// no top-level null, unknown fields rejected) is enforced by the shared
// checkStrictObject helper (opts.go) -- reused verbatim, not duplicated
// (D-10 parity with ParseDocOpts). An empty `{}` or absent opts is valid
// and yields a zero HTMLOpts.
func ParseHTMLOpts(raw []byte) (HTMLOpts, error) {
	if err := checkStrictObject(raw); err != nil {
		return HTMLOpts{}, err
	}
	var o HTMLOpts
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&o); err != nil {
		return HTMLOpts{}, fmt.Errorf("parse opts: %w", err)
	}
	if o.PageSize != "" {
		if _, ok := htmlPageSizeCSS[o.PageSize]; !ok {
			return HTMLOpts{}, fmt.Errorf("unsupported page_size %q", o.PageSize)
		}
	}
	// Range-check only when a margin was actually supplied; a nil MarginMM
	// (no margin_mm key) is valid and means "unset", not "0".
	if o.MarginMM != nil && (*o.MarginMM < htmlMarginMMMin || *o.MarginMM > htmlMarginMMMax) {
		return HTMLOpts{}, fmt.Errorf("margin_mm %d out of range [%d,%d]", *o.MarginMM, htmlMarginMMMin, htmlMarginMMMax)
	}
	return o, nil
}

// HTMLOptsFromMap round-trips a persisted map[string]any (job.Opts, already
// unmarshaled from the jobs.options jsonb column by internal/jobs) through
// ParseHTMLOpts -- the same strictness applied on the worker/converter read
// path as on the API write path (D-10, mirrors DocOptsFromMap). A
// nil/empty map yields a zero HTMLOpts, no error.
func HTMLOptsFromMap(m map[string]any) (HTMLOpts, error) {
	if len(m) == 0 {
		return HTMLOpts{}, nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return HTMLOpts{}, fmt.Errorf("marshal opts: %w", err)
	}
	return ParseHTMLOpts(raw)
}

// ValidateHTMLApplicability rejects opts that do not apply to the given
// (engine, source, target) conversion. Empty opts always apply (nothing was
// requested). A non-empty HTMLOpts applies only to html-engine conversions
// targeting pdf. Deliberately its OWN function scoped to EngineHTML, not
// merged into the shared ValidateApplicability (opts.go) -- RESEARCH.md is
// explicit HTMLOpts is a structurally different closed type from DocOpts,
// so the applicability check stays engine-scoped too.
func ValidateHTMLApplicability(engine, source, target string, o HTMLOpts) error {
	if isZeroHTMLOpts(o) {
		return nil
	}
	if engine != EngineHTML || NormalizeFormat(target) != "pdf" {
		return fmt.Errorf("html print options are only valid for html -> pdf conversions")
	}
	return nil
}

// isZeroHTMLOpts reports whether o carries no client-requested options at
// all -- the same "empty opts always apply" shortcut ValidateApplicability
// uses for DocOpts (opts.go), generalized to HTMLOpts's 4 fields.
func isZeroHTMLOpts(o HTMLOpts) bool {
	return o == HTMLOpts{}
}

// buildPrintCSS renders the fixed @page rule + print-color-adjust rule from
// ALREADY-VALIDATED HTMLOpts fields only -- never from raw client bytes
// (the same invariant PDFAFilterOptions establishes in opts.go, Pitfall 9's
// lesson applied here to CSS text). Callers MUST only pass an HTMLOpts that
// has already been through ParseHTMLOpts/HTMLOptsFromMap. `!important` on
// every injected property is deliberate: it must win the cascade regardless
// of any `@page`/print CSS the untrusted client HTML itself tries to set,
// and regardless of exactly where the block is injected relative to other
// <style>/<link> tags (RESEARCH.md Pattern 1).
func buildPrintCSS(o HTMLOpts) string {
	size := htmlPageSizeCSS[o.PageSize] // o.PageSize already validated against the closed enum
	if size == "" {
		size = htmlPageSizeCSS["a4"] // default page size when none was requested
	}
	if o.Landscape {
		size += " landscape"
	}
	// Emit the `margin` declaration ONLY when the client explicitly requested
	// one (MarginMM non-nil). A no-opts / unset-margin job (nil) gets no
	// `margin` at all, so chromium's default print margin and the client
	// HTML's own `@page` margin are respected -- rather than being forced to
	// a non-overridable `margin: 0mm !important` edge-to-edge default that a
	// zero-value int would have produced (WR-02/CR-03). An explicit
	// margin_mm:0 (MarginMM = &0) still emits `margin: 0mm !important`, so a
	// client can deliberately ask for true edge-to-edge output.
	var css string
	if o.MarginMM != nil {
		css = fmt.Sprintf("@page { size: %s !important; margin: %dmm !important; }\n", size, *o.MarginMM)
	} else {
		css = fmt.Sprintf("@page { size: %s !important; }\n", size)
	}
	if o.PrintBackground {
		css += "*, *::before, *::after { -webkit-print-color-adjust: exact !important; print-color-adjust: exact !important; }\n"
	} else {
		// print-color-adjust: economy is kept as the spec-correct hint for
		// other renderers, but Plan 04's live smoke checklist (item 3)
		// found chromium-headless-shell 150.0.7871.100's one-shot
		// --print-to-pdf path does NOT honor it -- a red background div
		// still printed under economy in a live test. The forced
		// background/background-color/background-image overrides below are
		// the live-verified mechanism that actually suppresses printed
		// backgrounds in this binary (still server-constant text selected
		// only by the validated PrintBackground bool, same invariant as
		// every other buildPrintCSS branch).
		css += "*, *::before, *::after { -webkit-print-color-adjust: economy !important; print-color-adjust: economy !important; " +
			"background: none !important; background-color: transparent !important; background-image: none !important; }\n"
	}
	return "<style>" + css + "</style>"
}
