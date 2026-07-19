package convert

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"
)

// requireFFprobe skips the test when ffprobe is not on PATH -- mirrors
// libreoffice_test.go's exec.LookPath skip convention for tests requiring a
// real external binary the test image controls.
func requireFFprobe(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not on PATH; install ffmpeg locally to exercise this test")
	}
}

// TestParseProbedDuration_AdversarialValuesFailClosed proves the declared-
// duration guard cannot be bypassed by adversarial ffprobe output: NaN, +/-Inf,
// negative, and huge (float->int64-overflowing) declared durations are all
// rejected in float space BEFORE the implementation-defined float->Duration
// conversion can run (CR-01). Runs ungated (no ffprobe) and is deliberately
// platform-independent -- the amd64 MinInt64-saturation bypass this guards
// against is invisible on arm64 dev machines.
func TestParseProbedDuration_AdversarialValuesFailClosed(t *testing.T) {
	for _, raw := range []string{
		"nan",
		"NaN",
		"inf",
		"+inf",
		"-inf",
		"-1",
		"-0.5",
		"1e18",                // overflows int64 nanoseconds: MinInt64 on amd64, MaxInt64 on arm64
		"9223372036854775807", // ffmpeg's int64-microseconds ceiling in seconds, absurd but parseable
	} {
		d, err := parseProbedDuration(raw)
		if err == nil {
			t.Errorf("parseProbedDuration(%q) = (%v, nil), want fail-closed error", raw, d)
		}
		if d != 0 {
			t.Errorf("parseProbedDuration(%q) returned duration=%v alongside an error, want zero-value", raw, d)
		}
	}
}

// TestParseProbedDuration_PlausibleValuesAccepted proves ordinary ffprobe
// output (including trailing newline, as ffprobe emits) still parses.
func TestParseProbedDuration_PlausibleValuesAccepted(t *testing.T) {
	cases := map[string]time.Duration{
		"2.000000\n": 2 * time.Second,
		"0":          0,
		"0.5":        500 * time.Millisecond,
		"3600":       time.Hour,
	}
	for raw, want := range cases {
		d, err := parseProbedDuration(raw)
		if err != nil {
			t.Errorf("parseProbedDuration(%q) error: %v", raw, err)
			continue
		}
		if d != want {
			t.Errorf("parseProbedDuration(%q) = %v, want %v", raw, d, want)
		}
	}
}

// TestFfprobeDurationArgs_FilePrefix asserts IN-01 (30-REVIEW.md,
// defense-in-depth): the argv element handed to ffprobe carries the
// explicit "file:" protocol prefix, so a future client-influenced filename
// cannot be reinterpreted as a protocol/URL specifier
// (concat:/http:/pipe:) or a leading-dash option. Also asserts AVE-02/
// ROADMAP SC5: the argv carries "-protocol_whitelist","file,crypto",
// because this reused probe is invoked FIRST on untrusted video input by
// Plan 03's guard stage (T-34-08b) -- fails against the pre-hardening argv,
// passes after. Runs ungated -- pure argv construction, no subprocess
// invoked.
func TestFfprobeDurationArgs_FilePrefix(t *testing.T) {
	got := ffprobeDurationArgs("/work/in.wav")
	want := []string{"-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", "-protocol_whitelist", "file,crypto", "file:/work/in.wav"}
	if len(got) != len(want) {
		t.Fatalf("ffprobeDurationArgs = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("ffprobeDurationArgs = %v, want %v", got, want)
		}
	}
}

func TestEnforceMaxDuration_UnderCeilingPasses(t *testing.T) {
	requireFFprobe(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := EnforceMaxDuration(ctx, "testdata/audio/sample.wav", time.Hour)
	if err != nil {
		t.Fatalf("EnforceMaxDuration under a generous ceiling returned error: %v", err)
	}
}

func TestEnforceMaxDuration_OverCeilingRejected(t *testing.T) {
	requireFFprobe(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := EnforceMaxDuration(ctx, "testdata/audio/sample.wav", 1*time.Millisecond)
	if !errors.Is(err, ErrAudioDurationExceeded) {
		t.Fatalf("EnforceMaxDuration over a tiny ceiling = %v, want errors.Is(err, ErrAudioDurationExceeded)", err)
	}
}

func TestProbeDuration_NonAudioFileReturnsError(t *testing.T) {
	requireFFprobe(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	garbage, err := os.CreateTemp(t.TempDir(), "garbage-*.bin")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := garbage.Write([]byte("not audio data at all, just plain garbage bytes")); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	garbage.Close()

	d, err := ProbeDuration(ctx, garbage.Name())
	if err == nil {
		t.Fatalf("ProbeDuration(garbage) = (%v, nil), want a non-nil error", d)
	}
	if d != 0 {
		t.Fatalf("ProbeDuration(garbage) returned duration=%v alongside an error, want zero-value duration on failure", d)
	}
}

func TestProbeDuration_KnownFixtureReturnsPlausibleDuration(t *testing.T) {
	requireFFprobe(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	d, err := ProbeDuration(ctx, "testdata/audio/sample.wav")
	if err != nil {
		t.Fatalf("ProbeDuration(sample.wav) error: %v", err)
	}
	// sample.wav was generated as a 2-second sine tone (Task 1 fixture).
	if d < 1*time.Second || d > 3*time.Second {
		t.Fatalf("ProbeDuration(sample.wav) = %v, want roughly 2s", d)
	}
}
