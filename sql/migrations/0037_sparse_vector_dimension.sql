-- 0029 step 2.1: pin the sparse vector dimension and record that the dictionary is
-- fixed at 65536 entries. The nnz limit (2048) is enforced on the write path
-- because a CHECK cannot count sparsevec non-zeros.
BEGIN;

ALTER TABLE chunk ALTER COLUMN sparse_embedding TYPE sparsevec(65536);

COMMENT ON COLUMN chunk.sparse_embedding IS
    'sparsevec(65536); token_id < 65536, nnz <= 2048 enforced at write time (decisions/0029)';

INSERT INTO schema_migration(version,checksum_sha256) VALUES ('0037', :'migration_checksum');
COMMIT;
