# Phase 11: API Routing & End-to-End Document Conversion - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-07-09
**Phase:** 11-API Routing & End-to-End Document Conversion
**Areas discussed:** Engine-routing mechanism, Live end-to-end test shape, Image-only validation scope

---

## Engine-routing mechanism

| Option | Description | Selected |
|--------|-------------|----------|
| Hardcoded document-extension set | Simple string-set check in handlers.go, duplicates the format list already known by LibreOfficeConverter.Pairs() | |
| `Engine()` method on Converter interface | Converter self-describes its engine class; Registry.Lookup already gives the Converter | ✓ |
| Separate format→engine map | New `convert.EngineFor(format)` function with its own map, not tied to Converter interface | |

**User's choice:** `Engine()` method on Converter interface.

**Follow-up — Registry API access:**

| Option | Description | Selected |
|--------|-------------|----------|
| New `Registry.EngineFor(from, to)` | Wraps Lookup + c.Engine() in one method, mirrors Supports() | ✓ |
| handlers.go calls Lookup + Engine() itself | No new Registry method; handler does the two-step call directly | |

**User's choice:** New `Registry.EngineFor(from, to)` convenience method.

---

## Live end-to-end test shape

| Option | Description | Selected |
|--------|-------------|----------|
| Committed Go test, env-gated | New *_test.go, follows existing skip-without-env-var convention, drives real HTTP API | ✓ |
| Manual verification during phase verification | curl/script run once during verify_phase_goal, not committed as a permanent test | |

**User's choice:** Committed Go test, env-gated.

**Follow-up — stack shape:**

| Option | Description | Selected |
|--------|-------------|----------|
| Real docker-compose stack | Test hits a running API + document-worker + Postgres/Redis/MinIO, real soffice invocation | ✓ |
| In-process handler calls | Test calls handlers directly in-process, still against real Postgres/Redis/MinIO/soffice but no real HTTP server or worker process | |

**User's choice:** Real docker-compose stack.

**Follow-up — webhook coverage:**

| Option | Description | Selected |
|--------|-------------|----------|
| Include webhook in the same test | One of 6 format runs sets callback_url to a local httptest.Server and asserts signed payload delivery | ✓ |
| Polling only, webhook tested separately | E2E test only covers upload→convert→download via polling; webhook already covered by Phase 2's existing tests | |

**User's choice:** Include webhook in the same test (folds SC#3 into the E2E test).

---

## Image-only validation scope

| Option | Description | Selected |
|--------|-------------|----------|
| Already done, confirm with a test | HasDimensionLimit already scopes correctly; add a focused test to lock in the behavior | ✓ |
| Audit for other image-only checks | Have researcher/planner re-scan handlers.go for other image-specific branches that might also need document exemptions | |

**User's choice:** Already done — confirm with a test, no handler code change needed for this point.

---

## Claude's Discretion

- Exact naming/location of the new E2E test package and its gating env var(s).
- Whether `EngineFor` returns `(string, bool)` or a different zero-value convention — should mirror `Lookup`'s shape.
- Test fixture strategy for the 6 sample documents (checked-in minimal fixtures vs. generated at test time).

## Deferred Ideas

None — this is the final phase in the v1.2 milestone (DOC-10), no new capabilities were suggested during discussion.
