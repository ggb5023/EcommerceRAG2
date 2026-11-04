-- Optional turn attribution for usage rows. Historical rows stay NULL; no backfill.
BEGIN;
SELECT pg_advisory_xact_lock(742018);
ALTER TABLE usage_log ADD COLUMN request_id TEXT;
ALTER TABLE usage_log ADD COLUMN execution_id BIGINT;
ALTER TABLE usage_log ADD COLUMN turn_id BIGINT;
CREATE INDEX idx_usage_log_request_id ON usage_log (request_id) WHERE request_id IS NOT NULL;
INSERT INTO schema_migration(version,checksum_sha256) VALUES ('0050', :'migration_checksum');
COMMIT;
