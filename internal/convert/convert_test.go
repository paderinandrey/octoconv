package convert

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"
)

func TestNormalizeFormat(t *testing.T) {
	cases := map[string]string{
		"PNG":   "png",
		".jpg":  "jpg",
		"JPEG":  "jpg",
		" tif ": "tiff",
		"webp":  "webp",
	}
	for in, want := range cases {
		if got := NormalizeFormat(in); got != want {
			t.Errorf("NormalizeFormat(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRegistryLibvipsPairs(t *testing.T) {
	// Default registry is populated by converters.go init().
	if !Default.Supports("png", "webp") {
		t.Error("expected png->webp to be supported")
	}
	// Alias + case normalization should resolve to the same pair.
	if !Default.Supports("JPEG", ".PNG") {
		t.Error("expected jpeg->png (aliased/cased) to be supported")
	}
	if Default.Supports("png", "png") {
		t.Error("identity pair png->png must not be registered")
	}
	if Default.Supports("png", "mp3") {
		t.Error("unsupported pair png->mp3 should not be supported")
	}
	if _, ok := Default.Lookup("heic", "jpg"); !ok {
		t.Error("expected a converter for heic->jpg")
	}
}

// TestRunCommandTimeout verifies the hardened exec kills a long-running child
// when the context deadline fires, and returns the context error.
func TestRunCommandTimeout(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := runCommand(ctx, "sleep", "10")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from timed-out command, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("command not killed promptly: took %v", elapsed)
	}
}

// TestRunCommandFailure surfaces a non-zero exit as an error.
func TestRunCommandFailure(t *testing.T) {
	if _, err := exec.LookPath("false"); err != nil {
		t.Skip("false not available")
	}
	if err := runCommand(context.Background(), "false"); err == nil {
		t.Fatal("expected error from `false`, got nil")
	}
}
