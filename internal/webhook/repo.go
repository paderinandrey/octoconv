package webhook

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repo is the webhook_deliveries repository backed by a pgx pool.
type Repo struct {
	pool *pgxpool.Pool
}

// NewRepo wraps a pgx pool.
func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

// RecordAttempt inserts one webhook_deliveries row for a single delivery
// attempt and returns its id. statusCode is nullable: pass nil for
// network/timeout failures that never received an HTTP response.
func (r *Repo) RecordAttempt(ctx context.Context, jobID uuid.UUID, url string, attempt int, statusCode *int, delivered bool) (uuid.UUID, error) {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `
		INSERT INTO webhook_deliveries (job_id, url, attempt, status_code, delivered)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`,
		jobID, url, attempt, statusCode, delivered,
	).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("record webhook attempt: %w", err)
	}
	return id, nil
}

// MarkDeadLetter flags a delivery row as dead_letter, once asynq exhausts
// MaxRetry for the underlying task (D-10). webhook_deliveries has no status
// enum to guard (unlike jobs.status), but the lock-then-update discipline
// still applies via pgx.BeginFunc.
func (r *Repo) MarkDeadLetter(ctx context.Context, deliveryID uuid.UUID) error {
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE webhook_deliveries SET dead_letter = true WHERE id = $1`, deliveryID)
		if err != nil {
			return fmt.Errorf("mark dead letter: %w", err)
		}
		return nil
	})
}
