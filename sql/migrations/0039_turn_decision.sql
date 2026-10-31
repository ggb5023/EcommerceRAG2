-- B6: one row per turn explaining why these evidence items were chosen or rejected.
-- chosen/rejected are NOT NULL with an empty-array default: "nothing was rejected"
-- and "nothing was recorded" must stay distinguishable.
BEGIN;

SELECT pg_advisory_xact_lock(742018);

CREATE TABLE turn_decision(
    tenant_id bigint NOT NULL,
    conversation_id text NOT NULL,
    turn_id text NOT NULL,
    chosen jsonb NOT NULL DEFAULT '[]'::jsonb,
    rejected jsonb NOT NULL DEFAULT '[]'::jsonb,
    strength text,
    failure_code text,
    confidence numeric,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, conversation_id, turn_id)
);

CREATE INDEX turn_decision_tenant_created_idx ON turn_decision(tenant_id, created_at DESC);

INSERT INTO schema_migration(version,checksum_sha256) VALUES ('0039', :'migration_checksum');
COMMIT;
