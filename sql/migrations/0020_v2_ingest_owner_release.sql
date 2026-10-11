-- Allow the current fenced owner to release its token after leaving running.
-- A non-null replacement owner still must advance the fencing epoch.
BEGIN;
SELECT pg_advisory_xact_lock(742018);

CREATE OR REPLACE FUNCTION guard_ingest_item_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.fencing_epoch < OLD.fencing_epoch THEN
        RAISE EXCEPTION 'fencing epoch cannot move backwards' USING ERRCODE = '55000';
    END IF;
    IF NEW.owner_token IS DISTINCT FROM OLD.owner_token
       AND OLD.status = 'running' AND OLD.lease_expires_at > now()
       AND NOT OLD.cancel_requested AND NEW.status = 'running' THEN
        RAISE EXCEPTION 'active ingest lease cannot be taken over' USING ERRCODE = '55000';
    END IF;
    IF NEW.owner_token IS NOT NULL
       AND NEW.owner_token IS DISTINCT FROM OLD.owner_token
       AND NEW.fencing_epoch <= OLD.fencing_epoch THEN
        RAISE EXCEPTION 'new ingest owner must advance the fencing epoch' USING ERRCODE = '55000';
    END IF;
    IF NEW.status = 'done' AND OLD.status <> 'done' AND pg_trigger_depth() = 1 THEN
        RAISE EXCEPTION 'ingest item can only complete through publication' USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END $$;

INSERT INTO schema_migration(version,checksum_sha256)
VALUES ('0020', :'migration_checksum') ON CONFLICT (version) DO NOTHING;
COMMIT;
