-- L3 content inspection: the merchant workbench reads business tables and
-- writes only its own findings. It never changes document/version publication.
BEGIN;

SELECT pg_advisory_xact_lock(742018);

CREATE TABLE inspection_report (
    id             BIGSERIAL PRIMARY KEY,
    tenant_id      BIGINT NOT NULL REFERENCES tenant(id),
    shop_id        TEXT,
    kind           TEXT NOT NULL CHECK (kind IN
                     ('expired_not_removed','newer_version_conflict',
                      'unanswered_high_frequency','citation_invalid')),
    severity       TEXT NOT NULL CHECK (severity IN ('high','medium','low')),
    status         TEXT NOT NULL DEFAULT 'open'
                     CHECK (status IN ('open','acknowledged','resolved','dismissed')),
    subject_type   TEXT NOT NULL,
    subject_id     TEXT NOT NULL,
    fingerprint    TEXT NOT NULL,
    evidence_json  JSONB NOT NULL DEFAULT '{}',
    detected_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at    TIMESTAMPTZ,
    CHECK ((status IN ('resolved','dismissed')) = (resolved_at IS NOT NULL))
);

-- 0016 drafted UNIQUE (tenant_id, fingerprint, detected_at). That lets every
-- run insert a new row and contradicts its own "same open report is not
-- repeated" rule. The partial index keeps one open row per finding, while a
-- closed row and a later reopened row may coexist.
CREATE UNIQUE INDEX inspection_report_open_fingerprint_uidx
    ON inspection_report (tenant_id, fingerprint) WHERE status='open';
CREATE INDEX inspection_report_list_idx
    ON inspection_report (tenant_id, status, severity, detected_at DESC);

GRANT SELECT,INSERT,UPDATE,DELETE ON inspection_report TO rag_v2_gateway;
GRANT USAGE,SELECT ON SEQUENCE inspection_report_id_seq TO rag_v2_gateway;

INSERT INTO schema_migration(version,checksum_sha256) VALUES ('0053', :'migration_checksum');
COMMIT;
