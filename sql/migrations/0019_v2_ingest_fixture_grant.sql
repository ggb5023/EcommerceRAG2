-- Development-only grant for the isolated v2 fixture submitter.
-- This is not an OIDC or production identity; real grants remain control-plane owned.
BEGIN;
SELECT pg_advisory_xact_lock(742018);

DO $$
DECLARE
    v_tenant BIGINT;
    v_user BIGINT;
BEGIN
    SELECT t.id, u.id INTO v_tenant, v_user
    FROM tenant t
    JOIN app_user u ON u.tenant_id=t.id AND u.external_id='dev-user'
    WHERE t.name='V2 LangGraph fixture tenant';
    IF v_tenant IS NOT NULL THEN
        INSERT INTO admin_role_grant(
            tenant_id,user_id,role,scope_kind,shop_ids,status,granted_by
        ) VALUES (
            v_tenant,v_user,'merchant_admin','shops',ARRAY['shop-demo'],'active',v_user
        )
        ON CONFLICT (tenant_id,user_id,role) DO UPDATE SET
            scope_kind=EXCLUDED.scope_kind,
            shop_ids=EXCLUDED.shop_ids,
            status='active',
            granted_by=EXCLUDED.granted_by,
            revoked_by=NULL,
            revoked_at=NULL;
    END IF;
END $$;

INSERT INTO schema_migration(version,checksum_sha256)
VALUES ('0019', :'migration_checksum') ON CONFLICT (version) DO NOTHING;
COMMIT;
