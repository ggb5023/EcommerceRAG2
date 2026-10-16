-- Development fixtures inserted by 0002, 0017, 0019 and 0021 stay in those
-- immutable files. Outside development and test databases they are disabled
-- here: the tenants stop resolving and their users are revoked. Development
-- databases keep them; scripts/dev/seed-fixtures.sql restores them explicitly.
BEGIN;
SELECT pg_advisory_xact_lock(742018);

-- Decides whether fixture identities may stay active in this database.
-- Known names: self-host installs use 'ecr_v2'; isolated test databases use
-- 'ecr_v2_test_*'; local development uses 'ecr_v2_dev_*'; the legacy M1 runner
-- uses '*m1_test*'. A wrong "true" leaves a usable fixture account in a real
-- installation, so unknown names must fall through to false.
CREATE FUNCTION ecr_is_development_database(db_name TEXT) RETURNS BOOLEAN
LANGUAGE plpgsql IMMUTABLE AS $fn$
BEGIN
    RETURN db_name ~ '^ecr_v2_(test|dev)_' OR db_name LIKE '%m1\_test%';
END;
$fn$;
REVOKE ALL ON FUNCTION ecr_is_development_database(TEXT) FROM PUBLIC;

DO $$
DECLARE
    fixture_tenants CONSTANT TEXT[] := ARRAY['M1 isolated prototype', 'V2 LangGraph fixture tenant'];
BEGIN
    IF NOT ecr_is_development_database(current_database()) THEN
        UPDATE app_user u SET revoked_at = now()
          FROM tenant t
         WHERE u.tenant_id = t.id AND t.name = ANY(fixture_tenants) AND u.revoked_at IS NULL;
        UPDATE tenant SET status = 'disabled'
         WHERE name = ANY(fixture_tenants) AND status = 'active';
    END IF;
END $$;

INSERT INTO schema_migration(version, checksum_sha256)
VALUES ('0029', :'migration_checksum') ON CONFLICT (version) DO NOTHING;
COMMIT;
