---
id: SEED-005
status: dormant
planted: 2026-07-23
planted_during: "after v1.8 AV Engine close"
trigger_when: "when scoping a milestone that adds a new engine class beyond image/document/html/audio/av"
scope: large
---

# SEED-005: CAD engine class

Add a sixth engine class for CAD drawings: convert `dwg`/`dxf`/`step`/`iges` (and similar) to `pdf`/`svg` plus a raster preview image, following the proven engine-class pattern (own queue/worker/binary/container/KEDA, fail-closed magic-bytes content validation, stage-aware transient/terminal retry) established across v1.2–v1.8.

## Why This Matters

CAD is the most-requested "next" format family for an internal conversion service, and the pipeline is already shaped for it — but it carries a **real, unresolved evaluation gate** that must be answered before it can be planned: native CAD-format handling is OSS libraries (e.g. LibreCAD/ODA-free tooling, limited format coverage) vs. a **commercial SDK** (ODA/Teigha, Autodesk — licensing cost + redistribution terms) vs. a **cloud API** (breaks the "workers stay offline, no external API" architectural constraint). This SDK/licensing/offline trade-off is the crux; it is noted as an open question in `PROJECT.md` and was deliberately deferred out of every milestone so far.

## When to Surface

**Trigger:** when scoping a milestone that adds a new engine class beyond image/document/html/audio/av — or specifically when a customer/internal request for CAD conversion arrives.

The very first planning step must be an **evaluation phase** (OSS vs commercial SDK vs cloud) — do NOT skip straight to an engine skeleton, because the format-handling decision dictates the container, the licensing, and whether the offline-worker constraint holds.

## Scope Estimate

**Large** — a full milestone. Evaluation gate + engine + worker + container (heavy, possibly licensed) + KEDA + content validation for a binary format family with weak magic-bytes structure (DWG version headers, DXF text, STEP/IGES ASCII).

## Breadcrumbs

- `jobs.engine` DB enum already reserves `'cad'` (added speculatively alongside `'av'`/`'archive'`/`'probe'` in the `0006_audio_engine.sql` migration family) — no migration needed to route it.
- The converter registry / reconciler `default` branch already documents CAD as an anticipated future engine: "av/cad/archive/probe are out of scope this milestone… a future engine must add its own case" (`.planning/research/ARCHITECTURE.md`).
- `PROJECT.md` records the CAD SDK open question and the "остальные классы движков — следующий этап роста" pending decision.
- `jobs.operation` enum (`0001_init.sql`) already includes `'render'` — usable for a CAD→raster-preview operation.

## Notes

Follows the engine-class template proven five times (image/document/html/audio/av). The novel risk is entirely in the format-handling choice, not the pipeline wiring.
