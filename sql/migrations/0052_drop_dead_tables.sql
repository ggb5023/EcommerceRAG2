-- Drop the five zero-row, zero-reference tables identified by WP53.
-- provider/provider_model are deliberately retained as P3 reservations.
BEGIN;
SELECT pg_advisory_xact_lock(742018);
DROP TABLE IF EXISTS ingest_package_file;
DROP TABLE IF EXISTS ingest_package_upload;
DROP TABLE IF EXISTS eval_run;
DROP TABLE IF EXISTS eval_case;
DROP TABLE IF EXISTS ingest_stage;

INSERT INTO schema_migration(version,checksum_sha256) VALUES ('0052', :'migration_checksum');
COMMIT;
