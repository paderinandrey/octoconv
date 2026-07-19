# Phase 34: AV Engine Foundation - Pattern Map

**Mapped:** 2026-07-19
**Files analyzed:** 8 new + 3 modified = 11
**Analogs found:** 11 / 11

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|--------------------|------|-----------|-----------------|----------------|
| `internal/convert/av.go` | service (Converter impl) | file-I/O (subprocess argv dispatch) | `internal/convert/whisper.go` | exact |
| `internal/convert/av_test.go` | test | request-response (argv-pinning + live-binary) | `internal/convert/whisper_test.go` | exact |
| `internal/convert/avopts.go` | model/validation | CRUD (parse/validate closed opts) | `internal/convert/audioopts.go` | exact |
| `internal/convert/avopts_test.go` | test | CRUD | `internal/convert/audioopts_test.go` | exact |
| `internal/convert/avduration.go` | utility (guard) | file-I/O (subprocess probe) | `internal/convert/audioduration.go` | exact |
| `internal/convert/avduration_test.go` | test | file-I/O | `internal/convert/audioduration_test.go` | exact |
| `internal/convert/avsniff.go` | utility (magic-bytes sniffer) | transform (byte-stream classify) | `internal/convert/audiosniff.go` (EBML walk: `internal/convert/dimensions.go`'s bounded-peek discipline) | exact (fixed-offset part); role-match (EBML walk part) |
| `internal/convert/avsniff_test.go` | test | transform | `internal/convert/audiosniff_test.go` | exact |
| `internal/convert/sniff.go` (MODIFIED) | utility (signature table) | transform | itself (extend `signatures` table + `MIMEType` switch) | exact |
| `internal/convert/convert.go` (MODIFIED) | config/constants | — | itself (extend `Engine*` const block only; NOT `Register`) | exact |
| `internal/convert/converters.go` (NOT touched this phase) | config (wiring) | — | `internal/convert/converters.go` | n/a — explicitly out of scope, see Shared Patterns |

**Scope fence reminder (from RESEARCH.md):** `AVConverter` is built and unit-tested this phase but **NOT** registered into `convert.Default` (no `converters.go` edit, no `Register(AVConverter{})` call) — mirrors Phase 30's own audio-engine fence. Do not let any plan step touch `converters.go`.

## Pattern Assignments

### `internal/convert/av.go` (service, file-I/O)

**Analog:** `internal/convert/whisper.go` (whole file — `AudioConverter`)

**Imports pattern** (whisper.go lines 1-12):
```go
package convert

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)
```
`av.go` needs the same set minus `runtime`/`strings` unless reused for a similar threads-resolution/output-base trick — check before dropping.

**Struct + Pairs() + Engine() pattern** (whisper.go lines 92-137):
```go
type AudioConverter struct {
	modelPath string
}

func (AudioConverter) Pairs() []Pair {
	pairs := make([]Pair, 0, len(audioSourceFormats)*len(audioTargetFormats))
	for _, from := range audioSourceFormats {
		for _, to := range audioTargetFormats {
			pairs = append(pairs, Pair{From: from, To: to})
		}
	}
	return pairs
}

func (AudioConverter) Engine() string { return EngineAudio }
```
`AVConverter` should follow the exact same shape: a plain struct (no injected fields needed unless a test wants to override a binary path — none of RESEARCH.md's plans call for that), `Pairs()` built from explicit source/target slices per RESEARCH.md's Open Question 1 recommendation (all five detected video formats valid for audio-extract/thumbnail sources; mov/avi/mkv/webm→mp4 and mp4→webm for transcode), `Engine()` returning a new `EngineAV` constant.

**Target-driven argv dispatch pattern** (whisper.go lines 139-157, 208-295 — `whisperOutputFlag` + `Convert`):
```go
func whisperOutputFlag(target string) []string {
	switch target {
	case "txt":
		return []string{"-otxt"}
	...
	default:
		return nil
	}
}
```
RESEARCH.md Pattern 1 explicitly maps this shape onto `av.go`'s three argv-builder functions (`transcodeToMP4Args`, `transcodeToWebMArgs`, plus audio-extract/thumbnail builders) dispatched once via `NormalizeFormat(filepath.Ext(outPath))` inside `Convert`, exactly like `whisperOutputFlag` is invoked once near the top of `AudioConverter.Convert` (whisper.go line 249) before any subprocess runs.

**"file:" protocol-prefix argv-builder isolation pattern** (whisper.go lines 159-173, `ffmpegNormalizeArgs`):
```go
func ffmpegNormalizeArgs(inPath, normPath string) []string {
	return []string{"-y", "-i", "file:" + inPath, "-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le", normPath}
}
```
Copy this isolation-for-unit-testability shape verbatim for every new ffmpeg/ffprobe argv builder in `av.go` — RESEARCH.md's own Pattern 1 code (`transcodeToMP4Args`, `transcodeToWebMArgs`) already follows it, additionally prefixing every invocation with `-nostdin` and `-protocol_whitelist file,crypto` (RESEARCH.md lines 161-183), which whisper.go's ffmpeg call does NOT yet have — this is a new-for-Phase-34 hardening addition, not a regression from the analog.

**Fail-fast-before-expensive-work pattern** (whisper.go lines 241-252):
```go
targetFormat := NormalizeFormat(filepath.Ext(outPath))
outFlags := whisperOutputFlag(targetFormat)
if outFlags == nil {
	return fmt.Errorf("audio: unsupported target format %q", targetFormat)
}
```
`av.go`'s `Convert` should validate the target format (and dispatch to one of the three argv builders) before invoking any subprocess, mirroring this exact early-return shape with an `"av: unsupported target format %q"`-style error.

**Convert() orchestration + error-prefix-per-stage pattern** (whisper.go lines 230-295):
```go
o, err := AudioOptsFromMap(opts)
if err != nil {
	return fmt.Errorf("audio: %w", err)
}
...
if _, err := runCommand(ctx, "ffmpeg", ffmpegNormalizeArgs(inPath, normPath)...); err != nil {
	return fmt.Errorf("audio: ffmpeg: %w", err)
}
...
if _, err := runCommand(ctx, "whisper-cli", args...); err != nil {
	return fmt.Errorf("audio: whisper-cli: %w", err)
}
return validateAudioOutput(outPath)
```
`AVConverter.Convert` should: (1) `AVOptsFromMap(opts)` → wrap errors `"av: %w"`; (2) dispatch on target format; (3) run the duration/resolution guard stage (`avduration.go`) BEFORE any ffmpeg encode/decode, per RESEARCH.md's architecture diagram step 2; (4) run `runCommand(ctx, "ffprobe", ...)` for the stream-copy-eligibility check (transcode only, Pattern 2); (5) run `runCommand(ctx, "ffmpeg", argv...)` wrapped `"av: ffmpeg: %w"`; (6) post-validate output (see below). Every subprocess-stage error gets a distinguishable prefix, exactly like whisper.go's `"audio: ffmpeg:"` vs `"audio: whisper-cli:"` split (used by the worker-layer terminal/transient classifier — same convention should hold for `av: ffmpeg:` even though Phase 34 does not wire a worker classifier yet).

**Output post-validation pattern** (whisper.go lines 297-311, `validateAudioOutput`):
```go
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
```
`av.go` needs a variant that treats a missing file (`os.Stat` error) AND a zero-byte file identically as "no output" (RESEARCH.md Pitfall 2 — ffmpeg's `image2` muxer with an out-of-range `-ss` produces **no file at all**, not an empty one, for the thumbnail path specifically). Copy this function's shape but ensure both `os.Stat` error and `Size()==0` map to the same class of error message; thumbnail additionally re-`Sniff()`s the output bytes per the architecture diagram (step 5) — no existing analog does this second check, it's new for AV.

---

### `internal/convert/avopts.go` (model/validation, CRUD)

**Analog:** `internal/convert/audioopts.go` (whole file)

**Closed allowlist + struct pattern** (audioopts.go lines 9-49):
```go
var audioLanguageAllowlist = map[string]bool{
	"auto": true, "en": true, "ru": true, "es": true, "fr": true, "de": true,
}

type AudioOpts struct {
	Language  string `json:"language,omitempty"`
	Translate bool   `json:"translate,omitempty"`
}
```
`AVOpts` needs three closed fields per RESEARCH.md AVO-01/02/03: `Timecode` (thumbnail seek point — needs a numeric range check, not a map-lookup allowlist, so also look at `opts.go`'s enum pattern below), `ResolutionHeight` (closed enum: 480/720/1080 — map-lookup like `audioLanguageAllowlist`), `Codec` (HEVC choice — map-lookup like `audioLanguageAllowlist`, own CRF constant per Pitfall 4, NOT shared with x264's).

**ParseXOpts strict-decode pattern** (audioopts.go lines 51-72):
```go
func ParseAudioOpts(raw []byte) (AudioOpts, error) {
	if err := checkStrictObject(raw); err != nil {
		return AudioOpts{}, err
	}
	var o AudioOpts
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&o); err != nil {
		return AudioOpts{}, fmt.Errorf("parse opts: %w", err)
	}
	if o.Language != "" && !audioLanguageAllowlist[o.Language] {
		return AudioOpts{}, fmt.Errorf("unsupported language %q", o.Language)
	}
	return o, nil
}
```
`ParseAVOpts` copies this shape exactly: `checkStrictObject` (reused verbatim from `opts.go`, do NOT reimplement) → strict `Decode` with `DisallowUnknownFields` → per-field allowlist/range checks appended after decode, one `if`-block per field, each returning a distinct `fmt.Errorf("unsupported ...")`/`fmt.Errorf("... out of range")`.

**XOptsFromMap round-trip pattern** (audioopts.go lines 74-89):
```go
func AudioOptsFromMap(m map[string]any) (AudioOpts, error) {
	if len(m) == 0 {
		return AudioOpts{}, nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return AudioOpts{}, fmt.Errorf("marshal opts: %w", err)
	}
	return ParseAudioOpts(raw)
}
```
Copy verbatim as `AVOptsFromMap` — this is the worker-side read path counterpart to the API write path, D-10 parity requirement repeated for every opts type in this codebase.

**Engine-scoped applicability-validation pattern** (audioopts.go lines 91-113):
```go
func ValidateAudioApplicability(engine, source, target string, o AudioOpts) error {
	if isZeroAudioOpts(o) {
		return nil
	}
	if engine != EngineAudio {
		return fmt.Errorf("audio transcription options are only valid for audio-engine conversions")
	}
	return nil
}

func isZeroAudioOpts(o AudioOpts) bool {
	return o == AudioOpts{}
}
```
`ValidateAVApplicability` + `isZeroAVOpts` follow this exact shape, scoped to `EngineAV`. Note: `opts.go`'s `ValidateApplicability` (DocOpts) additionally restricts by TARGET format (`NormalizeFormat(target) != "pdf"`), not just engine — `AVOpts.Timecode` should probably be restricted to thumbnail targets only (jpg/png/webp), and `ResolutionHeight`/`Codec` to transcode targets only (mp4/webm); use `opts.go` lines 130-138's target-aware shape as the secondary reference for this per-field applicability nuance, not just audioopts.go's engine-only shape.

**Shared strict-parsing helper (reuse verbatim, do not reimplement):** `checkStrictObject` — `internal/convert/opts.go` lines 68-106. See Shared Patterns below.

---

### `internal/convert/avduration.go` (utility/guard, file-I/O)

**Analog:** `internal/convert/audioduration.go` (whole file — `ProbeDuration`/`EnforceMaxDuration` REUSED verbatim per RESEARCH.md's "Don't Hand-Roll" table; only the resolution guard is NEW)

**Sentinel error + server-constant pattern** (audioduration.go lines 13-31):
```go
var ErrAudioDurationExceeded = errors.New("declared audio duration exceeds configured maximum")

const maxSaneDurationSeconds = 1 << 31
```
`avduration.go` needs `ErrAVDurationExceeded` (or the planner's single shared `ErrAVResourceExceeded` per RESEARCH.md line 51) and, new for this phase, an analogous plausibility ceiling for resolution (e.g. a `maxSaneDimension` server constant) — same "validate in a bounded numeric space before it can influence control flow" discipline as `maxSaneDurationSeconds`'s float-space pre-check (documented amd64/arm64 overflow footgun in lines 21-30 — do not skip this class of check for the new resolution probe's width/height ints, even though ints don't have the exact same float-conversion hazard; still guard `width <= 0 || height <= 0` etc.).

**Argv-builder isolation + probe/parse split pattern** (audioduration.go lines 33-79):
```go
func ffprobeDurationArgs(path string) []string {
	return []string{"-v", "error", "-show_entries",
		"format=duration", "-of", "default=noprint_wrappers=1:nokey=1", "file:" + path}
}

func ProbeDuration(ctx context.Context, path string) (time.Duration, error) {
	out, err := runCommand(ctx, "ffprobe", ffprobeDurationArgs(path)...)
	if err != nil {
		return 0, fmt.Errorf("ffprobe: %w", err)
	}
	return parseProbedDuration(string(out))
}
```
This function is called **unmodified** by `av.go` (import, don't copy) — RESEARCH.md's "Don't Hand-Roll" table is explicit that `ProbeDuration`/`EnforceMaxDuration` are reused verbatim, not re-derived. The NEW `probeVideoStream`/`EnforceMaxResolution` pair in `avduration.go` should mirror this exact `<verb>Args` + `runCommand` + parse-into-typed-struct split for independent unit-testability — RESEARCH.md's Code Examples section (lines 551-583) already gives the concrete target shape:
```go
type avStreamProbe struct {
	Streams []struct {
		CodecName string `json:"codec_name"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
	} `json:"streams"`
}

func ffprobeStreamArgs(path string) []string {
	return []string{"-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=codec_name,width,height",
		"-of", "json", "file:" + path}
}

func probeVideoStream(ctx context.Context, path string) (codec string, width, height int, err error) {
	out, err := runCommand(ctx, "ffprobe", ffprobeStreamArgs(path)...)
	if err != nil {
		return "", 0, 0, fmt.Errorf("ffprobe: %w", err)
	}
	var probe avStreamProbe
	if err := json.Unmarshal(out, &probe); err != nil || len(probe.Streams) == 0 {
		return "", 0, 0, fmt.Errorf("ffprobe: no video stream found or unparseable output")
	}
	s := probe.Streams[0]
	if s.Width <= 0 || s.Height <= 0 {
		return "", 0, 0, fmt.Errorf("ffprobe: implausible resolution %dx%d", s.Width, s.Height)
	}
	return s.CodecName, s.Width, s.Height, nil
}
```
Use directly as the starting implementation — it is already live-verified argv/JSON shape from RESEARCH.md, not just a pattern description.

**Fail-closed guard-function pattern** (audioduration.go lines 81-106, `EnforceMaxDuration`):
```go
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
```
`EnforceMaxResolution(ctx, path, maxHeight int) error` should follow this identical shape: probe → compare against a plain-parameter ceiling (NOT read from env inside `internal/convert`, per RESEARCH.md Open Question 2's recommendation to mirror this exact "plain parameter, env-wiring deferred to a later phase" precedent) → wrap the ceiling-exceeded case with `%w` against the new sentinel error.

**Reuse note:** `runCommand` itself (`exec.go`) is reused unmodified by both the existing `ProbeDuration` and the new `probeVideoStream`/thumbnail/transcode calls — no new exec-hardening mechanism needed anywhere in Phase 34 (RESEARCH.md "Don't Hand-Roll" row 1).

---

### `internal/convert/avsniff.go` (utility/sniffer, transform)

**Analog A (fixed-offset matchers mp4/mov/avi):** `internal/convert/audiosniff.go` lines 29-50 (`matchWAV`, `matchM4A`) and `internal/convert/sniff.go` lines 46-71 (`matchHEIC`, `matchWebP`)

```go
// matchWAV mirrors matchWebP's exact RIFF-container shape (sniff.go)
func matchWAV(b []byte) bool {
	return len(b) >= 12 && bytes.Equal(b[0:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WAVE"))
}

func matchHEIC(b []byte) bool {
	if len(b) < 12 || !bytes.Equal(b[4:8], []byte("ftyp")) {
		return false
	}
	return heicBrands[string(b[8:12])]
}
```
RESEARCH.md Pattern 4 already gives the exact target implementation, directly copying this ftyp+brand / RIFF+fourCC shape:
```go
var mp4VideoBrands = map[string]bool{
	"isom": true, "mp41": true, "mp42": true, "mp4v": true, "avc1": true,
	"iso2": true, "iso3": true, "iso4": true, "iso5": true,
	"iso6": true, "iso7": true, "iso8": true, "iso9": true,
	"3gp4": true, "3gp5": true, "3g2a": true, "dash": true,
}

func matchMP4(b []byte) bool {
	if len(b) < 12 || !bytes.Equal(b[4:8], []byte("ftyp")) {
		return false
	}
	return mp4VideoBrands[string(b[8:12])]
}

func matchMOV(b []byte) bool {
	if len(b) < 12 || !bytes.Equal(b[4:8], []byte("ftyp")) {
		return false
	}
	return string(b[8:12]) == "qt  "
}

func matchAVI(b []byte) bool {
	return len(b) >= 12 && bytes.Equal(b[0:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("AVI "))
}
```
These three go into `sniff.go`'s existing `signatures` table (see modified-file section below) — do NOT put them in `avsniff.go`'s own separate dispatch function; unlike EBML, they fit the existing 12-byte-window mechanism exactly.

**Analog B (bounded-peek, declared-length, fail-closed parser):** `internal/convert/audiosniff.go` lines 52-120 (`matchMP3`, `SniffAudio`) is the closest PRECEDENT for "variable-offset, declared-length format that cannot use the fixed 12-byte window," but the EBML DocType walker is a genuinely new algorithm (no existing file walks a chain of TLV elements) — `internal/convert/dimensions.go`'s `dimPeekLen` bounded-buffer discipline (`dimPeekLen = 64 * 1024`, fails closed with `ErrDimensionsUnknown` rather than growing/seeking past the bound) is the secondary analog for the "never trust a declared size past what you actually have" discipline embedded in `readSizeVint`/`matchEBML`.

```go
// matchMP3 — mirrors dimensions.go's ErrDimensionsUnknown philosophy for a
// declared value that can't be verified within the bound (D-07): never grow
// the buffer or seek further, just reject.
func matchMP3(b []byte) bool {
	if len(b) >= 3 && bytes.Equal(b[:3], []byte("ID3")) {
		if len(b) < 10 {
			return false // truncated ID3v2 fixed header: fail closed
		}
		size := int(b[6])<<21 | int(b[7])<<14 | int(b[8])<<7 | int(b[9])
		tagEnd := 10 + size
		if b[5]&0x10 != 0 {
			tagEnd += 10
		}
		if tagEnd < 0 || tagEnd+1 >= len(b) {
			return false // beyond bounded peek window: fail closed, do not grow/seek
		}
		return b[tagEnd] == 0xFF && (b[tagEnd+1]&0xE0) == 0xE0
	}
	return len(b) >= 2 && b[0] == 0xFF && (b[1]&0xE0) == 0xE0
}
```
Use verbatim as RESEARCH.md's own byte-exact target implementation for `matchEBML`/`vintLen`/`readSizeVint`/`readIDVint` (RESEARCH.md Pattern 3, lines 251-350) — it already follows this exact "fail closed the moment a declared length or offset would exceed the bounded buffer" convention; do not deviate from it.

**SniffAudio dispatcher pattern** (audiosniff.go lines 89-120) → target for `SniffVideo`:
```go
func SniffAudio(r io.Reader) (detected string, rest io.Reader, err error) {
	buf := make([]byte, mp3PeekLen)
	n, readErr := io.ReadFull(r, buf)
	if readErr != nil && readErr != io.ErrUnexpectedEOF && readErr != io.EOF {
		return "", nil, readErr
	}
	buf = buf[:n]
	rest = io.MultiReader(bytes.NewReader(buf), r)

	switch {
	case matchWAV(buf):
		return NormalizeFormat("wav"), rest, nil
	case matchOGG(buf):
		return NormalizeFormat("ogg"), rest, nil
	case matchM4A(buf):
		return NormalizeFormat("m4a"), rest, nil
	case matchMP3(buf):
		return NormalizeFormat("mp3"), rest, nil
	default:
		return "", rest, nil
	}
}
```
`SniffVideo(r io.Reader) (detected string, rest io.Reader, err error)` in `avsniff.go` copies this exact shape but ONLY needs to handle `mkv`/`webm` via `matchEBML` (a bounded `avPeekLen = 4*1024` window per RESEARCH.md) — mp4/mov/avi are NOT re-checked here, they are handled by extending `sniff.go`'s own `Sniff()`/`signatures` table (fixed 12-byte window, see below) since they fit that mechanism exactly. `SniffVideo` is a second, narrower peek-and-match function analogous to `SniffAudio`, not a full replacement for `Sniff`.

**Disjointness test requirement (RESEARCH.md Pattern 4, hard requirement):** a test in `avsniff_test.go` MUST enumerate `mp4VideoBrands`, `m4aBrands` (audiosniff.go), and `heicBrands` (sniff.go) and assert pairwise-empty intersection — mirrors the existing precedent `TestMatchM4A_ForeignBrandNotDetected` (`audiosniff_test.go:63`), which already proves `isom`/`mp41`/`mp42` are excluded from `m4aBrands` today; the new test makes that guarantee explicit and permanent instead of an implicit accident of two independently-written tables.

---

### `internal/convert/sniff.go` (MODIFIED — utility, transform)

**Analog:** itself (extend, don't restructure)

**Signatures table extension pattern** (sniff.go lines 35-44):
```go
var signatures = []signature{
	{"png", matchPNG},
	{"jpg", matchJPEG},
	{"webp", matchWebP},
	{"heic", matchHEIC},
	{"tiff", matchTIFF},
}
```
Append `{"mp4", matchMP4}`, `{"mov", matchMOV}`, `{"avi", matchAVI}` — `matchMP4`/`matchMOV`/`matchAVI` are defined in the new `avsniff.go` (package-level, same `convert` package, no import needed), the table itself lives in `sniff.go` per RESEARCH.md's Recommended Project Structure (line 144: "sniff.go: MODIFIED: signatures table += {...}").

**MIMEType switch extension pattern** (sniff.go lines 108-155):
```go
func MIMEType(format string) string {
	switch NormalizeFormat(format) {
	case "png":
		return "image/png"
	...
	case "json":
		return "application/json"
	default:
		return "application/octet-stream"
	}
}
```
Add `case "mp4": return "video/mp4"`, `case "mov": return "video/quicktime"`, `case "avi": return "video/x-msvideo"`, `case "mkv": return "video/x-matroska"`, `case "webm": return "video/webm"` — same one-case-per-format shape, append after the existing audio/transcription cases, before `default`.

---

### `internal/convert/convert.go` (MODIFIED — config/constants)

**Analog:** itself (extend the const block only)

**Engine-class constant pattern** (convert.go lines 19-24):
```go
const (
	EngineImage    = "image"
	EngineDocument = "document"
	EngineHTML     = "html"
	EngineAudio    = "audio"
)
```
Add `EngineAV = "av"` to this block — this is the SINGLE compile-time source of truth for the engine-class string value (per the existing doc comment on lines 11-18); `AVConverter.Engine()` returns this constant. **Do NOT** touch `Register`/`Default` in this file, and do NOT edit `converters.go` — registration is explicitly Phase 35 scope (RESEARCH.md Recommended Project Structure, line 145: "registration itself... stays OUT per the scope fence").

---

## Shared Patterns

### Hardened subprocess execution
**Source:** `internal/convert/exec.go` (whole file, `runCommand`)
**Apply to:** Every ffmpeg/ffprobe invocation in `av.go` and `avduration.go` — reused verbatim, zero AV-specific changes needed. `Setpgid`+SIGKILL-on-timeout, stdout/stderr capture, exit-code-vs-output semantics (D-09) all already handle ffmpeg/ffprobe identically to whisper-cli.
```go
func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	...
}
```

### Strict JSON opts parsing
**Source:** `internal/convert/opts.go` lines 68-106 (`checkStrictObject`)
**Apply to:** `avopts.go`'s `ParseAVOpts` — call `checkStrictObject(raw)` first, exactly like `ParseDocOpts`/`ParseAudioOpts` do, before the `json.NewDecoder(...).DisallowUnknownFields()` decode. Do not reimplement duplicate-key/trailing-data/top-level-null rejection.

### Declared-duration probing/guarding
**Source:** `internal/convert/audioduration.go` lines 49-106 (`ProbeDuration`, `EnforceMaxDuration`)
**Apply to:** `av.go`'s guard stage — call these two functions UNMODIFIED (they already work against any ffprobe-readable container, video included, per RESEARCH.md's live-verification against `src.mp4`). Only the NEW resolution guard (`probeVideoStream`/`EnforceMaxResolution`) needs to be written, in `avduration.go`, following the same split-for-testability shape.

### Cgroup-aware thread sizing (mechanism only; wiring is out of scope)
**Source:** `internal/convert/cgroup.go` (whole file, `CgroupCPULimit`) + `internal/convert/whisper.go` lines 32-60 (`audioThreads`/`SetAudioThreads`/`audioThreadCount`)
**Apply to:** `av.go`'s `-threads` argv flag needs the identical single-write-before-concurrent-reads package-level-var pattern (`avThreads`/`SetAVThreads`/`avThreadCount`) if a plan chooses to wire it this phase — RESEARCH.md flags the actual env-var wiring as Phase 36 scope, but the RESOLUTION mechanism (package var + setter + 2-tier fallback to `runtime.NumCPU()`) is fully reusable now if the planner decides `-threads` needs a real value rather than a hardcoded constant in Phase 34's argv builders.

### Fixed-offset ISOBMFF/RIFF magic-bytes matching
**Source:** `internal/convert/sniff.go` lines 46-71 (`matchHEIC`, `matchWebP`), `internal/convert/audiosniff.go` lines 29-50 (`matchWAV`, `matchM4A`)
**Apply to:** `avsniff.go`'s `matchMP4`/`matchMOV`/`matchAVI` — same `len(b) >= 12` + fixed-offset `bytes.Equal` shape, no new mechanism.

### Bounded-peek, fail-closed declared-length parsing
**Source:** `internal/convert/audiosniff.go` lines 52-87 (`matchMP3`), `internal/convert/dimensions.go` (`dimPeekLen` discipline, `ErrDimensionsUnknown`)
**Apply to:** `avsniff.go`'s `matchEBML`/`vintLen`/`readSizeVint`/`readIDVint` — every declared-size/offset check must fail closed (return `false`/an error) the instant it would read past the bounded peek buffer; never grow the buffer, never seek further into the stream.

### Package-level doc comment + exported-identifier comment density
**Source:** `internal/convert/audioduration.go` line 1 (package-role framing on `cgroup.go`, not repeated per-file), `internal/convert/whisper.go` lines 92-97 (`AudioConverter` doc comment starting with the identifier name)
**Apply to:** Every new AV file — CLAUDE.md requires exactly one package-level doc comment across the package (already present on `internal/convert/convert.go` line 1 and `internal/convert/cgroup.go` line 1) — do NOT add a second package doc comment to any new `av*.go` file. Every exported type/function in the new files needs a doc comment starting with its own name, at the same density as `whisper.go`/`audioduration.go`/`dimensions.go` (CLAUDE.md Comments convention, explicitly called out in RESEARCH.md line 54 as a bar these files must hold future engine-class implementers to).

## No Analog Found

None — every file in this phase has a strong, directly-precedented analog. The single genuinely novel algorithm (EBML/DocType bounded-peek walker in `avsniff.go`) has no prior Go implementation in this codebase to copy structurally, but RESEARCH.md Pattern 3 supplies a complete, byte-exact, live-verified reference implementation (not just a pattern description) — treat that code block as the target implementation directly, cross-checked against `matchMP3`'s fail-closed bounded-peek discipline for style/error-handling conventions only.

## Test File Patterns

**Analog:** `internal/convert/whisper_test.go` lines 1-42 (`requireLiveAudioBinaries`), `internal/convert/audiosniff_test.go` (table-driven matcher tests + fixture-based `SniffAudio` tests)

**Live-binary skip-gate pattern** (whisper_test.go lines 26-42):
```go
func requireLiveAudioBinaries(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH; see Plan 01's \"Local Development Setup\"")
	}
	if _, err := exec.LookPath("whisper-cli"); err != nil {
		t.Skip("whisper-cli not on PATH; see Plan 01's \"Local Development Setup\"")
	}
	...
}
```
`av_test.go` needs an analogous `requireLiveAVBinaries(t *testing.T)` that skip-gates on `exec.LookPath("ffmpeg")` and `exec.LookPath("ffprobe")` — and per RESEARCH.md Pitfall 3, a SEPARATE, narrower skip-gate for the webp thumbnail test specifically (skip only that one test if `ffmpeg -encoders` output lacks `libwebp`, don't skip the whole file).

**Argv-pinning unit test pattern** (implied by whisper_test.go's `TestWhisperArgs`, `TestFfmpegNormalizeArgs_FilePrefix` — pure function tests, no subprocess): every new argv-builder function (`transcodeToMP4Args`, `transcodeToWebMArgs`, thumbnail/extract builders, `ffprobeStreamArgs`) needs a pure-function test asserting the exact returned `[]string` slice, run unconditionally (no skip-gate) since these are pure functions with no subprocess dependency.

**Table-driven matcher test pattern** (audiosniff_test.go `TestMatchWAV`, `TestMatchM4A`, `TestMatchM4A_ForeignBrandNotDetected`): every new `matchX` function in `avsniff.go` gets its own `TestMatchX`/`TestMatchX_Rejects...` pair, plus the disjointness test described above.

## Metadata

**Analog search scope:** `internal/convert/` (all 39 files); no other directory searched — RESEARCH.md and CLAUDE.md both confirm Phase 34's file set is entirely scoped to this one package (no `internal/api`, `internal/worker`, or `cmd/*` changes this phase).
**Files scanned:** `whisper.go`/`whisper_test.go`, `audiosniff.go`/`audiosniff_test.go`, `audioopts.go`, `audioduration.go`, `sniff.go`, `exec.go`, `convert.go`, `opts.go`, `cgroup.go`, `dimensions.go` (partial, for `dimPeekLen` precedent).
**Pattern extraction date:** 2026-07-19
