-- The ledger now overwrites the same execution row with ON CONFLICT DO UPDATE.
-- 0040 granted only INSERT/SELECT; PostgreSQL requires UPDATE for the conflict
-- action even when no conflict occurs.
BEGIN;

SELECT pg_advisory_xact_lock(742018);

GRANT UPDATE ON turn_decision TO rag_v2_gateway;

INSERT INTO schema_migration(version,checksum_sha256) VALUES ('0043', :'migration_checksum');
COMMIT;
