# Testing Patterns

**Analysis Date:** 2026-07-02

## Test Framework

**Runner:**
- Go standard library `testing` package only — no third-party test framework (no testify, no ginkgo). Go 1.26.4 (`go.mod`).
- No test config file; behavior is stock `go test`.

**Assertion Library:**
- None. Assertions are plain `if` checks followed by `t.Fatalf` / `t.Errorf` / `t.Error` with formatted messages including "got vs want".

**Run Commands:**
```bash
go test ./...                    # Run all tests (unit tests only; integration tests self-skip without env vars)
go test ./... -run TestName      # Run a specific test
go test ./... -v                 # Verbose output
go test ./... -cover             # Coverage summary
```
There is no Makefile or CI workflow wrapping these commands — `go test ./...` is run directly.

## Test File Organization

**Location:**
- Co-located with the code under test, standard Go convention: `internal/api/handlers_test.go` next to `internal/api/handlers.go`, `internal/jobs/repo_test.go` next to `internal/jobs/repo.go`, `internal/queue/queue_test.go` next to `internal/queue/*.go`, `internal/convert/convert_test.go` next to `internal/convert/*.go`, `internal/storage/storage_test.go` next to `internal/storage/storage.go`.

**Naming:**
- `<source_file>_test.go`, same package name (internal test, no `_test` package suffix used anywhere) — e.g. `package api` in both `handlers.go` and `handlers_test.go`.
- Test functions: `Test<FunctionOrScenario>` — `TestCreateJob_OK`, `TestCreateJob_UnsupportedPair`, `TestCreateJob_TooLarge`, `TestGetJob_DonePresigned`, `TestGetJob_NotFound` (`internal/api/handlers_test.go`). Underscore separates the target and the scenario: `TestJobLifecycle`, `TestMarkFailed`, `TestGetNotFound` (`internal/jobs/repo_test.go`).

**Structure:**
```
internal/
  api/
    handlers.go
    handlers_test.go     # unit tests, in-process fakes, no external deps
  jobs/
    repo.go
    repo_test.go          # integration tests, requires live Postgres (DATABASE_URL)
  queue/
    queue.go
    client.go
    queue_test.go          # one unit test + one integration test (requires REDIS_ADDR)
  convert/
    convert.go
    exec.go
    libvips.go
    convert_test.go        # unit tests, exercises real `sleep`/`false` binaries for exec hardening
  storage/
    storage.go
    storage_test.go        # integration test, requires live S3/MinIO (S3_ENDPOINT)
```

No `internal/worker` or `internal/db` test files exist yet — worker orchestration and the migration runner are currently untested (see Coverage below).

## Test Structure

**Suite Organization:**
No `TestMain`, no external suite runner, no subtests (`t.Run`) in use anywhere — each scenario is its own top-level `Test...` function. Example from `internal/api/handlers_test.go:94-119`:
```go
func TestCreateJob_OK(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	q := &fakeQueue{}
	srv := newTestServer(repo, store, q)

	body, ct := multipartBody(t, "in.png", "webp", []byte("fakepng"))
	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	...
}
```

**Patterns:**
- Setup: construct fakes inline at the top of each test, then a `newTestServer(...)` / `newTestRepo(t)` helper wires them
- No teardown needed for unit tests (fakes are in-memory); integration tests register cleanup via `t.Cleanup(pool.Close)` (`internal/jobs/repo_test.go:25`)
- Assertion style: `if <condition-that-should-not-hold> { t.Fatalf(...) }` — `Fatalf` stops the test on structural failures (e.g. wrong HTTP status), `Errorf`/`Error` used when subsequent checks in the same test still provide useful signal

## Mocking

**Framework:** None — no gomock, testify/mock, or code-generated mocks. Hand-written fakes implementing the small consumer-defined interfaces from `internal/api/api.go` (`Repo`, `Storage`, `Enqueuer`).

**Patterns:**
```go
// internal/api/handlers_test.go:21-45
type fakeRepo struct {
	created   *jobs.CreateParams
	getJob    *jobs.Job
	getErr    error
	outputs   []jobs.Output
	createdID uuid.UUID
	createErr error
}

func (f *fakeRepo) Create(_ context.Context, p jobs.CreateParams) (uuid.UUID, error) {
	f.created = &p
	if f.createErr != nil {
		return uuid.Nil, f.createErr
	}
	if f.createdID == uuid.Nil {
		f.createdID = uuid.New()
	}
	return f.createdID, nil
}
```
Fakes are plain structs with exported/unexported fields set before the call to control behavior (e.g. `getErr`, `createErr`) and fields read after the call to assert what happened (e.g. `created`, `uploaded`, `enqueued`). Unused parameters use `_` (`func (f *fakeRepo) Get(_ context.Context, _ uuid.UUID) ...`).

**What to Mock:**
- The three consumer-defined interfaces at the API boundary (`Repo`, `Storage`, `Enqueuer`) are always faked in `internal/api` tests, so HTTP handler tests run with zero external dependencies (no DB, no S3, no Redis) and complete instantly.

**What NOT to Mock:**
- Real infrastructure is exercised directly (not mocked) in package-level integration tests one layer down: `internal/jobs` talks to a real Postgres via `db.Connect`/`db.Migrate`, `internal/storage` talks to real MinIO/S3, `internal/queue` talks to real Redis via asynq's `Inspector`. There is no fake Postgres/S3/Redis anywhere in the repo — correctness of the actual client wiring is verified against the real service, gated by env-var skip (see below).
- `internal/convert` exercises real OS processes (`sleep`, `false`) to test the hardened exec wrapper's timeout/kill behavior rather than mocking `os/exec` (`internal/convert/convert_test.go:48-78`).

## Fixtures and Factories

**Test Data:**
No fixture files or factory libraries. Test data is constructed inline as Go literals per test, e.g.:
```go
// internal/jobs/repo_test.go:33-46
id, err := r.Create(ctx, CreateParams{
	Operation:    "convert",
	Engine:       "image",
	SourceFormat: "png",
	TargetFormat: "webp",
	Input: Input{
		Ordinal:     0,
		ObjectKey:   "uploads/x/0-in.png",
		Filename:    "in.png",
		Format:      "png",
		SizeBytes:   1234,
		ContentType: "image/png",
	},
})
```
Multipart HTTP bodies are built with a small local helper, not a fixture file:
```go
// internal/api/handlers_test.go:70-86
func multipartBody(t *testing.T, filename, target string, data []byte) (*bytes.Buffer, string) { ... }
```

**Location:** Inline in each `_test.go` file; no shared `testdata/` directory exists in the repo.

## Coverage

**Requirements:** None enforced — no coverage threshold, no CI gate.

**View Coverage:**
```bash
go test ./... -cover
go test ./... -coverprofile=coverage.out && go tool cover -html=coverage.out
```

**Current gaps:**
- `internal/worker` (task handler orchestration: download → convert → upload → mark done/failed) has no test file at all — the highest-value untested unit given it's the core pipeline.
- `internal/db` (migration runner) has no test file.
- `cmd/api`, `cmd/worker`, `cmd/migrate` have no tests (acceptable — thin wiring only).

## Test Types

**Unit Tests:**
- `internal/api/handlers_test.go`: full HTTP-handler behavior via `httptest`, all dependencies faked — true unit tests, always run.
- `internal/convert/convert_test.go`: `NormalizeFormat`, registry `Supports`/`Lookup`, and the `runCommand` process-hardening logic — always run (uses real `sleep`/`false` binaries via `exec.LookPath` guard, self-skips if unavailable, `internal/convert/convert_test.go:49,72`).
- `internal/queue/queue_test.go` `TestConvertPayloadRoundTrip`: pure marshal/unmarshal round-trip, no I/O — always run.

**Integration Tests:**
- `internal/jobs/repo_test.go`: full job lifecycle against a real Postgres (`TestJobLifecycle`, `TestMarkFailed`, `TestGetNotFound`). Skipped via `t.Skip("DATABASE_URL not set; skipping integration test")` when `DATABASE_URL` is unset (`internal/jobs/repo_test.go:14-16`). When run, calls `db.Migrate` first to ensure schema exists.
- `internal/storage/storage_test.go` `TestRoundTrip`: upload/download/presign against real MinIO/S3. Skipped when `S3_ENDPOINT` is unset (`internal/storage/storage_test.go:18-20`).
- `internal/queue/queue_test.go` `TestEnqueueImageConvert`: enqueues via a real `asynq.Client` and verifies via `asynq.Inspector` against real Redis. Skipped when `REDIS_ADDR` is unset (`internal/queue/queue_test.go:33-35`), and cleans up the task it created (`insp.DeleteTask`, `internal/queue/queue_test.go:65`).
- **Convention:** every integration test opens with `if os.Getenv("<VAR>") == "" { t.Skip(...) }` as the very first statement, so `go test ./...` is always safe to run without infrastructure — it just skips those tests silently rather than failing.

**E2E Tests:** Not used — no end-to-end test harness spanning API + worker + real queue/storage/DB in one test.

## Common Patterns

**Async/Time-bound Testing:**
```go
// internal/convert/convert_test.go:48-68 — verifying context-deadline cancellation kills the child process promptly
func TestRunCommandTimeout(t *testing.T) {
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

**Error Testing:**
```go
// internal/jobs/repo_test.go:128-133 — asserting a specific sentinel error
func TestGetNotFound(t *testing.T) {
	r := newTestRepo(t)
	if _, err := r.Get(context.Background(), uuid.New()); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
```
Guarded state-transition failure is tested by calling the same transition twice and expecting the second to error (`internal/jobs/repo_test.go:59-65`):
```go
if err := r.MarkActive(ctx, id); err != nil {
	t.Fatalf("MarkActive: %v", err)
}
// Re-activating a non-queued job must fail (guard).
if err := r.MarkActive(ctx, id); err == nil {
	t.Fatal("expected illegal transition on second MarkActive")
}
```

**HTTP Handler Testing:**
Route through the real `chi.Router` returned by `srv.Routes()` rather than calling handler methods directly, so routing/middleware is exercised too:
```go
// internal/api/handlers_test.go:101-105
req := httptest.NewRequest(http.MethodPost, "/v1/jobs", body)
req.Header.Set("Content-Type", ct)
rec := httptest.NewRecorder()
srv.Routes().ServeHTTP(rec, req)
```

---

*Testing analysis: 2026-07-02*
