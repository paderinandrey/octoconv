CREATE EXTENSION IF NOT EXISTS pgcrypto;  -- gen_random_uuid()

CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TABLE clients (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE presets (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name          text NOT NULL,
    version       int  NOT NULL DEFAULT 1,
    scope         text NOT NULL CHECK (scope IN ('system', 'user')),
    client_id     uuid REFERENCES clients (id) ON DELETE CASCADE,
    operation     text NOT NULL
                  CHECK (operation IN ('convert', 'extract', 'archive', 'inspect', 'render')),
    target_format text,
    options       jsonb NOT NULL DEFAULT '{}'::jsonb,
    description   text,
    is_active     boolean NOT NULL DEFAULT true,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT presets_scope_owner_chk CHECK (
        (scope = 'system' AND client_id IS NULL) OR
        (scope = 'user'   AND client_id IS NOT NULL)
    )
);
CREATE UNIQUE INDEX presets_system_uq ON presets (name, version) WHERE scope = 'system';
CREATE UNIQUE INDEX presets_user_uq   ON presets (client_id, name, version) WHERE scope = 'user';
CREATE TRIGGER presets_set_updated
    BEFORE UPDATE ON presets
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE jobs (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    client_id      uuid REFERENCES clients (id) ON DELETE SET NULL,
    parent_id      uuid REFERENCES jobs (id) ON DELETE CASCADE,
    operation      text NOT NULL
                   CHECK (operation IN ('convert', 'extract', 'archive', 'inspect', 'render')),
    engine         text NOT NULL
                   CHECK (engine IN ('image', 'document', 'av', 'cad', 'archive', 'probe')),
    status         text NOT NULL DEFAULT 'queued'
                   CHECK (status IN ('awaiting_upload', 'queued', 'active', 'done', 'failed', 'canceled')),
    source_format  text,
    target_format  text,
    preset_name    text,
    preset_version int,
    options        jsonb NOT NULL DEFAULT '{}'::jsonb,
    priority       int NOT NULL DEFAULT 0,
    callback_url   text,
    attempts       int NOT NULL DEFAULT 0,
    error_code     text,
    error_message  text,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    started_at     timestamptz,
    finished_at    timestamptz,
    expires_at     timestamptz
);
CREATE INDEX jobs_status_idx         ON jobs (status);
CREATE INDEX jobs_engine_status_idx  ON jobs (engine, status);
CREATE INDEX jobs_client_created_idx ON jobs (client_id, created_at DESC);
CREATE INDEX jobs_parent_idx         ON jobs (parent_id) WHERE parent_id IS NOT NULL;
CREATE INDEX jobs_inflight_idx       ON jobs (created_at) WHERE status IN ('queued', 'active');
CREATE TRIGGER jobs_set_updated
    BEFORE UPDATE ON jobs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE job_inputs (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id       uuid NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    ordinal      int NOT NULL DEFAULT 0,
    object_key   text NOT NULL,
    filename     text,
    format       text,
    size_bytes   bigint,
    content_type text,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX job_inputs_job_idx ON job_inputs (job_id, ordinal);

CREATE TABLE job_outputs (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id       uuid NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    ordinal      int NOT NULL DEFAULT 0,
    object_key   text,
    filename     text,
    format       text,
    size_bytes   bigint,
    content_type text,
    metadata     jsonb,
    expires_at   timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX job_outputs_job_idx ON job_outputs (job_id, ordinal);

CREATE TABLE job_events (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    job_id      uuid NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    from_status text,
    to_status   text NOT NULL,
    detail      jsonb,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX job_events_job_idx ON job_events (job_id, created_at);

CREATE TABLE webhook_deliveries (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id      uuid NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    url         text NOT NULL,
    attempt     int NOT NULL DEFAULT 0,
    status_code int,
    delivered   boolean NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX webhook_deliveries_pending_idx ON webhook_deliveries (job_id) WHERE delivered = false;
CREATE TRIGGER webhook_deliveries_set_updated
    BEFORE UPDATE ON webhook_deliveries
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
