-- Customer identities and durable conversation control are Go-owned.
BEGIN;
SELECT pg_advisory_xact_lock(742018);

ALTER TABLE shop ADD COLUMN public_slug TEXT UNIQUE,
    ADD COLUMN customer_chat_enabled BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN welcome_message TEXT NOT NULL DEFAULT '欢迎咨询本店商品与服务。',
    ADD COLUMN public_permission_revision TEXT NOT NULL DEFAULT 'public-1';

CREATE TABLE customer_identity (
    id TEXT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    shop_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id,shop_id,id),
    FOREIGN KEY (tenant_id,shop_id) REFERENCES shop(tenant_id,id)
);
CREATE TABLE customer_session (
    id TEXT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    shop_id TEXT NOT NULL,
    customer_id TEXT NOT NULL,
    token_sha256 TEXT NOT NULL UNIQUE CHECK (token_sha256 ~ '^[0-9a-f]{64}$'),
    csrf_token TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    idle_until TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    FOREIGN KEY (tenant_id,shop_id,customer_id) REFERENCES customer_identity(tenant_id,shop_id,id)
);
CREATE TABLE customer_conversation (
    id TEXT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    shop_id TEXT NOT NULL,
    customer_id TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'AI' CHECK (state IN ('AI','WAITING_HUMAN','HUMAN','CLOSED')),
    epoch BIGINT NOT NULL DEFAULT 1 CHECK (epoch > 0),
    owner_user_id BIGINT REFERENCES app_user(id),
    owner_session_hash TEXT,
    lease_until TIMESTAMPTZ,
    active_execution TEXT,
    clarification_count INT NOT NULL DEFAULT 0 CHECK (clarification_count BETWEEN 0 AND 1),
    last_sequence BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id,id),
    FOREIGN KEY (tenant_id,shop_id,customer_id) REFERENCES customer_identity(tenant_id,shop_id,id),
    CHECK ((state='HUMAN' AND owner_user_id IS NOT NULL AND lease_until IS NOT NULL)
        OR (state<>'HUMAN' AND owner_user_id IS NULL AND lease_until IS NULL))
);
CREATE INDEX customer_queue ON customer_conversation(tenant_id,shop_id,state,updated_at);
CREATE TABLE customer_execution (
    id TEXT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    conversation_id TEXT NOT NULL,
    epoch BIGINT NOT NULL,
    session_id TEXT NOT NULL REFERENCES customer_session(id),
    state TEXT NOT NULL DEFAULT 'PENDING' CHECK (state IN ('PENDING','EXECUTING','DONE','ASKING','FAILED','CANCELLED')),
    behavior TEXT,
    reason_code TEXT,
    answer TEXT,
    evidence_json JSONB NOT NULL DEFAULT '[]',
    is_mock BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ,
    FOREIGN KEY (tenant_id,conversation_id) REFERENCES customer_conversation(tenant_id,id)
);
CREATE TABLE customer_message (
    id TEXT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    conversation_id TEXT NOT NULL,
    source TEXT NOT NULL CHECK (source IN ('customer','assistant','agent','system')),
    content TEXT NOT NULL,
    actor_user_id BIGINT REFERENCES app_user(id),
    execution_id TEXT UNIQUE REFERENCES customer_execution(id),
    citations_json JSONB NOT NULL DEFAULT '[]',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id,conversation_id) REFERENCES customer_conversation(tenant_id,id),
    CHECK ((source='agent') = (actor_user_id IS NOT NULL))
);
CREATE INDEX customer_message_history ON customer_message(tenant_id,conversation_id,created_at,id);
CREATE TABLE customer_event (
    tenant_id BIGINT NOT NULL,
    conversation_id TEXT NOT NULL,
    sequence BIGINT NOT NULL,
    event_type TEXT NOT NULL,
    data_json JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id,conversation_id,sequence),
    FOREIGN KEY (tenant_id,conversation_id) REFERENCES customer_conversation(tenant_id,id)
);
CREATE TABLE customer_command (
    tenant_id BIGINT NOT NULL,
    conversation_id TEXT NOT NULL,
    actor TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    payload_sha256 TEXT NOT NULL,
    result_json JSONB NOT NULL,
    PRIMARY KEY (tenant_id,conversation_id,actor,idempotency_key),
    FOREIGN KEY (tenant_id,conversation_id) REFERENCES customer_conversation(tenant_id,id)
);

-- A public permission revision covers ACL, resource and shop changes. Customer
-- scopes never derive authority from an employee's permission revision.
CREATE FUNCTION ecr_bump_public_revision() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=public,pg_temp AS $$
DECLARE old_tenant BIGINT; new_tenant BIGINT;
BEGIN
    IF TG_OP<>'INSERT' THEN
        IF TG_TABLE_NAME='tenant' THEN old_tenant:=OLD.id; ELSE old_tenant:=OLD.tenant_id; END IF;
        UPDATE shop SET public_permission_revision=ecr_new_permission_revision(old_tenant,'public_changed') WHERE tenant_id=old_tenant;
    END IF;
    IF TG_OP<>'DELETE' THEN
        IF TG_TABLE_NAME='tenant' THEN new_tenant:=NEW.id; ELSE new_tenant:=NEW.tenant_id; END IF;
        IF new_tenant IS DISTINCT FROM old_tenant THEN
            UPDATE shop SET public_permission_revision=ecr_new_permission_revision(new_tenant,'public_changed') WHERE tenant_id=new_tenant;
        END IF;
        RETURN NEW;
    END IF;
    RETURN OLD;
END;
$$;
REVOKE ALL ON FUNCTION ecr_bump_public_revision() FROM PUBLIC;
CREATE TRIGGER public_acl_revision AFTER INSERT OR UPDATE OR DELETE ON acl FOR EACH ROW EXECUTE FUNCTION ecr_bump_public_revision();
CREATE TRIGGER public_source_revision AFTER INSERT OR UPDATE OR DELETE ON source FOR EACH ROW EXECUTE FUNCTION ecr_bump_public_revision();
CREATE TRIGGER public_document_revision AFTER INSERT OR UPDATE OR DELETE ON document FOR EACH ROW EXECUTE FUNCTION ecr_bump_public_revision();
CREATE TRIGGER public_shop_revision AFTER UPDATE OF status,customer_chat_enabled ON shop FOR EACH ROW EXECUTE FUNCTION ecr_bump_public_revision();
CREATE TRIGGER public_tenant_revision AFTER UPDATE OF status ON tenant FOR EACH ROW EXECUTE FUNCTION ecr_bump_public_revision();

-- Existing staff role constraints stay unchanged. Only the ACL read audience
-- gains a customer role; it cannot be used to create an employee principal.
CREATE OR REPLACE FUNCTION ecr_validate_acl_scope() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE owner_tenant BIGINT; subject_tenant BIGINT;
BEGIN
    SELECT id INTO owner_tenant FROM tenant WHERE id=NEW.tenant_id;
    IF owner_tenant IS NULL THEN RAISE EXCEPTION 'ACL tenant does not exist' USING ERRCODE='23503'; END IF;
    IF NEW.resource_type='document' THEN SELECT tenant_id INTO owner_tenant FROM document WHERE id=NEW.resource_id;
    ELSIF NEW.resource_type='source' THEN SELECT tenant_id INTO owner_tenant FROM source WHERE id=NEW.resource_id;
    ELSE owner_tenant:=NEW.resource_id; END IF;
    IF owner_tenant IS DISTINCT FROM NEW.tenant_id THEN RAISE EXCEPTION 'ACL resource outside tenant' USING ERRCODE='23503'; END IF;
    IF NEW.subject_type='user' THEN
        SELECT tenant_id INTO subject_tenant FROM app_user WHERE tenant_id=NEW.tenant_id AND (external_id=NEW.subject_id OR id::text=NEW.subject_id) LIMIT 1;
        IF subject_tenant IS DISTINCT FROM NEW.tenant_id THEN RAISE EXCEPTION 'ACL user outside tenant' USING ERRCODE='23503'; END IF;
    ELSIF NEW.subject_id NOT IN ('owner','admin','operator','viewer','customer') THEN
        RAISE EXCEPTION 'ACL role invalid' USING ERRCODE='23514';
    END IF;
    IF NEW.subject_type='role' AND NEW.subject_id='customer' AND NEW.permission<>'read' THEN
        RAISE EXCEPTION 'Customer ACL is read only' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END;
$$;
REVOKE ALL ON customer_identity,customer_session,customer_conversation,customer_execution,customer_message,customer_event,customer_command FROM PUBLIC,rag_v2_ai,rag_v2_ingest;
GRANT SELECT,INSERT,UPDATE,DELETE ON customer_identity,customer_session,customer_conversation,customer_execution,customer_message,customer_event,customer_command TO rag_v2_gateway;
INSERT INTO schema_migration(version,checksum_sha256) VALUES('0026', :'migration_checksum');
COMMIT;
