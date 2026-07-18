package convert

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ErrAudioDurationExceeded is returned when the declared duration exceeds
// the configured ceiling -- fail-closed (D-07/VALID-03 precedent), the
// audio analog of ErrDimensionsUnknown/the image pixel-ceiling rejection
// (T-30-02). This phase only needs this Go-level error to exist and be
// unit-testable; mapping it to an HTTP 422 response is a future
// (out-of-scope) API-routing phase's job.
var ErrAudioDurationExceeded = errors.New("declared audio duration exceeds configured maximum")

// ProbeDuration runs ffprobe as its own short, bounded, killable subprocess
// (runCommand, exec.go) to read the container's declared duration BEFORE any
// decode/transcribe step runs -- the audio analog of dimensions.go's
// declared-pixel-ceiling check (VALID-03/Phase 7 precedent). ctx should
// carry a SHORT bound distinct from the full engine timeout (ffprobe reading
// container metadata is near-instant even for large files; it must never be
// allowed to run for the full AUDIO_ENGINE_TIMEOUT budget) -- see T-30-03.
func ProbeDuration(ctx context.Context, path string) (time.Duration, error) {
	out, err := runCommand(ctx, "ffprobe", "-v", "error", "-show_entries",
		"format=duration", "-of", "default=noprint_wrappers=1:nokey=1", path)
	if err != nil {
		return 0, fmt.Errorf("ffprobe: %w", err)
	}
	secs, perr := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if perr != nil {
		return 0, fmt.Errorf("ffprobe: unparseable duration %q: %w", out, perr)
	}
	return time.Duration(secs * float64(time.Second)), nil
}

// EnforceMaxDuration probes path's declared duration and rejects it with
// ErrAudioDurationExceeded when it exceeds max -- fail-closed BEFORE the
// normalize/transcribe pipeline ever runs (order matters: sniff -> duration
// guard -> normalize -> transcribe, never normalize-then-check, per
// 30-RESEARCH.md's "ffmpeg preprocessing on untrusted input" pitfall). max
// is a plain parameter, NOT read from any env var here -- the API layer
// wires AUDIO_MAX_DURATION_SECONDS in a later out-of-scope phase.
//
// Accepted residual risk (T-30, SC5): this guard rejects an oversized
// DECLARED duration, but cannot detect hallucination-on-silence -- whisper
// exits 0 with a structurally-valid transcript even on silence/music/noise,
// and the pinned whisper-cli v1.9.1 binary's native -oj/-ojf JSON output has
// no free no_speech_prob/avg_logprob field to lean on as a cheap signal (the
// only per-token confidence available is "p" under -ojf, a token
// probability, not a segment-level no-speech signal). Do not attempt
// hallucination detection/mitigation as part of this guard.
func EnforceMaxDuration(ctx context.Context, path string, max time.Duration) error {
	d, err := ProbeDuration(ctx, path)
	if err != nil {
		return err
	}
	if d > max {
		return fmt.Errorf("%w: declared %v exceeds ceiling %v", ErrAudioDurationExceeded, d, max)
	}
	return nil
}
