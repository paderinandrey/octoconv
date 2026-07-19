package convert

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// requireFFmpeg skips the test when ffmpeg is not on PATH -- mirrors
// requireFFprobe's (audioduration_test.go) skip-gate convention.
func requireFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH; install ffmpeg locally to exercise this test")
	}
}

// generateTestVideo synthesizes a tiny lavfi-generated 1-second video
// fixture at the given resolution into dir/name -- no binary video fixture
// is committed to the repo; mirrors 34-RESEARCH.md's live-verified
// synthetic-fixture approach used to produce the reference argv/JSON shapes
// this file implements against.
func generateTestVideo(t *testing.T, dir, name string, width, height int) string {
	t.Helper()
	requireFFmpeg(t)
	out := filepath.Join(dir, name)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := []string{"-y", "-f", "lavfi", "-i",
		fmt.Sprintf("color=c=black:s=%dx%d:d=1", width, height),
		"-c:v", "libx264", "-pix_fmt", "yuv420p", out}
	if _, err := runCommand(ctx, "ffmpeg", args...); err != nil {
		t.Fatalf("generate test video fixture: %v", err)
	}
	return out
}

// TestFfprobeStreamArgs_Hardening asserts AVE-02: the argv element handed to
// ffprobe carries "-protocol_whitelist","file,crypto" AND the "file:"
// protocol prefix on the path element (T-34-08). Runs ungated -- pure argv
// construction, no subprocess invoked.
func TestFfprobeStreamArgs_Hardening(t *testing.T) {
	got := ffprobeStreamArgs("/work/in.mp4")
	found := false
	for i := 0; i < len(got)-1; i++ {
		if got[i] == "-protocol_whitelist" && got[i+1] == "file,crypto" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ffprobeStreamArgs = %v, want it to contain -protocol_whitelist file,crypto", got)
	}
	last := got[len(got)-1]
	if last != "file:/work/in.mp4" {
		t.Fatalf("ffprobeStreamArgs last element = %q, want a file:-prefixed path", last)
	}
}

// TestProbeVideoStream_ParsesJSON proves probeVideoStream correctly parses a
// real ffprobe JSON response into (codec, width, height) against a
// live-generated fixture (skip-gated on ffmpeg/ffprobe, mirrors
// whisper_test.go's skip-gate philosophy).
func TestProbeVideoStream_ParsesJSON(t *testing.T) {
	requireFFmpeg(t)
	requireFFprobe(t)
	dir := t.TempDir()
	path := generateTestVideo(t, dir, "probe.mp4", 320, 240)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	codec, width, height, err := probeVideoStream(ctx, path)
	if err != nil {
		t.Fatalf("probeVideoStream: %v", err)
	}
	if codec != "h264" {
		t.Errorf("probeVideoStream codec = %q, want h264", codec)
	}
	if width != 320 || height != 240 {
		t.Errorf("probeVideoStream dims = %dx%d, want 320x240", width, height)
	}
}

// TestProbeVideoStream_NonVideoFileReturnsError proves the probe fails
// closed against a file with no video stream at all.
func TestProbeVideoStream_NonVideoFileReturnsError(t *testing.T) {
	requireFFprobe(t)
	dir := t.TempDir()
	garbage := filepath.Join(dir, "garbage.bin")
	if err := os.WriteFile(garbage, []byte("not a video file at all, just plain garbage bytes"), 0o600); err != nil {
		t.Fatalf("write garbage file: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	codec, width, height, err := probeVideoStream(ctx, garbage)
	if err == nil {
		t.Fatalf("probeVideoStream(garbage) = (%q, %d, %d, nil), want a non-nil error", codec, width, height)
	}
}

// TestEnforceMaxResolution_Rejects proves EnforceMaxResolution accepts an
// under-ceiling height and rejects an over-ceiling height with
// errors.Is(err, ErrAVResolutionExceeded), against a live-generated fixture.
func TestEnforceMaxResolution_Rejects(t *testing.T) {
	requireFFmpeg(t)
	requireFFprobe(t)
	dir := t.TempDir()
	path := generateTestVideo(t, dir, "guard.mp4", 640, 480)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := EnforceMaxResolution(ctx, path, 1080); err != nil {
		t.Fatalf("EnforceMaxResolution under a generous ceiling returned error: %v", err)
	}

	err := EnforceMaxResolution(ctx, path, 240)
	if !errors.Is(err, ErrAVResolutionExceeded) {
		t.Fatalf("EnforceMaxResolution over a tiny ceiling = %v, want errors.Is(err, ErrAVResolutionExceeded)", err)
	}
}
