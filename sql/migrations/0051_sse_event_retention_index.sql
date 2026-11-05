-- Supports tenant-scoped retention deletes without scanning the whole event log.
BEGIN;
SELECT pg_advisory_xact_lock(742018);
CREATE INDEX idx_sse_event_tenant_created ON sse_event (tenant_id, created_at);
INSERT INTO schema_migration(version,checksum_sha256) VALUES ('0051', :'migration_checksum');
COMMIT;
