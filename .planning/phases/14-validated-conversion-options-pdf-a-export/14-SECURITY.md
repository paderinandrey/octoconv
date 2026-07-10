---
phase: 14
slug: validated-conversion-options-pdf-a-export
status: verified
threats_open: 0
asvs_level: 1
created: 2026-07-11
---

# Phase 14 — Security

> Per-phase security contract: threat register, accepted risks, and audit trail.

---

## Trust Boundaries

| Boundary | Description | Data Crossing |
|----------|-------------|----------------|
| client → API (opts form field) | Untrusted `opts` JSON crosses the multipart boundary; first validation point for client bytes | Untrusted JSON string, capped at 4 KiB |
| repository → Postgres | Server-normalized options (validated at API layer) serialized into `jobs.options` jsonb | Normalized `map[string]any`, never raw client bytes |
| persisted opts → converter argv | `job.Opts` (read back from Postgres) crosses into the `soffice --convert-to` argument — the milestone's highest-severity net-new attack surface | Re-validated `DocOpts` struct; filter suffix is a compile-time constant |
| converter output → job status | LibreOffice output crosses into the done/failed decision; a mis-tagged PDF/A must not be reported as success | PDF bytes scanned for `/GTS_PDFA` OutputIntent marker |
| operator → ephemeral docker-compose stack | Acceptance-only stack for live PDF/A verification | n/a (already closed via Phase 13's SSRF-relaxation scoping) |

---

## Threat Register

| Threat ID | Category | Component | Disposition | Mitigation | Status |
|-----------|----------|-----------|-------------|------------|--------|
| T-14-01 | Tampering (UNO filter-property injection) | `PDFAFilterOptions` / `ParseDocOpts` / `handleCreateJob` opts parse | mitigate | Compile-time constant filter string keyed on a validated single-value enum; `DisallowUnknownFields` + allow-list; 422 before storage; `TestDocOptsInjectionResistance` + API-level `TestCreateJob_OptsInjectionAttempt` | closed |
| T-14-02 | Denial of Service (oversized opts field) | `handleCreateJob` `maxOptsBytes` cap | mitigate | 4 KiB cap → 422 before parse/marshal; outer body bounded by `maxUploadByte` | closed |
| T-14-02a | Denial of Service (options column write) | `repo.Create` options write | accept | Bounded upstream at the API layer before `CreateParams.Opts` | closed (accepted) |
| T-14-02b | Denial of Service (garbage persisted opts) | worker `HandleDocumentConvert` strict re-parse | mitigate | Malformed `job.Opts` strict-parsed → `MarkFailed("invalid_options", …)` + best-effort webhook enqueue BEFORE `SkipRetry` (`internal/worker/worker.go:262-276`, fix `c895b5a`); job terminally reported to client, no reconciler requeue churn | closed |
| T-14-01a | Tampering (column garbage) | `repo.Get` options unmarshal | mitigate | Unmarshal failure surfaces as a wrapped error; worker strict re-parse is the backstop | closed |
| T-14-03 | Integrity (false PDF/A claim) | `validateDocumentOutput` OutputIntent check + live acceptance | mitigate | `/GTS_PDFA` marker required else terminal fail; terminal signature coupled same commit; live-verified 2026-07-11 (non-authoritative; veraPDF = accepted residual risk DOCV3-01) | closed |
| T-14-04 | Information Disclosure (opts echo) | `handleGetJob` / create response | accept | Only server-normalized closed-schema opts echoed; ownership check guards cross-client access | closed (accepted) |
| T-14-SC | Tampering (supply chain) | Go module dependencies | accept | Zero new packages, stdlib only across all 3 plans; `go.mod`/`go.sum` unchanged since phase 1 | closed (accepted) |

*Status: open · closed*
*Disposition: mitigate (implementation required) · accept (documented risk) · transfer (third-party)*

---

## Accepted Risks Log

| Risk ID | Threat Ref | Rationale | Accepted By | Date |
|---------|------------|-----------|-------------|------|
| AR-14-01 | T-14-02a | The `jobs.options` column write in `Repo.Create` (`internal/jobs/repo.go:93-97`) has no independent size guard of its own — it accepts whatever `CreateParams.Opts` it is handed. This is accepted because the ONLY write path into `CreateParams.Opts` is `internal/api/handlers.go`'s `handleCreateJob`, which enforces `maxOptsBytes = 4096` (handlers.go:252) and rejects with 422 before `CreateParams` is ever constructed (handlers.go:295-303, `Opts: normalizedOpts` at line 303 sits after all validation). Confirmed: no other caller of `jobs.CreateParams{...}` with a non-nil `Opts` exists in the codebase (`grep -rn "CreateParams{" internal/` shows only `handlers.go` populating `Opts`). | Plan 14-01/14-03 authors; re-verified gsd-security-auditor | 2026-07-11 |
| AR-14-02 | T-14-04 | `handleGetJob` and the create response echo `job.Opts`/`normalizedOpts` (handlers.go:344-346, 385-387). This is accepted because the echoed value is always the server-normalized `DocOpts` struct round-tripped through `json.Marshal`/`json.Unmarshal` (D-08) — a closed schema with a single `pdf_profile` enum field, never raw client bytes, and never any secret/credential material. Cross-client access is already blocked by the pre-existing ownership guard in `handleGetJob` (handlers.go:375-379: a job belonging to a different `client.ID` returns the identical 404 as a genuinely-missing job — AUTH-03). Confirmed in code: no additional field beyond `pdf_profile` can ever appear in the echoed map, since `ParseDocOpts` rejects unknown keys before normalization. | Plan 14-03 authors; re-verified gsd-security-auditor | 2026-07-11 |
| AR-14-03 | T-14-SC | No new third-party/package-manager dependencies introduced across Plans 14-01/14-02/14-03 — only Go stdlib (`encoding/json`, `bytes`, `os`) plus already-vendored packages. Confirmed: `git diff b51044e~1 28c9be6 -- go.mod go.sum` (the phase's full commit range) produces no output; `go.mod`/`go.sum` last changed at `26fc50e` (phase 1, `httprate`). | Plan 14-01/14-02/14-03 authors; re-verified gsd-security-auditor | 2026-07-11 |

*Accepted risks do not resurface in future audit runs unless the underlying code changes.*

---

## Closed After Fix — Detail (T-14-02b)

> **Resolution 2026-07-11:** fixed in commit `c895b5a` — the corrupt-opts branch now calls `h.repo.MarkFailed(ctx, jobID, "invalid_options", "stored conversion options are invalid", {"opts_error": …})` and best-effort-enqueues the webhook before returning `SkipRetry`, mirroring the established terminal-branch pattern. `queued → failed` transition confirmed legal (`internal/jobs/repo.go:162`). `go build`/`go vet`/worker+jobs+convert tests re-run green. The audit finding below is preserved for the record.

### T-14-02b — DoS (garbage persisted opts) — worker `HandleDocumentConvert` strict re-parse (original finding)

**Declared mitigation plan:** "Malformed job.Opts strict-parsed → terminal SkipRetry, no retry burn" (14-02-PLAN.md threat_model).

**What is actually in the code** (`internal/worker/worker.go:256-264`):
```go
if _, err := convert.DocOptsFromMap(job.Opts); err != nil {
    return fmt.Errorf("%w: opts: %v", asynq.SkipRetry, err)
}
```
This check runs **before** `h.repo.MarkActive` and does **not** call `h.repo.MarkFailed`. `asynq.SkipRetry` does correctly stop *asynq's own* internal retry mechanism for that one task delivery — so the narrow claim "asynq will not retry this task up to `DOCUMENT_MAX_RETRY` times" is true and verified in code.

**Why this is still OPEN, not closed-with-caveat:** the job row is never transitioned out of `queued` (no `MarkFailed` call), so it is picked up by the reconciler's staleness sweep. Verified independently in `internal/reconciler/reconciler.go`:
- `FindStale` (reconciler.go:97) returns jobs stuck in `queued` past `QueuedStaleAfter`.
- `RequeueStale` (reconciler.go:170) puts the job back through the same `HandleDocumentConvert` path, which hits the identical `DocOptsFromMap` failure and returns `SkipRetry` again — the job re-enters `queued`, is found stale again, and is requeued again.
- This repeats until `MaxRecoveries` is exhausted (reconciler.go:110), at which point the job is finally marked failed — but with the misleading code `reconciler_exhausted` (reconciler.go:116), not a code describing the actual root cause (corrupt persisted opts).

**Conclusion:** the plan's literal claim — "no retry burn" — does not hold. The mechanism was moved from asynq's direct per-task retry to the reconciler's requeue cycle, which still burns real worker/reconciler cycles across multiple staleness intervals before the job is ever terminally reported to the client, and the eventual failure reason is wrong. This is the exact gap code review flagged as WR-02 (`14-REVIEW.md`), independently re-verified against `internal/reconciler/reconciler.go` in this audit (not merely trusting REVIEW.md prose).

**Files searched:** `internal/worker/worker.go:256-300`, `internal/reconciler/reconciler.go:90-230`, `internal/jobs/repo.go` (`MarkFailed` allows `queued -> failed`, confirming the review's proposed fix is legal).

**Fix (per REVIEW.md WR-02, not yet applied):**
```go
if _, err := convert.DocOptsFromMap(job.Opts); err != nil {
    _ = h.repo.MarkFailed(ctx, jobID, "invalid_opts", "stored conversion options are invalid",
        map[string]any{"opts_error": err.Error()})
    if job.CallbackURL != "" {
        _ = h.enqueuer.EnqueueWebhookDeliver(ctx, jobID)
    }
    return fmt.Errorf("%w: opts: %v", asynq.SkipRetry, err)
}
```

---

## Cross-Checked Review Findings (WR-01 / WR-02 / WR-05)

Per the audit's explicit mandate, each finding was traced against its mapped threat and a disposition rendered:

- **WR-01** (`ParseDocOpts` accepts trailing JSON/`null`/duplicate keys, `internal/convert/opts.go:39-50`) → mapped to **T-14-01**. Verified: `json.Decoder.Decode` reads only the first JSON value; trailing bytes, `null`, and duplicate-key "last wins" all pass where a truly strict decoder would reject them. **Mitigation still holds** despite this: `PDFAFilterOptions` (opts.go:92-97) never reads anything from `DocOpts` except an exact-equality comparison of the single `PDFProfile` string against the `pdfProfileA2b` constant — trailing garbage or an ignored second JSON object cannot change which branch is taken or influence the returned string, which is always the same compile-time literal. Re-ran `TestDocOptsInjectionResistance` (5/5 PASS) and confirmed by direct source read that `PDFAFilterOptions`'s only data dependency on `o` is the boolean-valued `==` check. T-14-01 remains **CLOSED**, but WR-01 is a real spec-conformance gap (the doc comment's "strict-decodes" claim is inaccurate) that should be fixed per REVIEW.md's suggested `dec.More()` check to avoid a false sense of strictness for any future caller.

- **WR-02** (corrupt-opts `SkipRetry` without `MarkFailed` strands the job in `queued`, `internal/worker/worker.go:262-264`) → mapped to **T-14-02b**. Verified independently against `internal/reconciler/reconciler.go` (see "Open Threat — Detail" above). **Mitigation does NOT fully hold** — see T-14-02b marked **OPEN**.

- **WR-05** (oversize-opts test payload also fails the enum allow-list, so it cannot detect removal of the size cap, `internal/api/handlers_test.go:1023-1045`) → mapped to **T-14-02**. Verified: the test constant is `` `{"pdf_profile":"` + strings.Repeat("a", 5000) + `"}` ``, which is simultaneously oversized (>4 KiB) AND an invalid `pdf_profile` value, so a regression that deleted the `len(rawOpts) > maxOptsBytes` guard (handlers.go:252) would still 422 via the allow-list check and this test would not catch it. **This is a test-quality gap, not an implementation gap** — the size-cap code itself (`internal/api/handlers.go:252-256`) was verified directly: it is an unconditional `len()` check that runs first, before `ParseDocOpts` is ever called, regardless of the field's content. T-14-02 remains **CLOSED** on the strength of the source-level guard, but the regression-detection test should be strengthened per REVIEW.md's suggested fix (pad with valid-JSON whitespace instead of an invalid enum value) so a future deletion of the cap is actually caught by CI.

---

## Security Audit Trail

| Audit Date | Threats Total | Closed | Open | Run By |
|------------|---------------|--------|------|--------|
| 2026-07-11 | 8 | 7 | 1 | gsd-security-auditor |
| 2026-07-11 (post-fix `c895b5a`) | 8 | 8 | 0 | orchestrator (targeted re-verification of T-14-02b fix) |

---

## Sign-Off

- [x] All threats have a disposition (mitigate / accept / transfer)
- [x] Accepted risks documented in Accepted Risks Log
- [x] `threats_open: 0` confirmed — T-14-02b fixed in `c895b5a` (2026-07-11)
- [x] `status: verified` set in frontmatter

**Approval:** granted 2026-07-11 — all 8 threats closed (5 mitigated & verified, 3 accepted & documented).

---

## Audit Notes (evidence trail)

All evidence below was gathered by reading the actual implementation files and re-running the cited tests in this audit session, plus independently tracing the reconciler code path referenced by REVIEW.md's WR-02 rather than trusting its prose alone.

### T-14-01 — Filter-property injection resistance
- `internal/convert/opts.go:39-50` (`ParseDocOpts`) — `DisallowUnknownFields()` + exact-string allow-list (`o.PDFProfile != "" && o.PDFProfile != pdfProfileA2b` → error).
- `internal/convert/opts.go:92-97` (`PDFAFilterOptions`) — returns only the compile-time `pdfaFilterOptionsSuffix` constant, gated by an `==` comparison; no `json.Marshal(o)`/`fmt.Sprintf` interpolation of client fields anywhere in the function body.
- `internal/api/handlers.go:257-279` — `ParseDocOpts` → `ValidateApplicability` → re-marshal of the *struct* (not raw bytes) into `normalizedOpts`, all before `s.storage.Upload` (line 288).
- Re-ran `go test ./internal/convert/... -run 'PDFA|Injection|OutputIntent' -v`: `TestPDFAFilterOptions`, `TestDocOptsInjectionResistance` (5/5 adversarial subtests), `TestValidatePDFAOutputIntent` all PASS.
- Re-ran `go test ./internal/api/... -run 'Opts|GetJob' -v`: `TestCreateJob_OptsInjectionAttempt` PASS (422, no storage write, no job created).

### T-14-02 — Oversized opts field cap
- `internal/api/handlers.go:252-256` — `len(rawOpts) > maxOptsBytes` (4096) checked first, before `ParseDocOpts`/`json.Marshal`, returns 422.
- `internal/api/handlers.go:82` — outer request body already bounded by `http.MaxBytesReader(w, r.Body, s.maxUploadByte)`.
- Re-ran `TestCreateJob_OptsOversizeRejected`: PASS (422, no upload, no job created). Noted as WR-05: test payload also fails the enum check — see Cross-Checked Review Findings above; does not change the CLOSED verdict since the cap itself is verified directly in source.

### T-14-02a — Options column write DoS (accept)
- `internal/jobs/repo.go:93-97` — no independent bound; relies on the upstream API cap. `grep -rn "CreateParams{" internal/` confirms `handlers.go` is the only populator of a non-nil `Opts`.
- Accepted risk entry AR-14-01 written to this SECURITY.md (see Accepted Risks Log).

### T-14-02b — Garbage persisted opts DoS (OPEN)
- `internal/worker/worker.go:256-264` — `SkipRetry` wrap present (asynq-level retry is avoided) but no `MarkFailed` call.
- `internal/reconciler/reconciler.go:90-230` — independently traced: `FindStale` → `RequeueStale` → re-delivery → same `SkipRetry` failure → re-stale → repeat until `MaxRecoveries` exhausted → `MarkFailed(..., "reconciler_exhausted", ...)` (reconciler.go:116). Confirms REVIEW.md's WR-02 claim of real (reconciler-driven) retry burn and a misleading terminal error code.
- Classified **OPEN** — see "Open Threat — Detail" above.

### T-14-01a — Column garbage on Get
- `internal/jobs/repo.go:335-336` — `json.Unmarshal(optsJSON, &j.Opts)` error wrapped as `fmt.Errorf("unmarshal job options: %w", err)`, propagates up through `Get` → `HandleDocumentConvert`'s `h.repo.Get` error path (worker.go:251-254, unwrapped, so asynq retries — appropriate, since a transient read/decode issue on a well-formed column is not expected but is not classified as a client-input problem either).
- The worker's own `DocOptsFromMap(job.Opts)` re-parse (T-14-02b's mechanism) is the declared backstop for genuinely malformed *content* that unmarshals successfully as a `map[string]any` but fails `DocOpts` validation.

### T-14-03 — False PDF/A claim
- `internal/convert/libreoffice.go:213-229` — `validateDocumentOutput` requires `bytes.Contains(data, gtsPDFAMarker)` when `wantPDFA` is true, on the `pdf` target branch, after `validatePDF` succeeds.
- `internal/worker/worker.go:57-60` — `terminalLibreOfficeSignatures` includes the lowercased exact match `"output missing pdf/a outputintent marker"`, coupled in the same plan/commit as the validator (D-06).
- `14-03-SUMMARY.md` — live docker-compose run against real LibreOffice 7.4 confirmed the exact marker `/Type/OutputIntent/S/GTS_PDFA1/...`, matched by the family-match constant with no code correction needed; human-approved checkpoint 2026-07-11.
- Non-authoritative sanity check only (Pitfall 8); full ISO 19005 conformance via veraPDF remains accepted residual risk DOCV3-01 (pre-existing, not re-litigated by this phase).

### T-14-04 — Opts echo information disclosure (accept)
- `internal/api/handlers.go:344-346` (create response), `:385-387` (`handleGetJob`) — only the normalized closed-schema `map[string]any` (max one key, `pdf_profile`) is ever echoed.
- `internal/api/handlers.go:375-379` — ownership guard: cross-client access returns the identical 404 as not-found (pre-existing AUTH-03 pattern, unmodified by this phase).
- Accepted risk entry AR-14-02 written to this SECURITY.md.

### T-14-SC — Supply chain (accept)
- `git diff b51044e~1 28c9be6 -- go.mod go.sum` — no output across the phase's full commit range.
- Accepted risk entry AR-14-03 written to this SECURITY.md.

### Full-tree verification (re-run in this audit session)
- `go build ./...` — exit 0.
- `go vet ./...` — clean.
- `go test ./internal/convert/... -run 'PDFA|Injection|OutputIntent' -v` — PASS (all subtests).
- `go test ./internal/api/... -run 'Opts|GetJob' -v` — PASS (14/14).

### Unregistered Flags (SUMMARY.md `## Threat Flags`)
None of the three plan SUMMARY.md files (14-01, 14-02, 14-03) contain a `## Threat Flags` section — no executor-surfaced new attack surface to reconcile. All new attack surface identified during this audit (WR-01/WR-02/WR-05, cross-checked above) came from `14-REVIEW.md`'s code review, not from executor self-flagging, and has been folded into the existing threat register (T-14-01, T-14-02b, T-14-02) per this task's explicit instruction rather than logged as a separate unregistered flag.
