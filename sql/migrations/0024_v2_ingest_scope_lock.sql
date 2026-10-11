BEGIN;
SELECT pg_advisory_xact_lock(742018);

-- Row locks require UPDATE privilege in PostgreSQL. This narrow function
-- locks authorization rows without granting an ingest account identity DML.
CREATE FUNCTION ecr_lock_ingest_scope(
    p_tenant BIGINT,p_user BIGINT,p_tenant_external TEXT,p_user_external TEXT,
    p_shop TEXT,p_role TEXT,p_revision TEXT,p_admin_role TEXT
) RETURNS BOOLEAN LANGUAGE plpgsql SECURITY DEFINER SET search_path=public,pg_temp AS $$
DECLARE authorized BOOLEAN;
BEGIN
    SELECT TRUE INTO authorized FROM app_user u
      JOIN tenant t ON t.id=u.tenant_id
      JOIN shop s ON s.tenant_id=t.id AND s.id=p_shop
      WHERE u.tenant_id=p_tenant AND u.id=p_user AND u.external_id=p_user_external
        AND u.permission_revision=p_revision AND u.role=p_role AND u.revoked_at IS NULL
        AND t.external_id=p_tenant_external AND t.status='active' AND s.status='active'
        AND p_shop=ANY(u.shop_ids) FOR SHARE OF u,t,s;
    IF authorized IS NOT TRUE OR p_admin_role NOT IN ('knowledge_reviewer','merchant_admin') THEN
        RETURN FALSE;
    END IF;
    authorized := FALSE;
    SELECT TRUE INTO authorized FROM admin_role_grant
      WHERE tenant_id=p_tenant AND user_id=p_user AND role=p_admin_role AND status='active'
        AND (scope_kind='tenant' OR p_shop=ANY(shop_ids)) FOR SHARE;
    RETURN COALESCE(authorized,FALSE);
END $$;
REVOKE ALL ON FUNCTION ecr_lock_ingest_scope(BIGINT,BIGINT,TEXT,TEXT,TEXT,TEXT,TEXT,TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION ecr_lock_ingest_scope(BIGINT,BIGINT,TEXT,TEXT,TEXT,TEXT,TEXT,TEXT) TO rag_v2_ingest;

INSERT INTO schema_migration(version,checksum_sha256)
VALUES ('0024', :'migration_checksum') ON CONFLICT (version) DO NOTHING;
COMMIT;
