-- Preserve AI ownership and old readers; only extend the allowed trace stages.
BEGIN;
SELECT pg_advisory_xact_lock(742018);
ALTER TABLE customer_retrieval_trace DROP CONSTRAINT customer_retrieval_trace_stage_check;
ALTER TABLE customer_retrieval_trace ADD CONSTRAINT customer_retrieval_trace_stage_check
    CHECK (stage IN ('v2_candidates','v2_rerank','v2_generation_input'));
INSERT INTO schema_migration(version,checksum_sha256) VALUES('0028', :'migration_checksum');
COMMIT;
