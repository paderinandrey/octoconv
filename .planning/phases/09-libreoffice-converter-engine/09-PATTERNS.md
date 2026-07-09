# Phase 9: LibreOffice Converter Engine - Pattern Map

**Mapped:** 2026-07-09
**Files analyzed:** 4 (1 new implementation, 1 new test, 2 modified)
**Analogs found:** 4 / 4

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|-----------------|----------------|
| `internal/convert/libreoffice.go` | service (Converter engine impl) | transform (file-I/O, subprocess) | `internal/convert/libvips.go` | exact (same interface, same package, only existing `Converter` impl) |
| `internal/convert/libreoffice_test.go` | test | file-I/O + subprocess (gated live test) | `internal/convert/convert_test.go` (`TestRunCommandTimeout`) | exact (same package, same `exec.LookPath`-gated pattern, exact scenario this phase's D-03 needs) |
| `internal/convert/converters.go` | config (registry wiring) | n/a | `internal/convert/converters.go` (self, already has commented placeholder) | exact — one-line uncomment/add |
| `Dockerfile.worker` | config (container provisioning) | n/a | `Dockerfile.worker` (self, already has `libvips-tools` install block) | exact — additive to existing `apt-get install` line |

Secondary/supporting analogs (not a full file to create, but pattern donors for logic inside `libreoffice.go`):

| Concern | Donor file | Why |
|---|---|---|
| Magic-byte output validation (D-02) | `internal/convert/sniff.go` (`matchPNG`, `matchWebP`, `Sniff`) | Established project idiom for "peek N bytes, compare against a signature" — RESEARCH.md Pattern 3 (`validatePDF`) directly mirrors this shape |
| Hardened subprocess exec (reused unchanged) | `internal/convert/exec.go` (`runCommand`) | `LibreOfficeConverter.Convert` calls this exactly as `LibvipsConverter.Convert` does — zero changes needed |
| Per-job workDir / outPath derivation (caller side, for context only — not a file this phase touches) | `internal/worker/worker.go:270-284` (`process`) | Confirms `filepath.Dir(outPath)` is already a unique, already-cleaned-up per-job directory `Convert()` can safely derive a profile dir from |

## Pattern Assignments

### `internal/convert/libreoffice.go` (service, transform) — NEW

**Analog:** `internal/convert/libvips.go` (the only existing `Converter` implementation; same package, same interface, same file-naming convention — one file per converter implementation).

**Imports pattern** — `libvips.go` lines 1-6:
```go
package convert

import (
	"context"
	"fmt"
)
```
`libreoffice.go` will need a larger but still stdlib-only import set (per RESEARCH.md's "Package Legitimacy Audit" — no new Go module deps): `bytes`, `context`, `fmt`, `io`, `os`, `path/filepath`, `strings`. No aliasing — full imports only, matching this project's "no import organization/aliasing" convention (CLAUDE.md Import Organization section).

**Struct + interface shape** — `libvips.go` lines 8-13:
```go
// imageFormats are the raster formats libvips converts between in this slice.
var imageFormats = []string{"png", "jpg", "webp", "heic", "tiff"}

// LibvipsConverter converts raster images by shelling out to the `vips` CLI.
// The output format is selected by outPath's extension (e.g. out.webp).
type LibvipsConverter struct{}
```
Copy this exact shape: `type LibreOfficeConverter struct{}` (stateless, empty struct, value receiver — RESEARCH.md's locked convention), with a doc comment starting with the identifier name (CLAUDE.md Comments convention).

**`Pairs()` pattern** — `libvips.go` lines 15-26:
```go
// Pairs returns every ordered pair of supported image formats (from != to).
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
```
`LibreOfficeConverter.Pairs()` is simpler (fixed `{format, "pdf"}` pairs for the 6 document formats, no all-pairs cross product needed) — return a literal slice of 6 `Pair{From: f, To: "pdf"}` built from a `documentFormats = []string{"docx", "odt", "xlsx", "ods", "pptx", "odp"}` var, mirroring the `imageFormats` var-then-loop idiom but simplified since target is always `"pdf"`.

**`Convert()` core pattern** — `libvips.go` lines 28-35:
```go
// Convert runs `vips copy <in> <out>`; libvips infers both codecs from the file
// extensions. ctx must carry the engine timeout.
func (LibvipsConverter) Convert(ctx context.Context, inPath, outPath string, _ map[string]any) error {
	if err := runCommand(ctx, "vips", "copy", inPath, outPath); err != nil {
		return fmt.Errorf("libvips: %w", err)
	}
	return nil
}
```
Note the exact signature shape: `Convert(ctx context.Context, inPath, outPath string, _ map[string]any) error` — `opts` is `map[string]any` (RESEARCH.md Pitfall 5: NOT `map[string]string`), unused param named `_`. Error wrapping uses the engine-name prefix (`"libvips: %w"`) — `libreoffice.go` must use `"libreoffice: %w"` at every wrap point, per CLAUDE.md's `fmt.Errorf("<action>: %w", err)` convention.

RESEARCH.md's Pattern 1 (already live-verified against a real Docker build, 2026-07-09) is the concrete implementation to copy almost verbatim — it already follows every convention above:
```go
func (LibreOfficeConverter) Convert(ctx context.Context, inPath, outPath string, _ map[string]any) error {
	workDir := filepath.Dir(outPath) // caller's per-job workDir; already unique, already cleaned up
	profileDir := filepath.Join(workDir, "lo-profile")
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		return fmt.Errorf("libreoffice: mkdir profile: %w", err)
	}

	filter, err := filterFor(filepath.Ext(inPath))
	if err != nil {
		return fmt.Errorf("libreoffice: %w", err)
	}

	args := []string{
		"--headless", "--invisible", "--nocrashreport", "--nodefault",
		"--nologo", "--nofirststartwizard", "--norestore",
		"-env:UserInstallation=file://" + profileDir,
		"--convert-to", "pdf:" + filter,
		"--outdir", workDir,
		inPath,
	}
	if err := runCommand(ctx, "soffice", args...); err != nil {
		return fmt.Errorf("libreoffice: %w", err)
	}

	producedPath := filepath.Join(workDir, strings.TrimSuffix(filepath.Base(inPath), filepath.Ext(inPath))+".pdf")
	if err := os.Rename(producedPath, outPath); err != nil {
		return fmt.Errorf("libreoffice: rename output: %w", err)
	}

	return validatePDF(outPath)
}
```
(Source: 09-RESEARCH.md "Pattern 1: Self-Contained Profile Isolation", live-verified 2026-07-09.)

**Hardened subprocess exec (reused unchanged)** — `internal/convert/exec.go` lines 1-46:
```go
func runCommand(ctx context.Context, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", name, err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-ctx.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done // reap
		return fmt.Errorf("%s killed: %w", name, ctx.Err())
	case err := <-done:
		if err != nil {
			return fmt.Errorf("%s failed: %w: %s", name, err, stderr.String())
		}
		return nil
	}
}
```
Call this exactly as `libvips.go` does — `runCommand(ctx, "soffice", args...)`. Do NOT modify `exec.go`; RESEARCH.md empirically confirmed (2026-07-09, live Docker probe) that `oosplash`'s fork of `soffice.bin` inherits the same process group, so the existing `Setpgid`+group-`SIGKILL` mechanism already works unchanged.

**Output validation pattern (D-02)** — mirrors `internal/convert/sniff.go` lines 42-53 (`matchPNG`, `matchWebP` — peek fixed-length magic bytes, compare with `bytes.Equal`):
```go
func matchPNG(b []byte) bool {
	sig := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	return len(b) >= len(sig) && bytes.Equal(b[:len(sig)], sig)
}
```
RESEARCH.md's Pattern 3 (`validatePDF`) directly extends this idiom to check both non-zero size and a `%PDF-` prefix:
```go
var pdfMagic = []byte("%PDF-")

func validatePDF(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("libreoffice: stat output: %w", err)
	}
	if fi.Size() == 0 {
		return fmt.Errorf("libreoffice: output is empty")
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("libreoffice: open output: %w", err)
	}
	defer f.Close()
	buf := make([]byte, len(pdfMagic))
	if _, err := io.ReadFull(f, buf); err != nil {
		return fmt.Errorf("libreoffice: read output header: %w", err)
	}
	if !bytes.Equal(buf, pdfMagic) {
		return fmt.Errorf("libreoffice: output missing %%PDF- magic bytes")
	}
	return nil
}
```
Per CONTEXT.md D-02, this validation lives **inside** `LibreOfficeConverter.Convert` (called as the final step, as shown in the Convert() excerpt above) — not as a generic hook in `worker.go`.

**Filter dispatch pattern (no direct codebase analog — new, explicit switch)**:
```go
func filterFor(sourceExt string) (string, error) {
	switch NormalizeFormat(sourceExt) {
	case "docx", "odt":
		return "writer_pdf_Export", nil
	case "xlsx", "ods":
		return "calc_pdf_Export", nil
	case "pptx", "odp":
		return "impress_pdf_Export", nil
	default:
		return "", fmt.Errorf("no pdf export filter for %q", sourceExt)
	}
}
```
Use the project's existing `NormalizeFormat` (`internal/convert/convert.go:29-39`) for the lookup key, not a local re-implementation — this is the same normalization `Registry.Register`/`Lookup` already apply, keeping format-string handling in one place.

---

### `internal/convert/libreoffice_test.go` (test) — NEW

**Analog:** `internal/convert/convert_test.go` — same package (`package convert`, not `convert_test`), same file, contains the existing gated-live-process pattern this phase's D-03 test must follow.

**Binary-presence-gated live test pattern** — `convert_test.go` lines 46-68 (`TestRunCommandTimeout`):
```go
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
```
This is the exact skeleton for D-03's `soffice`-specific proof test — swap `exec.LookPath("sleep")` for `exec.LookPath("soffice")`, and instead of asserting on `runCommand`'s return value alone, additionally shell out to `ps` and assert zero surviving `soffice`/`oosplash`/`soffice.bin` processes (RESEARCH.md's fully worked `TestLibreOfficeConverter_TimeoutKillsRealProcess` example, reproduced below, already follows this exact skip-guard + timeout-assertion shape):
```go
func TestLibreOfficeConverter_TimeoutKillsRealProcess(t *testing.T) {
	if _, err := exec.LookPath("soffice"); err != nil {
		t.Skip("soffice not on PATH; skipping (run inside the worker Docker image or CI)")
	}
	dir := t.TempDir()
	inPath := dir + "/in.txt"
	var buf bytes.Buffer
	for i := 0; i < 80_000; i++ {
		buf.WriteString("the quick brown fox jumps over the lazy dog\n")
	}
	if err := os.WriteFile(inPath, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	outPath := dir + "/out.pdf"
	_ = LibreOfficeConverter{}.Convert(ctx, inPath, outPath, nil) // expected to time out

	out, err := exec.Command("ps", "-eo", "pid,comm").CombinedOutput()
	if err != nil {
		t.Fatalf("ps: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "soffice") || strings.Contains(line, "oosplash") {
			t.Fatalf("surviving LibreOffice process after timeout: %s", line)
		}
	}
}
```
(Source: 09-RESEARCH.md "D-03 Live Integration Test", methodology empirically verified live 2026-07-09 against a real `debian:bookworm-slim` + `4:7.4.7-1+deb12u13` build.)

**Registry-assertion pattern** for `Pairs()`/format-support unit tests — `convert_test.go` lines 26-44 (`TestRegistryLibvipsPairs`):
```go
func TestRegistryLibvipsPairs(t *testing.T) {
	if !Default.Supports("png", "webp") {
		t.Error("expected png->webp to be supported")
	}
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
```
Mirror this for a `TestRegistryLibreOfficePairs` (or fold document-format assertions into a table alongside the image ones) once `converters.go` registers `LibreOfficeConverter{}` — asserts e.g. `Default.Supports("docx", "pdf")`, `!Default.Supports("pdf", "docx")` (one-directional only), and non-document pairs remain unsupported.

**Filter-selection and validation unit tests (no live process needed)** — plain table-driven tests for `filterFor()` and `validatePDF()`, following the `TestNormalizeFormat` table-driven style — `convert_test.go` lines 11-24:
```go
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
```
Use the same `map[string]string` cases-table shape for `filterFor`, and a small set of `os.WriteFile`-then-`validatePDF`-then-assert-error/nil cases for `validatePDF` (empty file → error, valid `%PDF-` prefix → nil, wrong magic bytes → error) — no `soffice`/Docker gating needed for these, they're pure-Go unit tests.

---

### `internal/convert/converters.go` (config, registry wiring) — MODIFIED

**Analog:** itself — `converters.go` already has a placeholder comment showing exactly what to do.

**Current state** (lines 1-10):
```go
package convert

// init wires concrete converters into the Default registry. To add support for
// a new engine or format pair, register it here with a single line.
func init() {
	Default.Register(LibvipsConverter{})
	// Future engines (one line each):
	// Default.Register(LibreOfficeConverter{})
	// Default.Register(FFmpegConverter{})
}
```
**Required change:** uncomment/promote the `LibreOfficeConverter{}` line:
```go
func init() {
	Default.Register(LibvipsConverter{})
	Default.Register(LibreOfficeConverter{})
	// Future engines (one line each):
	// Default.Register(FFmpegConverter{})
}
```
Note per CONTEXT.md's phase boundary: registering here makes the converter *reachable via `convert.Default.Lookup`/`Supports`* (needed for this phase's own registry unit tests), but does NOT make documents reachable end-to-end through the live queue/API — that routing is explicitly Phase 10/11 scope (no `document` queue, no `cmd/document-worker` wiring, no `handleCreateJob` routing changes in this phase).

---

### `Dockerfile.worker` (config, container provisioning) — MODIFIED

**Analog:** itself — the existing `libvips-tools` install block is the direct structural template for the additive LibreOffice install.

**Current state** (full file, 17 lines):
```dockerfile
# Build stage
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/worker ./cmd/worker

# Runtime stage: image engine needs the libvips CLI.
FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates libvips-tools \
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/worker /usr/local/bin/worker
# Run unprivileged: the worker shells out to untrusted-input engines.
USER nobody
ENTRYPOINT ["/usr/local/bin/worker"]
```
**Required change:** extend the single `apt-get install` line (do not add a second `RUN` layer — keep the existing single-layer-install-then-cleanup pattern) to add the LibreOffice `-nogui` packages and font packages, per RESEARCH.md's live-verified package list (installed cleanly against `debian:bookworm-slim` today, version `4:7.4.7-1+deb12u13`, all currently-tracked important CVEs fixed):
```dockerfile
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates \
      libvips-tools \
      libreoffice-writer-nogui \
      libreoffice-calc-nogui \
      libreoffice-impress-nogui \
      fonts-crosextra-carlito \
      fonts-crosextra-caladea \
      fonts-liberation2 \
 && rm -rf /var/lib/apt/lists/*
```
Note the comment above the `RUN apt-get` block (`# Runtime stage: image engine needs the libvips CLI.`) should be updated/extended to mention the document engine too, following the existing single-comment-per-block convention. `USER nobody` and the multi-stage build shape must remain unchanged (CLAUDE.md-locked: `CGO_ENABLED=0`, `debian:bookworm-slim`, unprivileged `nobody`) — RESEARCH.md empirically confirmed (2026-07-09) that `USER nobody` with an unwritable `$HOME` degrades gracefully (stderr warnings only, exit 0, correct output), so no new `HOME`/fontconfig Dockerfile provisioning is strictly required for correctness; if the plan chooses to set `HOME` for log-cleanliness, it belongs in the `cmd.Env` passed by `runCommand`'s caller inside `libreoffice.go`'s `Convert()` (a code change, not a Dockerfile change) — see RESEARCH.md Pitfall 3/Open Question 2.

---

## Shared Patterns

### Error wrapping (`fmt.Errorf("<action>: %w", err)`)
**Source:** `internal/convert/libvips.go:32` (`fmt.Errorf("libvips: %w", err)`), `internal/convert/exec.go` throughout
**Apply to:** every error return in `libreoffice.go` — prefix every wrap with `"libreoffice: "` exactly as `libvips.go` prefixes with `"libvips: "`.

### Hardened subprocess execution (reused unchanged)
**Source:** `internal/convert/exec.go` (`runCommand`, lines 19-46)
**Apply to:** `libreoffice.go`'s `Convert()` — call `runCommand(ctx, "soffice", args...)` exactly as `libvips.go` calls `runCommand(ctx, "vips", "copy", inPath, outPath)`. No changes to `exec.go` itself.

### Magic-byte output/input validation idiom
**Source:** `internal/convert/sniff.go` (`matchPNG`/`matchWebP`/`Sniff`, lines 42-93)
**Apply to:** `libreoffice.go`'s `validatePDF` — same "peek fixed prefix, `bytes.Equal` against a known signature" shape, applied to the converter's own output instead of client-supplied input (CONTEXT.md D-02's explicit framing).

### Stateless value-receiver `Converter` implementations
**Source:** `internal/convert/libvips.go:13` (`type LibvipsConverter struct{}`), `internal/convert/convert.go:20-25` (`Converter` interface)
**Apply to:** `type LibreOfficeConverter struct{}` — no fields, no constructor, registered directly as a struct literal (`converters.go`: `Default.Register(LibreOfficeConverter{})`).

### `exec.LookPath`-gated live process tests
**Source:** `internal/convert/convert_test.go:46-68` (`TestRunCommandTimeout`); same gating idiom (env-var-gated variant) also used in `internal/reconciler/reconciler_soak_test.go:21-25` (`DATABASE_URL`-gated `t.Skip`)
**Apply to:** `libreoffice_test.go`'s D-03 process-group-kill proof test — skip via `exec.LookPath("soffice")` rather than an env var, since the dependency being gated is a binary's presence on `PATH`, matching `TestRunCommandTimeout`'s exact style (binary presence, not a service URL).

## No Analog Found

None — every file in this phase's scope has a strong, same-package (or same-repo) analog. `libreoffice.go`'s filter-dispatch switch (`filterFor`) has no direct precedent in the codebase (it's new domain logic, not a structural pattern), but its *shape* (a `switch NormalizeFormat(x) { case ...: return ...; default: return "", err }`) is a standard small dispatch table with no risk of pattern ambiguity.

## Metadata

**Analog search scope:** `internal/convert/` (all files), `internal/worker/worker.go`, `internal/reconciler/reconciler_soak_test.go`, `Dockerfile.worker`, `cmd/worker/main.go`
**Files scanned:** 12 (`convert.go`, `libvips.go`, `exec.go`, `converters.go`, `sniff.go`, `sniff_test.go`, `docsniff.go`, `docsniff_test.go`, `dimensions.go`, `convert_test.go`, `worker.go`, `reconciler_soak_test.go`) plus `Dockerfile.worker` and `cmd/worker/main.go`
**Pattern extraction date:** 2026-07-09
