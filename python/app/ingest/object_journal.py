"""Fence external artifact I/O and reconcile objects after failed jobs."""
from __future__ import annotations

import psycopg

from app.storage import ObjectStore, ObjectStoreError


class JournaledStore:
    def __init__(self, store: ObjectStore, dsn: str, claim):
        self.store, self.dsn, self.claim = store, dsn, claim

    def put_bytes(self, key, data, **kwargs):
        claim = self.claim
        with psycopg.connect(self.dsn) as conn, conn.transaction():
            job = conn.execute(
                "SELECT id FROM ingest_job WHERE tenant_id=%s AND id=%s AND owner_token=%s "
                "AND fencing_epoch=%s AND status='running' AND lease_expires_at>now() "
                "AND NOT cancel_requested FOR SHARE",
                (claim.tenant_id, claim.job_id, claim.owner_token, claim.fencing_epoch),
            ).fetchone()
            if not job:
                raise ObjectStoreError("artifact owner is stale")
            conn.execute(
                "INSERT INTO ingest_artifact_object(tenant_id,job_id,fencing_epoch,owner_token,object_key,sha256,status) "
                "VALUES(%s,%s,%s,%s,%s,%s,'planned') ON CONFLICT(object_key) DO NOTHING",
                (claim.tenant_id, claim.job_id, claim.fencing_epoch, claim.owner_token, key, key.rsplit('/', 1)[-1]),
            )
        # The journal row survives a crash while a remote write is pending.
        with psycopg.connect(self.dsn) as conn, conn.transaction():
            row = conn.execute("SELECT status,owner_token FROM ingest_artifact_object WHERE object_key=%s FOR UPDATE", (key,)).fetchone()
            if not row or row[0] not in {"planned", "stored"} or row[1] != claim.owner_token:
                raise ObjectStoreError("artifact attempt is closed")
            metadata = self.store.put_bytes(key, data, **kwargs)
            conn.execute("UPDATE ingest_artifact_object SET status='stored',updated_at=now() WHERE object_key=%s", (key,))
            return metadata

    def delete(self, key):
        with psycopg.connect(self.dsn) as conn, conn.transaction():
            row = conn.execute("SELECT status,owner_token FROM ingest_artifact_object WHERE object_key=%s FOR UPDATE", (key,)).fetchone()
            if not row or row[1] != self.claim.owner_token or row[0] == "retained":
                raise ObjectStoreError("artifact deletion is not authorized")
            self.store.delete(key)
            conn.execute("UPDATE ingest_artifact_object SET status='cleaned',updated_at=now() WHERE object_key=%s", (key,))


def cleanup_orphans(store: ObjectStore, dsn: str, limit: int = 100) -> int:
    cleaned = 0
    with psycopg.connect(dsn) as conn, conn.transaction():
        # Acquire the job lock before object locks, in the same order as the
        # version commit. A commit already in flight settles before cleanup.
        jobs = conn.execute(
            "SELECT j.tenant_id,j.id FROM ingest_job j WHERE EXISTS(SELECT 1 FROM ingest_artifact_object a "
            "WHERE a.tenant_id=j.tenant_id AND a.job_id=j.id AND a.status IN ('planned','stored') "
            "AND (a.fencing_epoch<>j.fencing_epoch OR j.status<>'running' OR j.lease_expires_at<=now())) "
            "LIMIT %s FOR UPDATE OF j SKIP LOCKED", (limit,),
        ).fetchall()
        for tenant, job in jobs:
            rows = conn.execute(
                "SELECT a.object_key FROM ingest_artifact_object a JOIN ingest_job j ON j.tenant_id=a.tenant_id AND j.id=a.job_id "
                "WHERE a.tenant_id=%s AND a.job_id=%s AND a.status IN ('planned','stored') "
                "AND (a.fencing_epoch<>j.fencing_epoch OR j.status<>'running' OR j.lease_expires_at<=now()) "
                "FOR UPDATE OF a SKIP LOCKED", (tenant, job),
            ).fetchall()
            for (key,) in rows:
                try:
                    store.delete(key)
                except ObjectStoreError:
                    continue
                conn.execute("UPDATE ingest_artifact_object SET status='cleaned',updated_at=now() WHERE object_key=%s", (key,))
                cleaned += 1
    return cleaned
