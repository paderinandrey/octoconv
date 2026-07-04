// Package webhook delivers signed job-completion callbacks to client-supplied
// callback URLs and tracks delivery attempts in Postgres.
package webhook

import (
	"time"

	"github.com/google/uuid"
)

// Delivery is a row of the webhook_deliveries table: one row per delivery
// attempt for a job's callback_url. delivered/dead_letter are plain booleans
// in the schema (no status enum), so there is no parallel status-string
// constant set here — unlike jobs.Status*.
type Delivery struct {
	ID         uuid.UUID
	JobID      uuid.UUID
	URL        string
	Attempt    int
	StatusCode *int
	Delivered  bool
	DeadLetter bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
