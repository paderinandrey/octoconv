---
phase: 33
slug: keda-helm-chart-integration
status: verified
threats_open: 0
asvs_level: 1
created: 2026-07-19
---

# Phase 33 — Security

> Per-phase security contract: threat register, accepted risks, and audit trail.
> Register authored at plan time from 33-01-PLAN.md / 33-02-PLAN.md / 33-03-PLAN.md `<threat_model>` blocks; verified against implemented code, not documentation, per the FORCE adversarial-audit stance.

---

## Trust Boundaries

| Boundary | Description | Data Crossing |
|----------|-------------|---------------|
| KEDA/HPA → audio-worker Deployment | Autoscaler owns the 0↔N replica decision; a wrong grace/stabilization value lets it SIGTERM a live transcription | replica-count control signal |
| Prometheus → ScaledObject PromQL | An absent/empty metric series (api down, or QueueAudio unregistered) crosses here and, mishandled, scales a busy class to zero | scaler decision input |
| Chart ConfigMap vs docker-compose env | Two independently-maintained env surfaces that can silently drift | AUDIO_* env values |
| load-proof scripts → live OrbStack cluster/daemon | Scripts start/install/uninstall cluster resources; a failed teardown leaves OrbStack hot (four daemon-wedge incidents on record) | helm releases, kubectl processes |
| watcher subprocess → parent script | A reparented `kubectl -w` pipeline can survive an EXIT trap and orphan | OS process tree |
| script edits → frozen gate scripts | Editing keda-load-proof.sh/keda-gate.sh would invalidate the Phase-29 deferred re-run's validity | git history |

---

## Threat Register

| Threat ID | Category | Component | Disposition | Mitigation | Status |
|-----------|----------|-----------|-------------|------------|--------|
| T-33-01 | Denial of Service | deployment-audio-worker.yaml `terminationGracePeriodSeconds` | mitigate | 772s (> `AUDIO_ENGINE_TIMEOUT` 742s) so a genuine downscale never premature-SIGTERMs a live transcription | closed |
| T-33-02 | Denial of Service | scaledobject-audio.yaml `scaleDownStabilizationSeconds` | mitigate | Non-null 900s (> 742s worst-case) prevents a scale-down race against an in-flight job | closed |
| T-33-03 | Denial of Service | scaledobject-audio.yaml WR-01 triad + QueueAudio collector | mitigate | `ignoreNullValues: "false"` + `fallback.replicas: 1` + retry-inclusive PromQL, verbatim from first commit; `queue.QueueAudio` registered in the collector so the series exists at zero replicas | closed |
| T-33-04 | Tampering | ConfigMap vs docker-compose config drift | mitigate | 5 `AUDIO_*` keys byte-matching compose + `RECONCILER_ACTIVE_STALE_AFTER` 5m→15m landed in the same commit | closed |
| T-33-05 | Denial of Service | keda-audio-loadproof.sh teardown()/orphaned watcher | mitigate | EXIT trap always runs; watcher launched under `set -m`, killed by process-group + `pkill -f` belt-and-suspenders (Phase-29 WR-01/02/03 pattern); post-review hardened further (WR-01/WR-02 fixes below) | closed |
| T-33-06 | Denial of Service | fresh-install scale-to-zero (audio class) | mitigate | `SADD asynq:queues image document html audio webhook` seeded before expecting a genuine zero | closed |
| T-33-07 | Tampering | frozen gate scripts (keda-load-proof.sh, keda-gate.sh) | mitigate | Byte-unchanged discipline enforced by `git diff --quiet` gate in both Plan 02 and Plan 03; audio logic isolated to the new file | closed |
| T-33-08 | Denial of Service | OrbStack daemon (build/hot-stack) | mitigate | Sequential non-`:latest` builds; compose confirmed DOWN before any k8s work; never both hot; EXIT traps + explicit `orb stop k8s` return OrbStack to rest | closed |
| T-33-09 | Repudiation | load-proof auditability | mitigate | Phase-28-style timestamped evidence dir (gate-transcript, sc3 event-timeline, burst CSV/PNG) provides an auditable trail for the SC3 live claim | closed |
| T-33-10 | Denial of Service | orphaned `kubectl -w` watcher (live re-run) | mitigate | WR-04 process-group-kill + `pkill` pattern proven live via keda-audio-loadproof.sh; `pgrep -f 'kubectl get pod'` empty post-teardown in both live runs — see caveat below | closed (caveat) |
| T-33-SC | Tampering | supply chain (installs) | accept | No npm/pip/cargo/go-get installs across all 3 plans — chart YAML, one-line Go wiring, and shell authoring only; KEDA installed from the same pinned `kedacore/keda` helm repo the frozen scripts already use | closed |

*Status: open · closed*
*Disposition: mitigate (implementation required) · accept (documented risk) · transfer (third-party)*

### Verification Evidence

- **T-33-01**: `deploy/chart/octoconv/templates/deployment-audio-worker.yaml:46` `terminationGracePeriodSeconds: {{ .Values.audioWorker.terminationGracePeriodSeconds }}`; `deploy/chart/octoconv/values.yaml:66` `terminationGracePeriodSeconds: 772`. REVIEW.md independently cross-checked 772 ≥ asynq `ShutdownTimeout` 752s (`cmd/audio-worker/main.go:113`).
- **T-33-02**: `deploy/chart/octoconv/templates/scaledobject-audio.yaml:40-49` (falsy-0-guarded stabilization block) rendering `stabilizationWindowSeconds: {{ .Values.keda.audio.scaleDownStabilizationSeconds }}`; `deploy/chart/octoconv/values.yaml:198` `scaleDownStabilizationSeconds: 900`. `helm template ... -f values-local.yaml` grep-confirmed `stabilizationWindowSeconds: 900` in rendered output (33-01-SUMMARY.md).
- **T-33-03**: `scaledobject-audio.yaml:51-53` (`fallback.failureThreshold: 3`, `replicas: 1`), `:64` (`ignoreNullValues: "false"`), `:58` (`query: sum(octoconv_queue_depth{queue="audio", state=~"pending|active|retry"})`); `cmd/api/main.go:91-92` `NewQueueDepthCollector(..., queue.QueueImage, queue.QueueDocument, queue.QueueHTML, queue.QueueAudio, queue.QueueWebhook)`. REVIEW.md cross-checked PromQL labels against `internal/metrics/queue_collector.go:9-11,40-43` and `queue.QueueAudio = convert.EngineAudio = "audio"` (`internal/queue/queue.go:38`).
- **T-33-04**: `deploy/chart/octoconv/templates/configmap.yaml:35-39` (`AUDIO_WORKER_CONCURRENCY`, `AUDIO_ENGINE_TIMEOUT`, `AUDIO_MAX_RETRY`, `AUDIO_MAX_DURATION_SECONDS`, `AUDIO_MODEL_PATH`) and `:47` (`RECONCILER_ACTIVE_STALE_AFTER: "15m"`, no remaining `"5m"`). REVIEW.md independently confirmed all 5 values mirror `docker-compose.yml`'s audio-worker block exactly and 742s < 900s stale cap.
- **T-33-05**: `scripts/keda-audio-loadproof.sh:188` `trap teardown EXIT`; `:444` `set -m` before backgrounding the snapshot loop; `:153,:465` `kill -- -"$SNAPSHOT_PID"`; `:157,:467` `pkill -f "kubectl get pod ${AUDIO_POD} .* -w"`. Post-review hardening (33-REVIEW.md WR-01/WR-02, commits `2170595`/`ab2e332`) confirmed live in code: `:406` `AUDIO_POD=$(... 2>/dev/null || true)`, `:331` and `:549` `curl ... || true`.
- **T-33-06**: `scripts/keda-audio-loadproof.sh:283` `kubectl exec ... redis-cli SADD asynq:queues image document html audio webhook`.
- **T-33-07**: `git log --oneline -- scripts/keda-load-proof.sh scripts/keda-gate.sh` shows the last touching commit is `db14b42` (Phase 29, pre-dating all Phase 33 commits); 33-01/33-02/33-03 SUMMARYs each independently re-ran `git diff --quiet HEAD -- scripts/keda-load-proof.sh scripts/keda-gate.sh` before/after live execution.
- **T-33-08**: Live evidence in 33-03-SUMMARY.md: sequential non-`:latest` image builds, compose confirmed down throughout, `orb stop k8s` executed, `helm list -A` empty and `docker info` healthy at final state.
- **T-33-09**: `.planning/phases/33-keda-helm-chart-integration/evidence/` contains `gate-transcript-20260718T211401Z.log`, `sc3-audio-scale-from-zero-20260718T211401Z.txt`, `keda-load-proof-rerun{1,2}-*-gate-transcript-*.log`, and `keda-load-proof-rerun2-sc1-sc2-burst-*.{csv,png}` — all present on disk, verified by direct read (gate-transcript shows "ALL 10 ASSERTIONS PASSED").
- **T-33-10 (caveat)**: `pgrep -f 'kubectl get pod'` returned empty after both the Plan 03 Task 1 (`keda-audio-loadproof.sh`) and Task 2 (`keda-load-proof.sh` re-run) live executions — no orphaned watcher process was found. However, per 33-03-SUMMARY.md's own account, the frozen `keda-load-proof.sh`'s SC3 branch (where `SNAPSHOT_PID`/the watcher is actually created) was **never reached** in the live re-run: `STEP 7`'s `BUSY_POD` jsonpath selection failed loud (a pre-existing, out-of-scope `WR-05` defect against kubectl client v1.36.2's handling of absent `deletionTimestamp`), aborting before any watcher was spawned by that script. The WR-04 process-group-kill+pkill *code pattern* is unchanged since Phase 29 and was proven live-functional via the structurally identical Task 1 script, and the actual DoS risk (an orphaned process from this phase's activity) did not materialize — so this threat is assessed CLOSED. But the Plan 03 threat register's literal claim ("closes the Phase-29 deferred item") is **not fully substantiated**: 33-03-SUMMARY.md itself downgrades this to "RE-VERIFIED, not cleanly closed." Flagged here for visibility; not a blocker under `block_on: high` because no absent-mitigation / live-harm condition exists — only an overstated verification narrative for one of the two scripts involved. Recommend a small forward-fix to `keda-load-proof.sh`'s `BUSY_POD` jsonpath filter (tracked in 33-03-SUMMARY.md "Next Phase Readiness") to enable a genuine end-to-end re-run in a future phase.
- **T-33-SC**: No package-manager installs across all three plans' diffs (`deploy/chart/*`, `cmd/api/main.go`, `scripts/keda-audio-loadproof.sh` are chart YAML / Go / shell only); KEDA is installed via the same pinned `kedacore/keda` helm repo the frozen scripts already use, per 33-01/33-02/33-03 threat registers.

---

## Accepted Risks Log

| Risk ID | Threat Ref | Rationale | Accepted By | Date |
|---------|------------|-----------|--------------|------|
| AR-33-01 | T-33-SC | No new supply-chain surface introduced this phase (chart YAML + one-line Go wiring + shell authoring only, against an in-repo constant); KEDA install source is the same pinned `kedacore/keda` helm repo already used by the frozen sibling scripts, re-verified live at run time. Package Legitimacy Audit not applicable per 33-RESEARCH.md. | gsd-code-reviewer / phase executor (33-01/33-02/33-03 plans) | 2026-07-18 |

*Accepted risks do not resurface in future audit runs.*

---

## Security Audit Trail

| Audit Date | Threats Total | Closed | Open | Run By |
|------------|---------------|--------|------|--------|
| 2026-07-19 | 11 | 11 | 0 | secure-phase audit agent |

---

## Sign-Off

- [x] All threats have a disposition (mitigate / accept / transfer)
- [x] Accepted risks documented in Accepted Risks Log
- [x] `threats_open: 0` confirmed
- [x] `status: verified` set in frontmatter

**Approval:** verified 2026-07-19
