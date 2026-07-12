# Phase 15: HTML→PDF Chromium Engine - Pattern Map

**Mapped:** 2026-07-11
**Files analyzed:** 17 (5 new, 12 modified)
**Analogs found:** 17 / 17

This phase adds a THIRD engine class (`html`) mirroring the `document` engine class introduced in v1.2 (Phase 9-11). Almost every new/modified file has a direct, structurally-identical analog — this is the strongest analog coverage of any phase to date. The one non-mechanical deviation: RESEARCH.md's Pattern 1 finding means `htmlopts.go` follows `opts.go`'s *validation/strictness* shape but NOT its *argv-injection* shape — HTML print options become CSS injected into a worker-built copy of the HTML, not CLI flags (see the `htmlopts.go` section below for the corrected mechanism).

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|---|---|---|---|---|
| `internal/convert/chromium.go` (NEW) | service (Converter impl) | file-I/O / transform | `internal/convert/libreoffice.go` | exact |
| `internal/convert/htmlopts.go` (NEW) | utility (validated-opts + CSS builder) | transform | `internal/convert/opts.go` | role-match (mechanism corrected, see below) |
| `internal/convert/htmlsniff.go` (NEW) | utility (content sniff) | transform | `internal/convert/docsniff.go` / `internal/convert/sniff.go` | role-match |
| `internal/convert/convert.go` (MODIFY: add `EngineHTML`, `htm→html` alias) | config (constants) | — | same file (`EngineDocument`, `NormalizeFormat`) | exact |
| `cmd/chromium-worker/main.go` (NEW) | entry point | event-driven (asynq consumer) | `cmd/document-worker/main.go` | exact |
| `Dockerfile.chromium-worker` (NEW) | config (container build) | — | `Dockerfile.document-worker` | exact |
| `internal/db/migrations/0005_html_engine.sql` (NEW) | migration | batch (DDL) | `internal/db/migrations/0001_init.sql` (CHECK def) + `0003_webhook_dead_letter.sql` (style) | exact |
| `internal/queue/queue.go` (MODIFY) | config (task-type/queue constants + retry funcs) | event-driven | same file (`TypeDocumentConvert`/`DocumentRetryDelay`/`DocumentUniqueTTL`) | exact |
| `internal/queue/client.go` (MODIFY) | service (producer) | event-driven | same file (`EnqueueDocumentConvert`) | exact |
| `internal/api/api.go` (MODIFY: `Enqueuer` interface) | config (interface) | — | same file (`EnqueueDocumentConvert` method) | exact |
| `internal/api/handlers.go` (MODIFY: sniff branch + opts dispatch + engine switch) | controller | request-response | same file (docx/OLE-CFB sniff branches, `ParseDocOpts` call, engine switch) | exact |
| `internal/worker/worker.go` (MODIFY: `HandleHTMLConvert` + `isHTMLTerminal` + `terminalChromiumSignatures`) | service (task handler) | event-driven | same file (`HandleDocumentConvert`/`isDocumentTerminal`/`terminalLibreOfficeSignatures`) | exact |
| `internal/reconciler/reconciler.go` (MODIFY: engine switch case + `enqueuer` interface) | service (sweeper) | batch | same file (`case convert.EngineDocument`) | exact |
| `docker-compose.yml` (MODIFY: new `chromium-worker` service) | config | — | same file (`document-worker` service block) | exact |
| `docker-compose.e2e.yml` (MODIFY: `chromium-worker` extra_hosts + canary listener) | config | — | same file (`document-worker` extra_hosts block) | exact |
| `internal/e2e/e2e_test.go` (MODIFY: html→pdf happy path, canary network-block test, non-HTML-rejection test) | test | request-response | same file (`TestDocumentConversionE2E`, `TestOLECFBRejectionE2E`, `startWebhookReceiver`) | exact |

## Pattern Assignments

### `internal/convert/chromium.go` (NEW) — service, file-I/O/transform

**Analog:** `internal/convert/libreoffice.go` (263 lines, read in full)

**Imports pattern** (`internal/convert/libreoffice.go:1-11`):
```go
package convert

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)
```

**Converter struct + Pairs() pattern** (`internal/convert/libreoffice.go:30-44`):
```go
type LibreOfficeConverter struct{}

func (LibreOfficeConverter) Pairs() []Pair {
	pairs := make([]Pair, 0, len(documentFormats)+len(crossPairs))
	for _, f := range documentFormats {
		pairs = append(pairs, Pair{From: f, To: "pdf"})
	}
	pairs = append(pairs, crossPairs...)
	return pairs
}
```
For `ChromiumConverter`, `Pairs()` is trivial — a single `{From: "html", To: "pdf"}` entry (no cross-pairs, per D-06/HTML-03 scope).

**Convert() pattern — per-job workDir isolation, opts parse, argv assembly, runCommand, output rename/validate** (`internal/convert/libreoffice.go:54-131`):
```go
func (LibreOfficeConverter) Convert(ctx context.Context, inPath, outPath string, opts map[string]any) error {
	workDir := filepath.Dir(outPath) // caller's per-job workDir; already unique, already cleaned up
	...
	docOpts, err := DocOptsFromMap(opts)
	...
	args := []string{
		"--headless", "--invisible", "--nocrashreport", "--nodefault",
		...
	}
	if err := runCommand(ctx, "soffice", args...); err != nil {
		return fmt.Errorf("libreoffice: %w", err)
	}
	...
	if err := os.Rename(producedPath, outPath); err != nil {
		return fmt.Errorf("libreoffice: rename output: %w", err)
	}
	return validateDocumentOutput(outPath, targetFormat, isPDFA)
}
```
`ChromiumConverter.Convert` mirrors this shape exactly but with THREE deviations, all specified in RESEARCH.md's Code Examples section:
1. Before invoking `runCommand`, it must build a NEW file (`workDir/rendered.html` — never mutate/re-upload `inPath`) by copying the downloaded input and injecting the `<style>` block from `htmlopts.go`'s `buildPrintCSS` before `</head>` (case-insensitive search; fallback: right after `<html ...>`).
2. The exact argv (RESEARCH.md "Code Examples" section, verify-live flags):
```go
args := []string{
    "--headless",
    "--disable-gpu",
    "--no-sandbox",
    "--disable-dev-shm-usage",
    "--blink-settings=scriptEnabled=false",
    "--proxy-server=127.0.0.1:9",
    "--proxy-bypass-list=<-loopback>",
    "--host-resolver-rules=MAP * ~NOTFOUND",
    "--print-to-pdf=" + outPath,
    "file://" + renderedPath,
}
if err := runCommand(ctx, "chromium-headless-shell", args...); err != nil {
    return fmt.Errorf("chromium: %w", err)
}
```
3. No rename step is needed — `--print-to-pdf=<path>` writes directly to `outPath`; output validation reuses `validatePDF` (`internal/convert/libreoffice.go:174-205`) VERBATIM (target is always `"pdf"`), not `validateDocumentOutput`'s dispatch (no non-pdf target exists for this engine).

**Engine() pattern** (`internal/convert/libreoffice.go:133-134`):
```go
func (LibreOfficeConverter) Engine() string { return EngineDocument }
```
→ `func (ChromiumConverter) Engine() string { return EngineHTML }`

**Output validation — reuse verbatim, no new validator** (`internal/convert/libreoffice.go:171-205`, `validatePDF`): copy invocation as-is; do not write a new PDF validator.

---

### `internal/convert/htmlopts.go` (NEW) — utility, transform

**Analog:** `internal/convert/opts.go` (153 lines, read in full) — for the STRICTNESS mechanism (ParseHTMLOpts, DisallowUnknownFields, checkStrictObject reuse) — but NOT for the "compile-time string selected by validated enum → argv" mechanism, per RESEARCH.md Pattern 1 (CRITICAL finding: no CLI flags exist for page_size/margin/landscape/print_background).

**Closed-struct + strict-parse pattern to copy** (`internal/convert/opts.go:25-58`):
```go
type DocOpts struct {
	PDFProfile string `json:"pdf_profile,omitempty"`
}

func ParseDocOpts(raw []byte) (DocOpts, error) {
	if err := checkStrictObject(raw); err != nil {
		return DocOpts{}, err
	}
	var o DocOpts
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&o); err != nil {
		return DocOpts{}, fmt.Errorf("parse opts: %w", err)
	}
	if o.PDFProfile != "" && o.PDFProfile != pdfProfileA2b {
		return DocOpts{}, fmt.Errorf("unsupported pdf_profile %q", o.PDFProfile)
	}
	return o, nil
}
```
`HTMLOpts` mirrors this exactly but with 4 fields (`page_size`, `margin_mm`, `landscape`, `print_background`) each validated against its own closed range/enum (D-06): `page_size` against `{a4,letter,legal,a3,a5}`, `margin_mm` against `[0,50]`, the two bools unconstrained. `checkStrictObject` (`internal/convert/opts.go:68-106`) is package-private and already shared — call it directly, do not duplicate it.

**`*FromMap` round-trip pattern to copy** (`internal/convert/opts.go:108-122`, `DocOptsFromMap`): identical shape for `HTMLOptsFromMap`, used by the worker-side re-parse in `HandleHTMLConvert` (D-10 strictness parity, mirrors `internal/worker/worker.go:272` calling `convert.DocOptsFromMap(job.Opts)`).

**`ValidateApplicability` pattern to copy** (`internal/convert/opts.go:124-138`):
```go
func ValidateApplicability(engine, source, target string, o DocOpts) error {
	if o.PDFProfile == "" {
		return nil
	}
	if engine != EngineDocument || NormalizeFormat(target) != "pdf" {
		return fmt.Errorf("pdf_profile is only valid for document -> pdf conversions")
	}
	return nil
}
```
RESEARCH.md's Code Examples section explicitly says this should be its OWN function scoped to `EngineHTML` (not merged into the shared one) — e.g. `ValidateHTMLApplicability(engine, source, target string, o HTMLOpts) error`.

**MECHANISM CORRECTION — the part that does NOT mirror `opts.go`:** `PDFAFilterOptions` (`internal/convert/opts.go:140-153`) returns a compile-time argv suffix string. `htmlopts.go` instead needs a `buildPrintCSS(o HTMLOpts) string` function returning a `<style>@page{...}</style>` block — RESEARCH.md's "Code Examples" / Pattern 1 section gives the exact server-constant map and function body to copy verbatim:
```go
var htmlPageSizeCSS = map[string]string{
    "a4": "A4", "letter": "letter", "legal": "legal", "a3": "A3", "a5": "A5",
}

func buildPrintCSS(o HTMLOpts) string {
    size := htmlPageSizeCSS[o.PageSize]
    if o.Landscape {
        size += " landscape"
    }
    css := fmt.Sprintf("@page { size: %s !important; margin: %dmm !important; }\n", size, o.MarginMM)
    if o.PrintBackground {
        css += "*, *::before, *::after { -webkit-print-color-adjust: exact !important; print-color-adjust: exact !important; }\n"
    } else {
        css += "*, *::before, *::after { -webkit-print-color-adjust: economy !important; print-color-adjust: economy !important; }\n"
    }
    return "<style>" + css + "</style>"
}
```
The invariant carried over from `PDFAFilterOptions` (Pitfall 9): the returned string is built ONLY from already-validated `HTMLOpts` fields (never raw client bytes) — same "server-constant selected by validated enum" shape, just CSS text instead of a JSON filter suffix.

---

### `internal/convert/htmlsniff.go` (NEW) — utility, transform

**Analog:** `internal/convert/docsniff.go` (121 lines, read in full) for file shape/doc-comment convention; `internal/convert/sniff.go:78-93` (`Sniff`) for the magic-byte-vs-content-check contrast.

D-07 explicitly rejects a full HTML parser (`x/net/html`) — same reasoning `docsniff.go` uses to justify ZIP-central-directory-only inspection over full decompression (`internal/convert/docsniff.go:54-61` doc comment: "never re-parse... more than once per upload", "zero decompression"). The needed function, per RESEARCH.md's structure sketch:
```go
// LooksLikeHTML reports whether r's content is valid UTF-8, contains no NUL
// bytes, and begins (after leading whitespace/BOM) with an HTML marker
// (<!doctype html> or <html>, case-insensitive) — D-07's fail-closed content
// check, run AFTER the client's declared source/extension already claims html.
func LooksLikeHTML(r io.ReaderAt, size int64) bool { ... }
```
Doc-comment style to copy (`internal/convert/docsniff.go:1-8` package-doc pattern; `internal/convert/sniff.go:69-77` — `Sniff`'s doc comment explaining WHY the bounded-peek design, D-02/D-07 references). No signature/magic-byte table is needed (HTML has none) — this is closer to `IsOLECFB`'s single boolean-predicate shape (referenced in `internal/api/handlers.go:164`, `convert.IsOLECFB(file)`) than to `Sniff`'s multi-signature table.

---

### `internal/convert/convert.go` (MODIFY) — config

**Analog:** same file, `EngineDocument` + `NormalizeFormat`'s existing alias arms.

**Engine-class constant pattern** (`internal/convert/convert.go:17-20`):
```go
const (
	EngineImage    = "image"
	EngineDocument = "document"
)
```
→ add `EngineHTML = "html"` as a third const in the same block. The doc comment above (`internal/convert/convert.go:10-16`) already states this block is "the SINGLE compile-time source of truth" — no other file may hold a raw `"html"` literal; update that comment's cross-reference list too.

**`NormalizeFormat` alias pattern** (`internal/convert/convert.go:45-55`):
```go
func NormalizeFormat(f string) string {
	f = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(f), "."))
	switch f {
	case "jpeg":
		return "jpg"
	case "tif":
		return "tiff"
	default:
		return f
	}
}
```
RESEARCH.md's Pitfall D is explicit: add `case "htm": return "html"` as a third arm, one line, same shape as `jpeg`/`tif` — this is the correct integration point, NOT a special case inside the new HTML-detection branch in `handlers.go`.

---

### `cmd/chromium-worker/main.go` (NEW) — entry point, event-driven

**Analog:** `cmd/document-worker/main.go` (155 lines, read in full) — copy near-verbatim.

**Full skeleton to mirror** (`cmd/document-worker/main.go:30-128`): Postgres connect → storage.New → RedisOpt → queue.NewClient → jobs.NewRepo → `worker.NewHandler(...)` with an engine-specific timeout env var → `asynq.NewServeMux()` + `mux.HandleFunc(queue.TypeDocumentConvert, h.HandleDocumentConvert)` → `asynq.NewServer` with `Queues: map[string]int{queue.QueueDocument: 1}` → Prometheus queue-depth collector registration → graceful shutdown on SIGINT/SIGTERM with a 15s metrics-server shutdown timeout.

Concrete substitutions for `chromium-worker`:
- `envDuration("DOCUMENT_ENGINE_TIMEOUT", 300*time.Second)` → `envDuration("HTML_ENGINE_TIMEOUT", <value TBD by planner, D-09/Claude's Discretion>)`
- `mux.HandleFunc(queue.TypeDocumentConvert, h.HandleDocumentConvert)` → `mux.HandleFunc(queue.TypeHTMLConvert, h.HandleHTMLConvert)`
- `Queues: map[string]int{queue.QueueDocument: 1}` → `Queues: map[string]int{queue.QueueHTML: 1}`
- `envInt("DOCUMENT_WORKER_CONCURRENCY", 2)` → `envInt("HTML_WORKER_CONCURRENCY", <value TBD>)`
- `metrics.NewQueueDepthCollector(asynq.NewInspector(redisOpt), queue.QueueDocument)` → same with `queue.QueueHTML`
- Log lines: `"🐙 document-worker starting (queue=%s)"` → `"🐙 chromium-worker starting (queue=%s)"` (same emoji-prefix convention, `cmd/document-worker/main.go:93`)
- Same D-05 comment carried over verbatim: chromium-worker must NOT run the reconciler sweeper (that stays solely in `cmd/worker`), per `cmd/document-worker/main.go:76-78`'s comment.
- The `signingSecret`/webhook-inert-passthrough comment (`cmd/document-worker/main.go:50-54`) applies identically — `chromium-worker` never delivers/signs webhooks either.
- `envInt`/`envDuration`/`firstField` helpers (`cmd/document-worker/main.go:130-155`) — copy verbatim (package-private, duplicated per-`cmd/*` convention already established across `cmd/api`, `cmd/worker`, `cmd/document-worker`).

---

### `Dockerfile.chromium-worker` (NEW) — config

**Analog:** `Dockerfile.document-worker` (29 lines, read in full) — copy structure verbatim, swap the engine package/binary/entrypoint.

```dockerfile
# Build stage
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/document-worker ./cmd/document-worker

# Runtime stage: document engine needs LibreOffice (soffice) headless conversion.
FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates \
      tini \
      libreoffice-writer-nogui \
      ...
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/document-worker /usr/local/bin/document-worker
USER nobody
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/document-worker"]
```
Substitutions: `chromium-headless-shell` (+ `fonts-liberation2` per RESEARCH's font-package note, flag CJK fonts as an open scope question rather than assuming) replaces the `libreoffice-*`/font packages; `tini` is KEPT per D-09 pending live confirmation (RESEARCH.md Verify-Live Smoke Checklist item 7 — budget the live tini-vs-no-tini zombie-process test before finalizing); `USER nobody` is kept identically (`Dockerfile.document-worker:24` comment "the worker shells out to untrusted-input engines" applies even more strongly here). `--no-sandbox`/`--disable-dev-shm-usage` are Convert-time argv flags (chromium.go), not Dockerfile content, but the Dockerfile's `USER nobody` is WHY they're required (D-08 comment).

---

### `internal/db/migrations/0005_html_engine.sql` (NEW) — migration

**Analog:** `internal/db/migrations/0001_init.sql:47-48` (current CHECK definition) + `internal/db/migrations/0003_webhook_dead_letter.sql` (style: short doc-comment header explaining the "why", then the DDL).

**Current constraint** (`internal/db/migrations/0001_init.sql:47-48`):
```sql
    engine         text NOT NULL
                   CHECK (engine IN ('image', 'document', 'av', 'cad', 'archive', 'probe')),
```

**Exact migration to write** (verified-live text from RESEARCH.md's "Code Examples" section — Postgres auto-names an inline unnamed CHECK as `<table>_<column>_check`; RESEARCH flags this as needing a live `\d+ jobs` confirmation before finalizing the constraint name):
```sql
-- Add 'html' to jobs.engine's allow-list -- hard prerequisite for the
-- chromium HTML->PDF engine class (HTML-01). Must land BEFORE any
-- routing/queue-scaffolding work that could create an engine="html" row.
ALTER TABLE jobs DROP CONSTRAINT jobs_engine_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_engine_check
    CHECK (engine IN ('image', 'document', 'av', 'cad', 'archive', 'probe', 'html'));
```
File naming convention confirmed: `000X_snake_case_description.sql`, numbers 0001-0004 already exist, migrations run in filename-sorted order (`internal/db/db.go`'s embedded-migration runner, per RESEARCH.md).

---

### `internal/queue/queue.go` (MODIFY) — config, event-driven

**Analog:** same file's `TypeDocumentConvert`/`QueueDocument`/`NewDocumentConvertTask`/`documentRetrySchedule`/`DocumentRetryDelay`/`documentBackoffSum`/`DocumentUniqueTTL` (all in the file already read in full — 421 lines).

**Task-type/queue constant pattern** (`internal/queue/queue.go:19-34`):
```go
const (
	TypeImageConvert    = "image:convert"
	TypeWebhookDeliver  = "webhook:deliver"
	TypeDocumentConvert = "document:convert"
)

const (
	QueueImage    = convert.EngineImage
	QueueWebhook  = "webhook"
	QueueDocument = convert.EngineDocument
)
```
→ add `TypeHTMLConvert = "html:convert"` and `QueueHTML = convert.EngineHTML` — tied to the `convert.EngineHTML` single-source-of-truth const exactly as `QueueDocument` is tied to `convert.EngineDocument`.

**Task constructor pattern** (`internal/queue/queue.go:79-99`, `NewDocumentConvertTask`):
```go
func NewDocumentConvertTask(jobID uuid.UUID, maxRetry int, uniqueTTL time.Duration) (*asynq.Task, error) {
	b, err := json.Marshal(ConvertPayload{JobID: jobID})
	if err != nil {
		return nil, fmt.Errorf("marshal convert payload: %w", err)
	}
	return asynq.NewTask(TypeDocumentConvert, b,
		asynq.Queue(QueueDocument),
		asynq.MaxRetry(maxRetry),
		asynq.Unique(uniqueTTL),
	), nil
}
```
`NewHTMLConvertTask` mirrors exactly — reuses `ConvertPayload`/`ParseConvertPayload` (no new payload type needed, all job detail re-read from Postgres, same minimal-payload discipline noted at `internal/queue/queue.go:36-40`).

**Retry-schedule + delay-func pattern** (`internal/queue/queue.go:197-224`, `documentRetrySchedule`/`DocumentRetryDelay`): copy the no-jitter clamp-to-last-entry shape exactly (NOT `WebhookRetryDelay`'s jittered shape — the doc comment on `DocumentRetryDelay` at line 210-214 explicitly calls this out). Add a `case TypeHTMLConvert:` arm to `RetryDelayFunc` (`internal/queue/queue.go:233-244`).

**Unique-TTL derivation pattern** (`internal/queue/queue.go:299-334`, `documentBackoffSum`/`DocumentUniqueTTL`): copy the worst-case-lifetime derivation formula and its extensive doc-comment reasoning verbatim (the `(maxRetry+1)` correction, the "TTL derived not hardcoded" rationale) — reuse `uniqueTTLSafetyMargin` (`internal/queue/queue.go:246-249`) verbatim, no HTML-specific margin constant.

---

### `internal/queue/client.go` (MODIFY) — service, event-driven

**Analog:** same file (146 lines, read in full) — `documentMaxRetry`/`documentUniqueTTL` fields + `EnqueueDocumentConvert`.

**Struct field + constructor pattern** (`internal/queue/client.go:34-45, 57-68`):
```go
documentMaxRetry int
documentUniqueTTL time.Duration
...
documentMaxRetry := envInt("DOCUMENT_MAX_RETRY", 3)
documentEngineTimeout := envDuration("DOCUMENT_ENGINE_TIMEOUT", 300*time.Second)
return &Client{
	...
	documentMaxRetry:  documentMaxRetry,
	documentUniqueTTL: DocumentUniqueTTL(documentMaxRetry, documentEngineTimeout),
}, nil
```
→ add `htmlMaxRetry`/`htmlUniqueTTL` fields, read `HTML_MAX_RETRY`/`HTML_ENGINE_TIMEOUT` env vars via the existing `envInt`/`envDuration` helpers (`internal/queue/client.go:114-133`, already generic — no new helper needed).

**Enqueue method pattern** (`internal/queue/client.go:98-109`):
```go
func (c *Client) EnqueueDocumentConvert(ctx context.Context, jobID uuid.UUID) error {
	task, err := NewDocumentConvertTask(jobID, c.documentMaxRetry, c.documentUniqueTTL)
	if err != nil {
		return err
	}
	if _, err := c.c.EnqueueContext(ctx, task); err != nil {
		return fmt.Errorf("enqueue document convert %s: %w", jobID, err)
	}
	return nil
}
```
`EnqueueHTMLConvert` mirrors exactly.

---

### `internal/api/api.go` (MODIFY) — config (interface)

**Analog:** same file, `Enqueuer` interface (`internal/api/api.go:29-33`):
```go
type Enqueuer interface {
	EnqueueImageConvert(ctx context.Context, jobID uuid.UUID) error
	EnqueueDocumentConvert(ctx context.Context, jobID uuid.UUID) error
}
```
Add `EnqueueHTMLConvert(ctx context.Context, jobID uuid.UUID) error` as a third method. `*queue.Client` already satisfies this concretely once `EnqueueHTMLConvert` is added there — no other change needed in this file (interface segregation already scoped to exactly what `handlers.go` calls, per the file's own doc comment at line 29).

---

### `internal/api/handlers.go` (MODIFY) — controller, request-response

**Analog:** same file (427 lines, read in full) — three separate integration points inside `handleCreateJob`.

**1. Sniff-chain branch (HTML content detection, D-07)** — analog is the existing fail-closed branches at `internal/api/handlers.go:130-192` (the ZIP-container branch, the OLE-CFB branch, the generic unrecognized-content branch). Style to copy: reject BEFORE `s.storage.Upload` (line 288), log with the established `content validation rejected: client_id=%s filename=%q reason=...` format (e.g. line 171, 180, 188), 422 status via `writeError`. Concretely: HTML detection is NOT magic-byte-based (`convert.Sniff` returns `""` for HTML, D-07), so the branch structure is closer to the OLE-CFB branch (`internal/api/handlers.go:164-175`, a single `convert.IsOLECFB(file)` predicate call) than to the ZIP-disambiguation branch — a single `convert.LooksLikeHTML(file, header.Size)` predicate call gated on `source == "html"` (post `NormalizeFormat`'s new `htm→html` alias), placed in the `detected == ""` chain alongside the existing branches.

**2. Opts dispatch — the one non-mechanical integration point** (`internal/api/handlers.go:249-280`, currently unconditional `convert.ParseDocOpts`):
```go
docOpts, err := convert.ParseDocOpts([]byte(rawOpts))
if err != nil {
	...
}
if err := convert.ValidateApplicability(engine, detected, target, docOpts); err != nil {
	...
}
```
RESEARCH.md's "Opts dispatch" section is explicit this must become an engine-keyed branch: `if engine == convert.EngineDocument { ...ParseDocOpts...ValidateApplicability... } else if engine == convert.EngineHTML { ...ParseHTMLOpts...ValidateHTMLApplicability... }` — same size-cap (`maxOptsBytes`, line 32/252) and same normalize-before-persist round-trip (lines 268-279) apply to both branches unchanged.

**3. Engine-routing switch** (`internal/api/handlers.go:318-334`):
```go
var enqueueErr error
switch engine {
case convert.EngineImage:
	enqueueErr = s.queue.EnqueueImageConvert(ctx, createdID)
case convert.EngineDocument:
	enqueueErr = s.queue.EnqueueDocumentConvert(ctx, createdID)
default:
	writeError(w, http.StatusInternalServerError, "failed to enqueue job")
	return
}
```
Add `case convert.EngineHTML: enqueueErr = s.queue.EnqueueHTMLConvert(ctx, createdID)` — the `default:` fail-closed branch (T-11-02 comment) stays as-is, still catches any future unrouted engine.

---

### `internal/worker/worker.go` (MODIFY) — service, event-driven

**Analog:** same file (524 lines, read in full) — `HandleDocumentConvert` (lines 239-329), `isDocumentTerminal` (lines 112-140), `terminalLibreOfficeSignatures` (lines 38-66).

**Terminal-signature slice pattern** (`internal/worker/worker.go:38-66`):
```go
var terminalLibreOfficeSignatures = []string{
	"output missing %pdf- magic bytes",
	"output is empty",
	"no export filter for",
	"output does not match expected container format",
	"produced no output file",
	"output missing pdf/a outputintent marker",
	"pdf_profile requested for non-pdf target",
}
```
`terminalChromiumSignatures` mirrors this shape but is INITIALLY EMPTY or minimal — RESEARCH.md's Open Question 2 / Verify-Live Smoke Checklist item 2 is explicit this list must be populated from LIVE-CAPTURED stderr text (same discipline the `vips` list used, comment at `internal/worker/worker.go:28-31`: "Verified live-tested"), not guessed. `validatePDF`'s reused error strings ("output is empty", "output missing %pdf- magic bytes") DO carry over verbatim since that validator is reused as-is.

**Engine-scoped terminal-classifier pattern** (`internal/worker/worker.go:112-140`, `isDocumentTerminal`):
```go
func isDocumentTerminal(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return isTerminal(err)
}
```
`isHTMLTerminal` copies this EXACT shape — RESEARCH.md's Standard Stack/Architecture confirms HTML-01 wants "terminal-classified timeout" mirroring DOC-08's own timeout-is-terminal divergence (a stuck chromium render should not retry to exhaustion any more than a stuck soffice should). Delegates to the same shared `isTerminal` (lines 78-110) for non-timeout errors — extend that shared function's signature-checking loop with a `for _, sig := range terminalChromiumSignatures` block mirroring the existing `terminalLibreOfficeSignatures` loop (lines 102-108).

**Handler method pattern** (`internal/worker/worker.go:239-329`, `HandleDocumentConvert` — full method, structurally identical to `HandleImageConvert` at lines 185-237 with exactly the DOC-07/DOC-08 deltas called out in its own doc comment): `HandleHTMLConvert` copies this ENTIRE method shape — payload parse → `SkipRetry` on unparseable → `repo.Get` → (D-10 strict re-parse of persisted opts via `HTMLOptsFromMap`, mirroring lines 266-288's `DocOptsFromMap` re-parse-and-MarkFailed-on-corruption block) → `MarkActive` → `h.process(ctx, job)` (UNCHANGED, engine-agnostic via `registry.Lookup`, no html-specific fork needed inside `process`) → `isHTMLTerminal(err)` branch (mirrors lines 297-315 exactly, same `MarkFailed`/`metrics.RecordJobOutcome(queue.QueueHTML, ...)`/best-effort-webhook-enqueue shape) → success path (mirrors lines 322-328).

**`process()` — NO CHANGE NEEDED**: `h.process` (`internal/worker/worker.go:416-477`) is already fully engine-agnostic (`h.registry.Lookup(job.SourceFormat, job.TargetFormat)`, generic `workDir`/`inPath`/`outPath` naming by `job.SourceFormat`/`job.TargetFormat`) — `ChromiumConverter` registering itself in `convert.Default` (mirroring `internal/convert/converters.go`'s `init()` pattern for `LibreOfficeConverter`) is the ONLY wiring `process()` needs; confirms RESEARCH.md's "Converter interface does not change" claim.

---

### `internal/reconciler/reconciler.go` (MODIFY) — service, batch

**Analog:** same file (254 lines, read in full) — `enqueuer` interface (lines 42-47) + engine switch inside `sweep` (lines 130-150).

**Interface extension pattern** (`internal/reconciler/reconciler.go:42-47`):
```go
type enqueuer interface {
	EnqueueImageConvert(ctx context.Context, id uuid.UUID) error
	EnqueueWebhookDeliver(ctx context.Context, id uuid.UUID) error
	EnqueueDocumentConvert(ctx context.Context, id uuid.UUID) error
}
```
Add `EnqueueHTMLConvert(ctx context.Context, id uuid.UUID) error`.

**Engine-routing switch pattern** (`internal/reconciler/reconciler.go:132-150`):
```go
switch j.Engine {
case convert.EngineImage:
	enqueueErr = s.enq.EnqueueImageConvert(ctx, j.ID)
case convert.EngineDocument:
	enqueueErr = s.enq.EnqueueDocumentConvert(ctx, j.ID)
default:
	// Fail closed (T-10-03): av/cad/archive/probe are out of scope...
	metrics.RecordReconcilerAction("unroutable_engine")
	continue
}
```
Add `case convert.EngineHTML: enqueueErr = s.enq.EnqueueHTMLConvert(ctx, j.ID)`. RESEARCH.md's file:line table explicitly flags this as "currently `default:` fails closed and skips — must add an explicit case, not rely on fallthrough" — an html job stranded before this case lands would silently never recover (same class of bug the `T-10-03` comment already documents defending against for other out-of-scope engines).

---

### `docker-compose.yml` (MODIFY) — config

**Analog:** same file, `document-worker` service block (`docker-compose.yml:151-193`):
```yaml
  document-worker:
    build:
      context: .
      dockerfile: Dockerfile.document-worker
    container_name: octoconv-document-worker
    restart: always
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
      minio:
        condition: service_healthy
    environment:
      DATABASE_URL: postgres://octo:octo-pass@postgres:5432/octo_db
      REDIS_ADDR: redis:6379
      S3_ENDPOINT: minio:9000
      S3_ACCESS_KEY: minioadmin
      S3_SECRET_KEY: minioadmin
      S3_BUCKET: octoconv
      S3_USE_SSL: "false"
      DOCUMENT_WORKER_CONCURRENCY: "2"
      DOCUMENT_ENGINE_TIMEOUT: "300s"
      IMAGE_MAX_RETRY: "4"
      ENGINE_TIMEOUT: "120s"
      DOCUMENT_MAX_RETRY: "3"
      WEBHOOK_SIGNING_SECRET: "dev-only-change-me-in-real-deploys"
      WEBHOOK_PRESIGN_TTL: "6h"
      METRICS_ADDR: "127.0.0.1:9090"
    deploy:
      resources:
        limits:
          cpus: "2.0"
          memory: 1g
```
New `chromium-worker` service copies this shape: `Dockerfile.chromium-worker`, `HTML_WORKER_CONCURRENCY`/`HTML_ENGINE_TIMEOUT` env vars in place of the document ones, same S3/Postgres/Redis env block verbatim, same `deploy.resources.limits` shape (values TBD by planner — chromium is heavier than LibreOffice per RESEARCH.md's Standard Stack section, may need a higher memory limit than 1g). D-09's `--shm-size` note means this service block also likely needs a `shm_size:` key (not present on any existing service — a genuinely new compose primitive for this phase, flagged since no existing analog covers it in this codebase).

Also update the API service's `environment` block: `queue.NewClient()` reads HTML retry/timeout vars unconditionally too (same DEBT-05 pattern already documented at `docker-compose.yml:92-94, 133-135, 174-178` for the image/document cross-reads) — add `HTML_MAX_RETRY`/`HTML_ENGINE_TIMEOUT` to `api`, `worker`, and `document-worker` env blocks for parity, mirroring the existing three-way cross-read comments.

---

### `docker-compose.e2e.yml` (MODIFY) — config

**Analog:** same file, `document-worker` block (`docker-compose.e2e.yml:36-38`):
```yaml
  document-worker:
    extra_hosts:
      - "host.docker.internal:host-gateway"
```
Add an identical `chromium-worker:` block — RESEARCH.md's Verify-Live Smoke Checklist item 4 explicitly calls this out: "Add `chromium-worker` to `docker-compose.e2e.yml`'s `extra_hosts: host.docker.internal:host-gateway` block, mirroring `worker`/`document-worker`" — needed so the canary-listener test (D-04) can assert zero connections reach a host-bound receiver from inside the chromium-worker container. The file's own header comment (lines 1-15) explaining "E2E-ONLY... MUST NEVER be used in production" applies unchanged; no new top-of-file comment needed, just the new service block.

---

### `internal/e2e/e2e_test.go` (MODIFY) — test, request-response

**Analog A (happy path table):** `TestDocumentConversionE2E` (`internal/e2e/e2e_test.go:334-384`, full function read) — upload → `pollUntilDone` (5 min bound, "LibreOffice cold start" comment generalizes to "chromium cold start") → `assertDownloadIsPDF` (magic-bytes check, `internal/e2e/e2e_test.go:386-407`, reused verbatim — target is always pdf). New `TestHTMLConversionE2E` copies this shape with a single-fixture table (no 6-format loop needed, D-06's scope is one source format) plus subtests for each `page_size`/`landscape`/`print_background` combination if the planner wants HTML-03 live-verified per option (RESEARCH.md Verify-Live Smoke Checklist item 3 recommends this).

**Analog B (canary/network-block test) — NEW test shape, no direct 1:1 existing test, but `startWebhookReceiver` is the direct building block:** `startWebhookReceiver` (`internal/e2e/e2e_test.go:299-332`, full function read):
```go
func startWebhookReceiver(t *testing.T, webhookHost string) (string, <-chan webhookHit) {
	t.Helper()
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	...
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		...
		received <- webhookHit{...}
		w.WriteHeader(http.StatusOK)
	}))
	srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	_, port, err := net.SplitHostPort(ln.Addr().String())
	...
	callbackURL := fmt.Sprintf("http://%s/webhook", net.JoinHostPort(webhookHost, port))
	return callbackURL, received
}
```
RESEARCH.md's Verify-Live Smoke Checklist item 4 explicitly says to "reuse/generalize `startWebhookReceiver`... into a canary receiver" — a new `startCanaryReceiver(t, webhookHost) (url string, hits <-chan canaryHit)` function copies this EXACT `net.Listen("tcp","0.0.0.0:0")` + `host.docker.internal`-addressed-URL + buffered-channel shape, generalized to accept hits on arbitrary paths (`/canary-img`, `/canary-fetch`) rather than a single `/webhook` path. The new `TestHTMLNetworkBlockE2E` test then builds an HTML fixture referencing the canary URL via `<img src>` + `<script>fetch()</script>` (D-04) plus literal `169.254.169.254`/`redis`/`postgres` references, asserts ZERO items received on the channel AND the job completes successfully within the engine timeout (not hung) — same "positive proof, not absence-of-crash" discipline RESEARCH.md's Pitfall B calls out.

**Analog C (non-HTML-under-.html-extension rejection):** `TestOLECFBRejectionE2E` (`internal/e2e/e2e_test.go:529-550`, full function read):
```go
func TestOLECFBRejectionE2E(t *testing.T) {
	cfg := e2eSetup(t)
	apiKey := provisionClient(t)
	for _, filename := range oleCFBFixtures {
		t.Run(filename, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", filename))
			...
			body := postJobExpectStatus(t, cfg.baseURL, apiKey, filename, data, "pdf", "", http.StatusUnprocessableEntity)
			if !bytes.Contains(bytes.ToLower(body), []byte("password")) {
				t.Errorf("422 body for %s does not mention the remedy; body=%s", filename, body)
			}
		})
	}
}
```
`TestHTMLContentRejectionE2E` copies this exact shape: a fixture table of non-HTML binary/text content saved with a `.html` extension, `postJobExpectStatus(..., http.StatusUnprocessableEntity)`, loose substring assertion on the 422 body (not exact-string, per the comment at line 542-544 explaining WHY — reworded messages shouldn't brittle-fail the live test).

**Shared test helpers reused as-is, no modification needed:** `e2eSetup` (line 69), `provisionClient` (line 90), `postJob`/`postJobExpectStatus`/`postJobFull` (referenced, not re-defined), `pollUntilDone` (line ~250s per grep), `downloadClient()` (used at line 391).

## Shared Patterns

### Engine-class single-source-of-truth constant
**Source:** `internal/convert/convert.go:10-20`
**Apply to:** every file that currently branches on `convert.EngineDocument` — `internal/api/handlers.go`, `internal/reconciler/reconciler.go`, `internal/queue/queue.go`, `internal/queue/client.go`. NEVER hardcode the string literal `"html"` anywhere outside `convert.go`'s `EngineHTML` const.
```go
const (
	EngineImage    = "image"
	EngineDocument = "document"
	EngineHTML     = "html"
)
```

### Hardened one-shot external process exec
**Source:** `internal/convert/exec.go` (46 lines, read in full)
**Apply to:** `internal/convert/chromium.go` — the `runCommand` function is used AS-IS, zero modification (D-01 explicitly states this):
```go
func runCommand(ctx context.Context, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	...
	select {
	case <-ctx.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
		return fmt.Errorf("%s killed: %w", name, ctx.Err())
	case err := <-done:
		...
	}
}
```

### Terminal vs. transient error classification
**Source:** `internal/worker/worker.go:68-140` (`isTerminal`/`isDocumentTerminal`)
**Apply to:** `internal/worker/worker.go`'s new `isHTMLTerminal` — extend the shared `isTerminal`'s signature-substring loop, add a NEW engine-scoped wrapper that additionally treats `context.DeadlineExceeded` as terminal (mirrors `isDocumentTerminal`'s DOC-08 divergence exactly). Populate `terminalChromiumSignatures` from LIVE-CAPTURED stderr only (RESEARCH.md Open Question 2) — do not guess strings.

### Postgres-first double write + best-effort webhook enqueue
**Source:** `internal/api/handlers.go:293-338` (job creation) and `internal/worker/worker.go:296-328` (`HandleDocumentConvert`'s MarkFailed/MarkDone → best-effort webhook enqueue)
**Apply to:** `HandleHTMLConvert` — MarkFailed/MarkDone must commit to Postgres BEFORE any webhook enqueue attempt; a failed enqueue after a successful status commit must never fail the conversion (comment repeated verbatim at every occurrence in `worker.go`, e.g. lines 217-219, 310-313).

### Validated-opts closed-struct strictness (DisallowUnknownFields + checkStrictObject)
**Source:** `internal/convert/opts.go:44-106`
**Apply to:** `internal/convert/htmlopts.go`'s `ParseHTMLOpts` — reuse the package-private `checkStrictObject` helper directly (already shared, do not duplicate); same allow-list-rejection error-message style (`fmt.Errorf("unsupported pdf_profile %q", ...)` → `fmt.Errorf("unsupported page_size %q", ...)` etc.).

### tini-as-PID-1 for a forking external engine
**Source:** `Dockerfile.document-worker:24-29`
**Apply to:** `Dockerfile.chromium-worker` — same reaper rationale (chromium forks zygote/GPU/renderer processes, same class of problem as `soffice.bin`'s `oosplash`), but D-09 requires this be RE-CONFIRMED live for chromium's one-shot `--print-to-pdf` shape specifically (RESEARCH.md Verify-Live Smoke Checklist item 7) — do not silently assume the LibreOffice finding transfers unverified, even though it is the strong prior.

### Engine-class compose service topology
**Source:** `docker-compose.yml:151-193` (service) + `docker-compose.e2e.yml:36-38` (extra_hosts override)
**Apply to:** the new `chromium-worker` service in both files — same `depends_on: {postgres, redis, minio}` health-gated shape, same `deploy.resources.limits` presence (values TBD), same E2E-only `extra_hosts: host.docker.internal:host-gateway` override.

## No Analog Found

| File | Role | Data Flow | Reason |
|---|---|---|---|
| `docker-compose.e2e.yml` canary-listener wiring for network-block test | config | — | No existing compose service plays the "canary/adversarial listener proving zero connections" role — `startWebhookReceiver`'s httptest-based Go helper (test-side, not a compose service) is the closest building block; the compose file itself needs no new service (the canary listener runs IN the Go test process, reachable via the same `host.docker.internal` mechanism `startWebhookReceiver` already establishes), only the `chromium-worker: extra_hosts:` block (already covered above). Listed here because RESEARCH.md flags the exact canary mechanics ("language/port/connection-counting method") as Claude's Discretion — no prior art in this codebase for a negative-assertion ("prove zero requests arrived") test; `TestOLECFBRejectionE2E`'s positive-assertion shape (422 + substring) does not directly transfer to this test's shape (zero-hits + job-completes-successfully). |
| `docker-compose.yml` `shm_size:` compose key for chromium-worker | config | — | No existing service in `docker-compose.yml` sets `shm_size:` — this is a genuinely new compose primitive introduced by D-09's `/dev/shm` sizing requirement (Pitfall C), not a copy of an existing pattern. Planner should verify Docker Compose's exact `shm_size:` syntax live rather than guess. |

## Metadata

**Analog search scope:** `internal/convert/`, `internal/worker/`, `internal/queue/`, `internal/api/`, `internal/reconciler/`, `cmd/document-worker/`, `internal/e2e/`, `internal/db/migrations/`, root-level `Dockerfile.*` and `docker-compose*.yml`
**Files scanned (read in full or targeted):** 21 (`libreoffice.go`, `opts.go`, `convert.go`, `exec.go`, `docsniff.go`, `sniff.go`, `worker.go`, `queue.go`, `client.go`, `handlers.go`, `api.go`, `reconciler.go`, `cmd/document-worker/main.go`, `Dockerfile.document-worker`, `docker-compose.yml`, `docker-compose.e2e.yml`, `0001_init.sql`, `0002_client_api_keys.sql`, `0003_webhook_dead_letter.sql`, `0004_webhook_deliveries_job_idx.sql`, `internal/e2e/e2e_test.go` [targeted: header/setup, `startWebhookReceiver`, `TestDocumentConversionE2E`, `TestOLECFBRejectionE2E`, `TestPDFAExportE2E`])
**Pattern extraction date:** 2026-07-11
