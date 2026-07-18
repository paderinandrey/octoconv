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
