package convert

import (
	"context"
	"errors"
	"fmt"
	"math"
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
//
// MaxSourceDuration and MaxSourceResolutionHeight (D-09/Pitfall 4, Phase 36)
// let an operator-configured instance override the guard ceilings without
// editing package consts -- a zero value on either field falls back to
// avMaxSourceDuration/avMaxSourceResolutionHeight (resolved in Convert),
// so a bare AVConverter{} is provably identical to the pre-Phase-36 behavior:
// every existing caller/test that constructs AVConverter{} is unaffected.
// cmd/av-worker/main.go re-registers a configured instance at startup from
// AV_MAX_DURATION_SECONDS, overriding the zero-value registration in
// converters.go's init().
type AVConverter struct {
	MaxSourceDuration         time.Duration
	MaxSourceResolutionHeight int
}

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

// avScaleFilter builds the server-constructed -vf scale filter for a
// validated target height, or "" when no resize was requested. Width -2
// preserves the source aspect ratio AND rounds to an even dimension, which
// libx264/libx265/libvpx-vp9 all require. height reaches here only after
// ParseAVOpts has constrained it to the closed avResolutionHeights enum --
// it is never a raw client string interpolated into argv (AVO-02).
func avScaleFilter(height int) string {
	if height == 0 {
		return ""
	}
	return "scale=-2:" + strconv.Itoa(height)
}

// avInputArgs builds the leading, always-identical hardening prefix shared by
// every ffmpeg invocation in this file (AVE-02): -nostdin plus
// -protocol_whitelist file,crypto plus a "file:"-prefixed -i path, so ffmpeg
// can never reinterpret a path as a protocol/URL specifier (concat:/http:/
// pipe:) or a leading-dash option (IN-01 precedent, mirrors
// ffmpegNormalizeArgs/ffprobeDurationArgs).
func avInputArgs(inPath string) []string {
	return []string{
		"-y", "-nostdin", "-protocol_whitelist", "file,crypto",
		"-i", "file:" + inPath,
	}
}

// transcodeToMP4Args builds ffmpeg's argv for the mov/avi/mkv/webm -> mp4
// full re-encode path (AVC-01): H.264/AAC with +faststart. codec selects
// libx264 (default, or the explicit "h264" client choice) or libx265
// ("hevc", AVO-03) -- the HEVC branch uses x265DefaultCRF, NEVER
// x264DefaultCRF (Pitfall 4, 34-RESEARCH.md). height, when non-zero, emits a
// server-constructed scale filter (CR-01: the option was previously accepted
// and validated but never reached ffmpeg). videoIndex is the ABSOLUTE index
// of the stream avPrimaryVideoStream selected, mapped explicitly so this
// re-encode path and the stream-copy path agree on what "the video" means
// (CR-03).
func transcodeToMP4Args(inPath, outPath, codec string, height, videoIndex, threads int) []string {
	videoCodec := "libx264"
	crf := x264DefaultCRF
	if codec == "hevc" {
		videoCodec = "libx265"
		crf = x265DefaultCRF
	}
	args := append(avInputArgs(inPath),
		"-map", "0:"+strconv.Itoa(videoIndex), "-map", "0:a:0?")
	if f := avScaleFilter(height); f != "" {
		args = append(args, "-vf", f)
	}
	return append(args,
		"-c:v", videoCodec, "-preset", "veryfast", "-crf", strconv.Itoa(crf),
		"-c:a", "aac", "-b:a", "128k",
		"-movflags", "+faststart",
		"-threads", strconv.Itoa(threads),
		"file:"+outPath,
	)
}

// transcodeToWebMArgs builds ffmpeg's argv for the mp4 -> webm path
// (AVC-02): VP9/Opus, ALWAYS a full re-encode -- this builder never selects
// a stream-copy fast path; the caller (Convert) decides whether to call this
// builder at all vs. issuing a raw "-c copy" invocation, gated on
// avStreamCopyLegal. height/videoIndex carry the same meaning as in
// transcodeToMP4Args (CR-01/CR-03).
func transcodeToWebMArgs(inPath, outPath string, height, videoIndex, threads int) []string {
	args := append(avInputArgs(inPath),
		"-map", "0:"+strconv.Itoa(videoIndex), "-map", "0:a:0?")
	if f := avScaleFilter(height); f != "" {
		args = append(args, "-vf", f)
	}
	return append(args,
		"-c:v", "libvpx-vp9", "-b:v", "1M",
		"-c:a", "libopus",
		"-threads", strconv.Itoa(threads),
		"file:"+outPath,
	)
}

// streamCopyArgs builds ffmpeg's argv for the AVC-05 remux fast path. It maps
// EXACTLY the two streams avStreamCopyLegal inspected -- videoIndex (the
// absolute index of the probed primary video stream) and a:0 -- because a
// bare "-c copy" uses ffmpeg's default stream selection and would carry
// ADDITIONAL streams past the gate, letting a container smuggle a codec this
// project's own mp4/webm contract forbids (CR-03).
func streamCopyArgs(inPath, outPath, target string, videoIndex int) []string {
	args := append(avInputArgs(inPath),
		"-map", "0:"+strconv.Itoa(videoIndex), "-map", "0:a:0",
		"-c", "copy",
	)
	if target == "mp4" {
		args = append(args, "-movflags", "+faststart")
	}
	return append(args, "file:"+outPath)
}

// extractAudioArgs builds ffmpeg's argv for the video -> mp3/wav/m4a
// audio-extract path (AVC-03). -vn drops the video stream entirely.
// streamCopy selects "-c:a copy" (source already AAC, target m4a) instead of
// a target-specific re-encode codec -- the caller (Convert) decides
// streamCopy via an ffprobe codec check, never this function.
// A nil return means the target was not one of this builder's closed set --
// callers MUST treat a nil argv as a programming error and fail closed rather
// than invoke ffmpeg, which would otherwise fall back to extension-based
// codec auto-selection (WR-09).
func extractAudioArgs(inPath, outPath, target string, streamCopy bool) []string {
	args := append(avInputArgs(inPath), "-vn")
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
		default:
			return nil
		}
	}
	return append(args, "file:"+outPath)
}

// thumbnailArgs builds ffmpeg's argv for the video -> jpg/png/webp
// thumbnail-extract path (AVC-04): input-side -ss (fast seek near the
// target keyframe, BEFORE -i, per 34-RESEARCH.md "State of the Art") and an
// EXPLICIT per-target -c:v -- never relying on ffmpeg's extension-based
// auto-selection, which is known to fail for at least one target on at
// least one real ffmpeg build (Pitfall 3, 34-RESEARCH.md).
// videoIndex explicitly maps the probed primary video stream so a container's
// embedded cover art can never be extracted in place of the real video
// (CR-03). A nil return signals an out-of-set target, same fail-closed
// contract as extractAudioArgs (WR-09).
func thumbnailArgs(inPath, outPath, target string, timecode float64, videoIndex int) []string {
	args := []string{
		"-y", "-nostdin", "-protocol_whitelist", "file,crypto",
		"-ss", strconv.FormatFloat(timecode, 'f', -1, 64),
		"-i", "file:" + inPath,
		"-map", "0:" + strconv.Itoa(videoIndex),
		"-frames:v", "1",
	}
	switch target {
	case "jpg":
		args = append(args, "-c:v", "mjpeg", "-q:v", "2")
	case "png":
		args = append(args, "-c:v", "png")
	case "webp":
		args = append(args, "-c:v", "libwebp")
	default:
		return nil
	}
	return append(args, "file:"+outPath)
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

// avNoScalePassthroughMaxHeight bounds the resolution_height==0 ("no scale
// requested", AVO-02) RE-ENCODE path to the SAME closed enum ceiling any
// explicit resolution_height request is already validated against
// (avResolutionHeights, avopts.go: {480,720,1080}) -- Phase 36 Plan 04's
// supervised RTF matrix (scripts/av-rtf-measure.sh) measured this exact
// passthrough (no "-vf scale") path as the true worst case: hevc@2160p
// OOM-KILLED (exit 137) at the compose av-worker memory limit, a real
// memory-safety DoS signal, not just slowness -- and it is unmeasurable
// (unbounded) because no explicit AVOpts target ever exceeds 1080p. This is
// disposition (b) "bound the path" from the 36-04-PLAN.md passthrough-
// residual-disposition checkpoint (36-REVIEW blocker / 36-RESEARCH.md Open
// Q3): rather than (a) folding the fixture-sized passthrough p95 into the
// timeout derivation (which still under-covers a real source larger than
// the fixture) or (c) accepting the gap as documented residual risk, every
// legal request is aligned to the SAME measured envelope (hevc@1080
// p95_RTF=4.179133s) that drives AV_ENGINE_TIMEOUT (.env.example).
const avNoScalePassthroughMaxHeight = 1080

// ErrAVNoScalePassthroughExceeded classifies a resolution_height==0
// (no-scale) transcode request whose source height exceeds
// avNoScalePassthroughMaxHeight -- a fail-closed guard-stage error, mirroring
// ErrAVResolutionExceeded's shape (avduration.go), detected and returned
// BEFORE the expensive re-encode ffmpeg invocation ever runs. Deliberately
// scoped to the RE-ENCODE branch only (convertTranscode): a stream-copy
// ("-c copy") remux performs no decode/encode at all -- it is gated on
// avStreamCopyLegal requiring the source to ALREADY be in the target
// container's exact codec contract, so it carries none of the measured
// OOM/RTF risk this guard exists to close, and was never a cell the RTF
// matrix measured in the first place.
var ErrAVNoScalePassthroughExceeded = errors.New("av: no-scale passthrough resolution exceeds bound")

// enforceNoScalePassthroughBound rejects a no-scale (resolution_height==0)
// re-encode whose source height exceeds avNoScalePassthroughMaxHeight --
// fail-closed, mirroring enforceMaxResolutionOf's shape (avduration.go).
func enforceNoScalePassthroughBound(height int) error {
	if height > avNoScalePassthroughMaxHeight {
		return fmt.Errorf("%w: declared height %d exceeds %d", ErrAVNoScalePassthroughExceeded, height, avNoScalePassthroughMaxHeight)
	}
	return nil
}

// avDiskSafetyFactorDefault is the multiplier EnforceMinFreeDisk applies to
// a probed input file's size when sizing its "at least this much free
// space" ceiling (D-06/T-36-01), used when SetAVDiskSafetyFactor was never
// called (avDiskSafetyFactorOverride == 0). [ASSUMED] 3.0 is Claude's
// Discretion default per 36-CONTEXT.md -- sized to cover ffmpeg's own
// working set (decoded frame buffers, muxer staging, an intermediate
// re-encode) on top of the source and destination files' own footprint, not
// a value derived from a measured decode/encode disk-usage ratio.
const avDiskSafetyFactorDefault = 3.0

// avDiskSafetyFactorOverride stores the AV_DISK_SAFETY_FACTOR override for
// every subsequent EnforceMinFreeDisk call inside Convert. Set once at
// process startup via SetAVDiskSafetyFactor (cmd/av-worker/main.go), BEFORE
// the asynq server starts consuming tasks -- single write before any
// concurrent reader, no mutex needed, mirroring SetAudioThreads's contract
// (whisper.go). Zero means "never set", in which case
// effectiveAVDiskSafetyFactor falls back to avDiskSafetyFactorDefault.
var avDiskSafetyFactorOverride float64

// SetAVDiskSafetyFactor stores the AV_DISK_SAFETY_FACTOR override for every
// subsequent disk-space guard call. Call exactly once at process startup,
// BEFORE the asynq server starts consuming tasks -- mirrors
// SetAudioThreads's contract (whisper.go).
func SetAVDiskSafetyFactor(f float64) {
	avDiskSafetyFactorOverride = f
}

// effectiveAVDiskSafetyFactor resolves the configured override when
// positive, else avDiskSafetyFactorDefault -- mirrors
// effectiveVeraPDFTimeout's resolver shape (verapdf.go).
func effectiveAVDiskSafetyFactor() float64 {
	if avDiskSafetyFactorOverride > 0 {
		return avDiskSafetyFactorOverride
	}
	return avDiskSafetyFactorDefault
}

// avDefaultThumbnailTimecode is the seek point used when a client requests no
// timecode at all. It is clamped against the source's real duration by
// convertThumbnail, so it is a preference rather than a hard floor (CR-04).
const avDefaultThumbnailTimecode = 1.0

// avProbeTimeout bounds the guard stage's ffprobe subprocesses. ProbeDuration
// documents (audioduration.go) that its ctx must carry a SHORT bound distinct
// from the full engine timeout -- reading container metadata is near-instant
// even for huge files, so a hung ffprobe on a malformed container must never
// be allowed to burn the entire ENGINE_TIMEOUT budget (WR-05).
const avProbeTimeout = 15 * time.Second

// ErrAVOutputMissingOrEmpty classifies ffmpeg's "exit 0 but produced nothing
// usable" failure mode -- ffmpeg can exit 0 having written no file at all, or
// a zero-byte one (34-RESEARCH.md Pitfall 2); both shapes map to this SAME
// class so a caller can errors.Is-match "no usable output" regardless of
// which guard caught it. Strictly an ENGINE-fault class, detected only by
// post-hoc output validation -- client input faults never fold into it
// (WR-04, see ErrAVTimecodeOutOfRange).
var ErrAVOutputMissingOrEmpty = errors.New("av: output missing or empty")

// ErrAVTimecodeOutOfRange classifies a CLIENT-supplied thumbnail timecode
// past the source's declared duration -- a 4xx-class input fault detected
// pre-flight, before ffmpeg is invoked at all. Deliberately NOT folded into
// ErrAVOutputMissingOrEmpty's engine-fault class (WR-04): the API layer must
// be able to tell 422-worthy bad input from a 500-worthy engine failure, and
// the worker must be able to tell deterministically-terminal from retryable
// (asynq.SkipRetry discipline, CLAUDE.md "Error Handling").
var ErrAVTimecodeOutOfRange = errors.New("av: timecode exceeds source duration")

// ErrAVTranscodeFailed classifies a failed ffmpeg invocation in the
// transcode stage (convertTranscode, AVC-01/AVC-02/AVC-05) -- D-01
// (35-CONTEXT.md): before this sentinel, all three ffmpeg call sites in this
// file wrapped identically with the same "av" + "ffmpeg" prefix, so a caller
// could not tell which stage failed without string matching. The
// worker-layer classifier
// (isAVTerminal, internal/worker/worker.go) matches this with errors.Is and
// treats a TIMEOUT on this sentinel as TRANSIENT (D-02): transcode is the
// expensive operation, so a timeout may simply mean the retry budget ran out
// under load, not that the input is malformed. Any non-timeout transcode
// failure is still terminal.
var ErrAVTranscodeFailed = errors.New("av: transcode failed")

// ErrAVAudioExtractFailed classifies a failed ffmpeg invocation in the
// audio-extract stage (convertAudioExtract, AVC-03) -- D-01. Unlike
// ErrAVTranscodeFailed, isAVTerminal treats ANY failure on this sentinel
// (timeout or not) as TERMINAL (D-02): audio-extract is a cheap operation,
// so a timeout here indicates a pathological input, not budget exhaustion.
var ErrAVAudioExtractFailed = errors.New("av: audio-extract failed")

// ErrAVThumbnailFailed classifies a failed ffmpeg invocation in the
// thumbnail stage (convertThumbnail, AVC-04) -- D-01. Same TERMINAL-on-any-
// failure policy as ErrAVAudioExtractFailed (D-02): thumbnail extraction is
// cheap, so a timeout signals a pathological input.
var ErrAVThumbnailFailed = errors.New("av: thumbnail failed")

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
// invocation in this file, preferring the container's cgroup CPU quota over
// the host core count. runtime.NumCPU() reports the HOST's CPUs inside a
// container and does not honor a cgroup quota, so on a 16-core host it would
// hand ffmpeg -threads 16 while docker-compose.yml limits the worker to
// cpus: "2.0" -- oversubscription and CFS thrashing under concurrent jobs
// (WR-06). CgroupCPULimit (cgroup.go) fails open on cgroup v1 hosts and
// outside any container, so the NumCPU fallback still covers the local
// `go run` dev flow.
func avThreadCount() int {
	if n, ok := CgroupCPULimit(); ok {
		return n
	}
	return runtime.NumCPU()
}

// validateAVOutput guards against ffmpeg's "exit 0 but produced nothing
// usable" failure mode (Pitfall 2): a missing file (os.Stat error) and a
// zero-byte file map to the SAME ErrAVOutputMissingOrEmpty class. For
// thumbnail targets (jpg/png/webp) it additionally re-Sniff()s the output
// bytes to confirm they actually decode as the requested image format --
// no existing validateXOutput sibling does this second check, it is new for
// AV (34-PATTERNS.md).
func validateAVOutput(path, target string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAVOutputMissingOrEmpty, err)
	}
	if fi.Size() == 0 {
		return ErrAVOutputMissingOrEmpty
	}
	switch target {
	case "jpg", "png", "webp":
		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrAVOutputMissingOrEmpty, err)
		}
		defer f.Close()
		detected, _, sniffErr := Sniff(f)
		if sniffErr != nil {
			return fmt.Errorf("av: thumbnail not a valid %s: %w", target, sniffErr)
		}
		if detected != target {
			return fmt.Errorf("av: thumbnail not a valid %s (detected %q)", target, detected)
		}
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
	// defense, both fail-closed and independently required. maxDuration/
	// maxHeight resolve c's configured fields when non-zero, else fall back
	// to the package-const defaults (D-09/Pitfall 4) -- mirrors
	// api.NewServer's zero-value-defaulting-in-constructor pattern, so a
	// bare AVConverter{} enforces exactly today's 4h/4320 ceilings.
	maxDuration := c.MaxSourceDuration
	if maxDuration == 0 {
		maxDuration = avMaxSourceDuration
	}
	maxHeight := c.MaxSourceResolutionHeight
	if maxHeight == 0 {
		maxHeight = avMaxSourceResolutionHeight
	}

	src, err := avProbeSource(ctx, inPath)
	if err != nil {
		return fmt.Errorf("av: %w", err)
	}
	if err := enforceMaxDurationOf(src.duration, maxDuration); err != nil {
		return fmt.Errorf("av: %w", err)
	}
	if err := enforceMaxResolutionOf(src.videoStreams, maxHeight); err != nil {
		return fmt.Errorf("av: %w", err)
	}

	// Disk-space guard (D-06/T-36-01): fail-closed BEFORE the expensive
	// ffmpeg subprocess, same guard-before-expensive-work discipline as the
	// duration/resolution checks above. Sized proportional to the already-
	// on-disk input file (inPath was already downloaded from S3 by this
	// point), not the eventual output -- the guard's job is to bound
	// ffmpeg's OWN working-set footprint (decode buffers, muxer staging,
	// intermediate re-encode data) relative to what is already known to
	// exist on disk.
	fi, err := os.Stat(inPath)
	if err != nil {
		return fmt.Errorf("av: %w", err)
	}
	if err := EnforceMinFreeDisk(filepath.Dir(inPath), fi.Size(), effectiveAVDiskSafetyFactor()); err != nil {
		return fmt.Errorf("av: %w", err)
	}

	switch targetFormat {
	case "mp4", "webm":
		return c.convertTranscode(ctx, inPath, outPath, targetFormat, o, src)
	case "mp3", "wav", "m4a":
		return c.convertAudioExtract(ctx, inPath, outPath, targetFormat, src)
	default:
		return c.convertThumbnail(ctx, inPath, outPath, targetFormat, o, src)
	}
}

// avSourceProbe is everything the guard stage learned about the source,
// probed EXACTLY ONCE and threaded through to the conversion stage. Before
// WR-05 a single thumbnail job spawned ProbeDuration twice and
// probeVideoStream once on the same file, and a transcode spawned
// probeVideoStream twice -- redundant subprocesses whose independently
// derived answers could disagree if the file changed between probes. Probing
// once makes the guard's decision and the conversion's decision provably the
// same decision.
type avSourceProbe struct {
	duration     time.Duration
	videoStreams []avVideoStream
	primary      avVideoStream
	audioCodec   string
}

// avProbeSource runs the whole probe stage under its OWN short timeout,
// derived from but distinct from the caller's full engine budget (WR-05).
func avProbeSource(ctx context.Context, inPath string) (avSourceProbe, error) {
	probeCtx, cancel := context.WithTimeout(ctx, avProbeTimeout)
	defer cancel()

	dur, err := ProbeDuration(probeCtx, inPath)
	if err != nil {
		return avSourceProbe{}, err
	}
	streams, err := probeVideoStreams(probeCtx, inPath)
	if err != nil {
		return avSourceProbe{}, err
	}
	primary, ok := avPrimaryVideoStream(streams)
	if !ok {
		return avSourceProbe{}, fmt.Errorf("%w", ErrAVNoVideoStream)
	}
	// An absent audio stream is not an error: ffprobe reports empty output
	// and the conversion paths handle a video-only source (the transcode
	// builders map audio optionally, and a stream copy is disqualified
	// because avStreamCopyLegal cannot match an empty codec).
	audioCodec, err := probeAudioCodec(probeCtx, inPath)
	if err != nil {
		return avSourceProbe{}, err
	}
	return avSourceProbe{
		duration:     dur,
		videoStreams: streams,
		primary:      primary,
		audioCodec:   audioCodec,
	}, nil
}

// convertTranscode implements AVC-01/AVC-02/AVC-05: probe the source's
// declared video+audio codec, gate a "-c copy" remux on avStreamCopyLegal
// (never on ffmpeg's own muxer acceptance), else full re-encode via
// transcodeToMP4Args (HEVC-aware) or transcodeToWebMArgs.
func (c AVConverter) convertTranscode(ctx context.Context, inPath, outPath, target string, o AVOpts, src avSourceProbe) error {
	var args []string
	if avStreamCopyEligible(target, o, src) {
		args = streamCopyArgs(inPath, outPath, target, src.primary.Index)
	} else {
		// Passthrough bound (disposition (b), Phase 36 Plan 04): a re-encode
		// with no requested scale must not proceed against a source taller
		// than avNoScalePassthroughMaxHeight -- checked HERE, in the re-encode
		// branch only, BEFORE either argv builder runs (fail-closed before
		// the expensive ffmpeg invocation, same discipline as Convert's
		// duration/resolution guard stage).
		if o.ResolutionHeight == 0 {
			if err := enforceNoScalePassthroughBound(src.primary.Height); err != nil {
				return fmt.Errorf("av: %w", err)
			}
		}
		switch target {
		case "mp4":
			args = transcodeToMP4Args(inPath, outPath, o.Codec, o.ResolutionHeight, src.primary.Index, avThreadCount())
		case "webm":
			args = transcodeToWebMArgs(inPath, outPath, o.ResolutionHeight, src.primary.Index, avThreadCount())
		default:
			return fmt.Errorf("av: unsupported transcode target %q", target)
		}
	}

	if _, err := runCommand(ctx, "ffmpeg", args...); err != nil {
		return fmt.Errorf("%w: %w", ErrAVTranscodeFailed, err)
	}
	return validateAVOutput(outPath, target)
}

// avStreamCopyEligible decides whether the AVC-05 remux fast path can be
// taken. Container-codec legality (avStreamCopyLegal) is necessary but NOT
// sufficient: a stream copy reproduces the source bit-for-bit, so it cannot
// satisfy any client option that asks for different output bits. Before
// CR-01/CR-02 this gate consulted only the codec table, so an h264+aac source
// took the copy path unconditionally and a client's explicit
// {"codec": "hevc"} or {"resolution_height": 480} was validated,
// applicability-checked, and then silently discarded -- the client got an
// unscaled H.264 file with no error and no warning.
func avStreamCopyEligible(target string, o AVOpts, src avSourceProbe) bool {
	if !avStreamCopyLegal(target, src.primary.CodecName, src.audioCodec) {
		return false
	}
	// A resize is by definition a re-encode; no copy can produce it.
	if o.ResolutionHeight != 0 {
		return false
	}
	// An explicit codec request is honored only if the copy would actually
	// yield that codec -- i.e. the source is already in it.
	if o.Codec != "" && o.Codec != src.primary.CodecName {
		return false
	}
	return true
}

// convertAudioExtract implements AVC-03: extract the audio track only
// (-vn), using "-c:a copy" only when the source is already AAC and the
// target is m4a, else a target-specific re-encode.
//
// The source's audio codec comes from the already-probed src (avProbeSource),
// NOT a fresh ffprobe (WR-01, Phase 35): re-probing here duplicated a
// subprocess and violated avSourceProbe's own "probed EXACTLY ONCE" invariant,
// whose whole point is that the guard's decision and the conversion's decision
// are provably the same decision on the same bytes.
func (c AVConverter) convertAudioExtract(ctx context.Context, inPath, outPath, target string, src avSourceProbe) error {
	streamCopy := target == "m4a" && src.audioCodec == "aac"
	args := extractAudioArgs(inPath, outPath, target, streamCopy)
	if args == nil {
		return fmt.Errorf("av: unsupported audio-extract target %q", target)
	}
	if _, err := runCommand(ctx, "ffmpeg", args...); err != nil {
		return fmt.Errorf("%w: %w", ErrAVAudioExtractFailed, err)
	}
	return validateAVOutput(outPath, target)
}

// convertThumbnail implements AVC-04. The seek point is bounds-checked
// against the guard stage's already-probed duration BEFORE invoking ffmpeg at
// all (Pitfall 2 -- an out-of-range -ss produces NO output file, so rejecting
// up front is strictly better than letting ffmpeg silently no-op).
//
// CR-04, the unset-vs-zero distinction: an ABSENT timecode gets a default
// clamped against the real duration, so a sub-second source still yields a
// thumbnail instead of being permanently unconvertible; an EXPLICIT
// out-of-range timecode is still a hard client error. Only an explicit
// request can fail here, which is why the sentinel is the input-fault
// ErrAVTimecodeOutOfRange, not the engine-fault
// ErrAVOutputMissingOrEmpty (WR-04).
func (c AVConverter) convertThumbnail(ctx context.Context, inPath, outPath, target string, o AVOpts, src avSourceProbe) error {
	seconds := src.duration.Seconds()
	var timecode float64
	if o.Timecode != nil {
		timecode = *o.Timecode
		if timecode >= seconds {
			return fmt.Errorf("%w: timecode %.3fs exceeds source duration %.3fs",
				ErrAVTimecodeOutOfRange, timecode, seconds)
		}
	} else {
		// Default to avDefaultThumbnailTimecode, but never past the source:
		// half-duration keeps a sub-second clip inside range and still lands
		// on a representative frame rather than frame 0.
		timecode = math.Min(avDefaultThumbnailTimecode, seconds/2)
	}

	args := thumbnailArgs(inPath, outPath, target, timecode, src.primary.Index)
	if args == nil {
		return fmt.Errorf("av: unsupported thumbnail target %q", target)
	}
	if _, err := runCommand(ctx, "ffmpeg", args...); err != nil {
		return fmt.Errorf("%w: %w", ErrAVThumbnailFailed, err)
	}
	return validateAVOutput(outPath, target)
}
