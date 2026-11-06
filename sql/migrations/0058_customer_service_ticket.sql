-- 0041: pre-sales vs after-sales lanes and the after-sales ticket.
-- Terminal states and the ticket lifecycle stay Go-owned; Python never writes
-- these tables. Facts snapshots are stored on both execution paths so the
-- admin review page can show what an answer was grounded on.
BEGIN;

SELECT pg_advisory_xact_lock(742018);

-- Existing conversations are pre-sales and carry no order. The CHECK keeps the
-- two lanes disjoint: an after-sales conversation always has an order, a
-- pre-sales conversation never does.
ALTER TABLE customer_conversation
    ADD COLUMN service_type TEXT NOT NULL DEFAULT 'pre_sales',
    ADD COLUMN order_id TEXT,
    ADD CONSTRAINT customer_conversation_service_type_check
        CHECK (service_type IN ('pre_sales','after_sales')),
    ADD CONSTRAINT customer_conversation_order_check CHECK (
        (service_type='pre_sales' AND order_id IS NULL)
        OR (service_type='after_sales'
            AND order_id ~ '^[A-Z0-9][A-Z0-9_-]{3,39}$')
    );
CREATE INDEX customer_conversation_order_idx
    ON customer_conversation (tenant_id, shop_id, order_id)
    WHERE order_id IS NOT NULL;

-- Facts are snapshots: values can change between turns, but a stored answer
-- keeps the observation it was built from (`observed_at`, `is_mock`).
ALTER TABLE customer_execution
    ADD COLUMN facts_json JSONB NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE conversation_execution
    ADD COLUMN facts_json JSONB NOT NULL DEFAULT '[]'::jsonb;

-- One ticket per after-sales conversation while it is active. A resolved or
-- closed ticket stays for history; a later handoff opens a new active ticket.
CREATE TABLE customer_ticket (
    id TEXT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    shop_id TEXT NOT NULL,
    conversation_id TEXT NOT NULL,
    customer_id TEXT NOT NULL,
    service_type TEXT NOT NULL DEFAULT 'after_sales'
        CHECK (service_type IN ('pre_sales','after_sales')),
    order_id TEXT,
    status TEXT NOT NULL DEFAULT 'OPEN'
        CHECK (status IN ('OPEN','ASSIGNED','RESOLVED','CLOSED')),
    reason_code TEXT NOT NULL,
    assignee_user_id BIGINT REFERENCES app_user(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ,
    closed_at TIMESTAMPTZ,
    UNIQUE (tenant_id, id),
    FOREIGN KEY (tenant_id, shop_id, customer_id)
        REFERENCES customer_identity (tenant_id, shop_id, id),
    FOREIGN KEY (tenant_id, conversation_id)
        REFERENCES customer_conversation (tenant_id, id),
    CHECK (status <> 'ASSIGNED' OR assignee_user_id IS NOT NULL),
    CHECK (status <> 'RESOLVED' OR resolved_at IS NOT NULL),
    CHECK (status <> 'CLOSED' OR closed_at IS NOT NULL),
    CHECK (
        (service_type='pre_sales' AND order_id IS NULL)
        OR (service_type='after_sales' AND order_id IS NOT NULL)
    )
);

CREATE UNIQUE INDEX customer_ticket_active_idx
    ON customer_ticket (tenant_id, conversation_id)
    WHERE status IN ('OPEN','ASSIGNED');
CREATE INDEX customer_ticket_queue_idx
    ON customer_ticket (tenant_id, shop_id, status, created_at);
CREATE INDEX customer_ticket_order_idx
    ON customer_ticket (tenant_id, shop_id, order_id)
    WHERE order_id IS NOT NULL;

-- Only the gateway role may touch tickets; the AI and ingest identities are
-- explicitly kept out in case a future GRANT ALL is added.
REVOKE ALL ON customer_ticket FROM PUBLIC, rag_v2_ai, rag_v2_ingest;
GRANT SELECT,INSERT,UPDATE,DELETE ON customer_ticket TO rag_v2_gateway;

INSERT INTO schema_migration(version,checksum_sha256) VALUES ('0058', :'migration_checksum');
COMMIT;
