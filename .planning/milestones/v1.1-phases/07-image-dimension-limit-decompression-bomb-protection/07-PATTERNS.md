# Phase 7: Image Dimension Limit (Decompression-Bomb Protection) - Pattern Map

**Mapped:** 2026-07-09
**Files analyzed:** 7 (2 new, 5 modified)
**Analogs found:** 7 / 7

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|--------------------|------|-----------|-----------------|----------------|
| `internal/convert/dimensions.go` (NEW) | utility (format parser) | transform (bounded binary read) | `internal/convert/sniff.go` | exact |
| `internal/convert/dimensions_test.go` (NEW) | test | transform | `internal/convert/sniff_test.go` | exact |
| `internal/api/handlers.go` (MODIFIED — `handleCreateJob`) | controller | request-response | itself (existing Sniff-rejection block, same file) | exact |
| `internal/api/api.go` (MODIFIED — `Config`/`Server`) | config/provider | request-response | itself (existing `MaxUploadBytes`/`maxUploadByte` field) | exact |
| `internal/api/handlers_test.go` (MODIFIED — new rejection tests) | test | request-response | `TestCreateJob_ContentMismatch` / `TestCreateJob_UnsupportedPair` (same file) | exact |
| `cmd/api/main.go` (MODIFIED — env wiring) | config | request-response | itself (existing `envInt64("MAX_UPLOAD_BYTES", ...)` call) | exact |
| `.env.example` (MODIFIED — doc line) | config | — | itself (existing `MAX_UPLOAD_BYTES` line) | exact |

All files have exact analogs already in the codebase (mostly the very files being modified, following their own established local convention) — no "no analog found" section needed.

## Pattern Assignments

### `internal/convert/dimensions.go` (NEW) (utility, transform)

**Analog:** `internal/convert/sniff.go` (full file read above, 115 lines)

**Package/imports pattern** (`internal/convert/sniff.go:1-6`):
```go
package convert

import (
	"bytes"
	"io"
)
```
For `dimensions.go`, add `encoding/binary` and `errors`:
```go
package convert

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
)
```

**Constant + doc-comment convention** (`internal/convert/sniff.go:8-11`):
```go
// sniffLen is the number of leading bytes Sniff peeks at. 12 covers every
// signature in the table below, including WebP's "RIFF"+size+"WEBP" (offset
// 8-11) and HEIC's "ftyp"+brand (offset 4-11).
const sniffLen = 12
```
Mirror exactly for the new peek-window constant:
```go
// dimPeekLen is the bounded prefix size read to locate each format's
// declared-dimension fields (D-07). Any format whose fields aren't found
// within this window fails closed (ErrDimensionsUnknown) rather than
// growing the buffer or seeking further.
const dimPeekLen = 64 * 1024
```

**Closed dispatch-table convention** (`internal/convert/sniff.go:25-40`, the `signature`/`signatures` table — same shape as the 5-format-registered-list philosophy in `convert.Default`):
```go
type signature struct {
	format string
	match  func(buf []byte) bool
}

var signatures = []signature{
	{"png", matchPNG},
	{"jpg", matchJPEG},
	{"webp", matchWebP},
	{"heic", matchHEIC},
	{"tiff", matchTIFF},
}
```
Reuse the same "closed table scoped to exactly the 5 registered formats" shape for `dimensionParsers` (dispatch by format string instead of by matcher function) — see RESEARCH.md Pattern 1 for the exact `map[string]dimensionParser` code to copy verbatim.

**Core peek-and-restitch pattern to copy verbatim, generalized to a variable buffer size** (`internal/convert/sniff.go:78-93`):
```go
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
This is the exact idiom `Dimensions(format string, r io.Reader) (width, height uint32, rest io.Reader, err error)` must replicate — same `io.ReadFull` short-read tolerance (`readErr != io.ErrUnexpectedEOF && readErr != io.EOF`), same `io.MultiReader(bytes.NewReader(buf), r)` re-stitch. RESEARCH.md's Pattern 1 already contains the fully-written `Dimensions()` function ready to copy — use it directly, it was derived from this exact analog.

**Error-value convention** — no existing package-level sentinel error exists yet in `internal/convert`, so introduce one following the project's stated convention (`var Err<Reason>` documented in CLAUDE.md "Errors" section, cf. `jobs.ErrNotFound` in `internal/jobs/repo.go:14`):
```go
var ErrDimensionsUnknown = errors.New("cannot determine declared image dimensions")
```

**Per-format parser functions**: RESEARCH.md Patterns 2-6 contain fully-specced, byte-verified implementations (`pngDimensions`, `jpegDimensions`, `webpDimensions`, `tiffDimensions`, `heicDimensions`, plus the `walkBoxes` helper for HEIC) — copy these verbatim, they are the deliverable of this phase's research and already match project style (small pure functions taking `[]byte`, returning `(uint32, uint32, bool)`, explicit bounds checks before every slice access, `binary.BigEndian`/`binary.LittleEndian` for field extraction). Match `internal/convert/sniff.go`'s per-format function naming style (`matchPNG`, `matchJPEG`, ...) → new functions are `pngDimensions`, `jpegDimensions`, `webpDimensions`, `tiffDimensions`, `heicDimensions` (already so-named in RESEARCH.md).

**Doc-comment convention for the exported `Dimensions` function** (mirrors `Sniff`'s doc comment style at `internal/convert/sniff.go:69-77`) — copy the doc comment RESEARCH.md's Pattern 1 already drafted; it follows the "starts with the identifier name" convention from CLAUDE.md.

---

### `internal/convert/dimensions_test.go` (NEW) (test)

**Analog:** `internal/convert/sniff_test.go` (full file read above, 157 lines)

**Test structure/naming convention** (`internal/convert/sniff_test.go:9-18`):
```go
func TestSniffPNG(t *testing.T) {
	data := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52}
	detected, _, err := Sniff(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Sniff error: %v", err)
	}
	if detected != "png" {
		t.Fatalf("detected = %q, want png", detected)
	}
}
```
Mirror as `TestDimensionsPNG`, `TestDimensionsJPEG`, `TestDimensionsWebP_VP8`, `TestDimensionsWebP_VP8L`, `TestDimensionsWebP_VP8X`, `TestDimensionsTIFF_LittleEndian`, `TestDimensionsTIFF_BigEndian`, `TestDimensionsHEIC`, one per byte-fixture case, each constructing a minimal byte fixture and asserting `(width, height, err)`.

**"Preserves full stream" test convention to replicate** (`internal/convert/sniff_test.go:118-139`, `TestSniffPreservesFullStream`) — write an equivalent `TestDimensionsPreservesFullStream` asserting `io.ReadAll(rest)` reproduces the full original bytes (this directly covers D-07/Pitfall 5 from RESEARCH.md — the peeked prefix must not be dropped or duplicated).

**Short-input-no-panic test convention** (`internal/convert/sniff_test.go:105-116`, `TestSniffShortInputNoPanic`) — replicate as `TestDimensionsShortInputNoPanic`/`TestDimensionsTruncatedFailsClosed`, asserting a truncated/malformed input returns `ErrDimensionsUnknown` rather than panicking (covers RESEARCH.md Pitfall 6 and the fuzz-style truncated-input tests the Security Domain section recommends).

**Overflow/pitfall-specific test to add** (no direct analog — new case driven by RESEARCH.md Pitfall 1): a PNG/TIFF fixture with `width = height = 0xFFFFFFFF` asserting the pixel-count product is computed in `uint64` and correctly exceeds any realistic limit (i.e., the handler-level rejection path, or a `Dimensions()`-level unit test confirming no overflow/wraparound).

**JPEG DHT-exclusion test to add** (no direct analog — driven by RESEARCH.md Pitfall 3): a real-shaped JPEG fixture containing a `DHT` (0xFFC4) segment before the `SOF0` (0xFFC0) segment, asserting the parser correctly skips DHT and reads the true SOF width/height (not DHT's table bytes).

---

### `internal/api/handlers.go` — `handleCreateJob` (MODIFIED) (controller, request-response)

**Analog:** the function's own existing Sniff-rejection block, same file (`internal/api/handlers.go:116-150`, read above in full).

**Exact insertion point and pattern to copy** (`internal/api/handlers.go:143-150`, right after the pair-check, right before `callback_url` validation at line 155):
```go
	// Validate the conversion pair BEFORE writing anything to storage. The
	// DETECTED format (not the extension-derived one) is the source of truth
	// fed into the pair-check (D-05).
	if !convert.Default.Supports(detected, target) {
		writeError(w, http.StatusUnprocessableEntity,
			"unsupported conversion: "+detected+" -> "+target)
		return
	}
```

**Reject-before-storage-write + client_id-tagged log pattern to replicate** (`internal/api/handlers.go:125-141`, the unrecognized-content and mismatch rejections):
```go
	if detected == "" {
		// D-02: no known signature matches — reject rather than let the
		// (untrustworthy) extension win. D-08: scoped internal/* logging
		// exception for this rejection, tagged with the resolved client.
		log.Printf("content validation rejected: client_id=%s filename=%q reason=unrecognized_content", client.ID, filename)
		writeError(w, http.StatusUnprocessableEntity,
			"unrecognized file content for "+filename)
		return
	}
	if detected != source {
		// D-01/D-04: declared extension must be honest about the actual
		// content; no auto-correction to the detected format.
		log.Printf("content validation rejected: client_id=%s filename=%q reason=mismatch declared=%s detected=%s", client.ID, filename, source, detected)
		writeError(w, http.StatusUnprocessableEntity,
			"declared format "+source+" does not match detected content "+detected)
		return
	}
```
New code (from RESEARCH.md's "Handler integration point" Code Example, already correctly shaped to this exact analog) — insert immediately after the `Supports` pair-check block above:
```go
	// VALID-03: reject a decompression-bomb-shaped upload (declared pixel
	// dimensions exceeding the configured limit) before any storage write.
	// convert.Dimensions re-stitches its own bounded peek onto rest, so the
	// full original stream still reaches s.storage.Upload below unmodified.
	width, height, dimRest, err := convert.Dimensions(detected, rest)
	if err != nil {
		log.Printf("content validation rejected: client_id=%s filename=%q reason=dimensions_unknown", client.ID, filename)
		writeError(w, http.StatusUnprocessableEntity,
			"cannot determine declared image dimensions for "+filename)
		return
	}
	rest = dimRest
	totalPixels := uint64(width) * uint64(height)
	if totalPixels > s.maxImagePixels {
		log.Printf("content validation rejected: client_id=%s filename=%q reason=dimension_limit width=%d height=%d limit=%d", client.ID, filename, width, height, s.maxImagePixels)
		writeError(w, http.StatusUnprocessableEntity,
			"declared image dimensions exceed configured limit")
		return
	}
```
Note: `rest` and `err` are already declared (`:=`) earlier in the function from the `convert.Sniff` call at line 120 — the new block must reuse `=` for `rest`/`err` reassignment where appropriate, or shadow carefully; match existing variable-reuse convention ("err reused per-scope, not renamed" per CLAUDE.md Code Style).

**Downstream use of `rest`** (`internal/api/handlers.go:169`) — unchanged call site, but now receives the twice-re-stitched reader:
```go
	if err := s.storage.Upload(ctx, key, rest, header.Size, contentType); err != nil {
```

---

### `internal/api/api.go` — `Config`/`Server` (MODIFIED) (config/provider, request-response)

**Analog:** the existing `MaxUploadBytes`/`maxUploadByte` field, same file (`internal/api/api.go:50-98`, read above in full).

**Server struct field pattern to copy** (`internal/api/api.go:50-60`):
```go
type Server struct {
	repo          Repo
	storage       Storage
	queue         Enqueuer
	resolver      auth.ClientResolver
	health        HealthDeps
	maxUploadByte int64
	presignTTL    time.Duration
	ipRateRPM     int
	clientRateRPM int
}
```
Add a `maxImagePixels uint64` field (uint64 per RESEARCH.md Pitfall 1's overflow-safety requirement — note this is a **wider type** than the existing `int64`-typed `maxUploadByte`; this is a deliberate, documented deviation, not an inconsistency, because the comparison operand is a product of two `uint32`s).

**Config struct field pattern to copy** (`internal/api/api.go:63-68`):
```go
type Config struct {
	MaxUploadBytes     int64
	PresignTTL         time.Duration
	IPRateLimitRPM     int
	ClientRateLimitRPM int
}
```
Add `MaxImagePixels uint64`.

**NewServer default-fallback pattern to copy** (`internal/api/api.go:74-97`):
```go
func NewServer(repo Repo, storage Storage, queue Enqueuer, resolver auth.ClientResolver, health HealthDeps, cfg Config) *Server {
	if cfg.PresignTTL == 0 {
		cfg.PresignTTL = 15 * time.Minute
	}
	if cfg.MaxUploadBytes == 0 {
		cfg.MaxUploadBytes = 100 << 20 // 100 MiB
	}
	...
	return &Server{
		...
		maxUploadByte: cfg.MaxUploadBytes,
		...
	}
}
```
Add the same shape: `if cfg.MaxImagePixels == 0 { cfg.MaxImagePixels = 100_000_000 // D-05: 100 megapixels default }` and thread `maxImagePixels: cfg.MaxImagePixels` into the returned `&Server{...}` literal.

---

### `internal/api/handlers_test.go` (MODIFIED — new rejection tests) (test, request-response)

**Analog:** `TestCreateJob_ContentMismatch` and `TestCreateJob_UnsupportedPair`, same file (read above, lines 105-209 for helpers + first test; `TestCreateJob_UnsupportedPair` at line 263 follows the identical shape).

**Fixture-builder convention to copy** (`internal/api/handlers_test.go:107-117`):
```go
// pngBytesFixture returns the minimal magic-byte prefix that convert.Sniff
// detects as "png" (plus a few trailing bytes so it's a plausible file).
func pngBytesFixture() []byte {
	return []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52}
}
```
Add a new fixture builder, e.g. `oversizedPNGFixture()` that returns a full valid PNG signature + IHDR chunk with `width = height = 20000` (400 megapixels, over the 100-megapixel default) — needs the full 24-byte IHDR (signature[8] + length[4] + type[4] + width[4] + height[4]), unlike the existing 16-byte `pngBytesFixture` which stops mid-chunk (that's fine for `Sniff`, which only needs the first 8 bytes, but `Dimensions` needs the full 24).

**Test-case structure to copy verbatim** (`internal/api/handlers_test.go:190-209`, `TestCreateJob_ContentMismatch`):
```go
func TestCreateJob_ContentMismatch(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	srv, _ := newTestServer(repo, store, &fakeQueue{})

	body, ct := multipartBody(t, "in.jpg", "webp", pngBytesFixture())
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if !strings.Contains(resp["error"], "jpg") || !strings.Contains(resp["error"], "png") {
		t.Errorf("error = %q, want a message naming both declared (jpg) and detected (png)", resp["error"])
	}
	if store.uploaded {
		t.Error("must not upload to storage before content validation passes")
	}
}
```
Mirror as `TestCreateJob_DimensionLimitExceeded` (asserts 422 + `store.uploaded == false`, using the new oversized fixture) and `TestCreateJob_DimensionsUnknown` (a truncated/malformed-but-Sniff-passing fixture, asserting 422 with a "cannot determine declared image dimensions" style message).

**`newTestServer` helper needs a `MaxImagePixels` override variant** (`internal/api/handlers_test.go:140-143`):
```go
func newTestServer(repo Repo, store Storage, q Enqueuer) (*Server, *fakeResolver) {
	resolver := newFakeResolver()
	return NewServer(repo, store, q, resolver, healthyDeps(), Config{MaxUploadBytes: 1 << 20}), resolver
}
```
For the dimension-limit test, either add `MaxImagePixels` to this shared helper's `Config{...}` literal (small, e.g. `1_000_000` so a modest fixture triggers rejection without huge test fixtures) or construct the `Server` directly via `NewServer(..., Config{MaxUploadBytes: 1 << 20, MaxImagePixels: 1_000_000})` inline in the new test, following the same direct-`NewServer`-call precedent already used elsewhere in the file (e.g. `TestHealthz_Degraded` at line 343, `TestGetJob_DonePresigned` at line 380).

---

### `cmd/api/main.go` (MODIFIED — env wiring) (config, request-response)

**Analog:** the existing `envInt64("MAX_UPLOAD_BYTES", 100<<20)` call, same file (`cmd/api/main.go:99-103`, read above).

**Exact pattern to copy**:
```go
	srv := api.NewServer(jobs.NewRepo(pool), store, qc, resolver, health, api.Config{
		MaxUploadBytes:     envInt64("MAX_UPLOAD_BYTES", 100<<20),
		IPRateLimitRPM:     int(envInt64("RATE_LIMIT_IP_RPM", 60)),
		ClientRateLimitRPM: int(envInt64("RATE_LIMIT_CLIENT_RPM", 120)),
	})
```
Add a new field, casting `envInt64`'s `int64` return to `uint64` (since `envInt64` itself doesn't need duplicating — the existing helper already tolerates trailing inline `.env` comments per `firstField`, `cmd/api/main.go:156-173`):
```go
	srv := api.NewServer(jobs.NewRepo(pool), store, qc, resolver, health, api.Config{
		MaxUploadBytes:     envInt64("MAX_UPLOAD_BYTES", 100<<20),
		MaxImagePixels:     uint64(envInt64("MAX_IMAGE_PIXELS", 100_000_000)), // D-05: 100 megapixels default
		IPRateLimitRPM:     int(envInt64("RATE_LIMIT_IP_RPM", 60)),
		ClientRateLimitRPM: int(envInt64("RATE_LIMIT_CLIENT_RPM", 120)),
	})
```
No new helper function needed — `envInt64` (`cmd/api/main.go:156-164`) is reused as-is, matching the "don't hand-roll" guidance from RESEARCH.md.

---

### `.env.example` (MODIFIED — doc line) (config)

**Analog:** the existing `MAX_UPLOAD_BYTES` line, same file (`.env.example:16`):
```
MAX_UPLOAD_BYTES=104857600   # 100 MiB
```
Add immediately after it, matching the inline-comment doc style used throughout the file (cf. `RATE_LIMIT_IP_RPM=60   # coarse pre-auth IP flood guard, requests/min` at line 18):
```
MAX_IMAGE_PIXELS=100000000   # decompression-bomb guard: max declared width*height (default 100 megapixels, e.g. 10000x10000)
```

## Shared Patterns

### Peek-and-restitch (bounded, non-seekable stream read)
**Source:** `internal/convert/sniff.go:78-93` (`Sniff`)
**Apply to:** `internal/convert/dimensions.go`'s new `Dimensions()` function — identical `io.ReadFull` + short-read tolerance + `io.MultiReader(bytes.NewReader(buf), r)` re-stitch, just with `dimPeekLen` (64 KiB) instead of `sniffLen` (12 bytes).
```go
buf := make([]byte, sniffLen)
n, readErr := io.ReadFull(r, buf)
if readErr != nil && readErr != io.ErrUnexpectedEOF && readErr != io.EOF {
	return "", nil, readErr
}
buf = buf[:n]
rest = io.MultiReader(bytes.NewReader(buf), r)
```

### Reject-before-storage-write with client_id-tagged logging (D-08 style)
**Source:** `internal/api/handlers.go:125-141` (`handleCreateJob`'s existing Sniff-rejection blocks)
**Apply to:** the new dimension-check rejection block in `handleCreateJob` — identical shape: `log.Printf("content validation rejected: client_id=%s filename=%q reason=<reason> ...", client.ID, filename, ...)` immediately followed by `writeError(w, http.StatusUnprocessableEntity, "<message>")` and `return`, always BEFORE `s.storage.Upload`.
```go
log.Printf("content validation rejected: client_id=%s filename=%q reason=mismatch declared=%s detected=%s", client.ID, filename, source, detected)
writeError(w, http.StatusUnprocessableEntity,
	"declared format "+source+" does not match detected content "+detected)
return
```

### Config/env wiring with `os.Getenv`-only, no config file (D-05's `MAX_IMAGE_PIXELS`)
**Source:** `cmd/api/main.go:99-103` + `cmd/api/main.go:156-164` (`envInt64` helper) + `internal/api/api.go:74-97` (`NewServer` default-fallback)
**Apply to:** All three touch points — `cmd/api/main.go` (env parse call), `internal/api/api.go` (`Config` field + `Server` field + zero-value default), `.env.example` (doc line) — must move together as one cross-cutting change, exactly as `MAX_UPLOAD_BYTES` already threads through all three files today.

### Zero-dependency hardcoded format table (closed to the 5 registered formats)
**Source:** `internal/convert/sniff.go:25-40` (`signature`/`signatures`) and `internal/convert/convert.go:29-39` (`NormalizeFormat`)
**Apply to:** `dimensionParsers` in the new `dimensions.go` — same closed-table philosophy, same `NormalizeFormat` normalization before dispatch (RESEARCH.md's Pattern 1 already keys the map by `NormalizeFormat`-normalized strings, consistent with this precedent).

## No Analog Found

None — every file in this phase either has a direct analog in an existing sibling file (`sniff.go` → `dimensions.go`, `sniff_test.go` → `dimensions_test.go`) or is itself the analog for its own modification (the `MAX_UPLOAD_BYTES` threading pattern repeated for `MAX_IMAGE_PIXELS` across `handlers.go`/`api.go`/`main.go`/`.env.example`).

## Metadata

**Analog search scope:** `internal/convert/`, `internal/api/`, `cmd/api/`
**Files scanned:** `internal/convert/sniff.go`, `internal/convert/sniff_test.go`, `internal/convert/convert.go`, `internal/api/handlers.go`, `internal/api/api.go`, `internal/api/handlers_test.go`, `cmd/api/main.go`, `.env.example`
**Pattern extraction date:** 2026-07-09
