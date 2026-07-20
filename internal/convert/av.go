package convert

import "strconv"

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
