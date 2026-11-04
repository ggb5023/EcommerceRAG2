"""Best-effort token usage recording. No currency or balance is stored."""
from __future__ import annotations

import logging
import os
from collections.abc import Mapping
from contextlib import contextmanager
from contextvars import ContextVar
from dataclasses import dataclass

from app import metrics

LOGGER = logging.getLogger(__name__)
SLOTS = ("control", "embedding", "rerank", "generation")


@dataclass(frozen=True)
class UsageAttribution:
    request_id: str | None = None
    execution_id: int | None = None
    turn_id: int | None = None


_CURRENT: ContextVar[UsageAttribution | None] = ContextVar("usage_attribution", default=None)


def _numeric_id(value: object) -> int | None:
    if isinstance(value, bool) or value is None:
        return None
    if isinstance(value, int):
        return value
    text = str(value).strip()
    if not text.isdigit():
        return None
    return int(text)


def attribution_from_scope(scope: Mapping[str, object]) -> UsageAttribution:
    """Map a signed Go scope to the optional usage_log attribution columns.

    Staff turns carry numeric execution/turn ids. Customer execution ids are
    text, so only request_id is attributable for that channel.
    """
    request_id = str(scope.get("request_id") or "").strip() or None
    return UsageAttribution(
        request_id=request_id,
        execution_id=_numeric_id(scope.get("execution_id")),
        turn_id=_numeric_id(scope.get("turn_id")),
    )


@contextmanager
def use_attribution(attribution: UsageAttribution | None):
    token = _CURRENT.set(attribution)
    try:
        yield
    finally:
        _CURRENT.reset(token)


class UsageRecorder:
    def __init__(self, dsn: str | None = None, *, provider_id: int | None = None, sink=None):
        self.dsn = dsn
        self.provider_id = provider_id
        self.sink = sink

    @classmethod
    def from_environment(cls, *, role: str = "ai"):
        if role == "ai":
            dsn = (os.environ.get("V2_USAGE_DSN") or os.environ.get("V2_AI_DSN")
                   or os.environ.get("V2_RETRIEVER_DSN"))
        else:
            dsn = os.environ.get("V2_USAGE_DSN") or os.environ.get("V2_INGEST_DSN")
        raw = os.environ.get("V2_USAGE_PROVIDER_ID")
        try:
            provider_id = int(raw) if raw else None
        except ValueError:
            provider_id = None
        return cls(dsn, provider_id=provider_id) if dsn else None

    def _insert(self, row: dict) -> None:
        import psycopg
        with psycopg.connect(self.dsn, connect_timeout=3) as conn:
            conn.execute("""INSERT INTO usage_log(tenant_id,provider_id,slot,model,input_tokens,output_tokens,http_status,error_code,
                request_id,execution_id,turn_id) VALUES(%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s)""",
                (row["tenant_id"], row["provider_id"], row["slot"], row["model"],
                 row["input_tokens"], row["output_tokens"], row["http_status"], row["error_code"],
                 row["request_id"], row["execution_id"], row["turn_id"]))
            conn.commit()

    def record(self, *, tenant_id: int | None, provider_id: int | None = None, slot: str, model: str,
               input_tokens: int | None = None, output_tokens: int | None = None,
               http_status: int | None = None, error_code: str | None = None,
               request_id: str | None = None, execution_id: int | None = None,
               turn_id: int | None = None) -> bool:
        if slot not in SLOTS or not model:
            return False
        current = _CURRENT.get()
        if current is not None:
            if request_id is None:
                request_id = current.request_id
            if execution_id is None:
                execution_id = current.execution_id
            if turn_id is None:
                turn_id = current.turn_id
        row = {"tenant_id": tenant_id, "provider_id": provider_id if provider_id is not None else self.provider_id,
               "slot": slot, "model": model,
               "input_tokens": None if input_tokens is None else int(input_tokens),
               "output_tokens": None if output_tokens is None else int(output_tokens),
               "http_status": http_status, "error_code": error_code,
               "request_id": request_id, "execution_id": execution_id, "turn_id": turn_id}
        try:
            if self.sink is not None:
                self.sink(row)
            elif self.dsn:
                self._insert(row)
        except Exception:  # noqa: BLE001 - usage must never break a business call
            metrics.usage_log_write_failure()
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
