-- Bind each resume idempotency key to the exact execution it created.
-- This keeps retries stable after the parent turn leaves ASKING and also
-- prevents a later execution on the same turn from changing an old replay.
BEGIN;
SELECT pg_advisory_xact_lock(742018);

ALTER TABLE turn_idempotency
    ADD COLUMN execution_id BIGINT;

ALTER TABLE turn_idempotency
    ADD CONSTRAINT turn_idempotency_execution_fk
    FOREIGN KEY (tenant_id, execution_id)
    REFERENCES conversation_execution (tenant_id, id);

CREATE INDEX idx_turn_idempotency_execution
    ON turn_idempotency (tenant_id, execution_id);

INSERT INTO schema_migration(version, checksum_sha256)
VALUES ('0016', :'migration_checksum');
COMMIT;
