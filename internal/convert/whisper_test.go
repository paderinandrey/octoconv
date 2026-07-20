package convert

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// testAudioModelPath resolves the local whisper.cpp model path: AUDIO_MODEL_PATH
// env if set, else the default Plan 01 installed it to
// (~/.cache/whisper/ggml-base.bin).
func testAudioModelPath() string {
	if p := os.Getenv("AUDIO_MODEL_PATH"); p != "" {
		return p
	}
	return filepath.Join(os.Getenv("HOME"), ".cache/whisper/ggml-base.bin")
}

// requireLiveAudioBinaries skips the calling test unless ffmpeg, whisper-cli,
// and the model file are all present -- mirrors libreoffice_test.go's/
// verapdf_test.go's exec.LookPath skip-gate convention.
func requireLiveAudioBinaries(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH; see Plan 01's \"Local Development Setup\"")
	}
	if _, err := exec.LookPath("whisper-cli"); err != nil {
		t.Skip("whisper-cli not on PATH; see Plan 01's \"Local Development Setup\"")
	}
	modelPath := testAudioModelPath()
	if _, err := os.Stat(modelPath); err != nil {
		t.Skipf("whisper model not found at %s; see Plan 01's \"Local Development Setup\"", modelPath)
	}
	return modelPath
}

// audioSegment/audioToken mirror the source-verified whisper-cli v1.9.1
// output_json shape (RESEARCH.md "whisper-cli v1.9.1 JSON Schema") -- only
// the fields this test asserts on are declared; unknown fields are ignored
// by encoding/json by default (no DisallowUnknownFields here, this is a
// read-only structural probe of the live binary's output, not a strict
// input parser).
type audioTimestamps struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type audioOffsets struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

type audioToken struct {
	Text       string           `json:"text"`
	Timestamps *audioTimestamps `json:"timestamps"`
	Offsets    *audioOffsets    `json:"offsets"`
	ID         int64            `json:"id"`
	P          float64          `json:"p"`
}

type audioSegment struct {
	Timestamps audioTimestamps `json:"timestamps"`
	Offsets    audioOffsets    `json:"offsets"`
	Text       string          `json:"text"`
	Tokens     []audioToken    `json:"tokens"`
}

type audioTranscript struct {
	Transcription []audioSegment `json:"transcription"`
}

// TestAudioConverter_JSONFull_LiveBinary is the SC3 proof: target=json
// (whisperOutputFlag's -ojf) produces both segment- and word/token-level
// timestamps against the pinned whisper-cli v1.9.1 binary (AUD-02).
func TestAudioConverter_JSONFull_LiveBinary(t *testing.T) {
	modelPath := requireLiveAudioBinaries(t)
	c := AudioConverter{modelPath: modelPath}

	dir := t.TempDir()
	outPath := filepath.Join(dir, "out.json")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := c.Convert(ctx, "testdata/audio/jfk.wav", outPath, nil); err != nil {
		t.Fatalf("Convert(jfk.wav -> json) = %v, want nil", err)
	}

	raw, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}

	var transcript audioTranscript
	if err := json.Unmarshal(raw, &transcript); err != nil {
		t.Fatalf("output does not parse as JSON: %v\nraw: %s", err, raw)
	}

	if len(transcript.Transcription) == 0 {
		t.Fatal("transcription array is empty, want at least one segment")
	}

	// Structural, non-content assertions only (never assert an exact
	// transcript string -- ASR output is non-deterministic across model/
	// binary versions, RESEARCH.md's anti-pattern note).
	var lastOffsetTo int64
	sawTokenTimestamps := false
	for i, seg := range transcript.Transcription {
		if seg.Text == "" {
			t.Errorf("segment %d: text is empty, want non-empty", i)
		}
		if seg.Timestamps.From == "" || seg.Timestamps.To == "" {
			t.Errorf("segment %d: timestamps.from/to empty, want SRT-style strings", i)
		}
		if seg.Offsets.From > seg.Offsets.To {
			t.Errorf("segment %d: offsets.from (%d) > offsets.to (%d), want from <= to", i, seg.Offsets.From, seg.Offsets.To)
		}
		if seg.Offsets.From < lastOffsetTo {
			t.Errorf("segment %d: offsets.from (%d) < previous segment's offsets.to (%d), want monotonically non-decreasing", i, seg.Offsets.From, lastOffsetTo)
		}
		lastOffsetTo = seg.Offsets.To

		// -ojf implies a tokens array per segment (RESEARCH.md).
		if len(seg.Tokens) == 0 {
			t.Errorf("segment %d: tokens array is empty, want at least one token (-ojf requested)", i)
			continue
		}
		for j, tok := range seg.Tokens {
			if tok.Text == "" {
				t.Errorf("segment %d token %d: text is empty, want non-empty", i, j)
			}
			// id/p are present on every token per the schema; timestamps/
			// offsets are OPTIONAL per token (Assumption A3: only present
			// when whisper's t0/t1 guard passes) -- do not require them on
			// every token, only record that at least one carried them.
			if tok.Timestamps != nil {
				sawTokenTimestamps = true
			}
		}
	}
	if !sawTokenTimestamps {
		t.Error("no token in any segment carried timestamps; want at least one token with valid t0/t1 across a multi-second known-speech fixture")
	}
}

// TestAudioConverter_TextFormats_LiveBinary proves the txt/srt/vtt Pair
// selections each produce a non-empty output file via whisperOutputFlag
// (structural only -- content is non-deterministic ASR output).
func TestAudioConverter_TextFormats_LiveBinary(t *testing.T) {
	modelPath := requireLiveAudioBinaries(t)
	c := AudioConverter{modelPath: modelPath}

	for _, target := range []string{"txt", "srt", "vtt"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			outPath := filepath.Join(dir, "out."+target)

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			if err := c.Convert(ctx, "testdata/audio/jfk.wav", outPath, nil); err != nil {
				t.Fatalf("Convert(jfk.wav -> %s) = %v, want nil", target, err)
			}

			fi, err := os.Stat(outPath)
			if err != nil {
				t.Fatalf("stat output: %v", err)
			}
			if fi.Size() == 0 {
				t.Errorf("output for target %s is empty, want non-empty", target)
			}
			if filepath.Ext(outPath) != "."+target {
				t.Errorf("output extension = %q, want %q", filepath.Ext(outPath), "."+target)
			}
		})
	}
}

// TestAudioConverter_Contract asserts the Converter contract without
// requiring any live binary -- runs unconditionally.
func TestAudioConverter_Contract(t *testing.T) {
	if got := (AudioConverter{}).Engine(); got != EngineAudio {
		t.Errorf("Engine() = %q, want %q", got, EngineAudio)
	}

	pairs := (AudioConverter{}).Pairs()
	if len(pairs) != 36 {
		t.Errorf("len(Pairs()) = %d, want 36 (9 sources x 4 targets, D-04)", len(pairs))
	}

	// D-04: AudioConverter.Engine() must report EngineAudio for the five
	// video-container source pairs too, not just the original four
	// audio-native sources -- video-to-transcript rides the SAME engine
	// class/queue, no new engine constant is introduced.
	for _, from := range []string{"mp4", "mov", "avi", "mkv", "webm"} {
		found := false
		for _, p := range pairs {
			if p.From == from && p.To == "txt" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Pairs() missing {From: %q, To: \"txt\"}, want all five video containers present (D-04)", from)
		}
	}
	if got := (AudioConverter{}).Engine(); got != EngineAudio {
		t.Errorf("Engine() = %q, want %q (D-04: video sources ride the audio engine class)", got, EngineAudio)
	}

	flagCases := map[string][]string{
		"txt":  {"-otxt"},
		"srt":  {"-osrt"},
		"vtt":  {"-ovtt"},
		"json": {"-ojf"},
	}
	for target, want := range flagCases {
		got := whisperOutputFlag(target)
		if len(got) != len(want) || (len(got) > 0 && got[0] != want[0]) {
			t.Errorf("whisperOutputFlag(%q) = %v, want %v", target, got, want)
		}
	}

	if NormalizeFormat("json") != "json" {
		t.Errorf(`NormalizeFormat("json") = %q, want "json" (no alias collision)`, NormalizeFormat("json"))
	}
	if MIMEType("json") != "application/json" {
		t.Errorf(`MIMEType("json") = %q, want "application/json"`, MIMEType("json"))
	}
	if MIMEType("txt") != "text/plain" {
		t.Errorf(`MIMEType("txt") = %q, want "text/plain"`, MIMEType("txt"))
	}
	if MIMEType("srt") != "application/x-subrip" {
		t.Errorf(`MIMEType("srt") = %q, want "application/x-subrip"`, MIMEType("srt"))
	}
	if MIMEType("vtt") != "text/vtt" {
		t.Errorf(`MIMEType("vtt") = %q, want "text/vtt"`, MIMEType("vtt"))
	}

	// The four audio INPUT formats must map to real audio MIME types --
	// internal/api stores convert.MIMEType(detected) as the uploaded
	// input's Content-Type, so an omission here silently degrades every
	// audio upload to application/octet-stream (WR-04).
	inputMIMEs := map[string]string{
		"mp3": "audio/mpeg",
		"wav": "audio/wav",
		"m4a": "audio/mp4",
		"ogg": "audio/ogg",
	}
	for format, want := range inputMIMEs {
		if got := MIMEType(format); got != want {
			t.Errorf("MIMEType(%q) = %q, want %q", format, got, want)
		}
	}
}

// TestWhisperArgs asserts the exact whisper-cli argv construction, in
// particular that an absent language passes -l auto EXPLICITLY (WR-03) --
// whisper-cli's own built-in default is -l en, which would silently
// mis-transcribe non-English audio while exiting 0 -- and that an explicit
// -t <threads> pair is always present regardless of language/translate opts
// (T-32-04): whisper-cli's own default is host core count, which under a
// container cgroup CPU quota causes throttling rather than reflecting the
// container's real budget (PITFALLS.md Pitfall 5).
func TestWhisperArgs(t *testing.T) {
	cases := []struct {
		name    string
		o       AudioOpts
		threads int
		want    []string
	}{
		{
			name:    "no opts defaults to -l auto",
			o:       AudioOpts{},
			threads: 2,
			want:    []string{"-m", "/m/ggml.bin", "-f", "/w/norm.wav", "-of", "/w/out", "-otxt", "-l", "auto", "-t", "2"},
		},
		{
			name:    "explicit language passed through",
			o:       AudioOpts{Language: "ru"},
			threads: 2,
			want:    []string{"-m", "/m/ggml.bin", "-f", "/w/norm.wav", "-of", "/w/out", "-otxt", "-l", "ru", "-t", "2"},
		},
		{
			name:    "translate appends -tr",
			o:       AudioOpts{Language: "ru", Translate: true},
			threads: 2,
			want:    []string{"-m", "/m/ggml.bin", "-f", "/w/norm.wav", "-of", "/w/out", "-otxt", "-l", "ru", "-tr", "-t", "2"},
		},
		{
			name:    "threads=1",
			o:       AudioOpts{},
			threads: 1,
			want:    []string{"-m", "/m/ggml.bin", "-f", "/w/norm.wav", "-of", "/w/out", "-otxt", "-l", "auto", "-t", "1"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := whisperArgs("/m/ggml.bin", "/w/norm.wav", "/w/out", []string{"-otxt"}, tc.o, tc.threads)
			if len(got) != len(tc.want) {
				t.Fatalf("whisperArgs = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("whisperArgs = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// TestSetAudioThreads_AudioThreadCount pins the 2-tier audioThreadCount
// resolver: SetAudioThreads(n) for n > 0 wins outright; n <= 0 (including
// the zero value nothing ever set) falls through to runtime.NumCPU().
// audioThreads is process-wide package state (mirrors audioModelPath), so
// this test restores it afterward to avoid bleeding into sibling tests.
func TestSetAudioThreads_AudioThreadCount(t *testing.T) {
	orig := audioThreads
	defer func() { audioThreads = orig }()

	SetAudioThreads(4)
	if got := audioThreadCount(); got != 4 {
		t.Errorf("audioThreadCount() after SetAudioThreads(4) = %d, want 4", got)
	}

	SetAudioThreads(0)
	if got := audioThreadCount(); got != runtime.NumCPU() {
		t.Errorf("audioThreadCount() after SetAudioThreads(0) = %d, want runtime.NumCPU() = %d", got, runtime.NumCPU())
	}
}

// TestFfmpegNormalizeArgs_FilePrefix asserts IN-01 (30-REVIEW.md,
// defense-in-depth): the argv element handed to ffmpeg's -i flag carries the
// explicit "file:" protocol prefix, so a future client-influenced filename
// cannot be reinterpreted as a protocol/URL specifier
// (concat:/http:/pipe:) or a leading-dash option. It also pins WR-08
// (34-REVIEW.md): this invocation runs ffmpeg on untrusted client audio and
// must carry the same -nostdin/-protocol_whitelist hardening pair every other
// ffmpeg/ffprobe call site in this package does. Runs ungated -- pure argv
// construction, no subprocess invoked.
func TestFfmpegNormalizeArgs_FilePrefix(t *testing.T) {
	got := ffmpegNormalizeArgs("/work/in.mp3", "/work/norm.wav")
	want := []string{"-y", "-nostdin", "-protocol_whitelist", "file,crypto",
		"-i", "file:/work/in.mp3",
		"-map", "0:a:0",
		"-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le", "file:/work/norm.wav"}
	if len(got) != len(want) {
		t.Fatalf("ffmpegNormalizeArgs = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("ffmpegNormalizeArgs = %v, want %v", got, want)
		}
	}
}

// TestFfmpegNormalizeArgs_MapAdjacency pins the exact insertion point of
// D-04/D-05's "-map 0:a:0": immediately after the "-i file:<inPath>" pair
// and before "-ar" -- proves the deterministic-stream-selection fix lands
// where the multi-audio-track open question (35-RESEARCH.md Open Question
// 3) requires, and continues to assert the AVE-02 hardening flags
// (-protocol_whitelist file,crypto, -nostdin) and file: prefixes survive
// the insertion unchanged (T-34-10/WR-08 non-regression).
func TestFfmpegNormalizeArgs_MapAdjacency(t *testing.T) {
	got := ffmpegNormalizeArgs("/work/in.mp4", "/work/norm.wav")

	iIdx, mapIdx, arIdx := -1, -1, -1
	for i, a := range got {
		switch {
		case a == "-i" && iIdx == -1:
			iIdx = i
		case a == "-map" && mapIdx == -1:
			mapIdx = i
		case a == "-ar" && arIdx == -1:
			arIdx = i
		}
	}
	if iIdx == -1 || mapIdx == -1 || arIdx == -1 {
		t.Fatalf("ffmpegNormalizeArgs = %v, want -i, -map, and -ar all present", got)
	}
	if mapIdx != iIdx+2 {
		t.Errorf("ffmpegNormalizeArgs -map is at index %d, want it immediately after the -i file:<inPath> pair (index %d)", mapIdx, iIdx+2)
	}
	if got[mapIdx+1] != "0:a:0" {
		t.Errorf("ffmpegNormalizeArgs -map value = %q, want %q", got[mapIdx+1], "0:a:0")
	}
	if mapIdx >= arIdx {
		t.Errorf("ffmpegNormalizeArgs -map at index %d must precede -ar at index %d", mapIdx, arIdx)
	}

	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "-protocol_whitelist file,crypto") {
		t.Errorf("ffmpegNormalizeArgs = %v, want -protocol_whitelist file,crypto to survive the -map insertion", got)
	}
	if !strings.Contains(joined, "-nostdin") {
		t.Errorf("ffmpegNormalizeArgs = %v, want -nostdin to survive the -map insertion", got)
	}
	if got[iIdx+1] != "file:/work/in.mp4" {
		t.Errorf("ffmpegNormalizeArgs -i element = %q, want a file:-prefixed path", got[iIdx+1])
	}
	if last := got[len(got)-1]; !strings.HasPrefix(last, "file:") {
		t.Errorf("ffmpegNormalizeArgs output element = %q, want a file:-prefixed path", last)
	}
}

// TestAudioConverter_UnsupportedTargetFailsFast asserts Convert rejects an
// unsupported target extension BEFORE invoking any subprocess (WR-02): the
// input path deliberately does not exist, so if ffmpeg were invoked the error
// would be an "audio: ffmpeg:" failure instead of the unsupported-target
// error, and no stage-1 scratch file may appear. Runs ungated (no binaries
// required precisely because nothing may be executed).
func TestAudioConverter_UnsupportedTargetFailsFast(t *testing.T) {
	dir := t.TempDir()

	for _, out := range []string{"out.xyz", "out"} {
		outPath := filepath.Join(dir, out)
		err := (AudioConverter{}).Convert(context.Background(), filepath.Join(dir, "does-not-exist.wav"), outPath, nil)
		if err == nil {
			t.Fatalf("Convert(-> %s) = nil, want unsupported-target error", out)
		}
		if !strings.Contains(err.Error(), "unsupported target format") {
			t.Errorf("Convert(-> %s) error = %q, want it to mention \"unsupported target format\" (fail fast, not a subprocess error)", out, err.Error())
		}
		if _, statErr := os.Stat(filepath.Join(dir, "norm.wav")); statErr == nil {
			t.Errorf("Convert(-> %s) created norm.wav; stage 1 (ffmpeg) must not run for an unsupported target", out)
		}
	}
}

// TestAudioConverter_InsufficientBudgetFailsFast pins WR-04: when the
// whole-attempt ctx has less than minFfmpegBudget remaining (an upstream
// stage — e.g. a stalled S3 download — pre-consumed the budget), Convert
// fails fast BEFORE invoking ffmpeg with the distinct budget error rather
// than starting a doomed stage 1 that would be killed near-instantly and
// misclassified as a terminal "audio: ffmpeg:" input failure. The error
// deliberately carries NO "audio: ffmpeg:" prefix (so the worker's
// isAudioTerminal classifies it transient and asynq retries) and wraps
// context.DeadlineExceeded for errors.Is callers. Runs ungated (no binaries
// required precisely because nothing may be executed — proven by the absent
// norm.wav).
func TestAudioConverter_InsufficientBudgetFailsFast(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "out.txt")

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second) // well below minFfmpegBudget
	defer cancel()

	err := (AudioConverter{}).Convert(ctx, filepath.Join(dir, "in.wav"), outPath, nil)
	if err == nil {
		t.Fatal("Convert(near-exhausted budget) = nil, want insufficient-budget error")
	}
	if !strings.Contains(err.Error(), "insufficient attempt budget remaining") {
		t.Errorf("Convert(near-exhausted budget) error = %q, want it to mention \"insufficient attempt budget remaining\"", err.Error())
	}
	if strings.Contains(err.Error(), "audio: ffmpeg:") {
		t.Errorf("Convert(near-exhausted budget) error = %q must NOT carry the terminal \"audio: ffmpeg:\" stage prefix (WR-04)", err.Error())
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Convert(near-exhausted budget) error = %v, want errors.Is context.DeadlineExceeded", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "norm.wav")); statErr == nil {
		t.Error("Convert(near-exhausted budget) created norm.wav; stage 1 (ffmpeg) must not run below minFfmpegBudget")
	}
}

// TestAudioConverter_GarbageOpts asserts Convert rejects unparseable opts
// before invoking any subprocess (AudioOptsFromMap fails first) -- safe to
// run ungated since it never reaches ffmpeg/whisper-cli.
func TestAudioConverter_GarbageOpts(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "out.txt")

	err := (AudioConverter{}).Convert(context.Background(), "testdata/audio/jfk.wav", outPath, map[string]any{"bogus": 1})
	if err == nil {
		t.Fatal("Convert(garbage opts) = nil, want error")
	}
	if !strings.HasPrefix(err.Error(), "audio:") {
		t.Errorf("Convert(garbage opts) error = %q, want prefix \"audio:\"", err.Error())
	}
}

// TestSelectMinFfmpegBudget pins D-05: the five video containers D-04 added
// to audioSourceFormats select minFfmpegBudgetVideo; the original four
// audio-native sources keep the unchanged minFfmpegBudget. Runs
// unconditionally -- pure function, no subprocess.
func TestSelectMinFfmpegBudget(t *testing.T) {
	for _, ext := range []string{"mp4", "mov", "avi", "mkv", "webm"} {
		t.Run(ext, func(t *testing.T) {
			if got := selectMinFfmpegBudget("/work/in." + ext); got != minFfmpegBudgetVideo {
				t.Errorf("selectMinFfmpegBudget(.%s) = %v, want minFfmpegBudgetVideo (%v)", ext, got, minFfmpegBudgetVideo)
			}
		})
	}
	for _, ext := range []string{"mp3", "wav", "m4a", "ogg"} {
		t.Run(ext, func(t *testing.T) {
			if got := selectMinFfmpegBudget("/work/in." + ext); got != minFfmpegBudget {
				t.Errorf("selectMinFfmpegBudget(.%s) = %v, want minFfmpegBudget (%v)", ext, got, minFfmpegBudget)
			}
		})
	}
}

// TestAudioConverter_VideoNoAudioTrack_FailsClosed pins 35-RESEARCH.md Open
// Question 1 (RESOLVED, no code change needed): a video source with ZERO
// audio streams already fails closed today via the EXISTING isAudioTerminal
// classifier (internal/worker/worker.go) -- ffmpeg's stage-1 normalize
// exits non-zero ("Output file does not contain any stream" / "Stream map
// '0:a:0' matches no streams" once -map 0:a:0 is added), wrapped with the
// SAME "audio: ffmpeg:" prefix isAudioTerminal's strings.Contains check
// already matches on for every other ffmpeg-stage failure. This is
// currently an EMERGENT property of two independently-motivated mechanisms
// (ffmpeg's own stream-mapping failure + isAudioTerminal's blanket
// ffmpeg-stage rule), not a designed guarantee -- pinned here as a
// regression test rather than new production code, per D-04's own
// resolution.
func TestAudioConverter_VideoNoAudioTrack_FailsClosed(t *testing.T) {
	modelPath := requireLiveAudioBinaries(t)
	c := AudioConverter{modelPath: modelPath}

	dir := t.TempDir()
	src := filepath.Join(dir, "video-only.mp4")
	out, err := exec.Command("ffmpeg", "-y", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=64x64:rate=10",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", src).CombinedOutput()
	if err != nil {
		t.Fatalf("generate video-only fixture: %v\n%s", err, out)
	}

	outPath := filepath.Join(dir, "out.txt")
	// D-05: inPath is a video source, so selectMinFfmpegBudget requires
	// minFfmpegBudgetVideo (90s) remaining before stage 1 is even allowed to
	// start -- the timeout here must exceed that floor or Convert fails fast
	// with the DISTINCT "insufficient attempt budget remaining" error instead
	// of reaching ffmpeg at all, defeating the point of this test.
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	convErr := c.Convert(ctx, src, outPath, nil)
	if convErr == nil {
		t.Fatal("Convert(video with zero audio streams) = nil, want a fail-closed error")
	}
	if !strings.Contains(convErr.Error(), "audio: ffmpeg:") {
		t.Errorf("Convert(video, no audio track) error = %q, want it to carry the \"audio: ffmpeg:\" prefix isAudioTerminal already classifies TERMINAL", convErr.Error())
	}
}

// TestAudioConverter_VideoSilentAudioTrack_FailsClosed pins 35-RESEARCH.md
// Open Question 2 (PARTIALLY RESOLVED): a video source whose audio track is
// pure digital silence already fails closed today via validateAudioOutput's
// EXISTING "audio: output is empty" check (already in
// internal/worker/worker.go's terminalAudioSignatures) -- whisper-cli exits
// 0 but produces a genuinely empty transcript for near-zero-confidence
// synthetic silence.
//
// NOTE: this does NOT retire STATE.md's accepted hallucination-on-silence
// risk -- moderate-confidence real-world background chatter/music can still
// produce a structurally-valid, NON-EMPTY hallucinated transcript that this
// emptiness check cannot catch; only the specific "genuinely
// near-zero-confidence synthetic silence" shape tested here is closed.
func TestAudioConverter_VideoSilentAudioTrack_FailsClosed(t *testing.T) {
	modelPath := requireLiveAudioBinaries(t)
	c := AudioConverter{modelPath: modelPath}

	dir := t.TempDir()
	src := filepath.Join(dir, "video-silent-audio.mp4")
	out, err := exec.Command("ffmpeg", "-y", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=64x64:rate=10",
		"-f", "lavfi", "-i", "anullsrc=duration=2",
		"-shortest", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", src).CombinedOutput()
	if err != nil {
		t.Fatalf("generate silent-audio-track fixture: %v\n%s", err, out)
	}

	outPath := filepath.Join(dir, "out.txt")
	// D-05: same minFfmpegBudgetVideo (90s) floor as above -- must exceed it
	// or Convert fails fast on the budget check instead of reaching ffmpeg.
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	convErr := c.Convert(ctx, src, outPath, nil)
	if convErr == nil {
		t.Fatal("Convert(video with pure-silence audio track) = nil, want a fail-closed error")
	}
	if !strings.Contains(convErr.Error(), "audio: output is empty") {
		t.Errorf("Convert(video, silent audio track) error = %q, want it to carry \"audio: output is empty\" (isAudioTerminal's existing terminalAudioSignatures entry)", convErr.Error())
	}
}
