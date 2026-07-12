# Pitfalls Research

**Domain:** MCP server (stdio) wrapping an internal REST API; self-service preset CRUD; ISO 19005 (veraPDF) validation; OLE-CFB directory parsing
**Researched:** 2026-07-13
**Confidence:** MEDIUM-HIGH (MCP timeout/stdio behavior verified via multiple independent sources incl. official docs and live GitHub issues; veraPDF server-mode and CFB-DoS findings verified via official repos/advisories; presets/auth pitfalls derived directly from this codebase's existing conventions — HIGH confidence there)

## Critical Pitfalls

### Pitfall 1: `convert_file` blocks past the MCP client's idle-notification window

**What goes wrong:**
The seed design (`SEED-003`) commits `convert_file` to being a single blocking call: multipart upload → internal poll to `done`/`failed` → download. For document-class jobs (LibreOffice, chromium-headless) this can legitimately run well past a few seconds, and Claude Code / other MCP clients kill the call if it goes idle for too long.

**Why it happens:**
MCP clients enforce an **idle window**, not a fixed wall-clock timeout: if a tool call sends no response and no `notifications/progress` for the idle window, the client aborts it — the default idle window is documented as **~30 minutes for stdio servers** vs **~5 minutes for HTTP/SSE/WebSocket servers** (verified against MCP docs + live Claude Code issue reports). A naive implementation that awaits the full poll loop without ever calling `Session.NotifyProgress` looks fine in manual testing (fast image jobs finish before any window matters) and then times out unpredictably in production once a slow LibreOffice/chromium job or queue backpressure pushes total latency past the idle window — this is a "looks done but isn't" trap because the demo case (image convert) never exercises the failure path.

**How to avoid:**
- Every poll iteration inside `convert_file` MUST call `req.Session.NotifyProgress(ctx, ...)` with the current job status (queued/active), not just sleep-and-retry silently — this resets the idle-window clock on the client side regardless of total elapsed time.
- Read the client's declared progress token via `req.Params.GetProgressToken()` before starting the poll loop; if no token is present (some clients don't request progress), fall back to a documented hard cap and return a structured "still running, use get_job_status(job_id)" result rather than blocking indefinitely — this matches the seed's own admission that `get_job_status`/`download_result` exist "for long jobs."
- Bound the internal poll loop with its own context timeout derived from a config value (e.g. `MCP_CONVERT_TIMEOUT`), independent of `ENGINE_TIMEOUT`/`DOCUMENT_ENGINE_TIMEOUT` — never let `convert_file` block forever just because the underlying job is stuck in `queued` (reconciler will eventually resolve it, but the MCP call must not hang the agent session waiting).

**Warning signs:**
- Manual testing only exercises fast image conversions; no test forces a document/chromium-class job through `convert_file` to observe idle-window behavior.
- No code path calls `NotifyProgress` at all — grep for it before shipping.
- Poll loop has no independent max-duration guard distinct from queue/engine timeouts.

**Phase to address:** MCP server phase (SEED-003) — must be a design decision in the same plan that implements `convert_file`, not deferred.

---

### Pitfall 2: API key leaks into MCP tool results or error text

**What goes wrong:**
The MCP server holds a real client API key in its own environment (per the seed design) and uses it to call `POST /v1/jobs`/`GET /v1/jobs/{id}`. If an HTTP call fails (network error, 401, 500) and the handler naively wraps the raw `error` or dumps the outgoing request for debugging, the `Authorization: ApiKey <key>` header — or the key itself if it was interpolated into a URL/log line — ends up in the tool's returned content, which an LLM client may echo back to the user, log verbatim, or persist in conversation history.

**Why it happens:**
This codebase's own convention (`internal/api/handlers.go`) is explicit: **HTTP handlers never leak internal error text to clients** — they map errors to fixed strings and discard the underlying `err`. An MCP server built as a "thin wrapper" is tempting to write more loosely than a hardened HTTP handler because it "isn't public-facing," but MCP tool output is functionally client-facing: it goes straight into an LLM's context and potentially into transcripts/telemetry the agent framework captures. A generic `fmt.Errorf("request failed: %w", err)` where `err` is `*url.Error` will include the full request URL — and if the key was ever placed in a query string or the error type stringifies the request object, the header goes with it.

**How to avoid:**
- Construct the outgoing `http.Request` once in a small internal client wrapper (mirroring `internal/queue.Client` / `internal/storage.Client`'s "wrap the raw SDK" pattern) that NEVER logs or returns the `Authorization` header value; any error surfaced to the MCP tool result must go through the same "map to fixed message, discard raw err" discipline already used in `internal/api/handlers.go`.
- The API key must be read once from env at process start and held only inside the HTTP client wrapper's closure — not passed around as a bare string to logging call sites.
- Add a unit test (mirroring `handlers_test.go`'s error-mapping tests) that asserts a forced upstream 401/500 produces a tool result/error string that does NOT contain the configured key substring.

**Warning signs:**
- Any `log.Printf`/error string in `cmd/mcp-server` includes `%+v` on an `*http.Request`, `*http.Response`, or raw transport error.
- The key is threaded through function signatures as a bare `string` reused in multiple places instead of confined to one client constructor.

**Phase to address:** MCP server phase — same plan as Pitfall 1; add as an explicit security must-have, not an afterthought.

---

### Pitfall 3: Agent-supplied paths in `convert_file`/`download_result` enable path traversal or arbitrary file read/write

**What goes wrong:**
`convert_file(path, ...)` and `download_result(job_id)` (which presumably writes the downloaded result to a local path) both take a filesystem path that originates from the LLM agent's own reasoning, not a human typing a trusted CLI argument. A malicious or confused agent (prompt-injected via file content, or simply hallucinating) can supply `../../../../etc/passwd`, an absolute path outside any sanctioned working directory, or a symlink target, causing the MCP server (running with the developer's or service account's filesystem permissions) to read or overwrite files never intended to be touched.

**Why it happens:**
Every other input surface in this codebase treats client-controlled data as untrusted-until-validated (magic-bytes content sniffing, allowlisted `opts`, OLE-CFB fail-closed rejection). A stdio MCP server is easy to mentally categorize as "local dev tool, not the hardened public API," but it inherits the exact same trust boundary problem as accepting a file path from an HTTP client — except the "client" here is an LLM whose input can itself be attacker-influenced (e.g., processing an untrusted document that contains instructions).

**How to avoid:**
- Resolve every incoming path with `filepath.Clean` + `filepath.Abs`, then require it to be a strict descendant of one explicitly configured root directory (e.g. `MCP_WORKDIR`); reject with a fixed error message (never echo the resolved path) if it escapes the root — same "fail closed, no leak" posture as the CFB/API-key conventions.
- For `download_result`, generate the output path server-side inside the allowed root (e.g. based on `job_id`) rather than trusting an agent-supplied destination path directly; if a destination must be accepted, apply the same containment check.
- Do not follow symlinks when resolving the final path (`os.Lstat` check, reject if the resolved leaf is a symlink) to prevent a workdir-contained symlink from pointing outside the root.

**Warning signs:**
- Path handling code calls `os.Open(path)` / `os.Create(path)` directly on the agent-supplied string without any `filepath.Abs`/containment check.
- No test exercises `../`-style traversal or absolute-path inputs against `convert_file`/`download_result`.

**Phase to address:** MCP server phase — implement alongside the HTTP client wrapper (Pitfall 2), since both are "MCP server's first line of input handling."

---

### Pitfall 4: Returning file bytes in tool results blows up agent context and duplicates the existing presigned-URL contract

**What goes wrong:**
It's tempting for `download_result`/`convert_file` to read the converted file and return its bytes (raw or base64) as the tool result "for convenience." For anything beyond a tiny image this either exceeds practical MCP result size limits or consumes enormous LLM context, and it silently reimplements what `GET /v1/jobs/{id}`'s presigned URL already solves cleanly.

**Why it happens:**
The seed design explicitly favors "one call is better than a job_id for agent UX" for `convert_file`, and that convenience framing can bleed into "just hand back the file too." But the whole point of the existing presigned-URL design (`internal/storage`) is that large binary payloads never need to transit the API/MCP process at all.

**How to avoid:**
- `convert_file`/`download_result` should return the local filesystem path where the result was saved (written via the S3 presigned GET, streamed straight to disk, never buffered fully in memory) or the presigned URL itself if the agent's downstream use doesn't need local bytes — never inline file content as the tool result payload.
- If an agent truly needs to "see" content (e.g., a converted text-like format), that's a deliberate, separate, size-capped tool — not the default path for `convert_file`.

**Warning signs:**
- Tool result schema includes a `content`/`bytes`/`base64` field sized off the actual converted file rather than a `path`/`url` field.
- No explicit size check before any data is embedded in a tool response.

**Phase to address:** MCP server phase.

---

### Pitfall 5: Stray stdout writes corrupt the stdio JSON-RPC stream

**What goes wrong:**
Anything written to stdout that isn't a JSON-RPC message — a debug `fmt.Println`, a startup banner, a panic's default stack trace (which Go writes to stderr, but a recovered-and-reprinted panic could go to stdout if mishandled), or output from a shelled-out dependency inheriting the process's stdout — desynchronizes the newline-delimited JSON-RPC stream over stdin/stdout that `CommandTransport` in the official Go SDK relies on. The result ranges from a single garbled response to a fully wedged/crashed client session.

**Why it happens:**
This is a very common real-world MCP failure mode (multiple independent GitHub issues confirm it across ecosystems, e.g. ruvnet/claude-flow #835), because "just log it, we'll figure out formatting later" is a normal Go habit (`log.Printf` defaults to stderr, but any raw `fmt.Print*`/`os.Stdout.Write` slip is fatal here) and because this project's OWN logging convention (`cmd/*/main.go` uses `log.Printf`, which already goes to stderr by default) is easy to violate accidentally the first time someone reaches for `fmt.Println` during development and forgets to remove it, or when a future dependency (e.g. a debug/verbose flag in a vendored library) writes to stdout internally.

**How to avoid:**
- Establish as a hard rule for `cmd/mcp-server`: `stdout` is reserved exclusively for the MCP transport; ALL diagnostics go through `log.Printf`/`log.Println` (which already default to stderr in this codebase, matching the existing `🚀`/`🐙`-prefixed startup-log convention) — never `fmt.Println`/`fmt.Print` anywhere in this binary.
- If any invoked code path could write to stdout (unlikely here since the MCP server is a thin HTTP client, not a process-exec engine like `internal/convert`), redirect/discard it explicitly.
- Add a lightweight startup smoke test (or manual `mcp inspector` check, per MCP's own debugging docs) that pipes stdout through a JSON-line validator to catch any stray non-protocol output before shipping.

**Warning signs:**
- Any `fmt.Println`/`fmt.Printf` (not `log.Printf`) appears anywhere in `cmd/mcp-server`.
- Client-side symptom: intermittent "unexpected token" / JSON parse errors in the MCP client log that don't reproduce consistently (classic corrupted-stream signature).

**Phase to address:** MCP server phase — verification step, not a separate task; catch in code review by grepping for `fmt.Print` in the new package.

---

### Pitfall 6: Mass-assignment lets a client set `scope=system` or spoof `client_id` via the presets REST payload

**What goes wrong:**
`/v1/presets` (PRST-V2-01) is explicitly self-service, client-scope only — "system-scope остаётся за операторским CLI." If the REST handler naively binds the incoming JSON body onto (or close to) the same `CreateParams`/update struct the `manage-presets` CLI uses — which has `Scope` and `ClientID` fields for legitimate operator use — a client can include `"scope":"system"` or `"client_id":"<someone-else's-uuid>"` in the request body and either promote their preset to system-wide visibility (affecting every client) or attempt to write into another client's namespace.

**Why it happens:**
`internal/presets/repo.go`'s `CreateParams` struct carries `Scope` and `ClientID` as ordinary exported fields with no built-in authorization check — that's correct for a CLI operated by a trusted human with direct DB access, but wrong for a JSON body coming from an authenticated-but-untrusted-with-elevated-scope HTTP client. This is the single most common REST API mistake in general (classic mass-assignment / "just json.Unmarshal into the DB struct"), and it's especially easy to reintroduce here because the repo layer is deliberately being reused ("share the repo layer" to avoid CLI/REST semantic drift) — reuse of the struct must not become reuse of its full field surface as the wire format.

**How to avoid:**
- Define a separate, narrower REST request DTO (e.g. `presetRequest{Name, TargetFormat, Options, Description}` — no `Scope`, no `ClientID` fields at all) that the handler unmarshals into; the handler then constructs `presets.CreateParams{Scope: presets.ScopeUser, ClientID: &client.ID, ...}` itself, deriving both from `auth.ClientFromContext(r.Context())` — never from the request body. This mirrors the existing pattern where ownership fields (client_id via API key) are never client-supplied.
- Keep the `manage-presets` CLI's ability to set `scope=system`/arbitrary `client_id` as CLI-only — the REST DTO simply has no field capable of expressing it, which is a stronger guarantee than a runtime "if scope==system, reject" check (which is one missed-code-path away from a bypass).
- Add a test asserting a request body containing `"scope":"system"` either fails JSON-decode-into-DTO (field doesn't exist, ignored) or — if using a permissive decoder — is explicitly asserted to have no effect on the resulting `CreateParams`.

**Warning signs:**
- REST handler's request struct has a `Scope` or `ClientID` field with a JSON tag.
- Any code path passes `r.Body`-derived data directly into `presets.CreateParams` without an intermediate DTO.

**Phase to address:** Presets REST phase (PRST-V2-01) — this is the single highest-severity item in that phase; should be a named must-have with its own test, not folded silently into "CRUD works."

---

### Pitfall 7: IDOR across clients if preset ownership isn't derived solely from the authenticated context

**What goes wrong:**
A client calls `GET/PUT/DELETE /v1/presets/{id}` (or `{name}`) for a preset ID/name that belongs to a different client. If the handler looks up the preset by ID alone and only checks `scope=user` without also filtering by the authenticated client's own `client_id`, one client can read, modify, or deactivate another client's presets — a direct violation of the project's "404 not 403, no cross-client leak" convention already established for jobs (`GET /v1/jobs/{id}` → 404 on cross-client access, Phase 1).

**Why it happens:**
`internal/presets/repo.go`'s existing methods (`Get`, `List`, `Update`, `Deactivate`) already take `clientID *uuid.UUID` as an explicit parameter and bake the ownership filter into the SQL `WHERE` clause (matching `Resolve`'s no-leak design) — so the repo layer is actually already safe by construction, IF every call site passes the authenticated client's ID and never a request-supplied one. The pitfall is entirely at the REST handler boundary: if a future "get by id" convenience method is added that takes only `id uuid.UUID` (no `clientID` parameter) for operator/debugging convenience, and the REST handler is wired to that method instead of the ownership-scoped one, the IDOR reappears despite the repo's existing design.

**How to avoid:**
- REST handlers must call `Get`/`Update`/`Deactivate` with `clientID` sourced exclusively from `auth.ClientFromContext(r.Context())`, exactly mirroring `internal/jobs`'s existing cross-client 404 pattern — never accept or trust a `client_id` from the URL, query string, or body.
- If any `id`-only repo method is added for the CLI's operator convenience, it must live in a clearly-separate code path that the REST handler physically cannot reach (e.g. only called from `cmd/manage-presets`), not a shared "helper" the HTTP layer might be tempted to call directly.
- Reuse the exact `ErrNotFound`-for-everything semantics already documented in `presets.go` (D-03: nonexistent/inactive/cross-client all indistinguishable) — the REST handler's error mapping must translate `presets.ErrNotFound` to HTTP 404 uniformly, never 403, matching the existing jobs-handler convention.

**Warning signs:**
- Any REST handler reads `client_id` from `r.URL.Query()`, a path parameter, or the JSON body instead of the auth context.
- A new repo method's signature omits `clientID` "for convenience."

**Phase to address:** Presets REST phase (PRST-V2-01) — test explicitly: client A cannot read/modify/deactivate client B's preset, asserting 404 (not 403, not 200).

---

### Pitfall 8: veraPDF's JVM-per-invocation cost silently regresses job latency and the CI e2e budget

**What goes wrong:**
Replacing the current worker-side `OutputIntent` structural sanity check with real veraPDF ISO 19005 validation, if invoked as a fresh CLI process per PDF/A job (`java -jar verapdf...` or the `verapdf` wrapper script), adds JVM startup overhead (hundreds of ms to low seconds depending on classpath size) PLUS actual validation time to every single PDF/A-producing job. At small scale this is invisible; at the CI e2e tier — which already runs a fixed matrix of live conversions inside a 25-minute job cap — every additional PDF/A job absorbs this cost, and CI machines are typically slower/more contended than a dedicated worker host, making the regression show up in CI before it's noticed in production.

**Why it happens:**
The existing PDF/A implementation (v1.3 Phase 14) deliberately chose the cheap structural check specifically to avoid this exact cost ("veraPDF = Java-стек в контейнере воркера; для внутренних клиентов достаточно структурного маркера" — a Key Decision explicitly deferring this). Verified externally: veraPDF's own REST-service design (`veraPDF/veraPDF-rest`, official Docker image) exists specifically to amortize this JVM startup cost across many requests by keeping the JVM warm as a long-running daemon rather than re-invoking the CLI per file — which is strong external validation that per-invocation JVM cost is a known, real problem the veraPDF project itself designed around.

**How to avoid:**
- Do NOT shell out to a fresh `verapdf` CLI process per job from `document-worker` (which would repeat the exact CLI-per-call pattern the veraPDF project's own REST-service design exists to avoid). Prefer one of: (a) run veraPDF in its own long-lived server-mode container (`verapdf/rest` image or a custom always-on JVM process) that `document-worker` calls over a local HTTP/socket interface per job, keeping the JVM warm across jobs; or (b) if staying CLI-based for simplicity, explicitly measure and budget the added per-job latency against `DOCUMENT_ENGINE_TIMEOUT` and the CI e2e 25-minute cap before committing to the approach.
- Whichever approach is chosen, add a CI e2e timing check (or at minimum, note the new PDF/A job's wall-clock delta in the e2e log) so a future JVM-startup regression is visible rather than silently eating into the fixed 25-minute budget — this directly extends the project's own documented CI-timing discipline (`-timeout` overrides chosen deliberately per "Pitfall 7" referenced in `ci.yml`'s own comments).
- Treat this as an explicit "new engine-class-like resource" decision analogous to the LibreOffice/chromium worker isolation precedent (separate container, resource limits) rather than adding a JVM dependency inline into the existing `document-worker` image, which would also blow up that image's size (Pitfall 9 below).

**Warning signs:**
- CI e2e job duration creeps upward after this phase lands, without an isolated measurement showing why.
- `document-worker`'s image size grows by hundreds of MB (JRE + veraPDF jar) with no isolation from the LibreOffice footprint already in that container.

**Phase to address:** veraPDF phase (DOCV3-01) — architecture decision (daemon vs CLI-per-job) must be made and load-tested BEFORE wiring it into the hot path; CI timing impact should be an explicit acceptance check for the phase.

---

### Pitfall 9: veraPDF false "fail" on structurally-valid-but-exotic PDF/A outputs turns transient document quirks into terminal job failures

**What goes wrong:**
Real ISO 19005 validation is stricter and more detailed than the current structural sanity check, and different veraPDF parser backends can disagree on edge cases (verified: an open veraPDF-library issue documents the GreenField vs PDFBox parsers producing DIFFERENT PDF/A-2b conformance verdicts for the same file over a CID TrueType font subset / missing CIDSet descriptor entry). If LibreOffice's PDF/A export occasionally produces output that veraPDF flags as non-conformant on some narrow technicality (font subsetting edge cases are a known friction point), and the worker treats any veraPDF failure as an unconditional job failure, previously "good enough" jobs will start failing terminally in production with no operator-visible distinction between "genuinely broken PDF" and "veraPDF found a picky ISO nit LibreOffice's engine will always trip."

**Why it happens:**
The existing project convention for genuinely broken conversions is straightforward "mark failed, webhook the client" — but that convention was built for engine crashes and content-validation rejections, not for a validation step whose severity is graded (veraPDF validation profiles distinguish rule severity levels; not every failed rule is equally "the PDF is unusable"). Wiring `verapdf --flavour <PDF/A-2b> ... ; if nonzero/non-compliant: mark failed` without a severity policy conflates "technically imperfect" with "broken," and because LibreOffice's PDF/A export is a third-party black box, some rate of exotic-but-usable output is a near-certainty on real internal documents over time.

**How to avoid:**
- Choose upfront which veraPDF verdict states are terminal-fail vs which are recorded-but-non-blocking: at minimum, distinguish "compliant" / "non-compliant with only Warning-severity rule violations" / "non-compliant with Error-severity rule violations" if veraPDF's validation profile output exposes rule severity (verify against the specific validation profile's rule metadata before committing to a threshold).
- Record full veraPDF validation detail (rule ids, verdict) in `job_events` regardless of the pass/fail decision, so a later policy tightening doesn't require re-running historical jobs to see what would have failed.
- Add this severity-policy decision as an explicit, documented `Key Decision` (matching this project's existing decision-log discipline) rather than letting "veraPDF said no" silently become the new terminal-fail bar without discussion.
- Test against at least one real LibreOffice-produced PDF/A-2b output from the existing v1.3 fixtures to confirm the chosen policy doesn't immediately regress the "verified" v1.3 PDF/A conversions into new terminal failures.

**Warning signs:**
- No severity/threshold decision documented; code simply checks `exitCode != 0` or a boolean `isCompliant` field.
- Existing v1.3 live-verified PDF/A fixture files, when re-validated with real veraPDF, come back non-compliant — this is a canary that must be checked BEFORE shipping, not discovered after.

**Phase to address:** veraPDF phase (DOCV3-01) — severity policy is a design deliverable of this phase, verified against existing PDF/A fixtures before merge.

---

### Pitfall 10: Full CFB decompression/traversal reopens the exact DoS class the project already fail-closed rejects

**What goes wrong:**
`internal/convert/olecfb.go` currently does a deliberately minimal 8-byte magic check specifically to AVOID parsing the CFB directory. DOCV3-02 requires parsing the CFB directory to distinguish encrypted-OOXML from legacy-binary — but a full or naive CFB parser is a well-documented DoS surface: compound-file directory structures use sector-chain/FAT-like linked lists, and a maliciously crafted file can encode a **cycle in the directory chain**, causing a naive traversal to loop forever (verified: this is a real, currently-open vulnerability class — `openmcdf`'s GHSA-jxpf-xq2m-q525 is exactly "infinite loop denial of service via crafted CFB directory cycle" in a mainstream CFB library; Apache POI has multiple historical CVEs, e.g. CVE-2012-0213/CVE-2017-12626, for OOM/infinite-loop on malformed CFB/CDF structures). Since this parser sits in the API's pre-storage validation path (same trust boundary as magic-bytes sniffing — untrusted bytes, before any storage write), an unbounded/cyclic parse here is a request-time DoS against the API process itself, not just the worker.

**Why it happens:**
"Parse the directory to tell the two cases apart" sounds like a small, contained addition compared to "parse a whole file format," but directory-only CFB parsing still requires walking a FAT-like sector chain and a directory-entry tree, which is exactly where the known DoS bugs live (not in the payload streams, which this parser correctly never touches) — the temptation is to reach for a general-purpose CFB library (e.g. a Go port with wider format support) rather than hand-rolling the minimum bounded logic actually needed, importing a much larger, less-audited attack surface than the question's own framing ("parse ONLY the directory, never decompress") anticipates.

**How to avoid:**
- Hand-roll (do not adopt a general third-party CFB library — matches this project's zero-new-deps bias, see Pitfall 12) a minimal, bounded directory-only reader: cap the maximum number of sectors/directory entries walked (derived from the file's declared header size, itself bounds-checked against the actual `io.ReaderAt` length before trusting it), and track visited sector indices in a bitset/set to detect and immediately fail-closed-reject any repeat visit (cycle) rather than looping.
- Never follow the FAT/mini-FAT chains into stream content — the parser's job is exclusively "does the root storage contain an `EncryptionInfo`/`\x06DataSpaces` stream (encrypted) vs does it look like a legacy binary format's expected top-level streams (`WordDocument`/`Workbook`/`PowerPoint Document`)?" — both are directory-entry-name checks, never requiring the mini-FAT stream-reassembly logic where most real-world CFB parser bugs live.
- Any read of a declared length/offset/count field from the file MUST be bounds-checked against the actual buffer/reader length before being used to size an allocation or drive a loop bound — mirrors this project's existing decompression-bomb precedent (Phase 7's own-parser-not-shell-out choice for image dimensions) applied to CFB structure fields instead of image headers.
- Fuzz-test the new parser (Go's built-in `go test -fuzz`) against the directory-parsing entry point specifically, seeded with the legitimate legacy/encrypted fixtures already in the v1.3 test suite plus deliberately corrupted variants (truncated header, self-referential sector index, oversized declared sector count) before this ships.

**Warning signs:**
- New code imports a general external CFB/OLE2 parsing library instead of hand-rolling the minimal directory walk.
- No cycle-detection/visited-set exists in the sector-chain-walking loop.
- No fuzz target or malformed-input test cases beyond the two "happy path" fixtures (legacy, encrypted).
- Any loop bound is derived purely from a value read out of the file itself without also being capped against the actual file size.

**Phase to address:** CFB phase (DOCV3-02) — this is the highest-severity item in that phase given the directly-cited external CVE precedent; fuzzing should be a phase-exit gate, not optional polish.

---

### Pitfall 11: CFB encrypted-detection false positives / weakening the existing fail-closed default

**What goes wrong:**
Splitting the current single "reject both" 422 into two distinguished 422s ("password-protected" vs "legacy format") creates a third, more dangerous failure mode if the detection logic is wrong in the permissive direction: a file that superficially looks like it has encryption markers (or lacks the specific stream names the parser checks for) could be misclassified, and if that misclassification is ever used to ALLOW a "not actually encrypted, safe to convert" path rather than just choosing which 422 message to show, the project's hard-won fail-closed guarantee (D-05's "one 422 on both, without parsing the CFB directory" was itself explicitly the fail-closed-safe choice) regresses to accepting some previously-rejected inputs.

**Why it happens:**
The current Key Decision log entry states the reason the single-422 approach was chosen was PRECISELY to avoid needing a real CFB parser and its correctness risk. DOCV3-02 reverses that decision, and it's easy for the new parser's "unknown/unrecognized CFB substructure" case to be handled as a default accept-and-attempt-conversion (since the new code path naturally has more branches: encrypted vs legacy vs "neither pattern matched") rather than folding back to the same generic reject the old code always did.

**How to avoid:**
- The new parser must have exactly three outcomes, and the default/unknown case must map to REJECT: (1) confidently encrypted → 422 "password-protected"; (2) confidently legacy-binary → 422 "unsupported legacy format"; (3) anything else (unrecognized structure, ambiguous, partially-parsed) → generic 422 fail-closed reject, IDENTICAL in effect to today's single-422 behavior — never a path that proceeds to conversion or storage write.
- Never let CFB-directory parsing become a de-facto "is this actually convertible" allow-decision; its only job is choosing which REJECTION message to show. The `Converter`/`Registry` contract is unchanged: no converter is ever registered for OLE-CFB inputs, full stop.
- Test the "ambiguous/ill-formed but not clearly either case" branch explicitly (not just the two clean fixtures) to confirm it still 422s.

**Warning signs:**
- New code has any branch where CFB-detected input reaches the storage-write or job-creation path.
- The "unrecognized substructure" case is unhandled (falls through) rather than an explicit reject branch.

**Phase to address:** CFB phase (DOCV3-02) — pair directly with Pitfall 10's fuzz tests; both should be verified by the same test matrix.

---

### Pitfall 12: New Java/veraPDF dependency and MCP SDK choice bypass the project's zero-new-deps review discipline

**What goes wrong:**
This project has an explicit, repeatedly-honored bias against new dependencies (Phase 7's own-parser-instead-of-`golang.org/x/image`, Phase 4's magic-bytes-not-shell-out, the zero-new-Go-deps precedent throughout `go.mod`'s currently tight dependency list). Both veraPDF (an entire Java runtime + jar, not a Go library) and the MCP SDK (`modelcontextprotocol/go-sdk`, a brand-new first-party Go dependency) are exactly the kind of addition this project has previously avoided or deferred — if either is added without going through the same explicit "why this, why not zero-dep" review the codebase applies elsewhere, it's a silent philosophy break that later contributors won't have context for.

**Why it happens:**
Both additions are genuinely justified (veraPDF is the ONLY credible ISO 19005 conformance validator — there is no zero-dep equivalent, unlike image-dimension sniffing which had a feasible hand-rolled alternative; the MCP Go SDK is the official reference implementation, not a third-party convenience lib) — but "justified" still requires the explicit Key Decision entry this project always writes, not silent acceptance.

**How to avoid:**
- Write an explicit Key Decision entry for both: veraPDF (Java, containerized separately — not vendored into `document-worker`'s existing image, matching the LibreOffice/chromium container-isolation precedent) and the MCP SDK choice (official `modelcontextprotocol/go-sdk` vs `mark3labs/mcp-go`, per SEED-003's own framing) — with rationale for why NO zero-dep alternative exists, mirroring the phrasing style already used for e.g. the decompression-bomb decision.
- Confirm the MCP SDK addition doesn't transitively bloat `go.mod` for the OTHER binaries (`api`, `worker`, etc.) — it should be an import isolated to `cmd/mcp-server` and whatever shared client code it uses, not something that forces every binary in the module to carry MCP-protocol transitive dependencies. Verify with `go mod why` after the phase.

**Warning signs:**
- No Key Decision entry added to `PROJECT.md` for either dependency by phase-end.
- `go.mod`'s existing binaries (`api`, `worker`, `document-worker`) show new transitive dependencies traceable to the MCP SDK import after `go mod tidy`.

**Phase to address:** Both MCP server phase and veraPDF phase — each phase's planning step should explicitly name this as a review checkpoint, not the coding step.

---

## Technical Debt Patterns

| Shortcut | Immediate Benefit | Long-term Cost | When Acceptable |
|----------|-------------------|-----------------|------------------|
| CLI-per-job veraPDF invocation instead of daemon/server mode | Simpler wiring, no new long-running container | JVM startup cost paid on every job; regresses CI e2e timing and production job latency at scale | Only as an explicit, measured interim step with a documented follow-up to daemonize before wider rollout — never as the final v1.5 shape without a timing measurement |
| Sharing `presets.CreateParams` directly as the REST JSON DTO | Less boilerplate, fewer structs | Reopens mass-assignment (Pitfall 6) the moment `Scope`/`ClientID` become client-writable | Never — always use a narrower request DTO for the REST layer |
| `convert_file` blocking without progress notifications for "fast enough" image jobs | Simple implementation, passes manual testing | Silent time-bomb: first slow document/chromium job during a busy queue period times out the MCP client unpredictably | Never acceptable past a throwaway prototype — must be fixed before the MCP phase's exit criteria |
| Adopting a general third-party CFB parsing library instead of hand-rolling the bounded directory reader | Faster to implement, "someone else already solved sector-chain parsing" | Imports the exact DoS-prone code the project is trying to avoid, and breaks the zero-new-deps bias | Never, given the project's explicit precedent (Phase 4/7) of hand-rolling parsers for this exact reason |

## Integration Gotchas

| Integration | Common Mistake | Correct Approach |
|-------------|-----------------|-------------------|
| MCP client (Claude Code / other hosts) | Assuming a fixed wall-clock timeout (e.g. "60s, so keep calls short") | Design around the documented idle-window model (30 min stdio / 5 min HTTP) and use progress notifications to reset it, not to race a fixed clock |
| veraPDF (Java process) | Shelling out fresh per job like the existing `os/exec` engine pattern (`internal/convert/exec.go`) without accounting for JVM startup, unlike the lightweight `vips`/`soffice`/`chromium` CLI calls this pattern was designed around | Isolate veraPDF as its own long-lived service (matches LibreOffice/chromium container-isolation precedent) or explicitly budget/measure the per-job JVM cost before committing to CLI-per-job |
| `manage-presets` CLI vs new REST endpoint | Letting the two diverge in validation semantics because REST was built against a hand-rolled request struct instead of the shared `internal/presets` repo/domain logic | Both CLI and REST call the identical `internal/presets.Repo` methods and the identical `optscheck.go` re-validation logic; only the request-decoding/DTO layer differs (and REST's DTO is deliberately narrower, per Pitfall 6) |
| Rate limiting on new `/v1/presets` endpoints | Forgetting to wire the existing `ratelimit.PerClient`/`ByIP` chi middleware onto the new router group, since it's easy to mount new routes outside the existing middleware chain when adding a new resource | New `/v1/presets` routes must sit inside the same `auth.Middleware` → `ratelimit.PerClient` chain as `/v1/jobs`, verified by an explicit test hitting the rate limit on the new endpoint (mirroring existing jobs-endpoint rate-limit tests) |

## Performance Traps

| Trap | Symptoms | Prevention | When It Breaks |
|------|----------|------------|-----------------|
| CLI-per-job veraPDF JVM startup | Document-worker job duration creeps up specifically for PDF/A-target jobs; CI e2e tier duration grows | Daemonize veraPDF (server mode) or explicitly measure/budget the added latency against `DOCUMENT_ENGINE_TIMEOUT` and the CI e2e 25-min cap | Becomes visible almost immediately in CI (fixed small fixture set × added fixed JVM-start cost per invocation), well before production scale |
| `convert_file` blocking poll loop with tight interval | High Postgres/API load from an MCP server aggressively polling `GET /v1/jobs/{id}` every few hundred ms across many concurrent agent sessions | Backoff the poll interval (mirrors the image queue's own backoff precedent: 2s/5s/15s) and rely on progress notifications for liveness, not poll frequency | Noticeable once more than a handful of concurrent MCP sessions are converting documents simultaneously |
| Unbounded CFB directory/sector walk | API process CPU pegged / request hangs on a crafted upload | Bounded, cycle-detected directory-only parse (Pitfall 10) | A single malicious/malformed upload triggers it — this is a correctness bug, not a scale threshold |

## Security Mistakes

| Mistake | Risk | Prevention |
|---------|------|------------|
| API key echoed into MCP tool error text | Credential exposure into LLM context/transcripts/logs outside the API's own trust boundary | Fixed, non-leaking error mapping in the MCP HTTP client wrapper, mirroring `internal/api/handlers.go`'s existing discipline |
| Agent-supplied filesystem paths trusted without containment check | Path traversal / arbitrary file read-write from a compromised or confused agent | `filepath.Abs` + root-containment check + symlink rejection on every path the MCP server touches |
| `scope`/`client_id` fields present in the presets REST request DTO | Mass-assignment → privilege escalation to system scope or cross-client writes | Narrow REST DTO with no scope/ownership fields; server derives both from `auth.ClientFromContext` |
| CFB parser accepting "unrecognized structure" as a pass-through | Regresses the project's fail-closed guarantee for OLE-CFB inputs | Explicit three-way outcome (encrypted / legacy / generic-reject-default), never a fourth "proceed anyway" branch |
| General third-party CFB library import | Inherits known DoS bug classes (directory-chain cycles, OOM on malformed length fields) into a pre-storage, pre-auth-adjacent validation path | Hand-rolled, bounded, cycle-detected, fuzz-tested directory-only reader; no stream/mini-FAT reassembly code at all |

## UX Pitfalls

| Pitfall | User Impact | Better Approach |
|---------|-------------|-------------------|
| `convert_file` silently hangs with no feedback during a slow document job | Agent/developer perceives the tool as broken or the session as frozen | Progress notifications on every poll tick, describing current job status (queued/active) |
| Presets REST error messages differ in wording/shape from the existing `manage-presets` CLI or the jobs API's `writeError` convention | Confusing, inconsistent API surface for the same underlying concept | Reuse `writeError`'s exact JSON shape (`{"error": "..."}`) and the same no-leak 404 semantics already used across `/v1/jobs` |
| New "distinguished" CFB 422s use inconsistent/ambiguous wording between "password-protected" and "legacy format" | Client-side error handling can't reliably branch on the difference | Define stable, documented error codes/messages for both 422 sub-cases as part of this phase's contract, not just free-text differences |

## "Looks Done But Isn't" Checklist

- [ ] **`convert_file`:** Often missing progress notifications on slow (document/chromium-class) jobs — verify by forcing a real document conversion through the MCP tool, not just an image conversion, and confirming no idle-window timeout occurs.
- [ ] **MCP error handling:** Often missing a check that the API key never appears in any tool-visible error string — verify with a forced-failure test (bad upstream response) asserting the key substring is absent from the result.
- [ ] **Presets REST:** Often missing a negative IDOR test (client A can't touch client B's preset) and a mass-assignment test (`scope`/`client_id` in payload has no effect) — verify both exist as automated tests before merge.
- [ ] **veraPDF integration:** Often missing a re-check against the EXISTING v1.3 PDF/A fixtures — verify those known-good outputs still pass under real veraPDF validation before shipping the stricter check.
- [ ] **CFB directory parser:** Often missing fuzz/malformed-input coverage beyond the two happy-path fixtures (legacy, encrypted) — verify a fuzz target exists and has run for a nontrivial corpus/time budget, and that a crafted cyclic-chain input is an explicit test case.
- [ ] **CI e2e timing:** Often missing an explicit before/after duration comparison when a new engine-adjacent process (veraPDF) enters the hot path — verify the e2e job's wall-clock duration is checked against the 25-minute cap with the new step included, not assumed fine because the local sandbox was fast.

## Recovery Strategies

| Pitfall | Recovery Cost | Recovery Steps |
|---------|----------------|-----------------|
| `convert_file` blocking timeouts discovered in production | LOW | Add progress notifications and/or a documented max-duration fallback to `get_job_status`; no data-layer changes needed since job state already lives in Postgres |
| API key found leaking in error text | LOW-MEDIUM | Patch the error-mapping call site; rotate the exposed client API key via the existing two-slot rotation mechanism (`manage-clients add-key`/`revoke`) since a leaked key must be treated as compromised |
| Mass-assignment/IDOR found in presets REST after ship | MEDIUM | Tighten the DTO/ownership derivation; audit `job_events`-equivalent history (or add preset audit logging if absent) to check whether any client actually exploited the gap before the fix landed |
| veraPDF found flakily failing previously-good jobs in production | MEDIUM | Introduce the severity-policy distinction retroactively (Pitfall 9) using recorded veraPDF rule-level output if it was logged even during the unpolicied period; if not logged, must re-run affected jobs' outputs through veraPDF again from S3 (TTL permitting) |
| CFB parser DoS/cycle found in production | HIGH | Requires an emergency patch + likely a temporary revert to the old single-422 behavior while a bounded/fuzzed replacement is prepared, since this sits pre-auth-adjacent in the API request path and directly affects service availability |

## Pitfall-to-Phase Mapping

| Pitfall | Prevention Phase | Verification |
|---------|-------------------|----------------|
| Blocking timeout past MCP idle window (P1) | MCP server phase | Live test: force a document/chromium-class job through `convert_file`, confirm no client-side timeout, confirm `NotifyProgress` calls observed |
| API key leak into tool results (P2) | MCP server phase | Forced-failure unit test asserting key substring absent from all tool error paths |
| Path traversal via agent-supplied paths (P3) | MCP server phase | Test with `../`-traversal and absolute-path inputs against both `convert_file` and `download_result` |
| Giant file bytes in tool results (P4) | MCP server phase | Code review + test asserting tool results carry paths/URLs, never file content, above a trivial size |
| Stdout corruption (P5) | MCP server phase | Grep for `fmt.Print*` in `cmd/mcp-server`; pipe stdout through a JSON-line validator during manual smoke test |
| Mass-assignment on presets REST (P6) | Presets REST phase | Test: payload with `scope`/`client_id` fields has zero effect on resulting preset's actual scope/owner |
| IDOR across clients on presets REST (P7) | Presets REST phase | Test: client A's request against client B's preset id/name returns 404, not 403, not 200 |
| veraPDF JVM-cost regression (P8) | veraPDF phase | Explicit before/after CI e2e wall-clock measurement with the new validation step included |
| veraPDF false-fail severity policy (P9) | veraPDF phase | Re-validate existing v1.3 PDF/A-2b fixtures under real veraPDF before merge; documented severity threshold Key Decision |
| CFB directory-parse DoS (P10) | CFB phase | Fuzz target covering the directory-walk entry point; explicit cyclic-chain crafted-input test |
| CFB fail-closed default weakening (P11) | CFB phase | Test: ambiguous/unrecognized CFB structure still returns generic 422, never proceeds to conversion |
| New-dependency review bypass (P12) | Both MCP server phase and veraPDF phase | `PROJECT.md` Key Decisions entry present for each dependency by phase-end; `go mod why` confirms no unwanted transitive bleed into other binaries |

## Sources

- MCP idle-window/timeout behavior: [Claude Code MCP docs](https://code.claude.com/docs/en/mcp), live GitHub issues on anthropics/claude-code (#17662, #22542, #47076, #65643, #44006) — MEDIUM-HIGH confidence (cross-referenced multiple independent bug reports plus docs)
- Stdio stdout-corruption failure mode: [MCP official debugging docs](https://modelcontextprotocol.io/docs/tools/debugging), ruvnet/claude-flow issue #835, dirmacs/daedra issue #4 — HIGH confidence (official docs + multiple independent real-world reports)
- Go SDK progress notifications (`NotifyProgress`, `GetProgressToken`): [pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp), [go-sdk protocol docs](https://github.com/modelcontextprotocol/go-sdk/blob/main/docs/protocol.md) — MEDIUM confidence (official SDK docs, not independently executed against a running server)
- veraPDF server/REST mode amortizing JVM cost: [veraPDF/veraPDF-rest](https://github.com/veraPDF/veraPDF-rest), [verapdf/rest Docker Hub](https://hub.docker.com/r/verapdf/rest) — HIGH confidence (official project's own architecture choice)
- veraPDF parser-dependent verdict divergence (GreenField vs PDFBox): [veraPDF-library issue #1253](https://github.com/veraPDF/veraPDF-library/issues/1253) — MEDIUM confidence (single documented issue, real but not exhaustive)
- CFB directory-cycle DoS precedent: [openmcdf GHSA-jxpf-xq2m-q525](https://github.com/openmcdf/openmcdf/security/advisories/GHSA-jxpf-xq2m-q525) — HIGH confidence (official security advisory)
- Apache POI historical CFB/CDF DoS CVEs (CVE-2012-0213, CVE-2017-12626): [cvedetails.com Apache POI DoS list](https://www.cvedetails.com/vulnerability-list/vendor_id-45/product_id-22766/opdos-1/Apache-POI.html) — MEDIUM confidence (aggregator listing, cross-referenced with IBM security bulletins in the same search)
- Codebase conventions (fail-closed 404/no-leak, error-mapping discipline, rate-limit middleware ordering, zero-new-deps precedent, guarded transitions): `internal/api/handlers.go`, `internal/auth/middleware.go`, `internal/ratelimit/ratelimit.go`, `internal/presets/repo.go`, `internal/presets/presets.go`, `internal/convert/olecfb.go`, `.planning/PROJECT.md` Key Decisions — HIGH confidence (direct repository inspection)
- CI e2e budget structure (25-min cap, 4-tier pipeline): `.github/workflows/ci.yml` — HIGH confidence (direct repository inspection)
- SEED-003 design sketch (thin MCP wrapper, blocking `convert_file`, SDK choice): `.planning/seeds/SEED-003.md` — HIGH confidence (direct repository inspection)

---
*Pitfalls research for: OctoConv v1.5 (MCP Access & Document Fidelity)*
*Researched: 2026-07-13*
