-- Isolated v2 HTTP fixture tenant. This is development data only and is not
-- an OIDC identity or a production merchant dataset. Go maps this database
-- tenant name to the signed scope id `v2-demo-tenant`.
BEGIN;
SELECT pg_advisory_xact_lock(742018);

INSERT INTO tenant (name, status)
SELECT 'V2 LangGraph fixture tenant', 'active'
WHERE NOT EXISTS (SELECT 1 FROM tenant WHERE name = 'V2 LangGraph fixture tenant');

INSERT INTO app_user (tenant_id, external_id, role, shop_ids, display_name)
SELECT id, 'dev-user', 'operator', ARRAY['shop-demo'], 'V2 development user'
FROM tenant WHERE name = 'V2 LangGraph fixture tenant'
ON CONFLICT (tenant_id, external_id) DO UPDATE
SET role = EXCLUDED.role, shop_ids = EXCLUDED.shop_ids,
    revoked_at = NULL, display_name = EXCLUDED.display_name;

INSERT INTO shop (tenant_id, id, name, status)
SELECT id, 'shop-demo', 'V2 fixture shop', 'active'
FROM tenant WHERE name = 'V2 LangGraph fixture tenant'
ON CONFLICT (tenant_id, id) DO UPDATE SET name = EXCLUDED.name, status = 'active';

DO $$
DECLARE
    v_tenant BIGINT;
    v_source BIGINT;
BEGIN
    SELECT id INTO v_tenant FROM tenant WHERE name = 'V2 LangGraph fixture tenant';
    SELECT id INTO v_source FROM source
      WHERE tenant_id=v_tenant AND shop_id='shop-demo' AND type='v2_fixture'
      ORDER BY id LIMIT 1;
    IF v_source IS NULL THEN
        INSERT INTO source(tenant_id, shop_id, type, uri)
        VALUES(v_tenant, 'shop-demo', 'v2_fixture', 'fixture://v2')
        RETURNING id INTO v_source;
    END IF;
    INSERT INTO document(tenant_id, source_id, logical_key, title, doc_type, status, meta_json)
    VALUES
      (v_tenant, v_source, 'v2-doc-bottle', 'V2 bottle fixture', 'markdown', 'active', '{"disclosure_class":"external_allowed"}'::jsonb),
      (v_tenant, v_source, 'v2-doc-shipping', 'V2 shipping fixture', 'markdown', 'active', '{"disclosure_class":"external_allowed"}'::jsonb),
      (v_tenant, v_source, 'v2-doc-internal', 'V2 internal fixture', 'markdown', 'active', '{"disclosure_class":"internal_only"}'::jsonb)
    ON CONFLICT (tenant_id, logical_key) DO UPDATE
      SET source_id=EXCLUDED.source_id, title=EXCLUDED.title, doc_type=EXCLUDED.doc_type,
          status='active', meta_json=EXCLUDED.meta_json;
END $$;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='rag_app') THEN
        GRANT SELECT ON tenant, app_user, shop, source, document TO rag_app;
    END IF;
END $$;

INSERT INTO schema_migration(version, checksum_sha256)
VALUES ('0017', :'migration_checksum') ON CONFLICT (version) DO NOTHING;
COMMIT;
