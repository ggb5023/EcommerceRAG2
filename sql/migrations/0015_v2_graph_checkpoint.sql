-- Minimal v2 ASKING/HITL control row. LangGraph's PostgresSaver stores the
-- empty checkpoint payload; this table owns scope, TTL and single-consumer
-- state. No query, evidence or chat body is stored here.
BEGIN;
SELECT pg_advisory_xact_lock(742018);

CREATE TABLE v2_asking_checkpoint (
    thread_id TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    conversation_id TEXT NOT NULL,
    turn_id TEXT NOT NULL,
    permission_revision TEXT NOT NULL,
    pending_kind TEXT NOT NULL CHECK (pending_kind IN ('ASKING', 'HITL')),
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    checkpoint_id TEXT NOT NULL,
    consumed_at TIMESTAMPTZ,
    CHECK (length(thread_id) > 0),
    CHECK (length(tenant_id) > 0),
    CHECK (length(user_id) > 0),
    CHECK (length(conversation_id) > 0),
    CHECK (length(turn_id) > 0),
    CHECK (length(permission_revision) > 0),
    CHECK (expires_at > created_at)
);

CREATE INDEX v2_asking_checkpoint_expiry_idx
    ON v2_asking_checkpoint (expires_at)
    WHERE consumed_at IS NULL;

GRANT SELECT, INSERT, UPDATE, DELETE ON v2_asking_checkpoint TO rag_app;

-- LangGraph PostgresSaver schema, pinned to the 2.x migration shape used by
-- the locked dependency.  The application role receives DML only; schema
-- creation and upgrades remain migration-job responsibilities.  The upstream
-- package uses CREATE INDEX CONCURRENTLY in one of its setup steps, which is
-- deliberately represented as ordinary transactional indexes here.
CREATE TABLE IF NOT EXISTS checkpoint_migrations (
    v INTEGER PRIMARY KEY
);
CREATE TABLE IF NOT EXISTS checkpoints (
    thread_id TEXT NOT NULL,
    checkpoint_ns TEXT NOT NULL DEFAULT '',
    checkpoint_id TEXT NOT NULL,
    parent_checkpoint_id TEXT,
    type TEXT,
    checkpoint JSONB NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}',
    PRIMARY KEY (thread_id, checkpoint_ns, checkpoint_id)
);
CREATE TABLE IF NOT EXISTS checkpoint_blobs (
    thread_id TEXT NOT NULL,
    checkpoint_ns TEXT NOT NULL DEFAULT '',
    channel TEXT NOT NULL,
    version TEXT NOT NULL,
    type TEXT NOT NULL,
    blob BYTEA,
    PRIMARY KEY (thread_id, checkpoint_ns, channel, version)
);
CREATE TABLE IF NOT EXISTS checkpoint_writes (
    thread_id TEXT NOT NULL,
    checkpoint_ns TEXT NOT NULL DEFAULT '',
    checkpoint_id TEXT NOT NULL,
    task_id TEXT NOT NULL,
    idx INTEGER NOT NULL,
    channel TEXT NOT NULL,
    type TEXT,
    blob BYTEA NOT NULL,
    task_path TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (thread_id, checkpoint_ns, checkpoint_id, task_id, idx)
);
CREATE INDEX IF NOT EXISTS checkpoints_thread_id_idx ON checkpoints(thread_id);
CREATE INDEX IF NOT EXISTS checkpoint_blobs_thread_id_idx ON checkpoint_blobs(thread_id);
CREATE INDEX IF NOT EXISTS checkpoint_writes_thread_id_idx ON checkpoint_writes(thread_id);

INSERT INTO checkpoint_migrations(v)
SELECT value FROM generate_series(0, 9) AS value
ON CONFLICT (v) DO NOTHING;

GRANT SELECT, INSERT, UPDATE, DELETE ON checkpoint_migrations TO rag_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON checkpoints TO rag_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON checkpoint_blobs TO rag_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON checkpoint_writes TO rag_app;

INSERT INTO schema_migration(version, checksum_sha256)
VALUES ('0015', :'migration_checksum') ON CONFLICT (version) DO NOTHING;
COMMIT;
