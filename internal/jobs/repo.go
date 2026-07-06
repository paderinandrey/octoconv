package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a job does not exist.
var ErrNotFound = errors.New("job not found")

// detailActionRecovery is the job_events.detail->>'action' tag written by
// RequeueStale and read back by RecoveryCount. Both MUST reference this
// single constant (never a literal string) so the tag can never drift out of
// sync between the writer and the reader — a mismatch would silently break
// the reconciler's recovery cap (RECON-02, Pitfall 5).
const detailActionRecovery = "reconciler_recovery"

// StaleJob is a lightweight row returned by FindStale: just enough for the
// reconciler to decide how to recover the job (id + the status it was found
// stranded in).
type StaleJob struct {
	ID     uuid.UUID
	Status string
}

// Repo is the jobs repository backed by a pgx pool.
type Repo struct {
	pool *pgxpool.Pool
}

// NewRepo wraps a pgx pool.
func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

// CreateParams describes a new convert job and its single input. ID is the
// caller-provided job id so storage keys (which embed the id) and the row match.
type CreateParams struct {
	ID           uuid.UUID
	ClientID     uuid.UUID
	Operation    string
	Engine       string
	SourceFormat string
	TargetFormat string
	CallbackURL  string
	Input        Input
}

// Create inserts a job (status=queued), its input row, and the initial
// job_events transition in one transaction, returning the job id. If ID is the
// zero value one is generated. The caller enqueues the asynq task only after
// this succeeds (Postgres-first double write).
func (r *Repo) Create(ctx context.Context, p CreateParams) (uuid.UUID, error) {
	jobID := p.ID
	if jobID == uuid.Nil {
		jobID = uuid.New()
	}

	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO jobs (id, client_id, operation, engine, status, source_format, target_format, callback_url)
			VALUES ($1, $2, $3, $4, 'queued', $5, $6, $7)`,
			jobID, p.ClientID, p.Operation, p.Engine, p.SourceFormat, p.TargetFormat, p.CallbackURL,
		); err != nil {
			return fmt.Errorf("insert job: %w", err)
		}

		in := p.Input
		if _, err := tx.Exec(ctx, `
			INSERT INTO job_inputs (job_id, ordinal, object_key, filename, format, size_bytes, content_type)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			jobID, in.Ordinal, in.ObjectKey, in.Filename, in.Format, in.SizeBytes, in.ContentType,
		); err != nil {
			return fmt.Errorf("insert job_input: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO job_events (job_id, from_status, to_status)
			VALUES ($1, NULL, 'queued')`, jobID,
		); err != nil {
			return fmt.Errorf("insert job_event: %w", err)
		}
		return nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	return jobID, nil
}

// MarkActive transitions a job to active and stamps started_at, appending an
// event. The transition allows queued->active AND active->active so asynq's
// internal same-task retry re-entering the handler does not trip the illegal-
// transition guard; started_at uses COALESCE so it stays pinned to the FIRST
// activation, not the most recent retry, which the reconciler's active-
// staleness check depends on for true elapsed running time.
func (r *Repo) MarkActive(ctx context.Context, id uuid.UUID) error {
	return r.transition(ctx, id, StatusActive, []string{StatusQueued, StatusActive}, nil, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE jobs SET status = 'active', started_at = COALESCE(started_at, now()), attempts = attempts + 1 WHERE id = $1`, id)
		return err
	})
}

// MarkDone transitions an active job to done and stamps finished_at.
func (r *Repo) MarkDone(ctx context.Context, id uuid.UUID) error {
	return r.transition(ctx, id, StatusDone, []string{StatusActive}, nil, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE jobs SET status = 'done', finished_at = now() WHERE id = $1`, id)
		return err
	})
}

// MarkFailed transitions a job to failed, recording the error and finished_at.
// detail is an optional structured payload (e.g. raw engine stderr) attached
// to the job_events row for internal diagnostics only — error_message/code
// stay short and sanitized since they are exposed via GET /jobs/{id} and
// webhook payloads.
func (r *Repo) MarkFailed(ctx context.Context, id uuid.UUID, code, message string, detail map[string]any) error {
	return r.transition(ctx, id, StatusFailed, []string{StatusQueued, StatusActive}, detail, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE jobs SET status = 'failed', finished_at = now(), error_code = $2, error_message = $3 WHERE id = $1`,
			id, code, message)
		return err
	})
}

// RequeueStale requeues a job stranded in queued (lost enqueue) or active
// (crashed worker / exhausted asynq retry budget) back to queued, via the
// same guarded, row-locked transition every other status change goes through
// — NEVER an ad-hoc UPDATE. reason is a short machine-readable tag (e.g.
// "stale_queued"/"stale_active") recorded in the job_events.detail payload
// alongside the reconciler_recovery action tag, so the recovery cap
// (RecoveryCount) and audit trail (RECON-03) stay consistent.
func (r *Repo) RequeueStale(ctx context.Context, id uuid.UUID, reason string) error {
	detail := map[string]any{"action": detailActionRecovery, "reason": reason}
	return r.transition(ctx, id, StatusQueued, []string{StatusQueued, StatusActive}, detail, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE jobs SET status = 'queued' WHERE id = $1`, id)
		return err
	})
}

// RecoveryCount returns how many times the reconciler has already requeued
// this job (i.e. the number of job_events rows tagged detailActionRecovery),
// so the sweeper can compare against the recovery cap (RECONCILER_MAX_RECOVERIES).
func (r *Repo) RecoveryCount(ctx context.Context, id uuid.UUID) (int, error) {
	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM job_events WHERE job_id = $1 AND detail->>'action' = $2`,
		id, detailActionRecovery,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("count recoveries for job %s: %w", id, err)
	}
	return n, nil
}

// FindStale returns jobs stranded past their staleness threshold: queued
// jobs older than queuedStaleAfter (lost enqueue) or active jobs whose
// started_at is older than activeStaleAfter (crashed worker / exhausted
// asynq retry budget). Cutoffs are computed in Go and bound as timestamptz
// parameters so the comparison stays index-friendly against jobs_inflight_idx
// (created_at) WHERE status IN ('queued','active').
func (r *Repo) FindStale(ctx context.Context, queuedStaleAfter, activeStaleAfter time.Duration) ([]StaleJob, error) {
	now := time.Now()
	queuedCutoff := now.Add(-queuedStaleAfter)
	activeCutoff := now.Add(-activeStaleAfter)

	rows, err := r.pool.Query(ctx, `
		SELECT id, status FROM jobs
		WHERE (status = 'queued' AND created_at < $1)
		   OR (status = 'active' AND started_at < $2)`,
		queuedCutoff, activeCutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("query stale jobs: %w", err)
	}
	defer rows.Close()

	var out []StaleJob
	for rows.Next() {
		var j StaleJob
		if err := rows.Scan(&j.ID, &j.Status); err != nil {
			return nil, fmt.Errorf("scan stale job: %w", err)
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// AddOutput inserts a job_outputs row.
func (r *Repo) AddOutput(ctx context.Context, jobID uuid.UUID, o Output) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO job_outputs (job_id, ordinal, object_key, filename, format, size_bytes, content_type)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		jobID, o.Ordinal, o.ObjectKey, o.Filename, o.Format, o.SizeBytes, o.ContentType)
	if err != nil {
		return fmt.Errorf("insert job_output: %w", err)
	}
	return nil
}

// Get loads a job by id. Returns ErrNotFound if it does not exist.
func (r *Repo) Get(ctx context.Context, id uuid.UUID) (*Job, error) {
	var j Job
	var src, tgt, cb, code, msg *string
	var clientID *uuid.UUID
	err := r.pool.QueryRow(ctx, `
		SELECT id, client_id, operation, engine, status, source_format, target_format,
		       callback_url, error_code, error_message, created_at, started_at, finished_at
		FROM jobs WHERE id = $1`, id,
	).Scan(&j.ID, &clientID, &j.Operation, &j.Engine, &j.Status, &src, &tgt,
		&cb, &code, &msg, &j.CreatedAt, &j.StartedAt, &j.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get job: %w", err)
	}
	// client_id is ON DELETE SET NULL (0001_init.sql); a legacy or orphaned
	// row can have a null client_id, so scan via a pointer and default to
	// uuid.Nil, matching the nullable-column deref style used elsewhere here.
	if clientID != nil {
		j.ClientID = *clientID
	}
	j.SourceFormat = deref(src)
	j.TargetFormat = deref(tgt)
	j.CallbackURL = deref(cb)
	j.ErrorCode = deref(code)
	j.ErrorMessage = deref(msg)
	return &j, nil
}

// Inputs lists a job's inputs ordered by ordinal.
func (r *Repo) Inputs(ctx context.Context, jobID uuid.UUID) ([]Input, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT ordinal, object_key, filename, format, size_bytes, content_type
		FROM job_inputs WHERE job_id = $1 ORDER BY ordinal`, jobID)
	if err != nil {
		return nil, fmt.Errorf("query inputs: %w", err)
	}
	defer rows.Close()

	var out []Input
	for rows.Next() {
		var in Input
		var name, format, ct *string
		var size *int64
		if err := rows.Scan(&in.Ordinal, &in.ObjectKey, &name, &format, &size, &ct); err != nil {
			return nil, fmt.Errorf("scan input: %w", err)
		}
		in.Filename, in.Format, in.ContentType = deref(name), deref(format), deref(ct)
		if size != nil {
			in.SizeBytes = *size
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// Outputs lists a job's outputs ordered by ordinal.
func (r *Repo) Outputs(ctx context.Context, jobID uuid.UUID) ([]Output, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT ordinal, object_key, filename, format, size_bytes, content_type
		FROM job_outputs WHERE job_id = $1 ORDER BY ordinal`, jobID)
	if err != nil {
		return nil, fmt.Errorf("query outputs: %w", err)
	}
	defer rows.Close()

	var out []Output
	for rows.Next() {
		var o Output
		var key, name, format, ct *string
		var size *int64
		if err := rows.Scan(&o.Ordinal, &key, &name, &format, &size, &ct); err != nil {
			return nil, fmt.Errorf("scan output: %w", err)
		}
		o.ObjectKey, o.Filename, o.Format, o.ContentType = deref(key), deref(name), deref(format), deref(ct)
		if size != nil {
			o.SizeBytes = *size
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// transition performs a guarded status change plus an append to job_events in a
// single transaction. It errors if the job is not in one of the allowed source
// statuses (concurrency/idempotency guard). detail, when non-nil, is marshaled
// into the job_events.detail jsonb column; when nil, the column stays NULL.
func (r *Repo) transition(
	ctx context.Context,
	id uuid.UUID,
	to string,
	allowedFrom []string,
	detail map[string]any,
	apply func(ctx context.Context, tx pgx.Tx) error,
) error {
	var detailJSON []byte
	if detail != nil {
		var err error
		detailJSON, err = json.Marshal(detail)
		if err != nil {
			return fmt.Errorf("marshal transition detail: %w", err)
		}
	}
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		var from string
		if err := tx.QueryRow(ctx,
			`SELECT status FROM jobs WHERE id = $1 FOR UPDATE`, id,
		).Scan(&from); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("lock job: %w", err)
		}

		if !contains(allowedFrom, from) {
			return fmt.Errorf("illegal transition %s -> %s for job %s", from, to, id)
		}

		if err := apply(ctx, tx); err != nil {
			return fmt.Errorf("apply transition: %w", err)
		}

		if _, err := tx.Exec(ctx,
			`INSERT INTO job_events (job_id, from_status, to_status, detail) VALUES ($1, $2, $3, $4)`,
			id, from, to, detailJSON,
		); err != nil {
			return fmt.Errorf("insert job_event: %w", err)
		}
		return nil
	})
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
