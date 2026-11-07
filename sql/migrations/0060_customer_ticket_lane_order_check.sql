-- 0058's customer_ticket lane check used a regex on a nullable column. A NULL
-- check result is treated as satisfied, so after_sales + NULL order slipped
-- through. 0060 rebuilds the constraint with an explicit IS NOT NULL and a
-- format check. 0058 is already applied and must not be modified.
BEGIN;

SELECT pg_advisory_xact_lock(742018);

DO $$
DECLARE bad BIGINT;
BEGIN
    SELECT count(*) INTO bad
    FROM customer_ticket
    WHERE NOT (
        (service_type = 'pre_sales' AND order_id IS NULL)
        OR (service_type = 'after_sales'
            AND order_id IS NOT NULL
            AND order_id ~ '^[A-Z0-9][A-Z0-9_-]{3,39}$')
    );
    IF bad > 0 THEN
        RAISE EXCEPTION 'customer_ticket has % row(s) violating the lane/order check; fix them before 0060', bad;
    END IF;
END $$;

ALTER TABLE customer_ticket
    ADD CONSTRAINT customer_ticket_lane_order_check CHECK (
        (service_type = 'pre_sales' AND order_id IS NULL)
        OR (service_type = 'after_sales'
            AND order_id IS NOT NULL
            AND order_id ~ '^[A-Z0-9][A-Z0-9_-]{3,39}$')
    );

-- 0058 auto-named its fourth unnamed CHECK customer_ticket_check3.
ALTER TABLE customer_ticket DROP CONSTRAINT IF EXISTS customer_ticket_check3;

INSERT INTO schema_migration(version,checksum_sha256) VALUES ('0060', :'migration_checksum');
COMMIT;
