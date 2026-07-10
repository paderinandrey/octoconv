# Architecture Research

**Domain:** Async file-conversion service — extending an existing engine-class/queue-per-class Go architecture (chi + asynq + Redis + Postgres + S3/MinIO) with cross-format document conversion, `opts` plumbing, OLE-CFB pre-flight rejection, a third (chromium) engine class, and webhook-consumer decoupling.
**Researched:** 2026-07-10
**Confidence:** HIGH (all findings grounded in direct reads of the current `main` codebase; two load-bearing external claims — LibreOffice CLI PDF/A filter syntax, headless Chromium Docker sandboxing — verified via WebSearch, MEDIUM confidence, multiple sources agree)

This is a **subsequent-milestone** architecture doc for v1.3 "Document Class v2". It does not re-derive the existing system (chi API → Postgres-first double write → asynq engine-class queue → worker → converter registry → S3, all already shipped and documented in CLAUDE.md/PROJECT.md). It focuses exclusively on how the five v1.3 features graft onto that system, with concrete file-level integration points.

## Standard Architecture (current shipped shape — unchanged parts)

```
┌───────────────────────────────────────────────────────────────────────┐
│ API (cmd/api, internal/api)                                           │
│  handleCreateJob: Sniff → SniffContainer(zip) → EngineFor → Dimensions │
│  → Upload(S3) → jobs.Repo.Create (Postgres-first) → queue.EnqueueX     │
├───────────────────────────────────────────────────────────────────────┤
│ Redis / asynq — one queue per engine class: image, document, webhook  │
├───────────────────────────────────────────────────────────────────────┤
│  cmd/worker (image)         cmd/document-worker (document)            │
│  mux: image:convert         mux: document:convert                     │
│  mux: webhook:deliver  ←──────────── ACCIDENTAL: only image worker    │
│  + reconciler.Sweeper                consumes webhook:deliver (SEED-2)│
├───────────────────────────────────────────────────────────────────────┤
│ internal/convert: Converter{Pairs, Convert, Engine} + Registry          │
│   LibvipsConverter (image→image, engine="image")                       │
│   LibreOfficeConverter (6 docs→pdf only, engine="document")             │
├───────────────────────────────────────────────────────────────────────┤
│ Postgres (jobs/job_inputs/job_outputs/job_events/webhook_deliveries)   │
│ S3/MinIO (uploads/, results/)                                          │
└───────────────────────────────────────────────────────────────────────┘
```

### Component Responsibilities (existing, for reference)

| Component | Responsibility | File |
|-----------|----------------|------|
| Sniff chain | Magic-byte + ZIP-structural content detection, fail-closed | `internal/convert/sniff.go`, `internal/convert/docsniff.go` |
| Converter registry | `(from,to)` → Converter, `Engine()`/`EngineFor` for queue routing | `internal/convert/convert.go` |
| Worker `process()` | download → `conv.Convert` → upload → record output → mark done | `internal/worker/worker.go:365-426` |
| Reconciler | engine-aware stale-job recovery + webhook-gap sweep, same process as image worker | `internal/reconciler/reconciler.go`, run only from `cmd/worker/main.go` |
| Jobs schema | `jobs.options jsonb NOT NULL DEFAULT '{}'`, `jobs.engine CHECK (... IN ('image','document','av','cad','archive','probe'))` | `internal/db/migrations/0001_init.sql:41-66` |

## Integration Point (a): Output format is no longer always `pdf`

**Good news, verified by direct read:** the worker's output-side plumbing is **already fully generalized** — no change needed there.

`internal/worker/worker.go:396-421` (`process()`) already derives everything from `job.TargetFormat`, not a hardcoded `"pdf"`:
```go
outName := "out." + job.TargetFormat
outKey := storage.OutputKey(job.ID, 0, outName)
h.uploadFrom(attemptCtx, outKey, outPath, convert.MIMEType(job.TargetFormat))
...
h.repo.AddOutput(attemptCtx, job.ID, jobs.Output{Format: job.TargetFormat, ContentType: convert.MIMEType(job.TargetFormat), ...})
```
`convert.MIMEType` (`internal/convert/sniff.go:102-131`) already has entries for all six document formats plus `pdf`, so Content-Type for a docx→odt job is already correct today with zero code change. This is the single biggest simplifying fact for DOC-V2-01: **the API and worker's output-naming/Content-Type layers need no changes.**

**The actual gap is entirely inside `LibreOfficeConverter.Convert`** (`internal/convert/libreoffice.go`), which today hardcodes the PDF path in three places:

1. `Pairs()` (line 21-27) only emits `{format, "pdf"}` — must be extended to emit the cross-format pairs (`docx→odt`, `odt→docx`, `xlsx→ods`, `ods→xlsx`, `pptx→odp`, `odp→pptx`) so `convert.Default.Register` indexes them and `EngineFor`/`Lookup` see them.
2. `filterFor(sourceExt)` (line 76-87) picks a LibreOffice *export* filter name keyed only on the **source** application family, always ending in `_pdf_Export`. It must become target-aware — e.g. `filterFor(sourceExt, targetFormat)` — because the filter name differs by target: `writer8` for odt-target, `"MS Word 2007 XML"` for docx-target, `calc8`/`"Calc MS Excel 2007 XML"`, `impress8`/`"Impress MS PowerPoint 2007 XML"`. This requires a small lookup table keyed on `(sourceApp, targetFormat)`, not just `sourceApp → pdf filter`.
3. The `--convert-to` invocation itself (line 50) is `"--convert-to", "pdf:" + filter` — must become `"--convert-to", targetFormat + ":" + filter`, and the produced-file rename logic (line 62, `workDir/in.<base>.pdf`) must use `.` + targetFormat instead of the hardcoded `.pdf` suffix.

**Output validation — the real answer to "what validates a docx→odt output":** `validatePDF(path)` (line 92-117) unconditionally checks the `%PDF-` magic prefix, which is meaningless for a non-PDF target. The correct generalization reuses infrastructure that **already exists one layer up**: `convert.SniffContainer` (`internal/convert/docsniff.go`), currently only called from `internal/api/handlers.go` at upload time to disambiguate the six ZIP-based office formats. Since `SniffContainer` lives in the same `convert` package as `LibreOfficeConverter`, it is directly callable from the worker side with zero new import surface:

```go
// Sketch — internal/convert/libreoffice.go
func validateDocumentOutput(path, targetFormat string) error {
    fi, err := os.Stat(path)
    if err != nil { return fmt.Errorf("libreoffice: stat output: %w", err) }
    if fi.Size() == 0 { return fmt.Errorf("libreoffice: output is empty") }
    if NormalizeFormat(targetFormat) == "pdf" {
        return validatePDF(path) // keep exact %PDF- check unchanged
    }
    f, err := os.Open(path)
    if err != nil { return fmt.Errorf("libreoffice: open output: %w", err) }
    defer f.Close()
    cr, err := SniffContainer(f, fi.Size())
    if err != nil || cr.Format != NormalizeFormat(targetFormat) || cr.DuplicateRootPart {
        return fmt.Errorf("libreoffice: output missing expected container format %s", targetFormat)
    }
    return nil
}
```
This is a genuine reuse of an existing, already-tested security-relevant primitive rather than a new hand-rolled magic-byte table per ODF/OOXML format — consistent with the project's stated zero-new-deps philosophy (Key Decisions table, PROJECT.md).

**Cross-file coupling this creates (must change together):** `internal/worker/worker.go`'s `terminalLibreOfficeSignatures` list (lines 45-49) pattern-matches on the exact error strings `validatePDF` produces (`"output missing %pdf- magic bytes"`, `"output is empty"`). If `validateDocumentOutput`'s new non-PDF branch produces a different message (e.g. `"output missing expected container format"`), that string **must** be added to `terminalLibreOfficeSignatures`, or a corrupt/mismatched cross-format output will be misclassified as transient and retried pointlessly against `isDocumentTerminal` (`internal/worker/worker.go:115-123`) up to `DOCUMENT_MAX_RETRY` times before finally failing — the same fail-slow bug class `validatePDF` was originally built to prevent.

**Build-order implication:** the `Converter` interface itself (`internal/convert/convert.go`) needs no change — `Pairs()`/`Convert()`/`Engine()` already support arbitrary `(from,to)` pairs and an opts-aware signature. This work is entirely internal to `internal/convert/libreoffice.go` (+ its test file) and must land **before** any cross-format pair is registered in `internal/convert/converters.go`, since registering an untested pair would silently start producing unvalidated LibreOffice output.

## Integration Point (b): `opts` plumbing (API → jobs table → `Converter.Convert`)

**Current state, verified:** the DB column already exists and is inert. `jobs.options jsonb NOT NULL DEFAULT '{}'::jsonb` is in the original schema (`internal/db/migrations/0001_init.sql:55`), but:
- `jobs.CreateParams` and `jobs.Job` (`internal/jobs/jobs.go`) have no `Opts` field.
- `Repo.Create`'s INSERT (`internal/jobs/repo.go:87-93`) never writes `options`.
- `Repo.Get`'s SELECT (`internal/jobs/repo.go:294-299`) never reads `options`.
- `internal/api/handlers.go`'s `handleCreateJob` never reads an `opts` form field.
- `internal/worker/worker.go:404` calls `conv.Convert(attemptCtx, inPath, outPath, nil)` — **hardcoded `nil`**, discarding whatever `job.Opts` would be even if it existed.
- `LibreOfficeConverter.Convert`'s `opts` parameter is literally named `_` (line 34) — actively discarded.

So the full chain needs five coordinated changes, all mechanical given the DB column already exists (no new migration needed for storage — only for the `engine` CHECK constraint, see (d)/(e) below):

1. **`internal/jobs/jobs.go`** — add `Opts map[string]any` to both `Job` and `CreateParams`.
2. **`internal/jobs/repo.go`** — `Create` inserts `options` (marshal `p.Opts`, default to `{}` if nil, mirroring the existing `detail`-marshal pattern in `transition()`); `Get` selects `options` into a `[]byte`/`json.RawMessage` and unmarshals into `Job.Opts`.
3. **`internal/api/handlers.go`** — parse an optional `opts` multipart field (`r.FormValue("opts")`, a JSON-encoded string, mirroring how `callback_url` is optional-and-validated at lines 219-225). Given the project's consistent fail-closed posture (unknown macro/mismatch/dimension → 422, never silently ignored), `opts` should be validated against a **closed allow-list of recognized keys** for this milestone (just the PDF/A key, e.g. `{"pdf_a": true}` or `{"pdf_a_variant": "1b"}`) — an unrecognized key or wrong-typed value should 422, not pass through silently to the engine. This validation belongs in `internal/api/handlers.go` (or a small new `internal/api/opts.go` mirroring `callbackurl.go`'s single-purpose-file convention), not in the worker, so bad input is rejected before any storage write — same principle already applied to format-pair/dimension/zip-bomb checks.
4. **`internal/worker/worker.go:404`** — change `conv.Convert(attemptCtx, inPath, outPath, nil)` to `conv.Convert(attemptCtx, inPath, outPath, job.Opts)`. `job` is already loaded via `h.repo.Get` before `process()` is called (both `HandleImageConvert` and `HandleDocumentConvert`), so no new DB read is needed.
5. **`internal/convert/libreoffice.go`** — `Convert`'s opts parameter stops being discarded; when `NormalizeFormat(target) == "pdf"` and `opts["pdf_a"]` (or equivalent) is set, the `--convert-to` filter string gains a `FilterOptions` JSON suffix. **Verified via WebSearch (MEDIUM confidence, multiple independent sources agree — vmiklos.hu blog, LibreOffice help docs, Ask LibreOffice):** the CLI syntax is
   ```
   soffice --convert-to 'pdf:writer_pdf_Export:{"SelectPdfVersion":{"type":"long","value":"1"}}' in.docx
   ```
   where `SelectPdfVersion` value `1` selects PDF/A-1b (value `0` is plain PDF). This must be quoted as a single shell argument — since `runCommand` (`internal/convert/exec.go`) already builds `args []string` and calls `exec.Command` directly (no shell interpolation), the JSON-with-embedded-double-quotes can be passed as one `args` element with **no shell-escaping risk** (no `/bin/sh -c` in the exec path) — this is actually safer than the naive shell-quoted examples found via search, and requires no change to `exec.go`.

**Why opts validation must be closed-allow-list, not passthrough:** the project's constraint set (internal-only clients, no untrusted third parties) lowers but does not eliminate risk — `opts` flows unvalidated all the way to a CLI invocation of `soffice`. An unbounded/free-form JSON blob accepted at the API and forwarded into filter-options JSON risks either LibreOffice CLI parse errors surfacing as confusing engine failures, or (lower likelihood but non-zero) unexpected FilterData keys triggering undocumented LibreOffice behavior. A closed key allow-list validated at the API layer (same posture as the macro/zip-bomb/dimension checks already in `handleCreateJob`) is the only approach consistent with existing conventions.

**Build-order implication:** items 1-4 (plumbing) are prerequisite infrastructure with zero engine-specific logic and should land as their own unit before item 5 (PDF/A-specific filter logic), and before the chromium engine work, since HTML→PDF's own opts (if any future ones are needed, e.g. page size/margins) would reuse the exact same plumbing.

## Integration Point (c): OLE-CFB pre-flight detection in `handleCreateJob`'s sniff chain

Current chain in `internal/api/handlers.go:118-177`, in order:
1. `convert.Sniff(file)` — magic-byte match against the 5 registered image formats (`internal/convert/sniff.go`). Legacy binary Office files (`.doc`/`.xls`/`.ppt`, and password-protected OOXML which falls back to CFB container) do **not** match any of these signatures, so `detected == ""`.
2. If `detected == ""`, check the first 4 bytes for the ZIP local-file-header magic `PK\x03\x04` (line 135) and, if matched, run `convert.SniffContainer` to disambiguate OOXML/ODF and check zip-bomb/macro signals.
3. If still `detected == ""` after both, fall through to the generic 422 `"unrecognized file content for " + filename` (line 161-169).

Legacy `.doc`/`.xls`/`.ppt` (and CFB-wrapped password-protected OOXML — Microsoft's "Agile Encryption" wraps an encrypted OOXML payload inside an OLE-CFB container even for `.docx`-named files) begin with the fixed 8-byte OLE-CFB signature `D0 CF 11 E0 A1 B1 1A E1`. Today these fall all the way through to the generic 422 at step 3 **only if** they don't accidentally satisfy some other check — but worse, if the OLE-CFB detection is *not* added, such a file passes `Sniff` as unrecognized, fails the ZIP branch (CFB is not ZIP), and is correctly 422'd today anyway... **except** the milestone's stated problem (`DOC-V2-02` in PROJECT.md: "запароленные/legacy бинарные doc/xls/ppt получают чёткий 422 на входе, а не невнятное падение soffice по таймауту") implies these files are *currently* passing the extension/mismatch check somehow and reaching the worker, or that the desired end-state is a **specific, diagnostic** rejection message rather than today's generic "unrecognized content" — worth flagging as a build-time verification item.

**Where it slots in:** as a **third detection branch**, inserted at the same nesting level as the existing ZIP branch (between it and the final generic-422 fallback), reading the same prefix bytes already fetched via `file.ReadAt`. Recommended shape — a new file mirroring `docsniff.go`'s single-purpose convention, e.g. `internal/convert/olecfb.go`:

```go
var oleCFBMagic = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

// IsOLECFB reports whether r begins with the OLE Compound File Binary
// signature — the container format for legacy .doc/.xls/.ppt and for
// password-protected "Agile Encryption" OOXML. Never a supported target;
// this is a fail-closed pre-flight rejection, not a Sniff/EngineFor pair.
func IsOLECFB(r io.ReaderAt) bool {
    var buf [8]byte
    n, _ := r.ReadAt(buf[:], 0)
    return n == 8 && bytes.Equal(buf[:], oleCFBMagic)
}
```
Called from `internal/api/handlers.go` right after the ZIP-branch's `if detected == ""` block (same `file.ReadAt` receiver already in scope, since `file` is the `multipart.File` which implements `io.ReaderAt`), producing a **distinct** log reason and message from the generic "unrecognized content" case — e.g. `reason=legacy_or_encrypted_document` / `"legacy or password-protected document format not supported"` — so operators/clients can tell this apart from a truly-garbage upload. This deliberately does **not** go through `convert.Default.EngineFor` (no converter is ever registered for this "format" — it is unconditionally rejected, same treatment as `HasMacro`/zip-bomb today), so no `Converter`/`Registry` change is needed.

**Why this must NOT be added as a `sniff.go` `signatures` table entry:** that table's contract (`Sniff` returns a *supported, registered* format name) is different from "detected but explicitly rejected." Reusing it would require either registering a fake `"olecfb"` format with no converter (breaking `EngineFor`'s fail-closed default-case assumption in `handleCreateJob`, line 271-278, which today only ever expects `EngineFor` to return values a real `Converter.Engine()` produced) or special-casing `sniff.go` for a non-convertible signature, which conflates two different roles (detect-a-supported-format vs. detect-and-reject-a-known-bad-format). Keeping it as a separate, explicit check in `handleCreateJob` mirrors how `HasMacro`/zip-bomb rejections are already handled inline rather than through the registry.

**Build-order implication:** independent of (a)/(b)/(d)/(e) — pure API-layer addition, no DB/queue/worker changes, no dependency on cross-format pairs landing first. Can be built and shipped first or in parallel with everything else.

## Integration Point (d): Webhook consumer decoupling topology

**Current state (SEED-002 confirmed by direct read):** `internal/worker/worker.go`'s `HandleWebhookDeliver` is a `Handler` method usable by any process, but only `cmd/worker/main.go:85` registers it (`mux.HandleFunc(queue.TypeWebhookDeliver, h.HandleWebhookDeliver)`) and claims the `webhook` queue capacity (`Queues: map[string]int{queue.QueueImage: 2, queue.QueueWebhook: 1}`, line 89). `cmd/document-worker/main.go` explicitly documents the opposite choice (lines 50-53: `signingSecret` is accepted but "inert," "document-worker neither delivers nor signs webhooks (D-06 — cmd/worker remains the sole webhook consumer)"). Deploying only `document-worker` (or a future `chromium-worker`) while `worker` (image) is down silently stops all webhook delivery — the exact SEED-002 defect.

Important mechanism fact that shapes all three options: **asynq is a pull-based work queue, not pub/sub** — multiple processes registering the same task type on the same queue name **share capacity, they do not duplicate execution** (a given task is dequeued by exactly one consumer). This means any topology change here is safely additive/incremental — a new consumer can be introduced alongside the old one with zero risk of double-delivery, and the old registration removed afterward once the new one is proven live. `webhook_deliveries`'s per-job `asynq.Unique` lock (already in place, `internal/queue/queue.go:104-114`) provides the separate, independent duplicate-prevention guarantee this doesn't need to worry about.

**Option 1 — dedicated `cmd/webhook-worker` binary (recommended):**
- New `cmd/webhook-worker/main.go`, near-identical skeleton to `cmd/document-worker/main.go` but registering only `mux.HandleFunc(queue.TypeWebhookDeliver, h.HandleWebhookDeliver)` on `Queues: map[string]int{queue.QueueWebhook: N}`.
- New `Dockerfile.webhook-worker`: no `libvips-tools`, no LibreOffice, no `tini` — this process never forks an external engine (no `runCommand`/process-group concerns at all), so it needs only `ca-certificates` on top of `debian:bookworm-slim` (or could even move to `distroless/static` given `CGO_ENABLED=0` static builds — worth flagging as a possible further hardening, out of scope to decide here). Lightest container of the whole fleet.
- New compose service `webhook-worker`, same `depends_on`/env shape as `document-worker` minus `DOCUMENT_*` vars, plus the required `WEBHOOK_SIGNING_SECRET` (this process becomes the **sole** required consumer of that secret going forward).
- `worker.NewHandler`'s existing constructor signature (`internal/worker/worker.go:139`) is reused as-is — `webhook-worker` still needs a `*convert.Registry` argument even though it never calls `process()`/`conv.Convert`; passing `convert.Default` is harmless (same pattern `document-worker` already follows for fields it doesn't exercise).
- **Migration path (safe, given the pull-queue fact above):** add `cmd/webhook-worker` and deploy it consuming `QueueWebhook` *before* removing the registration from `cmd/worker/main.go` — both can run simultaneously with zero double-delivery risk — then remove `mux.HandleFunc(queue.TypeWebhookDeliver, ...)` and the `QueueWebhook` entry from `cmd/worker/main.go`'s `Queues` map, and drop `webhook.NewRepo`/`webhook.NewDeliverer`/`signingSecret` wiring from `cmd/worker/main.go` (the image worker no longer needs `WEBHOOK_SIGNING_SECRET` at all after this — mirroring `document-worker`'s existing accepted-but-inert pattern, except here it can be removed entirely since nothing calls `HandleWebhookDeliver` from that binary anymore).
- **Trade-off vs. the existing one-binary-per-engine-class pattern:** webhook delivery isn't an "engine class" in the `Converter.Engine()` sense — there's no `Converter` implementation, no format pair, no `EngineFor` entry — so this new binary is architecturally a *different kind* of worker (a cross-cutting delivery process, not an engine-class consumer). It's still the best fit because it's the only option that fully satisfies "any subset of engine-workers deployed must not lose webhooks" — with this topology, webhook delivery has **zero** dependency on any engine worker's liveness.
- **Reconciler stays where it is.** `reconciler.Sweeper`'s webhook-gap sweep (RECON-04, `internal/reconciler/reconciler.go:222-252`) only *enqueues* onto `QueueWebhook` via `queue.Client` — it never consumes. No change needed to move it; it's already decoupled from the consumer side through Redis. (It remains true that only `cmd/worker/main.go` runs the sweeper at all per the existing D-05 comment — that's an orthogonal, already-settled decision, not something this milestone needs to revisit unless the image worker's role changes further.)

**Option 2 — every engine worker also consumes the webhook queue:**
- Each of `cmd/worker`, `cmd/document-worker`, and the future `cmd/chromium-worker` registers `TypeWebhookDeliver` on its own mux alongside its own engine type.
- Pro: as long as *any one* worker (of any class) is up, webhooks flow — arguably stronger redundancy than a single dedicated binary (which is itself a single point of failure unless it's scaled to ≥2 replicas).
- Con: reverses `document-worker`'s current explicit design decision (D-06) and requires provisioning `WEBHOOK_SIGNING_SECRET` + `webhook.Repo`/`Deliverer` wiring into every worker binary, including the new chromium worker — multiplying the secret's distribution footprint across N containers instead of 1, and re-coupling engine-class deploys to webhook-delivery code paths (the exact shape of coupling SEED-002 is trying to eliminate, just spread wider instead of removed). Also dilutes/complicates per-process `Concurrency`/`Queues` capacity planning as engine classes grow (already 3, `PROJECT.md`'s Out of Scope notes more are coming later) — every new engine class binary has to also reason about webhook-queue capacity, not just its own.

**Option 3 — API-process consumer (webhook delivery folded into `cmd/api`):**
- Con, decisively: `cmd/api/main.go` today has **zero** asynq-server/task-consumer code — it is purely an HTTP-serving process (`http.Server` + a separate metrics `http.Server`, both with their own graceful-shutdown sequences already). Adding an `asynq.Server` here means a third independent shutdown sequence to coordinate, and couples API-process resource sizing to webhook-delivery throughput even though the two have unrelated scaling profiles (API scales with inbound request rate; webhook delivery scales with completed-job rate × external-endpoint latency, and can legitimately want a much higher concurrency for slow/misbehaving client callback endpoints without touching API capacity at all). It also breaks the clean one-process-one-job convention that has held without exception since v1.0 (`cmd/api`, `cmd/worker`, `cmd/document-worker`, `cmd/migrate`, `cmd/manage-clients` — each has exactly one responsibility).

**Recommendation:** Option 1. It is the only topology that fully and unambiguously satisfies the SEED-002 requirement, it is safely rollout-able with zero double-delivery risk (pull-queue semantics), and it extends — rather than contradicts — the existing engine-class-worker precedent (new `cmd/`, new minimal `Dockerfile.*`, new compose service), even though webhook delivery is not itself an engine class.

## Integration Point (e): Chromium HTML→PDF engine — container, safety model

**Fits the existing `Converter` abstraction with zero interface changes.** A new `internal/convert/chromium.go` implementing `Converter{Pairs() []Pair{{"html","pdf"}}, Convert(...), Engine() string { return "html" }}` shells out through the **already-hardened** `runCommand` (`internal/convert/exec.go`) exactly like `LibvipsConverter`/`LibreOfficeConverter` do — no changes needed to `exec.go`'s `Setpgid`+`SIGKILL`-on-timeout process-group handling, since headless Chromium's `--print-to-pdf` CLI mode is a single foreground process (verified: unlike LibreOffice's `oosplash`→`soffice.bin` fork chain, which required `tini` as PID 1 in `Dockerfile.document-worker`, a plain `chromium --headless --print-to-pdf=out.pdf in.html` invocation does not fork a detached child the way `oosplash` does — but this should be confirmed live during implementation the same way `tini`'s necessity was confirmed live for LibreOffice in the existing codebase, per that Dockerfile's own comment "confirmed live (09-02)").

**New engine-class scaffolding needed (mirrors v1.2's document-engine pattern exactly, called out as the ready template in PROJECT.md's Key context):**
- `internal/db/migrations/0005_html_engine.sql` (or similar) — **the `jobs.engine` CHECK constraint is a closed list** (`CHECK (engine IN ('image', 'document', 'av', 'cad', 'archive', 'probe'))`, `internal/db/migrations/0001_init.sql:47-48`) and does **not** include `html`/`chromium`. This is a hard blocker — the third engine class cannot be created without an `ALTER TABLE jobs DROP CONSTRAINT ... ADD CONSTRAINT ... CHECK (engine IN (..., 'html'))` migration landing first (naming: given the existing list already reserves `av`/`cad`/`archive`/`probe` as placeholders for later engine classes with no `html` slot, a new value must be added, not substituted).
- `internal/queue/queue.go` — new `TypeHTMLConvert`/`QueueHTML` constants, `NewHTMLConvertTask`, `htmlRetrySchedule`/`HTMLRetryDelay`, `HTMLUniqueTTL` — mirroring the image/document pairs exactly (each new engine class has so far always gotten its own retry schedule + derived unique-TTL, not shared constants — this is the established convention, not incidental duplication, per the codebase's own comments about `IMAGE_MAX_RETRY` vs `DOCUMENT_MAX_RETRY` being deliberately different budgets for different per-attempt costs).
- `internal/queue/client.go` — `EnqueueHTMLConvert`, wired into `internal/api/api.go`'s `Enqueuer` interface and `internal/api/handlers.go`'s engine-switch (lines 265-278, currently `image`/`document` only) and `internal/reconciler/reconciler.go`'s engine-switch (lines 131-149, currently `image`/`document` only, `default` fail-closed-skips unrecognized engines — this default-skip behavior is exactly why `html` must be added as an explicit `case`, not left to fall through).
- New `cmd/chromium-worker` binary + `Dockerfile.chromium-worker` + compose service, following `cmd/document-worker`'s shape (own `*_ENGINE_TIMEOUT`/`*_WORKER_CONCURRENCY` env vars, own resource limits in compose, `USER nobody`).

**Container requirements (fonts, sandboxing, resource limits) — verified via WebSearch, MEDIUM confidence, multiple independent sources agree (oneuptime.com Docker/Chrome guide, hexdocs ChromicPDF docs, docker-html-to-pdf reference project):**
- **Fonts:** headless Chromium ships no fonts of its own on a minimal Debian base; install a font package explicitly (mirroring `Dockerfile.document-worker`'s `fonts-crosextra-*`/`fonts-liberation2` pattern) — at minimum `fonts-liberation` for common Latin text; if the HTML corpus includes non-Latin scripts, additional font packages (e.g. `fonts-noto-cjk`) would need adding, which inflates image size non-trivially — worth flagging as a scope question for planning (which scripts must render correctly) rather than assuming.
- **Sandboxing:** Chromium's own internal process sandbox expects either a SUID-root helper binary or unprivileged user namespaces; both are typically unavailable in a `debian:bookworm-slim` container running as non-root `nobody` (the same non-root posture this project already uses for `worker`/`document-worker`, for the same "shells out to untrusted-input engines" reason stated in both existing Dockerfiles). Sources confirm the two real options: (1) `--no-sandbox` — the flag virtually every containerized-Chrome guide defaults to, accepting loss of Chromium's *own* internal sandbox layer while relying on Docker's container boundary as the outer isolation layer (consistent with how this project already relies on the container boundary + non-root user + process-group kill rather than an in-process sandbox for LibreOffice/libvips); or (2) a custom `seccomp` security profile (`security_opt: seccomp=./chromium.json`) that grants exactly the syscalls Chromium's sandbox needs while keeping it enabled — meaningfully more secure but adds a new artifact (a seccomp JSON profile) to maintain, and no such per-service `security_opt` precedent exists yet in `docker-compose.yml`. Given the project's stated risk posture (internal clients only, not adversarial third parties, existing accepted-residual-risk decisions logged in PROJECT.md's Key Decisions table for e.g. resource-exhaustion via complex documents), `--no-sandbox` plus the existing container/process/resource-limit isolation stack is the pragmatic default, consistent with existing choices — the seccomp-profile route is a valid escalation if the risk tolerance for this specific engine is judged higher than for libvips/LibreOffice (arguable, since Chromium's attack surface from arbitrary attacker HTML/CSS/JS is considerably larger than libvips' or LibreOffice's from a document).
- **Resource limits:** existing convention (both `worker` and `document-worker` set `cpus: "2.0"`/`memory: 1g` in compose) should extend to `chromium-worker` — Chromium is materially more memory-hungry per invocation than either existing engine, so this ceiling likely needs raising for this specific service rather than copy-pasted verbatim; also add `--disable-dev-shm-usage` (documented as close to mandatory for containerized Chrome, since Docker's default `/dev/shm` size of 64MB is too small for Chromium's shared-memory usage and causes crashes) alongside `--disable-gpu` (no GPU in this deployment target) as baseline CLI flags for the `Convert()` invocation.

**HTML input safety differs fundamentally from ZIP-based formats — this is the milestone's own stated highest-risk item, and rightly so:**
The existing document-format safety model (`docsniff.go`'s zip-bomb/macro checks) protects against a **passive** attack surface — a crafted archive that misbehaves the *parser* (decompression bomb) or smuggles executable content that would run *if* opened interactively (macros, unconditionally rejected). HTML is an **active** attack surface: the engine *itself* (a full browser rendering engine) will execute arbitrary CSS/JS and attempt arbitrary outbound network fetches by design — `<img src="http://169.254.169.254/...">`, `<link rel="stylesheet" href="https://...">`, `<script src="...">`, CSS `url(...)`, `fetch()`/`XMLHttpRequest` from injected JS, `<iframe src="http://internal-service/...">` — every one of these is a live SSRF vector at *render* time, structurally the same class of risk the project already built a dedicated guard for (`internal/api/callbackurl.go`'s `validateCallbackURL`/`isBlockedIP`), except here the untrusted payload is the entire HTML/CSS/JS document rather than a single URL string, so a single upfront URL-validation function cannot cover it — the check has to happen at render time, inside Chromium, against every resource load it attempts.

The milestone's own framing ("offline rendering") is the right target, but achieving it requires more than just "don't add a proxy" — a plain `chromium --headless --print-to-pdf` invocation with default flags **will** attempt real network fetches for every absolute `http(s)://` reference in the document, because vanilla `--print-to-pdf` mode has no request-interception hook (that capability only exists via the DevTools Protocol, i.e. driving Chromium through Puppeteer/Playwright-style tooling — one of the WebSearch sources for this milestone explicitly notes "using Puppeteer... provides better flexibility... allows intercepting and cancelling network requests" as the alternative to relying on bare `--print-to-pdf`). Two concrete mitigation paths, in order of robustness, both compatible with the project's zero-new-deps philosophy (neither requires a new language runtime or SDK):
1. **Network-level allow-list at the container/compose layer** — the `chromium-worker` container only needs egress to Postgres, Redis, and S3/MinIO hosts (the same three dependencies every worker needs); a custom Docker network + firewall rule (or, in a future Kubernetes deployment, a `NetworkPolicy`) that denies all other egress achieves genuine offline rendering without any code change, and is the most bulletproof option because it holds even against JS-driven fetches that a URL-parsing approach could miss.
2. **Chromium CLI-level network black-holing** — flags such as `--host-resolver-rules="MAP * 127.0.0.1"` (force all DNS resolution to a local sinkhole) are a documented technique for making a headless Chromium instance effectively unable to reach the real network, without needing Puppeteer/CDP request-interception or a firewall change; this is lower-effort than (1) but is a Chromium-internal control, not an infrastructure-level guarantee, so it is weaker in the same way relying purely on `validateCallbackURL` (application-level) would be weaker than a network firewall for the webhook SSRF case.
Given the project already accepted an analogous trade-off for webhook SSRF (an application-level check plus a narrow, explicit opt-out, rather than a network firewall), a hybrid of both — CLI flags as the first line of defense, network-level egress restriction on the `chromium-worker` container as defense-in-depth given the qualitatively larger attack surface (full JS engine vs. a single outbound POST) — is the recommended target, with the exact mechanism (compose network rules vs. iptables vs. a sidecar) an implementation detail to resolve during planning rather than research.

**Build-order implication:** the `jobs.engine` CHECK-constraint migration is a hard prerequisite for *any* chromium-engine work (job creation will fail at the DB layer otherwise) and should land in the same wave as the `internal/queue` scaffolding (new task type/queue/retry schedule) — before the converter implementation or the container/Dockerfile work, since those can be developed and unit-tested against the registry/queue plumbing independently of a working Chromium binary. The network-egress-restriction mechanism for offline rendering should be decided before (not after) the `chromium-worker` Dockerfile/compose service is finalized, since it likely shapes the container's network configuration (custom bridge network, explicit allow-listed hosts) rather than being a post-hoc addition.

## Data Flow — what changes end to end

### Cross-format document job (docx → odt)
```
handleCreateJob: Sniff→"" ; PK\x03\x04 branch ; SniffContainer→docx ; EngineFor("docx","odt")→"document"
  → Upload(S3, uploads/{id}/0-file.docx) → Repo.Create(engine="document", opts={}) → EnqueueDocumentConvert
document-worker: process() → registry.Lookup("docx","odt") → LibreOfficeConverter.Convert(..., "odt", opts)
  → soffice --convert-to odt:writer8 ... → produced workDir/in.odt
  → validateDocumentOutput(path,"odt") → SniffContainer(output) confirms cr.Format=="odt"
  → upload results/{id}/0-out.odt, Content-Type=application/vnd.oasis.opendocument.text (already correct, MIMEType unchanged)
  → AddOutput(Format="odt") → MarkDone → EnqueueWebhookDeliver (now consumed by cmd/webhook-worker, not cmd/worker)
```

### PDF/A export (opts-driven)
```
handleCreateJob: opts form field {"pdf_a":"1b"} validated against closed allow-list
  → Repo.Create(..., Opts={"pdf_a":"1b"})
document-worker: process() → job.Opts carried through → conv.Convert(ctx,in,out,job.Opts)
  → LibreOfficeConverter reads opts["pdf_a"] → --convert-to 'pdf:writer_pdf_Export:{"SelectPdfVersion":{"type":"long","value":"1"}}'
  → validateDocumentOutput(path,"pdf") → existing %PDF- check (unchanged)
```

### HTML→PDF (new engine class)
```
handleCreateJob: Sniff→"" (html has no binary magic bytes worth trusting) ; no ZIP/CFB match
  → detect via extension + a minimal structural check (e.g. leading "<!DOCTYPE"/"<html" after whitespace-trim,
    itself an open question for planning — html is unlike every other supported format in having no reliable
    magic-byte signature, an existing-pattern gap worth flagging, not resolving, here)
  → EngineFor("html","pdf")→"html" → Upload → Repo.Create(engine="html") → EnqueueHTMLConvert
chromium-worker: process() → registry.Lookup("html","pdf") → ChromiumConverter.Convert
  → chromium --headless --disable-gpu --disable-dev-shm-usage --no-sandbox
             --host-resolver-rules="MAP * 127.0.0.1" --print-to-pdf=out.pdf file://in.html
  → validatePDF(out.pdf) (existing %PDF- check, reused verbatim — pdf target unchanged from document engine)
  → AddOutput(Format="pdf") → MarkDone → EnqueueWebhookDeliver
```

## Anti-Patterns to Avoid

### Anti-Pattern: Registering a cross-format pair before generalizing output validation
**What people do:** add `{docx, odt}` etc. to `LibreOfficeConverter.Pairs()` and wire the `--convert-to` target-aware filter, but leave `validatePDF` unconditionally called at the end of `Convert`.
**Why it's wrong:** every non-PDF-target job would fail validation on a correctly-produced file (the `%PDF-` check would reject valid odt/docx/xlsx output), turning every cross-format conversion into a guaranteed terminal failure.
**Instead:** land `validateDocumentOutput`'s format dispatch (reusing `SniffContainer`) and the matching `terminalLibreOfficeSignatures` update in `internal/worker/worker.go` in the *same* change as the first cross-format pair registration.

### Anti-Pattern: Treating `opts` as a passthrough blob
**What people do:** accept any JSON object in the `opts` form field and forward it verbatim into `Converter.Convert`.
**Why it's wrong:** breaks the project's consistent fail-closed-at-the-API-boundary discipline (format mismatch, zip-bomb, macro, dimension-limit are all rejected with a specific 422 before storage/DB writes) and turns unvalidated client input directly into engine CLI arguments.
**Instead:** validate `opts` against a closed key/value allow-list in `internal/api` before it ever reaches `jobs.CreateParams`.

### Anti-Pattern: Letting every worker consume the webhook queue "just in case"
**What people do:** register `TypeWebhookDeliver` on every engine worker's mux for redundancy.
**Why it's wrong:** re-couples webhook delivery (and its signing-secret distribution) to every engine worker's deploy lifecycle — the opposite of SEED-002's goal — and dilutes per-process capacity planning as engine classes grow.
**Instead:** one dedicated `cmd/webhook-worker`, decoupled from every engine class.

### Anti-Pattern: Trusting `--print-to-pdf`'s default network behavior for "offline rendering"
**What people do:** assume that because the API never uploads HTML anywhere but S3/local disk, Chromium won't reach the network.
**Why it's wrong:** vanilla headless Chromium `--print-to-pdf` will fetch every absolute-URL resource referenced by the HTML/CSS/JS at render time; "offline" is not the default, it must be actively enforced (DNS sinkhole flag and/or network egress restriction).
**Instead:** treat this exactly like the existing `callback_url` SSRF guard — an explicit, tested control, not an assumption.

## Integration Points Summary

| Boundary | Change | Files |
|----------|--------|-------|
| API sniff chain ↔ OLE-CFB rejection | New third detection branch, own error path | `internal/api/handlers.go`, new `internal/convert/olecfb.go` |
| API ↔ jobs.opts | New optional form field + closed-allowlist validation | `internal/api/handlers.go`, new `internal/api/opts.go` (suggested) |
| jobs.Opts ↔ Postgres | New struct field + INSERT/SELECT columns (column already exists) | `internal/jobs/jobs.go`, `internal/jobs/repo.go` |
| worker.process() ↔ Converter.Convert | Replace hardcoded `nil` opts with `job.Opts` | `internal/worker/worker.go:404` |
| LibreOfficeConverter ↔ target format | Target-aware filter table, generalized rename, generalized validation | `internal/convert/libreoffice.go` |
| worker terminal-classification ↔ new validation error strings | Must update in lockstep | `internal/worker/worker.go` (`terminalLibreOfficeSignatures`) |
| jobs.engine CHECK constraint ↔ new engine class | New migration required before any `html`-engine job can be created | new `internal/db/migrations/000X_html_engine.sql` |
| queue/reconciler/api engine-switches ↔ new engine class | Explicit new `case "html"` in each fail-closed switch | `internal/api/handlers.go`, `internal/reconciler/reconciler.go`, `internal/queue/client.go` |
| webhook delivery ↔ consumer process | Move `mux.HandleFunc(TypeWebhookDeliver,...)` off `cmd/worker` onto new `cmd/webhook-worker` | `cmd/worker/main.go`, new `cmd/webhook-worker/main.go`, new `Dockerfile.webhook-worker` |
| chromium engine ↔ network egress | New container-level network-restriction decision, not just CLI flags | new `Dockerfile.chromium-worker`, `docker-compose.yml` |

## Sources

- Direct reads of current `main` branch: `internal/convert/{convert,converters,libreoffice,docsniff,sniff,dimensions,exec}.go`, `internal/api/{handlers,api,callbackurl}.go`, `internal/worker/worker.go`, `internal/jobs/{jobs,repo}.go`, `internal/queue/{queue,client}.go`, `internal/reconciler/reconciler.go`, `internal/webhook/deliver.go`, `internal/db/migrations/0001_init.sql`, `cmd/{api,worker,document-worker}/main.go`, `Dockerfile.{worker,document-worker}`, `docker-compose.yml` — HIGH confidence, ground truth.
- [Using Parameters from Filters in --convert-to Command Line (Ask LibreOffice)](https://ask.libreoffice.org/t/using-parameters-from-filters-in-convert-to-command-line-with-multiple-parameters/114631) — MEDIUM confidence, PDF/A `SelectPdfVersion` FilterOptions JSON syntax
- [What is Miklos hacking – Improved PDF export options in the command-line and in Online](https://vmiklos.hu/blog/pdf-convert-to.html) — MEDIUM confidence, corroborates FilterOptions CLI syntax
- [PDF Export Command Line Parameters (LibreOffice help)](https://help.libreoffice.org/latest/en-US/text/shared/guide/pdf_params.html) — MEDIUM confidence, official filter-options reference
- [How to Set Up Docker for PDF Generation with Headless Chrome (oneuptime.com)](https://oneuptime.com/blog/post/2026-02-08-how-to-set-up-docker-for-pdf-generation-with-headless-chrome/view) — MEDIUM confidence, `--no-sandbox`/`--disable-dev-shm-usage`/`--disable-gpu` container flags
- [ChromicPDF docs (hexdocs.pm)](https://hexdocs.pm/chromic_pdf/ChromicPDF.html) — MEDIUM confidence, sandbox-vs-seccomp-profile trade-off in Docker
- [docker-html-to-pdf reference project (GitHub)](https://github.com/pinkeen/docker-html-to-pdf) — MEDIUM confidence, dockerized headless-Chrome-to-PDF conventions

---
*Architecture research for: OctoConv v1.3 "Document Class v2" milestone integration*
*Researched: 2026-07-10*
