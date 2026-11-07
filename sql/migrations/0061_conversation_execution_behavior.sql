-- 0041 follow-up: the staff reply behavior was inferred from the terminal
-- state, so a DONE execution that carried a CLARIFY or ANSWER decision could
-- not be rendered faithfully (CLARIFY appeared as ANSWER). Persist the
-- Go-owned behavior; legacy rows stay NULL and the admin read falls back to
-- state inference only for those rows. Append-only: 0058-0060 are unchanged.
BEGIN;

SELECT pg_advisory_xact_lock(742018);

ALTER TABLE conversation_execution ADD COLUMN behavior TEXT;

INSERT INTO schema_migration(version,checksum_sha256) VALUES ('0061', :'migration_checksum');
COMMIT;
