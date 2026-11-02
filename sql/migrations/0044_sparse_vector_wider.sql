-- 0029 c revision (WP13): the real provider's sparse dictionary can exceed
-- 65536 entries. Bailian qwen3.7-text-embedding measured max token_id=113496,
-- so widen to 2^17 and leave ~15% headroom. 0037 stays immutable history.
-- The USING expression rewrites the pgvector literal suffix and preserves every
-- token id and weight; NULL stays NULL.
BEGIN;

ALTER TABLE chunk ALTER COLUMN sparse_embedding TYPE sparsevec(131072)
    USING (regexp_replace(sparse_embedding::text, '/65536$', '/131072')::sparsevec(131072));

COMMENT ON COLUMN chunk.sparse_embedding IS
    'sparsevec(131072); token_id < 131072, nnz <= 2048 enforced at write time (decisions/0029, WP13)';

INSERT INTO schema_migration(version,checksum_sha256) VALUES ('0044', :'migration_checksum');
COMMIT;
