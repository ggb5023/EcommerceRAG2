-- Token usage only; no currency, balance or price is stored.
BEGIN;
SELECT pg_advisory_xact_lock(742018);
CREATE TABLE usage_log (
    id BIGSERIAL PRIMARY KEY,
    tenant_id BIGINT,
    provider_id BIGINT,
    slot TEXT NOT NULL CHECK (slot IN ('control','embedding','rerank','generation')),
    model TEXT NOT NULL,
    input_tokens INTEGER CHECK (input_tokens IS NULL OR input_tokens >= 0),
    output_tokens INTEGER CHECK (output_tokens IS NULL OR output_tokens >= 0),
    http_status INTEGER,
    error_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_usage_log_tenant_time ON usage_log (tenant_id, created_at DESC);
GRANT INSERT ON usage_log TO rag_v2_ai;
GRANT INSERT ON usage_log TO rag_v2_ingest;
GRANT SELECT ON usage_log TO rag_v2_gateway;
GRANT USAGE,SELECT ON SEQUENCE usage_log_id_seq TO rag_v2_ai,rag_v2_ingest;
INSERT INTO schema_migration(version,checksum_sha256) VALUES ('0035', :'migration_checksum');
COMMIT;
