-- Local password accounts: gateway-owned credentials, sessions and auth events.
-- tenant/shop writers stay migrations and bootstrap; this migration adds no
-- tenant-creation function and no SECURITY DEFINER helper.
BEGIN;
SELECT pg_advisory_xact_lock(742018);

ALTER TABLE app_user ADD COLUMN revoked_by BIGINT REFERENCES app_user(id),
    ADD COLUMN revoked_reason TEXT;
ALTER TABLE app_user ADD CONSTRAINT app_user_revoked_reason_check
    CHECK (revoked_reason IS NULL OR revoked_reason IN ('account_disabled'));

CREATE TABLE local_credential (
    tenant_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    password_hash TEXT NOT NULL,
    must_change_password BOOLEAN NOT NULL DEFAULT false,
    failed_attempts INTEGER NOT NULL DEFAULT 0,
    locked_until TIMESTAMPTZ,
    last_failed_at TIMESTAMPTZ,
    password_changed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id,user_id),
    FOREIGN KEY (tenant_id,user_id) REFERENCES app_user(tenant_id,id) ON DELETE CASCADE,
    CHECK (failed_attempts >= 0)
);

CREATE TABLE local_session (
    id_hash TEXT PRIMARY KEY CHECK (id_hash ~ '^[0-9a-f]{64}$'),
    tenant_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    csrf_hash TEXT NOT NULL CHECK (csrf_hash ~ '^[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    idle_expires_at TIMESTAMPTZ NOT NULL,
    absolute_expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    revoked_reason TEXT CHECK (revoked_reason IS NULL OR revoked_reason IN
        ('password_changed','admin_reset','logout','lockout','account_revoked')),
    FOREIGN KEY (tenant_id,user_id) REFERENCES app_user(tenant_id,id) ON DELETE CASCADE,
    CHECK (absolute_expires_at > created_at),
    CHECK ((revoked_at IS NULL) = (revoked_reason IS NULL))
);
CREATE INDEX idx_local_session_user ON local_session(tenant_id,user_id) WHERE revoked_at IS NULL;
CREATE INDEX idx_local_session_expiry ON local_session(absolute_expires_at);

CREATE TABLE local_auth_event (
    id BIGSERIAL PRIMARY KEY,
    event TEXT NOT NULL CHECK (event IN
        ('login_failed','login_succeeded','password_changed','account_locked','rate_limited',
         'account_disabled','account_enabled')),
    subject_hash TEXT,
    tenant_id BIGINT,
    user_id BIGINT,
    ip INET,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_local_auth_event_timeline ON local_auth_event(created_at DESC, id DESC);
CREATE INDEX idx_local_auth_event_subject ON local_auth_event(subject_hash, created_at DESC);

REVOKE ALL ON local_credential,local_session,local_auth_event FROM PUBLIC,rag_v2_ai,rag_v2_ingest;
GRANT SELECT,INSERT,UPDATE,DELETE ON local_credential,local_session TO rag_v2_gateway;
GRANT SELECT,INSERT ON local_auth_event TO rag_v2_gateway;
GRANT USAGE,SELECT ON SEQUENCE local_auth_event_id_seq TO rag_v2_gateway;
-- CLI/staff creation inserts app_user rows through the gateway role.
GRANT USAGE,SELECT ON SEQUENCE app_user_id_seq TO rag_v2_gateway;

INSERT INTO schema_migration(version,checksum_sha256) VALUES ('0030', :'migration_checksum');
COMMIT;
