"""Best-effort token usage recording. No currency or balance is stored."""
from __future__ import annotations

import logging
import os

LOGGER = logging.getLogger(__name__)
SLOTS = ("control", "embedding", "rerank", "generation")


class UsageRecorder:
    def __init__(self, dsn: str | None = None, *, provider_id: int | None = None, sink=None):
        self.dsn = dsn
        self.provider_id = provider_id
        self.sink = sink

    @classmethod
    def from_environment(cls, *, role: str = "ai"):
        dsn = os.environ.get("V2_USAGE_DSN") or os.environ.get("V2_AI_DSN" if role == "ai" else "V2_INGEST_DSN")
        raw = os.environ.get("V2_USAGE_PROVIDER_ID")
        try:
            provider_id = int(raw) if raw else None
        except ValueError:
            provider_id = None
        return cls(dsn, provider_id=provider_id) if dsn else None

    def _insert(self, row: dict) -> None:
        import psycopg
        with psycopg.connect(self.dsn, connect_timeout=3) as conn:
            conn.execute("""INSERT INTO usage_log(tenant_id,provider_id,slot,model,input_tokens,output_tokens,http_status,error_code)
                VALUES(%s,%s,%s,%s,%s,%s,%s,%s)""", (row["tenant_id"], row["provider_id"], row["slot"], row["model"],
                row["input_tokens"], row["output_tokens"], row["http_status"], row["error_code"]))
            conn.commit()

    def record(self, *, tenant_id: int | None, provider_id: int | None = None, slot: str, model: str,
               input_tokens: int | None = None, output_tokens: int | None = None,
               http_status: int | None = None, error_code: str | None = None) -> bool:
        if slot not in SLOTS or not model:
            return False
        row = {"tenant_id": tenant_id, "provider_id": provider_id if provider_id is not None else self.provider_id,
               "slot": slot, "model": model,
               "input_tokens": None if input_tokens is None else int(input_tokens),
               "output_tokens": None if output_tokens is None else int(output_tokens),
               "http_status": http_status, "error_code": error_code}
        try:
            if self.sink is not None:
                self.sink(row)
            elif self.dsn:
                self._insert(row)
        except Exception:  # noqa: BLE001 - usage must never break a business call
            LOGGER.warning("usage_log write failed; business call continues")
            return False
        return True


def aggregate(dsn: str, tenant_id: int | None, *, since=None, until=None, slot: str | None = None) -> dict:
    import psycopg
    from psycopg.rows import dict_row
    where, params = ["tenant_id IS NOT DISTINCT FROM %s"], [tenant_id]
    if since:
        where.append("created_at >= %s"); params.append(since)
    if until:
        where.append("created_at < %s"); params.append(until)
    if slot:
        where.append("slot = %s"); params.append(slot)
    with psycopg.connect(dsn, connect_timeout=3, row_factory=dict_row) as conn:
        rows = conn.execute(f"SELECT slot,model,coalesce(sum(input_tokens),0) input_tokens,"
            f"coalesce(sum(output_tokens),0) output_tokens,count(*) calls,"
            f"count(*) FILTER (WHERE error_code IS NOT NULL) failed_calls FROM usage_log "
            f"WHERE {' AND '.join(where)} GROUP BY slot,model ORDER BY slot,model", params).fetchall()
    return {"input_tokens": sum(int(r["input_tokens"] or 0) for r in rows),
            "output_tokens": sum(int(r["output_tokens"] or 0) for r in rows),
            "calls": sum(int(r["calls"] or 0) for r in rows),
            "failed_calls": sum(int(r["failed_calls"] or 0) for r in rows),
            "by_slot": rows, "by_model": rows}
