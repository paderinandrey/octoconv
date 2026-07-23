package convert

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// requireLiveAVBinaries skips the calling test unless ffmpeg and ffprobe are
// both present -- mirrors requireLiveAudioBinaries's skip-gate convention
// (whisper_test.go).
func requireLiveAVBinaries(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH; see 34-RESEARCH.md \"Local Development Setup\"")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not on PATH; see 34-RESEARCH.md \"Local Development Setup\"")
	}
}

// requireLibwebpEncoder skips ONLY the calling (sub)test when the local
// ffmpeg build lacks a libwebp encoder (34-RESEARCH.md Pitfall 3 /
// Environment Availability) -- narrower than requireLiveAVBinaries, which
// only checks the binaries are on PATH, not their compiled-in encoder set.
func requireLibwebpEncoder(t *testing.T) {
	t.Helper()
	out, err := exec.Command("ffmpeg", "-hide_banner", "-encoders").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "libwebp") {
		t.Skip("local ffmpeg build lacks the libwebp encoder; see 34-RESEARCH.md Pitfall 3")
	}
}

// assertArgv fails the test if got and want are not element-for-element
// equal, mirroring whisper_test.go's inline argv-pinning comparison style.
func assertArgv(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("argv = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("argv = %v, want %v", got, want)
		}
	}
}

// TestTranscodeToMP4Args pins the mov/avi/mkv/webm -> mp4 argv shape
// (AVC-01), including the leading AVE-02 hardening flags and the
// "file:"-prefixed -i path. Runs unconditionally -- pure function, no
// subprocess.
func TestTranscodeToMP4Args(t *testing.T) {
	got := transcodeToMP4Args("/work/in.mov", "/work/out.mp4", "h264", 0, 0, 4)
	want := []string{
		"-y", "-nostdin", "-protocol_whitelist", "file,crypto",
		"-i", "file:/work/in.mov",
		"-map", "0:0", "-map", "0:a:0?",
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "23",
		"-c:a", "aac", "-b:a", "128k",
		"-movflags", "+faststart",
		"-threads", "4",
		"file:/work/out.mp4",
	}
	assertArgv(t, got, want)
}

// TestTranscodeArgs_ResolutionHeightEmitsScaleFilter pins CR-01: a validated
// resolution_height must actually reach ffmpeg as a server-constructed scale
// filter. Before the fix the option was parsed, range-checked and
// applicability-checked, then silently discarded -- the client got a
// full-resolution transcode with no error and no warning. Width -2 preserves
// aspect ratio and keeps the dimension even (required by
// libx264/libx265/libvpx-vp9).
func TestTranscodeArgs_ResolutionHeightEmitsScaleFilter(t *testing.T) {
	for _, height := range []int{480, 720, 1080} {
		want := "-vf scale=-2:" + strconv.Itoa(height)
		mp4 := strings.Join(transcodeToMP4Args("/work/in.mov", "/work/out.mp4", "h264", height, 0, 4), " ")
		if !strings.Contains(mp4, want) {
			t.Errorf("transcodeToMP4Args(height=%d) = %q, want it to contain %q", height, mp4, want)
		}
		webm := strings.Join(transcodeToWebMArgs("/work/in.mp4", "/work/out.webm", height, 0, 2), " ")
		if !strings.Contains(webm, want) {
			t.Errorf("transcodeToWebMArgs(height=%d) = %q, want it to contain %q", height, webm, want)
		}
	}
	// height 0 means "no resize requested": no filter at all, not scale=-2:0.
	for _, argv := range [][]string{
		transcodeToMP4Args("/work/in.mov", "/work/out.mp4", "h264", 0, 0, 4),
		transcodeToWebMArgs("/work/in.mp4", "/work/out.webm", 0, 0, 2),
	} {
		if slices.Contains(argv, "-vf") {
			t.Errorf("argv %v carries a -vf filter for height 0, want none", argv)
		}
	}
}

// TestTranscodeArgs_MapsProbedVideoStream pins CR-03: both transcode builders
// map the ABSOLUTE index of the video stream the probe selected, so the
// re-encode path and the stream-copy path agree on what "the video" means and
// a container's embedded cover art can never be transcoded in its place.
func TestTranscodeArgs_MapsProbedVideoStream(t *testing.T) {
	mp4 := strings.Join(transcodeToMP4Args("/work/in.mov", "/work/out.mp4", "h264", 0, 3, 4), " ")
	if !strings.Contains(mp4, "-map 0:3 -map 0:a:0?") {
		t.Errorf("transcodeToMP4Args = %q, want it to map probed video index 3", mp4)
	}
	webm := strings.Join(transcodeToWebMArgs("/work/in.mp4", "/work/out.webm", 0, 3, 2), " ")
	if !strings.Contains(webm, "-map 0:3 -map 0:a:0?") {
		t.Errorf("transcodeToWebMArgs = %q, want it to map probed video index 3", webm)
	}
}

// TestStreamCopyArgs_MapsExactlyTheGatedStreams pins CR-03's core security
// fix: avStreamCopyLegal inspects exactly one video and one audio stream, so
// the copy must MAP exactly those two. A bare "-c copy" uses ffmpeg's default
// stream selection and would carry additional streams -- e.g. a second audio
// stream in a codec this project's own mp4 contract forbids -- straight past
// the gate that exists to prevent precisely that.
func TestStreamCopyArgs_MapsExactlyTheGatedStreams(t *testing.T) {
	got := streamCopyArgs("/work/in.mkv", "/work/out.mp4", "mp4", 2)
	want := []string{
		"-y", "-nostdin", "-protocol_whitelist", "file,crypto",
		"-i", "file:/work/in.mkv",
		"-map", "0:2", "-map", "0:a:0",
		"-c", "copy",
		"-movflags", "+faststart",
		"file:/work/out.mp4",
	}
	assertArgv(t, got, want)

	webm := streamCopyArgs("/work/in.mkv", "/work/out.webm", "webm", 0)
	if slices.Contains(webm, "+faststart") {
		t.Errorf("streamCopyArgs(webm) = %v, want no mp4-only +faststart flag", webm)
	}
}

// TestAVBuildersHardenEveryInvocation is AVE-02's table canary: EVERY argv
// builder in this file must carry -nostdin and -protocol_whitelist
// file,crypto, and must "file:"-prefix BOTH the input and the output path
// (WR-01 -- -protocol_whitelist constrains input protocols only, so an
// unprefixed output would still be reinterpretable as a protocol or, with a
// leading dash, as an option).
func TestAVBuildersHardenEveryInvocation(t *testing.T) {
	builders := map[string][]string{
		"transcodeToMP4Args":  transcodeToMP4Args("/work/in.mov", "/work/out.mp4", "h264", 720, 0, 4),
		"transcodeToWebMArgs": transcodeToWebMArgs("/work/in.mp4", "/work/out.webm", 720, 0, 2),
		"streamCopyArgs":      streamCopyArgs("/work/in.mkv", "/work/out.mp4", "mp4", 0),
		"extractAudioArgs":    extractAudioArgs("/work/in.mp4", "/work/out.mp3", "mp3", false),
		"thumbnailArgs":       thumbnailArgs("/work/in.mp4", "/work/out.jpg", "jpg", 1.5, 0),
	}
	for name, argv := range builders {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(strings.Join(argv, " "), "-protocol_whitelist file,crypto") {
				t.Errorf("%s = %v, want -protocol_whitelist file,crypto", name, argv)
			}
			if !slices.Contains(argv, "-nostdin") {
				t.Errorf("%s = %v, want -nostdin", name, argv)
			}
			for i, a := range argv {
				if a == "-i" && i+1 < len(argv) && !strings.HasPrefix(argv[i+1], "file:") {
					t.Errorf("%s -i element = %q, want a file:-prefixed path", name, argv[i+1])
				}
			}
			if last := argv[len(argv)-1]; !strings.HasPrefix(last, "file:") {
				t.Errorf("%s output element = %q, want a file:-prefixed path", name, last)
			}
		})
	}
}

// TestAVBuildersFailClosedOnUnknownTarget pins WR-09: the argv builders'
// target switches must fail closed rather than return a codec-less argv that
// hands stream selection back to ffmpeg's extension-based auto-selection --
// the exact behavior thumbnailArgs' own doc comment says must never be
// relied on (Pitfall 3).
func TestAVBuildersFailClosedOnUnknownTarget(t *testing.T) {
	if got := extractAudioArgs("/work/in.mp4", "/work/out.xyz", "xyz", false); got != nil {
		t.Errorf("extractAudioArgs(unknown target) = %v, want nil", got)
	}
	if got := thumbnailArgs("/work/in.mp4", "/work/out.xyz", "xyz", 1.0, 0); got != nil {
		t.Errorf("thumbnailArgs(unknown target) = %v, want nil", got)
	}
}

// TestAVStreamCopyEligible pins CR-02: container-codec legality is necessary
// but NOT sufficient for the remux fast path. A stream copy reproduces the
// source bit-for-bit, so it cannot satisfy a client option asking for
// different output bits. Before the fix an h264+aac source took the copy
// branch unconditionally, so an explicit {"codec":"hevc"} silently produced
// H.264 -- and because it only manifested for h264/aac sources, the existing
// VP9-source re-encode test could never catch it.
func TestAVStreamCopyEligible(t *testing.T) {
	h264aac := avSourceProbe{
		primary:    avVideoStream{Index: 0, CodecName: "h264"},
		audioCodec: "aac",
	}
	tc := func(f float64) *float64 { return &f }
	cases := []struct {
		name string
		o    AVOpts
		want bool
	}{
		{"no opts copies", AVOpts{}, true},
		{"explicit hevc must re-encode", AVOpts{Codec: "hevc"}, false},
		{"explicit h264 matching source may copy", AVOpts{Codec: "h264"}, true},
		{"resize must re-encode", AVOpts{ResolutionHeight: 480}, false},
		{"resize plus matching codec must still re-encode", AVOpts{Codec: "h264", ResolutionHeight: 720}, false},
		{"unrelated opt does not block copy", AVOpts{Timecode: tc(1)}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := avStreamCopyEligible("mp4", c.o, h264aac); got != c.want {
				t.Errorf("avStreamCopyEligible(mp4, %+v) = %v, want %v", c.o, got, c.want)
			}
		})
	}

	// Illegal source codecs are never copy-eligible regardless of opts.
	vp9aac := avSourceProbe{primary: avVideoStream{CodecName: "vp9"}, audioCodec: "aac"}
	if avStreamCopyEligible("mp4", AVOpts{}, vp9aac) {
		t.Error("avStreamCopyEligible(mp4, vp9+aac) = true, want false (AVC-05 codec contract)")
	}
}

// TestHEVCUsesX265CRF proves the HEVC branch references x265DefaultCRF (28),
// NEVER x264DefaultCRF (23) -- Pitfall 4, AVO-03.
func TestHEVCUsesX265CRF(t *testing.T) {
	got := transcodeToMP4Args("/work/in.mov", "/work/out.mp4", "hevc", 0, 0, 4)
	want := []string{
		"-y", "-nostdin", "-protocol_whitelist", "file,crypto",
		"-i", "file:/work/in.mov",
		"-map", "0:0", "-map", "0:a:0?",
		"-c:v", "libx265", "-preset", "veryfast", "-crf", "28",
		"-c:a", "aac", "-b:a", "128k",
		"-movflags", "+faststart",
		"-threads", "4",
		"file:/work/out.mp4",
	}
	assertArgv(t, got, want)
	if strings.Contains(strings.Join(got, " "), "libx265 -preset veryfast -crf 23") {
		t.Error("HEVC transcode argv must not carry x264DefaultCRF's value (23)")
	}
}

// TestTranscodeToWebMArgs pins the mp4 -> webm argv shape (AVC-02, always a
// full re-encode). Runs unconditionally.
func TestTranscodeToWebMArgs(t *testing.T) {
	got := transcodeToWebMArgs("/work/in.mp4", "/work/out.webm", 0, 0, 2)
	want := []string{
		"-y", "-nostdin", "-protocol_whitelist", "file,crypto",
		"-i", "file:/work/in.mp4",
		"-map", "0:0", "-map", "0:a:0?",
		"-c:v", "libvpx-vp9", "-b:v", "1M",
		"-c:a", "libopus",
		"-threads", "2",
		"file:/work/out.webm",
	}
	assertArgv(t, got, want)
}

// TestExtractAudioArgs pins the audio-extract argv shape (AVC-03), including
// the AAC-source->m4a stream-copy case. Runs unconditionally.
func TestExtractAudioArgs(t *testing.T) {
	cases := []struct {
		name       string
		target     string
		streamCopy bool
		want       []string
	}{
		{
			name:   "mp3",
			target: "mp3",
			want: []string{
				"-y", "-nostdin", "-protocol_whitelist", "file,crypto",
				"-i", "file:/work/in.mp4", "-vn",
				"-c:a", "libmp3lame", "-q:a", "2",
				"file:/work/out.mp3",
			},
		},
		{
			name:   "wav",
			target: "wav",
			want: []string{
				"-y", "-nostdin", "-protocol_whitelist", "file,crypto",
				"-i", "file:/work/in.mp4", "-vn",
				"-c:a", "pcm_s16le",
				"file:/work/out.wav",
			},
		},
		{
			name:   "m4a re-encode",
			target: "m4a",
			want: []string{
				"-y", "-nostdin", "-protocol_whitelist", "file,crypto",
				"-i", "file:/work/in.mp4", "-vn",
				"-c:a", "aac", "-b:a", "128k",
				"file:/work/out.m4a",
			},
		},
		{
			name:       "m4a stream-copy",
			target:     "m4a",
			streamCopy: true,
			want: []string{
				"-y", "-nostdin", "-protocol_whitelist", "file,crypto",
				"-i", "file:/work/in.mp4", "-vn",
				"-c:a", "copy",
				"file:/work/out.m4a",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			outPath := "/work/out." + tc.target
			got := extractAudioArgs("/work/in.mp4", outPath, tc.target, tc.streamCopy)
			assertArgv(t, got, tc.want)
		})
	}
}

// TestThumbnailArgs_ExplicitCodec proves each of jpg/png/webp gets an
// explicit -c:v (mjpeg/png/libwebp, Pitfall 3), that -ss precedes -i
// (input-side seek), and that the AVE-02 hardening flags + "file:" prefix
// are present. Runs unconditionally.
func TestThumbnailArgs_ExplicitCodec(t *testing.T) {
	cases := []struct {
		target string
		codec  string
	}{
		{"jpg", "mjpeg"},
		{"png", "png"},
		{"webp", "libwebp"},
	}
	for _, tc := range cases {
		t.Run(tc.target, func(t *testing.T) {
			outPath := "/work/thumb." + tc.target
			got := thumbnailArgs("/work/in.mp4", outPath, tc.target, 1.5, 0)

			ssIdx, iIdx, codecIdx := -1, -1, -1
			for i, a := range got {
				switch {
				case a == "-ss" && ssIdx == -1:
					ssIdx = i
				case a == "-i" && iIdx == -1:
					iIdx = i
				case a == "-c:v" && codecIdx == -1:
					codecIdx = i
				}
			}
			if ssIdx == -1 || iIdx == -1 || ssIdx >= iIdx {
				t.Fatalf("thumbnailArgs(%q) = %v, want -ss BEFORE -i", tc.target, got)
			}
			if codecIdx == -1 || codecIdx+1 >= len(got) || got[codecIdx+1] != tc.codec {
				t.Fatalf("thumbnailArgs(%q) = %v, want explicit -c:v %s", tc.target, got, tc.codec)
			}
			if got[iIdx+1] != "file:/work/in.mp4" {
				t.Fatalf("thumbnailArgs(%q) -i element = %q, want file:-prefixed path", tc.target, got[iIdx+1])
			}
			hasWhitelist := false
			for i, a := range got {
				if a == "-protocol_whitelist" && i+1 < len(got) && got[i+1] == "file,crypto" {
					hasWhitelist = true
				}
			}
			if !hasWhitelist {
				t.Fatalf("thumbnailArgs(%q) = %v, want -protocol_whitelist file,crypto", tc.target, got)
			}
		})
	}
}

// TestAVConverter_PairsSelfDisjoint asserts no duplicate (from,to) pair
// within AVConverter.Pairs() (34-RESEARCH.md Open Question 3, RESOLVED:
// cheap same-converter insurance; the cross-converter disjointness test
// against AudioConverter.Pairs() remains Phase 35's responsibility).
func TestAVConverter_PairsSelfDisjoint(t *testing.T) {
	seen := make(map[Pair]bool)
	for _, p := range (AVConverter{}).Pairs() {
		if seen[p] {
			t.Errorf("duplicate pair %+v in AVConverter.Pairs()", p)
		}
		seen[p] = true
	}
}

// TestAVConverter_Contract asserts the Converter contract without requiring
// any live binary -- runs unconditionally.
func TestAVConverter_Contract(t *testing.T) {
	if got := (AVConverter{}).Engine(); got != EngineAV {
		t.Errorf("Engine() = %q, want %q", got, EngineAV)
	}
	if len(AVConverter{}.Pairs()) == 0 {
		t.Error("Pairs() is empty, want the locked pair set")
	}
}

// TestAVConverter_FieldDefaulting_BareStructMatchesPackageConsts pins D-09's
// core contract (Pitfall 4): a bare (zero-value) AVConverter{} enforces
// EXACTLY the pre-Phase-36 avMaxSourceDuration (4h)/
// avMaxSourceResolutionHeight (4320) ceilings. A tiny, fast fixture passes
// both guards against the zero-value struct, proving the resolver's "0 means
// use the package default" fallback engages -- every existing caller/test
// that constructs AVConverter{} is provably unaffected by this refactor.
func TestAVConverter_FieldDefaulting_BareStructMatchesPackageConsts(t *testing.T) {
	requireLiveAVBinaries(t)
	dir := t.TempDir()
	src := mustGenerateAVFixture(t, dir, "src.mp4", "-c:v", "libx264", "-c:a", "aac")
	outPath := filepath.Join(dir, "out.mp3")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := (AVConverter{}).Convert(ctx, src, outPath, nil); err != nil {
		t.Fatalf("bare AVConverter{}.Convert = %v, want nil (fixture is well under the 4h/4320 defaults)", err)
	}
}

// TestAVConverter_FieldDefaulting_ConfiguredDurationRejects proves a
// non-zero MaxSourceDuration field overrides avMaxSourceDuration AND is
// actually enforced -- a 2-second fixture must be rejected against a
// 1-second configured ceiling, with the same ErrAudioDurationExceeded
// sentinel the reused duration guard (enforceMaxDurationOf) always uses.
func TestAVConverter_FieldDefaulting_ConfiguredDurationRejects(t *testing.T) {
	requireLiveAVBinaries(t)
	dir := t.TempDir()
	src := mustGenerateAVFixture(t, dir, "src.mp4", "-c:v", "libx264", "-c:a", "aac")
	outPath := filepath.Join(dir, "out.mp3")

	c := AVConverter{MaxSourceDuration: 1 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := c.Convert(ctx, src, outPath, nil)
	if !errors.Is(err, ErrAudioDurationExceeded) {
		t.Fatalf("Convert with MaxSourceDuration=1s against a 2s fixture = %v, want errors.Is(err, ErrAudioDurationExceeded)", err)
	}
}

// TestAVConverter_FieldDefaulting_ConfiguredResolutionRejects proves a
// non-zero MaxSourceResolutionHeight field overrides
// avMaxSourceResolutionHeight AND is actually enforced -- the 64px-tall
// fixture must be rejected against a 32px configured ceiling, with the same
// ErrAVResolutionExceeded sentinel enforceMaxResolutionOf always uses.
func TestAVConverter_FieldDefaulting_ConfiguredResolutionRejects(t *testing.T) {
	requireLiveAVBinaries(t)
	dir := t.TempDir()
	src := mustGenerateAVFixture(t, dir, "src.mp4", "-c:v", "libx264", "-c:a", "aac")
	outPath := filepath.Join(dir, "out.mp3")

	c := AVConverter{MaxSourceResolutionHeight: 32}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := c.Convert(ctx, src, outPath, nil)
	if !errors.Is(err, ErrAVResolutionExceeded) {
		t.Fatalf("Convert with MaxSourceResolutionHeight=32 against a 64px fixture = %v, want errors.Is(err, ErrAVResolutionExceeded)", err)
	}
}

// TestAVStreamCopyLegal pins avStreamCopyLegal's allowlist (AVC-05/T-34-11):
// mp4<-h264/aac and webm<-vp9/opus are the ONLY legal combinations -- every
// other combination, including an unknown target container, is false. Runs
// unconditionally.
func TestAVStreamCopyLegal(t *testing.T) {
	cases := []struct {
		target, vcodec, acodec string
		want                   bool
	}{
		{"mp4", "h264", "aac", true},
		{"mp4", "vp9", "opus", false},
		{"webm", "vp9", "opus", true},
		{"webm", "h264", "aac", false},
		{"avi", "h264", "aac", false},
	}
	for _, tc := range cases {
		if got := avStreamCopyLegal(tc.target, tc.vcodec, tc.acodec); got != tc.want {
			t.Errorf("avStreamCopyLegal(%q,%q,%q) = %v, want %v", tc.target, tc.vcodec, tc.acodec, got, tc.want)
		}
	}
}

// TestFfprobeAudioCodecArgs_Hardening proves the stream-copy-eligibility
// audio-codec probe carries the AVE-02 hardening flags and a
// "file:"-prefixed path. Runs unconditionally -- pure function, no
// subprocess.
func TestFfprobeAudioCodecArgs_Hardening(t *testing.T) {
	got := ffprobeAudioCodecArgs("/work/in.mp4")
	hasWhitelist, hasFilePrefix := false, false
	for i, a := range got {
		if a == "-protocol_whitelist" && i+1 < len(got) && got[i+1] == "file,crypto" {
			hasWhitelist = true
		}
		if a == "file:/work/in.mp4" {
			hasFilePrefix = true
		}
	}
	if !hasWhitelist {
		t.Errorf("ffprobeAudioCodecArgs = %v, want -protocol_whitelist file,crypto", got)
	}
	if !hasFilePrefix {
		t.Errorf("ffprobeAudioCodecArgs = %v, want a file:-prefixed path element", got)
	}
}

// TestAVConverter_UnsupportedTargetFailsFast asserts Convert rejects an
// unsupported target extension BEFORE the guard stage/any subprocess runs --
// mirrors AudioConverter's equivalent test (whisper_test.go). The input path
// deliberately does not exist; if the guard stage or a subprocess ran first,
// the error would instead be an "av: ffprobe:"/"av:" duration/resolution
// failure, not this fail-fast message. Runs ungated.
func TestAVConverter_UnsupportedTargetFailsFast(t *testing.T) {
	dir := t.TempDir()
	for _, out := range []string{"out.xyz", "out"} {
		outPath := filepath.Join(dir, out)
		err := (AVConverter{}).Convert(context.Background(), filepath.Join(dir, "does-not-exist.mp4"), outPath, nil)
		if err == nil {
			t.Fatalf("Convert(-> %s) = nil, want unsupported-target error", out)
		}
		if !strings.Contains(err.Error(), "unsupported target format") {
			t.Errorf("Convert(-> %s) error = %q, want it to mention \"unsupported target format\"", out, err.Error())
		}
	}
}

// TestAVConverter_GarbageOpts asserts Convert rejects unparseable opts
// before invoking any subprocess (AVOptsFromMap fails first) -- safe to run
// ungated since it never reaches ffprobe/ffmpeg.
func TestAVConverter_GarbageOpts(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "out.mp4")
	err := (AVConverter{}).Convert(context.Background(), filepath.Join(dir, "does-not-exist.mov"), outPath, map[string]any{"bogus": 1})
	if err == nil {
		t.Fatal("Convert(garbage opts) = nil, want error")
	}
	if !strings.HasPrefix(err.Error(), "av:") {
		t.Errorf("Convert(garbage opts) error = %q, want prefix \"av:\"", err.Error())
	}
}

// mustGenerateAVFixture creates a tiny lavfi-synthesized ~2s video fixture
// with explicit video/audio codecs -- mirrors 34-RESEARCH.md's
// live-verification methodology (testsrc+sine), never a committed binary
// fixture.
func mustGenerateAVFixture(t *testing.T, dir, filename string, codecArgs ...string) string {
	t.Helper()
	path := filepath.Join(dir, filename)
	args := append([]string{
		"-y",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=64x64:rate=10",
		"-f", "lavfi", "-i", "sine=duration=2",
		"-shortest",
	}, codecArgs...)
	args = append(args, path)
	out, err := exec.Command("ffmpeg", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("generate fixture %s: %v\n%s", filename, err, out)
	}
	return path
}

// TestAVConverter_Transcode_LiveBinary proves the mov -> mp4 transcode path
// (AVC-01) produces an ffprobe-confirmed h264 output against a real ffmpeg
// binary.
func TestAVConverter_Transcode_LiveBinary(t *testing.T) {
	requireLiveAVBinaries(t)
	dir := t.TempDir()
	src := mustGenerateAVFixture(t, dir, "src.mov", "-c:v", "libx264", "-c:a", "aac")
	outPath := filepath.Join(dir, "out.mp4")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := (AVConverter{}).Convert(ctx, src, outPath, nil); err != nil {
		t.Fatalf("Convert(mov->mp4) = %v, want nil", err)
	}
	codec, _, _, err := probeVideoStream(context.Background(), outPath)
	if err != nil {
		t.Fatalf("probeVideoStream(out): %v", err)
	}
	if codec != "h264" {
		t.Errorf("output video codec = %q, want h264", codec)
	}
}

// TestAVConverter_MP4ToWebM_LiveBinary proves the mp4 -> webm transcode path
// (AVC-02) always produces a VP9/Opus output against a real ffmpeg binary.
func TestAVConverter_MP4ToWebM_LiveBinary(t *testing.T) {
	requireLiveAVBinaries(t)
	dir := t.TempDir()
	src := mustGenerateAVFixture(t, dir, "src.mp4", "-c:v", "libx264", "-c:a", "aac")
	outPath := filepath.Join(dir, "out.webm")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := (AVConverter{}).Convert(ctx, src, outPath, nil); err != nil {
		t.Fatalf("Convert(mp4->webm) = %v, want nil", err)
	}
	videoCodec, _, _, err := probeVideoStream(context.Background(), outPath)
	if err != nil {
		t.Fatalf("probeVideoStream(out): %v", err)
	}
	if videoCodec != "vp9" {
		t.Errorf("output video codec = %q, want vp9", videoCodec)
	}
	audioCodec, err := probeAudioCodec(context.Background(), outPath)
	if err != nil {
		t.Fatalf("probeAudioCodec(out): %v", err)
	}
	if audioCodec != "opus" {
		t.Errorf("output audio codec = %q, want opus", audioCodec)
	}
}

// TestAVConverter_VP9SourceToMP4_ReEncodes is the AVC-05/T-34-11 load-bearing
// contract test: a VP9/Opus source targeting mp4 must NOT be silently
// remuxed (ffmpeg's mp4 muxer would happily accept it, 34-RESEARCH.md
// Anti-Pattern 2) -- avStreamCopyLegal forces a full re-encode to h264/aac
// instead.
func TestAVConverter_VP9SourceToMP4_ReEncodes(t *testing.T) {
	requireLiveAVBinaries(t)
	dir := t.TempDir()
	src := mustGenerateAVFixture(t, dir, "src.webm", "-c:v", "libvpx-vp9", "-c:a", "libopus")
	outPath := filepath.Join(dir, "out.mp4")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := (AVConverter{}).Convert(ctx, src, outPath, nil); err != nil {
		t.Fatalf("Convert(webm/vp9/opus->mp4) = %v, want nil", err)
	}
	videoCodec, _, _, err := probeVideoStream(context.Background(), outPath)
	if err != nil {
		t.Fatalf("probeVideoStream(out): %v", err)
	}
	if videoCodec != "h264" {
		t.Errorf("output video codec = %q, want h264 (re-encode, not a silent VP9 remux into mp4)", videoCodec)
	}
	audioCodec, err := probeAudioCodec(context.Background(), outPath)
	if err != nil {
		t.Fatalf("probeAudioCodec(out): %v", err)
	}
	if audioCodec != "aac" {
		t.Errorf("output audio codec = %q, want aac (re-encode, not a silent Opus remux into mp4)", audioCodec)
	}
}

// TestAVConverter_AudioExtract_LiveBinary proves the video -> mp3/wav/m4a
// audio-extract path (AVC-03) against a real ffmpeg binary, including the
// AAC-source->m4a stream-copy case.
func TestAVConverter_AudioExtract_LiveBinary(t *testing.T) {
	requireLiveAVBinaries(t)
	dir := t.TempDir()
	src := mustGenerateAVFixture(t, dir, "src.mp4", "-c:v", "libx264", "-c:a", "aac")

	for _, target := range []string{"mp3", "wav", "m4a"} {
		t.Run(target, func(t *testing.T) {
			outPath := filepath.Join(dir, "out."+target)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := (AVConverter{}).Convert(ctx, src, outPath, nil); err != nil {
				t.Fatalf("Convert(mp4->%s) = %v, want nil", target, err)
			}
			fi, err := os.Stat(outPath)
			if err != nil || fi.Size() == 0 {
				t.Fatalf("stat output failed or empty: err=%v", err)
			}
		})
	}

	// AAC source -> m4a target: must use -c:a copy (stream unchanged, still aac).
	t.Run("m4a AAC stream-copy", func(t *testing.T) {
		outPath := filepath.Join(dir, "out_copy.m4a")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := (AVConverter{}).Convert(ctx, src, outPath, nil); err != nil {
			t.Fatalf("Convert(mp4->m4a, aac source) = %v, want nil", err)
		}
		codec, err := probeAudioCodec(context.Background(), outPath)
		if err != nil {
			t.Fatalf("probeAudioCodec(out): %v", err)
		}
		if codec != "aac" {
			t.Errorf("output audio codec = %q, want aac", codec)
		}
	})
}

// TestAVConverter_Thumbnail_LiveBinary proves the video -> jpg/png/webp
// thumbnail path (AVC-04) against a real ffmpeg binary; the webp subtest
// skips when the local ffmpeg build lacks the libwebp encoder.
func TestAVConverter_Thumbnail_LiveBinary(t *testing.T) {
	requireLiveAVBinaries(t)
	dir := t.TempDir()
	src := mustGenerateAVFixture(t, dir, "src.mp4", "-c:v", "libx264", "-c:a", "aac")

	for _, target := range []string{"jpg", "png", "webp"} {
		t.Run(target, func(t *testing.T) {
			if target == "webp" {
				requireLibwebpEncoder(t)
			}
			outPath := filepath.Join(dir, "thumb."+target)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := (AVConverter{}).Convert(ctx, src, outPath, nil); err != nil {
				t.Fatalf("Convert(mp4->%s) = %v, want nil", target, err)
			}
			f, err := os.Open(outPath)
			if err != nil {
				t.Fatalf("open output: %v", err)
			}
			defer f.Close()
			detected, _, err := Sniff(f)
			if err != nil {
				t.Fatalf("Sniff(output): %v", err)
			}
			if detected != target {
				t.Errorf("Sniff(output) = %q, want %q", detected, target)
			}
		})
	}
}

// TestAVConverter_Thumbnail_OutOfRangeSS proves an EXPLICIT out-of-range
// timecode fails closed (Pitfall 2): no output file is created, and the error
// is the client-input-fault ErrAVTimecodeOutOfRange class -- deliberately NOT
// ErrAVOutputMissingOrEmpty, which stays reserved for genuine engine faults
// so the API can tell a 422 from a 500 and the worker can tell terminal from
// retryable (WR-04).
func TestAVConverter_Thumbnail_OutOfRangeSS(t *testing.T) {
	requireLiveAVBinaries(t)
	dir := t.TempDir()
	src := mustGenerateAVFixture(t, dir, "src.mp4", "-c:v", "libx264", "-c:a", "aac") // ~2s
	outPath := filepath.Join(dir, "thumb.jpg")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := (AVConverter{}).Convert(ctx, src, outPath, map[string]any{"timecode": float64(100)})
	if err == nil {
		t.Fatal("Convert(out-of-range timecode) = nil, want a fail-closed error")
	}
	if !errors.Is(err, ErrAVTimecodeOutOfRange) {
		t.Errorf("Convert(out-of-range timecode) error = %v, want errors.Is ErrAVTimecodeOutOfRange", err)
	}
	if errors.Is(err, ErrAVOutputMissingOrEmpty) {
		t.Errorf("Convert(out-of-range timecode) error = %v, want it NOT folded into the engine-fault class", err)
	}
	if _, statErr := os.Stat(outPath); statErr == nil {
		t.Error("Convert(out-of-range timecode) created an output file; want none (Pitfall 2)")
	}
}

// TestAVConverter_Thumbnail_SubSecondSource pins CR-04: a source shorter than
// the 1.0s default seek point must still yield a thumbnail. Before the fix
// the default was substituted unconditionally and then bounds-rejected, so
// EVERY sub-second source was permanently unconvertible with no client-side
// remedy other than guessing a smaller timecode.
func TestAVConverter_Thumbnail_SubSecondSource(t *testing.T) {
	requireLiveAVBinaries(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "short.mp4")
	out, err := exec.Command("ffmpeg", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=0.4:size=64x64:rate=25",
		"-c:v", "libx264", src).CombinedOutput()
	if err != nil {
		t.Fatalf("generate sub-second fixture: %v\n%s", err, out)
	}

	outPath := filepath.Join(dir, "thumb.jpg")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := (AVConverter{}).Convert(ctx, src, outPath, nil); err != nil {
		t.Fatalf("Convert(0.4s source -> jpg) = %v, want nil (CR-04: default must clamp, not fail)", err)
	}
	if fi, statErr := os.Stat(outPath); statErr != nil || fi.Size() == 0 {
		t.Errorf("Convert(0.4s source) produced no usable thumbnail (stat err %v)", statErr)
	}
}

// TestAVConverter_Thumbnail_ExplicitZeroTimecode pins the other half of
// CR-04: {"timecode": 0} is a legitimate request for the FIRST frame and must
// be honored, not silently rewritten to the 1.0s default. With a plain
// float64 field it was byte-indistinguishable from an absent field.
func TestAVConverter_Thumbnail_ExplicitZeroTimecode(t *testing.T) {
	requireLiveAVBinaries(t)
	dir := t.TempDir()
	src := mustGenerateAVFixture(t, dir, "src.mp4", "-c:v", "libx264", "-c:a", "aac")
	outPath := filepath.Join(dir, "thumb.png")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := (AVConverter{}).Convert(ctx, src, outPath, map[string]any{"timecode": float64(0)}); err != nil {
		t.Fatalf("Convert(timecode 0 -> png) = %v, want nil (frame 0 is requestable)", err)
	}
	if fi, statErr := os.Stat(outPath); statErr != nil || fi.Size() == 0 {
		t.Errorf("Convert(timecode 0) produced no usable thumbnail (stat err %v)", statErr)
	}
}

// TestValidateAVOutput_SniffMismatch proves a non-image thumbnail output
// (bytes that do not decode as the requested target) is rejected.
func TestValidateAVOutput_SniffMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "thumb.jpg")
	if err := os.WriteFile(path, []byte("not an image, just plain text bytes"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	err := validateAVOutput(path, "jpg")
	if err == nil {
		t.Fatal("validateAVOutput(non-image bytes, jpg) = nil, want a Sniff-mismatch error")
	}
	if !strings.Contains(err.Error(), "thumbnail not a valid") {
		t.Errorf("validateAVOutput error = %q, want it to mention \"thumbnail not a valid\"", err.Error())
	}
}

// TestConvertTranscode_NoScalePassthroughBound pins Phase 36 Plan 04's
// disposition (b) "bound the path" fix (36-04-PLAN.md passthrough-residual-
// disposition, 36-RESEARCH.md Open Q3): a no-scale (resolution_height==0)
// re-encode request against a source taller than avNoScalePassthroughMaxHeight
// (1080) must be rejected with ErrAVNoScalePassthroughExceeded BEFORE any
// ffmpeg subprocess runs -- proven here by pointing convertTranscode at a
// nonexistent input path: if the guard did not fire first, the error would
// instead be ErrAVTranscodeFailed (a real ffmpeg invocation attempt), not
// this sentinel. Mirrors TestAVStageSentinels_Distinguishable's same-package,
// no-live-fixture-needed style.
func TestConvertTranscode_NoScalePassthroughBound(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist.mp4")
	c := AVConverter{}

	t.Run("source taller than 1080 with no requested scale is rejected", func(t *testing.T) {
		src := avSourceProbe{
			primary:    avVideoStream{Index: 0, CodecName: "h265", Height: 2160},
			audioCodec: "aac",
		}
		err := c.convertTranscode(context.Background(), missing, filepath.Join(dir, "out.mp4"), "mp4", AVOpts{}, src)
		if err == nil {
			t.Fatal("convertTranscode(2160p source, resolution_height=0) = nil, want a fail-closed error")
		}
		if !errors.Is(err, ErrAVNoScalePassthroughExceeded) {
			t.Errorf("convertTranscode error = %v, want errors.Is ErrAVNoScalePassthroughExceeded", err)
		}
		if errors.Is(err, ErrAVTranscodeFailed) {
			t.Error("convertTranscode error matches ErrAVTranscodeFailed, want the guard to fire BEFORE any ffmpeg invocation is attempted")
		}
		if _, statErr := os.Stat(filepath.Join(dir, "out.mp4")); statErr == nil {
			t.Error("convertTranscode(rejected passthrough) produced an output file, want none")
		}
	})

	t.Run("source at exactly the 1080 ceiling is not rejected by this guard", func(t *testing.T) {
		src := avSourceProbe{
			primary:    avVideoStream{Index: 0, CodecName: "h265", Height: 1080},
			audioCodec: "aac",
		}
		err := c.convertTranscode(context.Background(), missing, filepath.Join(dir, "out2.mp4"), "mp4", AVOpts{}, src)
		if err == nil {
			t.Fatal("convertTranscode(1080p source, missing input) = nil, want an ffmpeg-invocation error")
		}
		if errors.Is(err, ErrAVNoScalePassthroughExceeded) {
			t.Errorf("convertTranscode(1080p source) error = %v, want it NOT rejected by the no-scale passthrough bound", err)
		}
		if !errors.Is(err, ErrAVTranscodeFailed) {
			t.Errorf("convertTranscode(1080p source) error = %v, want errors.Is ErrAVTranscodeFailed (guard passed, real ffmpeg invocation attempted)", err)
		}
	})

	t.Run("an explicit resolution_height request bypasses the no-scale bound", func(t *testing.T) {
		src := avSourceProbe{
			primary:    avVideoStream{Index: 0, CodecName: "h265", Height: 2160},
			audioCodec: "aac",
		}
		err := c.convertTranscode(context.Background(), missing, filepath.Join(dir, "out3.mp4"), "mp4", AVOpts{ResolutionHeight: 1080}, src)
		if errors.Is(err, ErrAVNoScalePassthroughExceeded) {
			t.Errorf("convertTranscode(2160p source, resolution_height=1080) error = %v, want it NOT rejected by the no-scale bound (an explicit scale request is not a passthrough)", err)
		}
		if !errors.Is(err, ErrAVTranscodeFailed) {
			t.Errorf("convertTranscode(2160p source, resolution_height=1080) error = %v, want errors.Is ErrAVTranscodeFailed (guard passed, real ffmpeg invocation attempted)", err)
		}
	})

	t.Run("stream-copy remux is exempt from the no-scale bound", func(t *testing.T) {
		// h264/aac source, mp4 target, no resize/codec override requested:
		// avStreamCopyEligible is true, so convertTranscode takes the "-c
		// copy" branch and never reaches the passthrough guard at all -- a
		// remux performs no decode/encode and carries none of the measured
		// OOM/RTF risk this guard exists to close.
		src := avSourceProbe{
			primary:    avVideoStream{Index: 0, CodecName: "h264", Height: 2160},
			audioCodec: "aac",
		}
		err := c.convertTranscode(context.Background(), missing, filepath.Join(dir, "out4.mp4"), "mp4", AVOpts{}, src)
		if errors.Is(err, ErrAVNoScalePassthroughExceeded) {
			t.Errorf("convertTranscode(h264/aac source, stream-copy eligible) error = %v, want it NOT rejected by the no-scale bound", err)
		}
		if !errors.Is(err, ErrAVTranscodeFailed) {
			t.Errorf("convertTranscode(h264/aac source, stream-copy eligible) error = %v, want errors.Is ErrAVTranscodeFailed (stream-copy branch attempted against a missing input)", err)
		}
	})
}

// TestConvertTranscode_NoScaleBound_PreservesAVE02Flags re-asserts AVE-02
// (T-36-11): the passthrough-bound fix touches ONLY convertTranscode's
// guard logic, not the argv builders -- transcodeToMP4Args/
// transcodeToWebMArgs, the two builders on the affected re-encode path, must
// still carry -nostdin, -protocol_whitelist file,crypto, and "file:"-prefixed
// input/output paths after this change, byte-for-byte identical to their
// pre-fix shape (TestTranscodeToMP4Args/TestTranscodeToWebMArgs already pin
// the exact argv; this test is the AVE-02-specific re-assertion the plan
// requires alongside the new guard test above).
func TestConvertTranscode_NoScaleBound_PreservesAVE02Flags(t *testing.T) {
	builders := map[string][]string{
		"transcodeToMP4Args (no-scale)":  transcodeToMP4Args("/work/in.mov", "/work/out.mp4", "hevc", 0, 0, 2),
		"transcodeToWebMArgs (no-scale)": transcodeToWebMArgs("/work/in.mp4", "/work/out.webm", 0, 0, 2),
	}
	for name, argv := range builders {
		t.Run(name, func(t *testing.T) {
			joined := strings.Join(argv, " ")
			if !slices.Contains(argv, "-nostdin") {
				t.Errorf("%s = %v, want -nostdin", name, argv)
			}
			if !strings.Contains(joined, "-protocol_whitelist file,crypto") {
				t.Errorf("%s = %v, want -protocol_whitelist file,crypto", name, argv)
			}
			for i, a := range argv {
				if a == "-i" && i+1 < len(argv) && !strings.HasPrefix(argv[i+1], "file:") {
					t.Errorf("%s -i element = %q, want a file:-prefixed path", name, argv[i+1])
				}
			}
			if last := argv[len(argv)-1]; !strings.HasPrefix(last, "file:") {
				t.Errorf("%s output element = %q, want a file:-prefixed path", name, last)
			}
		})
	}
}

// TestAVStageSentinels_Distinguishable proves each of the three ffmpeg-stage
// call sites (D-01, 35-CONTEXT.md) wraps with its OWN sentinel, so
// errors.Is can tell transcode/audio-extract/thumbnail failures apart
// without string matching -- before this refactor all three sites emitted
// the identical "av: ffmpeg: %w" prefix. Runs against real ffmpeg with a
// nonexistent input path so each stage's ffmpeg invocation fails fast and
// deterministically -- no live fixture needed, and calling the unexported
// stage methods directly (same-package test) means the guard stage's own
// probe is never invoked, isolating exactly the ffmpeg-wrap behavior under
// test.
func TestAVStageSentinels_Distinguishable(t *testing.T) {
	requireLiveAVBinaries(t)
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	c := AVConverter{}
	src := avSourceProbe{
		duration:   2 * time.Second,
		primary:    avVideoStream{Index: 0, CodecName: "h264"},
		audioCodec: "aac",
	}

	t.Run("transcode", func(t *testing.T) {
		err := c.convertTranscode(ctx, missing, filepath.Join(dir, "out.mp4"), "mp4", AVOpts{}, src)
		if err == nil {
			t.Fatal("convertTranscode(missing input) = nil, want error")
		}
		if !errors.Is(err, ErrAVTranscodeFailed) {
			t.Errorf("convertTranscode error = %v, want errors.Is ErrAVTranscodeFailed", err)
		}
		if errors.Is(err, ErrAVAudioExtractFailed) || errors.Is(err, ErrAVThumbnailFailed) {
			t.Errorf("convertTranscode error = %v, want it NOT to match the other two stage sentinels", err)
		}
	})

	t.Run("audio-extract", func(t *testing.T) {
		err := c.convertAudioExtract(ctx, missing, filepath.Join(dir, "out.mp3"), "mp3", src)
		if err == nil {
			t.Fatal("convertAudioExtract(missing input) = nil, want error")
		}
		if !errors.Is(err, ErrAVAudioExtractFailed) {
			t.Errorf("convertAudioExtract error = %v, want errors.Is ErrAVAudioExtractFailed", err)
		}
		if errors.Is(err, ErrAVTranscodeFailed) || errors.Is(err, ErrAVThumbnailFailed) {
			t.Errorf("convertAudioExtract error = %v, want it NOT to match the other two stage sentinels", err)
		}
	})

	t.Run("thumbnail", func(t *testing.T) {
		err := c.convertThumbnail(ctx, missing, filepath.Join(dir, "thumb.jpg"), "jpg", AVOpts{}, src)
		if err == nil {
			t.Fatal("convertThumbnail(missing input) = nil, want error")
		}
		if !errors.Is(err, ErrAVThumbnailFailed) {
			t.Errorf("convertThumbnail error = %v, want errors.Is ErrAVThumbnailFailed", err)
		}
		if errors.Is(err, ErrAVTranscodeFailed) || errors.Is(err, ErrAVAudioExtractFailed) {
			t.Errorf("convertThumbnail error = %v, want it NOT to match the other two stage sentinels", err)
		}
	})
}

// TestAVTranscodeFailed_PreservesDeadlineExceeded proves the multi-%w wrap
// (fmt.Errorf("%w: %w", ErrAVTranscodeFailed, err)) keeps runCommand's
// underlying context.DeadlineExceeded reachable via errors.Is -- Go 1.20+
// multi-%w unwraps every wrapped error, not just the first, so
// errors.Is(err, context.DeadlineExceeded) must still hold on a transcode
// timeout for isAVTerminal (worker.go, Phase 35 Plan 03) to tell a
// transient timeout apart from a deterministic ffmpeg failure.
func TestAVTranscodeFailed_PreservesDeadlineExceeded(t *testing.T) {
	requireLiveAVBinaries(t)
	dir := t.TempDir()
	src := mustGenerateAVFixture(t, dir, "src.mov", "-c:v", "libx264", "-c:a", "aac")

	// A ctx whose deadline is already in the past: runCommand's Start()/Wait()
	// select races ctx.Done() (already closed) against the process's own
	// completion channel (not yet ready at select time), so it deterministically
	// takes the "killed: context deadline exceeded" branch.
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-1*time.Second))
	defer cancel()

	c := AVConverter{}
	srcProbe := avSourceProbe{duration: 2 * time.Second, primary: avVideoStream{Index: 0, CodecName: "h264"}, audioCodec: "aac"}
	err := c.convertTranscode(ctx, src, filepath.Join(dir, "out.mp4"), "mp4", AVOpts{}, srcProbe)
	if err == nil {
		t.Fatal("convertTranscode(expired ctx) = nil, want error")
	}
	if !errors.Is(err, ErrAVTranscodeFailed) {
		t.Errorf("convertTranscode(expired ctx) error = %v, want errors.Is ErrAVTranscodeFailed", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("convertTranscode(expired ctx) error = %v, want errors.Is context.DeadlineExceeded (multi-%%w must preserve the underlying runCommand error)", err)
	}
}

// TestAVProbeSource_NoVideoStream proves an audio-only source (zero video
// streams) yields an error satisfying errors.Is(err, ErrAVNoVideoStream) --
// IN-01 fold-in. Runs against a real ffmpeg-generated audio-only mp4.
func TestAVProbeSource_NoVideoStream(t *testing.T) {
	requireLiveAVBinaries(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "audio-only.mp4")
	out, err := exec.Command("ffmpeg", "-y", "-loglevel", "error",
		"-f", "lavfi", "-i", "sine=duration=1",
		"-c:a", "aac", path).CombinedOutput()
	if err != nil {
		t.Fatalf("generate audio-only fixture: %v\n%s", err, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, probeErr := avProbeSource(ctx, path)
	if probeErr == nil {
		t.Fatal("avProbeSource(audio-only source) = nil error, want ErrAVNoVideoStream")
	}
	if !errors.Is(probeErr, ErrAVNoVideoStream) {
		t.Errorf("avProbeSource(audio-only source) error = %v, want errors.Is ErrAVNoVideoStream", probeErr)
	}
}

// TestProtocolWhitelist_BlocksHTTP_Canary is AVE-02's required offline canary
// (34-RESEARCH.md "Protocol-whitelist offline canary"): a crafted HLS m3u8
// referencing an http:// segment must be rejected, with zero outbound
// connection attempted and no output file produced.
//
// WR-10: this drives the canary through the PRODUCTION argv builders rather
// than a hand-written inline argv. The old version proved only that ffmpeg's
// own -protocol_whitelist flag works -- which was never in doubt -- and would
// have kept passing if someone deleted the flag from transcodeToMP4Args
// tomorrow. Anchoring it to the builders gives the regression protection the
// test is named for. TestAVBuildersHardenEveryInvocation complements this by
// asserting the flag pair is present in every builder's output.
func TestProtocolWhitelist_BlocksHTTP_Canary(t *testing.T) {
	requireLiveAVBinaries(t)
	dir := t.TempDir()
	evilPath := filepath.Join(dir, "evil.m3u8")
	playlist := "#EXTM3U\n#EXT-X-TARGETDURATION:10\n#EXTINF:10.0,\nhttp://169.254.169.254/latest/meta-data/evil.ts\n#EXT-X-ENDLIST\n"
	if err := os.WriteFile(evilPath, []byte(playlist), 0o644); err != nil {
		t.Fatalf("write evil.m3u8: %v", err)
	}

	builders := map[string][]string{
		"transcodeToMP4Args": transcodeToMP4Args(evilPath, filepath.Join(dir, "mp4.mp4"), "h264", 0, 0, 2),
		"streamCopyArgs":     streamCopyArgs(evilPath, filepath.Join(dir, "copy.mp4"), "mp4", 0),
		"thumbnailArgs":      thumbnailArgs(evilPath, filepath.Join(dir, "thumb.jpg"), "jpg", 0, 0),
	}
	for name, argv := range builders {
		t.Run(name, func(t *testing.T) {
			outPath := strings.TrimPrefix(argv[len(argv)-1], "file:")
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if _, err := runCommand(ctx, "ffmpeg", argv...); err == nil {
				t.Fatalf("%s against an http:// HLS segment reference = nil error, want a protocol-whitelist rejection", name)
			}
			if _, statErr := os.Stat(outPath); statErr == nil {
				t.Errorf("%s produced %s despite protocol-whitelist rejection; want no output file, no outbound connection succeeded", name, outPath)
			}
		})
	}

	// Belt and braces: the full production entry point must reject it too.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	outPath := filepath.Join(dir, "convert.mp4")
	if err := (AVConverter{}).Convert(ctx, evilPath, outPath, nil); err == nil {
		t.Fatal("AVConverter.Convert(evil.m3u8) = nil, want a fail-closed rejection")
	}
	if _, statErr := os.Stat(outPath); statErr == nil {
		t.Error("AVConverter.Convert(evil.m3u8) produced an output file; want none")
	}
}
