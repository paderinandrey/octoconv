---
phase: 23-verapdf-validation
plan: 01
subsystem: infra
tags: [verapdf, jvm, docker, pdf-a, iso19005, libreoffice, measurement-gate]

# Dependency graph
requires:
  - phase: 09-document-conversion
    provides: Dockerfile.document-worker (LibreOffice soffice runtime), DOCUMENT_ENGINE_TIMEOUT convention
  - phase: 14-pdfa-export
    provides: internal/convert/opts.go PDFAFilterOptions / pdfaFilterOptionsSuffix, /GTS_PDFA sanity check (validateDocumentOutput)
provides:
  - veraPDF CLI (pinned verapdf/cli:v1.30.2) bundled into Dockerfile.document-worker via multi-stage COPY, Debian-JRE fallback path (path B)
  - Live-verified JVM cold-start measurement on a real PDF/A-2b export (nearest-rank p95 = 4650ms, GO against the 10s D-01 budget)
  - Verified veraPDF CLI contract (flags, exit codes, XML machine-report schema) for Plan 02 to consume
  - Committed compliant/non-compliant report + PDF fixtures for Plan 02 (parser unit tests) and Plan 03 (live terminal-fail gate)
affects: [23-02-verapdf-wiring, 23-03-verapdf-e2e]

# Tech tracking
tech-stack:
  added: ["verapdf/cli:v1.30.2 (Docker Hub, bundled as app jars only, path B)", "openjdk-17-jre-headless (Debian bookworm, glibc-native JRE for veraPDF)"]
  patterns: ["Measure-before-wire go/no-go gate (D-01) run against a fully-built production image, not a synthetic benchmark", "verapdf launch script auto-detects java via PATH -- no JAVA_HOME env needed"]

key-files:
  created:
    - scripts/verapdf-measure.sh
    - internal/convert/testdata/verapdf_compliant.mrr.xml
    - internal/convert/testdata/verapdf_noncompliant.mrr.xml
    - internal/convert/testdata/verapdf_noncompliant.pdf
  modified:
    - Dockerfile.document-worker
    - docker-compose.yml
    - .env.example

key-decisions:
  - "Pinned tag is verapdf/cli:v1.30.2 (leading 'v'), not '1.30.2' as researched in STACK.md -- verified live against Docker Hub, the bare tag does not exist"
  - "Path A (jlink-trimmed JRE COPY) FAILS live: the copied JRE is musl-linked (Alpine source image) and cannot resolve libc.musl-x86_64.so.1 under bookworm's glibc loader -- switched to documented fallback path B (apt-get openjdk-17-jre-headless + COPY only /opt/verapdf app jars)"
  - "verapdf/cli is amd64-only on Docker Hub (no arm64 manifest) -- pinned document-worker service to platform: linux/amd64 in docker-compose.yml; all measurement was run via OrbStack's amd64 emulation (Rosetta-class, not qemu) on an Apple Silicon host"
  - "Nearest-rank p95 = 4650ms over 10 runs -- well inside the D-01 10s budget -- GO verdict, no daemon-fallback re-plan needed"

requirements-completed: [PDFA-02]

# Metrics
duration: 45min
completed: 2026-07-13
---

# Phase 23 Plan 01: veraPDF Packaging + JVM Cold-Start Measurement Gate Summary

**veraPDF CLI bundled into Dockerfile.document-worker via a Debian-JRE fallback path (jlink JRE fails the glibc boundary live); measured JVM cold-start p95 = 4650ms over 10 real PDF/A-2b validations -- GO, 2.15x margin under the 10s D-01 budget.**

## Performance

- **Duration:** ~45 min
- **Started:** 2026-07-13T13:25:00+03:00 (approx.)
- **Completed:** 2026-07-13T14:14:16+03:00
- **Tasks:** 2 auto tasks completed + checkpoint reached (GO)
- **Files modified/created:** 7 (3 modified, 4 created)

## Accomplishments
- veraPDF CLI (pinned `verapdf/cli:v1.30.2`) bundled into `Dockerfile.document-worker`; `verapdf --version`/`--help` run successfully inside the built image
- Live-disproved the flagged musl->glibc risk for the jlink-trimmed JRE (path A) and implemented the documented Debian-JRE fallback (path B) instead
- Measured real JVM cold-start cost on a genuine LibreOffice PDF/A-2b export: nearest-rank p95 = 4650ms over 10 runs, well inside the 10s budget (D-01)
- Confirmed the Pitfall 9 regression canary: a real LibreOffice PDF/A-2b export validates `isCompliant="true"` under the pinned veraPDF
- Confirmed a plain (non-PDF/A) LibreOffice export is correctly flagged `isCompliant="false"`
- Captured and committed the exact veraPDF CLI contract (flags, exit codes, XML machine-report schema) for Plan 02
- Committed compliant/non-compliant `.mrr.xml` report fixtures and a non-compliant `.pdf` fixture for Plan 02 (offline parser unit tests) and Plan 03 (live terminal-fail e2e gate)

## Task Commits

1. **Task 1: Bundle veraPDF into Dockerfile.document-worker and verify java loads across the glibc boundary** - `5ff59e0` (feat)
2. **Task 2: Measure JVM cold-start cost, capture the CLI contract, and commit report + PDF fixtures** - `c83248e` (feat)

**Plan metadata:** (this commit, docs: complete plan)

## Files Created/Modified
- `Dockerfile.document-worker` - Added veraPDF stage (`FROM verapdf/cli:v1.30.2 AS verapdf`) and, in the runtime stage, `apt-get install openjdk-17-jre-headless` + `COPY --from=verapdf /opt/verapdf /opt/verapdf` + `PATH` extension (path B, glibc-native JRE)
- `docker-compose.yml` - `platform: linux/amd64` pin on `document-worker` (verapdf/cli has no arm64 manifest); added `VERAPDF_TIMEOUT: "60s"` env
- `.env.example` - Documented `VERAPDF_TIMEOUT=60s` alongside `DOCUMENT_ENGINE_TIMEOUT`
- `scripts/verapdf-measure.sh` - Builds the image, generates a real PDF/A-2b export via soffice (same FilterOptions suffix as `internal/convert/opts.go`), runs `verapdf -f 2b --format xml` 10x, computes nearest-rank p95, asserts the D-01 budget, generates + validates the non-compliant canary, captures fixtures, observes peak memory
- `internal/convert/testdata/verapdf_compliant.mrr.xml` - Real captured XML machine-readable report, `isCompliant="true"` (Plan 02 parser fixture)
- `internal/convert/testdata/verapdf_noncompliant.mrr.xml` - Real captured XML machine-readable report, `isCompliant="false"` (Plan 02 parser fixture)
- `internal/convert/testdata/verapdf_noncompliant.pdf` - Deliberately non-compliant PDF (plain soffice PDF export, no PDF/A FilterOptions) for Plan 03's live terminal-fail gate

## Decisions Made

- **Tag correction (Rule 3, blocking):** The plan's frontmatter and research (`STACK.md`) reference `verapdf/cli:1.30.2`. Live verification against the Docker Hub API (`hub.docker.com/v2/repositories/verapdf/cli/tags`) shows the actual tag is `v1.30.2` (leading "v") — `1.30.2` does not exist as a tag. Corrected in `Dockerfile.document-worker`.
- **glibc path B, not path A (plan-anticipated branch, not a deviation):** Live-tested path A per the plan's own risk flag. `ldd` on the copied `/opt/java/openjdk/bin/java` reported `libc.musl-x86_64.so.1 => not found` inside the Debian bookworm runtime stage, and running `verapdf --version` failed with OrbStack's dynamic-loader error ("Dynamic loader not found: /lib/ld-musl-x86_64.so.1"). This is exactly the risk STACK.md flagged (verapdf/cli's final stage is Alpine/musl; the jlink JRE it produces is musl-linked). Switched to the plan's documented path B: `apt-get install openjdk-17-jre-headless` (glibc-native JRE, resolved via PATH by veraPDF's Maven-appassembler-generated launch script with no `JAVA_HOME` needed) + `COPY --from=verapdf /opt/verapdf /opt/verapdf` (app jars only, `cli-1.30.2.jar`). Verified: `verapdf --version` and `--help` both succeed; conversions and validations run correctly (see measurement below).
- **Platform pin (Rule 3, blocking — required for the plan's own pinned dependency to function):** `verapdf/cli` publishes amd64-only images (no arm64 manifest, confirmed via Docker Hub API). The executing host is Apple Silicon (arm64). Without a platform pin, `docker build` on an arm64 host would build the final `debian:bookworm-slim` runtime stage natively for arm64 while `COPY --from=verapdf` would (in an amd64-emulated build) attempt to copy amd64 binaries into an arm64 image, which cannot work regardless of glibc/musl. Added `platform: linux/amd64` to `docker-compose.yml`'s `document-worker` service, and built/ran everything in this plan with `--platform linux/amd64` (OrbStack's amd64 emulation, Rosetta-class translation — not slower qemu-style instruction emulation). This is a necessary consequence of D-03's own pinned dependency choice, not an independent architectural decision; documented here since it constrains where this service can run natively without emulation.
- **Emulation caveat on the raw numbers:** The measured p95 (4650ms) was captured under OrbStack's amd64-on-arm64 emulation, not bare-metal amd64 Linux. Rosetta-class translation is materially faster than qemu-style emulation, so this measurement is a reasonable (if not perfectly calibrated) proxy for the real amd64 deployment target — and the 2.15x margin under budget (4650ms vs. 10000ms) leaves headroom that should absorb realistic native-vs-emulated variance. If the production deployment target is confirmed non-amd64 (e.g., arm64 Kubernetes nodes), this measurement should be re-run natively before final rollout, but that is outside this plan's scope (D-03 fixed the amd64-only pinned image as the accepted stack decision).

## Verified veraPDF CLI Contract (for Plan 02)

Captured against the pinned `verapdf/cli:v1.30.2` image (via `verapdf --help`):

- **Flavour selection:** `-f, --flavour` — Default `0` (auto-detect). Possible values: `[0, 1a, 1b, 2a, 2b, 2u, 3a, 3b, 3u, 4, 4f, 4e, ua1, ua2, wt1r, wt1a]`. Confirms D-06's `-f 2b` usage.
- **Machine-readable output format:** `--format` — Default `xml`. Possible values: `[raw, xml, text, html, json]`. **The MRR ("machine-readable report") flag value is literally `xml`, not a separate `mrr` value** — this differs from an assumption of a distinct `--format mrr`; use `--format xml` (or omit `--format`, since `xml` is already the default).
- **Report schema (real, captured verbatim in `internal/convert/testdata/verapdf_*.mrr.xml`):**
  - Root `<report>` → `<jobs><job><validationReport jobEndStatus="normal" profileName="..." statement="..." isCompliant="true|false"><details passedRules="N" failedRules="N" .../></validationReport></job></jobs>`
  - Non-compliant reports additionally include `<rule specification="ISO 19005-2:2011" clause="..." status="failed" ...><description>...</description><check status="failed"><errorMessage>...</errorMessage></check></rule>` entries under `<details>` — the parser should read the `isCompliant` attribute on `<validationReport>` as the authoritative compliance signal (matches D-06's fail-closed intent), and can optionally surface the first `<errorMessage>` for job_events (D-07).
  - `<batchSummary totalJobs="N" failedToParse="N" encrypted="N" outOfMemory="N" veraExceptions="N"><validationReports compliant="N" nonCompliant="N" failedJobs="N">` — `failedToParse`/`outOfMemory`/`veraExceptions`/`failedJobs` are the validator-error-vs-invalid-report signals worth checking before trusting `isCompliant` (D-06's "an unverifiable archival claim is a failed archival claim" — these counters, not just `isCompliant`, indicate a validator failure).
- **Exit codes (verified live, do NOT rely on these alone per the plan's own caution — the report content is authoritative, but the codes ARE meaningfully different in practice):**
  - `0` — compliant (`isCompliant="true"`)
  - `1` — non-compliant (`isCompliant="false"`)
  - (Validator/error exit codes for malformed/unparseable input were not exercised in this plan — Plan 02/03 should verify this case explicitly before relying on exit code alone for the error path, per D-06.)

## Measurement Results (D-01/D-02)

**Nearest-rank p95 method:** 10 per-run wall-clock durations (ms), sorted ascending, rank = ceil(0.95 × 10) = 10 (the slowest of the 10 runs).

Raw per-run wall-clock (ms), in execution order:
```
4650, 3771, 3502, 3807, 3715, 3953, 3683, 4529, 3864, 4446
```

Sorted ascending: `3502, 3683, 3715, 3771, 3807, 3864, 3953, 4446, 4529, 4650`

**p95 (rank 10) = 4650ms = 4.650s**

**Budget:** ≤ 10s (D-01) — **PASS, 2.15x margin.**

**Compliant canary (Pitfall 9):** real LibreOffice PDF/A-2b export → `isCompliant="true"`, 144 passed rules / 0 failed rules, 536 passed checks / 0 failed checks — **PASS.**

**Non-compliant fixture:** plain (non-PDF/A) LibreOffice export through `-f 2b` → `isCompliant="false"`, 2 failed rules (missing XMP metadata stream, DeviceRGB used without an RGB OutputIntent), exit code `1` — **confirmed a valid non-compliant canary.**

**Peak memory:** ~438.6 MiB (459,862,016 bytes, cgroup v2 `memory.peak`) of the 1024 MiB container limit during the full measurement run (JVM cold-starts + idle LibreOffice profile creation) — comfortable headroom under the 1g ceiling.

**Image size delta (D-10 companion observation):** `octoconv-document-worker:latest` (pre-veraPDF) = 549MB; `octoconv-document-worker:verapdf` (with veraPDF bundled, path B) = 742MB — a **+193MB delta**, larger than STACK.md's ~54MB jlink-JRE estimate because path B uses the full Debian `openjdk-17-jre-headless` package rather than the smaller jlink-trimmed JRE that path A would have shipped (path A was not viable, see Decisions above).

## GO/NO-GO VERDICT: **GO**

All D-01 gate conditions hold:
- Measured nearest-rank p95 (4650ms) ≤ 10s budget — PASS
- Real LibreOffice PDF/A-2b export validates `isCompliant=true` (no Pitfall 9 regression) — PASS
- glibc path recorded: **path B** (Debian `openjdk-17-jre-headless`), `verapdf --version` succeeds live inside the built image — PASS
- Exact `-f 2b` + `--format xml` flags/exit codes/report schema captured — PASS

**Plan 23-02 (wiring) may proceed.**

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Corrected the veraPDF Docker Hub tag**
- **Found during:** Task 1 (pre-build tag verification)
- **Issue:** Plan/STACK.md reference `verapdf/cli:1.30.2`; the actual Docker Hub tag has a leading "v" (`v1.30.2`) — the bare tag does not exist and `docker build` would fail to resolve the stage.
- **Fix:** Used `verapdf/cli:v1.30.2` in `Dockerfile.document-worker`, with an inline comment recording the correction.
- **Files modified:** `Dockerfile.document-worker`
- **Verification:** `docker build` resolves and pulls the stage successfully.
- **Committed in:** `5ff59e0`

**2. [Rule 3 - Blocking, plan-anticipated] Switched glibc path A → path B**
- **Found during:** Task 1 (live glibc verification, exactly as the plan instructed)
- **Issue:** The copied jlink JRE (`/opt/java/openjdk`) is musl-linked; `libjli.so` cannot resolve `libc.musl-x86_64.so.1` under bookworm's glibc dynamic loader — `verapdf --version` failed outright.
- **Fix:** Removed the `/opt/java/openjdk` COPY; added `openjdk-17-jre-headless` to the existing `apt-get install` list; kept `COPY --from=verapdf /opt/verapdf /opt/verapdf` (app jars only) and adjusted `PATH` accordingly. veraPDF's launch script auto-detects `java` via `PATH`, so no `JAVA_HOME` env was needed.
- **Files modified:** `Dockerfile.document-worker`
- **Verification:** `verapdf --version`/`--help` succeed; the full measurement script (10 real validations + 2 canaries) runs green.
- **Committed in:** `5ff59e0`

**3. [Rule 3 - Blocking] Pinned document-worker service to `platform: linux/amd64`**
- **Found during:** Task 1 (pre-build architecture check)
- **Issue:** `verapdf/cli` has no arm64 manifest on Docker Hub — the pinned dependency (D-03) is amd64-only, and this repo's dev/CI hosts may include arm64 (Apple Silicon).
- **Fix:** Added `platform: linux/amd64` to the `document-worker` service in `docker-compose.yml`; all builds/runs in this plan used `--platform linux/amd64` explicitly.
- **Files modified:** `docker-compose.yml`
- **Verification:** Image builds and runs correctly under OrbStack's amd64 emulation.
- **Committed in:** `5ff59e0`

---

**Total deviations:** 3 auto-fixed (all Rule 3 — blocking issues required to make the plan's own pinned dependency (D-03) actually function; none change scope beyond what D-03 already committed to).
**Impact on plan:** All three fixes were necessary preconditions for the plan's stated objective (bundle the pinned `verapdf/cli:1.30.2`/`v1.30.2` image and prove it runs live) to be achievable at all. No scope creep — the measurement, fixtures, and CLI contract capture were executed exactly as specified.

## Issues Encountered

None beyond the glibc-boundary failure the plan explicitly anticipated and instructed a fallback for (see Deviations #2 above) — this was expected risk verification, not an unplanned problem.

## User Setup Required

None - no external service configuration required. The go/no-go decision recorded here is the operator-facing artifact for this checkpoint.

## Next Phase Readiness

- **Plan 23-02 (wiring) is cleared to proceed** — the GO verdict is recorded above with raw numbers.
- Plan 23-02 should use `--format xml` (not a separate `mrr` value) and read the `isCompliant` attribute on `<validationReport>` as authoritative, cross-checked against `<batchSummary>`'s `failedToParse`/`outOfMemory`/`veraExceptions` counters for the validator-error path (D-06).
- Plan 23-02/23-03 should verify the exit code for a genuinely malformed/unparseable PDF input (not exercised in this plan) before relying on exit codes alone.
- The `platform: linux/amd64` pin on `document-worker` is a new constraint worth flagging to whoever owns the deployment target — if production nodes are arm64, this plan's Docker-level pin means the whole document-worker service (not just veraPDF) now runs emulated there, which should be confirmed/re-measured before rollout.

---
*Phase: 23-verapdf-validation*
*Completed: 2026-07-13*

## Self-Check: PASSED

All created/modified files verified present on disk; both task commits (`5ff59e0`, `c83248e`) verified present in git log.
