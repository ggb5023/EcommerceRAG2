-- v2 persistent retrieval indexes. The Python AI plane reads these tables;
-- version publication and all business writes remain control-plane-owned.
BEGIN;
SELECT pg_advisory_xact_lock(742018);

CREATE INDEX IF NOT EXISTS idx_v2_document_active_scope
    ON document (tenant_id, logical_key, active_version_id);
CREATE INDEX IF NOT EXISTS idx_v2_chunk_scope_version
    ON chunk (tenant_id, shop_id, version_id, chunk_index);
CREATE INDEX IF NOT EXISTS idx_v2_chunk_embed_text_trgm
    ON chunk USING gin (embed_text gin_trgm_ops);

INSERT INTO schema_migration(version, checksum_sha256)
VALUES ('0018', :'migration_checksum') ON CONFLICT (version) DO NOTHING;
COMMIT;
