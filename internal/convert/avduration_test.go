package convert

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// generateAudioOnlyFixture builds a real, audio-only mp4 (no video stream at
// all) -- the fixture shape TestProbeVideoStreams_NoVideoStream and
// TestProbeVideoStream_NoVideoStream need to prove ErrAVNoVideoStream (IN-01
// fold-in) is what probeVideoStreams/probeVideoStream return when ffprobe's
// "-select_streams v" reports zero streams.
func generateAudioOnlyFixture(t *testing.T, dir, name string) string {
	t.Helper()
	requireFFmpeg(t)
	path := filepath.Join(dir, name)
	out, err := exec.Command("ffmpeg", "-y", "-loglevel", "error",
		"-f", "lavfi", "-i", "sine=duration=1",
		"-c:a", "aac", path).CombinedOutput()
	if err != nil {
		t.Fatalf("generate audio-only fixture: %v\n%s", err, out)
	}
	return path
}

// TestProbeVideoStreams_NoVideoStream proves probeVideoStreams returns an
// error satisfying errors.Is(err, ErrAVNoVideoStream) for a real audio-only
// container -- IN-01 fold-in (34-REVIEW-FIX.md).
func TestProbeVideoStreams_NoVideoStream(t *testing.T) {
	requireFFmpeg(t)
	requireFFprobe(t)
	dir := t.TempDir()
	path := generateAudioOnlyFixture(t, dir, "audio-only.mp4")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := probeVideoStreams(ctx, path)
	if err == nil {
		t.Fatal("probeVideoStreams(audio-only) = nil, want ErrAVNoVideoStream")
	}
	if !errors.Is(err, ErrAVNoVideoStream) {
		t.Errorf("probeVideoStreams(audio-only) error = %v, want errors.Is ErrAVNoVideoStream", err)
	}
}

// TestProbeVideoStream_NoVideoStream proves the singular probeVideoStream
// accessor (layered over probeVideoStreams) propagates the same sentinel.
func TestProbeVideoStream_NoVideoStream(t *testing.T) {
	requireFFmpeg(t)
	requireFFprobe(t)
	dir := t.TempDir()
	path := generateAudioOnlyFixture(t, dir, "audio-only.mp4")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, _, _, err := probeVideoStream(ctx, path)
	if err == nil {
		t.Fatal("probeVideoStream(audio-only) = nil, want ErrAVNoVideoStream")
	}
	if !errors.Is(err, ErrAVNoVideoStream) {
		t.Errorf("probeVideoStream(audio-only) error = %v, want errors.Is ErrAVNoVideoStream", err)
	}
}

// generateCoverArtVideo builds a fixture whose FIRST video stream is a tiny
// 32x32 embedded picture and whose real, much larger video stream sits behind
// it at v:1 -- the exact shape of CR-03's bypass (2). Deliberately muxed to
// MKV: the mp4/mov muxers reorder streams so the real video lands at v:0
// anyway, which would make this fixture pass vacuously. Returns the fixture
// path and the real stream's height.
func generateCoverArtVideo(t *testing.T, dir string, realHeight int) (string, int) {
	t.Helper()
	real := filepath.Join(dir, "real.mp4")
	out, err := exec.Command("ffmpeg", "-y", "-loglevel", "error",
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc=duration=1:size=%dx%d:rate=10", realHeight*4/3, realHeight),
		"-f", "lavfi", "-i", "sine=duration=1", "-shortest",
		"-c:v", "libx264", "-c:a", "aac", real).CombinedOutput()
	if err != nil {
		t.Fatalf("generate real video: %v\n%s", err, out)
	}
	cover := filepath.Join(dir, "cover.jpg")
	if out, err := exec.Command("ffmpeg", "-y", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=red:size=32x32:d=0.1",
		"-frames:v", "1", cover).CombinedOutput(); err != nil {
		t.Fatalf("generate cover art: %v\n%s", err, out)
	}
	path := filepath.Join(dir, "withcover.mkv")
	if out, err := exec.Command("ffmpeg", "-y", "-loglevel", "error",
		"-i", real, "-i", cover,
		"-map", "1:v", "-map", "0:v", "-map", "0:a", "-c", "copy",
		"-disposition:v:0", "attached_pic", path).CombinedOutput(); err != nil {
		t.Fatalf("mux cover art: %v\n%s", err, out)
	}
	// Guard the fixture itself: if a future ffmpeg starts reordering MKV
	// streams too, this test would silently stop testing anything.
	probe, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=codec_name", "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatalf("probe fixture: %v", err)
	}
	if got := strings.TrimSpace(string(probe)); got != "mjpeg" {
		t.Skipf("fixture v:0 is %q, not the cover art; this ffmpeg build reorders MKV streams so the bypass shape cannot be constructed", got)
	}
	return path, realHeight
}

// TestProbeVideoStream_IgnoresCoverArt is CR-03's regression pin. ffprobe
// reports embedded cover art as a video stream, so a probe that stopped at
// "v:0" could report a 32x32 mjpeg thumbnail's codec and height while the
// real video sat behind it. That defeated two things at once: the AVC-05
// stream-copy codec contract (the gate saw "mjpeg", not the real codec) and,
// more seriously, the resolution axis of the decode-bomb guard -- an
// arbitrarily large real video stream would sail past EnforceMaxResolution on
// the strength of its thumbnail's height.
func TestProbeVideoStream_IgnoresCoverArt(t *testing.T) {
	requireFFmpeg(t)
	requireFFprobe(t)
	dir := t.TempDir()
	path, realHeight := generateCoverArtVideo(t, dir, 480)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	codec, _, height, err := probeVideoStream(ctx, path)
	if err != nil {
		t.Fatalf("probeVideoStream: %v", err)
	}
	if codec == "mjpeg" {
		t.Errorf("probeVideoStream codec = %q, want the real video codec, not the cover art's", codec)
	}
	if height != realHeight {
		t.Errorf("probeVideoStream height = %d, want the real stream's %d", height, realHeight)
	}

	// The guard must trip on the REAL stream's height, not the thumbnail's.
	if err := EnforceMaxResolution(ctx, path, 64); !errors.Is(err, ErrAVResolutionExceeded) {
		t.Errorf("EnforceMaxResolution(ceiling 64) = %v, want ErrAVResolutionExceeded -- a %dpx stream must not hide behind a 32px cover image", err, realHeight)
	}
}
