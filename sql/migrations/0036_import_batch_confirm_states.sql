-- Two-phase import confirmation: claim the batch, then queue, then settle.
-- A failed queue releases the claim back to 'staged'; Submit carries an
-- idempotency key, so a retry cannot create a second job (refactor-input #14).
BEGIN;
SELECT pg_advisory_xact_lock(742018);

ALTER TABLE import_batch DROP CONSTRAINT import_batch_status_check;
ALTER TABLE import_batch ADD CONSTRAINT import_batch_status_check
    CHECK (status IN ('staged','confirming','confirmed','revoked','expired'));

ALTER TABLE import_batch ADD COLUMN confirm_started_at TIMESTAMPTZ;
ALTER TABLE import_batch ADD CONSTRAINT import_batch_confirm_started_check
    CHECK ((status = 'confirming') = (confirm_started_at IS NOT NULL));

INSERT INTO schema_migration(version,checksum_sha256) VALUES ('0036', :'migration_checksum');
COMMIT;
