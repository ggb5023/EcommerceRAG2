-- 0029/B6 follow-up: the gateway writes the decision row, the admin side reads it.
-- Migration 0039 created the table without the runtime grants, so the insert was
-- rejected with "permission denied for table turn_decision".
BEGIN;

SELECT pg_advisory_xact_lock(742018);

GRANT INSERT ON turn_decision TO rag_v2_gateway;
GRANT SELECT ON turn_decision TO rag_v2_gateway;
GRANT SELECT ON turn_decision TO rag_v2_ai;

INSERT INTO schema_migration(version,checksum_sha256) VALUES ('0040', :'migration_checksum');
COMMIT;
