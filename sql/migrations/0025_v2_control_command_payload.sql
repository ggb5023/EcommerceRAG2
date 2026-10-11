BEGIN;
SELECT pg_advisory_xact_lock(742018);
ALTER TABLE control_command ADD COLUMN request_bytes BYTEA NOT NULL;
INSERT INTO schema_migration(version,checksum_sha256)
VALUES ('0025', :'migration_checksum') ON CONFLICT (version) DO NOTHING;
COMMIT;
