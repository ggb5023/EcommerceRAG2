-- Re-activate the development fixture identities created by migrations 0002,
-- 0017, 0019 and 0021. Migration 0029 leaves them active in development and
-- test databases; run this only if a development database was migrated before
-- it was renamed, or after a test revoked them. Refuses any other database.
--
--   psql "$V2_MIGRATION_DSN" -X -v ON_ERROR_STOP=1 -f scripts/dev/seed-fixtures.sql
BEGIN;
DO $$
BEGIN
    IF NOT ecr_is_development_database(current_database()) THEN
        RAISE EXCEPTION 'fixture seed requires a development or test database';
    END IF;
    UPDATE tenant SET status = 'active'
     WHERE name IN ('M1 isolated prototype', 'V2 LangGraph fixture tenant') AND status <> 'active';
    UPDATE app_user u SET revoked_at = NULL
      FROM tenant t
     WHERE u.tenant_id = t.id
       AND t.name IN ('M1 isolated prototype', 'V2 LangGraph fixture tenant')
       AND u.revoked_at IS NOT NULL;
END $$;
COMMIT;
