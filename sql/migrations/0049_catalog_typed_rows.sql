-- M8 typed product storage: one row per SKU and one row per size-chart size.
-- Written by Python ingest in the same transaction as the chunk projection.
BEGIN;

SELECT pg_advisory_xact_lock(742018);

CREATE TABLE catalog_sku (
    tenant_id          BIGINT NOT NULL REFERENCES tenant(id),
    shop_id            TEXT   NOT NULL,
    version_id         BIGINT NOT NULL,
    source_row         INT    NOT NULL CHECK (source_row > 0),
    chunk_index        INT    NOT NULL CHECK (chunk_index >= 0),
    sku_code           TEXT   NOT NULL CHECK (btrim(sku_code) <> '' AND char_length(sku_code) <= 128),
    spu_code           TEXT,
    title              TEXT   NOT NULL CHECK (btrim(title) <> ''),
    category           TEXT,
    brand              TEXT,
    color              TEXT,
    size_label         TEXT,
    size_chart_code    TEXT,
    attributes         JSONB  NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(attributes) = 'object'),
    list_price_cents   BIGINT CHECK (list_price_cents IS NULL OR list_price_cents >= 0),
    currency           CHAR(3) CHECK (currency IS NULL OR currency ~ '^[A-Z]{3}$'),
    price_observed_at  DATE,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, version_id) REFERENCES document_version(tenant_id, id),
    CHECK (list_price_cents IS NULL OR (currency IS NOT NULL AND price_observed_at IS NOT NULL)),
    CHECK (price_observed_at IS NULL OR list_price_cents IS NOT NULL),
    CHECK (currency IS NULL OR list_price_cents IS NOT NULL),
    CHECK (NOT attributes ? 'capacity_ml' OR
           (jsonb_typeof(attributes->'capacity_ml') = 'number' AND (attributes->>'capacity_ml')::numeric >= 0)),
    CHECK (NOT attributes ? 'length_cm' OR
           (jsonb_typeof(attributes->'length_cm') = 'number' AND (attributes->>'length_cm')::numeric >= 0)),
    CHECK (NOT attributes ? 'width_cm' OR
           (jsonb_typeof(attributes->'width_cm') = 'number' AND (attributes->>'width_cm')::numeric >= 0)),
    CHECK (NOT attributes ? 'height_cm' OR
           (jsonb_typeof(attributes->'height_cm') = 'number' AND (attributes->>'height_cm')::numeric >= 0)),
    CHECK (NOT attributes ? 'weight_g' OR
           (jsonb_typeof(attributes->'weight_g') = 'number' AND (attributes->>'weight_g')::numeric >= 0)),
    CHECK (NOT attributes ? 'material' OR jsonb_typeof(attributes->'material') = 'string'),
    CHECK (NOT attributes ? 'pattern' OR jsonb_typeof(attributes->'pattern') = 'string')
);
CREATE UNIQUE INDEX catalog_sku_version_sku_uidx
    ON catalog_sku (tenant_id, shop_id, version_id, lower(sku_code));
CREATE INDEX catalog_sku_scope_sku_idx ON catalog_sku (tenant_id, shop_id, sku_code);
CREATE INDEX catalog_sku_version_idx ON catalog_sku (tenant_id, version_id);

CREATE TABLE catalog_size_chart (
    tenant_id        BIGINT NOT NULL REFERENCES tenant(id),
    shop_id          TEXT   NOT NULL,
    version_id       BIGINT NOT NULL,
    source_row       INT    NOT NULL CHECK (source_row > 0),
    chunk_index      INT    NOT NULL CHECK (chunk_index >= 0),
    chart_code       TEXT   NOT NULL CHECK (btrim(chart_code) <> ''),
    size_label       TEXT   NOT NULL CHECK (btrim(size_label) <> ''),
    size_system      TEXT   NOT NULL CHECK (size_system IN
                         ('cn_shoe','cn_clothing','cn_kids','unisex','custom')),
    measurements     JSONB  NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(measurements) = 'object'),
    fit_note         TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, version_id) REFERENCES document_version(tenant_id, id),
    CHECK (NOT measurements ? 'chest_cm' OR
           (jsonb_typeof(measurements->'chest_cm') = 'number' AND (measurements->>'chest_cm')::numeric >= 0)),
    CHECK (NOT measurements ? 'waist_cm' OR
           (jsonb_typeof(measurements->'waist_cm') = 'number' AND (measurements->>'waist_cm')::numeric >= 0)),
    CHECK (NOT measurements ? 'hip_cm' OR
           (jsonb_typeof(measurements->'hip_cm') = 'number' AND (measurements->>'hip_cm')::numeric >= 0)),
    CHECK (NOT measurements ? 'foot_length_cm' OR
           (jsonb_typeof(measurements->'foot_length_cm') = 'number' AND (measurements->>'foot_length_cm')::numeric >= 0)),
    CHECK (NOT measurements ? 'height_cm' OR
           (jsonb_typeof(measurements->'height_cm') = 'number' AND (measurements->>'height_cm')::numeric >= 0)),
    CHECK (NOT measurements ? 'weight_kg' OR
           (jsonb_typeof(measurements->'weight_kg') = 'number' AND (measurements->>'weight_kg')::numeric >= 0))
);
CREATE UNIQUE INDEX catalog_size_chart_unique
    ON catalog_size_chart (tenant_id, shop_id, version_id, chart_code, size_label);
CREATE INDEX catalog_size_chart_filter_idx
    ON catalog_size_chart (tenant_id, shop_id, chart_code, size_label);

GRANT SELECT,INSERT ON catalog_sku,catalog_size_chart TO rag_v2_ingest;
GRANT SELECT ON catalog_sku,catalog_size_chart TO rag_v2_gateway,rag_v2_ai;

INSERT INTO schema_migration(version,checksum_sha256) VALUES ('0049', :'migration_checksum');
COMMIT;
