---
id: SEED-007
status: dormant
planted: 2026-07-23
planted_during: "after v1.8 AV Engine close"
trigger_when: "when scoping a milestone that adds a new engine class beyond image/document/html/audio/av"
scope: medium
---

# SEED-007: File-preview / thumbnail engine

A first-class `preview` engine that generates a preview image / thumbnail for arbitrary uploaded file types — documents (first-page render), images (downscaled thumb), video (representative frame), etc. — so any file in the system can get a lightweight visual representation for listings/UIs.

## Why This Matters

Every file-management surface eventually wants thumbnails. Today the capability exists only in fragments: `av-worker` extracts a video thumbnail frame, and libvips/Chromium/LibreOffice can each render a preview of their own formats — but there is no unified "give me a preview of THIS file, whatever it is" entry point. Generalizing it into one `preview` class (routing internally to the right renderer per detected type) is a small, high-leverage product feature. **Not currently captured anywhere in planning** before this seed.

## When to Surface

**Trigger:** when scoping a milestone that adds a new engine class, or when a UI/listing feature needs thumbnails.

## Scope Estimate

**Medium** — a phase or two. Much of the underlying rendering already exists (video frame via ffmpeg, image downscale via libvips, doc first-page via LibreOffice/Chromium); the work is a unifying `preview` engine + routing-by-detected-type + a consistent output contract (fixed max dimensions, format, fail-closed on unpreviewable types) rather than a brand-new external binary.

## Breadcrumbs

- `av-worker` already does video thumbnail extraction (`internal/convert/av.go`, thumbnail-to-jpg/png/webp) — the seed generalizes this to all types.
- `jobs.operation` enum (`0001_init.sql`) already includes `'render'` — the natural operation value for preview generation.
- Distinct from the reserved `'probe'` engine value (which reads as metadata/`inspect`-probing, not visual preview) and the `'inspect'` operation — preview is visual output, probe is data output.
- libvips (image), Chromium (HTML/first-page), LibreOffice (document first-page) are all already wired as engines and can back the per-type renderers.

## Notes

Lower-risk than CAD/archive: reuses existing renderers behind one routing surface. The design decision is the output contract (dimensions/format/fallback) and whether preview is a standalone engine or a cross-cutting operation on existing engines.
