package convert

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
)

// avResolutionHeights is the closed set of accepted resolution_height values
// (AVO-02) -- deliberately a small fixed enum (480/720/1080), never an
// arbitrary client-supplied WxH pair, mirroring audioLanguageAllowlist's
// map-lookup selection discipline (audioopts.go).
var avResolutionHeights = map[int]bool{
	480:  true,
	720:  true,
	1080: true,
}

// avCodecAllowlist is the closed set of accepted codec values (AVO-03).
// "h264" is the default/baseline transcode codec; "hevc" is the opt-in
// alternative with its OWN CRF default (x265DefaultCRF below) -- never
// x264DefaultCRF's value (Pitfall 4, 34-RESEARCH.md).
var avCodecAllowlist = map[string]bool{
	"h264": true,
	"hevc": true,
}

// avThumbnailTargets is the closed set of output formats a non-zero
// Timecode may apply to -- mirrors ValidateApplicability's
// NormalizeFormat(target)=="pdf" gating shape (opts.go), generalized to a
// small target set instead of a single format.
var avThumbnailTargets = map[string]bool{
	"jpg":  true,
	"png":  true,
	"webp": true,
}

// avTranscodeTargets is the closed set of output formats a non-zero
// ResolutionHeight may apply to.
var avTranscodeTargets = map[string]bool{
	"mp4":  true,
	"webm": true,
}

// x264DefaultCRF is the server-constant default CRF for the H.264 transcode
// path. NOT interchangeable with x265DefaultCRF: x264 and x265's CRF scales
// are not directly comparable -- x265's perceptually-equivalent CRF values
// run several points higher than x264's for similar visual quality
// (34-RESEARCH.md Pitfall 4, live-verified this phase). A future edit adding
// an HEVC code path MUST NOT reuse this constant.
const x264DefaultCRF = 23

// x265DefaultCRF is the server-constant default CRF for the HEVC transcode
// path, deliberately its OWN value -- never copied from x264DefaultCRF
// (AVO-03, Pitfall 4). CRF 28 was live-verified this phase to produce a
// valid, ffprobe-confirmed hevc output; treat as a starting point for
// quality tuning, not a proven perceptual-parity match to x264's CRF 23.
const x265DefaultCRF = 28

// AVOpts is the closed, strictly-parsed set of client-requested AV
// (transcode/thumbnail) conversion options (AVO-01/AVO-02/AVO-03). Every
// field is validated against a fixed allowlist/range by ParseAVOpts -- no
// field here ever carries raw, unvalidated client bytes past the parse
// boundary; once validated, ResolutionHeight/Codec select a server-side
// constant (scale filter, CRF), they are never concatenated into ffmpeg
// argv directly (mirrors PDFAFilterOptions's enum-to-server-constant
// mapping, opts.go).
type AVOpts struct {
	// Timecode is the thumbnail seek point in seconds (thumbnail targets
	// only), range-checked here (>= 0); duration-relative clamping happens
	// later, in Convert, not here.
	//
	// A POINTER, deliberately (CR-04): with a plain float64 an explicit
	// {"timecode": 0} -- a legitimate request for the very first frame -- was
	// byte-identical to an absent field, so it was silently rewritten to the
	// 1.0s default AND treated by isZeroAVOpts as "no options requested",
	// short-circuiting ValidateAVApplicability. nil now means "unset" and
	// only nil takes the default.
	Timecode *float64 `json:"timecode,omitempty"`
	// ResolutionHeight is the target output height in pixels (transcode
	// targets only), validated against the closed avResolutionHeights enum
	// -- never an arbitrary client WxH pair (AVO-02).
	ResolutionHeight int `json:"resolution_height,omitempty"`
	// Codec is the target video codec (transcode targets only, mp4 only),
	// validated against the closed avCodecAllowlist -- selects
	// x264DefaultCRF or x265DefaultCRF server-side, never a raw client value
	// reaching ffmpeg's -c:v argv directly (AVO-03).
	Codec string `json:"codec,omitempty"`
}

// ParseAVOpts strict-decodes raw JSON into an AVOpts and validates every
// field against its closed allowlist/range. Strictness (exactly one
// top-level JSON object, no duplicate keys, no trailing bytes, no top-level
// null, unknown fields rejected) is enforced by the shared checkStrictObject
// helper (opts.go) -- reused verbatim, not duplicated (mirrors
// ParseAudioOpts/ParseDocOpts, D-10 parity). An empty `{}` or absent opts is
// valid and yields a zero AVOpts.
func ParseAVOpts(raw []byte) (AVOpts, error) {
	if err := checkStrictObject(raw); err != nil {
		return AVOpts{}, err
	}
	var o AVOpts
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&o); err != nil {
		return AVOpts{}, fmt.Errorf("parse opts: %w", err)
	}
	if o.Timecode != nil && (*o.Timecode < 0 || math.IsNaN(*o.Timecode) || math.IsInf(*o.Timecode, 0)) {
		return AVOpts{}, fmt.Errorf("timecode out of range %v", *o.Timecode)
	}
	if o.ResolutionHeight != 0 && !avResolutionHeights[o.ResolutionHeight] {
		return AVOpts{}, fmt.Errorf("unsupported resolution_height %d", o.ResolutionHeight)
	}
	if o.Codec != "" && !avCodecAllowlist[o.Codec] {
		return AVOpts{}, fmt.Errorf("unsupported codec %q", o.Codec)
	}
	return o, nil
}

// AVOptsFromMap round-trips a persisted map[string]any (job.Opts, already
// unmarshaled from the jobs.options jsonb column by internal/jobs) through
// ParseAVOpts -- the same strictness applied on the worker/converter read
// path as on the API write path (D-10, mirrors AudioOptsFromMap/
// DocOptsFromMap). A nil/empty map yields a zero AVOpts, no error.
func AVOptsFromMap(m map[string]any) (AVOpts, error) {
	if len(m) == 0 {
		return AVOpts{}, nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return AVOpts{}, fmt.Errorf("marshal opts: %w", err)
	}
	return ParseAVOpts(raw)
}

// ValidateAVApplicability rejects opts that do not apply to the given
// (engine, source, target) conversion. Empty opts always apply (nothing was
// requested). A non-zero Timecode applies only to thumbnail targets
// (jpg/png/webp); a non-zero ResolutionHeight applies only to transcode
// targets (mp4/webm); Codec=="hevc" applies only to an mp4 target -- all
// gated on engine==EngineAV. Deliberately its own function scoped to
// EngineAV, not merged into the shared ValidateApplicability (opts.go) --
// mirrors ValidateAudioApplicability's engine-scoped shape (audioopts.go).
func ValidateAVApplicability(engine, source, target string, o AVOpts) error {
	if isZeroAVOpts(o) {
		return nil
	}
	if engine != EngineAV {
		return fmt.Errorf("av options are only valid for av-engine conversions")
	}
	normTarget := NormalizeFormat(target)
	if o.Timecode != nil && !avThumbnailTargets[normTarget] {
		return fmt.Errorf("timecode is only valid for thumbnail targets")
	}
	if o.ResolutionHeight != 0 && !avTranscodeTargets[normTarget] {
		return fmt.Errorf("resolution_height is only valid for transcode targets")
	}
	if o.Codec == "hevc" && normTarget != "mp4" {
		return fmt.Errorf("codec hevc is only valid for mp4 targets")
	}
	return nil
}

// isZeroAVOpts reports whether o carries no client-requested options at all
// -- the same "empty opts always apply" shortcut ValidateApplicability uses
// for DocOpts/AudioOpts (opts.go, audioopts.go), generalized to AVOpts's 3
// fields. Note Timecode is checked for nil, not for 0: an explicit
// {"timecode": 0} IS a request and must still be applicability-checked
// (CR-04).
func isZeroAVOpts(o AVOpts) bool {
	return o.Timecode == nil && o.ResolutionHeight == 0 && o.Codec == ""
}
