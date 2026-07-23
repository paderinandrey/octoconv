# Roadmap: OctoConv

## Milestones

- ✅ **v1.0 Hardening MVP** — Phases 1-4 (shipped 2026-07-08) — see `.planning/milestones/v1.0-ROADMAP.md`
- ✅ **v1.1 Tech Debt Cleanup** — Phases 5-7 (shipped 2026-07-08) — see `.planning/milestones/v1.1-ROADMAP.md`
- ✅ **v1.2 Document Engine Class** — Phases 8-11 (shipped 2026-07-10) — see `.planning/milestones/v1.2-ROADMAP.md`
- ✅ **v1.3 Document Class v2** — Phases 12-16 (shipped 2026-07-12) — see `.planning/milestones/v1.3-ROADMAP.md`
- ✅ **v1.4 CI, Presets & Debt Cleanup** — Phases 17-19 (shipped 2026-07-13) — see `.planning/milestones/v1.4-ROADMAP.md`
- ✅ **v1.5 MCP Access & Document Fidelity** — Phases 20-23 (shipped 2026-07-13) — see `.planning/milestones/v1.5-ROADMAP.md`
- ✅ **v1.6 Kubernetes & KEDA** — Phases 24-28 (shipped 2026-07-17) — see `.planning/milestones/v1.6-ROADMAP.md`
- ✅ **v1.7 Audio Engine & Hardening** — Phases 29-33 (shipped 2026-07-18) — see `.planning/milestones/v1.7-ROADMAP.md`
- ✅ **v1.8 AV Engine (video/ffmpeg)** — Phases 34-37 (shipped 2026-07-23) — see `.planning/milestones/v1.8-ROADMAP.md`

## Phases

**Phase Numbering:**
- Integer phases (1, 2, 3): Planned milestone work
- Decimal phases (2.1, 2.2): Urgent insertions (marked with INSERTED)

Decimal phases appear between their surrounding integers in numeric order.

<details>
<summary>✅ v1.0 Hardening MVP (Phases 1-4) — SHIPPED 2026-07-08</summary>

- [x] Phase 1: Merge, Auth & Rate Limiting (4/4 plans) — completed 2026-07-04
- [x] Phase 2: Webhook Delivery (3/3 plans) — completed 2026-07-04
- [x] Phase 3: Retry-Safety & Reconciler (3/3 plans) — completed 2026-07-06
- [x] Phase 4: Content Validation, Storage Lifecycle & Observability (5/5 plans) — completed 2026-07-07

Full details: `.planning/milestones/v1.0-ROADMAP.md`

</details>

<details>
<summary>✅ v1.1 Tech Debt Cleanup (Phases 5-7) — SHIPPED 2026-07-08</summary>

- [x] Phase 5: Webhook SSRF Private-IP Opt-Out (1/1 plans) — completed 2026-07-08
- [x] Phase 6: Reconciler Webhook-Gap Sweep & Staleness Soak Test (4/4 plans) — completed 2026-07-08
- [x] Phase 7: Image Dimension Limit (Decompression-Bomb Protection) (2/2 plans) — completed 2026-07-08

Full details: `.planning/milestones/v1.1-ROADMAP.md`

</details>

<details>
<summary>✅ v1.2 Document Engine Class (Phases 8-11) — SHIPPED 2026-07-10</summary>

- [x] Phase 8: Document Content Safety & Format Detection (2/2 plans) — completed 2026-07-09
- [x] Phase 9: LibreOffice Converter Engine (2/2 plans) — completed 2026-07-09
- [x] Phase 10: Document Worker & Reconciler Integration (4/4 plans) — completed 2026-07-09
- [x] Phase 11: API Routing & End-to-End Document Conversion (4/4 plans, incl. gap closure 11-04) — completed 2026-07-10

Full details: `.planning/milestones/v1.2-ROADMAP.md`

</details>

<details>
<summary>✅ v1.3 Document Class v2 (Phases 12-16) — SHIPPED 2026-07-12</summary>

- [x] Phase 12: Tech Debt Cleanup (1/1 plans) — completed 2026-07-10
- [x] Phase 13: Cross-Format Conversion & Input Safety (3/3 plans) — completed 2026-07-10
- [x] Phase 14: Validated Conversion Options & PDF/A Export (3/3 plans) — completed 2026-07-10
- [x] Phase 15: HTML→PDF Chromium Engine (5/5 plans) — completed 2026-07-11
- [x] Phase 16: Webhook Delivery Decoupling (5/5 plans, incl. gap closure 16-05) — completed 2026-07-12

Full details: `.planning/milestones/v1.3-ROADMAP.md`

</details>

<details>
<summary>✅ v1.4 CI, Presets & Debt Cleanup (Phases 17-19) — SHIPPED 2026-07-13</summary>

- [x] Phase 17: Tech Debt Cleanup (2/2 plans) — completed 2026-07-12
- [x] Phase 18: Presets (4/4 plans) — completed 2026-07-12
- [x] Phase 19: CI Pipeline (2/2 plans) — completed 2026-07-13

Full details: `.planning/milestones/v1.4-ROADMAP.md`

</details>

<details>
<summary>✅ v1.5 MCP Access & Document Fidelity (Phases 20-23) — SHIPPED 2026-07-13</summary>

- [x] Phase 20: Presets REST CRUD & Format Discovery (2/2 plans) — completed 2026-07-13
- [x] Phase 21: MCP Server (3/3 plans) — completed 2026-07-13
- [x] Phase 22: CFB Encrypted-vs-Legacy Classification (2/2 plans) — completed 2026-07-13
- [x] Phase 23: veraPDF ISO 19005 Validation (3/3 plans) — completed 2026-07-13

Full details: `.planning/milestones/v1.5-ROADMAP.md`

</details>

<details>
<summary>✅ v1.6 Kubernetes & KEDA (Phases 24-28) — SHIPPED 2026-07-17</summary>

- [x] Phase 24: Helm Chart Core & Landmine Closure (3/3 plans) — completed 2026-07-14
- [x] Phase 25: MCP Streamable HTTP (3/3 plans) — completed 2026-07-14
- [x] Phase 26: Operator System-Presets REST (2/2 plans) — completed 2026-07-14
- [x] Phase 27: KEDA Autoscaling (3/3 plans) — completed 2026-07-16
- [x] Phase 28: Autoscale Load-Proof (3/3 plans) — completed 2026-07-17

Full details: `.planning/milestones/v1.6-ROADMAP.md`

</details>

<details>
<summary>✅ v1.7 Audio Engine & Hardening (Phases 29-33) — SHIPPED 2026-07-18</summary>

- [x] Phase 29: v1.6 Hardening Tail (3/3 plans) — completed 2026-07-17
- [x] Phase 30: Audio Engine Foundation (3/3 plans) — completed 2026-07-18
- [x] Phase 31: Queue, Worker & Routing Integration (4/4 plans) — completed 2026-07-18
- [x] Phase 32: Containerization & Local E2E + RTF Gate (5/5 plans) — completed 2026-07-18
- [x] Phase 33: KEDA/Helm Chart Integration (3/3 plans) — completed 2026-07-18

Full details: `.planning/milestones/v1.7-ROADMAP.md`

</details>

<details>
<summary>✅ v1.8 AV Engine (video/ffmpeg) (Phases 34-37) — SHIPPED 2026-07-23</summary>

- [x] Phase 34: AV Engine Foundation (3/3 plans) — completed 2026-07-20
- [x] Phase 35: Queue, Worker & Routing Integration (7/7 plans) — completed 2026-07-22
- [x] Phase 36: Containerization & RTF-Measured Timeout (5/5 plans) — completed 2026-07-23
- [x] Phase 37: KEDA/Helm Chart Integration (3/3 plans) — completed 2026-07-23

Full details: `.planning/milestones/v1.8-ROADMAP.md`

</details>

## Progress

| Phase | Milestone | Plans Complete | Status | Completed |
|-------|-----------|-----------------|--------|-----------|
| 1. Merge, Auth & Rate Limiting | v1.0 | 4/4 | Complete | 2026-07-04 |
| 2. Webhook Delivery | v1.0 | 3/3 | Complete | 2026-07-04 |
| 3. Retry-Safety & Reconciler | v1.0 | 3/3 | Complete | 2026-07-06 |
| 4. Content Validation, Storage Lifecycle & Observability | v1.0 | 5/5 | Complete | 2026-07-07 |
| 5. Webhook SSRF Private-IP Opt-Out | v1.1 | 1/1 | Complete | 2026-07-08 |
| 6. Reconciler Webhook-Gap Sweep & Staleness Soak Test | v1.1 | 4/4 | Complete | 2026-07-08 |
| 7. Image Dimension Limit (Decompression-Bomb Protection) | v1.1 | 2/2 | Complete | 2026-07-08 |
| 8. Document Content Safety & Format Detection | v1.2 | 2/2 | Complete | 2026-07-09 |
| 9. LibreOffice Converter Engine | v1.2 | 2/2 | Complete | 2026-07-09 |
| 10. Document Worker & Reconciler Integration | v1.2 | 4/4 | Complete | 2026-07-09 |
| 11. API Routing & End-to-End Document Conversion | v1.2 | 4/4 | Complete | 2026-07-10 |
| 12. Tech Debt Cleanup | v1.3 | 1/1 | Complete    | 2026-07-10 |
| 13. Cross-Format Conversion & Input Safety | v1.3 | 3/3 | Complete    | 2026-07-10 |
| 14. Validated Conversion Options & PDF/A Export | v1.3 | 3/3 | Complete    | 2026-07-10 |
| 15. HTML→PDF Chromium Engine | v1.3 | 5/5 | Complete    | 2026-07-11 |
| 16. Webhook Delivery Decoupling | v1.3 | 5/5 | Complete | 2026-07-12 |
| 17. Tech Debt Cleanup | v1.4 | 2/2 | Complete | 2026-07-12 |
| 18. Presets | v1.4 | 4/4 | Complete | 2026-07-12 |
| 19. CI Pipeline | v1.4 | 2/2 | Complete | 2026-07-13 |
| 20. Presets REST CRUD & Format Discovery | v1.5 | 2/2 | Complete | 2026-07-13 |
| 21. MCP Server | v1.5 | 3/3 | Complete | 2026-07-13 |
| 22. CFB Classification | v1.5 | 2/2 | Complete | 2026-07-13 |
| 23. veraPDF ISO 19005 Validation | v1.5 | 3/3 | Complete | 2026-07-13 |
| 24. Helm Chart Core & Landmine Closure | v1.6 | 3/3 | Complete | 2026-07-14 |
| 25. MCP Streamable HTTP | v1.6 | 3/3 | Complete | 2026-07-14 |
| 26. Operator System-Presets REST | v1.6 | 2/2 | Complete    | 2026-07-14 |
| 27. KEDA Autoscaling | v1.6 | 3/3 | Complete    | 2026-07-16 |
| 28. Autoscale Load-Proof | v1.6 | 3/3 | Complete    | 2026-07-17 |
| 29. v1.6 Hardening Tail | v1.7 | 3/3 | Complete    | 2026-07-17 |
| 30. Audio Engine Foundation | v1.7 | 3/3 | Complete    | 2026-07-18 |
| 31. Queue, Worker & Routing Integration | v1.7 | 4/4 | Complete    | 2026-07-18 |
| 32. Containerization & Local E2E + RTF Gate | v1.7 | 5/5 | Complete    | 2026-07-18 |
| 33. KEDA/Helm Chart Integration | v1.7 | 3/3 | Complete    | 2026-07-18 |
| 34. AV Engine Foundation | v1.8 | 3/3 | Complete    | 2026-07-20 |
| 35. Queue, Worker & Routing Integration | v1.8 | 7/7 | Complete    | 2026-07-22 |
| 36. Containerization & RTF-Measured Timeout | v1.8 | 5/5 | Complete    | 2026-07-23 |
| 37. KEDA/Helm Chart Integration | v1.8 | 3/3 | Complete    | 2026-07-23 |

---

*v1.7 shipped 2026-07-18. v1.8 (AV Engine — video/ffmpeg) roadmapped 2026-07-19, Phases 34-37. Next: `/gsd:plan-phase 34`.*
