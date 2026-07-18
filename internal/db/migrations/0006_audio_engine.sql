-- Add 'audio' to the jobs.engine allow-list (AUD-05).
--
-- Hard prerequisite for the fourth (audio/whisper) engine class: no
-- engine="audio" job row can be created until this constraint accepts the
-- value. The constraint name jobs_engine_check is Postgres's auto-generated
-- name for the inline, unnamed column CHECK declared in 0001_init.sql (the
-- standard <table>_<column>_check convention for an unnamed CHECK), already
-- confirmed live by the 0005_html_engine.sql migration this one mirrors.
ALTER TABLE jobs DROP CONSTRAINT jobs_engine_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_engine_check
    CHECK (engine IN ('image', 'document', 'av', 'cad', 'archive', 'probe', 'html', 'audio'));
