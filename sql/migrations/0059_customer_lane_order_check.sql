-- 0058's lane check used a regex on a nullable column. PostgreSQL treats a
-- NULL check result as satisfied, so after_sales + NULL order slipped through.
-- Recreate the constraint with an explicit IS NOT NULL. This is a new migration;
-- 0058 is already applied and is not modified.
BEGIN;

SELECT pg_advisory_xact_lock(742018);

ALTER TABLE customer_conversation
    DROP CONSTRAINT IF EXISTS customer_conversation_order_check;
ALTER TABLE customer_conversation
    ADD CONSTRAINT customer_conversation_order_check CHECK (
        (service_type='pre_sales' AND order_id IS NULL)
        OR (service_type='after_sales'
            AND order_id IS NOT NULL
            AND order_id ~ '^[A-Z0-9][A-Z0-9_-]{3,39}$')
    );

INSERT INTO schema_migration(version,checksum_sha256) VALUES ('0059', :'migration_checksum');
COMMIT;
