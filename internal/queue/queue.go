// Package queue defines the asynq task types and helpers used to dispatch
// conversion work to the engine-class workers.
package queue

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

// Task type names. One asynq task type per engine class operation.
const (
	TypeImageConvert   = "image:convert"
	TypeWebhookDeliver = "webhook:deliver"
)

// Queue names. asynq routes tasks to a queue per engine class so workers and
// autoscaling can be scoped to a single class.
const (
	QueueImage   = "image"
	QueueWebhook = "webhook"
)

// ConvertPayload is the task payload. It carries only the job id — all task
// details live in Postgres, the system of record.
type ConvertPayload struct {
	JobID uuid.UUID `json:"job_id"`
}

// WebhookPayload is the task payload for a webhook delivery attempt. It
// carries only the job id — the handler re-reads callback_url/status/error
// details from Postgres per attempt, same minimal-payload discipline as
// ConvertPayload (D-04).
type WebhookPayload struct {
	JobID uuid.UUID `json:"job_id"`
}

// NewImageConvertTask builds an asynq task for an image conversion job, routed
// to the image queue.
func NewImageConvertTask(jobID uuid.UUID) (*asynq.Task, error) {
	b, err := json.Marshal(ConvertPayload{JobID: jobID})
	if err != nil {
		return nil, fmt.Errorf("marshal convert payload: %w", err)
	}
	return asynq.NewTask(TypeImageConvert, b, asynq.Queue(QueueImage)), nil
}

// ParseConvertPayload decodes a ConvertPayload from a task body.
func ParseConvertPayload(b []byte) (ConvertPayload, error) {
	var p ConvertPayload
	if err := json.Unmarshal(b, &p); err != nil {
		return p, fmt.Errorf("unmarshal convert payload: %w", err)
	}
	return p, nil
}

// NewWebhookDeliverTask builds an asynq task for a single webhook delivery
// job, routed to the webhook queue and bounded to MaxRetry=6 (D-05) — retry
// count is a property of the task, set once at creation.
func NewWebhookDeliverTask(jobID uuid.UUID) (*asynq.Task, error) {
	b, err := json.Marshal(WebhookPayload{JobID: jobID})
	if err != nil {
		return nil, fmt.Errorf("marshal webhook payload: %w", err)
	}
	return asynq.NewTask(TypeWebhookDeliver, b, asynq.Queue(QueueWebhook), asynq.MaxRetry(6)), nil
}

// ParseWebhookPayload decodes a WebhookPayload from a task body.
func ParseWebhookPayload(b []byte) (WebhookPayload, error) {
	var p WebhookPayload
	if err := json.Unmarshal(b, &p); err != nil {
		return p, fmt.Errorf("unmarshal webhook payload: %w", err)
	}
	return p, nil
}

// webhookRetrySchedule is the base backoff schedule for webhook delivery
// retries (D-05): 30s, 1m, 2m, 4m, 8m, 15m — a ~30 minute total retry window
// across MaxRetry=6 attempts.
var webhookRetrySchedule = []time.Duration{
	30 * time.Second,
	1 * time.Minute,
	2 * time.Minute,
	4 * time.Minute,
	8 * time.Minute,
	15 * time.Minute,
}

// WebhookRetryDelay is an asynq RetryDelayFunc for the webhook queue. asynq
// calls this with n = the 0-based count of retries so far (0 on the first
// retry, after the first delivery attempt failed), so n indexes directly
// into webhookRetrySchedule with no off-by-one adjustment. The index is
// clamped to the last entry once n exceeds the schedule length, with up to
// ±25% jitter so simultaneously-failing deliveries don't thundering-herd a
// recovering endpoint (D-05).
func WebhookRetryDelay(n int, e error, t *asynq.Task) time.Duration {
	idx := n
	if idx < 0 {
		idx = 0
	}
	if idx >= len(webhookRetrySchedule) {
		idx = len(webhookRetrySchedule) - 1
	}
	base := webhookRetrySchedule[idx]

	// Jitter of up to ±25%.
	jitterRange := float64(base) * 0.25
	jitter := (rand.Float64()*2 - 1) * jitterRange
	delay := time.Duration(float64(base) + jitter)
	if delay < 0 {
		delay = 0
	}
	return delay
}

// RedisOpt builds the asynq Redis connection options from REDIS_ADDR.
func RedisOpt() (asynq.RedisClientOpt, error) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		return asynq.RedisClientOpt{}, fmt.Errorf("REDIS_ADDR must be set")
	}
	return asynq.RedisClientOpt{Addr: addr}, nil
}
