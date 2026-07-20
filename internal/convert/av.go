package convert

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// avTranscodeToMP4Sources is the closed set of source containers valid for
// the mov/avi/mkv/webm -> mp4 transcode path (AVC-01). Note mp4 itself is a
// transcode TARGET here, not a source -- it is added as a source only for
// audio-extract/thumbnail below.
var avTranscodeToMP4Sources = []string{"mov", "avi", "mkv", "webm"}

// avExtractSources is the closed set of source containers valid for BOTH
// the audio-extract (AVC-03) and thumbnail-extract (AVC-04) paths -- all
// five detected video formats (34-RESEARCH.md Open Question 1, RESOLVED
// this task: "video" reads as source-format-agnostic for these two
// features).
var avExtractSources = []string{"mp4", "mov", "avi", "mkv", "webm"}

// avAudioExtractTargets is the closed set of audio-extract output formats
// (AVC-03).
var avAudioExtractTargets = []string{"mp3", "wav", "m4a"}

// avThumbnailExtractTargets is the closed set of thumbnail-extract output
// formats (AVC-04).
var avThumbnailExtractTargets = []string{"jpg", "png", "webp"}

// AVConverter transcodes video, extracts audio tracks, and extracts
// thumbnails by shelling out to `ffmpeg`/`ffprobe` -- the fifth engine class
// (EngineAV), mirroring AudioConverter's (whisper.go) shape: a plain struct,
// Pairs()/Engine()/Convert(), argv builders isolated as their own pure
// functions for unit-testability. It is built and unit-tested this phase
// directly against a real ffmpeg/ffprobe binary but deliberately NOT
// registered into convert.Default (Phase 34 scope fence, mirrors Phase 30's
// own AudioConverter fence) -- registration is Phase 35's responsibility.
type AVConverter struct{}

// Pairs returns the locked pair set (34-RESEARCH.md Open Question 1,
// RESOLVED 34-03 Task 1): transcode {mov,avi,mkv,webm}->mp4 plus mp4->webm
// (AVC-01/AVC-02); audio-extract from all five detected video formats to
// mp3/wav/m4a (AVC-03); thumbnail from all five detected video formats to
// jpg/png/webp (AVC-04). This pair set shapes Phase 35's cross-converter
// disjointness test against AudioConverter.Pairs() -- do not change it
// without updating that Phase 35 test's expectations.
func (AVConverter) Pairs() []Pair {
	pairs := make([]Pair, 0,
		len(avTranscodeToMP4Sources)+1+
			len(avExtractSources)*(len(avAudioExtractTargets)+len(avThumbnailExtractTargets)))
	for _, from := range avTranscodeToMP4Sources {
		pairs = append(pairs, Pair{From: from, To: "mp4"})
	}
	pairs = append(pairs, Pair{From: "mp4", To: "webm"})
	for _, from := range avExtractSources {
		for _, to := range avAudioExtractTargets {
			pairs = append(pairs, Pair{From: from, To: to})
		}
	}
	for _, from := range avExtractSources {
		for _, to := range avThumbnailExtractTargets {
			pairs = append(pairs, Pair{From: from, To: to})
		}
	}
	return pairs
}

// Engine reports the AV engine class (D-01).
func (AVConverter) Engine() string { return EngineAV }

// transcodeToMP4Args builds ffmpeg's argv for the mov/avi/mkv/webm -> mp4
// full re-encode path (AVC-01): H.264/AAC with +faststart. codec selects
// libx264 (default, or the explicit "h264" client choice) or libx265
// ("hevc", AVO-03) -- the HEVC branch uses x265DefaultCRF, NEVER
// x264DefaultCRF (Pitfall 4, 34-RESEARCH.md). Every element leads with the
// AVE-02 hardening flags (-protocol_whitelist file,crypto) and a
// "file:"-prefixed -i path (IN-01 precedent, mirrors
// ffmpegNormalizeArgs/ffprobeDurationArgs).
func transcodeToMP4Args(inPath, outPath, codec string, threads int) []string {
	videoCodec := "libx264"
	crf := x264DefaultCRF
	if codec == "hevc" {
		videoCodec = "libx265"
		crf = x265DefaultCRF
	}
	return []string{
		"-y", "-nostdin", "-protocol_whitelist", "file,crypto",
		"-i", "file:" + inPath,
		"-c:v", videoCodec, "-preset", "veryfast", "-crf", strconv.Itoa(crf),
		"-c:a", "aac", "-b:a", "128k",
		"-movflags", "+faststart",
		"-threads", strconv.Itoa(threads),
		outPath,
	}
}

// transcodeToWebMArgs builds ffmpeg's argv for the mp4 -> webm path
// (AVC-02): VP9/Opus, ALWAYS a full re-encode -- this builder never selects
// a stream-copy fast path; the caller (Convert) decides whether to call this
// builder at all vs. issuing a raw "-c copy" invocation, gated on
// avStreamCopyLegal.
func transcodeToWebMArgs(inPath, outPath string, threads int) []string {
	return []string{
		"-y", "-nostdin", "-protocol_whitelist", "file,crypto",
		"-i", "file:" + inPath,
		"-c:v", "libvpx-vp9", "-b:v", "1M",
		"-c:a", "libopus",
		"-threads", strconv.Itoa(threads),
		outPath,
	}
}

// extractAudioArgs builds ffmpeg's argv for the video -> mp3/wav/m4a
// audio-extract path (AVC-03). -vn drops the video stream entirely.
// streamCopy selects "-c:a copy" (source already AAC, target m4a) instead of
// a target-specific re-encode codec -- the caller (Convert) decides
// streamCopy via an ffprobe codec check, never this function.
func extractAudioArgs(inPath, outPath, target string, streamCopy bool) []string {
	args := []string{
		"-y", "-nostdin", "-protocol_whitelist", "file,crypto",
		"-i", "file:" + inPath,
		"-vn",
	}
	if streamCopy {
		args = append(args, "-c:a", "copy")
	} else {
		switch target {
		case "mp3":
			args = append(args, "-c:a", "libmp3lame", "-q:a", "2")
		case "wav":
			args = append(args, "-c:a", "pcm_s16le")
		case "m4a":
			args = append(args, "-c:a", "aac", "-b:a", "128k")
		}
	}
	return append(args, outPath)
}

// thumbnailArgs builds ffmpeg's argv for the video -> jpg/png/webp
// thumbnail-extract path (AVC-04): input-side -ss (fast seek near the
// target keyframe, BEFORE -i, per 34-RESEARCH.md "State of the Art") and an
// EXPLICIT per-target -c:v -- never relying on ffmpeg's extension-based
// auto-selection, which is known to fail for at least one target on at
// least one real ffmpeg build (Pitfall 3, 34-RESEARCH.md).
func thumbnailArgs(inPath, outPath, target string, timecode float64) []string {
	args := []string{
		"-y", "-nostdin", "-protocol_whitelist", "file,crypto",
		"-ss", strconv.FormatFloat(timecode, 'f', -1, 64),
		"-i", "file:" + inPath,
		"-frames:v", "1",
	}
	switch target {
	case "jpg":
		args = append(args, "-c:v", "mjpeg", "-q:v", "2")
	case "png":
		args = append(args, "-c:v", "png")
	case "webp":
		args = append(args, "-c:v", "libwebp")
	}
	return append(args, outPath)
}

// avMaxSourceDuration is the Phase-34 plausibility ceiling on a source
// video's declared duration, enforced by the guard stage (EnforceMaxDuration,
// reused verbatim) BEFORE any ffmpeg decode/encode (AVE-02/T-34-12). A plain
// package constant, NOT read from any env var here -- the actual
// AV_MAX_DURATION_SECONDS wiring is deferred to Phase 36 (mirrors
// EnforceMaxDuration's own "plain parameter" contract, 34-RESEARCH.md Open
// Question 2, RESOLVED).
const avMaxSourceDuration = 4 * time.Hour

// avMaxSourceResolutionHeight is the Phase-34 plausibility ceiling on a
// source video's declared height, enforced by the guard stage
// (EnforceMaxResolution) BEFORE any ffmpeg decode/encode. Also a plain
// package constant -- env wiring deferred to Phase 36, same as above. 4320
// (8K UHD) generously covers legitimate client uploads while still bounding
// a resolution decode-bomb.
const avMaxSourceResolutionHeight = 4320

// ErrAVOutputMissingOrEmpty classifies ffmpeg's "exit 0 but produced nothing
// usable" failure mode -- an out-of-range thumbnail -ss produces NO output
// file at all, not an empty one (34-RESEARCH.md Pitfall 2); both shapes (a
// missing file and a zero-byte file) map to this SAME class so a caller can
// errors.Is-match "no usable output" regardless of which guard caught it.
// The thumbnail path's pre-flight bounds check below reuses this same
// sentinel, not a separate ad hoc error, for the identical reason.
var ErrAVOutputMissingOrEmpty = errors.New("av: output missing or empty")

// avStreamCopyLegal reports whether srcVideoCodec/srcAudioCodec are already
// legal in targetContainer per THIS PROJECT's own AVC-01/AVC-02 codec
// contract -- NEVER derived from what ffmpeg's muxer happens to accept
// (live-verified in 34-RESEARCH.md: ffmpeg's mp4 muxer accepts a VP9/Opus
// `-c copy` remux without error, which would silently violate the
// mp4-target=H.264/AAC contract if this check were skipped, AVC-05/T-34-11).
func avStreamCopyLegal(targetContainer, srcVideoCodec, srcAudioCodec string) bool {
	switch targetContainer {
	case "mp4":
		return srcVideoCodec == "h264" && srcAudioCodec == "aac"
	case "webm":
		return srcVideoCodec == "vp9" && srcAudioCodec == "opus"
	default:
		return false
	}
}

// ffprobeAudioCodecArgs builds ffprobe's argv for the stream-copy
// eligibility audio-codec probe (AVC-05), hardened identically to every
// other ffprobe/ffmpeg invocation in this file (AVE-02: -protocol_whitelist
// file,crypto + "file:"-prefixed path).
func ffprobeAudioCodecArgs(path string) []string {
	return []string{"-v", "error", "-select_streams", "a:0",
		"-show_entries", "stream=codec_name", "-of", "csv=p=0",
		"-protocol_whitelist", "file,crypto", "file:" + path}
}

// probeAudioCodec runs ffprobe to read the source's declared audio codec --
// used by both the transcode path's stream-copy-eligibility check and the
// audio-extract path's AAC-source->m4a stream-copy check (AVC-03/AVC-05).
func probeAudioCodec(ctx context.Context, path string) (string, error) {
	out, err := runCommand(ctx, "ffprobe", ffprobeAudioCodecArgs(path)...)
	if err != nil {
		return "", fmt.Errorf("ffprobe: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// avThreadCount resolves the -threads value passed to every ffmpeg
// invocation in this file. A plain runtime.NumCPU() read -- no env var, no
// cgroup-aware sizing wired yet (that mechanism, CgroupCPULimit, is reusable
// verbatim by a LATER phase per 34-PATTERNS.md's "Cgroup-aware thread
// sizing" note; wiring it here would be premature for a converter that is
// not yet registered/queued).
func avThreadCount() int {
	return runtime.NumCPU()
}

// validateAVOutput guards against ffmpeg's "exit 0 but produced nothing
// usable" failure mode (Pitfall 2): a missing file (os.Stat error) and a
// zero-byte file map to the SAME ErrAVOutputMissingOrEmpty class. This is
// the minimal (non-thumbnail-aware) form -- Task 3 hardens it to also
// re-Sniff() a thumbnail's bytes.
func validateAVOutput(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAVOutputMissingOrEmpty, err)
	}
	if fi.Size() == 0 {
		return ErrAVOutputMissingOrEmpty
	}
	return nil
}

// Convert dispatches on the target format (transcode/audio-extract/
// thumbnail), runs the duration+resolution guard stage BEFORE any ffmpeg
// decode/encode (AVE-02/T-34-12), then invokes exactly one of the three
// ffmpeg pipelines. Mirrors AudioConverter.Convert's shape (whisper.go):
// AVOptsFromMap -> fail-fast target dispatch -> guard stage -> subprocess
// stage(s), each wrapped with a distinguishable "av: <stage>:" error prefix.
func (c AVConverter) Convert(ctx context.Context, inPath, outPath string, opts map[string]any) error {
	o, err := AVOptsFromMap(opts)
	if err != nil {
		return fmt.Errorf("av: %w", err)
	}

	// Fail fast on an unsupported target BEFORE the guard stage/any
	// subprocess runs -- mirrors AudioConverter.Convert's shape
	// (whisper.go): a caller bug (unrecognized/missing outPath extension)
	// must not burn an ffprobe+ffmpeg round trip before failing.
	targetFormat := NormalizeFormat(filepath.Ext(outPath))
	switch targetFormat {
	case "mp4", "webm", "mp3", "wav", "m4a", "jpg", "png", "webp":
	default:
		return fmt.Errorf("av: unsupported target format %q", targetFormat)
	}

	// Guard stage (AVE-02/T-34-12): duration + resolution ceilings enforced
	// BEFORE any ffmpeg encode/decode -- multi-axis decompression-bomb
	// defense, both fail-closed and independently required.
	if err := EnforceMaxDuration(ctx, inPath, avMaxSourceDuration); err != nil {
		return fmt.Errorf("av: %w", err)
	}
	if err := EnforceMaxResolution(ctx, inPath, avMaxSourceResolutionHeight); err != nil {
		return fmt.Errorf("av: %w", err)
	}

	switch targetFormat {
	case "mp4", "webm":
		return c.convertTranscode(ctx, inPath, outPath, targetFormat, o)
	case "mp3", "wav", "m4a":
		return c.convertAudioExtract(ctx, inPath, outPath, targetFormat)
	default:
		return c.convertThumbnail(ctx, inPath, outPath, targetFormat, o)
	}
}

// convertTranscode implements AVC-01/AVC-02/AVC-05: probe the source's
// declared video+audio codec, gate a "-c copy" remux on avStreamCopyLegal
// (never on ffmpeg's own muxer acceptance), else full re-encode via
// transcodeToMP4Args (HEVC-aware) or transcodeToWebMArgs.
func (c AVConverter) convertTranscode(ctx context.Context, inPath, outPath, target string, o AVOpts) error {
	srcVideoCodec, _, _, err := probeVideoStream(ctx, inPath)
	if err != nil {
		return fmt.Errorf("av: ffprobe: %w", err)
	}
	srcAudioCodec, err := probeAudioCodec(ctx, inPath)
	if err != nil {
		return fmt.Errorf("av: ffprobe: %w", err)
	}

	var args []string
	if avStreamCopyLegal(target, srcVideoCodec, srcAudioCodec) {
		args = []string{
			"-y", "-nostdin", "-protocol_whitelist", "file,crypto",
			"-i", "file:" + inPath,
			"-c", "copy",
		}
		if target == "mp4" {
			args = append(args, "-movflags", "+faststart")
		}
		args = append(args, outPath)
	} else {
		switch target {
		case "mp4":
			args = transcodeToMP4Args(inPath, outPath, o.Codec, avThreadCount())
		case "webm":
			args = transcodeToWebMArgs(inPath, outPath, avThreadCount())
		}
	}

	if _, err := runCommand(ctx, "ffmpeg", args...); err != nil {
		return fmt.Errorf("av: ffmpeg: %w", err)
	}
	return validateAVOutput(outPath)
}

// convertAudioExtract implements AVC-03: extract the audio track only
// (-vn), using "-c:a copy" only when the source is already AAC and the
// target is m4a, else a target-specific re-encode.
func (c AVConverter) convertAudioExtract(ctx context.Context, inPath, outPath, target string) error {
	streamCopy := false
	if target == "m4a" {
		srcAudioCodec, err := probeAudioCodec(ctx, inPath)
		if err != nil {
			return fmt.Errorf("av: ffprobe: %w", err)
		}
		streamCopy = srcAudioCodec == "aac"
	}
	if _, err := runCommand(ctx, "ffmpeg", extractAudioArgs(inPath, outPath, target, streamCopy)...); err != nil {
		return fmt.Errorf("av: ffmpeg: %w", err)
	}
	return validateAVOutput(outPath)
}

// convertThumbnail implements AVC-04: default timecode 1.0s when unset,
// pre-flight bounds-checked against ProbeDuration BEFORE invoking ffmpeg at
// all (Pitfall 2 -- an out-of-range -ss produces NO output file, so
// rejecting up front is strictly better than letting ffmpeg silently no-op).
func (c AVConverter) convertThumbnail(ctx context.Context, inPath, outPath, target string, o AVOpts) error {
	timecode := o.Timecode
	if timecode == 0 {
		timecode = 1.0
	}
	dur, err := ProbeDuration(ctx, inPath)
	if err != nil {
		return fmt.Errorf("av: ffprobe: %w", err)
	}
	if timecode >= dur.Seconds() {
		return fmt.Errorf("%w: timecode %.3fs exceeds source duration %.3fs", ErrAVOutputMissingOrEmpty, timecode, dur.Seconds())
	}
	if _, err := runCommand(ctx, "ffmpeg", thumbnailArgs(inPath, outPath, target, timecode)...); err != nil {
		return fmt.Errorf("av: ffmpeg: %w", err)
	}
	return validateAVOutput(outPath)
}
