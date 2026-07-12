---
status: implemented
implemented_in: "v1.3 Phase 16 (WEBH-01) — cmd/webhook-worker + PGAdvisoryLock sweeper election, verified 2026-07-12"
planted_during: "Phase 10 discussion (v1.2 Document Engine Class), 2026-07-09"
trigger_when: "A third engine-class worker (e.g. CAD) is added and an operator wants to deploy it without also deploying cmd/worker (the image engine) — or webhook delivery throughput becomes a bottleneck"
---

# SEED-002: Decouple webhook delivery from any specific engine worker binary

## The problem

Today (and after Phase 10), `cmd/worker` (the image engine) is the sole consumer of the
`webhook` asynq queue, registered alongside `TypeImageConvert` in the same `asynq.Server`
multi-queue config. `cmd/document-worker` (Phase 10) only *produces* webhook-delivery tasks
(`queue.Client.EnqueueWebhookDeliver`) — it never consumes the queue.

This means webhook delivery is silently, accidentally coupled to whether `cmd/worker`
specifically is deployed. An operator who wants to run ONLY a subset of engine workers —
e.g. a CAD-only deployment with `cmd/api` + a future `cmd/cad-worker`, and no image or
document workers at all — would get jobs that complete correctly but whose webhook
notifications never fire, because nothing is listening on the `webhook` queue.

This isn't a Phase 10 regression — the coupling exists today between `cmd/worker` and any
hypothetical engine-only deployment — but it becomes a real, not just hypothetical, problem
the moment a third engine-class worker binary exists and someone wants to deploy engines
à la carte.

## Decided design: dedicated `cmd/webhook-worker` binary

A small binary/container whose only job is consuming the `webhook` queue and delivering
payloads — no engine-specific dependencies (no libvips, no LibreOffice), always deployed
regardless of which engine workers are present. Matches the project's existing
"one binary = one concern" separation (`cmd/api` / `cmd/worker` / `cmd/document-worker` /
`cmd/migrate`) — this is just one more binary in that family. Already noted as a "cheap,
non-breaking future migration" in Phase 2's (v1.0) original CONTEXT.md.

An alternative — `cmd/api` acting as the dispatcher, running a background `asynq.Server`
consuming the `webhook` queue alongside its HTTP server — was considered and rejected: it
mixes two different runtime profiles (HTTP request-serving + background async consumption)
into one process, which the project has so far deliberately kept separate, for a marginal
operational saving (one fewer container) that doesn't outweigh the consistency cost.

Raised and decided during Phase 10 discussion. Deliberately NOT implemented in Phase 10
(scope discipline — orthogonal to the document engine, applies equally to any engine
class); this seed captures the locked design decision so implementation is a
straightforward migration whenever it's actually needed, not a fresh design discussion.

## When to Surface

- A third+ engine-class worker binary is being planned (CAD, av/ffmpeg, archive, etc.) and
  "does the operator need to deploy `cmd/worker` just to get webhooks" comes up
- An operator explicitly wants an à la carte engine deployment (only some engine workers,
  not all)
- Webhook delivery latency/throughput becomes a real operational bottleneck tied to
  `cmd/worker`'s image-conversion concurrency
