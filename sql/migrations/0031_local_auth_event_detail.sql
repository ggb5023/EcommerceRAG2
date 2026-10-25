-- Local auth events carry a small structured detail (for example the source of a
-- failed password change). Append-only migration after 0030.
BEGIN;
SELECT pg_advisory_xact_lock(742018);

ALTER TABLE local_auth_event ADD COLUMN detail JSONB NOT NULL DEFAULT '{}'::jsonb;

INSERT INTO schema_migration(version,checksum_sha256) VALUES ('0031', :'migration_checksum');
COMMIT;
