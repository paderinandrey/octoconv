package convert

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// audioModelPath stores the AUDIO_MODEL_PATH budget for every subsequent
// AudioConverter.model() call. It is set once at process startup via
// SetAudioModelPath -- mirroring effectiveVeraPDFTimeout's threading
// (verapdf.go), this package never reads AUDIO_MODEL_PATH (or any env var)
// directly; env-only-in-main is the enforced convention. Zero value (empty
// string) means "never set", in which case model() falls through to
// defaultAudioModelPath.
var audioModelPath string

// SetAudioModelPath stores the AUDIO_MODEL_PATH override for every
// subsequent AudioConverter.model() call. Call exactly once at process
// startup, BEFORE the asynq server starts consuming tasks (single write
// before any concurrent reader -- no mutex needed, mirroring
// SetVeraPDFTimeout's contract in verapdf.go).
func SetAudioModelPath(path string) {
	audioModelPath = path
}

// audioSourceFormats and audioTargetFormats are the two disjoint sets whose
// cross-product Pairs() advertises (AUD-02). Unlike libvips's imageFormats
// (a single set converted to itself, minus identity pairs), source and
// target here never overlap, so every combination is valid -- no from==to
// filter is needed (mirrors libvips.go's nested-loop shape).
var (
	audioSourceFormats = []string{"mp3", "wav", "m4a", "ogg"}
	audioTargetFormats = []string{"txt", "srt", "vtt", "json"}
)

// defaultAudioModelPath is the path Phase 32 will bake the whisper.cpp model
// to inside the worker image. It is a compile-time server constant, never
// derived from client input (T-30-10) -- the model is NEVER built from
// client bytes (Pitfall 11 / anti-pattern: an unvalidated path reaching a
// subprocess argv is a path-traversal/elevation risk, the same class of
// issue OPTS-01/02 already closed once for LibreOffice's PDF/A filter
// suffix and AudioOpts.Language's allowlist).
const defaultAudioModelPath = "/models/ggml-base.bin"

// AudioConverter transcribes audio by shelling out to `ffmpeg` (normalize)
// then `whisper-cli` (transcribe) -- the fourth engine class (EngineAudio),
// after image (libvips), document (LibreOffice), and html (chromium). It is
// registered into convert.Default by converters.go (Phase 31, AUD-05), which
// makes EngineFor/Classes()/Lookup audio-aware; the model path itself is
// resolved at runtime via model(), never at registration time.
type AudioConverter struct {
	// modelPath overrides defaultAudioModelPath when non-empty, letting
	// tests inject a local model path (e.g. Plan 01's
	// ~/.cache/whisper/ggml-base.bin) without making Convert's signature
	// env-dependent -- consistent with the other converters, which never
	// read os.Getenv directly.
	modelPath string
}

// model returns the model path to pass to whisper-cli's -m flag: the
// injected modelPath when set (test-only), else the process-wide
// audioModelPath set via SetAudioModelPath (AUDIO_MODEL_PATH, resolved once
// at cmd/*-worker/main.go startup), else the server-constant default. This
// 3-tier fallback is a strict superset of the prior 2-tier behavior -- no
// existing test-injection path changes.
func (c AudioConverter) model() string {
	if c.modelPath != "" {
		return c.modelPath
	}
	if audioModelPath != "" {
		return audioModelPath
	}
	return defaultAudioModelPath
}

// Pairs returns every {source, target} combination of audioSourceFormats x
// audioTargetFormats (16 pairs) -- source and target sets are disjoint, so
// no identity-pair filter is needed (unlike libvips.go's imageFormats).
func (AudioConverter) Pairs() []Pair {
	pairs := make([]Pair, 0, len(audioSourceFormats)*len(audioTargetFormats))
	for _, from := range audioSourceFormats {
		for _, to := range audioTargetFormats {
			pairs = append(pairs, Pair{From: from, To: to})
		}
	}
	return pairs
}

// Engine reports the audio engine class (D-01).
func (AudioConverter) Engine() string { return EngineAudio }

// whisperOutputFlag maps a normalized target format onto whisper-cli's own
// output flag (RESEARCH.md "CLI flags relevant to the json target"). json
// maps to -ojf (not the plain -oj) so segment- AND word/token-level
// timestamps come in one invocation -- the SEED-001-forward mapping AUD-02
// requires (target=json carries both timestamp granularities).
func whisperOutputFlag(target string) []string {
	switch target {
	case "txt":
		return []string{"-otxt"}
	case "srt":
		return []string{"-osrt"}
	case "vtt":
		return []string{"-ovtt"}
	case "json":
		return []string{"-ojf"}
	default:
		return nil
	}
}

// whisperArgs builds whisper-cli's argv from already-validated inputs.
// o.Language/o.Translate are allowlist-validated upstream (AudioOptsFromMap
// -> ParseAudioOpts) and passed as discrete argv slice elements, never a
// shell string; runCommand never invokes a shell (exec.go), so there is no
// shell-metacharacter injection surface regardless (RESEARCH.md "Argv
// construction"). When no language was requested, -l auto is passed
// EXPLICITLY: whisper-cli's own built-in default is -l en, which would
// silently mis-transcribe non-English audio for a Russian-first internal
// client base while exiting 0 with a structurally valid transcript (WR-03)
// -- "auto" is already in audioLanguageAllowlist, so the default and the
// explicit client opt take the identical path.
func whisperArgs(model, normPath, outBase string, outFlags []string, o AudioOpts) []string {
	args := []string{"-m", model, "-f", normPath, "-of", outBase}
	args = append(args, outFlags...)
	lang := o.Language
	if lang == "" {
		lang = "auto"
	}
	args = append(args, "-l", lang)
	if o.Translate {
		args = append(args, "-tr")
	}
	return args
}

// Convert runs the two-stage ffmpeg-normalize -> whisper-cli-transcribe
// pipeline (RESEARCH.md "Pattern 1: Two-stage hardened-exec pipeline"),
// both stages sharing the single caller-supplied ctx (one
// AUDIO_ENGINE_TIMEOUT bound covers both, per the phase's success
// criterion 2). Stage 1 normalizes inPath to 16kHz mono s16 PCM WAV at a
// scratch path inside filepath.Dir(outPath) (mirrors chromium.go's
// workDir-scratch-file convention -- inPath is never mutated). Stage 2
// invokes whisper-cli against the normalized WAV, selecting the output
// flag via the Pair mechanism (whisperOutputFlag) and pinning a
// deterministic -of output path (RESEARCH.md "Output file naming" --
// whisper-cli's default naming convention must never be relied upon).
//
// Accepted residual risk (SC5, transcription half): whisper-family models
// hallucinate (loop a short phrase) on silence/music/noise and exit 0 with
// a structurally-valid transcript. The pinned whisper-cli v1.9.1 binary
// exposes no no_speech_prob/avg_logprob field to catch this
// (source-verified, RESEARCH.md "Hallucination on Silence") -- no
// stderr-substring classifier in this codebase can distinguish a
// hallucinated transcript from a genuine one. This is logged here as an
// explicit accepted risk, not attempted as a build requirement of this
// phase; whisper-cli v1.9.1's own --vad/--vad-model flags are a possible
// future mitigation lever, not built.
func (c AudioConverter) Convert(ctx context.Context, inPath, outPath string, opts map[string]any) error {
	// Garbage opts (e.g. a corrupt jobs.options column) is a deterministic
	// failure, not a transient one -- AudioOptsFromMap applies the same
	// strictness (DisallowUnknownFields + allowlist) here as at the API
	// write path (D-10), mirroring DocOptsFromMap's/HTMLOptsFromMap's use
	// in the other converters' Convert.
	o, err := AudioOptsFromMap(opts)
	if err != nil {
		return fmt.Errorf("audio: %w", err)
	}

	// Fail fast on an unsupported target BEFORE stage 1 runs: without this
	// check, a caller bug (unrecognized/missing outPath extension) would
	// burn a full ffmpeg decode plus a whisper-cli transcription -- the most
	// expensive operation in the system -- only to fail at the final
	// validateAudioOutput stat with a misleading error. Registry routing
	// makes this unreachable in the wired flow, but Convert is exported and
	// the sibling converters fail fast on their invalid-input paths.
	targetFormat := NormalizeFormat(filepath.Ext(outPath))
	outFlags := whisperOutputFlag(targetFormat)
	if outFlags == nil {
		return fmt.Errorf("audio: unsupported target format %q", targetFormat)
	}

	workDir := filepath.Dir(outPath) // caller's per-job workDir; already unique, already cleaned up
	normPath := filepath.Join(workDir, "norm.wav")

	// Stage 1: ffmpeg normalize. A distinguishable "ffmpeg:" prefix on this
	// stage's errors lets a FUTURE worker-layer classifier (Phase 31, out
	// of scope here) split ffmpeg-stage failures (malformed input ->
	// likely terminal) from whisper-stage failures (likely transient) --
	// deliberately does not foreclose that classifier.
	if _, err := runCommand(ctx, "ffmpeg", "-y", "-i", inPath,
		"-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le", normPath); err != nil {
		return fmt.Errorf("audio: ffmpeg: %w", err)
	}

	// Stage 2: whisper-cli transcribe. targetFormat (validated above) drives
	// which output flag Pairs() advertised for this job; -of pins a
	// deterministic output path (never rely on whisper-cli's default
	// input-filename-based naming -- RESEARCH.md "Output file naming").
	outBase := strings.TrimSuffix(outPath, filepath.Ext(outPath))
	args := whisperArgs(c.model(), normPath, outBase, outFlags, o)
	if _, err := runCommand(ctx, "whisper-cli", args...); err != nil {
		return fmt.Errorf("audio: whisper-cli: %w", err)
	}

	return validateAudioOutput(outPath)
}

// validateAudioOutput guards against the same class of "exit 0 but
// empty/missing output" failure mode validatePDF closes for LibreOffice
// (libreoffice.go) -- only the size>0 subset applies here: unlike PDF,
// none of txt/srt/vtt/json share a single magic-byte signature, so a
// content-format check is not possible generically.
func validateAudioOutput(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("audio: stat output: %w", err)
	}
	if fi.Size() == 0 {
		return fmt.Errorf("audio: output is empty")
	}
	return nil
}
