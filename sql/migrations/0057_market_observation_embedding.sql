-- 0032 stage 3: dense embedding for the shared intelligence observations.
-- The column is nullable: an embedding failure must never block a crawl, and
-- rows without a vector are skipped by semantic search. A pgvector HNSW index
-- is intentionally deferred until real row counts justify it.
BEGIN;

SELECT pg_advisory_xact_lock(742018);

ALTER TABLE market_observation ADD COLUMN embedding vector(1024);

-- The intel worker remains the only writer. This explicit re-grant documents
-- that the new column belongs to the same role boundary (table-level grants
-- already cover new columns).
GRANT SELECT,INSERT,UPDATE ON market_observation TO rag_v2_intel;

INSERT INTO schema_migration(version,checksum_sha256) VALUES ('0057', :'migration_checksum');
COMMIT;
