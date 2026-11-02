-- WP21 suggestion retention deletes old hidden turns and their dependent rows.
-- turn_decision is written by the admin store and retrieval_trace by the AI
-- retrieval path; deleting a pruned turn needs DELETE on both child tables.
BEGIN;

SELECT pg_advisory_xact_lock(742018);

GRANT DELETE ON turn_decision TO rag_v2_gateway;
GRANT DELETE ON retrieval_trace TO rag_v2_gateway;

INSERT INTO schema_migration(version,checksum_sha256) VALUES ('0046', :'migration_checksum');
COMMIT;
