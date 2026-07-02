-- Add hashed API-key storage to clients.
--
-- Two independent key slots (primary/secondary), each with its OWN
-- revoked-at timestamp, so revoking one slot does NOT disable the other.
-- This is what makes zero-downtime key rotation actually work: an operator
-- adds a new secondary key, rolls callers over to it, then revokes the old
-- primary — at no point are both keys invalid at once. A single shared
-- revoked_at column would kill both keys simultaneously and defeat rotation.
ALTER TABLE clients
    ADD COLUMN api_key_hash            text UNIQUE,
    ADD COLUMN api_key_hash_secondary  text UNIQUE,
    ADD COLUMN primary_revoked_at      timestamptz,
    ADD COLUMN secondary_revoked_at    timestamptz,
    ADD COLUMN updated_at              timestamptz NOT NULL DEFAULT now();

-- Partial indexes for the hot per-request auth lookup: only actively-valid
-- (non-revoked) key digests need to be indexed. Uniqueness is already
-- enforced by the column-level UNIQUE constraints above; these are plain
-- partial indexes, mirroring the jobs_inflight_idx partial-index convention
-- in 0001_init.sql.
CREATE INDEX clients_api_key_hash_idx
    ON clients (api_key_hash)
    WHERE primary_revoked_at IS NULL;

CREATE INDEX clients_api_key_hash_secondary_idx
    ON clients (api_key_hash_secondary)
    WHERE secondary_revoked_at IS NULL;

-- Reuses the set_updated_at() function already defined in 0001_init.sql.
CREATE TRIGGER clients_set_updated
    BEFORE UPDATE ON clients
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
