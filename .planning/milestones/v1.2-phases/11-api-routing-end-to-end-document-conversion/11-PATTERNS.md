# Phase 11: API Routing & End-to-End Document Conversion - Pattern Map

**Mapped:** 2026-07-09
**Files analyzed:** 7 (5 modify, 1 modify-test, 1 new E2E test package)
**Analogs found:** 7 / 7

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `internal/convert/convert.go` | model/interface+registry | transform (lookup table) | itself (extend existing `Lookup`/`Supports` shape) | exact (self-extension) |
| `internal/convert/libvips.go` | service (converter engine) | transform | `internal/convert/libreoffice.go` (sibling converter, already implements `Pairs()`) | exact |
| `internal/convert/libreoffice.go` | service (converter engine) | transform | `internal/convert/libvips.go` (sibling converter) | exact |
| `internal/api/api.go` | config/interfaces (dependency contracts) | request-response | itself (extend existing `Enqueuer` interface) + `internal/reconciler/reconciler.go`'s `enqueuer` interface (already has both methods) | exact |
| `internal/api/handlers.go` | controller | request-response (CRUD-ish: create job) | itself (`handleCreateJob`) + `internal/reconciler/reconciler.go`'s `sweep()` engine-switch (lines 131-149) | exact |
| `internal/api/handlers_test.go` | test | request-response | itself (existing `fakeQueue`, `TestCreateJob_DocumentDetectedAndAccepted`, `TestCreateJob_ODFDetectedAndAccepted`) | exact |
| New E2E test (location TBD by planner, e.g. `internal/e2e/e2e_test.go`) | test (E2E, HTTP) | request-response + polling | `internal/queue/queue_test.go` (env-gated integration test) + `internal/webhook/deliver_test.go` (`httptest.Server` receiver) + `internal/jobs/repo_test.go` (env-gated skip convention) | role-match (composite — no true E2E test exists yet) |

## Pattern Assignments

### `internal/convert/convert.go` (interface + registry, MODIFY)

**Analog:** itself — `Converter` interface and `Registry.Lookup`/`Supports` (this IS the shape `Engine()`/`EngineFor` must mirror, per CONTEXT.md D-01/D-02).

**Current interface** (lines 17-25):
```go
// Converter turns a file in one format into another by shelling out to an
// external engine. inPath and outPath are local filesystem paths; the output
// format is implied by outPath's extension.
type Converter interface {
	// Pairs reports the format pairs this converter can handle.
	Pairs() []Pair
	// Convert reads inPath and writes the converted result to outPath.
	Convert(ctx context.Context, inPath, outPath string, opts map[string]any) error
}
```
Add a third method, following the exact one-line doc-comment-per-method style:
```go
	// Engine reports the engine class this converter belongs to (e.g.
	// "image", "document") — the single source of truth for queue/worker
	// routing decisions (D-01).
	Engine() string
```

**Lookup/Supports pattern to mirror for `EngineFor`** (lines 59-69):
```go
// Lookup finds the converter for a (from, to) pair, normalizing inputs.
func (r *Registry) Lookup(from, to string) (Converter, bool) {
	c, ok := r.m[Pair{From: NormalizeFormat(from), To: NormalizeFormat(to)}]
	return c, ok
}

// Supports reports whether a (from, to) pair is convertible.
func (r *Registry) Supports(from, to string) bool {
	_, ok := r.Lookup(from, to)
	return ok
}
```
`EngineFor` is a thin wrapper in the exact same style — `(string, bool)` shape mirroring `Lookup`'s `(Converter, bool)`:
```go
// EngineFor reports the engine class for a (from, to) pair, wrapping Lookup
// (D-02).
func (r *Registry) EngineFor(from, to string) (string, bool) {
	c, ok := r.Lookup(from, to)
	if !ok {
		return "", false
	}
	return c.Engine(), true
}
```

**Package doc comment** (line 1-2, unchanged — still accurate, mentions libvips only; consider whether to update to mention both engines, planner's call, not required by CONTEXT.md).

---

### `internal/convert/libvips.go` (converter, MODIFY)

**Analog:** `internal/convert/libreoffice.go` (sibling converter — read together, they define the exact two-implementation contract this method extends).

**Existing struct + Pairs()** (lines 11-26):
```go
// LibvipsConverter converts raster images by shelling out to the `vips` CLI.
// The output format is selected by outPath's extension (e.g. out.webp).
type LibvipsConverter struct{}

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
Add, mirroring the one-line-doc-comment-starting-with-identifier convention:
```go
// Engine reports the engine class this converter belongs to (D-01).
func (LibvipsConverter) Engine() string { return "image" }
```

---

### `internal/convert/libreoffice.go` (converter, MODIFY)

**Analog:** `internal/convert/libvips.go` (sibling converter).

**Existing struct + Pairs()** (lines 16-27):
```go
// LibreOfficeConverter converts office documents to PDF by shelling out to
// the `soffice` CLI in headless mode.
type LibreOfficeConverter struct{}

// Pairs returns one {format, "pdf"} pair per supported source document format.
func (LibreOfficeConverter) Pairs() []Pair {
	pairs := make([]Pair, 0, len(documentFormats))
	for _, f := range documentFormats {
		pairs = append(pairs, Pair{From: f, To: "pdf"})
	}
	return pairs
}
```
Add:
```go
// Engine reports the engine class this converter belongs to (D-01).
func (LibreOfficeConverter) Engine() string { return "document" }
```

**Constant strings note:** the project convention (per CLAUDE.md "String constants for enum-like values") uses untyped string consts for status values (`StatusQueued` etc. in `internal/jobs/jobs.go`). `"image"`/`"document"` engine-class strings already appear as bare literals throughout `internal/reconciler/reconciler.go` (`case "image":`, `case "document":`) and `internal/api/handlers.go` (`engineImage = "image"` const, line 27). Follow the existing `engineImage` const pattern in `internal/api/handlers.go` — planner should likely add a sibling `engineDocument = "document"` const there rather than introduce typed enums.

---

### `internal/api/api.go` (interfaces, MODIFY)

**Analog:** itself (extend `Enqueuer`) + `internal/reconciler/reconciler.go`'s `enqueuer` interface, which is the exact prior instance of this same extension (Phase 10 added `EnqueueDocumentConvert` there).

**Current Enqueuer** (lines 29-32):
```go
// Enqueuer dispatches image conversion work.
type Enqueuer interface {
	EnqueueImageConvert(ctx context.Context, jobID uuid.UUID) error
}
```

**Mirror target — reconciler's already-extended interface** (`internal/reconciler/reconciler.go` lines 41-46):
```go
// enqueuer is the subset of *queue.Client the sweeper depends on.
type enqueuer interface {
	EnqueueImageConvert(ctx context.Context, id uuid.UUID) error
	EnqueueWebhookDeliver(ctx context.Context, id uuid.UUID) error
	EnqueueDocumentConvert(ctx context.Context, id uuid.UUID) error
}
```
Apply the same addition to `api.Enqueuer` (doc comment should be updated too, since it currently says "dispatches image conversion work" — now dispatches both):
```go
// Enqueuer dispatches conversion work to the appropriate engine-class queue.
type Enqueuer interface {
	EnqueueImageConvert(ctx context.Context, jobID uuid.UUID) error
	EnqueueDocumentConvert(ctx context.Context, jobID uuid.UUID) error
}
```
`*queue.Client` (`internal/queue/client.go` lines 100-109) already implements `EnqueueDocumentConvert` — no queue-package change needed, this is purely a consumer-side interface widening.

---

### `internal/api/handlers.go` (controller, MODIFY)

**Analog:** itself (`handleCreateJob`) — the exact call sites to change are named in CONTEXT.md. Secondary analog for the engine-switch shape: `internal/reconciler/reconciler.go` `sweep()` (lines 131-149), which already branches on `j.Engine` for `image`/`document`/`default` with a fail-closed default case.

**Current hardcoded engine constant** (lines 23-29):
```go
const (
	formFieldFile        = "file"
	formFieldTarget      = "target"
	formFieldCallbackURL = "callback_url"
	engineImage          = "image"
	operationConv        = "convert"
)
```

**Current hardcoded call sites to replace** (lines 181-185, 237-263):
```go
	// Validate the conversion pair BEFORE writing anything to storage. The
	// DETECTED format (not the extension-derived one) is the source of truth
	// fed into the pair-check (D-05).
	if !convert.Default.Supports(detected, target) {
		writeError(w, http.StatusUnprocessableEntity,
			"unsupported conversion: "+detected+" -> "+target)
		return
	}
	...
	createdID, err := s.repo.Create(ctx, jobs.CreateParams{
		ID:           jobID,
		ClientID:     client.ID,
		Operation:    operationConv,
		Engine:       engineImage,               // <-- hardcoded, D-02 target
		SourceFormat: detected,
		TargetFormat: target,
		CallbackURL:  callbackURL,
		Input: jobs.Input{ ... },
	})
	...
	if err := s.queue.EnqueueImageConvert(ctx, createdID); err != nil {  // <-- hardcoded, D-02 target
		writeError(w, http.StatusInternalServerError, "failed to enqueue job")
		return
	}
```
D-02's intended replacement: `Supports` check becomes an `EngineFor` lookup (both the pair-validity check AND the engine decision come from one call), then the engine string drives both `CreateParams.Engine` and the enqueue call — mirroring reconciler's `switch j.Engine { case "image": ...; case "document": ...; default: fail closed }` shape (`internal/reconciler/reconciler.go` lines 131-149) for the enqueue dispatch:
```go
	engine, ok := convert.Default.EngineFor(detected, target)
	if !ok {
		writeError(w, http.StatusUnprocessableEntity,
			"unsupported conversion: "+detected+" -> "+target)
		return
	}
	...
	createdID, err := s.repo.Create(ctx, jobs.CreateParams{
		...
		Engine: engine,
		...
	})
	...
	switch engine {
	case engineImage:
		err = s.queue.EnqueueImageConvert(ctx, createdID)
	case engineDocument:
		err = s.queue.EnqueueDocumentConvert(ctx, createdID)
	default:
		// unreachable in practice (EngineFor only returns registered
		// converters' Engine() values) but fail closed rather than silently
		// drop the enqueue.
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to enqueue job")
		return
	}
```
(Exact structure — extra local var, switch vs if-chain, added `engineDocument` const — is the planner's/executor's call; the constraint from CONTEXT.md is only: use `EngineFor`, extend `Enqueuer`, and drive both `Engine:` and the enqueue call from its result.)

**HasDimensionLimit scoping — confirmed correct, no change needed** (lines 187-211, esp. line 195):
```go
	// VALID-03: reject a decompression-bomb-shaped upload (declared pixel
	// dimensions exceeding the configured limit) before any storage write.
	// ...
	// HasDimensionLimit scopes this to image formats only — pixel dimensions
	// are not a document concept, so documents skip the block entirely
	// ...
	if convert.HasDimensionLimit(detected) {
		width, height, dimRest, err := convert.Dimensions(detected, rest)
		...
	}
```
`HasDimensionLimit` (`internal/convert/dimensions.go` lines 76-79) checks membership in `dimensionParsers`, a closed map of exactly `{png, jpg, webp, heic, tiff}` — no document format is a key, so `HasDimensionLimit("docx")` etc. is always `false`. D-06 requires only a new **test** locking this in (see handlers_test.go section below), not a code change here.

---

### `internal/api/handlers_test.go` (test, MODIFY)

**Analog:** itself. `fakeQueue` must grow a second method to satisfy the widened `Enqueuer` interface; `TestCreateJob_DocumentDetectedAndAccepted` / `TestCreateJob_ODFDetectedAndAccepted` must be updated (their doc comments explicitly say "as of Phase 9... via the existing, still-single EnqueueImageConvert call... document-specific queue routing is Phase 10/11's job" — this phase is exactly that job, so these tests' assertions and comments are now stale and must be corrected); and D-06 needs a new focused test.

**Current fakeQueue** (lines 72-77):
```go
type fakeQueue struct{ enqueued uuid.UUID }

func (f *fakeQueue) EnqueueImageConvert(_ context.Context, id uuid.UUID) error {
	f.enqueued = id
	return nil
}
```
Must extend to track which queue was used, e.g.:
```go
type fakeQueue struct {
	enqueuedImage    uuid.UUID
	enqueuedDocument uuid.UUID
}

func (f *fakeQueue) EnqueueImageConvert(_ context.Context, id uuid.UUID) error {
	f.enqueuedImage = id
	return nil
}
func (f *fakeQueue) EnqueueDocumentConvert(_ context.Context, id uuid.UUID) error {
	f.enqueuedDocument = id
	return nil
}
```
(Field naming/shape is executor's call — the constraint is only that `fakeQueue` must implement both interface methods or the package won't compile once `api.Enqueuer` is widened.)

**Stale assertions to fix** (lines 537-539, 571-573 — both currently assert the OLD single-queue behavior with an explicit comment saying this is "Phase 10/11's job"):
```go
	if queue.enqueued != repo.createdID {
		t.Error("must enqueue the created job (via the existing single EnqueueImageConvert path -- document-specific queue routing is Phase 10/11)")
	}
```
Now must assert `queue.enqueuedDocument == repo.createdID` (and `queue.enqueuedImage == uuid.Nil`) for `TestCreateJob_DocumentDetectedAndAccepted` / `TestCreateJob_ODFDetectedAndAccepted`, and the stale doc comments on both tests (lines 500-511, 542-545) should be updated to drop the "transitional behavior" framing.

**Existing image-path assertion to preserve as the counter-case** (`TestCreateJob_OK`, line 337):
```go
	if q.enqueued != repo.createdID {
		t.Errorf("enqueued %s, want %s", q.enqueued, repo.createdID)
	}
```
This becomes `q.enqueuedImage != repo.createdID` (and should additionally assert `q.enqueuedDocument == uuid.Nil`) once `fakeQueue` is split — this is the regression guard that an image upload never touches the document queue.

**New D-06 test — pattern to follow** (mirrors `TestCreateJob_DimensionLimitExceeded`'s shape at lines 445-467, but asserting acceptance instead of rejection, and specifically proving the dimension-parser code path is never reached for a document): construct a `docxFixture(t)` (already exists, lines 171-181) with a target `Config{MaxImagePixels: <tiny>}` that would reject ANY image (even a 1x1), then assert the docx job is still accepted (202) — proving `HasDimensionLimit` genuinely short-circuits for documents rather than accidentally passing due to a generous limit. Example shape:
```go
func TestCreateJob_DocumentSkipsDimensionCheck(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	resolver := newFakeResolver()
	srv := NewServer(repo, store, &fakeQueue{}, resolver, healthyDeps(), Config{
		MaxUploadBytes:  1 << 20,
		MaxImagePixels:  1, // would reject any real image at all
	})

	body, ct := multipartBody(t, "in.docx", "pdf", docxFixture(t))
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (document must skip the pixel-dimension check entirely); body=%s", rec.Code, rec.Body.String())
	}
}
```

---

### New E2E test package (CREATE — location per D-03, planner's call)

**Analogs (composite — no single existing analog; combine three patterns):**

1. **Env-gated skip convention** — `internal/queue/queue_test.go` lines 279-282:
```go
func TestEnqueueImageConvert(t *testing.T) {
	if os.Getenv("REDIS_ADDR") == "" {
		t.Skip("REDIS_ADDR not set; skipping integration test")
	}
	...
}
```
Same shape applies to the new E2E test's gating var (e.g. `E2E_BASE_URL`): `if os.Getenv("E2E_BASE_URL") == "" { t.Skip(...) }` as the very first statement (`.planning/codebase/TESTING.md` "every integration test opens with `if os.Getenv(\"<VAR>\") == \"\" { t.Skip(...) }` as the very first statement").

2. **httptest.Server webhook receiver** — `internal/webhook/deliver_test.go` lines 21-32:
```go
srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	gotContentType = r.Header.Get("Content-Type")
	gotSignature = r.Header.Get("X-OctoConv-Signature")
	gotTimestamp = r.Header.Get("X-OctoConv-Timestamp")
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	gotBody = b
	w.WriteHeader(http.StatusOK)
}))
defer srv.Close()
```
D-05 needs an equivalent local `httptest.Server`, but it must be reachable from the containerized worker (not `localhost`-only) if the API/worker run inside docker-compose — the E2E test's `callback_url` needs a host-reachable address (e.g. bind explicitly and pass the Docker host's address/`host.docker.internal`, or run the receiver so the containers can reach it). This is a networking detail the planner/executor must resolve; call out in the plan as a known risk.

3. **Real HTTP client hitting a live API + polling loop** — no existing analog does HTTP polling; nearest precedent is the guarded-transition retry idiom (`.planning/codebase/TESTING.md` "Async/Time-bound Testing", `internal/convert/convert_test.go` lines 48-68) for the general shape of "act, then assert with a bounded wait," adapted to poll `GET /v1/jobs/{id}` on an interval until `status == "done"` or a deadline, e.g.:
```go
func pollUntilDone(t *testing.T, baseURL, apiKey string, jobID string, timeout time.Duration) map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/v1/jobs/"+jobID, nil)
		req.Header.Set("Authorization", "ApiKey "+apiKey)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET job: %v", err)
		}
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if body["status"] == "done" || body["status"] == "failed" {
			return body
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("job %s did not reach a terminal state within %v", jobID, timeout)
	return nil
}
```

4. **Auth provisioning for the E2E client** — the test needs a valid client + API key against the running stack's real Postgres. Closest existing pattern: `cmd/manage-clients/main.go` `create` subcommand (lines 44-57) does exactly this via `auth.GenerateKey()` + `auth.HashKey(salt, raw)` + `clients.Repo.Create`. The E2E test can either (a) shell out to the already-built `manage-clients` binary/image against the compose stack, or (b) connect directly to Postgres via `db.Connect`/`clients.NewRepo` and replicate the three-line provisioning sequence in-test (mirroring `internal/jobs/repo_test.go`'s pattern of connecting directly to a live `DATABASE_URL`). Concrete provisioning snippet to copy:
```go
raw, err := auth.GenerateKey()
...
hash := auth.HashKey(salt, raw) // salt = os.Getenv("API_KEY_SALT"), must match the running API's value
id, err := repo.Create(ctx, "e2e-test-client", hash)
```

5. **Format-pair table-driven coverage (D-04)** — no existing table-driven test in this codebase uses `t.Run` (`TESTING.md`: "no subtests (`t.Run`) in use anywhere"), so either follow that convention literally (6 separate top-level `Test...` functions, one per pair, matching `TestFilterFor`'s map-driven-but-non-subtest shape in `internal/convert/libreoffice_test.go` lines 14-24) or introduce `t.Run` as a deliberate, called-out deviation for this first E2E suite — planner's call, but note the deviation from established convention either way.

**Fixture strategy reference** — `internal/api/handlers_test.go`'s `docxFixture`/`odtFixture` (lines 171-201) build minimal *structurally-valid-enough-to-pass-SniffContainer* zips, but those are NOT valid enough for `soffice` to actually render into a PDF (D-04 requires a REAL end-to-end conversion, not just acceptance). The E2E test needs genuinely openable docx/xlsx/pptx/odt/ods/odp fixtures — either checked-in minimal real files (`testdata/`, a convention that doesn't exist yet anywhere in this repo per `TESTING.md` "no shared `testdata/` directory exists") or generated at test time via a seed `soffice --convert-to` step (mirrors `TestLibreOfficeConverter_ConvertProducesValidPDF`'s txt→odt seeding trick in `internal/convert/libreoffice_test.go` lines 196-210). Explicitly left to planner/executor per CONTEXT.md's Claude's Discretion section.

## Shared Patterns

### Env-gated integration/E2E test skip
**Source:** `internal/queue/queue_test.go:279-282`, `internal/jobs/repo_test.go:16-17` (`if os.Getenv("DATABASE_URL") == "" { t.Skip(...) }`), `internal/storage/storage_test.go:18-20`
**Apply to:** The new E2E test file — first statement, one env var check per required piece of live infra (API base URL at minimum; possibly also API_KEY_SALT/DATABASE_URL depending on provisioning strategy chosen).
```go
if os.Getenv("E2E_BASE_URL") == "" {
	t.Skip("E2E_BASE_URL not set; skipping E2E test")
}
```

### Engine-class dispatch (image vs document)
**Source:** `internal/reconciler/reconciler.go:131-149` (the one place in the codebase that ALREADY branches on engine class for enqueue routing, added in Phase 10)
**Apply to:** `internal/api/handlers.go`'s new engine-aware routing in `handleCreateJob` — same two-case-plus-fail-closed-default shape, same "engine decides which Enqueue* method to call" logic, now duplicated (deliberately — no shared helper was introduced for the reconciler's version, D-01/D-02 keep this a straightforward per-call-site decision rather than introducing a queue-package-level dispatcher).
```go
switch j.Engine {
case "image":
	enqueueErr = s.enq.EnqueueImageConvert(ctx, j.ID)
case "document":
	enqueueErr = s.enq.EnqueueDocumentConvert(ctx, j.ID)
default:
	// fail closed — see reconciler.go's full comment for the reasoning
}
```

### Interface segregation for consumer-defined dependencies
**Source:** `internal/api/api.go:16-32` (`Repo`, `Storage`, `Enqueuer`), `internal/reconciler/reconciler.go:31-46` (`jobStore`, `enqueuer`)
**Apply to:** `internal/api/api.go`'s `Enqueuer` widening — each interface declares only the methods the consuming package actually calls; `*queue.Client` already satisfies the wider `Enqueuer` structurally (Go implicit interface satisfaction), no queue-package change required.

### Self-describing Converter (Pairs → Engine)
**Source:** `internal/convert/convert.go:20-25` (`Converter` interface), both concrete implementations
**Apply to:** `Engine()` extends the existing self-description idiom rather than introducing a parallel `map[string]string` engine-class lookup table — every new converter registers its engine class the same way it registers its pairs, at the type level, with zero central bookkeeping beyond `Registry.Register` iterating `Pairs()` (already does this) — `EngineFor` simply also reads `c.Engine()` after `Lookup` succeeds.

## No Analog Found

None — every file in scope has at least a role-match analog. The new E2E test package is the weakest match (composite of three existing patterns rather than a single direct analog), since `TESTING.md` explicitly documents "E2E Tests: Not used" as the pre-Phase-11 state.

## Metadata

**Analog search scope:** `internal/api/`, `internal/convert/`, `internal/queue/`, `internal/reconciler/`, `internal/webhook/`, `internal/jobs/`, `internal/clients/`, `internal/auth/`, `cmd/manage-clients/`, `.planning/codebase/TESTING.md`, `docker-compose.yml`
**Files scanned:** ~20 (all non-generated `.go` files under `internal/` and `cmd/`, plus `docker-compose.yml` and `TESTING.md`)
**Pattern extraction date:** 2026-07-09
