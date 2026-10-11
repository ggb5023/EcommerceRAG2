BEGIN;
SELECT pg_advisory_xact_lock(742018);

-- Register before external I/O. A retained row is committed atomically with
-- its version, so unknown commit outcomes can be reconciled without deleting
-- objects that may already be referenced by a published document.
CREATE TABLE ingest_artifact_object (
    tenant_id BIGINT NOT NULL,
    job_id BIGINT NOT NULL,
    fencing_epoch BIGINT NOT NULL,
    owner_token TEXT NOT NULL,
    object_key TEXT PRIMARY KEY,
    sha256 TEXT NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    status TEXT NOT NULL CHECK (status IN ('planned','stored','retained','cleaned')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id,job_id) REFERENCES ingest_job(tenant_id,id)
);
CREATE INDEX ingest_artifact_cleanup_idx ON ingest_artifact_object(status,updated_at)
    WHERE status IN ('planned','stored');
GRANT SELECT ON ingest_artifact_object TO rag_v2_gateway;
GRANT SELECT,INSERT,UPDATE,DELETE ON ingest_artifact_object TO rag_v2_ingest;

-- Preserve the original audit/catalog grants: application code uses the
-- validated functions rather than direct grant mutation.
REVOKE INSERT,UPDATE,DELETE ON admin_role_grant FROM rag_v2_gateway;
GRANT SELECT ON admin_role_catalog TO rag_v2_gateway,rag_v2_ingest;

INSERT INTO schema_migration(version,checksum_sha256)
VALUES ('0023', :'migration_checksum') ON CONFLICT (version) DO NOTHING;
COMMIT;
