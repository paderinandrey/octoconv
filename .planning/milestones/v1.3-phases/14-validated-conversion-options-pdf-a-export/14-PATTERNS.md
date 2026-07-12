# Phase 14: Validated Conversion Options & PDF/A Export - Pattern Map

**Mapped:** 2026-07-10
**Files analyzed:** 8 (6 modified existing, 1 likely-new small file, 1 test file extended)
**Analogs found:** 8 / 8 (all analogs are in-repo; every touched file already exists and IS its own closest analog — this phase extends established patterns rather than introducing a new subsystem)

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|-----------------|----------------|
| `internal/api/handlers.go` (`handleCreateJob` — parse+validate `opts` field) | controller | request-response | itself — existing `callback_url` optional-field validation block (lines 226-235) | exact (same file, same function, same fail-closed-before-storage idiom) |
| `internal/convert/opts.go` (NEW, suggested) — closed `DocOpts` struct + syntax/applicability validation | utility/model | transform | `internal/convert/olecfb.go` (single-purpose small file, package-level doc comment, exported `Is`/`Validate`-style function) + `internal/convert/convert.go`'s `Pair`/`NormalizeFormat` for the "closed lookup table" idiom | role-match (new file, but shape is a direct copy of `olecfb.go`'s single-purpose-file convention) |
| `internal/convert/libreoffice.go` (`Convert` stops discarding opts; `filterTable`-style PDF/A filter-options constant; `validateDocumentOutput` OutputIntent check) | service (converter) | transform | itself — existing `filterTable`/`filterFor` closed-map idiom (lines 102-135) and `validateDocumentOutput`'s target-format dispatch (lines 173-205) | exact (same file, same established table-driven pattern, mechanical extension) |
| `internal/worker/worker.go` (`terminalLibreOfficeSignatures` update; `job.Opts` threaded into `conv.Convert`; strict opts parse from Postgres) | worker/consumer | event-driven | itself — `terminalLibreOfficeSignatures` slice (lines 51-57) and `process()`'s `conv.Convert(attemptCtx, inPath, outPath, nil)` call site (line 412) | exact (same file, same slice, same call site — literal one-line + one-slice-entry change) |
| `internal/jobs/jobs.go` (`Job`/`CreateParams` gain `Opts` field) | model | CRUD | itself — existing field list (`Job` struct lines 23-37, `CreateParams` lines 65-74) | exact (add one field, same struct, same style) |
| `internal/jobs/repo.go` (`Create` INSERT writes `options`; `Get` SELECT reads `options`) | service (repository) | CRUD | itself — `transition()`'s existing `detail map[string]any` → `json.Marshal` → jsonb column pattern (lines 383-393) and `Get`'s nullable-column `*string`/`deref` pattern (lines 290-317) | exact (same file, same marshal/unmarshal idiom already used for `job_events.detail`) |
| `internal/e2e/e2e_test.go` (PDF/A pair + negative opts cases; `buildJobRequest`/`postJob`/`postJobExpectStatus` gain an `opts` parameter) | test (e2e/integration) | request-response | itself — `TestCrossFormatConversionE2E` (lines 388-420) + `TestOLECFBRejectionE2E` (lines 461-486) for the two behaviors needed (happy-path assertion, 422 assertion) | exact (same file, same two established table-driven test shapes) |
| `internal/convert/libreoffice_test.go` (unit tests for opts→filter-options builder, injection test) | test (unit) | transform | itself — `TestFilterFor` (lines 14-61) and `TestValidateDocumentOutput` (lines 105-162) | exact (same file, same table-driven-with-error-substring-assertion shape) |

## Pattern Assignments

### `internal/api/handlers.go` — `opts` field parsing + two-step validation (controller, request-response)

**Analog:** the file's own existing `callback_url` optional-field block, same function, same ordering discipline.

**Imports pattern** (lines 1-21, unchanged — no new import needed unless `opts.go` is added to `internal/convert`, in which case nothing new is needed since `convert` is already imported):
```go
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/apaderin/octoconv/internal/auth"
	"github.com/apaderin/octoconv/internal/convert"
	"github.com/apaderin/octoconv/internal/jobs"
	"github.com/apaderin/octoconv/internal/storage"
)
```

**Optional-field-with-validation pattern to copy** (`internal/api/handlers.go:226-235`, the `callback_url` block — this is the direct template for the new `opts` block; note it runs AFTER format-pair validation, exactly where D-04 says applicability validation belongs):
```go
	// callback_url is optional (per-job, D-02); an empty value leaves the
	// existing polling path unchanged. When present it is SSRF-validated
	// BEFORE writing anything to storage, same discipline as the format pair.
	callbackURL := r.FormValue(formFieldCallbackURL)
	if callbackURL != "" {
		if err := validateCallbackURL(callbackURL); err != nil {
			writeError(w, http.StatusBadRequest, "invalid callback_url")
			return
		}
	}
```
Copy this shape for `opts`: `r.FormValue(formFieldOpts)` → if non-empty, `json.NewDecoder(strings.NewReader(raw)).DisallowUnknownFields().Decode(&struct)` (syntax step, D-04 step 1) → then, immediately after the existing `engine, ok := convert.Default.EngineFor(detected, target)` block (lines 193-198), call the applicability validator (D-04 step 2) with `(engine, detected, target, opts)` already in scope. Both failure branches follow the exact `writeError(w, http.StatusUnprocessableEntity, "<short message>")` idiom used throughout this function (see the format-pair-unsupported message at line 195-197 for the closest style match for a 422).

**Constant-block pattern** (`internal/api/handlers.go:23-28`) — add a new form-field constant here, matching the existing style:
```go
const (
	formFieldFile        = "file"
	formFieldTarget      = "target"
	formFieldCallbackURL = "callback_url"
	operationConv        = "convert"
)
```

**CreateParams wiring** (lines 250-266) — `jobs.CreateParams{...}` literal must gain an `Opts:` field, mirroring how `CallbackURL: callbackURL` is already threaded through unchanged from the parsed form value into the repository call.

**Response echo (D-09)** — `writeJSON(w, http.StatusAccepted, map[string]any{...})` (lines 294-297) and the `resp := map[string]any{...}` builder in `handleGetJob` (lines 331-334) are the two places to add `"opts": job.Opts` conditionally (only when non-empty, per D-09's `omitempty` requirement) — same map-literal-then-conditional-key-append idiom already used for `download_url`/`error_code`/`error_message` in `handleGetJob` (lines 336-358).

---

### `internal/convert/opts.go` (NEW — suggested location for `DocOpts` + validation) (model/utility, transform)

**Analog:** `internal/convert/olecfb.go` (single-purpose small file convention) + `internal/convert/convert.go`'s `Pair`/`NormalizeFormat` (closed enum/lookup idiom).

**Package doc comment + single-purpose-file shape to copy** (`internal/convert/olecfb.go:1-16`):
```go
package convert

import (
	"bytes"
	"io"
)

// oleCFBMagic is the 8-byte Compound File Binary (OLE2/MS-CFB) signature ...
var oleCFBMagic = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

// IsOLECFB reports whether r begins with the OLE-CFB magic signature. This
// is a fail-closed pre-flight rejection check, NOT a Sniff/SniffContainer
// registry entry: ...
func IsOLECFB(r io.ReaderAt) bool {
	buf := make([]byte, len(oleCFBMagic))
	n, _ := r.ReadAt(buf, 0)
	return n == len(oleCFBMagic) && bytes.Equal(buf, oleCFBMagic)
}
```
Follow this exact convention for a new `internal/convert/opts.go`: a short exported type (`DocOpts`), a closed enum constant/allow-list (`pdf/a-2b` today, per D-01), and an exported applicability-check function — every identifier gets a Go-convention doc comment starting with its own name, and non-obvious "why" reasoning stays inline (see how `olecfb.go` explains why it's NOT a registry entry).

**Closed-enum / closed-table idiom to copy** (`internal/convert/convert.go:17-20`, engine-class constants, AND `internal/convert/libreoffice.go:108-122`, `filterTable`):
```go
// Engine-class identifiers (D-01/DEBT-02). This is the SINGLE compile-time
// source of truth for engine-class string values ...
const (
	EngineImage    = "image"
	EngineDocument = "document"
)
```
```go
// filterTable is the explicit (source, target) -> LibreOffice export filter
// name table (D-02). There is deliberately no auto-derivation from extension:
// every supported pair is listed here by hand ...
var filterTable = map[[2]string]string{ ... }
```
Copy this pattern for `pdf_profile`'s closed enum (`"pdf/a-2b"` only, D-01) and for the mapping from a validated `DocOpts` field to the hardcoded `SelectPdfVersion`/`EmbedStandardFonts` filter-options constants (D-07, Pitfall 7/9) — the map/const MUST be keyed on the validated struct field, never on raw client bytes, exactly as `filterTable` is keyed on the already-`NormalizeFormat`-passed `(source, target)` pair, never on a raw client string.

**Strict JSON decode idiom (D-02, D-04, D-10)** — no existing analog decodes JSON with `DisallowUnknownFields` in this codebase yet (this is genuinely new plumbing); the closest existing JSON-marshal convention to stay consistent with is `internal/jobs/repo.go`'s `json.Marshal(detail)` (`transition`, lines 383-393) for the *write* side, and standard `encoding/json` idioms for the *read* side:
```go
dec := json.NewDecoder(strings.NewReader(raw))
dec.DisallowUnknownFields()
var opts DocOpts
if err := dec.Decode(&opts); err != nil {
    // -> 422 at the API layer; -> terminal/SkipRetry at the worker layer (D-10)
}
```

---

### `internal/convert/libreoffice.go` — opts stop being discarded; PDF/A filter-options + OutputIntent check (service/converter, transform)

**Analog:** itself — `filterTable`/`filterFor` (closed-table idiom) and `validateDocumentOutput` (target-format dispatch), both already-shipped Phase 13 patterns this phase extends mechanically.

**Convert signature — currently discards opts** (`internal/convert/libreoffice.go:54`):
```go
func (LibreOfficeConverter) Convert(ctx context.Context, inPath, outPath string, _ map[string]any) error {
```
Change `_ map[string]any` to a named parameter; per D-07, the RAW `map[string]any` must never be marshaled into the filter-options string — `Convert` (or a helper it calls) must first re-derive/receive the already-validated `DocOpts` struct (see `opts.go` above) and build the `--convert-to` filter suffix purely from server-side constants keyed on `opts.PDFProfile`.

**`--convert-to` construction site to extend** (`internal/convert/libreoffice.go:61-74`):
```go
	targetFormat := NormalizeFormat(filepath.Ext(outPath))
	filter, err := filterFor(filepath.Ext(inPath), filepath.Ext(outPath))
	if err != nil {
		return fmt.Errorf("libreoffice: %w", err)
	}

	args := []string{
		"--headless", "--invisible", "--nocrashreport", "--nodefault",
		"--nologo", "--nofirststartwizard", "--norestore",
		"-env:UserInstallation=file://" + profileDir,
		"--convert-to", targetFormat + ":" + filter,
		"--outdir", workDir,
		inPath,
	}
```
The PDF/A case appends a server-constant-built FilterOptions JSON suffix to the existing `targetFormat + ":" + filter` string (research's verified syntax: `pdf:writer_pdf_Export:{"SelectPdfVersion":{"type":"long","value":"2"},"EmbedStandardFonts":{"type":"boolean","value":true}}`) — this whole string remains ONE `args` element (no shell involved, `runCommand`/`exec.Command` already takes an argv array — see `internal/convert/exec.go:19-20`), so no new escaping mechanism is needed.

**`validateDocumentOutput` dispatch to extend for the OutputIntent check** (`internal/convert/libreoffice.go:173-205`, the exact insertion point per D-05/D-06):
```go
func validateDocumentOutput(path, targetFormat string) error {
	target := NormalizeFormat(targetFormat)
	if target == "pdf" {
		return validatePDF(path)
	}
	...
}
```
D-05 requires: when `pdf_profile` was requested, `validateDocumentOutput`'s `target == "pdf"` branch must additionally grep the produced bytes for the `/GTS_PDFA` OutputIntent marker (Pitfall 8 — explicitly non-authoritative sanity check, not full ISO 19005 conformance) and return a terminal error (following `validatePDF`'s own error-message style, e.g. `"libreoffice: output missing PDF/A OutputIntent marker"`) if absent. This requires `validateDocumentOutput`'s signature to also receive whether PDF/A was requested (either an added parameter or reading it off the already-in-scope opts) — model the new terminal-error message string on the two existing ones (`"libreoffice: output missing %%PDF- magic bytes"` at line 156/168, `"libreoffice: output is empty"` at line 150) so it slots naturally into `terminalLibreOfficeSignatures` below.

**Error-wrapping convention** (unchanged, apply throughout): `fmt.Errorf("libreoffice: <action>: %w", err)` — every error in this file is prefixed `"libreoffice: "` (lines 58, 64, 76, 90, 93, 147, 150, 156, 160, 165, 168, 189, 192, 196, 202) so new errors must follow the same prefix.

---

### `internal/worker/worker.go` — terminal-signature update + strict opts parse + threading opts into Convert (worker/consumer, event-driven)

**Analog:** itself — `terminalLibreOfficeSignatures` slice and the `process()` call site.

**Terminal-signature list to extend (D-06 — CRITICAL, same commit as the validator change)** (`internal/worker/worker.go:51-57`):
```go
var terminalLibreOfficeSignatures = []string{
	"output missing %pdf- magic bytes",
	"output is empty",
	"no export filter for",
	"output does not match expected container format",
	"produced no output file",
}
```
Append the new PDF/A OutputIntent-missing error's lowercased substring here in the SAME commit that introduces the check in `libreoffice.go` — this is the exact pattern the file's own comment (lines 38-50) documents as mandatory, and is D-06's explicit requirement, inherited verbatim from Phase 13's D-03/D-04.

**`conv.Convert` call site to change** (`internal/worker/worker.go:412`):
```go
	if err := conv.Convert(attemptCtx, inPath, outPath, nil); err != nil {
		return fmt.Errorf("convert: %w", err)
	}
```
Replace the hardcoded `nil` with `job.Opts` (already loaded via `h.repo.Get` earlier in `process()`, no new DB read needed — `job` is the same variable already in scope at line 373's function signature `func (h *Handler) process(ctx context.Context, job *jobs.Job) error`).

**Strict re-parse from Postgres (D-10)** — per D-10, the worker does NOT re-validate business rules (applicability/enum values), only strict-parses whatever `jobs.Job.Opts` is (already unmarshaled by `internal/jobs/repo.go`'s `Get`, see below) into the same closed struct with `DisallowUnknownFields`; garbage in the column is a terminal error (`asynq.SkipRetry`-wrapped, matching the existing unparseable-payload idiom at lines 177-181/240-244):
```go
	payload, err := queue.ParseConvertPayload(t.Payload())
	if err != nil {
		// Unparseable payload: nothing we can retry into success.
		return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
	}
```
Copy this exact `fmt.Errorf("%w: %v", asynq.SkipRetry, err)` idiom for a strict-opts-reparse failure — it is the established "unparseable input, no retry can help" pattern used identically in `HandleImageConvert`, `HandleDocumentConvert`, and `HandleWebhookDeliver` (lines 177-181, 240-244, 295-299).

---

### `internal/jobs/jobs.go` — add `Opts` field (model, CRUD)

**Analog:** itself.

**Struct pattern to extend** (`internal/jobs/jobs.go:23-37` and `65-74`):
```go
// Job is a row of the jobs table (subset used by the image slice).
type Job struct {
	ID           uuid.UUID
	ClientID     uuid.UUID
	Operation    string
	Engine       string
	Status       string
	SourceFormat string
	TargetFormat string
	CallbackURL  string
	ErrorCode    string
	ErrorMessage string
	CreatedAt    time.Time
	StartedAt    *time.Time
	FinishedAt   *time.Time
}
```
```go
// CreateParams describes a new convert job and its single input. ID is the
// caller-provided job id so storage keys (which embed the id) and the row match.
type CreateParams struct {
	ID           uuid.UUID
	ClientID     uuid.UUID
	Operation    string
	Engine       string
	SourceFormat string
	TargetFormat string
	CallbackURL  string
	Input        Input
}
```
Add `Opts map[string]any` to both, in the same position CallbackURL occupies (last scalar field before the Input struct) — the codebase already treats the DB column as normalized/validated-struct-serialized-to-map (per D-08), so `map[string]any` (round-tripping through jsonb) is the right shape here, matching `internal/jobs/repo.go`'s existing `detail map[string]any` idiom used for `job_events.detail`.

---

### `internal/jobs/repo.go` — INSERT/SELECT the `options` column (service/repository, CRUD)

**Analog:** itself — `transition()`'s existing `detail map[string]any → json.Marshal → jsonb` idiom (lines 383-393) is the exact template for `Create`'s write side; `Get`'s nullable-column `*string`/`deref` idiom (lines 290-317) is the template for the read side, adjusted for jsonb→map instead of jsonb→string.

**Write-side marshal pattern to copy** (`internal/jobs/repo.go:383-393`, inside `transition`):
```go
	var detailJSON []byte
	if detail != nil {
		var err error
		detailJSON, err = json.Marshal(detail)
		if err != nil {
			return fmt.Errorf("marshal transition detail: %w", err)
		}
	}
```
Apply the same shape inside `Create` (lines 80-116): marshal `p.Opts` (defaulting to `{}` per D-08/ARCHITECTURE.md's explicit note — "default to `{}` if nil, mirroring the existing `detail`-marshal pattern"), then extend the existing INSERT:
```go
		if _, err := tx.Exec(ctx, `
			INSERT INTO jobs (id, client_id, operation, engine, status, source_format, target_format, callback_url)
			VALUES ($1, $2, $3, $4, 'queued', $5, $6, $7)`,
			jobID, p.ClientID, p.Operation, p.Engine, p.SourceFormat, p.TargetFormat, p.CallbackURL,
		); err != nil {
			return fmt.Errorf("insert job: %w", err)
		}
```
add `options` as an 8th column + `$8` bound to the marshaled bytes, following the exact `fmt.Errorf("insert job: %w", err)` wrap style already used.

**Read-side nullable-column pattern to copy** (`internal/jobs/repo.go:290-317`, `Get`):
```go
func (r *Repo) Get(ctx context.Context, id uuid.UUID) (*Job, error) {
	var j Job
	var src, tgt, cb, code, msg *string
	var clientID *uuid.UUID
	err := r.pool.QueryRow(ctx, `
		SELECT id, client_id, operation, engine, status, source_format, target_format,
		       callback_url, error_code, error_message, created_at, started_at, finished_at
		FROM jobs WHERE id = $1`, id,
	).Scan(&j.ID, &clientID, &j.Operation, &j.Engine, &j.Status, &src, &tgt,
		&cb, &code, &msg, &j.CreatedAt, &j.StartedAt, &j.FinishedAt)
	...
	j.SourceFormat = deref(src)
	j.TargetFormat = deref(tgt)
	j.CallbackURL = deref(cb)
	j.ErrorCode = deref(code)
	j.ErrorMessage = deref(msg)
	return &j, nil
}
```
Add `options` to the SELECT column list, scan into a `[]byte`/`json.RawMessage` local (the column is `NOT NULL DEFAULT '{}'::jsonb` per `internal/db/migrations/0001_init.sql:25`, so unlike `src`/`tgt`/`cb` it is never NULL — a plain `[]byte` scan target, not a pointer, is correct and simpler than the existing nullable-deref idiom), then `json.Unmarshal(optsJSON, &j.Opts)` before `return &j, nil`.

---

### `internal/e2e/e2e_test.go` — PDF/A pair + negative opts cases (test/integration, request-response)

**Analog:** `TestCrossFormatConversionE2E` (happy-path table pattern) + `TestOLECFBRejectionE2E` (422-rejection pattern), both in this same file.

**Happy-path table-driven test to copy** (lines 388-420, `TestCrossFormatConversionE2E`):
```go
func TestCrossFormatConversionE2E(t *testing.T) {
	cfg := e2eSetup(t)
	apiKey := provisionClient(t)

	for _, pair := range crossFormatPairs {
		t.Run(pair.filename+"->"+pair.target, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", pair.filename))
			if err != nil {
				t.Fatalf("read fixture %s: %v", pair.filename, err)
			}

			jobID := postJob(t, cfg.baseURL, apiKey, pair.filename, data, pair.target, "")

			body := pollUntilDone(t, cfg.baseURL, apiKey, jobID, 5*time.Minute)

			downloadURL, _ := body["download_url"].(string)
			if downloadURL == "" {
				t.Fatalf("job %s done but download_url missing/empty: %v", jobID, body)
			}
			assertDownloadIsFormat(t, downloadURL, pair.target)
		})
	}
}
```
A new PDF/A test follows this exact shape: `sample.docx -> pdf` with `opts={"pdf_profile":"pdf/a-2b"}`, polling to done, then a NEW assertion helper (`assertDownloadIsPDFA`, modeled on `assertDownloadIsPDF` at lines 354-372) that greps the downloaded bytes for the `/GTS_PDFA` OutputIntent marker rather than just the `%PDF-` prefix.

**422-rejection table-driven test to copy** (lines 461-486, `TestOLECFBRejectionE2E`):
```go
func TestOLECFBRejectionE2E(t *testing.T) {
	cfg := e2eSetup(t)
	apiKey := provisionClient(t)

	for _, filename := range oleCFBFixtures {
		t.Run(filename, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", filename))
			if err != nil {
				t.Fatalf("read fixture %s: %v", filename, err)
			}

			body := postJobExpectStatus(t, cfg.baseURL, apiKey, filename, data, "pdf", http.StatusUnprocessableEntity)

			if !bytes.Contains(bytes.ToLower(body), []byte("password")) {
				t.Errorf("422 body for %s does not mention the remedy; body=%s", filename, body)
			}
		})
	}
}
```
Copy this shape for the two negative-opts cases the phase requires: (a) `pdf_profile` applied to an image job (unsupported applicability, D-03) and (b) `pdf_profile` on a docx->odt job (unsupported applicability, D-03) — both expect `http.StatusUnprocessableEntity`. A third negative case (malformed opts JSON / unknown key) expects a 400/422 depending on where the planner puts the syntax-error status code (D-04 step 1 is separate from step 2's 422; confirm against D-03's stated "422 fail-closed" wording, which appears to apply specifically to applicability — the syntax step's status is Claude's Discretion per CONTEXT.md).

**REQUIRED helper-signature change:** `buildJobRequest`, `postJob`, and `postJobExpectStatus` (lines 120-205) currently take `(t, baseURL, apiKey, filename, data, target, callbackURL)` with NO opts parameter — the PDF/A test needs an `opts` value threaded through the multipart body the same way `callback_url` already is:
```go
	if callbackURL != "" {
		if err := w.WriteField("callback_url", callbackURL); err != nil {
			t.Fatalf("write callback_url field: %v", err)
		}
	}
```
The planner should add an analogous `if opts != "" { w.WriteField("opts", opts) }` block and extend all three call signatures (or add new sibling helpers `postJobWithOpts`/`postJobExpectStatusWithOpts` if changing the existing signature would touch too many call sites — Claude's Discretion, not specified in CONTEXT.md).

---

### `internal/convert/libreoffice_test.go` — opts→filter-options builder unit test + injection test (test/unit, transform)

**Analog:** `TestFilterFor` (table-driven map + explicit error-substring assertions, lines 14-61) and `TestValidateDocumentOutput` (three-case shape: valid/empty/wrong, lines 105-162).

**Table-driven-with-error-case shape to copy** (lines 14-61, abbreviated):
```go
func TestFilterFor(t *testing.T) {
	cases := map[[2]string]string{ ... }
	for in, want := range cases {
		got, err := filterFor(in[0], in[1])
		if err != nil {
			t.Errorf("filterFor(%q, %q) unexpected error: %v", in[0], in[1], err)
			continue
		}
		if got != want {
			t.Errorf("filterFor(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}

	if got, err := filterFor("docx", "mp3"); err == nil {
		t.Errorf("filterFor(\"docx\", \"mp3\") = %q, nil, want an error", got)
	} else if got != "" {
		t.Errorf("filterFor(\"docx\", \"mp3\") returned non-empty filter %q alongside error", got)
	} else if !strings.Contains(err.Error(), "no export filter for") {
		t.Errorf("filterFor(\"docx\", \"mp3\") error = %v, want substring \"no export filter for\"", err)
	}
}
```
This is the exact template for a new `TestPDFAFilterOptions` (asserting the built filter-options JSON string contains both `SelectPdfVersion` and `EmbedStandardFonts:true` whenever `pdf_profile` is set, per Pitfall 7) and — critically — the injection test required by D-07/success-criterion-1: a table of adversarial `opts` map values (e.g. `{"pdf_profile": "pdf/a-2b\",\"EncryptFile\":true,\"x\":\""}`) asserted to produce IDENTICAL filter-options output as the clean case, proving client bytes never reach the argv string (Pitfall 9's exact concern).

---

## Shared Patterns

### Fail-closed-before-storage-write ordering
**Source:** `internal/api/handlers.go` (the whole `handleCreateJob` body — Sniff → format-pair validation → dimension check → callback_url validation → THEN `s.storage.Upload`/`s.repo.Create`, lines 73-298)
**Apply to:** the new `opts` syntax + applicability validation — both MUST run before line 243 (`s.storage.Upload`). D-03/D-04 in CONTEXT.md are explicit about this ordering; it is the single most consistent architectural invariant in this file and every prior phase.
```go
	// Validate the conversion pair BEFORE writing anything to storage, and
	// derive the engine class in the same step (D-01/D-02).
	engine, ok := convert.Default.EngineFor(detected, target)
	if !ok {
		writeError(w, http.StatusUnprocessableEntity,
			"unsupported conversion: "+detected+" -> "+target)
		return
	}
	// <-- opts applicability validation belongs HERE, per D-04: engine/detected/target are now known
```

### Closed allow-list / never-passthrough for engine CLI arguments
**Source:** `internal/convert/libreoffice.go`'s `filterTable` (lines 108-122) and `internal/api/callbackurl.go`'s `isBlockedIP` allow/deny logic
**Apply to:** `internal/convert/opts.go` (new) and the PDF/A filter-options builder in `libreoffice.go`
```go
var filterTable = map[[2]string]string{
	{"docx", "pdf"}: "writer_pdf_Export",
	...
}
```
Never marshal the client-controlled `map[string]any` directly; always look up a validated struct field against a server-side constant table (Pitfall 9, D-07 — this is the phase's single highest-severity constraint).

### Same-commit terminal-signature coupling
**Source:** `internal/worker/worker.go:38-57` (comment + `terminalLibreOfficeSignatures`), inherited from Phase 13's D-03/D-04
**Apply to:** any new deterministic validator error string added to `validateDocumentOutput` in this phase
```go
var terminalLibreOfficeSignatures = []string{
	"output missing %pdf- magic bytes",
	"output is empty",
	"no export filter for",
	"output does not match expected container format",
	"produced no output file",
	// new PDF/A OutputIntent-missing signature goes here, SAME commit as the validator (D-06)
}
```

### `fmt.Errorf("<pkg-prefix>: <action>: %w", err)` error wrapping
**Source:** consistent across `internal/convert/libreoffice.go` (`"libreoffice: "` prefix throughout), `internal/jobs/repo.go` (`"insert job: %w"`, `"marshal transition detail: %w"`, etc.)
**Apply to:** every new error path in `opts.go`, `libreoffice.go`, `repo.go`

### Strict/lenient validation split between API and worker (D-10)
**Source:** established nowhere yet as literally as this phase needs it, but the closest existing precedent is `queue.ParseConvertPayload`'s "unparseable → `asynq.SkipRetry`" idiom (`internal/worker/worker.go:177-181`) — the worker treats malformed input as terminal, never as a second validation authority
**Apply to:** `internal/worker/worker.go`'s opts re-parse — strict-parse only (`DisallowUnknownFields`), never re-run the applicability/enum-value checks that live solely in `internal/api`/`internal/convert`

## No Analog Found

None — every file in this phase's touch-point list already exists and is modified in place following its own established local conventions; the one genuinely new file (`internal/convert/opts.go`, suggested location) has a direct structural analog in `internal/convert/olecfb.go` (same package, same single-purpose-file convention, same "closed check, fail-closed" spirit).

## Metadata

**Analog search scope:** `internal/api/`, `internal/convert/`, `internal/jobs/`, `internal/worker/`, `internal/e2e/`, `internal/queue/` (confirmed `ConvertPayload` carries only `job_id`, no opts-through-queue path — matches D-08/discretion note)
**Files scanned:** `internal/api/handlers.go`, `internal/api/callbackurl.go`, `internal/api/handlers_test.go`, `internal/convert/convert.go`, `internal/convert/libreoffice.go`, `internal/convert/libreoffice_test.go`, `internal/convert/olecfb.go`, `internal/convert/docsniff.go`, `internal/convert/exec.go`, `internal/jobs/jobs.go`, `internal/jobs/repo.go`, `internal/jobs/repo_test.go`, `internal/worker/worker.go`, `internal/queue/queue.go`, `internal/e2e/e2e_test.go`, `internal/db/migrations/0001_init.sql`
**Pattern extraction date:** 2026-07-10
```
