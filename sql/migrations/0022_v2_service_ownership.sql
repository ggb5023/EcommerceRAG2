-- V2 control commands, durable ingestion receipts and service write boundaries.
BEGIN;
SELECT pg_advisory_xact_lock(742018);

ALTER TABLE tenant ADD COLUMN external_id TEXT UNIQUE;
UPDATE tenant SET external_id='v2-demo-tenant' WHERE name='V2 LangGraph fixture tenant';

CREATE TABLE control_command (
    tenant_id BIGINT NOT NULL REFERENCES tenant(id),
    command_id TEXT NOT NULL,
    actor_user_id BIGINT NOT NULL REFERENCES app_user(id),
    shop_id TEXT NOT NULL,
    action TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    payload_sha256 TEXT NOT NULL CHECK (payload_sha256 ~ '^[0-9a-f]{64}$'),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','applied','rejected')),
    result_json JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id,command_id),
    FOREIGN KEY (tenant_id,shop_id) REFERENCES shop(tenant_id,id)
);

CREATE TABLE ingest_command_result (
    tenant_id BIGINT NOT NULL REFERENCES tenant(id),
    command_id TEXT NOT NULL,
    actor_user_id BIGINT NOT NULL REFERENCES app_user(id),
    shop_id TEXT NOT NULL,
    action TEXT NOT NULL,
    payload_sha256 TEXT NOT NULL CHECK (payload_sha256 ~ '^[0-9a-f]{64}$'),
    result_json JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id,command_id),
    FOREIGN KEY (tenant_id,shop_id) REFERENCES shop(tenant_id,id)
);

-- Machine lifecycle events are separate from Go's administrative audit.
CREATE TABLE ingest_event (LIKE admin_audit INCLUDING DEFAULTS INCLUDING CONSTRAINTS INCLUDING IDENTITY);
ALTER TABLE ingest_event ALTER COLUMN id DROP DEFAULT;
CREATE SEQUENCE ingest_event_id_seq OWNED BY ingest_event.id;
ALTER TABLE ingest_event ALTER COLUMN id SET DEFAULT nextval('ingest_event_id_seq');
ALTER TABLE ingest_event ADD PRIMARY KEY(id);

GRANT USAGE ON SCHEMA public TO rag_v2_gateway,rag_v2_ai,rag_v2_ingest;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO rag_v2_gateway;
GRANT INSERT,UPDATE,DELETE ON conversation,turn,message,conversation_execution,evidence_snapshot,
    citation,customer_reply,sse_event,turn_idempotency,control_command,routing_trace,tool_call_trace,feedback,admin_permission_request,
    admin_role_grant,acl,permission_revision,app_user,shop TO rag_v2_gateway;
GRANT INSERT ON admin_audit TO rag_v2_gateway;
GRANT USAGE,SELECT ON SEQUENCE conversation_id_seq,turn_id_seq,message_id_seq,
    conversation_execution_id_seq,evidence_snapshot_id_seq,citation_id_seq,
    admin_audit_id_seq,admin_permission_request_id_seq,admin_role_grant_id_seq,
    acl_id_seq,routing_trace_id_seq,tool_call_trace_id_seq,feedback_id_seq TO rag_v2_gateway;
GRANT EXECUTE ON FUNCTION ecr_issue_admin_grant(BIGINT,BIGINT,TEXT,TEXT,TEXT[],BIGINT,BIGINT,TEXT),
    ecr_revoke_admin_grant(BIGINT,BIGINT,BIGINT,TEXT),
    ecr_decide_admin_request(BIGINT,BIGINT,BIGINT,TEXT,TEXT),
    ecr_check_admin_grant(BIGINT,BIGINT,TEXT,TEXT) TO rag_v2_gateway;

GRANT SELECT ON tenant,app_user,shop,source,document,document_version,chunk,schema_migration,
    checkpoint_migrations TO rag_v2_ai;
GRANT SELECT,INSERT,UPDATE,DELETE ON v2_asking_checkpoint,checkpoints,checkpoint_blobs,
    checkpoint_writes,retrieval_trace TO rag_v2_ai;
GRANT USAGE,SELECT ON SEQUENCE retrieval_trace_id_seq TO rag_v2_ai;

GRANT SELECT ON tenant,app_user,shop,admin_role_grant,schema_migration TO rag_v2_ingest;
GRANT SELECT,INSERT,UPDATE,DELETE ON source,document,document_version,chunk,ingest_job,
    ingest_job_item,ingest_payload,ingest_publication,ingest_stage,ingest_package_upload,
    ingest_package_file,ingest_worker_heartbeat,ingest_command_result TO rag_v2_ingest;
GRANT SELECT,INSERT,DELETE ON source_manifest,source_file,ingest_event TO rag_v2_ingest;
GRANT USAGE,SELECT ON SEQUENCE source_id_seq,document_id_seq,document_version_id_seq,
    chunk_id_seq,ingest_job_id_seq,ingest_job_item_id_seq,ingest_stage_id_seq,
    source_manifest_id_seq,source_file_id_seq,ingest_event_id_seq TO rag_v2_ingest;

INSERT INTO schema_migration(version,checksum_sha256)
VALUES ('0022', :'migration_checksum') ON CONFLICT (version) DO NOTHING;
COMMIT;
