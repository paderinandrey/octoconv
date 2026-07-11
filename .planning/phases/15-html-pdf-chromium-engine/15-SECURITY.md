---
phase: 15
slug: html-pdf-chromium-engine
status: verified
threats_open: 0
asvs_level: 1
created: 2026-07-11
---

# Phase 15 — Security

> Per-phase security contract: threat register, accepted risks, and audit trail.
> This is the milestone's highest-risk phase: an active network actor
> (headless chromium) rendering attacker-influenced HTML under a removed
> chrome-sandbox (`--no-sandbox` forced by `USER nobody`).

---

## Trust Boundaries

| Boundary | Description | Data Crossing |
|----------|-------------|----------------|
| client→DB (`jobs.engine` column) | `engine` is server-derived (`EngineFor`), never client-supplied; CHECK constraint is a defense-in-depth backstop. | engine class string |
| reconciler→queue | Reconciler re-enqueues stranded rows; an unroutable engine must fail closed, not silently drop. | job id |
| client HTML → CSS/CSP builder | Untrusted HTML bytes + untrusted opts JSON cross into the worker; `buildPrintCSS`/`ParseHTMLOpts`/CSP injection are the choke point. | HTML bytes, opts JSON |
| client HTML → chromium renderer | Rendered copy (client content + server CSS/CSP) is handed to a removed-sandbox browser process. | HTML+CSS+CSP bytes |
| worker fs → chromium `file://` | chromium is launched with `file://` access to a worker-generated path; untrusted HTML can reference other `file://` paths. | local filesystem read |
| client→API (multipart) | Declared source/extension + opts JSON are attacker-controlled; sniff + opts validation must fail closed pre-storage. | multipart upload |
| container→network egress | chromium-worker renders attacker-influenced HTML; compose network config is secondary containment behind CLI network-block flags. | outbound network attempts |
| build→apt package | `chromium-headless-shell` pulled from the Debian archive at image build time. | OS package |
| chromium fork tree→PID 1 | chromium forks zygote/GPU/renderer subprocesses; without a reaper they can zombie on timeout-kill. | process lifecycle |

---

## Threat Register

| Threat ID | Category | Component | Disposition | Mitigation | Status |
|-----------|----------|-----------|-------------|------------|--------|
| T-15-01 | Tampering | `jobs.engine` column | mitigate | CHECK constraint allow-list incl. `'html'` | closed |
| T-15-02 | Denial of Service | reconciler sweep | mitigate | Explicit `case convert.EngineHTML` (no fail-closed skip-forever) | closed |
| T-15-03 | Repudiation | engine-class literal drift | accept | `convert.EngineHTML` single-source-of-truth const | closed |
| T-15-04 | Tampering (injection) | `buildPrintCSS`/`ParseHTMLOpts` | mitigate | CSS from validated enum → server-constant map only; `checkStrictObject`+`DisallowUnknownFields`; injection unit test | closed |
| T-15-05 | Information Disclosure | SSRF via rendered refs | mitigate | Three network-block flags (dead proxy + loopback-bypass removal + NXDOMAIN resolver) + `--no-pdf-header-footer`; live canary zero-hits (Plan 04 + Plan 05 acceptance) | closed |
| T-15-06 | Elevation of Privilege | removed-sandbox renderer | mitigate | JS disabled via CSP `<meta script-src 'none'>` injected as **first child of `<head>`** (`injectCSPFirst`, CR-01 fix) + container isolation + `USER nobody` + resource caps | closed |
| T-15-07 | Information Disclosure | `file://` residual read of sibling files | accept | Same-UID nobody temp dirs; internal trust model; live-tested + logged as residual risk in PROJECT.md (DOC-V2-05 precedent) | closed |
| T-15-08/09 | Spoofing | binary-as-.html | mitigate | `LooksLikeHTML` fail-closed 422 pre-storage | closed |
| T-15-10 | Tampering | opts smuggling across engines | mitigate | Engine-keyed dispatch (`ParseHTMLOpts`+`ValidateHTMLApplicability`); normalized struct persisted | closed |
| T-15-11/15 | Denial of Service | stuck/hung render | mitigate | `isHTMLTerminal` timeout-terminal, no retry storm; concurrency cap | closed |
| T-15-12 | Tampering | corrupt `jobs.options` | mitigate | `HTMLOptsFromMap` strict re-parse → `MarkFailed` terminal (Phase 14 T-14-02b discipline) | closed |
| T-15-13 | Denial of Service | zombie/dev-shm | mitigate | tini PID-1 + `--disable-dev-shm-usage` + compose `shm_size` | closed |
| T-15-14 | Denial of Service | render-bomb HTML | accept | `HTML_ENGINE_TIMEOUT` + `HTML_WORKER_CONCURRENCY` cap + `MAX_UPLOAD_BYTES`; matches DOC-V2-05 residual | closed |
| T-15-SC | Tampering | chromium-headless-shell apt install | accept | Official Debian bookworm archive, no language-registry install | closed |

*Status: open · closed*
*Disposition: mitigate (implementation required) · accept (documented risk) · transfer (third-party)*

---

## Threat Verification Evidence

### Mitigate dispositions (grep-verified in cited implementation files)

| Threat ID | Evidence |
|-----------|----------|
| T-15-01 | `internal/db/migrations/0005_html_engine.sql:10-11` — `ALTER TABLE jobs ADD CONSTRAINT jobs_engine_check CHECK (engine IN (..., 'html'))`; `internal/db/migrations/0001_init.sql:47-48` shows the prior allow-list without `'html'`. Live-confirmed applied against Postgres, constraint name matches, per 15-04-SUMMARY.md item 0. |
| T-15-02 | `internal/reconciler/reconciler.go:139` — `case convert.EngineHTML:` explicit arm in the engine sweep switch, ahead of the fail-closed `default:` (`metrics.RecordReconcilerAction("unroutable_engine")` at line 151 remains the backstop for genuinely out-of-scope engines, not html). |
| T-15-04 | `internal/convert/htmlopts.go:53-71` — `ParseHTMLOpts` calls shared `checkStrictObject`, `json.Decoder.DisallowUnknownFields()`, validates `PageSize` against closed `htmlPageSizeCSS` map and `MarginMM` against `[0,50]`. `buildPrintCSS` (`htmlopts.go:123-149`) builds CSS only from `htmlPageSizeCSS[o.PageSize]` and already-range-checked `o.MarginMM`/bools — no client byte reaches the returned string. `internal/convert/chromium_test.go` contains injection-resistance tests (per 15-02-PLAN acceptance criteria). |
| T-15-05 | `internal/convert/chromium.go:165-176` — unconditional argv: `--proxy-server=127.0.0.1:9`, `--proxy-bypass-list=<-loopback>`, `--host-resolver-rules=MAP * ~NOTFOUND`, plus `--no-pdf-header-footer` (added as a Plan 04 live finding to close an info-disclosure gap not in the original RESEARCH flag list). Live canary proof: 15-04-SUMMARY.md (6 targets, zero hits) and 15-05-SUMMARY.md `TestHTMLNetworkBlockE2E` (external IP, loopback IP/hostname, compose hosts, `file://`) — zero hits, job completed. |
| T-15-06 | **CR-01 verified fixed.** `internal/convert/chromium.go:88-102` — `injectCSPFirst` inserts `cspNoScriptMeta` immediately after the opening `<head ...>` tag (first child of `<head>`), *not* before `</head>` (that placement, used only for the print `<style>` via `injectPrintCSS`, was the original CR-01 defect). `chromium.go:144` — `Convert` calls `injectCSPFirst` before `injectPrintCSS`, confirming the fix is wired into the actual render path (not just present as a dead helper). Regression tests confirmed present: `internal/convert/chromium_test.go` — `TestInjectCSPFirstPrecedesInHeadScript` (line 70) and `TestInjectCSPFirstNoHeadFallsBackToHTMLOpen` (line 96). Secondary controls: `Dockerfile.chromium-worker:26` (`USER nobody`), `chromium.go:168` (`--no-sandbox`, required under `USER nobody`), `docker-compose.yml:246-250` (2 CPU / 2g memory limits). |
| T-15-08/09 | `internal/api/handlers.go:164-175` — `if detected == "" && source == "html" && convert.LooksLikeHTML(file, header.Size) { detected = "html" }`, placed BEFORE the generic unrecognized-content 422 branch (`handlers.go:188-196`); a `.html`-named file that fails `LooksLikeHTML` falls through to that 422, which executes before any `s.storage.Upload` call in the handler (upload happens later in the function, confirmed by reading the surrounding control flow). `internal/convert/htmlsniff.go:43-86` — `LooksLikeHTML` rejects NUL bytes, invalid UTF-8, and missing doctype/html markers (fail-closed). |
| T-15-10 | `internal/api/handlers.go:275-296` — engine-keyed `switch engine { case convert.EngineHTML: ... default: ParseDocOpts ... }`; html jobs go through `ParseHTMLOpts`+`ValidateHTMLApplicability` exclusively, and the persisted value is `json.Marshal(htmlOpts)` (the normalized struct), never raw client bytes. `internal/convert/htmlopts.go:97-105` — `ValidateHTMLApplicability` rejects non-zero `HTMLOpts` unless `engine==EngineHTML && target==pdf`. |
| T-15-11/15 | `internal/worker/worker.go:186-194` — `isHTMLTerminal` classifies `context.DeadlineExceeded` terminal (mirrors `isDocumentTerminal`'s DOC-08 pattern exactly); wired into `HandleHTMLConvert`'s terminal branch at `worker.go:441`. Concurrency cap: `cmd/chromium-worker/main.go:86` — `Concurrency: envInt("HTML_WORKER_CONCURRENCY", 2)`. Live SC4 proof in 15-05-SUMMARY.md: `HTML_ENGINE_TIMEOUT=50ms` override produced exactly one `active→failed` transition, no retry storm. |
| T-15-12 | `internal/worker/worker.go:416-432` — `HandleHTMLConvert` calls `convert.HTMLOptsFromMap(job.Opts)` before `MarkActive`; on error, `h.repo.MarkFailed(...)` is called BEFORE the `asynq.SkipRetry` wrap (`ferr := h.repo.MarkFailed(...)` then `return fmt.Errorf("%w: opts: %v", asynq.SkipRetry, err)`), matching the documented Phase-14 T-14-02b discipline (MarkFailed-before-SkipRetry). |
| T-15-13 | `Dockerfile.chromium-worker:32` — `ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/chromium-worker"]` (tini as PID 1). `internal/convert/chromium.go:169` — `--disable-dev-shm-usage` in argv. `docker-compose.yml:245` — `shm_size: "256m"` on the `chromium-worker` service. All three present simultaneously (belt-and-suspenders, per Pitfall C). |

### Accept dispositions (documentation verified)

| Threat ID | Verification |
|-----------|--------------|
| T-15-03 | `internal/convert/convert.go:18-20` — `EngineHTML = "html"` is the single const in the same block as `EngineImage`/`EngineDocument`; all call sites (`queue.go`, `client.go`, `api.go`, `reconciler.go`, `handlers.go`, `worker.go`, `chromium.go`) reference `convert.EngineHTML`, not a raw `"html"` literal (grep-confirmed no bare `"html"` engine-string outside `convert.go` and generated test fixtures). Accepted-risk rationale (residual literal-drift risk, low) recorded in the Accepted Risks Log below. |
| T-15-07 | Live-tested in 15-04-SUMMARY.md (item 6): `<img src="file:///usr/share/pixmaps/debian-logo.png">` successfully loaded under `USER nobody`; confirmed independently (narrower, text-file case) in 15-05-SUMMARY.md's canary test (`file:///etc/hostname`, no visible leak but bytes were fetchable in principle). Formally recorded as an accepted residual risk in `.planning/PROJECT.md` (line 117, "`file://` residual read внутри chromium-worker — accepted residual risk v1.3 (Phase 15)"), matching the DOC-V2-05 precedent. Entry present in Accepted Risks Log below. |
| T-15-14 | `internal/convert/chromium.go` timeout bound via `attemptCtx` (worker.go `process()`, shared across engines) derived from `HTML_ENGINE_TIMEOUT` (`cmd/chromium-worker/main.go:69`, default 60s); concurrency bound via `HTML_WORKER_CONCURRENCY` (`main.go:86`, default 2); upload size bound via `http.MaxBytesReader(w, r.Body, s.maxUploadByte)` (`internal/api/handlers.go:82`, `MAX_UPLOAD_BYTES`-derived). No active DOM-complexity anti-DoS control exists — this is the accepted residual, matching DOC-V2-05. Entry present in Accepted Risks Log below. |
| T-15-SC | `Dockerfile.chromium-worker:12-21` — `apt-get install ... chromium-headless-shell ...` sourced from the default `debian:bookworm-slim` apt sources (no third-party repo added, no `add-apt-repository`, no external `.deb` download) — official Debian archive package. Entry present in Accepted Risks Log below. |

---

## Unregistered Flags

None — no SUMMARY.md `## Threat Flags` section was found across `15-01-SUMMARY.md` through `15-05-SUMMARY.md` that introduces attack surface not already mapped to a threat ID above. The 15-04/15-05 SUMMARY "Deviations from Plan" entries (CSP-meta JS-disable replacing the broken launch flag; forced `background:none` CSS override; `--no-pdf-header-footer` addition; `terminalChromiumSignatures` gaining `"stat output"`) are all in-scope corrections to already-registered T-15-04/T-15-05/T-15-06/T-15-11 mitigations, not new unmapped surface — each is evidenced above under its corresponding threat ID. The code-review CR-02 (chromium PDF-validation error-prefix mislabeling) and CR-03 (deferred: forced `margin:0` default on no-opts jobs) are diagnostics/UX findings, not attack-surface flags; CR-03 in particular does not weaken any threat mitigation above (it forces a *stricter*, not weaker, default) and is tracked as a fast-follow per `15-REVIEW.md`.

---

## Accepted Risks Log

| Risk ID | Threat Ref | Rationale | Accepted By | Date |
|---------|------------|-----------|-------------|------|
| AR-15-01 | T-15-03 | `EngineHTML` is a single compile-time const referenced everywhere; residual risk is a future engineer bypassing the const with a raw `"html"` literal — low likelihood, no client-facing exposure, caught by code review (DEBT-02 pattern). | Plan 01 author (register-authored-at-plan-time) | 2026-07-10 |
| AR-15-02 | T-15-07 | `file://` inside chromium-worker can read any world-readable file accessible to `USER nobody`, including sibling job workDirs (same UID does not isolate). Live-tested (Plan 04 item 6, Plan 05 canary): passive `<img>/<link>/<script src>` loads succeed; active `fetch()`/XHR to `file://` is separately blocked by Chromium itself. Internal-only-clients trust model (no external tenants); matches the v1.2 DOC-V2-05 precedent already accepted for the document engine. Recorded in `.planning/PROJECT.md` Key Decisions (line 117). Future mitigation (bind-mount only the job's own workDir) noted as a deferred future option, not blocking Phase 15. | User (checkpoint approval, Plan 04 Task 2) | 2026-07-11 |
| AR-15-03 | T-15-14 | No active HTML/DOM-complexity anti-DoS analysis exists; a pathologically complex HTML document could consume worker resources up to `HTML_ENGINE_TIMEOUT`. Bounded by timeout + `HTML_WORKER_CONCURRENCY` cap + `MAX_UPLOAD_BYTES` (upload size ceiling). Matches the v1.2 DOC-V2-05 accepted residual for the document engine (same rationale, same milestone posture: revisit if load grows). | Plan 04/05 author (register-authored-at-plan-time) | 2026-07-11 |
| AR-15-04 | T-15-SC | `chromium-headless-shell` is installed via `apt-get install` from the default Debian bookworm archive baked into the `debian:bookworm-slim` base image — no third-party repository, no unpinned upstream binary download. Not a language-ecosystem package registry (npm/pip/cargo), so the project's slopcheck supply-chain gate is N/A; Debian archive provenance is the accepted trust boundary. | Plan 04 author (register-authored-at-plan-time) | 2026-07-11 |

*Accepted risks do not resurface in future audit runs.*

---

## Security Audit Trail

| Audit Date | Threats Total | Closed | Open | Run By |
|------------|---------------|--------|------|--------|
| 2026-07-11 | 14 | 14 | 0 | gsd-security-auditor |

**Audit notes:**
- All 5 plan `<threat_model>` blocks loaded (15-01 through 15-05); duplicate threat IDs across plans (T-15-05, T-15-06, T-15-07, T-15-09) consolidated to their strongest/most-recent evidence (live e2e in Plan 05 supersedes unit-test-only evidence in Plan 02 where both exist).
- T-15-06 received explicit adversarial scrutiny per the task's constraint: the plan's originally-declared mechanism (`--blink-settings=scriptEnabled=false`) is CONFIRMED ABSENT from the shipped argv (correctly — it was live-tested broken in Plan 04 and replaced). The replacement mechanism (CSP `<meta>` injection) was itself found defective at first implementation (code review CR-01: injected at end-of-`<head>`, too late to govern head-preceding scripts) and is CONFIRMED FIXED in the current code: `injectCSPFirst` (`internal/convert/chromium.go:88-102`) places the CSP as the first child of `<head>`, with two passing regression tests (`TestInjectCSPFirstPrecedesInHeadScript`, `TestInjectCSPFirstNoHeadFallsBackToHTMLOpen`). This audit judged T-15-06 against the actual post-CR-01-fix code, not the plan's stale `scriptEnabled=false` text, per instruction.
- No new/unmapped attack surface (`unregistered_flag`) found in any of the 5 SUMMARY.md files.
- Implementation files were read-only for this audit; no code was modified.

---

## Sign-Off

- [x] All threats have a disposition (mitigate / accept / transfer)
- [x] Accepted risks documented in Accepted Risks Log
- [x] `threats_open: 0` confirmed
- [x] `status: verified` set in frontmatter

**Approval:** verified 2026-07-11
