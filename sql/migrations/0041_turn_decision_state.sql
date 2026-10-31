-- B6: without the terminal state the recorded row cannot answer "why did this turn
-- stop"; existing rows keep NULL to mark them as pre-migration records.
BEGIN;

SELECT pg_advisory_xact_lock(742018);

ALTER TABLE turn_decision ADD COLUMN state text;

COMMENT ON COLUMN turn_decision.state IS 'ASKING / DONE / FAILED; NULL for rows written before 0041';

INSERT INTO schema_migration(version,checksum_sha256) VALUES ('0041', :'migration_checksum');
COMMIT;
