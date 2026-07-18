# Phase 30: Audio Engine Foundation - Pattern Map

**Mapped:** 2026-07-18
**Files analyzed:** 9 new + 2 modified (11 total, per RESEARCH.md's "Recommended file additions")
**Analogs found:** 11 / 11

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|--------------------|------|-----------|-----------------|----------------|
| `internal/convert/convert.go` (MODIFY: add `EngineAudio`) | config/registry | CRUD (constant registration) | `internal/convert/convert.go:18-22` (existing `EngineImage`/`EngineDocument`/`EngineHTML` block) | exact — same file, add one const |
| `internal/convert/sniff.go` (MODIFY: doc comment only) | utility | transform | `internal/convert/sniff.go:8-11,31-33` (existing `sniffLen`/`signatures` doc comments) | exact — same file, comment-only edit |
| `internal/convert/audiosniff.go` (NEW) | utility | transform (magic-bytes detection) | `internal/convert/sniff.go` (image `Sniff`/`signatures`) + `internal/convert/dimensions.go` (bounded-peek fail-closed discipline for the MP3 variable-offset case) | role-match (image sniff is same role/flow but fixed-window; dimensions.go is the closer flow-match for MP3's variable-offset bounded peek) |
| `internal/convert/audioopts.go` (NEW) | model/validation | transform (strict parse + allow-list) | `internal/convert/opts.go` (`DocOpts`/`ParseDocOpts`/`checkStrictObject`) and `internal/convert/htmlopts.go` (`HTMLOpts`/map-lookup selection pattern) | exact — RESEARCH.md explicitly specifies "mirror verbatim" |
| `internal/convert/audioduration.go` (NEW) | utility/guard | request-response (subprocess probe + fail-closed) | `internal/convert/dimensions.go` (`Dimensions`, `ErrDimensionsUnknown`, fail-closed philosophy) + `internal/convert/exec.go` (`runCommand`, for the actual subprocess invocation) | role-match — dimensions.go is in-memory parse, audioduration.go is subprocess-based, but the fail-closed error contract is identical |
| `internal/convert/whisper.go` (NEW) | service (Converter impl) | streaming/file-I/O (two-stage subprocess pipeline) | `internal/convert/chromium.go` (`ChromiumConverter.Convert`) — closest for the opts-parse → workDir-scratch-file → `runCommand` → output-validate shape; `internal/convert/libvips.go` for the minimal `Pairs()`/`Engine()` shape | role-match — first TWO-stage pipeline in the codebase; chromium.go is the best single-stage analog to extend |
| `internal/convert/converters.go` (MODIFY: register `AudioConverter{}`) | config | CRUD (registration) | `internal/convert/converters.go` (existing `init()` — already has a `// Future engines` comment anticipating `FFmpegConverter{}`) | exact — same file, one-line addition |
| `internal/convert/audiosniff_test.go` (NEW) | test | transform | `internal/convert/sniff_test.go` and `internal/convert/dimensions_test.go` (bounded/fail-closed adversarial fixture tests) | exact for structure, role-match for MP3-specific adversarial cases |
| `internal/convert/audioopts_test.go` (NEW) | test | transform | `internal/convert/htmlopts_test.go` (`TestHTMLOptsFromMap`, injection test at lines 260-276) | exact — RESEARCH.md explicitly cites this file's injection-test pattern |
| `internal/convert/audioduration_test.go` (NEW) | test | request-response | `internal/convert/dimensions_test.go` | role-match |
| `internal/convert/whisper_test.go` (NEW) | test | streaming/file-I/O | `internal/convert/libreoffice_test.go` (`exec.LookPath` skip-gated live-binary tests, lines 359-360, 451-452) | exact — RESEARCH.md explicitly cites this skip pattern |

## Pattern Assignments

### `internal/convert/convert.go` (MODIFY — add `EngineAudio` const)

**Analog:** same file, `internal/convert/convert.go:18-22`

**Core pattern** (lines 18-22):
```go
const (
	EngineImage    = "image"
	EngineDocument = "document"
	EngineHTML     = "html"
)
```
Add `EngineAudio = "audio"` as a fourth line inside this same const block. Per the doc comment immediately above it (lines 11-17), this is the SINGLE compile-time source of truth for the engine-class string — no other file may hold a raw `"audio"` literal. `AudioConverter.Engine()` (in `whisper.go`) must return this constant, mirroring `LibvipsConverter.Engine()` returning `EngineImage` (`libvips.go:38`).

---

### `internal/convert/audiosniff.go` (NEW, utility/transform)

**Analogs:** `internal/convert/sniff.go` (fixed-window matchers + `Sniff`'s `io.MultiReader` re-stitch shape) and `internal/convert/dimensions.go` (bounded-peek, fail-closed discipline for the MP3 variable-offset case).

**Imports pattern** (`sniff.go` lines 1-6):
```go
package convert

import (
	"bytes"
	"io"
)
```
`audiosniff.go` will need the same two imports at minimum; the MP3 synchsafe decode needs no extra import (plain integer bit-shifts, no `encoding/binary` needed since the RESEARCH.md algorithm hand-rolls the shift).

**Fixed-window matcher pattern** (`sniff.go:52-54`, `matchWebP` — WAV's closest sibling, identical RIFF-container shape):
```go
func matchWebP(b []byte) bool {
	return len(b) >= 12 && bytes.Equal(b[0:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP"))
}
```
Copy this shape verbatim for `matchWAV` (RESEARCH.md already gives the exact target: `bytes.Equal(b[0:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WAVE"))`).

**Brand-table matcher pattern** (`sniff.go:13-23,62-67`, `heicBrands`/`matchHEIC` — M4A's closest sibling, identical ISOBMFF `ftyp`+brand shape):
```go
var heicBrands = map[string]bool{
	"heic": true, "heix": true, "hevc": true, "hevx": true, "mif1": true, "msf1": true,
}

func matchHEIC(b []byte) bool {
	if len(b) < 12 || !bytes.Equal(b[4:8], []byte("ftyp")) {
		return false
	}
	return heicBrands[string(b[8:12])]
}
```
Copy this exact shape for `m4aBrands`/`matchM4A` (RESEARCH.md already supplies the target brand table: `M4A `, `M4B `, `isom`, `mp42`).

**Registry table + `Sniff` re-stitch pattern** (`sniff.go:34-40,78-93`):
```go
var signatures = []signature{
	{"png", matchPNG},
	{"jpg", matchJPEG},
	{"webp", matchWebP},
	{"heic", matchHEIC},
	{"tiff", matchTIFF},
}

func Sniff(r io.Reader) (detected string, rest io.Reader, err error) {
	buf := make([]byte, sniffLen)
	n, readErr := io.ReadFull(r, buf)
	if readErr != nil && readErr != io.ErrUnexpectedEOF && readErr != io.EOF {
		return "", nil, readErr
	}
	buf = buf[:n]
	rest = io.MultiReader(bytes.NewReader(buf), r)

	for _, sig := range signatures {
		if sig.match(buf) {
			return NormalizeFormat(sig.format), rest, nil
		}
	}
	return "", rest, nil
}
```
Per RESEARCH.md's explicit design note (line 154), do NOT add MP3/WAV/OGG/M4A into this same `signatures` table — MP3 needs a different (larger, variable-offset) peek window. Instead, write a **separate** `SniffAudio(r io.Reader) (detected string, rest io.Reader, err error)` in `audiosniff.go` that mirrors this exact `io.ReadFull` + `io.MultiReader` re-stitch shape but uses `mp3PeekLen` (512 KiB, per RESEARCH.md) instead of `sniffLen` (12), and dispatches to `matchWAV`/`matchOGG`/`matchM4A`/`matchMP3` (the latter needing the larger buffer; the former three work fine against the same larger buffer too since they only look at fixed low offsets).

**Fail-closed bounded-peek discipline to copy** (`dimensions.go:12-21,50-68`, the doc-comment reasoning `audiosniff.go`'s MP3 detector must follow verbatim for the ID3v2 synchsafe-size skip):
```go
// dimPeekLen is the bounded prefix size read to locate each format's
// declared-dimension fields. ... Any format whose fields aren't found within
// this window fails closed (ErrDimensionsUnknown) rather than growing the
// buffer or seeking further — D-07's explicit fail-closed guidance.
const dimPeekLen = 64 * 1024
```
Apply the identical "declared value pushes us past the bounded window → reject, never grow/seek" contract to `matchMP3`'s `tagEnd` bounds check (RESEARCH.md already supplies the exact `matchMP3` implementation; this is the *why* to cite in `audiosniff.go`'s doc comments, matching the project's convention of explaining non-obvious "why" decisions inline — see `dimensions.go:12-21`, and CLAUDE.md's "Comments" convention).

**Analog test structure** (`dimensions_test.go` — adversarial/fail-closed fixture pattern; read the file's test names via Grep rather than the whole file, since only the *shape* is needed):
```
TestDimensions_TruncatedIFD_FailsClosed / TestDimensions_OversizedDeclaredOffset_FailsClosed
```
(exact names not re-quoted here to avoid an unnecessary full-file read; the pattern is: one `t.Run`/`func Test...` per adversarial fixture, asserting `errors.Is(err, ErrDimensionsUnknown)` or equivalent, mirrored for `audiosniff_test.go`'s truncated-ID3v2-header / oversized-synchsafe-size / corrupt-ftyp-brand fixtures per RESEARCH.md's `testdata/` list).

---

### `internal/convert/audioopts.go` (NEW, model/validation)

**Analog:** `internal/convert/opts.go` (`DocOpts`, `ParseDocOpts`, `checkStrictObject`) — reuse `checkStrictObject` **unchanged, unduplicated** (D-10 parity, exactly as `htmlopts.go` already does).

**Imports pattern** (`opts.go:1-9`):
```go
package convert

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)
```
`audioopts.go` only needs `bytes`, `encoding/json`, `fmt` (it does not define its own `checkStrictObject`, so no `errors`/`io` needed — those stay in `opts.go`).

**Closed-struct + strict-parse + allow-list pattern** (`opts.go:31-58`):
```go
type DocOpts struct {
	PDFProfile string `json:"pdf_profile,omitempty"`
}

func ParseDocOpts(raw []byte) (DocOpts, error) {
	if err := checkStrictObject(raw); err != nil {
		return DocOpts{}, err
	}
	var o DocOpts
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&o); err != nil {
		return DocOpts{}, fmt.Errorf("parse opts: %w", err)
	}
	if o.PDFProfile != "" && o.PDFProfile != pdfProfileA2b {
		return DocOpts{}, fmt.Errorf("unsupported pdf_profile %q", o.PDFProfile)
	}
	return o, nil
}
```
RESEARCH.md's `AudioOpts`/`ParseAudioOpts` code block (lines 226-264) already follows this shape exactly (allow-list is a `map[string]bool` lookup, matching `htmlopts.go`'s `htmlPageSizeCSS` map-lookup style rather than `opts.go`'s single-constant-equality style, per RESEARCH.md's own note on why). Copy RESEARCH.md's `AudioOpts`/`ParseAudioOpts`/`audioLanguageAllowlist` blocks verbatim.

**FromMap round-trip pattern** (`htmlopts.go:83-97`, `HTMLOptsFromMap`):
```go
func HTMLOptsFromMap(m map[string]any) (HTMLOpts, error) {
	if len(m) == 0 {
		return HTMLOpts{}, nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return HTMLOpts{}, fmt.Errorf("marshal opts: %w", err)
	}
	return ParseHTMLOpts(raw)
}
```
Copy verbatim for `AudioOptsFromMap` (RESEARCH.md line 267 already stubs this as "identical shape").

**Applicability-guard pattern** (`htmlopts.go:99-121`, `ValidateHTMLApplicability`/`isZeroHTMLOpts` — the closer analog vs. `opts.go`'s `ValidateApplicability` because HTMLOpts, like AudioOpts, is its OWN engine-scoped function per RESEARCH.md's design, not merged into the shared `ValidateApplicability`):
```go
func ValidateHTMLApplicability(engine, source, target string, o HTMLOpts) error {
	if isZeroHTMLOpts(o) {
		return nil
	}
	if engine != EngineHTML || NormalizeFormat(target) != "pdf" {
		return fmt.Errorf("html print options are only valid for html -> pdf conversions")
	}
	return nil
}

func isZeroHTMLOpts(o HTMLOpts) bool {
	return o == HTMLOpts{}
}
```
`audioopts.go`'s `ValidateAudioApplicability` (named in RESEARCH.md's file-list, line 333) should follow this exact shape: `isZeroAudioOpts(o) == AudioOpts{}` zero-check, then `engine != EngineAudio` gate.

**Injection-safety pattern to copy into `whisper.go`'s argv construction** — the map-lookup-never-string-concat invariant (`htmlopts.go:132-133`, `buildPrintCSS`):
```go
size := htmlPageSizeCSS[o.PageSize] // o.PageSize already validated against the closed enum
```
`AudioOpts.Language`, once validated against `audioLanguageAllowlist`, is passed as an `exec.Command` argv slice element (never shell-interpolated — `runCommand` never invokes a shell, `exec.go:29`), which is a stronger guarantee than `buildPrintCSS`'s map-lookup-into-a-string need (there is no shell metacharacter surface in argv slice elements at all). Still validate through the allowlist first, per RESEARCH.md's Pitfall 11 discussion.

---

### `internal/convert/audioopts_test.go` (NEW, test)

**Analog:** `internal/convert/htmlopts_test.go` — explicitly cited by RESEARCH.md for the injection-test pattern and by-name for `TestDocOptsFromMap`/`TestHTMLOptsFromMap` round-trip parity.

**FromMap round-trip test** (`htmlopts_test.go:97`, `TestHTMLOptsFromMap` — read only the signature to avoid an unneeded full-file re-read; the body follows the standard table-driven `t.Run` per case, mirroring `ParseHTMLOpts`'s own test table above it).

**Injection test pattern** (`htmlopts_test.go:260-276`):
```go
t.Run("injection cannot reach CSS -- ParseHTMLOpts rejects attacker text first", func(t *testing.T) {
	_, err := ParseHTMLOpts([]byte(`{"page_size":"a4</style><script>alert(1)</script>"}`))
	if err == nil {
		t.Fatal("ParseHTMLOpts accepted an injection payload as page_size, want rejection")
	}
	css := buildPrintCSS(HTMLOpts{PageSize: "a4</style><script>alert(1)</script>"})
	if strings.Contains(css, "<script>") || strings.Contains(css, "alert(1)") {
		t.Errorf("buildPrintCSS leaked raw client bytes into CSS: %q", css)
	}
})
```
Mirror this two-part shape for `audioopts_test.go`'s AUD-03 injection test: (1) assert `ParseAudioOpts([]byte(`{"language":"; rm -rf /"}`))` (or backtick/`$(whoami)` variants, per RESEARCH.md line 270) is rejected by `audioLanguageAllowlist`, and (2) assert that even a hand-constructed `AudioOpts{Language: "; rm -rf /"}` bypassing the parser never produces a non-argv-slice-element code path (i.e. assert the argv-building helper in `whisper.go` only ever appends the raw string as one `args = append(args, "-l", o.Language)` slice element, never through `fmt.Sprintf` into a shell string) — RESEARCH.md lines 270 already specifies exactly this two-part assertion.

---

### `internal/convert/audioduration.go` (NEW, utility/guard)

**Analogs:** `internal/convert/dimensions.go` (fail-closed sentinel-error contract) + `internal/convert/exec.go` (`runCommand`, for the actual `ffprobe` subprocess invocation).

**Fail-closed sentinel-error pattern** (`dimensions.go:23-27`):
```go
// ErrDimensionsUnknown is returned when a registered format's declared
// pixel dimensions could not be located within the bounded peek window —
// treated as a rejection (D-07), not a fallback accept, since this is a
// resource-exhaustion security control.
var ErrDimensionsUnknown = errors.New("cannot determine declared image dimensions")
```
Mirror this exact doc-comment shape and package-level `var Err...` convention (CLAUDE.md's "Errors: package-level `var Err<Reason>`") for `ErrAudioDurationExceeded` — RESEARCH.md line 309 already supplies the exact declaration.

**Hardened subprocess invocation pattern** (`exec.go:28-56`, `runCommand` — called verbatim, not reimplemented):
```go
func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// ... process-group SIGKILL-on-timeout, stdout returned even on non-zero exit
}
```
`ProbeDuration` (RESEARCH.md lines 291-302) calls this exact helper with `runCommand(ctx, "ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", path)` — copy RESEARCH.md's `ProbeDuration` implementation verbatim; it already follows the `fmt.Errorf("<op>: %w", err)` wrapping convention (CLAUDE.md's "Error Handling" section) consistent with `storage.go`/`repo.go`.

---

### `internal/convert/audioduration_test.go` (NEW, test)

**Analog:** `internal/convert/dimensions_test.go` — table-driven adversarial/fail-closed fixture tests. Only the file's existence/shape was consulted (not re-read in full, since `dimensions.go`'s own doc comments already fully specify the fail-closed contract to test against); write one `t.Run`/`func Test...` per: (1) a duration under the ceiling passes, (2) a duration over the ceiling returns `ErrAudioDurationExceeded` (`errors.Is`), (3) an unparseable ffprobe stdout returns a wrapped error, not a silent zero-duration pass.

---

### `internal/convert/whisper.go` (NEW, service/Converter impl)

**Analog:** `internal/convert/chromium.go` (`ChromiumConverter`) for the opts-parse → workDir-scratch-file → `runCommand` → output-validate shape; `internal/convert/libvips.go` for the minimal `Pairs()`/`Engine()` boilerplate.

**Imports pattern** (`chromium.go:1-9`):
```go
package convert

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
)
```
`whisper.go` will additionally need `strings` (for `strings.TrimSuffix`, per RESEARCH.md's `Convert` body) — no `bytes`/`os` needed unless output-file validation is added (see below).

**Minimal `Pairs()`/`Engine()` shape** (`libvips.go:11-38`, the simplest existing Converter — closer boilerplate template than chromium's since libvips also has no opts parsing complexity to imitate for this part):
```go
type LibvipsConverter struct{}

func (LibvipsConverter) Pairs() []Pair {
	pairs := make([]Pair, 0, len(imageFormats)*(len(imageFormats)-1))
	for _, from := range imageFormats {
		for _, to := range imageFormats {
			if from != to {
				pairs = append(pairs, Pair{From: from, To: to})
			}
		}
	}
	return pairs
}

func (LibvipsConverter) Convert(ctx context.Context, inPath, outPath string, _ map[string]any) error {
	if _, err := runCommand(ctx, "vips", "copy", inPath, outPath); err != nil {
		return fmt.Errorf("libvips: %w", err)
	}
	return nil
}

func (LibvipsConverter) Engine() string { return EngineImage }
```
`AudioConverter.Pairs()` should follow the flatter `ChromiumConverter.Pairs()` shape instead (`chromium.go:18-20`, a small explicit slice) since audio's `Pairs()` is `{mp3,wav,m4a,ogg} × {txt,srt,vtt,json}` — likely also better hand-listed or built via nested loops depending on plan-time cardinality; either existing shape (libvips's cross-product loop or chromium's flat literal) is a valid template, chosen by the planner based on final pair-count.

**Two-stage hardened-exec pipeline pattern** (`chromium.go:120-149`, opts-parse-first + workDir-scratch-file-write + `runCommand` shape — the closest existing single-stage-but-scratch-file-using analog to extend into two stages):
```go
func (ChromiumConverter) Convert(ctx context.Context, inPath, outPath string, opts map[string]any) error {
	workDir := filepath.Dir(outPath) // caller's per-job workDir; already unique, already cleaned up

	o, err := HTMLOptsFromMap(opts)
	if err != nil {
		return fmt.Errorf("chromium: %w", err)
	}

	input, err := os.ReadFile(inPath)
	if err != nil {
		return fmt.Errorf("chromium: read input: %w", err)
	}
	// ... build a scratch file inside workDir, never mutate inPath ...
	renderedPath := filepath.Join(workDir, "rendered.html")
	if err := os.WriteFile(renderedPath, rendered, 0o600); err != nil {
		return fmt.Errorf("chromium: write rendered html: %w", err)
	}

	if _, err := runCommand(ctx, "chromium-headless-shell", args...); err != nil {
		return fmt.Errorf("chromium: %w", err)
	}

	if err := validatePDF(outPath); err != nil {
		return fmt.Errorf("chromium: %w", err)
	}
	return nil
}
```
RESEARCH.md's own `Convert` skeleton (lines 360-397) already follows this exact shape, extended to two `runCommand` calls with distinguishable error-message prefixes (`"audio: ffmpeg: %w"` / `"audio: whisper-cli: %w"`) — this stage-prefix discipline directly mirrors `chromium.go`'s single `"chromium: %w"` wrap-per-stage convention, generalized to two stages per RESEARCH.md's own Anti-Patterns note (do not foreclose Phase 31's future stage-aware terminal/transient classifier). Copy RESEARCH.md's `Convert` implementation verbatim as the starting point.

**Output-validation pattern to consider adopting** (`libreoffice.go:174-205`, `validatePDF` — the project's only existing "engine exits 0 but wrote empty/corrupt output" guard):
```go
func validatePDF(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("libreoffice: stat output: %w", err)
	}
	if fi.Size() == 0 {
		return fmt.Errorf("libreoffice: output is empty")
	}
	// ... magic-byte check ...
}
```
RESEARCH.md's `Convert` skeleton explicitly flags this as a **future** step ("future: validate outPath exists + is non-empty, mirrors validatePDF's discipline" — line 396), not required for this phase's minimum, but the planner should note `whisper-cli`'s output formats (txt/srt/vtt/json) have no shared magic-byte the way `%PDF-` does — an `fi.Size() == 0` empty-file check alone (no magic-byte check) is the applicable subset of this pattern if adopted.

**Registration pattern** (`converters.go:1-11`):
```go
func init() {
	Default.Register(LibvipsConverter{})
	Default.Register(LibreOfficeConverter{})
	Default.Register(ChromiumConverter{})
	// Future engines (one line each):
	// Default.Register(FFmpegConverter{})
}
```
Add `Default.Register(AudioConverter{})` as a fourth line (the file's own comment already anticipated this, though under a different placeholder name).

---

### `internal/convert/whisper_test.go` (NEW, test)

**Analog:** `internal/convert/libreoffice_test.go` — `exec.LookPath`-gated skip pattern, explicitly cited by RESEARCH.md.

**Skip-gated live-binary test pattern** (`libreoffice_test.go:450-453`):
```go
func TestLibreOfficeConverter_TimeoutKillsRealProcess(t *testing.T) {
	if _, err := exec.LookPath("soffice"); err != nil {
		t.Skip("soffice not on PATH; run inside the worker test image")
	}
	// ...
}
```
RESEARCH.md's own `whisper_test.go` skeleton (lines 466-478) already follows this exact double-gate shape (`ffmpeg` AND `whisper-cli` both `exec.LookPath`-checked, each with its own skip message) — copy verbatim, including the `verapdf_test.go:359-360` variant for a single-binary gate if a test only needs `ffmpeg` (e.g. a normalize-stage-only unit test).

---

## Shared Patterns

### Hardened process execution
**Source:** `internal/convert/exec.go:28-56` (`runCommand`)
**Apply to:** `audioduration.go` (`ProbeDuration` → `ffprobe`), `whisper.go` (`Convert` → `ffmpeg` then `whisper-cli`)
```go
func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// process-group SIGKILL-on-timeout; stdout returned even on non-zero exit (D-09)
}
```
Never write a new exec wrapper — every external tool invocation in this phase (`ffprobe`, `ffmpeg`, `whisper-cli`) goes through this exact helper, unmodified.

### Closed-struct strict-parse opts
**Source:** `internal/convert/opts.go:60-106` (`checkStrictObject`)
**Apply to:** `audioopts.go` (`ParseAudioOpts`)
```go
func checkStrictObject(raw []byte) error {
	// exactly one top-level JSON object, no duplicate keys, no trailing bytes, no top-level null
}
```
Reused **unchanged, unduplicated** — `audioopts.go` calls this shared helper exactly as `htmlopts.go` already does (D-10 parity), never redefines its own copy.

### Fail-closed bounded-peek / declared-value guard
**Source:** `internal/convert/dimensions.go:12-27` (`dimPeekLen`, `ErrDimensionsUnknown` doc-comment philosophy)
**Apply to:** `audiosniff.go` (MP3 `tagEnd` bound check), `audioduration.go` (`ErrAudioDurationExceeded`)
```go
// Any format whose fields aren't found within this window fails closed
// ... rather than growing the buffer or seeking further — D-07's explicit
// fail-closed guidance.
```
Every new declared-value check in this phase (ID3v2 synchsafe size, ffprobe duration) must reject rather than silently degrade when a bound is exceeded — same security posture, generalized from in-memory parsing to subprocess output.

### Engine-class constant + registry wiring
**Source:** `internal/convert/convert.go:18-22`, `internal/convert/converters.go:1-11`
**Apply to:** `convert.go` (add `EngineAudio`), `converters.go` (add `Default.Register(AudioConverter{})`)
```go
const (
	EngineImage    = "image"
	EngineDocument = "document"
	EngineHTML     = "html"
	// EngineAudio  = "audio"  <- this phase's addition
)
```
This is the SINGLE compile-time source of truth for the engine-class string (per `convert.go`'s own doc comment) — no file outside `convert.go` may hold a raw `"audio"` literal.

### Error wrapping convention
**Source:** CLAUDE.md "Error Handling" + `internal/storage/storage.go`, `internal/convert/chromium.go` (`"chromium: %w"` prefix per stage)
**Apply to:** every new file in this phase
```go
return fmt.Errorf("<action>: %w", err)
```
Stage-distinguishable prefixes (`"audio: ffmpeg: %w"` vs `"audio: whisper-cli: %w"`) matter specifically for `whisper.go`'s two-stage pipeline, per RESEARCH.md's explicit note that this shape must not foreclose Phase 31's future stage-aware retry classifier.

## No Analog Found

None — every file in RESEARCH.md's "Recommended file additions" list has a strong in-repo analog (this is the fourth engine class following an already-3x-proven shape).

## Metadata

**Analog search scope:** `internal/convert/` (all `.go` and `_test.go` files; this phase touches no other package)
**Files scanned:** `convert.go`, `sniff.go`, `dimensions.go`, `opts.go`, `htmlopts.go`, `exec.go`, `libvips.go`, `chromium.go`, `libreoffice.go` (partial: `validatePDF` section), `docsniff.go` (partial), `converters.go`, `htmlopts_test.go` (partial), `libreoffice_test.go` (partial: skip-pattern sections)
**Pattern extraction date:** 2026-07-18
