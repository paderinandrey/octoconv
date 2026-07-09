---
phase: 09-libreoffice-converter-engine
verified: 2026-07-09T15:40:00Z
status: passed
score: 3/3 must-haves verified
overrides_applied: 0
---

# Phase 9: LibreOffice Converter Engine Verification Report

**Phase Goal:** The worker can turn an accepted office document into a trustworthy PDF via LibreOffice headless, and never leaves an orphaned `soffice` process behind.
**Verified:** 2026-07-09T15:40:00Z
**Status:** passed
**Re-verification:** No — initial verification

## Verification Methodology

This phase's central claim (DOC-06, live process-kill proof) cannot be trusted from SUMMARY.md text alone. I independently re-ran the entire Docker-based live proof from scratch rather than trusting the captured output in 09-02-SUMMARY.md:

- Rebuilt `Dockerfile.worker` (`octoconv-worker:verify9`) and `Dockerfile.worker-test` (`octoconv-lo-test:verify9`) myself, in this session, from the current repo state.
- Ran `TestLibreOfficeConverter_TimeoutKillsRealProcess` and `TestLibreOfficeConverter_ConvertProducesValidPDF` **3 consecutive times** inside the freshly built test image (not reused from the executor's session) to check for flakiness — all 3 runs: both PASS, zero SKIP.
- Ran the full `./internal/convert/` suite (all 62+ tests) live inside the image — exit 0.
- Independently confirmed `tini` is wired as PID 1 in both images via `docker inspect` (Entrypoint) and a live `ps -eo pid,comm` check inside a running container (`PID 1 = tini`), rather than trusting the Dockerfile comment.
- Independently confirmed `soffice` is on `PATH` and `USER nobody` is active in the runtime image via `docker run --entrypoint which/id`.
- Ran an **additional, self-devised concurrency probe** (not present in the repo's test suite) — 4 simultaneous `soffice` conversions with 4 distinct `-env:UserInstallation` profile dirs inside the container — to independently corroborate Success Criterion 1's "concurrent jobs never collide" claim beyond what RESEARCH.md documented from the researcher's own session. All 4 produced valid, correctly-sized `%PDF-` output with zero lock errors.
- Ran `go build ./...`, `go vet ./...`, `gofmt -l`, and `go test ./...` locally (no Docker) to confirm the non-gated portions of the phase and the full repo test suite are green.
- Read every line of `internal/convert/libreoffice.go`, `libreoffice_test.go`, `converters.go`, `convert.go`, `exec.go`, `Dockerfile.worker`, `Dockerfile.worker-test` directly — did not rely on grep-only checks for code content.
- Confirmed all 6 commits referenced in both SUMMARYs (`5eb92b4`, `69731f9`, `02befac`, `41085ba`, `6f60c49`, `5174636`) exist in git history with content matching their stated purpose.

**What I took from SUMMARY.md without independent re-verification:** the timing/duration metrics and the narrative description of the debugging process that led to discovering the zombie-reaping bug (the *mechanism* of the fix — tini as PID 1 — was independently verified; the story of *how it was found* was not re-litigated).

## Goal Achievement

### Observable Truths (Roadmap Success Criteria)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Worker converts docx/xlsx/pptx/odt/ods/odp to PDF through LibreOffice headless, each job isolated via its own `-env:UserInstallation` profile so concurrent jobs never collide | VERIFIED | `internal/convert/libreoffice.go:34-68` — `Convert` derives `profileDir := filepath.Join(filepath.Dir(outPath), "lo-profile")`, unique per caller-supplied `outPath`; `Pairs()` registers all 6 documentFormats -> pdf; `filterFor` maps to the correct Writer/Calc/Impress export filter per format. Independently re-ran 4 concurrent `soffice` invocations inside `octoconv-lo-test:verify9` with 4 distinct profile dirs — all 4 succeeded with valid `%PDF-` output and zero lock errors (see Methodology). `convert.go`'s `Converter` interface is untouched (`git diff` shows no changes to `internal/convert/convert.go` in this phase's commits) — isolation was achieved purely by self-derivation, exactly as claimed. |
| 2 | Worker validates conversion output (non-zero size, valid `%PDF-` magic bytes) before marking a job `done`; invalid output is a terminal failure | VERIFIED | `internal/convert/libreoffice.go:93-114` (`validatePDF`) checks `os.Stat` size == 0 (error), then reads exactly `len(pdfMagic)`=5 bytes and compares via `bytes.Equal` against `%PDF-` (error if mismatched). Called as the final statement of `Convert` (`return validatePDF(outPath)`) — any invalid output propagates as a non-nil error, which the worker's existing `MarkFailed` path (unchanged, outside this phase's scope) treats as terminal. `TestValidatePDF` unit-tests all three branches (valid/empty/wrong-magic) — reran locally, PASS. |
| 3 | An integration test proves a timed-out conversion leaves zero surviving `soffice`/`soffice.bin` processes and children, not an assumption inherited from the image engine | VERIFIED (live re-run, not just SUMMARY-reported) | `TestLibreOfficeConverter_TimeoutKillsRealProcess` (`internal/convert/libreoffice_test.go:112-186`) drives `runCommand` directly (not `Convert`, avoiding the `filterFor(".txt")` short-circuit false-pass), polls `ps` for `soffice.bin` actually running before cancelling, then asserts zero `soffice`/`oosplash` survivors via a post-kill `ps` scan. I independently rebuilt both Docker images and ran this test **3 consecutive times** in a fresh container — all 3: `--- PASS (0.03s-0.14s)`, no `--- SKIP`. I also independently confirmed the underlying fix mechanism (`tini` as PID 1, reaping the zombie `soffice.bin` left behind after the group SIGKILL reparents it to PID 1) via `docker inspect` Entrypoint and a live in-container `ps` check showing `PID 1 = tini`. |

**Score:** 3/3 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/convert/libreoffice.go` | `LibreOfficeConverter` (Pairs, Convert), `filterFor`, `validatePDF` | VERIFIED | Exists, all symbols present, matches plan verbatim: `type LibreOfficeConverter struct{}`, `Convert(ctx context.Context, inPath, outPath string, _ map[string]any) error` (exact signature confirmed, `map[string]any` not `map[string]string`), `filterFor` with 3 correct filter names, `validatePDF` with size+magic checks. |
| `internal/convert/converters.go` | `Default.Register(LibreOfficeConverter{})` | VERIFIED | Line present, uncommented, immediately after `Default.Register(LibvipsConverter{})`. |
| `internal/convert/libreoffice_test.go` | Unit tests + soffice-gated live tests | VERIFIED | 5 tests present (`TestFilterFor`, `TestValidatePDF`, `TestRegistryLibreOfficePairs`, `TestLibreOfficeConverter_TimeoutKillsRealProcess`, `TestLibreOfficeConverter_ConvertProducesValidPDF`). Unit tests pass bare; gated tests skip cleanly locally and PASS live in Docker (independently re-run). |
| `Dockerfile.worker` | Runtime image with LibreOffice + fonts, `soffice` on PATH | VERIFIED | Single `RUN apt-get` line (`grep -c` = 1) includes all 3 `libreoffice-*-nogui` + 3 font packages + `tini`; `USER nobody` and `CGO_ENABLED=0` two-stage build preserved; independently rebuilt and confirmed `soffice` on PATH and `nobody` uid via `docker run`. |
| `Dockerfile.worker-test` | Go + LibreOffice + procps test harness | VERIFIED | `FROM golang:1.26-bookworm`, installs LibreOffice/font packages + `procps` + `tini` + `libvips-tools`, `CMD ["go","test","./internal/convert/","-v"]` under `tini` entrypoint. Independently rebuilt and ran. |

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| `internal/convert/libreoffice.go` | `runCommand` (`exec.go`) | soffice invocation with `-env:UserInstallation` | WIRED | `runCommand(ctx, "soffice", args...)` called with the full arg slice including `-env:UserInstallation=file://"+profileDir`. `exec.go` itself is byte-for-byte unmodified in this phase (confirmed via commit diffs — only `5eb92b4`/`69731f9`/`41085ba`/`6f60c49` touched files, none touch `exec.go`). |
| `internal/convert/libreoffice.go` | `validatePDF` | final step of `Convert` | WIRED | `return validatePDF(outPath)` is the literal last statement of `Convert`. |
| `internal/convert/converters.go` | `convert.Default` | `init()` registration | WIRED | Confirmed reachable: `TestRegistryLibreOfficePairs` (re-run locally, PASS) proves `Default.Supports("docx","pdf")` etc. resolve through the live registry, not just source-text presence. |
| `Dockerfile.worker` | `soffice` on PATH | apt-get install | WIRED | Independently confirmed via `docker run --entrypoint which octoconv-worker:verify9 soffice` -> `/usr/bin/soffice`. |
| `Dockerfile.worker` / `Dockerfile.worker-test` | `tini` as PID 1 | `ENTRYPOINT ["/usr/bin/tini", "--", ...]` | WIRED | Independently confirmed via `docker inspect` Entrypoint field and a live `ps -eo pid,comm` inside a running container showing `PID 1 = tini`. |

### Data-Flow Trace (Level 4)

Not applicable in the traditional sense (no UI/state rendering) — the phase's "data flow" equivalent is the process-execution/output-file pipeline, which is covered above (Key Link Verification: `runCommand` -> `validatePDF` -> return) and independently proven live via Docker rather than a static trace.

### Behavioral Spot-Checks / Live Docker Verification

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| `Dockerfile.worker` builds | `docker build -f Dockerfile.worker -t octoconv-worker:verify9 .` | Success, image built | PASS |
| `Dockerfile.worker-test` builds | `docker build -f Dockerfile.worker-test -t octoconv-lo-test:verify9 .` | Success, image built | PASS |
| `soffice` on PATH, `USER nobody` active | `docker run --entrypoint which/id octoconv-worker:verify9` | `/usr/bin/soffice`; `uid=65534(nobody)` | PASS |
| `tini` is PID 1 | `docker run --entrypoint /usr/bin/tini ... ps -eo pid,comm` | `1 tini` | PASS |
| DOC-06 kill test, run 1/3 | `docker run --rm octoconv-lo-test:verify9 go test ./internal/convert/ -run TestLibreOfficeConverter -v` | `--- PASS: TimeoutKillsRealProcess (0.14s)`, `--- PASS: ConvertProducesValidPDF (1.34s)`, exit 0 | PASS |
| DOC-06 kill test, run 2/3 | same | `--- PASS (0.04s)` / `--- PASS (1.07s)`, exit 0 | PASS |
| DOC-06 kill test, run 3/3 | same | `--- PASS (0.03s)` / `--- PASS (1.06s)`, exit 0 | PASS — no flakiness observed across 3 runs |
| Full `internal/convert` suite live | `docker run --rm octoconv-lo-test:verify9 go test ./internal/convert/ -v` | All tests PASS (62+), exit 0 | PASS |
| Concurrency isolation (self-devised, beyond repo's test suite) | 4 concurrent `soffice` conversions, 4 distinct `-env:UserInstallation` dirs, inside container | 4/4 produced valid `%PDF-` output, no lock errors | PASS |
| Local (non-Docker) unit tests | `go test ./internal/convert/ -run 'TestFilterFor\|TestValidatePDF\|TestRegistryLibreOfficePairs\|TestLibreOfficeConverter'` | 3 PASS, 2 SKIP (soffice absent locally, as expected) | PASS |
| Full repo build/vet/fmt/test | `go build ./... && go vet ./... && gofmt -l . && go test ./...` | build/vet/test all clean; `gofmt -l` flags only the pre-existing, out-of-scope `internal/queue/queue_test.go` | PASS |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| DOC-04 | 09-01, 09-02 | Worker converts documents to PDF via LibreOffice headless with per-job isolated profile | SATISFIED | `LibreOfficeConverter.Convert` + independently re-verified live conversion + concurrency probe |
| DOC-05 | 09-01, 09-02 | Worker validates conversion output (size + `%PDF-`) before `done`; invalid output is terminal | SATISFIED | `validatePDF`, unit-tested and live-tested |
| DOC-06 | 09-01, 09-02 | Hardened process-exec verified to kill real `soffice`/`soffice.bin` via integration test, not assumption | SATISFIED | `TestLibreOfficeConverter_TimeoutKillsRealProcess`, independently re-run 3x live in freshly built Docker image, 3/3 PASS |

No orphaned requirements found — REQUIREMENTS.md maps only DOC-04/05/06 to Phase 9, and all three appear in both plans' `requirements` frontmatter.

### Anti-Patterns Found

None. Scanned `internal/convert/libreoffice.go`, `libreoffice_test.go`, `converters.go`, `Dockerfile.worker`, `Dockerfile.worker-test` for `TBD`/`FIXME`/`XXX`/`TODO`/`HACK`/`PLACEHOLDER`/"not yet implemented"/empty-return stubs — zero matches.

One pre-existing, out-of-scope `gofmt` issue in `internal/queue/queue_test.go` (from Phase 6, commit `6af87c1`) was found by the executor and correctly logged to `deferred-items.md` rather than silently fixed or ignored — verified this file is genuinely untouched by any Phase 9 commit (`git show <phase-9-commits> --stat | grep queue` returns nothing) and the gofmt issue predates this phase. Not a Phase 9 blocker.

### Phase 8 Test Rename (Coherence Check)

Commit `e4406a5` (outside both Phase 9 plans, applied by the orchestrator per the task prompt) renamed `TestCreateJob_DocumentDetectedButUnsupported` -> `TestCreateJob_DocumentDetectedAndAccepted` and `TestCreateJob_ODFDetectedButUnsupported` -> `TestCreateJob_ODFDetectedAndAccepted`, flipping assertions from 422 to 202. Read the full diff: the rename is coherent with LibreOfficeConverter's registration in `convert.Default` (registering it makes `docx/odt -> pdf` genuinely `Supported`, so Phase 8's original 422-unsupported assertion is no longer true). The updated tests correctly assert 202 + upload + job creation + enqueue via the existing single image-queue path, with doc comments explicitly flagging this as transitional behavior pending Phase 10/11's dedicated document-queue routing. `go test ./...` confirms the full suite (including these renamed tests) is green. This is coherent, not a Phase 9 defect, per the task's explicit framing.

### Human Verification Required

None. This phase is backend/infrastructure-only (no UI, no user-facing flow) and its central claim (live process-kill guarantee) was independently re-executed against real Docker/soffice by this verifier rather than deferred to a human.

### Gaps Summary

No gaps found. All three roadmap Success Criteria are VERIFIED against the actual codebase and independently re-proven live in Docker (not merely trusted from SUMMARY.md). The `Converter` interface (`internal/convert/convert.go`) is confirmed unmodified — isolation is achieved by self-derivation exactly as the plan specified. `exec.go` is confirmed unmodified — LibreOffice reuses the existing hardened process-group-kill wrapper without any change to it. The `tini`-as-PID-1 fix documented in 09-02-SUMMARY.md as a real bug found-and-fixed during execution was independently corroborated at the Docker image level (not just trusted from the SUMMARY narrative), and the kill test was re-run 3 times fresh to rule out flakiness.

---

_Verified: 2026-07-09T15:40:00Z_
_Verifier: Claude (gsd-verifier)_
