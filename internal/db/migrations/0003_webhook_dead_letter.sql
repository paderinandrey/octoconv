-- Add dead-letter tracking to webhook_deliveries (D-10).
--
-- Set true on the row for the final delivery attempt once asynq exhausts
-- MaxRetry (~30 min backoff window, see internal/queue/queue.go). Operators
-- investigate dead-lettered rows via direct SQL in v1 — no CLI/API tooling
-- yet (see WEBHOOK-V2-02 in REQUIREMENTS.md for the planned v2 replay tool).
ALTER TABLE webhook_deliveries
    ADD COLUMN dead_letter boolean NOT NULL DEFAULT false;

CREATE INDEX webhook_deliveries_dead_letter_idx
    ON webhook_deliveries (job_id) WHERE dead_letter = true;
