package convert

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrAVResolutionExceeded is returned when the probed video resolution
// exceeds the configured maximum height -- fail-closed (mirrors
// ErrAudioDurationExceeded's shape/doc-comment, audioduration.go), rejecting
// a huge-resolution decode bomb before the expensive ffmpeg stage ever runs
// (AVE-02/T-34-07).
var ErrAVResolutionExceeded = errors.New("declared video resolution exceeds configured maximum")

// ErrAVNoVideoStream classifies ffprobe reporting zero video streams --
// IN-01 fold-in (34-REVIEW-FIX.md "Residual Risk / Follow-ups for Phase
// 35"): a generic-brand ISOBMFF audio-only file (major brand
// isom/mp42, not the M4A /M4B brands m4aBrands checks) sniffs as mp4 and
// routes to the av engine once AVConverter registers, then dies here. Before
// this sentinel all three call sites in this package (avProbeSource in
// av.go, probeVideoStreams and probeVideoStream below) emitted the identical
// anonymous fmt.Errorf("ffprobe: no video stream found"), giving the worker
// no way to mark such a job terminal with a distinguishable error_code
// instead of retrying a hopeless job.
var ErrAVNoVideoStream = errors.New("ffprobe: no video stream found")

// avVideoStream is one decoded entry from ffprobe's video-stream listing.
// Index is the stream's ABSOLUTE index within the container (not its
// video-relative ordinal) so a later ffmpeg "-map 0:<Index>" selects
// provably the same stream this probe inspected (CR-03).
type avVideoStream struct {
	Index       int
	CodecName   string
	Width       int
	Height      int
	AttachedPic bool
}

// avStreamProbe mirrors ffprobe's `-show_entries
// stream=index,codec_name,width,height:stream_disposition=attached_pic -of
// json` output shape (34-RESEARCH.md Code Examples, live-verified against
// ffmpeg/ffprobe 8.1.2) -- kept as its own unexported type so
// probeVideoStreams's JSON parse is independently reasoned about, mirroring
// ProbeDuration's runCommand+parse split (audioduration.go).
type avStreamProbe struct {
	Streams []struct {
		Index       int    `json:"index"`
		CodecName   string `json:"codec_name"`
		Width       int    `json:"width"`
		Height      int    `json:"height"`
		Disposition struct {
			AttachedPic int `json:"attached_pic"`
		} `json:"disposition"`
	} `json:"streams"`
}

// ffprobeStreamArgs builds ffprobe's argv for probeVideoStreams, isolated as
// its own function so the "-protocol_whitelist file,crypto" hardening
// (AVE-02, closes T-34-08) and the "file:" prefix (IN-01 precedent,
// audioduration.go) are unit-testable without invoking a real ffprobe
// subprocess (mirrors ffprobeDurationArgs's argv-pinning test style).
//
// CR-03: this deliberately selects EVERY video stream ("-select_streams v"),
// not just "v:0". A container may carry embedded cover art, which ffprobe
// reports as a video stream -- if the probe stopped at v:0 it could report a
// tiny mjpeg thumbnail's codec and height while an 8K real video stream sat
// at v:1, silently defeating BOTH the stream-copy codec contract and the
// resolution axis of the decode-bomb guard. attached_pic is requested so
// cover art can be told apart from real video.
func ffprobeStreamArgs(path string) []string {
	return []string{"-v", "error", "-select_streams", "v",
		"-show_entries", "stream=index,codec_name,width,height:stream_disposition=attached_pic",
		"-of", "json", "-protocol_whitelist", "file,crypto", "file:" + path}
}

// probeVideoStreams runs ffprobe as its own short, bounded, killable
// subprocess (runCommand, exec.go) to read EVERY declared video stream's
// codec/resolution BEFORE any decode/transcode step runs -- fails closed on
// an unparseable probe, a container with no video stream at all, or any
// stream with an implausible (<= 0) width/height.
func probeVideoStreams(ctx context.Context, path string) ([]avVideoStream, error) {
	out, err := runCommand(ctx, "ffprobe", ffprobeStreamArgs(path)...)
	if err != nil {
		return nil, fmt.Errorf("ffprobe: %w", err)
	}
	var probe avStreamProbe
	if err := json.Unmarshal(out, &probe); err != nil {
		return nil, fmt.Errorf("ffprobe: unparseable stream probe output: %w", err)
	}
	if len(probe.Streams) == 0 {
		return nil, fmt.Errorf("%w", ErrAVNoVideoStream)
	}
	streams := make([]avVideoStream, 0, len(probe.Streams))
	for _, s := range probe.Streams {
		if s.Width <= 0 || s.Height <= 0 {
			return nil, fmt.Errorf("ffprobe: implausible resolution %dx%d", s.Width, s.Height)
		}
		streams = append(streams, avVideoStream{
			Index:       s.Index,
			CodecName:   s.CodecName,
			Width:       s.Width,
			Height:      s.Height,
			AttachedPic: s.Disposition.AttachedPic == 1,
		})
	}
	return streams, nil
}

// avPrimaryVideoStream picks the stream that "the video" means for every
// downstream decision: the largest-area stream that is NOT embedded cover
// art. Cover art is excluded first and only falls back to being eligible if
// the container carries nothing else, so a real video stream always wins over
// an attached picture regardless of container stream order (CR-03).
func avPrimaryVideoStream(streams []avVideoStream) (avVideoStream, bool) {
	best, found := avVideoStream{}, false
	for _, s := range streams {
		if s.AttachedPic {
			continue
		}
		if !found || s.Width*s.Height > best.Width*best.Height {
			best, found = s, true
		}
	}
	if found {
		return best, true
	}
	// Cover-art-only container: still return something so callers fail on a
	// contract check rather than on a missing value.
	for _, s := range streams {
		if !found || s.Width*s.Height > best.Width*best.Height {
			best, found = s, true
		}
	}
	return best, found
}

// avMaxVideoHeight returns the tallest declared height across ALL video
// streams, cover art included. The resolution guard deliberately uses the
// maximum rather than the primary stream's height: a decode-bomb hidden in
// any stream of the container must trip the ceiling, even one this pipeline
// does not currently intend to map (fail-closed, CR-03).
func avMaxVideoHeight(streams []avVideoStream) int {
	max := 0
	for _, s := range streams {
		if s.Height > max {
			max = s.Height
		}
	}
	return max
}

// probeVideoStream reports the primary (largest non-cover-art) video
// stream's declared codec and resolution -- the narrow accessor most callers
// want, layered over probeVideoStreams so the cover-art exclusion applies
// uniformly.
func probeVideoStream(ctx context.Context, path string) (codec string, width, height int, err error) {
	streams, err := probeVideoStreams(ctx, path)
	if err != nil {
		return "", 0, 0, err
	}
	s, ok := avPrimaryVideoStream(streams)
	if !ok {
		return "", 0, 0, fmt.Errorf("%w", ErrAVNoVideoStream)
	}
	return s.CodecName, s.Width, s.Height, nil
}

// EnforceMaxResolution probes path's declared video resolution and rejects
// it with ErrAVResolutionExceeded when the height exceeds maxHeight --
// fail-closed BEFORE the expensive decode/transcode stage ever runs
// (mirrors EnforceMaxDuration's fail-closed shape, audioduration.go);
// complements the reused duration-guard axis for a multi-axis decode-bomb
// defense (T-34-07). maxHeight is a plain parameter, NOT read from any env
// var here -- the API layer wires the actual ceiling in a later
// out-of-scope phase (mirrors EnforceMaxDuration's own note).
func EnforceMaxResolution(ctx context.Context, path string, maxHeight int) error {
	streams, err := probeVideoStreams(ctx, path)
	if err != nil {
		return err
	}
	return enforceMaxResolutionOf(streams, maxHeight)
}

// enforceMaxResolutionOf applies the height ceiling to an ALREADY-probed
// stream list, so the AV engine's guard stage can reuse a single probe
// instead of spawning another ffprobe (WR-05).
func enforceMaxResolutionOf(streams []avVideoStream, maxHeight int) error {
	if height := avMaxVideoHeight(streams); height > maxHeight {
		return fmt.Errorf("%w: declared height %d exceeds ceiling %d", ErrAVResolutionExceeded, height, maxHeight)
	}
	return nil
}
