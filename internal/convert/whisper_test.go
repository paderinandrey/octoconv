package convert

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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
	if len(pairs) != 16 {
		t.Errorf("len(Pairs()) = %d, want 16 (4 sources x 4 targets)", len(pairs))
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
