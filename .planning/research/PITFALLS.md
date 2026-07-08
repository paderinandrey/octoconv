# Pitfalls Research: Adding LibreOffice Document Conversion to OctoConv

**Domain:** Adding a second (heavier, historically flakier) `os/exec`-based conversion engine class — LibreOffice headless — to an existing hardened async job-processing system (Go, asynq/Redis, Postgres-first state machine, guarded transitions, engine-class queue routing).
**Researched:** 2026-07-09
**Confidence:** MEDIUM-HIGH (LibreOffice headless failure modes are extremely well-documented across TDF bugzilla, Gotenberg's production issue tracker, and multiple independent Docker/serverless LibreOffice wrapper projects — verified against >=2 independent sources each; codebase-specific integration gaps below are HIGH confidence, read directly from `internal/reconciler/reconciler.go`, `internal/queue/queue.go`, `internal/worker/worker.go`, `cmd/worker/main.go`)

This supersedes the previous milestone's PITFALLS.md (which covered v1.0/v1.1 hardening — auth, webhooks, reconciler, magic-bytes, observability — all now shipped and validated). This research is scoped entirely to v1.2's document-engine addition.

This research assumes the milestone's stated approach: LibreOffice registers as one more `convert.Converter` in the existing `Registry` (no new Handler/Capability contract), reusing the existing `runCommand` hardened-exec wrapper (`internal/convert/exec.go`), a new `document` asynq queue mirroring `image`'s engine-class routing pattern, and a separate `DOCUMENT_ENGINE_TIMEOUT`.

## Critical Pitfalls

### Pitfall 1: Reconciler is hardcoded to `EnqueueImageConvert` — it will misroute or wrongly exhaust stranded document jobs

**What goes wrong:**
`internal/reconciler/reconciler.go`'s `enqueuer` interface only declares `EnqueueImageConvert` and `EnqueueWebhookDeliver` (lines 42-45), and `sweep()` unconditionally calls `s.enq.EnqueueImageConvert(ctx, j.ID)` (line 127) for **every** job `FindStale` returns, regardless of which engine class the job actually belongs to. `jobs.StaleJob` (repo.go:37) also currently carries only `{ID, Status}` — no format/engine info. If a `document` queue is added without touching the reconciler, any docx/xlsx/pptx job that gets stranded in `queued`/`active` (worker crash, container restart, Redis blip) will be re-enqueued onto the **image** queue as an `image:convert` task. The image worker's `HandleImageConvert` will call `registry.Lookup("docx", "pdf")`, get a miss, and hit the existing terminal-classification string check `strings.Contains(msg, "no converter for")` in `isTerminal()` (worker.go:55) — so the job is marked permanently `failed` with a misleading `engine_error` / "unsupported or corrupted input format" message, even though a live document worker could have processed it correctly. Worse, this consumes one of the job's `MaxRecoveries` reconciler slots on a bug, not a real failure.

**Why it happens:**
The reconciler was built and tested against a single engine class (image). Its enqueuer interface was never designed to be format/engine-aware because there was nothing to route between. Adding a second `Convert*`-shaped task type is an easy thing to forget to wire into the reconciler, because the reconciler's tests/e2e verification historically only exercised the image path (RECON-04/RECON-05 in `.planning/PROJECT.md` were verified for image jobs specifically).

**How to avoid:**
- Extend `jobs.StaleJob` (and the `FindStale` SQL) to also select `source_format, target_format` (or a persisted `engine`/`queue` column — `CreateParams` in `repo.go` already has an `Engine` field, confirm whether it's persisted and queryable).
- Add `EnqueueDocumentConvert` to the reconciler's `enqueuer` interface, and route `sweep()`'s recovery call through `convert.Default.Lookup(sourceFormat, targetFormat)` (or an equivalent queue-name lookup) instead of hardcoding `EnqueueImageConvert`.
- Add a live e2e test that strands a **document** job (kill the worker mid-conversion, or directly flip its row to `active` with an old `updated_at`) and asserts the reconciler recovers it onto the `document` queue, not `image`.

**Warning signs:**
- Grep for `EnqueueImageConvert` outside `internal/worker`/`internal/api` — any reconciler-side hardcoded call site is the smoking gun.
- In staging/load-testing: kill `-9` the worker process mid-document-conversion and watch whether the job eventually completes via `document` queue reprocessing or dies with `no converter for docx -> pdf`.

**Phase to address:**
Must be handled in the same phase that adds the `document` queue/converter — this is not deferrable, since the reconciler already runs continuously in production against `main` and will act on document jobs from the moment they exist, whether or not anyone remembered to update it.

---

### Pitfall 2: LibreOffice can exit 0 while writing an empty, truncated, or otherwise corrupted PDF — the existing `isTerminal`/upload pipeline has no output validation at all

**What goes wrong:**
LibreOffice headless has a long, well-documented history (TDF bugzilla #52125, multiple Ask LibreOffice threads, and independent wrapper-library postmortems) of returning **exit code 0** on conversions that actually failed internally — producing a 0-byte or truncated PDF, or silently dropping content — rather than a clean non-zero exit. This is fundamentally different from libvips' behavior, which the codebase's own comment (`worker.go:28-31`) already notes returns exit code 1 for every failure, requiring stderr-substring classification. LibreOffice can be *worse*: no error at all, on either the exit code or stderr, yet a broken output file. Separately, `internal/worker/worker.go`'s `process()` (lines 245-306) never inspects `outPath` before uploading it — it uploads whatever bytes exist at `outPath` and marks the job `done` as long as `conv.Convert()` returns `nil` and the S3 upload succeeds. A 0-byte or garbage PDF would sail straight through to `MarkDone`, a webhook fired to the client with a "success" `download_url`, and the client would download an unusable file.

**Why it happens:**
The existing error-classification design (`isTerminal`) was built entirely around libvips' behavior (always non-zero exit, message-based terminal/transient split). It implicitly assumed "process succeeded" == "output is valid," which held for libvips in practice but does not hold for LibreOffice.

**How to avoid:**
- Add a generic, engine-agnostic output-sanity check in `process()` (not converter-specific) immediately after `conv.Convert()` succeeds and before `uploadFrom()`: file exists, size > 0, and (for `pdf` targets specifically) the first bytes equal `%PDF-`. This protects every current and future converter, not just LibreOffice, and is cheap (a few bytes read).
- Treat a failed sanity check as a **terminal** error (retrying won't fix a structurally-corrupt LibreOffice run on the same input) — do not let it fall into `isTerminal`'s transient default.
- Additionally define a `terminalLibreOfficeSignatures` list (mirroring `terminalVipsSignatures`) once real stderr text is observed from live LibreOffice runs in this environment — do not assume libvips' signatures generalize; LibreOffice's error vocabulary is completely different (e.g., "Error: source file could not be loaded").
- Log/record the actual output file size in `job_events.detail` on every document conversion (even successes) for at least the first weeks post-launch, to catch a slow-onset pattern of "successful" near-empty PDFs before customers notice.

**Warning signs:**
- Any `done` job whose `job_outputs.size_bytes` is anomalously small relative to a "reasonable minimum PDF" (a few hundred bytes at least) is a red flag worth alerting on.
- Manual smoke test: feed LibreOffice a deliberately-corrupt but well-formed-zip docx (e.g., valid `[Content_Types].xml` but garbage `document.xml`) and confirm the worker does NOT mark the job `done`.

**Phase to address:**
Must be handled in the core LibreOffice-Converter implementation phase — this is a correctness gap in the shared `process()` path, not an edge case to defer. Ship the output-validation check in the same plan that introduces the LibreOffice converter, and make it generic so it also silently hardens the existing image path for free.

---

### Pitfall 3: The user-profile lock is a hard concurrency ceiling that the existing hardened-exec wrapper does not account for — and a SIGKILLed stuck instance can leave a stale lock that poisons *all* subsequent conversions

**What goes wrong:**
LibreOffice headless writes a lock file (`.~lock.<name>#`) tied to its `$HOME`/`UserInstallation` profile directory the moment it starts. A second `soffice --headless` invocation that shares the **same** `$HOME`/profile path while the first is still running will not run a second independent conversion — depending on version/build it will either (a) silently attach to/hang waiting for the already-running instance, or (b) fail outright/crash (LibreOffice bugzilla #82775, #106134: "headless mode does not allow concurrent jobs," "crashes when serving multiple concurrent requests"). This is categorically different from libvips, which is a plain stateless CLI tool with no shared-state singleton behavior — the existing hardened-exec model (spawn one process per job, `Setpgid`, SIGKILL on timeout, reap, done) implicitly assumes each invocation is fully independent, which is exactly the assumption LibreOffice violates under concurrency.

The interaction with the existing timeout-and-SIGKILL machinery (`internal/convert/exec.go`) is the dangerous part: `runCommand` kills the process group with `SIGKILL` on `ctx.Done()` — a hard, un-catchable signal. LibreOffice never gets a chance to run its own lock-file cleanup on exit. If a `soffice --headless` process gets stuck (e.g., on a pathological document) and is SIGKILLed by `DOCUMENT_ENGINE_TIMEOUT`, the stale `.~lock` file (and the crash-marker inside its profile) is very plausibly left behind on the shared filesystem, exactly as documented in multiple LibreOffice bug reports about SIGKILL/crash leaving `.~lock` files that block the *next* invocation from starting at all. If every worker goroutine/process shares one `$HOME`/profile path (the default if `HOME` is not overridden per-invocation), **one hung document poisons the entire document queue** until the lock is manually removed — a total document-conversion outage triggered by a single bad input file, with no automatic recovery (the reconciler would just keep re-enqueuing the job, and it would keep failing/timing out against the same stale lock).

**Why it happens:**
LibreOffice's headless mode was designed around a single long-lived "one office suite per user session" model, not a stateless multi-tenant conversion-worker model. Every wrapper project in the ecosystem (Gotenberg, `unoconv`, various serverless LibreOffice projects) has hit and had to work around this; it is the single most common LibreOffice-in-production pitfall across the ecosystem.

**How to avoid:**
- **Never** run two `soffice --headless` invocations against the same profile directory concurrently. Pass a unique `-env:UserInstallation=file:///<tmpdir>` per invocation, generated per-job (the same `os.MkdirTemp("", "octoconv-"+job.ID.String()+"-")` workDir already created in `process()` is a natural place to derive a per-job profile dir).
- With a per-job unique profile dir, a stale lock from a killed process can no longer poison *other* jobs — it only affects that one job's already-doomed temp directory, which `defer os.RemoveAll(workDir)` in `process()` already cleans up regardless of success/failure. This turns a fleet-wide outage into a single-job failure, which is the correct blast radius.
- Still explicitly cap **concurrent LibreOffice invocations per worker process** at 1 (or a small, deliberately-chosen N) even with per-job profiles — LibreOffice's own internal state (shared memory segments, X11-less rendering backend) has documented instability under true parallelism regardless of profile isolation (see Pitfall 6). Do not rely on `WORKER_CONCURRENCY`/asynq's queue concurrency map alone; it was tuned for cheap libvips processes, not LibreOffice.
- Do not attempt to reuse a single long-lived "listener" `soffice --accept=...` daemon process to avoid per-job cold-start cost (a design several wrapper projects have tried) — it reintroduces exactly the shared-profile/shared-process-state problem this fix avoids, and Gotenberg's own issue tracker documents this as *more* unstable than one-process-per-conversion, requiring considerable extra retry/health-check machinery this codebase does not have.

**Warning signs:**
- A sudden pattern of `DOCUMENT_ENGINE_TIMEOUT` failures across *unrelated* jobs starting at the same moment (rather than one bad document) — check for a shared, non-per-job `HOME`/profile path.
- `find <profile-root> -name '.~lock*'` finding stale lock files that don't correspond to any currently-running `soffice.bin` process.

**Phase to address:**
Must be handled in the core LibreOffice-Converter implementation phase — the per-job unique `UserInstallation` path is a one-line addition to the command args at the same point libvips is invoked, and is far cheaper to build in from day one than to retrofit after a first fleet-wide lock-poisoning incident.

---

### Pitfall 4: The soffice launcher may fork a detached `soffice.bin` that escapes the existing `Setpgid`/process-group-SIGKILL — verify, don't assume, the hardened-exec wrapper actually kills LibreOffice on timeout

**What goes wrong:**
`internal/convert/exec.go`'s doc comment already explicitly names LibreOffice's `soffice.bin` as the reason the process-group-kill design exists ("children such as LibreOffice's `soffice.bin` do not get orphaned/left hanging"), but this has never actually been exercised against a real LibreOffice binary — libvips is a single self-contained process with no forking launcher. Depending on which LibreOffice build/package ends up in the worker image, `/usr/bin/soffice` is either (a) a thin script/binary that directly `exec`s into `soffice.bin` — in which case the PID never changes and `Setpgid` on the original child correctly covers the real worker process — or (b) an `oosplash`-based launcher that **forks** a separate `soffice.bin` child process, a documented pattern in LibreOffice's own mailing-list history ("killing soffice.bin from oosplash") specifically because POSIX signal-forwarding from a launcher to a forked child is inherently racy. If the packaged build forks rather than execs, and the forked child ever calls `setsid`/detaches into its own session (a plausible defensive move for a GUI app trying to survive terminal disconnects), `syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)` will kill the launcher but the real `soffice.bin` doing the actual (possibly stuck) conversion work can survive, orphaned, still holding its profile lock — recreating Pitfall 3's poisoning scenario even with per-job profile dirs, because the orphaned process keeps running indefinitely, consuming CPU/RAM the timeout was supposed to reclaim.

**Why it happens:**
The hardened-exec wrapper's contract ("kill the whole process group") is correct in principle, but its soundness depends on an assumption about the specific binary's process topology that has never been tested against real LibreOffice — it was written speculatively, ahead of this milestone, based on general awareness of `soffice.bin`'s reputation rather than verification against the actual chosen Docker base image's LibreOffice package.

**How to avoid:**
- On the actual runtime base image chosen for the worker (Debian bookworm-slim, matching existing `Dockerfile.worker`), explicitly verify empirically: spawn a `soffice --headless` conversion of a document engineered to hang (e.g., an extremely complex file, or a test-only stub binary that sleeps), let `runCommand`'s timeout fire, and run `ps -ef` / `pgrep soffice` immediately after to confirm **zero** `soffice`/`soffice.bin`/`oosplash` processes remain. Do this as an explicit integration test, not a one-off manual check, since a Debian package upgrade could silently change the launcher topology later.
- Prefer invoking `soffice.bin` directly (bypassing any wrapper/launcher script) if the package layout allows it — several production LibreOffice-conversion wrappers do exactly this specifically to sidestep launcher-forking ambiguity.
- If forking is confirmed, extend the kill to also search for and kill any `soffice.bin` processes whose profile-dir argument matches the job's per-job `UserInstallation` path (belt-and-suspenders), not just the process group.

**Warning signs:**
- Any `docker exec <worker> ps aux` during/after load testing showing accumulating `soffice.bin <defunct>` or long-lived orphaned `soffice.bin` entries after their originating job has already timed out/failed in Postgres.
- Container memory creeping upward over many timeout events without corresponding process count staying flat.

**Phase to address:**
Must be verified in the core LibreOffice-Converter implementation phase, as an explicit test/checklist item before this milestone is considered done — this directly determines whether the project's one and only timeout-safety mechanism (the hardened-exec wrapper the whole architecture already leans on) actually works for the new engine.

---

### Pitfall 5: Running as `USER nobody` with no writable `$HOME`/font cache will produce cryptic, not obviously LibreOffice-related, startup failures

**What goes wrong:**
The current `Dockerfile.worker` runs the worker binary as `USER nobody` with no `HOME` environment variable set and no font packages installed — fine for libvips (a stateless CLI with no profile/cache needs) but not fine for LibreOffice, which on first run needs to create and write a user profile/configuration directory (normally `$HOME/.config/libreoffice`) and, separately, build/read a fontconfig cache. On most minimal base images, `nobody`'s `$HOME` in `/etc/passwd` is `/nonexistent` or similarly unwritable. The failure mode here is not a clean, obvious "permission denied, please chmod" — it is typically one of: `soffice` hanging on startup waiting on a directory it cannot create, a bare non-descriptive fatal crash, or (per the per-job `-env:UserInstallation` fix in Pitfall 3) the same failure just relocated to whatever temp directory is chosen for the profile if that directory itself is not writable by `nobody`.

**Why it happens:**
`nobody` is deliberately a minimal, low-privilege account with no home directory by design — this is exactly why it was already chosen for security (limiting blast radius of untrusted-input engine execution), but LibreOffice, unlike libvips, actually needs a small amount of writable, per-process state to function at all.

**How to avoid:**
- Explicitly set `HOME` for the worker process (or at minimum for each `soffice` invocation via `cmd.Env` in a LibreOffice-specific wrapper around `runCommand`) to a location guaranteed writable by `nobody` — the simplest robust choice is the same per-job `os.MkdirTemp` workDir already created in `process()` (which is created by the worker process itself, so it inherits whatever UID the worker runs as, and is already cleaned up via `defer os.RemoveAll(workDir)`).
- Confirm `/tmp` in the runtime image is world-writable (standard `1777` — true by default on `debian:bookworm-slim`, but worth an explicit assertion/test rather than an assumption, since a future hardening pass to the Dockerfile could tighten this).
- Pre-provision the LibreOffice font cache once (a fontconfig cache build, e.g., `fc-cache -f`) during the Docker **build** stage as `root`, so `nobody` at runtime only ever needs to *read* the cache, never build it from scratch — building it lazily at runtime as an unprivileged user with no writable home is a likely source of the "first request is mysteriously slow/fails, subsequent ones are fine" symptom documented in several LibreOffice-in-Docker projects.
- Explicitly install a font package (at minimum `fonts-liberation` or `fonts-dejavu-core`, both liberally licensed and Debian-packaged) rather than shipping zero fonts — see Pitfall 7 for why this also affects output correctness, not just startup.

**Warning signs:**
- Worker container logs showing `soffice`/LibreOffice invocations that neither succeed nor time out within a normal duration on the very first request after a fresh container start, but behave normally afterward (classic "lazily building a cache it can't persist" signature).
- Any conversion failing identically and immediately regardless of input document — a strong signal the failure is environmental (profile/HOME), not document-specific.

**Phase to address:**
Must be handled in the core LibreOffice-Converter implementation phase, specifically in the `Dockerfile.worker` changes — this is a one-time environment-provisioning cost, cheap to get right up front, expensive to debug after the fact because the resulting errors do not obviously point at "HOME is unwritable."

---

### Pitfall 6: A single malicious/malformed office document can exhaust CPU/RAM well within `DOCUMENT_ENGINE_TIMEOUT` — wall-clock timeout alone does not bound worst-case resource usage the way it effectively does for libvips

**What goes wrong:**
The milestone plan's only stated document-specific safeguard is `DOCUMENT_ENGINE_TIMEOUT` (a larger wall-clock bound than `ENGINE_TIMEOUT`, to accommodate legitimately slow large-file conversions). This is a materially weaker guarantee for LibreOffice than the equivalent timeout is for libvips. libvips processes images with a roughly predictable memory footprint proportional to (already pixel-limited via `MAX_IMAGE_PIXELS`, per the existing decompression-bomb protection) decoded pixel buffers — a stuck/slow libvips job is overwhelmingly a *time* problem, not a *memory* problem, so a wall-clock timeout is a reasonably tight proxy for "this went wrong, kill it." LibreOffice's resource profile under a crafted or pathological document (a spreadsheet with an enormous but sparsely-populated used-range, deeply nested/merged tables, thousands of embedded images or OLE objects, ZIP-based OOXML "quadratic blowup" via highly-repetitive shared-string tables) is a genuine, separate DoS surface: LibreOffice can spike to multiple GB of RSS well *before* the wall-clock timeout fires, especially on a busy CPU where the same clock budget yields less real work done. A timeout still eventually kills the process, but only *after* the container has potentially already been OOM-killed by the kernel (see the shared-container pitfall in Technical Debt Patterns below), which is a much worse failure mode than a clean timeout-triggered `SkipRetry`/`failed` job.

**Why it happens:**
Office document formats (OOXML/ODF) are ZIP containers of deeply-relational XML with no built-in size/complexity ceiling analogous to an image's simple width x height product — the same class of "declared vs. actual size" attack this project already explicitly defended against for images (`MAX_IMAGE_PIXELS`, magic-byte + declared-size validation, Phase 7 in `.planning/PROJECT.md`) has a real analog for spreadsheets/documents (cell count x formula complexity, embedded-image count/size, XML entity/reference nesting) that the current milestone scope does not mention addressing.

**How to avoid:**
- Set a hard **container-level memory limit** on whichever container runs document conversions (the existing `docker-compose.yml` already sets `cpus: "2.0", memory: 1g` on the worker — decide explicitly whether document conversions share this limit or get their own, separately-sized container) so a memory-runaway LibreOffice process is killed by the kernel/cgroup (OOM) well before it can affect co-located jobs, rather than silently degrading host memory pressure.
- At minimum, validate **declared** input complexity cheaply before invoking LibreOffice at all — e.g., reject XLSX/DOCX/PPTX files above a configurable size ceiling (mirroring `MAX_UPLOAD_BYTES`, which already exists at the API layer) and, if feasible without adding a new dependency, a cheap pre-check of the ZIP central directory (already unzipping the file is required anyway to read `[Content_Types].xml`) for absurd nesting/entry counts — the same "detect before you fully process" philosophy already used for image decompression bombs.
- Do not treat `DOCUMENT_ENGINE_TIMEOUT` alone as equivalent protection to what `ENGINE_TIMEOUT` + `MAX_IMAGE_PIXELS` jointly provide for images — the roadmap should explicitly decide whether a memory ceiling / input-complexity check is in scope for this milestone or an accepted, documented residual risk for v1.2.

**Warning signs:**
- Worker container OOM-kill events (visible via `docker inspect`/`OOMKilled: true`, or a spike in container restarts) correlating with document conversions, especially before `DOCUMENT_ENGINE_TIMEOUT` would have fired.
- Document conversion jobs whose engine RSS (if instrumented) grows non-linearly with input file size.

**Phase to address:**
Should be explicitly decided (not silently deferred) in the core LibreOffice-Converter implementation phase: either (a) ship a memory-ceiling/complexity-check alongside the timeout as launch-blocking, or (b) explicitly document it in `.planning/PROJECT.md`'s "Out of Scope"/accepted-risk list the same way CAD and HTML->PDF already are, so it is a deliberate decision rather than a silent gap discovered later via an incident.

---

## Technical Debt Patterns

| Shortcut | Immediate Benefit | Long-term Cost | When Acceptable |
|----------|-------------------|-----------------|------------------|
| Reuse `ENGINE_TIMEOUT`'s attempt-scoped `context.WithTimeout` pattern in `process()` verbatim for `DOCUMENT_ENGINE_TIMEOUT` without re-deriving a document-specific `asynq.Unique` TTL (i.e., copy `ImageUniqueTTL`'s constant/shape instead of computing a genuine `DocumentUniqueTTL(maxRetry, DOCUMENT_ENGINE_TIMEOUT)`) | Saves writing/testing a second derived-TTL function | If `DOCUMENT_ENGINE_TIMEOUT` is meaningfully larger than `ENGINE_TIMEOUT` (which the milestone explicitly says it will be) and the unique-lock TTL is not derived to match, a single long-running LibreOffice attempt can outlive its own dedupe lock while still legitimately in progress, letting the reconciler spawn a second concurrent task for the same job (the exact T-03-10 double-processing race the image path was already hardened against) | Never — this is a straightforward, already-proven pattern (`ImageUniqueTTL`) to replicate with the right constants; there is no good reason to skip it |
| Run the document converter inside the same worker binary/container/`WORKER_CONCURRENCY` pool as image conversions, sharing the existing 2 CPU/1 GiB `docker-compose.yml` resource limits | No new Dockerfile/compose service, fastest to ship | A single heavy or malicious document conversion can starve/OOM the shared container, degrading or failing concurrent image jobs that have nothing to do with the document engine — directly contradicting this project's own documented "engine-class queue routing... so worker pools can scale independently per engine" architectural intent | Acceptable only as an explicit, time-boxed MVP decision for a first internal-only rollout with a known single low-volume consumer (per PROJECT.md's stated context of one concrete internal service needing this now) — should be revisited before any second consumer/higher volume onboards |
| Ship the LibreOffice converter with only `terminalVipsSignatures`-style stderr matching for terminal/transient classification, without an output-file sanity check | Matches the existing `isTerminal` pattern with minimal new code | Silent "success" on empty/corrupted PDFs (Pitfall 2) ships to production and is only discovered when a client complains about an unusable file | Never acceptable as the shipped state — the output-sanity check is cheap and closes a real correctness gap, not a hypothetical one |
| Skip a per-job unique `-env:UserInstallation` profile directory and rely on a single shared LibreOffice profile per worker process | Slightly simpler `Convert()` implementation | A single stuck/killed conversion can leave a stale lock that blocks every subsequent document conversion until manual intervention (Pitfall 3) — a full outage from one bad file | Never — the per-job profile dir is a one-argument addition with no real downside |

## Integration Gotchas

| Integration | Common Mistake | Correct Approach |
|--------------|------------------|--------------------|
| Existing `runCommand` hardened-exec wrapper (`internal/convert/exec.go`) | Assuming its process-group-kill guarantee, written speculatively with LibreOffice in mind but never tested against it, actually holds for the specific LibreOffice package/launcher chosen | Explicitly verify (Pitfall 4) via an integration test that spawns a hung `soffice --headless` call and confirms zero surviving processes after the timeout fires, on the exact base image used in `Dockerfile.worker` |
| Existing `isTerminal()` classifier (`internal/worker/worker.go`) | Reusing `terminalVipsSignatures` string list or assuming LibreOffice's failure vocabulary/exit-code semantics match libvips' (always-non-zero-exit) behavior | Build a separate, LibreOffice-specific terminal-signature list from real observed stderr text, and add the generic output-sanity check (Pitfall 2) that does not depend on exit code or stderr text at all |
| Existing reconciler (`internal/reconciler/reconciler.go`) | Leaving `enqueuer`/`FindStale` hardcoded to the image queue when adding the document queue | Make the reconciler engine/format-aware (Pitfall 1) before the document queue goes live — this is not a "nice to have," it is an existing production component that will act on document jobs immediately |
| Existing `asynq.Unique` TTL derivation (`internal/queue/queue.go`'s `ImageUniqueTTL`) | Hardcoding or copy-pasting the image TTL constant instead of computing `DocumentUniqueTTL` from `DOCUMENT_MAX_RETRY`/`DOCUMENT_ENGINE_TIMEOUT` following the exact same derivation formula | Write `DocumentUniqueTTL` (and a matching `DocumentRetryDelay`/schedule if document retry semantics should differ from image's fast 2s/5s/15s schedule — LibreOffice failures are less likely to be quick network blips and may warrant a slower backoff) |
| Docker base image (`Dockerfile.worker`) | Installing a LibreOffice package without also setting `HOME`, pre-building the fontconfig cache as root, and installing at least one font package | Add `HOME` env / per-job profile dir, `RUN fc-cache -f` in the build/runtime stage as root before dropping to `USER nobody`, and install `fonts-liberation`/`fonts-dejavu-core` explicitly (Pitfall 5, Pitfall 7) |

## Performance Traps

| Trap | Symptoms | Prevention | When It Breaks |
|------|----------|------------|-----------------|
| Shared worker container/goroutine pool for image + document engines | Image job latency/timeouts spike specifically when a large document conversion is also in flight; container memory usage correlates with document job concurrency, not just image job volume | Give the document engine class its own container/deployment (separate `cmd/worker`-style binary or a config flag selecting which queues a given worker process serves) with its own resource limits, matching the project's stated engine-class-isolation intent | Breaks as soon as document conversion volume/size is non-trivial relative to image volume — likely visible even at modest internal usage given LibreOffice's much heavier per-conversion footprint vs. libvips |
| Cold-start cost per conversion (no long-lived LibreOffice listener process) | Every document conversion pays LibreOffice's multi-second startup cost (loading UNO components, building font metrics) on top of actual conversion time | Accept this cost deliberately (it is the safe, well-supported pattern — see Pitfall 3's warning against a shared listener process) rather than "optimizing" toward a persistent `--accept=` daemon that reintroduces shared-state instability | Becomes a real UX/throughput problem only at high sustained document-conversion request rates — for this project's "one internal consumer, real need now" scope, likely acceptable as-is; revisit if volume grows enough to justify a properly-designed worker-pool-of-N-isolated-instances architecture (what Gotenberg itself does) |
| Unbounded per-job profile directories under `/tmp` if a job hangs long enough to be killed by the reconciler rather than the engine timeout | Disk usage on the worker grows if `os.RemoveAll(workDir)` cleanup is ever skipped on a code path (e.g., a panic before the `defer` registers, or a process crash) | Rely on the existing `defer os.RemoveAll(workDir)` pattern already in `process()` (correct as written) but add a periodic sweep/cleanup of orphaned `octoconv-*` temp directories older than some threshold as a defense-in-depth measure, given LibreOffice's higher crash/kill likelihood than libvips | Only manifests after repeated worker crashes/OOM-kills over time — low urgency, but cheap to add alongside the LibreOffice work while the failure mode is fresh in mind |

## Security Mistakes

| Mistake | Risk | Prevention |
|---------|------|------------|
| Treating office documents as "just another file format" with the same threat model already covered by magic-byte validation (Phase 7 of v1.0) | OOXML/ODF documents are ZIP containers of XML — magic-byte + declared-size checks (built for flat raster image formats) do not address ZIP-based resource-exhaustion vectors (nested/repetitive compression, huge shared-string tables) at all; a format-pair check passing does not mean the document is safe to hand to LibreOffice | Add a document-specific pre-check layer (Pitfall 6) distinct from the existing image magic-byte validator — do not assume the prior phase's protections generalize to this format family |
| Allowing LibreOffice to process documents containing macros or external references (remote image/OLE links, DDE fields) without disabling them | LibreOffice headless conversion of a document with macros or a remote-reference field could, depending on configuration, attempt outbound network calls or execute embedded code during "just" a format conversion — a document-borne SSRF/RCE surface this project has no prior experience defending against (the existing SSRF hardening was built entirely around `callback_url` webhook delivery, not document content) | Explicitly invoke LibreOffice with macro execution disabled (`--headless` alone does not guarantee this on all versions — verify the exact CLI flags/registry modifications needed to lock down macro security level via the per-job user profile) and treat this as a launch-blocking security review item, not an afterthought |
| Leaving a stale LibreOffice profile/lock directory world-readable/writable under a shared `/tmp` on a multi-tenant-ish worker container | If profile directories are not job-scoped and cleaned up, one client's converted-document metadata/temp artifacts could theoretically be visible to a subsequent job's LibreOffice process reusing the same profile path | The same per-job unique `UserInstallation` + `os.MkdirTemp` + `defer os.RemoveAll` pattern already recommended for Pitfall 3 also closes this cross-job data-exposure angle as a side effect |

## "Looks Done But Isn't" Checklist

- [ ] **LibreOffice converter registered in `convert.Default`:** Often missing the per-job unique `-env:UserInstallation` argument — verify by grepping the `Convert()` implementation for a per-job temp profile path, not a shared/default one.
- [ ] **Terminal/transient error classification for documents:** Often missing an output-file sanity check (non-zero size, `%PDF-` magic bytes) entirely, relying only on exit code/stderr like the image path — verify with a test that feeds a "successful but empty output" scenario (can be simulated by wrapping/stubbing the engine call in a test) and asserts the job is NOT marked `done`.
- [ ] **Reconciler support for the document queue:** Often missing entirely on a first pass — verify `internal/reconciler/reconciler.go`'s `enqueuer` interface has an `EnqueueDocumentConvert` method and `sweep()` actually calls it for document-format stale jobs, with a live/e2e test analogous to the existing RECON-04/RECON-05 verification.
- [ ] **`DocumentUniqueTTL`/retry schedule:** Often copy-pasted from the image constants instead of independently derived from `DOCUMENT_ENGINE_TIMEOUT`/`DOCUMENT_MAX_RETRY` — verify a dedicated derivation function exists and is unit-tested for monotonicity, mirroring `TestImageUniqueTTL`.
- [ ] **`Dockerfile.worker` environment for LibreOffice under `USER nobody`:** Often missing `HOME`, a pre-built fontconfig cache, and font packages — verify by running the actual built worker image's document conversion path in CI/staging, not just locally as a non-`nobody` developer user (permission and cache issues frequently only reproduce under the real unprivileged runtime user).
- [ ] **Hardened-exec timeout kill actually terminates LibreOffice's real worker process:** Often assumed true by inheritance from the doc comment already in `exec.go` without ever being empirically verified against the real LibreOffice binary — verify with an explicit "kill a hung soffice and check `ps` after" test (Pitfall 4).
- [ ] **Container resource isolation between engine classes:** Often deferred/shared "for now" — verify the roadmap makes an explicit, documented decision (separate container vs. shared) rather than silently inheriting the image worker's existing resource limits.

## Recovery Strategies

| Pitfall | Recovery Cost | Recovery Steps |
|---------|-----------------|------------------|
| Stale profile lock poisoning the document queue (Pitfall 3) discovered in production without the per-job-profile fix already in place | LOW (once diagnosed) | Manually `find`/delete stale `.~lock*` files and any orphaned `soffice.bin` processes on the affected worker container; restart the worker; then ship the per-job `UserInstallation` fix so this cannot recur |
| Reconciler misrouting document jobs onto the image queue (Pitfall 1) already shipped and has failed some real jobs as `engine_error` | MEDIUM | Query `job_events` for `engine_error` failures with `detail.engine_stderr` containing `no converter for` on document-format jobs; these are false failures — manually re-run/re-enqueue them onto the correct document queue after the reconciler fix ships; notify the affected internal consumer if a webhook already fired a false failure |
| Silent empty/corrupted PDF already delivered to a client via webhook before the output-sanity check (Pitfall 2) shipped | MEDIUM | Cross-reference `job_outputs.size_bytes` for anomalously small `done` document jobs in the affected time window, proactively contact the affected internal consumer, and backfill the validation check before re-processing |
| Worker container OOM-killed by a resource-heavy document (Pitfall 6), taking down concurrent image jobs | LOW (operationally, once observed) | The existing transient-error/asynq-retry + reconciler machinery already recovers jobs stranded by a worker crash without code changes — but treat repeated OOM-kills as a signal to expedite the memory-ceiling/isolation fix rather than relying indefinitely on crash-recovery as the safety net |

## Pitfall-to-Phase Mapping

| Pitfall | Prevention Phase | Verification |
|---------|-------------------|----------------|
| 1. Reconciler hardcoded to image queue | Core LibreOffice-Converter implementation phase (launch-blocking) | Live e2e test: strand a document job, confirm reconciler recovers it via the document queue, not image |
| 2. Exit-0-but-corrupt output / no output validation | Core LibreOffice-Converter implementation phase (launch-blocking) | Unit/integration test asserting a job is NOT marked `done` when the engine writes a 0-byte or non-PDF-magic output |
| 3. Profile-lock concurrency + SIGKILL interaction | Core LibreOffice-Converter implementation phase (launch-blocking) | Concurrency test: two simultaneous document conversions on the same worker succeed independently; a killed/timed-out conversion does not block a subsequent one |
| 4. Process-group kill may not catch `soffice.bin` | Core LibreOffice-Converter implementation phase, explicit verification step (launch-blocking) | Integration test: spawn a hung conversion, let the timeout fire, assert zero surviving `soffice*` processes via `ps`/`pgrep` |
| 5. Unprivileged-user `$HOME`/font-cache provisioning | Core LibreOffice-Converter implementation phase, `Dockerfile.worker` changes (launch-blocking) | Build and run the actual worker image as `nobody` in CI, execute a real document conversion, confirm no permission/cache errors on a cold container |
| 6. Resource-exhaustion / DoS via crafted documents | Explicit roadmap decision required in the core phase: either ship a mitigation (memory ceiling + input-complexity pre-check) or formally document as accepted residual risk in `.planning/PROJECT.md`'s Out of Scope, mirroring CAD/HTML->PDF | If deferred: documented and dated; if addressed: load test with a deliberately pathological spreadsheet/document confirming the worker container survives (OOM-kills the conversion, not the whole node) |
| 7. Font-substitution / version-skew output variance | Can be a documented accepted-limitation for v1.2 — not launch-blocking, but must be communicated to the internal consumer | Documented in release notes / API docs that PDF pagination/layout fidelity depends on the container's installed font set, not guaranteed to pixel-match the original authoring application |
| Shared worker container/resource contention between engine classes | Should be an explicit decision in the core phase's design, even if the MVP choice is "share for now" | Roadmap or `.planning/PROJECT.md` Key Decisions entry explicitly recording the choice and its tradeoff, so it isn't accidentally load-bearing/permanent by default |
| Derived `DocumentUniqueTTL`/retry schedule (Technical Debt row) | Core LibreOffice-Converter implementation phase | Unit test mirroring `TestImageUniqueTTL`'s monotonicity/determinism assertions for a new `DocumentUniqueTTL` function |

## Pitfall 7 (non-blocking): Version-Skew and Font-Substitution Output Variance

**What goes wrong:** LibreOffice's rendering/layout engine is not guaranteed to be pixel- or even page-count-stable across minor versions, and — independent of version — a document referencing a font not installed in the conversion environment will be silently substituted with a different font (LibreOffice's own documentation acknowledges font substitution "might result in a broken layout... with unintended line- and page-breaks"). Since the worker container controls exactly which fonts exist (per Pitfall 5, likely a minimal set like `fonts-liberation`/`fonts-dejavu-core`, not the original Microsoft "ClearType"/"Core Fonts for the Web" family many real-world `.docx` files assume), pagination and line-wrapping in the output PDF are very unlikely to exactly match what the document looks like in the client's original authoring application (Word/LibreOffice on their own machine).

**Why it happens:** This is an inherent, structural property of cross-application/cross-environment document rendering, not a bug introduced by this project — every LibreOffice-based conversion service in the ecosystem has this limitation, and none of the wrapper projects surveyed claim to have solved it, only to have installed a broader font set to reduce (not eliminate) the mismatch rate.

**How to avoid / accept:** Install a reasonably broad, redistributable font set (`fonts-liberation` specifically provides metric-compatible substitutes for the most common Microsoft core fonts — Arial/Times New Roman/Courier New equivalents — which meaningfully reduces, though does not eliminate, layout drift) and **pin the LibreOffice package version** in the Dockerfile (not `apt-get install libreoffice` floating-latest) so output does not silently drift across unrelated worker redeploys. Communicate to internal consumers that pixel-perfect layout fidelity to the original authoring application is not guaranteed — this is worth an explicit line in API documentation, not a silent gap.

**Detection:** Not something a warning sign will catch at runtime (it won't throw an error) — the only mitigation is proactive: a small fixed set of "golden" test documents (using common real-world fonts) converted on every LibreOffice version bump, with output page-count/rough-visual diffed against a checked-in baseline, to catch version-upgrade-induced drift before it reaches production.

**Phase to address:** Acceptable as a documented, accepted limitation for v1.2 rather than an engineering problem to solve — but the roadmap should explicitly record the decision (font package chosen, version-pinning policy) in `.planning/PROJECT.md`'s Key Decisions, the same way HTML->PDF and CAD were explicitly scoped out with a documented reason, so it reads as a deliberate choice rather than an oversight discovered later.

## Sources

- LibreOffice bugzilla #52125 — "libreoffice --headless should return 0 on successful conversion" (exit-code-0-on-failure history): https://bugs.documentfoundation.org/show_bug.cgi?id=52125
- LibreOffice bugzilla #106134 — "headless mode does not allow concurrent jobs": https://www.mail-archive.com/libreoffice-bugs@lists.freedesktop.org/msg397604.html
- LibreOffice bugzilla #82775 — "libreoffice in headless mode crashes when serving multiple concurrent requests": https://libreoffice-bugs.freedesktop.narkive.com/MddL6PYa/bug-82775-new-libreoffice-in-headless-mode-crashes-when-serving-multiple-concurrent-requests
- LibreOffice bugzilla #95843 — "Headless mode leaves zombie process": https://bugs.documentfoundation.org/show_bug.cgi?id=95843
- Ask LibreOffice — "How can i run multiple instances of soffice.bin at a time" (profile-lock / `-env:UserInstallation` workaround): https://ask.libreoffice.org/en/question/42975/how-can-i-run-multiple-instances-of-sofficebin-at-a-time/
- Ask LibreOffice — "Headless LibreOffice fails with zero status code?": https://ask.libreoffice.org/t/headless-libreoffice-fails-with-zero-status-code/49388
- Ask LibreOffice — "Converting docx in headless mode hangs" (hang on corrupted input, high CPU): https://ask.libreoffice.org/t/converting-docx-in-headless-mode-hangs/37502
- Gotenberg — "Libreoffice Concurrence" issue #94 (single-instance stateful lock, no parallel operations): https://github.com/thecodingmachine/gotenberg/issues/94
- Gotenberg troubleshooting docs (start-timeout, queue-under-load behavior, fixed-in-8.32.0 clean-state-on-failed-launch): https://gotenberg.dev/docs/troubleshooting
- Gotenberg issue #1023 — "Shutdown LibreOffice when idle" (soffice.bin lingers forever without an external timeout mechanism): https://github.com/gotenberg/gotenberg/issues/1023
- jdhao's digital space — "Serving Concurrent Requests for LibreOffice Service" (horizontal-scaling-over-single-instance-parallelism recommendation): https://jdhao.github.io/2021/06/11/libreoffice_concurrent_requests/
- LibreOffice dev mailing list — "killing soffice.bin from oosplash" (launcher-forks-child signal-forwarding hazard): https://listarchives.libreoffice.org/global/dev/2012/msg07032.html
- The Document Foundation Design Blog — "Dealing with Missing Fonts" (font substitution breaking layout): https://design.blog.documentfoundation.org/2016/10/21/dealing-with-missing-fonts/
- The Document Foundation Blog — "LibreOffice Tips & Tricks: Replacing Microsoft Fonts" (fonts-liberation-style metric-compatible substitutes): https://blog.documentfoundation.org/blog/2020/09/08/libreoffice-tt-replacing-microsoft-fonts/
- BigBlueButton issue #13388 — "Installed fonts not fully available in LibreOffice docker container" (container font-availability gap): https://github.com/bigbluebutton/bigbluebutton/issues/13388
- Codebase-derived findings (HIGH confidence, direct source read): `internal/reconciler/reconciler.go`, `internal/queue/queue.go`, `internal/worker/worker.go`, `internal/convert/exec.go`, `internal/convert/convert.go`, `internal/convert/libvips.go`, `cmd/worker/main.go`, `Dockerfile.worker`, `.planning/PROJECT.md`

---
*Pitfalls research for: OctoConv v1.2 (Document Engine Class — LibreOffice)*
*Researched: 2026-07-09*
