"""Persistence for the intelligence worker. Only rag_v2_intel connects here."""
from __future__ import annotations

import json
from dataclasses import dataclass
from datetime import datetime, timedelta

import psycopg


@dataclass(frozen=True)
class IntelJob:
    id: int
    tenant_id: int | None
    watch_config_id: int | None
    market: str
    category: str
    platform: str
    query: str
    retry_count: int
    fencing_epoch: int


class IntelDatabase:
    def __init__(self, dsn: str) -> None:
        if not dsn.strip():
            raise ValueError("V2_INTEL_DSN is required")
        self.dsn = dsn

    def _connect(self):
        return psycopg.connect(self.dsn, connect_timeout=5)

    def ensure_jobs(self, recrawl_hours: int) -> int:
        """Create one pending job per enabled watch without a recent job."""
        if recrawl_hours <= 0:
            raise ValueError("recrawl interval must be positive")
        with self._connect() as conn:
            with conn.cursor() as cursor:
                cursor.execute(
                    """INSERT INTO intel_job(
                           tenant_id,watch_config_id,market,category,platform,query,status,next_attempt_at)
                       SELECT w.tenant_id,w.id,w.market,w.category,w.platform,
                              w.market || ' ' || w.category || ' ' || w.platform,'pending',now()
                       FROM intel_watch_config w
                       WHERE w.enabled
                         AND NOT EXISTS (
                           SELECT 1 FROM intel_job j
                           WHERE j.watch_config_id=w.id
                             AND (j.created_at > now() - %s
                                  OR j.status IN ('pending','running'))
                         )""",
                    (timedelta(hours=recrawl_hours),),
                )
                created = cursor.rowcount
            conn.commit()
        return max(created, 0)

    def claim_job(self, lease_seconds: int) -> IntelJob | None:
        if lease_seconds <= 0:
            raise ValueError("lease must be positive")
        with self._connect() as conn:
            with conn.cursor() as cursor:
                cursor.execute(
                    """WITH candidate AS (
                           SELECT id FROM intel_job
                           WHERE (status='pending' AND next_attempt_at<=now())
                              OR (status='running' AND lease_expires_at<now())
                           ORDER BY next_attempt_at,id
                           FOR UPDATE SKIP LOCKED
                           LIMIT 1
                       )
                       UPDATE intel_job j
                       SET status='running',lease_expires_at=now()+%s,
                           fencing_epoch=fencing_epoch+1,updated_at=now()
                       FROM candidate c WHERE j.id=c.id
                       RETURNING j.id,j.tenant_id,j.watch_config_id,j.market,j.category,
                                 j.platform,j.query,j.retry_count,j.fencing_epoch""",
                    (timedelta(seconds=lease_seconds),),
                )
                row = cursor.fetchone()
            conn.commit()
        if row is None:
            return None
        return IntelJob(*row)

    def complete_success(self, job: IntelJob, results) -> bool:
        """Upsert observations and finish the job only while the fence is ours."""
        stored = 0
        with self._connect() as conn:
            with conn.cursor() as cursor:
                cursor.execute(
                    "SELECT fencing_epoch FROM intel_job WHERE id=%s AND status='running' FOR UPDATE",
                    (job.id,),
                )
                row = cursor.fetchone()
                if row is None or row[0] != job.fencing_epoch:
                    conn.rollback()
                    return False
                for result in results:
                    if not result.url.strip():
                        continue
                    payload = json.dumps(
                        {
                            "title": result.title,
                            "content": result.content,
                            "score": result.score,
                            "published_date": result.published_date,
                        },
                        ensure_ascii=False,
                    )
                    cursor.execute(
                        """INSERT INTO market_observation(
                               market,category,platform,observed_at,source_url,payload_json)
                           VALUES(%s,%s,%s,now(),%s,%s::jsonb)
                           ON CONFLICT (market,category,platform,source_url)
                           DO UPDATE SET payload_json=EXCLUDED.payload_json,
                                         observed_at=EXCLUDED.observed_at""",
                        (job.market, job.category, job.platform, result.url, payload),
                    )
                    stored += 1
                cursor.execute(
                    """UPDATE intel_job
                       SET status='done',observed_count=%s,error=NULL,lease_expires_at=NULL,updated_at=now()
                       WHERE id=%s AND fencing_epoch=%s AND status='running'""",
                    (stored, job.id, job.fencing_epoch),
                )
                if cursor.rowcount != 1:
                    conn.rollback()
                    return False
            conn.commit()
        return True

    def complete_failure(self, job: IntelJob, status: str, error: str | None,
                         retry_at: datetime | None = None) -> bool:
        if status not in {"pending", "failed"}:
            raise ValueError("failure status must be pending or failed")
        if status == "pending" and retry_at is None:
            raise ValueError("retry_at is required for a pending retry")
        with self._connect() as conn:
            with conn.cursor() as cursor:
                if status == "pending":
                    cursor.execute(
                        """UPDATE intel_job
                           SET status='pending',retry_count=retry_count+1,error=%s,
                               lease_expires_at=NULL,next_attempt_at=%s,updated_at=now()
                           WHERE id=%s AND fencing_epoch=%s AND status='running'""",
                        (error, retry_at, job.id, job.fencing_epoch),
                    )
                else:
                    cursor.execute(
                        """UPDATE intel_job
                           SET status='failed',error=%s,lease_expires_at=NULL,updated_at=now()
                           WHERE id=%s AND fencing_epoch=%s AND status='running'""",
                        (error, job.id, job.fencing_epoch),
                    )
                ok = cursor.rowcount == 1
            conn.commit()
        return ok

    def list_observations(self, market: str, category: str, platform: str,
                          since: datetime | None, limit: int) -> list[dict]:
        if not 1 <= limit <= 100:
            raise ValueError("limit must be between 1 and 100")
        with self._connect() as conn:
            rows = conn.execute(
                """SELECT market,category,platform,COALESCE(source_url,''),
                          COALESCE(payload_json->>'title',''),
                          COALESCE((payload_json->>'score')::double precision,0),
                          COALESCE(payload_json->>'published_date',''),
                          observed_at
                   FROM market_observation
                   WHERE (%s='' OR market=%s)
                     AND (%s='' OR category=%s)
                     AND (%s='' OR platform=%s)
                     AND (%s::timestamptz IS NULL OR observed_at >= %s)
                   ORDER BY observed_at DESC,id DESC
                   LIMIT %s""",
                (market, market, category, category, platform, platform, since, since, limit),
            ).fetchall()
        return [
            {
                "market": row[0], "category": row[1], "platform": row[2],
                "source_url": row[3], "title": row[4], "score": float(row[5]),
                "published_date": row[6], "observed_at": row[7].isoformat(),
            }
            for row in rows
        ]
