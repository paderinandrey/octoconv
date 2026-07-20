package worker

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"

	"github.com/apaderin/octoconv/internal/convert"
)

func TestIsTerminalStorageNoSuchKey(t *testing.T) {
	// internal/storage wraps every minio error via fmt.Errorf("...: %w", err);
	// isTerminal must unwrap through that chain to find the NoSuchKey code.
	raw := minio.ErrorResponse{Code: minio.NoSuchKey, Message: "The specified key does not exist."}
	wrapped := fmt.Errorf("download %q: %w", "uploads/x/0-in.png", raw)
	if !isTerminal(wrapped) {
		t.Fatal("expected isTerminal(NoSuchKey) = true")
	}
}

func TestIsTerminalNoConverter(t *testing.T) {
	err := fmt.Errorf("no converter for %s -> %s", "bmp", "webp")
	if !isTerminal(err) {
		t.Fatal("expected isTerminal(\"no converter for ...\") = true")
	}
}

func TestIsTerminalVipsSignatures(t *testing.T) {
	cases := []string{
		"convert: exit status 1: vips__file_read_signature: is not a known file format",
		"convert: exit status 1: Premature end of JPEG file",
		"convert: exit status 1: JPEG datastream contains no image",
	}
	for _, msg := range cases {
		if !isTerminal(errors.New(msg)) {
			t.Fatalf("expected isTerminal(%q) = true", msg)
		}
	}
}

func TestIsTerminalTransientDefault(t *testing.T) {
	cases := []error{
		errors.New("dial tcp: connection refused"),
		errors.New("context deadline exceeded"),
		fmt.Errorf("upload %q: %w", "results/x/0-out.webp", errors.New("connection reset by peer")),
		nil,
	}
	for _, err := range cases {
		if isTerminal(err) {
			t.Fatalf("expected isTerminal(%v) = false (broad-retry default, D-01)", err)
		}
	}
}

func TestIsTerminalLibreOfficeSignatures(t *testing.T) {
	cases := []string{
		"convert: libreoffice: output missing %PDF- magic bytes",
		"convert: libreoffice: output is empty",
		"convert: libreoffice: no export filter for docx -> mp3",
		"convert: libreoffice: output does not match expected container format odt",
		"convert: libreoffice: produced no output file for \"odt\": stat /work/in.odt: no such file or directory",
		"convert: libreoffice: pdf_profile requested for non-pdf target \"odt\"",
	}
	for _, msg := range cases {
		if !isTerminal(errors.New(msg)) {
			t.Fatalf("expected isTerminal(%q) = true", msg)
		}
	}
}

// TestIsTerminalTimeoutUnchanged asserts that a wrapped context.DeadlineExceeded
// (the shape a document/image engine timeout actually surfaces as) is STILL
// transient under the shared isTerminal — the image path (HandleImageConvert)
// must keep retrying its timeouts. Only the engine-scoped isDocumentTerminal
// diverges from this.
func TestIsTerminalTimeoutUnchanged(t *testing.T) {
	wrapped := fmt.Errorf("convert: %w", fmt.Errorf("soffice killed: %w", context.DeadlineExceeded))
	if isTerminal(wrapped) {
		t.Fatal("expected isTerminal(wrapped context.DeadlineExceeded) = false — image path must keep retrying timeouts")
	}
}

func TestIsDocumentTerminal(t *testing.T) {
	// A DOCUMENT_ENGINE_TIMEOUT expiry (exec.go's process-group-kill shape,
	// preserved through libreoffice.go and process()'s %w wrapping) IS
	// terminal for the document engine — DOC-08's deliberate divergence.
	timeoutErr := fmt.Errorf("convert: %w", fmt.Errorf("soffice killed: %w", context.DeadlineExceeded))
	if !isDocumentTerminal(timeoutErr) {
		t.Fatal("expected isDocumentTerminal(wrapped context.DeadlineExceeded) = true (DOC-08)")
	}

	// Delegates to isTerminal for every non-timeout signature.
	terminalCases := []error{
		fmt.Errorf("no converter for %s -> %s", "docx", "png"),
		errors.New("convert: libreoffice: output is empty"),
		errors.New("convert: libreoffice: output missing %PDF- magic bytes"),
		errors.New("convert: libreoffice: no export filter for docx -> mp3"),
		errors.New("convert: libreoffice: output does not match expected container format odt"),
		fmt.Errorf("download %q: %w", "uploads/x/0-in.docx", minio.ErrorResponse{Code: minio.NoSuchKey}),
	}
	for _, err := range terminalCases {
		if !isDocumentTerminal(err) {
			t.Fatalf("expected isDocumentTerminal(%v) = true", err)
		}
	}

	transientCases := []error{
		errors.New("dial tcp: connection refused"),
		nil,
	}
	for _, err := range transientCases {
		if isDocumentTerminal(err) {
			t.Fatalf("expected isDocumentTerminal(%v) = false", err)
		}
	}
}

func TestIsTerminalChromiumSignatures(t *testing.T) {
	cases := []string{
		"convert: chromium: output missing %PDF- magic bytes",
		"convert: chromium: output is empty",
		// Live-observed (Plan 04): chromium-headless-shell can exit 0 while
		// writing no output file at all; validatePDF's os.Stat failure
		// surfaces as "chromium: stat output: stat <path>: no such file or
		// directory".
		"convert: chromium: stat output: stat /work/out.pdf: no such file or directory",
	}
	for _, msg := range cases {
		if !isTerminal(errors.New(msg)) {
			t.Fatalf("expected isTerminal(%q) = true", msg)
		}
	}
}

// TestIsTerminalVeraPDFSignatures proves D-06 (phase 23): both terminal
// veraPDF error strings ("pdf/a non-compliant" for a clean non-compliant
// report, "pdf/a validation error" for any invocation/parse failure --
// fail-closed) classify terminal via the shared isTerminal, regardless of
// how they arrive wrapped (validateDocumentOutput wraps with "libreoffice:
// %w", process() wraps with "convert: %w").
func TestIsTerminalVeraPDFSignatures(t *testing.T) {
	cases := []string{
		"convert: libreoffice: verapdf: pdf/a non-compliant: 6.6.2.1: the document catalog dictionary doesn't contain metadata key",
		"convert: libreoffice: verapdf: pdf/a validation error: parse machine-readable report: EOF",
	}
	for _, msg := range cases {
		if !isTerminal(errors.New(msg)) {
			t.Fatalf("expected isTerminal(%q) = true", msg)
		}
	}
}

// TestIsDocumentTerminalVeraPDFSignatures proves both veraPDF terminal
// signatures also classify terminal through isDocumentTerminal (the
// document engine's engine-scoped classifier that HandleDocumentConvert
// actually calls) -- fail-closed reaches MarkFailed+SkipRetry, not an asynq
// retry loop, and (per D-07) rides the existing
// {"engine_stderr": err.Error()} MarkFailed detail path with no new logging.
func TestIsDocumentTerminalVeraPDFSignatures(t *testing.T) {
	cases := []error{
		errors.New("convert: libreoffice: verapdf: pdf/a non-compliant: 6.2.4.3: DeviceRGB colour space is used without RGB output intent profile"),
		errors.New("convert: libreoffice: verapdf: pdf/a validation error: start verapdf: exec: \"verapdf\": executable file not found in $PATH"),
	}
	for _, err := range cases {
		if !isDocumentTerminal(err) {
			t.Fatalf("expected isDocumentTerminal(%v) = true", err)
		}
	}
}

func TestIsHTMLTerminal(t *testing.T) {
	// An HTML_ENGINE_TIMEOUT expiry (exec.go's process-group-kill shape,
	// preserved through chromium.go and process()'s %w wrapping) IS terminal
	// for the html engine — HTML-01's deliberate divergence, mirroring
	// isDocumentTerminal's DOC-08 shape.
	timeoutErr := fmt.Errorf("convert: %w", fmt.Errorf("chromium-headless-shell killed: %w", context.DeadlineExceeded))
	if !isHTMLTerminal(timeoutErr) {
		t.Fatal("expected isHTMLTerminal(wrapped context.DeadlineExceeded) = true (HTML-01)")
	}

	// Delegates to isTerminal for every non-timeout signature.
	terminalCases := []error{
		fmt.Errorf("no converter for %s -> %s", "html", "png"),
		errors.New("convert: chromium: output is empty"),
		errors.New("convert: chromium: output missing %PDF- magic bytes"),
		fmt.Errorf("download %q: %w", "uploads/x/0-in.html", minio.ErrorResponse{Code: minio.NoSuchKey}),
	}
	for _, err := range terminalCases {
		if !isHTMLTerminal(err) {
			t.Fatalf("expected isHTMLTerminal(%v) = true", err)
		}
	}

	transientCases := []error{
		errors.New("dial tcp: connection refused"),
		nil,
	}
	for _, err := range transientCases {
		if isHTMLTerminal(err) {
			t.Fatalf("expected isHTMLTerminal(%v) = false", err)
		}
	}
}

// TestIsAudioTerminal proves Key Decision 1's stage-aware split (SC2,
// BINDING per STATE.md — the superseded blanket-timeoutIsTerminal shape must
// NOT be re-litigated): a ffmpeg-stage failure/timeout is terminal (a
// malformed/adversarial input signal); a whisper-stage timeout on audio that
// already passed the ffprobe duration/format check is transient, bounded by
// AUDIO_MAX_RETRY; a duration-guard rejection (ErrAudioDurationExceeded) is
// always terminal; every other error falls through to the shared isTerminal
// classifier (minio.NoSuchKey / "no converter for" / nil).
func TestIsAudioTerminal(t *testing.T) {
	if isAudioTerminal(nil) {
		t.Fatal("expected isAudioTerminal(nil) = false")
	}

	// ffmpeg-stage timeout -> terminal (malformed/adversarial input signal).
	ffmpegTimeout := fmt.Errorf("convert: %w", fmt.Errorf("audio: ffmpeg: %w", fmt.Errorf("ffmpeg killed: %w", context.DeadlineExceeded)))
	if !isAudioTerminal(ffmpegTimeout) {
		t.Fatal("expected isAudioTerminal(ffmpeg-stage timeout) = true (Key Decision 1)")
	}

	// ffmpeg-stage non-timeout failure -> terminal.
	ffmpegFailure := fmt.Errorf("convert: %w", fmt.Errorf("audio: ffmpeg: %w", errors.New("exit status 1: Invalid data found when processing input")))
	if !isAudioTerminal(ffmpegFailure) {
		t.Fatal("expected isAudioTerminal(ffmpeg-stage failure) = true (Key Decision 1)")
	}

	// whisper-stage timeout on already-duration-validated audio -> transient
	// (the distinguishing SC2 case: this must NOT match the ffmpeg-stage
	// terminal signature, and must NOT delegate to timeoutIsTerminal's
	// blanket DeadlineExceeded-is-terminal shape).
	whisperTimeout := fmt.Errorf("convert: %w", fmt.Errorf("audio: whisper-cli: %w", fmt.Errorf("whisper-cli killed: %w", context.DeadlineExceeded)))
	if isAudioTerminal(whisperTimeout) {
		t.Fatal("expected isAudioTerminal(whisper-stage timeout) = false (Key Decision 1 — bounded by AUDIO_MAX_RETRY)")
	}

	// ErrAudioDurationExceeded -> always terminal.
	durationErr := fmt.Errorf("convert: %w", convert.ErrAudioDurationExceeded)
	if !isAudioTerminal(durationErr) {
		t.Fatal("expected isAudioTerminal(ErrAudioDurationExceeded) = true")
	}

	// Deterministic ffprobe-stage failures -> terminal (WR-01): the duration
	// guard's probe runs BEFORE ffmpeg on the same untrusted input, so a
	// corrupt-but-sniffable file (valid magic bytes, broken container
	// metadata) must fail terminal at the very first probe, not burn the
	// AUDIO_MAX_RETRY + reconciler budget. Shapes mirror audioduration.go's
	// actual wrapping: ProbeDuration wraps runCommand errors with
	// "ffprobe: %w" (yielding exec.go's "ffprobe failed:" on a non-zero
	// exit) and parseProbedDuration emits the unparseable/implausible
	// rejections directly.
	ffprobeTerminalCases := []error{
		errors.New("ffprobe: ffprobe failed: exit status 1: moov atom not found"),
		errors.New(`ffprobe: unparseable duration "N/A": strconv.ParseFloat: parsing "N/A": invalid syntax`),
		errors.New("ffprobe: implausible duration -1"),
	}
	for _, err := range ffprobeTerminalCases {
		if !isAudioTerminal(err) {
			t.Fatalf("expected isAudioTerminal(%v) = true (deterministic ffprobe-stage failure, WR-01)", err)
		}
	}

	// ffprobe environment/timeout shapes -> transient (WR-01's documented
	// split): a missing binary or a probe-ctx expiry is a deployment/load
	// problem, not an input problem — retry may succeed.
	ffprobeTransientCases := []error{
		fmt.Errorf("ffprobe: %w", errors.New(`start ffprobe: exec: "ffprobe": executable file not found in $PATH`)),
		fmt.Errorf("ffprobe: %w", fmt.Errorf("ffprobe killed: %w", context.DeadlineExceeded)),
	}
	for _, err := range ffprobeTransientCases {
		if isAudioTerminal(err) {
			t.Fatalf("expected isAudioTerminal(%v) = false (ffprobe environment/timeout shape stays transient, WR-01)", err)
		}
	}

	// Shared base classifier fallthrough.
	sharedCases := []error{
		fmt.Errorf("no converter for %s -> %s", "mp3", "txt"),
		fmt.Errorf("download %q: %w", "uploads/x/0-in.mp3", minio.ErrorResponse{Code: minio.NoSuchKey}),
	}
	for _, err := range sharedCases {
		if !isAudioTerminal(err) {
			t.Fatalf("expected isAudioTerminal(%v) = true (shared isTerminal fallthrough)", err)
		}
	}

	// Non-timeout whisper-cli failure -> transient by default (31-RESEARCH.md
	// A2, adopted: input to whisper-cli is a server-produced normalized WAV,
	// so a non-timeout failure is most plausibly environment/config).
	whisperFailure := fmt.Errorf("convert: %w", fmt.Errorf("audio: whisper-cli: %w", errors.New("exit status 1: failed to load model")))
	if isAudioTerminal(whisperFailure) {
		t.Fatal("expected isAudioTerminal(non-timeout whisper-cli failure) = false (transient default)")
	}

	if isAudioTerminal(errors.New("dial tcp: connection refused")) {
		t.Fatal("expected isAudioTerminal(transient network error) = false")
	}

	// WR-04: whisper.go's pre-ffmpeg minimum-budget guard fires when an
	// upstream stage (stalled S3 download) pre-consumed the whole-attempt
	// deadline. Its distinct error shape deliberately carries NO
	// "audio: ffmpeg:" prefix, so it must classify transient (asynq retries
	// the network-transient attempt) instead of the terminal
	// corrupt-input misclassification the guard exists to prevent.
	budgetErr := fmt.Errorf("convert: %w", fmt.Errorf("audio: insufficient attempt budget remaining: %w", context.DeadlineExceeded))
	if isAudioTerminal(budgetErr) {
		t.Fatal("expected isAudioTerminal(insufficient-budget error) = false (upstream budget exhaustion is transient, WR-04)")
	}
}

// TestIsAudioTerminalOutputSignatures pins WR-03: validateAudioOutput's
// exit-0-but-no-output shapes ("audio: output is empty", "audio: stat
// output", whisper.go) classify terminal via audio's OWN
// terminalAudioSignatures list — NOT by accidentally substring-matching the
// LibreOffice/Chromium lists' "output is empty"/"stat output" entries inside
// the shared isTerminal loop, which is how they classified before WR-03. The
// second half proves the independence directly: with the foreign lists
// emptied, the audio messages must STILL classify terminal, so rewording a
// LibreOffice/Chromium signature can never silently flip audio retry
// behavior.
func TestIsAudioTerminalOutputSignatures(t *testing.T) {
	cases := []error{
		errors.New("convert: audio: output is empty"),
		errors.New("convert: audio: stat output: stat /work/out.txt: no such file or directory"),
	}
	for _, err := range cases {
		if !isAudioTerminal(err) {
			t.Fatalf("expected isAudioTerminal(%v) = true (validateAudioOutput no-output shape, WR-03)", err)
		}
	}

	// Independence proof: the classification must not depend on another
	// engine's signature list. Empty the foreign lists and re-check.
	savedLO, savedCh := terminalLibreOfficeSignatures, terminalChromiumSignatures
	terminalLibreOfficeSignatures, terminalChromiumSignatures = nil, nil
	defer func() {
		terminalLibreOfficeSignatures, terminalChromiumSignatures = savedLO, savedCh
	}()
	for _, err := range cases {
		if !isAudioTerminal(err) {
			t.Fatalf("expected isAudioTerminal(%v) = true even with the LibreOffice/Chromium signature lists emptied — audio must classify via terminalAudioSignatures, not a foreign list (WR-03)", err)
		}
	}
}

// TestIsAVTerminal pins D-02 (35-CONTEXT.md, BINDING): a stage-aware
// transient/terminal split derived FRESH for video, NOT a copy of
// isAudioTerminal's blanket "any ffmpeg-stage failure/timeout is terminal"
// rule. The distinguishing case is the transcode-timeout row: audio's rule
// would classify it terminal (ffmpeg is audio's cheap stage), but av's
// transcode is the expensive stage, so a timeout there must stay transient.
func TestIsAVTerminal(t *testing.T) {
	if isAVTerminal(nil) {
		t.Fatal("expected isAVTerminal(nil) = false")
	}

	// D-02's single most important row: ErrAVTranscodeFailed wrapping
	// context.DeadlineExceeded -> transient. This is the one assertion that
	// distinguishes isAVTerminal from isAudioTerminal.
	transcodeTimeout := fmt.Errorf("convert: %w", fmt.Errorf("%w: %w", convert.ErrAVTranscodeFailed, fmt.Errorf("ffmpeg killed: %w", context.DeadlineExceeded)))
	if isAVTerminal(transcodeTimeout) {
		t.Fatal("D-02: expected isAVTerminal(transcode timeout) = false (transient — transcode is the expensive stage)")
	}

	// ErrAVTranscodeFailed wrapping a non-timeout ffmpeg exit error ->
	// terminal.
	transcodeFailure := fmt.Errorf("convert: %w", fmt.Errorf("%w: %w", convert.ErrAVTranscodeFailed, errors.New("exit status 1: Invalid data found when processing input")))
	if !isAVTerminal(transcodeFailure) {
		t.Fatal("D-02: expected isAVTerminal(transcode non-timeout failure) = true")
	}

	// ErrAVAudioExtractFailed and ErrAVThumbnailFailed -> terminal, both
	// with and without a wrapped context.DeadlineExceeded (cheap stages,
	// ANY failure is terminal).
	cheapStageCases := []error{
		fmt.Errorf("convert: %w", fmt.Errorf("%w: %w", convert.ErrAVAudioExtractFailed, fmt.Errorf("ffmpeg killed: %w", context.DeadlineExceeded))),
		fmt.Errorf("convert: %w", fmt.Errorf("%w: %w", convert.ErrAVAudioExtractFailed, errors.New("exit status 1"))),
		fmt.Errorf("convert: %w", fmt.Errorf("%w: %w", convert.ErrAVThumbnailFailed, fmt.Errorf("ffmpeg killed: %w", context.DeadlineExceeded))),
		fmt.Errorf("convert: %w", fmt.Errorf("%w: %w", convert.ErrAVThumbnailFailed, errors.New("exit status 1"))),
	}
	for _, err := range cheapStageCases {
		if !isAVTerminal(err) {
			t.Fatalf("D-02: expected isAVTerminal(%v) = true (cheap stage, timeout or not)", err)
		}
	}

	// Deterministic guard/output-validation rejections -> terminal
	// regardless of stage or timeout.
	deterministicCases := []error{
		fmt.Errorf("convert: %w", convert.ErrAVOutputMissingOrEmpty),
		fmt.Errorf("convert: %w", convert.ErrAVTimecodeOutOfRange),
		fmt.Errorf("convert: %w", convert.ErrAVResolutionExceeded),
		fmt.Errorf("convert: %w", convert.ErrAudioDurationExceeded),
		fmt.Errorf("convert: %w", convert.ErrAVNoVideoStream),
	}
	for _, err := range deterministicCases {
		if !isAVTerminal(err) {
			t.Fatalf("D-02: expected isAVTerminal(%v) = true (deterministic rejection, always terminal)", err)
		}
	}

	// Unrecognized S3/Postgres blip errors fall through to isTerminal(err)
	// and match its verdict.
	sharedCases := []error{
		fmt.Errorf("no converter for %s -> %s", "mkv", "mp4"),
		fmt.Errorf("download %q: %w", "uploads/x/0-in.mp4", minio.ErrorResponse{Code: minio.NoSuchKey}),
	}
	for _, err := range sharedCases {
		if !isAVTerminal(err) {
			t.Fatalf("expected isAVTerminal(%v) = true (shared isTerminal fallthrough)", err)
		}
	}
	if isAVTerminal(errors.New("dial tcp: connection refused")) {
		t.Fatal("expected isAVTerminal(transient network error) = false")
	}

	// Contrast row: isAudioTerminal and isAVTerminal must DISAGREE on a
	// transcode-timeout-shaped error, so a future refactor that collapses
	// the two classifiers fails loudly. isAudioTerminal keys on the literal
	// "audio: ffmpeg:" prefix, so this row is constructed with that exact
	// shape wrapping the same underlying context.DeadlineExceeded, to prove
	// the two rules are genuinely independent, not just differently-typed
	// spellings of the same behavior.
	audioShapedTimeout := fmt.Errorf("convert: %w", fmt.Errorf("audio: ffmpeg: %w", fmt.Errorf("ffmpeg killed: %w", context.DeadlineExceeded)))
	if !isAudioTerminal(audioShapedTimeout) {
		t.Fatal("contrast setup: expected isAudioTerminal(audio-shaped ffmpeg timeout) = true")
	}
	if isAVTerminal(transcodeTimeout) == isAudioTerminal(audioShapedTimeout) {
		t.Fatal("D-02 contrast: isAVTerminal(transcode timeout) and isAudioTerminal(ffmpeg-stage timeout) must DISAGREE — isAVTerminal must not become a copy of isAudioTerminal")
	}
}

// TestEnforceAudioGuardBeforeConvert_IN02 pins T-30-08/IN-02: an audio job
// whose downloaded file exceeds the configured ceiling is rejected BEFORE
// Convert runs (the guard runs strictly before conv.Convert, not merely
// documented as running before it), and the resulting error classifies
// terminal via isAudioTerminal. process()'s h.repo/h.store are concrete
// types (not interfaces, per ARCHITECTURE.md's "Key Abstractions" note), so
// a full Handler cannot be constructed without live Postgres/S3 — this test
// drives enforceAudioGuardBeforeConvert directly, the smallest testable
// seam process() delegates to. Gated on a real ffprobe binary (mirrors
// internal/convert's requireLiveAudioBinaries skip-gate convention) since
// EnforceMaxDuration shells out to ffprobe — there is no fake seam for it.
func TestEnforceAudioGuardBeforeConvert_IN02(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not on PATH; see Plan 01's \"Local Development Setup\"")
	}

	fixture := filepath.Join("..", "convert", "testdata", "audio", "jfk.wav") // ~11s WAV
	convertCalled := false
	convertFn := func() error {
		convertCalled = true
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Ceiling of 1s is well below jfk.wav's ~11s declared duration.
	err := enforceAudioGuardBeforeConvert(ctx, convert.EngineAudio, fixture, 1*time.Second, convertFn)
	if err == nil {
		t.Fatal("enforceAudioGuardBeforeConvert(over-ceiling audio) = nil, want ErrAudioDurationExceeded")
	}
	if !errors.Is(err, convert.ErrAudioDurationExceeded) {
		t.Fatalf("enforceAudioGuardBeforeConvert error = %v, want errors.Is ErrAudioDurationExceeded", err)
	}
	if convertCalled {
		t.Fatal("convertFn was called despite the duration guard rejecting the input — guard did not run before Convert (IN-02)")
	}
	if !isAudioTerminal(err) {
		t.Fatalf("isAudioTerminal(%v) = false, want true (duration-guard rejection always classifies terminal)", err)
	}

	// Non-audio engines never invoke the guard: a generously-small ceiling
	// (0, the zero value every non-audio worker cmd passes) must NOT reject
	// an image/document/html job — convertFn always runs.
	convertCalled = false
	if err := enforceAudioGuardBeforeConvert(ctx, convert.EngineImage, fixture, 0, convertFn); err != nil {
		t.Fatalf("enforceAudioGuardBeforeConvert(non-audio engine) = %v, want nil (guard must not run)", err)
	}
	if !convertCalled {
		t.Fatal("convertFn was not called for a non-audio engine — gate must be job.Engine == convert.EngineAudio only")
	}
}

// TestEnforceAudioGuardProbeExpiryTransient_WR02 pins the WR-01/WR-02
// interaction: when the guard's probe ctx expires (here forced via an
// already-expired parent — audioProbeTimeout derives from the parent, so the
// child deadline is expired too), the resulting "ffprobe: ffprobe killed:"
// shape must classify TRANSIENT via isAudioTerminal (a hung probe is a
// load/environment problem, not an input problem — retry may succeed), and
// convertFn must never run. Gated on a real ffprobe binary since
// EnforceMaxDuration shells out to it — there is no fake seam.
func TestEnforceAudioGuardProbeExpiryTransient_WR02(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not on PATH; see Plan 01's \"Local Development Setup\"")
	}

	fixture := filepath.Join("..", "convert", "testdata", "audio", "jfk.wav")
	convertCalled := false

	expiredCtx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	err := enforceAudioGuardBeforeConvert(expiredCtx, convert.EngineAudio, fixture, 4*time.Hour, func() error {
		convertCalled = true
		return nil
	})
	if err == nil {
		t.Fatal("enforceAudioGuardBeforeConvert(expired probe ctx) = nil, want ffprobe-killed error")
	}
	if !strings.Contains(err.Error(), "ffprobe killed:") {
		t.Fatalf("enforceAudioGuardBeforeConvert(expired probe ctx) error = %v, want the \"ffprobe killed:\" ctx-expiry shape", err)
	}
	if convertCalled {
		t.Fatal("convertFn ran despite the probe failing — guard must run strictly before Convert")
	}
	if isAudioTerminal(err) {
		t.Fatalf("isAudioTerminal(%v) = true, want false (probe-ctx expiry stays transient, WR-01/WR-02)", err)
	}
}

// TestAVFailureCode pins HandleAVConvert's terminal-path error_code mapping
// (D-09/IN-01) without requiring a live Postgres/S3 Handler: avFailureCode
// is the pure function HandleAVConvert delegates to, so this test exercises
// the mapping directly.
func TestAVFailureCode(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode string
	}{
		{
			name:     "timecode out of range",
			err:      fmt.Errorf("convert: %w", convert.ErrAVTimecodeOutOfRange),
			wantCode: "timecode_out_of_range",
		},
		{
			name:     "duration exceeded",
			err:      fmt.Errorf("convert: %w", convert.ErrAudioDurationExceeded),
			wantCode: "duration_exceeded",
		},
		{
			name:     "resolution exceeded",
			err:      fmt.Errorf("convert: %w", convert.ErrAVResolutionExceeded),
			wantCode: "resolution_exceeded",
		},
		{
			name:     "no video stream",
			err:      fmt.Errorf("convert: %w", convert.ErrAVNoVideoStream),
			wantCode: "no_video_stream",
		},
		{
			name:     "generic engine failure",
			err:      fmt.Errorf("convert: %w", convert.ErrAVTranscodeFailed),
			wantCode: "engine_error",
		},
	}
	for _, tc := range cases {
		code, message := avFailureCode(tc.err)
		if code != tc.wantCode {
			t.Errorf("%s: avFailureCode(%v) code = %q, want %q", tc.name, tc.err, code, tc.wantCode)
		}
		if message == "" {
			t.Errorf("%s: avFailureCode(%v) message is empty, want a fixed client-facing message", tc.name, tc.err)
		}
	}
}
