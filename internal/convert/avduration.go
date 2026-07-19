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

// avStreamProbe mirrors ffprobe's `-show_entries
// stream=codec_name,width,height -of json` output shape (34-RESEARCH.md
// Code Examples, live-verified against ffmpeg/ffprobe 8.1.2) -- kept as its
// own unexported type so probeVideoStream's JSON parse is independently
// reasoned about, mirroring ProbeDuration's runCommand+parse split
// (audioduration.go).
type avStreamProbe struct {
	Streams []struct {
		CodecName string `json:"codec_name"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
	} `json:"streams"`
}

// ffprobeStreamArgs builds ffprobe's argv for probeVideoStream, isolated as
// its own function so the "-protocol_whitelist file,crypto" hardening
// (AVE-02, closes T-34-08) and the "file:" prefix (IN-01 precedent,
// audioduration.go) are unit-testable without invoking a real ffprobe
// subprocess (mirrors ffprobeDurationArgs's argv-pinning test style).
func ffprobeStreamArgs(path string) []string {
	return []string{"-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=codec_name,width,height",
		"-of", "json", "-protocol_whitelist", "file,crypto", "file:" + path}
}

// probeVideoStream runs ffprobe as its own short, bounded, killable
// subprocess (runCommand, exec.go) to read the container's declared video
// codec/resolution BEFORE any decode/transcode step runs -- fails closed on
// an unparseable probe, a missing video stream, or an implausible (<= 0)
// width/height.
func probeVideoStream(ctx context.Context, path string) (codec string, width, height int, err error) {
	out, err := runCommand(ctx, "ffprobe", ffprobeStreamArgs(path)...)
	if err != nil {
		return "", 0, 0, fmt.Errorf("ffprobe: %w", err)
	}
	var probe avStreamProbe
	if err := json.Unmarshal(out, &probe); err != nil || len(probe.Streams) == 0 {
		return "", 0, 0, fmt.Errorf("ffprobe: no video stream found or unparseable output")
	}
	s := probe.Streams[0]
	if s.Width <= 0 || s.Height <= 0 {
		return "", 0, 0, fmt.Errorf("ffprobe: implausible resolution %dx%d", s.Width, s.Height)
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
	_, _, height, err := probeVideoStream(ctx, path)
	if err != nil {
		return err
	}
	if height > maxHeight {
		return fmt.Errorf("%w: declared height %d exceeds ceiling %d", ErrAVResolutionExceeded, height, maxHeight)
	}
	return nil
}
