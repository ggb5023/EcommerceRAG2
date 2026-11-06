-- 0032 third-tier foundation: a tenant-free market shared layer and the tenant
-- watch config. market_observation is reachable only by rag_v2_intel; answer
-- roles must never appear in a GRANT on this table.
BEGIN;

SELECT pg_advisory_xact_lock(742018);

CREATE TABLE market_observation (
    id            BIGSERIAL PRIMARY KEY,
    market        TEXT NOT NULL CHECK (btrim(market) <> ''),
    category      TEXT NOT NULL CHECK (btrim(category) <> ''),
    platform      TEXT NOT NULL CHECK (btrim(platform) <> ''),
    observed_at   TIMESTAMPTZ NOT NULL,
    source_url    TEXT,
    payload_json  JSONB NOT NULL DEFAULT '{}',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX market_observation_observed_at_idx
    ON market_observation (observed_at DESC);

CREATE TABLE intel_watch_config (
    id          BIGSERIAL PRIMARY KEY,
    tenant_id   BIGINT NOT NULL REFERENCES tenant(id),
    market      TEXT NOT NULL CHECK (btrim(market) <> ''),
    category    TEXT NOT NULL CHECK (btrim(category) <> ''),
    platform    TEXT NOT NULL CHECK (btrim(platform) <> ''),
    enabled     BOOLEAN NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, market, category, platform)
);

-- Shared layer: the intelligence worker is the only runtime role. No answer
-- role (rag_v2_ai / rag_v2_ingest / rag_v2_gateway) receives a grant here.
GRANT SELECT,INSERT,UPDATE ON market_observation TO rag_v2_intel;
GRANT USAGE,SELECT ON SEQUENCE market_observation_id_seq TO rag_v2_intel;

-- Tenant layer: admin owns the watch list; the worker reads it.
GRANT SELECT,INSERT,UPDATE,DELETE ON intel_watch_config TO rag_v2_gateway;
GRANT USAGE,SELECT ON SEQUENCE intel_watch_config_id_seq TO rag_v2_gateway;
GRANT SELECT ON intel_watch_config TO rag_v2_intel;

INSERT INTO schema_migration(version,checksum_sha256) VALUES ('0054', :'migration_checksum');
COMMIT;
