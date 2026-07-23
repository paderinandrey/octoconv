---
id: SEED-006
status: dormant
planted: 2026-07-23
planted_during: "after v1.8 AV Engine close"
trigger_when: "when scoping a milestone that adds a new engine class beyond image/document/html/audio/av"
scope: large
---

# SEED-006: Archive engine class (zip/unzip)

Add an engine class for archive handling: accept an archive upload (`zip`, and possibly `tar`/`tar.gz`), **unpack** it, optionally run each contained file through the existing conversion pipeline, and **repackage** the results — a batch/fan-out conversion surface on top of the current one-file-in/one-file-out model.

## Why This Matters

Turns OctoConv from a single-file converter into a batch one ("convert everything in this zip to pdf"), which is a common real-world ask. The job schema already anticipates fan-out: `job_inputs`/`job_outputs` carry an `ordinal` column precisely so one job can have multiple inputs/outputs (currently only ordinal 0 is used). Archive handling is the first real consumer of that multi-input/output design.

## When to Surface

**Trigger:** when scoping a milestone that adds a new engine class beyond image/document/html/audio/av, or when batch/bulk conversion is requested.

## Scope Estimate

**Large** — a full milestone. The pipeline wiring is straightforward, but the **security surface is the hard part** and is mandatory, not optional:

- **Zip-bomb** protection (compression ratio + total uncompressed size ceiling) — extend the existing `MAX_DOCUMENT_UNCOMPRESSED_BYTES` / decompression-bomb pattern.
- **Path traversal** (`../`, absolute paths, symlink entries) — reject fail-closed before extraction.
- **Nested archives** / recursion depth bombs — bounded depth.
- Per-entry content re-validation (each extracted file goes through the same magic-bytes sniffing as a direct upload — never trust archive-declared types).
- Fan-out cost control: a 10-file zip is 10 conversions — needs per-job entry-count / total-work ceilings and sensible timeout budgeting.

## Breadcrumbs

- `jobs.engine` DB enum already reserves `'archive'` (0006 migration family); `jobs.operation` enum (`0001_init.sql`) already includes `'archive'`.
- The reconciler/registry `default` branch names `archive` as an anticipated future engine (`.planning/research/ARCHITECTURE.md`).
- `job_inputs`/`job_outputs.ordinal` columns (`internal/db/migrations/0001_init.sql`) exist specifically to support multi-input/multi-output jobs — no schema change needed for fan-out.
- Existing structural-ZIP/OOXML validation in `internal/convert` (document engine) is the closest precedent for safe zip inspection.

## Notes

The engine-class pattern is proven; the differentiator here is the security-first extraction layer and being the first genuine multi-input/multi-output consumer.
