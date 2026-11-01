-- B6/02: key the decision ledger by execution instead of turn.
-- A resumed turn has several executions (ASKING, then DONE/FAILED); the old
-- turn-keyed primary key let the ASKING row block the terminal one.
BEGIN;

SELECT pg_advisory_xact_lock(742018);

ALTER TABLE turn_decision ADD COLUMN execution_id bigint;

-- Backfill by matching the recorded state first: a resumed turn keeps an
-- ASKING execution and a later DONE execution, and the old ledger could only
-- keep the first row. Rows with no state (pre-0041) fall back to the latest
-- execution. Anything still unmatched keeps NULL and is excluded by queries.
UPDATE turn_decision td SET execution_id = (
    SELECT e.id FROM conversation_execution e
    WHERE e.tenant_id=td.tenant_id AND e.turn_id=td.turn_id::bigint AND e.status=td.state
    ORDER BY e.execution_no DESC LIMIT 1)
WHERE td.execution_id IS NULL AND td.state IS NOT NULL AND td.turn_id ~ '^[0-9]+$';

UPDATE turn_decision td SET execution_id = (
    SELECT e.id FROM conversation_execution e
    WHERE e.tenant_id=td.tenant_id AND e.turn_id=td.turn_id::bigint
    ORDER BY e.execution_no DESC LIMIT 1)
WHERE td.execution_id IS NULL AND td.turn_id ~ '^[0-9]+$';

-- A primary key cannot contain NULL, so the execution key is a unique index:
-- unmapped historical rows stay visible for forensics and never collide.
ALTER TABLE turn_decision DROP CONSTRAINT turn_decision_pkey;
CREATE UNIQUE INDEX turn_decision_execution_uidx ON turn_decision(tenant_id, execution_id);
CREATE INDEX turn_decision_turn_idx ON turn_decision(tenant_id, turn_id);
ALTER TABLE turn_decision ADD CONSTRAINT turn_decision_execution_fk
    FOREIGN KEY (tenant_id, execution_id) REFERENCES conversation_execution(tenant_id, id);

COMMENT ON COLUMN turn_decision.execution_id IS 'conversation_execution.id; NULL only for pre-0042 rows that could not be backfilled';
COMMENT ON COLUMN turn_decision.state IS 'ASKING / DONE / FAILED / CANCELLED; NULL for rows written before 0041';

INSERT INTO schema_migration(version,checksum_sha256) VALUES ('0042', :'migration_checksum');
COMMIT;
