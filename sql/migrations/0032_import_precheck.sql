-- L1 import precheck: staged batches, agent rules evidence, append-only tenant switch.
-- New tables only; existing document_version/reviewed_* identity is unchanged.
BEGIN;
SELECT pg_advisory_xact_lock(742018);

CREATE TABLE import_batch (
    id                  BIGSERIAL PRIMARY KEY,
    tenant_id           BIGINT NOT NULL REFERENCES tenant(id),
    shop_id             TEXT   NOT NULL,
    created_by          BIGINT NOT NULL REFERENCES app_user(id),
    status              TEXT   NOT NULL DEFAULT 'staged'
                        CHECK (status IN ('staged','confirmed','revoked','expired')),
    business_date       DATE   NOT NULL,
    expires_at          TIMESTAMPTZ NOT NULL DEFAULT (now() + interval '24 hours'),
    agent_rules_version TEXT   NOT NULL,
    agent_model_id      TEXT   NOT NULL,
    manifest_yaml       TEXT   NOT NULL,
    manifest_sha256     TEXT   NOT NULL CHECK (manifest_sha256 ~ '^[0-9a-f]{64}$'),
    ingest_job_id       BIGINT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at          TIMESTAMPTZ,
    CHECK ((status = 'revoked') = (revoked_at IS NOT NULL))
);
CREATE INDEX idx_import_batch_scope ON import_batch (tenant_id, shop_id, created_at DESC);
CREATE INDEX idx_import_batch_expiry ON import_batch (status, expires_at);

CREATE TABLE import_item (
    id                          BIGSERIAL PRIMARY KEY,
    batch_id                    BIGINT NOT NULL REFERENCES import_batch(id) ON DELETE CASCADE,
    source_file_id              BIGINT REFERENCES source_file(id),
    document_id                 BIGINT REFERENCES document(id),
    source_hash                 TEXT NOT NULL CHECK (source_hash ~ '^[0-9a-f]{64}$'),
    temp_path                   TEXT NOT NULL,
    suggested_disclosure_class  TEXT CHECK (suggested_disclosure_class IN
                                  ('external_allowed','internal_only','unclassified')),
    suggested_effective_from    DATE,
    precheck_result             JSONB NOT NULL DEFAULT '[]',
    precheck_detail             JSONB NOT NULL DEFAULT '{}',
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_import_item_batch ON import_item (batch_id, id);
CREATE INDEX idx_import_item_hash ON import_item (source_hash);

CREATE TABLE tenant_agent_review (
    id             BIGSERIAL PRIMARY KEY,
    tenant_id      BIGINT NOT NULL REFERENCES tenant(id),
    enabled        BOOLEAN NOT NULL,
    policy_version TEXT NOT NULL,
    actor_user_id  BIGINT NOT NULL REFERENCES app_user(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_tenant_agent_review_latest
    ON tenant_agent_review (tenant_id, created_at DESC, id DESC);

CREATE FUNCTION ecr_tenant_agent_review_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'tenant_agent_review is append-only' USING ERRCODE='55000';
END $$;
CREATE TRIGGER trg_tenant_agent_review_append_only
    BEFORE UPDATE OR DELETE ON tenant_agent_review
    FOR EACH ROW EXECUTE FUNCTION ecr_tenant_agent_review_append_only();

GRANT SELECT,INSERT,UPDATE,DELETE ON import_batch,import_item TO rag_v2_gateway;
GRANT SELECT,INSERT ON tenant_agent_review TO rag_v2_gateway;
GRANT USAGE,SELECT ON SEQUENCE import_batch_id_seq,import_item_id_seq TO rag_v2_gateway;
GRANT USAGE,SELECT ON SEQUENCE tenant_agent_review_id_seq TO rag_v2_gateway;

INSERT INTO schema_migration(version,checksum_sha256) VALUES ('0032', :'migration_checksum');
COMMIT;
