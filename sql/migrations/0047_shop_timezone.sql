-- M7 stage 2: the shop timezone is the source of the merchant business date.
BEGIN;

SELECT pg_advisory_xact_lock(742018);

ALTER TABLE shop ADD COLUMN timezone TEXT NOT NULL DEFAULT 'Asia/Shanghai';

INSERT INTO schema_migration(version,checksum_sha256) VALUES ('0047', :'migration_checksum');
COMMIT;
