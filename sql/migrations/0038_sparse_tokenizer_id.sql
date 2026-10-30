-- 0029 step 2: the sparse payload comes from a different model than the dense
-- vectors, so it records its own producer instead of overwriting tokenizer_id.
BEGIN;

SELECT pg_advisory_xact_lock(742018);

ALTER TABLE chunk ADD COLUMN sparse_tokenizer_id text;

COMMENT ON COLUMN chunk.sparse_tokenizer_id IS
    '<provider>:<model>:sparse for the vector in sparse_embedding (decisions/0029)';

INSERT INTO schema_migration(version,checksum_sha256) VALUES ('0038', :'migration_checksum');
COMMIT;
