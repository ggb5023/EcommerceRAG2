-- Tenant-scoped model providers: encrypted credentials and per-slot prices.
-- Existing global provider configuration and budget window are unchanged.
BEGIN;
SELECT pg_advisory_xact_lock(742018);

CREATE TABLE provider (
    id                 BIGSERIAL PRIMARY KEY,
    tenant_id          BIGINT NOT NULL REFERENCES tenant(id),
    name               TEXT NOT NULL,
    kind               TEXT NOT NULL CHECK (kind IN ('openai_compatible','bailian','mock')),
    base_url           TEXT NOT NULL,
    api_key_ciphertext TEXT NOT NULL DEFAULT '',
    key_last4          TEXT NOT NULL DEFAULT '',
    key_version        INTEGER NOT NULL DEFAULT 1 CHECK (key_version >= 1),
    enabled            BOOLEAN NOT NULL DEFAULT false,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, name)
);

CREATE TABLE provider_model (
    id            BIGSERIAL PRIMARY KEY,
    provider_id   BIGINT NOT NULL REFERENCES provider(id) ON DELETE CASCADE,
    slot          TEXT NOT NULL CHECK (slot IN ('control','embedding','rerank','generation')),
    model_id      TEXT NOT NULL,
    embedding_dim INTEGER CHECK (embedding_dim IS NULL OR embedding_dim > 0),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider_id, slot)
);

CREATE INDEX idx_provider_tenant ON provider (tenant_id, enabled);
CREATE INDEX idx_provider_model_provider ON provider_model (provider_id, slot);

GRANT SELECT,INSERT,UPDATE,DELETE ON provider,provider_model TO rag_v2_gateway;
GRANT USAGE,SELECT ON SEQUENCE provider_id_seq,provider_model_id_seq TO rag_v2_gateway;

INSERT INTO schema_migration(version,checksum_sha256) VALUES ('0034', :'migration_checksum');
COMMIT;
