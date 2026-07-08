// Package reconciler periodically sweeps Postgres for jobs stranded in
// queued/active past a staleness threshold and requeues or terminally fails
// them, bounded by a recovery cap.
package reconciler

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/apaderin/octoconv/internal/jobs"
	"github.com/apaderin/octoconv/internal/metrics"
)

// Config tunes the sweep: how stale a queued/active job must be before it is
// considered stranded, how often to sweep, and how many recoveries a single
// job may accumulate before it is terminally failed.
type Config struct {
	QueuedStaleAfter time.Duration
	ActiveStaleAfter time.Duration
	SweepInterval    time.Duration
	MaxRecoveries    int
}

// jobStore is the subset of *jobs.Repo the sweeper depends on (interface
// segregation, mirroring internal/api/api.go), so Sweeper is unit-testable
// with an in-memory fake — no DB required.
type jobStore interface {
	FindStale(ctx context.Context, queuedStaleAfter, activeStaleAfter time.Duration) ([]jobs.StaleJob, error)
	RecoveryCount(ctx context.Context, id uuid.UUID) (int, error)
	RequeueStale(ctx context.Context, id uuid.UUID, reason string) error
	MarkFailed(ctx context.Context, id uuid.UUID, code, message string, detail map[string]any) error
	Get(ctx context.Context, id uuid.UUID) (*jobs.Job, error)
	FindWebhookGaps(ctx context.Context, activeStaleAfter time.Duration) ([]jobs.WebhookGapJob, error)
	RecordWebhookGapRecovered(ctx context.Context, id uuid.UUID, status string) error
}

// enqueuer is the subset of *queue.Client the sweeper depends on.
type enqueuer interface {
	EnqueueImageConvert(ctx context.Context, id uuid.UUID) error
	EnqueueWebhookDeliver(ctx context.Context, id uuid.UUID) error
}

// Sweeper periodically scans for stale jobs and recovers or exhausts them.
type Sweeper struct {
	store jobStore
	enq   enqueuer
	cfg   Config
}

// NewSweeper builds a Sweeper. store and enq are typically *jobs.Repo and
// *queue.Client, which satisfy jobStore/enqueuer concretely.
func NewSweeper(store jobStore, enq enqueuer, cfg Config) *Sweeper {
	return &Sweeper{store: store, enq: enq, cfg: cfg}
}

// Run ticks every cfg.SweepInterval and calls sweep until ctx is cancelled,
// at which point it stops the ticker and returns promptly (no leaked
// goroutine).
func (s *Sweeper) Run(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.SweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweep(ctx)
		}
	}
}

// sweep scans for stale jobs and, for each one under the recovery cap,
// attempts an enqueue-first recovery guarded by asynq.ErrDuplicateTask; jobs
// at or over the cap are terminally failed with a webhook fired on
// exhaustion if callback_url is set. A per-job error is swallowed (best
// effort — the next tick retries) so one bad job never stalls the sweep. No
// logging is added here — visibility is limited to job_events (D-15); Phase
// 4 owns OBS logging/metrics.
//
// sweep also runs a SECOND, independent scan (RECON-04, Phase 6) for
// done/failed jobs whose completion webhook was silently never enqueued
// (e.g. a Redis blip at the exact moment the worker tried to fire
// EnqueueWebhookDeliver). This webhook-gap recovery is enqueue-first and
// asynq.ErrDuplicateTask-guarded exactly like the queued/active loop above,
// but it is a ONE-SHOT, self-terminating action: it uses
// detailActionWebhookGapRecovered (not detailActionRecovery), so
// RecoveryCount never counts it toward MaxRecoveries. There is nothing to
// "exhaust" — once a webhook_deliveries row exists (delivered, undelivered,
// or dead-lettered), FindWebhookGaps never matches that job again (D-05).
func (s *Sweeper) sweep(ctx context.Context) {
	stale, err := s.store.FindStale(ctx, s.cfg.QueuedStaleAfter, s.cfg.ActiveStaleAfter)
	if err != nil {
		// Best-effort: the next tick retries.
		return
	}

	for _, j := range stale {
		n, err := s.store.RecoveryCount(ctx, j.ID)
		if err != nil {
			// Best-effort: skip this job, the next tick retries.
			continue
		}

		if n >= s.cfg.MaxRecoveries {
			// Cap exceeded: terminally fail and, if a callback_url is set,
			// fire a webhook (D-14, Postgres-first best-effort — the failed
			// status is already committed by MarkFailed above, so a failed
			// enqueue must not undo it).
			job, _ := s.store.Get(ctx, j.ID)
			_ = s.store.MarkFailed(ctx, j.ID, "reconciler_exhausted", "recovery attempts exhausted", map[string]any{"action": "reconciler_exhausted"})
			metrics.RecordReconcilerAction("exhausted")
			if job != nil && job.CallbackURL != "" {
				_ = s.enq.EnqueueWebhookDeliver(ctx, j.ID)
			}
			continue
		}

		// Under the cap: attempt recovery ENQUEUE-FIRST so the asynq.Unique
		// lock (Plan 01) decides whether a task is genuinely needed. Only a
		// successful, non-duplicate enqueue proves the job is actually
		// stranded (no live task/lock) rather than merely backlogged or
		// still being retried by asynq.
		if err := s.enq.EnqueueImageConvert(ctx, j.ID); err != nil {
			if errors.Is(err, asynq.ErrDuplicateTask) {
				// A live task/lock for this job already exists — the job is
				// backlogged or asynq is still retrying it, NOT stranded.
				// This is the expected, safe case: no status change, no
				// recovery event, so a merely-backlogged queued job can
				// never accrue a spurious recovery toward MaxRecoveries and
				// be falsely driven to reconciler_exhausted (RECON-01).
				continue
			}
			// Any other transient enqueue error: best-effort, retry next tick.
			continue
		}

		// Only after a SUCCESSFUL, non-duplicate enqueue do we flip the row
		// back to queued and record the recovery event. RequeueStale is
		// called AFTER (not before) the enqueue specifically so a
		// legitimately-backlogged job never accrues a spurious recovery.
		reason := "stale_" + j.Status
		if err := s.store.RequeueStale(ctx, j.ID, reason); err != nil {
			// A fresh task has ALREADY been enqueued at this point. If the
			// recovery event silently fails to record, RecoveryCount would
			// under-count against MaxRecoveries — a permanently-broken job
			// could then receive MORE than MaxRecoveries real re-enqueues
			// before finally being exhausted. To degrade gracefully instead
			// of silently under-counting, retry exactly ONCE more (bounded —
			// never an unbounded loop, so a persistently-failing DB never
			// stalls the sweep).
			if err := s.store.RequeueStale(ctx, j.ID, reason); err != nil {
				// Both attempts failed: discard the error. This job's
				// recovery event is lost for this sweep (the cap
				// under-counts by one); the already-enqueued fresh task
				// still processes idempotently via MarkActive, and the next
				// sweep re-checks this job's (still active/queued) state.
				//
				// The common benign case behind this path: a legitimately-
				// slow original worker completed the job between FindStale
				// and RequeueStale (status now done/failed), so
				// RequeueStale's guarded transition correctly returns an
				// illegal-transition error on both attempts — no recovery
				// was actually needed, and the freshly-enqueued task no-ops
				// at MarkActive.
				//
				// T-03-12 (accepted residual, symmetric over-count case): if
				// the FIRST RequeueStale call actually committed
				// server-side but the client observed a transport error
				// between COMMIT and ack (a case pgx.BeginFunc does not
				// exclude), this bounded retry runs a SECOND genuinely-
				// successful transition and writes a SECOND
				// reconciler_recovery event for ONE real recovery —
				// over-counting RecoveryCount by one, so the job could hit
				// reconciler_exhausted one recovery earlier than intended.
				// This is bounded (one extra event, since the retry is
				// single-shot), low-probability, and does NOT reopen the
				// no-duplicate-task guarantee (independently protected by
				// asynq.Unique, T-03-10) — accepted and documented rather
				// than defended with a pre-write idempotency check, since a
				// clean "was this exact recovery already recorded this
				// sweep" check against the append-only event log is not
				// straightforward and the cost (one early exhaustion in a
				// rare double-fault) is acceptable for the MVP.
				continue
			}
		}

		// Reached only after a genuinely successful RequeueStale (first
		// attempt or the single bounded retry) — never on the
		// asynq.ErrDuplicateTask continue above, which is a backlogged
		// no-op, not a recovery.
		metrics.RecordReconcilerAction("recovered")
	}

	// Second scan (RECON-04): done/failed jobs with a callback_url and zero
	// webhook_deliveries rows, past ActiveStaleAfter since finished_at
	// (D-04, reusing the existing threshold rather than a new one).
	// Best-effort on a finder error, same discipline as FindStale above.
	gaps, err := s.store.FindWebhookGaps(ctx, s.cfg.ActiveStaleAfter)
	if err != nil {
		return
	}

	for _, g := range gaps {
		// Enqueue-first: the asynq.Unique lock (Plan 01) is the actual
		// duplicate-delivery guard. Only a successful, non-duplicate
		// enqueue proves this is a genuine gap rather than a delivery
		// already live/queued for this job.
		if err := s.enq.EnqueueWebhookDeliver(ctx, g.ID); err != nil {
			if errors.Is(err, asynq.ErrDuplicateTask) {
				// A delivery is already live/queued for this job — not
				// actually a gap, skip silently (same reasoning as the
				// image-recovery loop's duplicate guard above).
				continue
			}
			// Any other transient enqueue error: best-effort, retry next tick.
			continue
		}

		// Only after a successful, non-duplicate enqueue: record the
		// recovery event and the metric. This is uncapped/single-shot —
		// RecordWebhookGapRecovered does not touch RecoveryCount/MaxRecoveries.
		_ = s.store.RecordWebhookGapRecovered(ctx, g.ID, g.Status)
		metrics.RecordReconcilerAction("webhook_gap_recovered")
	}
}
