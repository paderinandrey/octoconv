-- Add 'html' to the jobs.engine allow-list (HTML-01/D-08).
--
-- Hard prerequisite for the third (chromium) engine class: no engine="html"
-- job row can be created until this constraint accepts the value. The
-- constraint name jobs_engine_check is Postgres's auto-generated name for
-- the inline, unnamed column CHECK declared in 0001_init.sql (the standard
-- <table>_<column>_check convention for an unnamed CHECK) -- a live \d jobs
-- confirmation happens during Plan 05 acceptance; if the live name differs,
-- this migration's DROP is corrected then.
ALTER TABLE jobs DROP CONSTRAINT jobs_engine_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_engine_check
    CHECK (engine IN ('image', 'document', 'av', 'cad', 'archive', 'probe', 'html'));
