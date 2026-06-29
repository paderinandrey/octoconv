package jobs

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a job does not exist.
var ErrNotFound = errors.New("job not found")

// Repo is the jobs repository backed by a pgx pool.
type Repo struct {
	pool *pgxpool.Pool
}

// NewRepo wraps a pgx pool.
func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

// CreateParams describes a new convert job and its single input.
type CreateParams struct {
	Operation    string
	Engine       string
	SourceFormat string
	TargetFormat string
	Input        Input
}

// Create inserts a job (status=queued), its input row, and the initial
// job_events transition in one transaction, returning the new job id. The
// caller enqueues the asynq task only after this succeeds (Postgres-first
// double write).
func (r *Repo) Create(ctx context.Context, p CreateParams) (uuid.UUID, error) {
	var jobID uuid.UUID

	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO jobs (operation, engine, status, source_format, target_format)
			VALUES ($1, $2, 'queued', $3, $4)
			RETURNING id`,
			p.Operation, p.Engine, p.SourceFormat, p.TargetFormat,
		).Scan(&jobID); err != nil {
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
// event. The transition is guarded so only queued jobs move to active.
func (r *Repo) MarkActive(ctx context.Context, id uuid.UUID) error {
	return r.transition(ctx, id, StatusActive, []string{StatusQueued}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE jobs SET status = 'active', started_at = now(), attempts = attempts + 1 WHERE id = $1`, id)
		return err
	})
}

// MarkDone transitions an active job to done and stamps finished_at.
func (r *Repo) MarkDone(ctx context.Context, id uuid.UUID) error {
	return r.transition(ctx, id, StatusDone, []string{StatusActive}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE jobs SET status = 'done', finished_at = now() WHERE id = $1`, id)
		return err
	})
}

// MarkFailed transitions a job to failed, recording the error and finished_at.
func (r *Repo) MarkFailed(ctx context.Context, id uuid.UUID, code, message string) error {
	return r.transition(ctx, id, StatusFailed, []string{StatusQueued, StatusActive}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE jobs SET status = 'failed', finished_at = now(), error_code = $2, error_message = $3 WHERE id = $1`,
			id, code, message)
		return err
	})
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
	var src, tgt, code, msg *string
	err := r.pool.QueryRow(ctx, `
		SELECT id, operation, engine, status, source_format, target_format,
		       error_code, error_message, created_at, started_at, finished_at
		FROM jobs WHERE id = $1`, id,
	).Scan(&j.ID, &j.Operation, &j.Engine, &j.Status, &src, &tgt,
		&code, &msg, &j.CreatedAt, &j.StartedAt, &j.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get job: %w", err)
	}
	j.SourceFormat = deref(src)
	j.TargetFormat = deref(tgt)
	j.ErrorCode = deref(code)
	j.ErrorMessage = deref(msg)
	return &j, nil
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
// statuses (concurrency/idempotency guard).
func (r *Repo) transition(
	ctx context.Context,
	id uuid.UUID,
	to string,
	allowedFrom []string,
	apply func(ctx context.Context, tx pgx.Tx) error,
) error {
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
			`INSERT INTO job_events (job_id, from_status, to_status) VALUES ($1, $2, $3)`,
			id, from, to,
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
