-- AI-owned diagnostics for customer executions; staff turn traces stay unchanged.
BEGIN;
SELECT pg_advisory_xact_lock(742018);
ALTER TABLE customer_execution ADD CONSTRAINT customer_execution_tenant_identity UNIQUE (tenant_id,id);
CREATE TABLE customer_retrieval_trace (
    id BIGSERIAL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    execution_id TEXT NOT NULL,
    stage TEXT NOT NULL CHECK (stage IN ('v2_candidates','v2_rerank')),
    candidates_json JSONB NOT NULL CHECK (jsonb_typeof(candidates_json)='array'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id,execution_id) REFERENCES customer_execution(tenant_id,id)
);
CREATE INDEX customer_retrieval_trace_execution ON customer_retrieval_trace(tenant_id,execution_id,id);
REVOKE ALL ON customer_retrieval_trace FROM PUBLIC,rag_v2_gateway,rag_v2_ingest;
GRANT SELECT,INSERT ON customer_retrieval_trace TO rag_v2_ai;
GRANT USAGE,SELECT ON SEQUENCE customer_retrieval_trace_id_seq TO rag_v2_ai;
INSERT INTO schema_migration(version,checksum_sha256) VALUES('0027', :'migration_checksum');
COMMIT;
