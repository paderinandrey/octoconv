-- Add a supporting index for the reconciler's webhook-gap sweep (RECON-04).
--
-- FindWebhookGaps' NOT EXISTS subquery must prove "no row exists for this
-- job_id at all" (delivered, undelivered, AND dead-lettered rows all count
-- as "not a gap", per D-05) — the existing webhook_deliveries_pending_idx
-- is a PARTIAL index (WHERE delivered = false) and cannot serve a query
-- that must also see delivered=true/dead-lettered rows.
CREATE INDEX webhook_deliveries_job_id_idx
    ON webhook_deliveries (job_id);
