-- L2 review suggestions: agent output is evidence only; reviewed_* stays NULL
-- until the merchant's explicit review action.
BEGIN;
SELECT pg_advisory_xact_lock(742018);

CREATE TABLE review_suggestion (
    id                       BIGSERIAL PRIMARY KEY,
    tenant_id                BIGINT NOT NULL REFERENCES tenant(id),
    version_id               BIGINT NOT NULL,
    content_hash             TEXT NOT NULL CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    business_date            DATE NOT NULL,
    agent_rules_version      TEXT NOT NULL,
    decision                 TEXT NOT NULL CHECK (decision IN ('approved','needs_revision')),
    reasons                  JSONB NOT NULL DEFAULT '[]',
    evidence_chunk_ids       TEXT[] NOT NULL DEFAULT '{}',
    agent_model_id           TEXT NOT NULL,
    agent_attestation_sha256 TEXT NOT NULL CHECK (agent_attestation_sha256 ~ '^[0-9a-f]{64}$'),
    agent_input_sha256       TEXT NOT NULL CHECK (agent_input_sha256 ~ '^[0-9a-f]{64}$'),
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, version_id, content_hash, agent_rules_version),
    FOREIGN KEY (tenant_id, version_id) REFERENCES document_version (tenant_id, id)
);
CREATE INDEX idx_review_suggestion_lookup ON review_suggestion (tenant_id, version_id, content_hash);

GRANT SELECT,INSERT ON review_suggestion TO rag_v2_gateway;
GRANT USAGE,SELECT ON SEQUENCE review_suggestion_id_seq TO rag_v2_gateway;

INSERT INTO schema_migration(version,checksum_sha256) VALUES ('0033', :'migration_checksum');
COMMIT;
