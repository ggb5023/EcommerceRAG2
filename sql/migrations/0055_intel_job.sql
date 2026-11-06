-- 0032 stage 2: the crawl queue and the observation identity needed by the
-- Tavily worker. The queue copies ingest's lease/fencing semantics but never
-- writes ingest_job. Only rag_v2_intel may write these shared rows.
BEGIN;

SELECT pg_advisory_xact_lock(742018);

-- A Tavily result is identified by its query dimensions and source URL.
-- NULL source URLs do not collide; the worker always supplies a URL.
ALTER TABLE market_observation
    ADD CONSTRAINT market_observation_identity_unique
    UNIQUE (market, category, platform, source_url);

CREATE TABLE intel_job (
    id                BIGSERIAL PRIMARY KEY,
    tenant_id         BIGINT REFERENCES tenant(id) ON DELETE SET NULL,
    watch_config_id   BIGINT REFERENCES intel_watch_config(id) ON DELETE SET NULL,
    market            TEXT NOT NULL CHECK (btrim(market) <> ''),
    category          TEXT NOT NULL CHECK (btrim(category) <> ''),
    platform          TEXT NOT NULL CHECK (btrim(platform) <> ''),
    query             TEXT NOT NULL CHECK (btrim(query) <> ''),
    status            TEXT NOT NULL DEFAULT 'pending'
                          CHECK (status IN ('pending','running','done','failed')),
    lease_expires_at  TIMESTAMPTZ,
    retry_count       INT NOT NULL DEFAULT 0 CHECK (retry_count >= 0),
    next_attempt_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    error             TEXT,
    observed_count    INT NOT NULL DEFAULT 0 CHECK (observed_count >= 0),
    fencing_epoch     BIGINT NOT NULL DEFAULT 0 CHECK (fencing_epoch >= 0),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX intel_job_due_idx ON intel_job (status, next_attempt_at);
CREATE INDEX intel_job_watch_created_idx ON intel_job (watch_config_id, created_at DESC);

-- Search is a usage_log slot with no token concept (0021): the worker records
-- one row per Tavily call with NULL input/output tokens.
ALTER TABLE usage_log DROP CONSTRAINT IF EXISTS usage_log_slot_check;
ALTER TABLE usage_log ADD CONSTRAINT usage_log_slot_check
    CHECK (slot IN ('control','embedding','rerank','generation','search'));

GRANT SELECT,INSERT,UPDATE ON intel_job TO rag_v2_intel;
GRANT USAGE,SELECT ON SEQUENCE intel_job_id_seq TO rag_v2_intel;
GRANT INSERT ON usage_log TO rag_v2_intel;
GRANT USAGE,SELECT ON SEQUENCE usage_log_id_seq TO rag_v2_intel;

INSERT INTO schema_migration(version,checksum_sha256) VALUES ('0055', :'migration_checksum');
COMMIT;
