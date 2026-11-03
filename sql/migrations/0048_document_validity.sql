-- M7 stage 2: version-level effective dates; chunk keeps the materialized copy.
BEGIN;

SELECT pg_advisory_xact_lock(742018);

ALTER TABLE document_version ADD COLUMN effective_from DATE;
ALTER TABLE document_version ADD COLUMN effective_to DATE;
ALTER TABLE document_version ADD CONSTRAINT document_version_effective_range_check
    CHECK (effective_to IS NULL OR effective_from IS NULL OR effective_to >= effective_from);

INSERT INTO schema_migration(version,checksum_sha256) VALUES ('0048', :'migration_checksum');
COMMIT;
