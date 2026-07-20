package convert

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	got := transcodeToMP4Args("/work/in.mov", "/work/out.mp4", "h264", 4)
	want := []string{
		"-y", "-nostdin", "-protocol_whitelist", "file,crypto",
		"-i", "file:/work/in.mov",
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "23",
		"-c:a", "aac", "-b:a", "128k",
		"-movflags", "+faststart",
		"-threads", "4",
		"/work/out.mp4",
	}
	assertArgv(t, got, want)
}

// TestHEVCUsesX265CRF proves the HEVC branch references x265DefaultCRF (28),
// NEVER x264DefaultCRF (23) -- Pitfall 4, AVO-03.
func TestHEVCUsesX265CRF(t *testing.T) {
	got := transcodeToMP4Args("/work/in.mov", "/work/out.mp4", "hevc", 4)
	want := []string{
		"-y", "-nostdin", "-protocol_whitelist", "file,crypto",
		"-i", "file:/work/in.mov",
		"-c:v", "libx265", "-preset", "veryfast", "-crf", "28",
		"-c:a", "aac", "-b:a", "128k",
		"-movflags", "+faststart",
		"-threads", "4",
		"/work/out.mp4",
	}
	assertArgv(t, got, want)
	if strings.Contains(strings.Join(got, " "), "libx265 -preset veryfast -crf 23") {
		t.Error("HEVC transcode argv must not carry x264DefaultCRF's value (23)")
	}
}

// TestTranscodeToWebMArgs pins the mp4 -> webm argv shape (AVC-02, always a
// full re-encode). Runs unconditionally.
func TestTranscodeToWebMArgs(t *testing.T) {
	got := transcodeToWebMArgs("/work/in.mp4", "/work/out.webm", 2)
	want := []string{
		"-y", "-nostdin", "-protocol_whitelist", "file,crypto",
		"-i", "file:/work/in.mp4",
		"-c:v", "libvpx-vp9", "-b:v", "1M",
		"-c:a", "libopus",
		"-threads", "2",
		"/work/out.webm",
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
				"/work/out.mp3",
			},
		},
		{
			name:   "wav",
			target: "wav",
			want: []string{
				"-y", "-nostdin", "-protocol_whitelist", "file,crypto",
				"-i", "file:/work/in.mp4", "-vn",
				"-c:a", "pcm_s16le",
				"/work/out.wav",
			},
		},
		{
			name:   "m4a re-encode",
			target: "m4a",
			want: []string{
				"-y", "-nostdin", "-protocol_whitelist", "file,crypto",
				"-i", "file:/work/in.mp4", "-vn",
				"-c:a", "aac", "-b:a", "128k",
				"/work/out.m4a",
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
				"/work/out.m4a",
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
			got := thumbnailArgs("/work/in.mp4", outPath, tc.target, 1.5)

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
