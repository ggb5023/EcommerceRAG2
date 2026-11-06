-- The intel process verifies the full migration checksum set before serving.
-- schema_migration is read-only metadata, not a business table; granting
-- SELECT to rag_v2_intel is required for that fail-closed startup check.
BEGIN;

SELECT pg_advisory_xact_lock(742018);

GRANT SELECT ON schema_migration TO rag_v2_intel;

INSERT INTO schema_migration(version,checksum_sha256) VALUES ('0056', :'migration_checksum');
COMMIT;
