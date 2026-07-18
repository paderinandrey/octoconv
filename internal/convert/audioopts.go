package convert

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// audioLanguageAllowlist is the closed set of accepted `language` values
// (AUD-03/OPTS-01 precedent). "auto" maps to whisper-cli's own `-l auto`
// (language auto-detect); every other entry maps 1:1 to a
// whisper.cpp/Whisper ISO-639-1-ish language code. This is a deliberate
// minimal closed set covering auto-detect plus the internal clients'
// immediate languages (Russian-first company, common Latin-script
// languages) -- intentionally closed and extended per real client demand,
// never open-ended. Never accept an arbitrary client string here -- a raw
// string reaching whisper-cli's argv is the audio-engine's analog of the
// PDF/A FilterOptions injection risk OPTS-01/02 already closed once for
// LibreOffice (Pitfall 11).
var audioLanguageAllowlist = map[string]bool{
	"auto": true,
	"en":   true,
	"ru":   true,
	"es":   true,
	"fr":   true,
	"de":   true,
}

// AudioOpts is the closed, strictly-parsed set of client-requested
// transcription options (AUD-03/OPTS-01 precedent). Language is validated
// against the closed audioLanguageAllowlist above -- it is never passed as
// a raw client string into whisper-cli's -l argv flag. Mirrors DocOpts's
// enum pattern (opts.go) selected via a map lookup (htmlopts.go's style)
// only because the language code list is longer than a single constant;
// the SELECTION mechanism (map lookup, never string concat) is identical.
//
// Once validated, Language reaches whisper-cli ONLY as a discrete
// exec.Command argv slice element (e.g. `args = append(args, "-l",
// o.Language)`, Plan 03) -- runCommand (exec.go) never invokes a shell, so
// there is no shell-metacharacter injection surface at all. The allowlist
// closes the remaining risk: an unvalidated string reaching a future
// path-shaped opt (e.g. a model selector) could enable path traversal
// (Pitfall 11); a future AudioOpts extension MUST re-derive this same
// allowlist/server-constant discipline rather than concatenating client
// bytes into a path.
type AudioOpts struct {
	Language  string `json:"language,omitempty"`
	Translate bool   `json:"translate,omitempty"`
}

// ParseAudioOpts strict-decodes raw JSON into an AudioOpts and validates
// Language against the closed audioLanguageAllowlist. Strictness (exactly
// one top-level JSON object, no duplicate keys, no trailing bytes, no
// top-level null, unknown fields rejected) is enforced by the shared
// checkStrictObject helper (opts.go) -- reused verbatim, not duplicated
// (D-10 parity with ParseDocOpts/ParseHTMLOpts). An empty `{}` or absent
// opts is valid and yields a zero AudioOpts.
func ParseAudioOpts(raw []byte) (AudioOpts, error) {
	if err := checkStrictObject(raw); err != nil {
		return AudioOpts{}, err
	}
	var o AudioOpts
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&o); err != nil {
		return AudioOpts{}, fmt.Errorf("parse opts: %w", err)
	}
	if o.Language != "" && !audioLanguageAllowlist[o.Language] {
		return AudioOpts{}, fmt.Errorf("unsupported language %q", o.Language)
	}
	return o, nil
}

// AudioOptsFromMap round-trips a persisted map[string]any (job.Opts,
// already unmarshaled from the jobs.options jsonb column by internal/jobs)
// through ParseAudioOpts -- the same strictness applied on the
// worker/converter read path as on the API write path (D-10, mirrors
// DocOptsFromMap/HTMLOptsFromMap). A nil/empty map yields a zero AudioOpts,
// no error.
func AudioOptsFromMap(m map[string]any) (AudioOpts, error) {
	if len(m) == 0 {
		return AudioOpts{}, nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return AudioOpts{}, fmt.Errorf("marshal opts: %w", err)
	}
	return ParseAudioOpts(raw)
}

// ValidateAudioApplicability rejects opts that do not apply to the given
// (engine, source, target) conversion. Empty opts always apply (nothing
// was requested). A non-empty AudioOpts applies only to audio-engine
// conversions. Deliberately its OWN function scoped to EngineAudio, not
// merged into the shared ValidateApplicability (opts.go) -- mirrors
// ValidateHTMLApplicability's engine-scoped shape (htmlopts.go).
func ValidateAudioApplicability(engine, source, target string, o AudioOpts) error {
	if isZeroAudioOpts(o) {
		return nil
	}
	if engine != EngineAudio {
		return fmt.Errorf("audio transcription options are only valid for audio-engine conversions")
	}
	return nil
}

// isZeroAudioOpts reports whether o carries no client-requested options at
// all -- the same "empty opts always apply" shortcut ValidateApplicability
// uses for DocOpts/HTMLOpts (opts.go, htmlopts.go), generalized to
// AudioOpts's 2 fields.
func isZeroAudioOpts(o AudioOpts) bool {
	return o == AudioOpts{}
}
