"""Fail-closed startup checks for the independent V2 service processes."""
from __future__ import annotations

import hashlib
import os
from decimal import Decimal, InvalidOperation
from pathlib import Path

import psycopg
from psycopg.conninfo import conninfo_to_dict


def verify_database(dsn: str, expected_role: str) -> None:
    if not dsn:
        raise ValueError("explicit service database configuration is required")
    with psycopg.connect(dsn) as conn:
        role, superuser, bypassrls = conn.execute(
            "SELECT current_user,rolsuper,rolbypassrls FROM pg_roles WHERE rolname=current_user"
        ).fetchone()
        if superuser or bypassrls or role != expected_role:
            raise ValueError("database service role does not match its ownership")
        recorded = dict(conn.execute("SELECT version,checksum_sha256 FROM schema_migration").fetchall())
    directory = Path(os.environ.get('V2_MIGRATION_DIR', str(Path(__file__).resolve().parents[2] / 'sql/migrations')))
    if not directory.is_absolute() or not directory.is_dir():
        raise ValueError('database migration directory is unavailable')
    files = sorted(directory.glob("[0-9][0-9][0-9][0-9]_*.sql"))
    if not files:
        raise ValueError('database migration files are missing')
    expected = {p.name[:4]: hashlib.sha256(p.read_bytes()).hexdigest() for p in files}
    if recorded != expected:
        raise ValueError("database migration checksums do not match this build")


def verify_ai_configuration() -> None:
    if os.environ.get("V2_FIXTURE_MODE") == "1":
        if os.environ.get("APP_ENV") != "test":
            raise ValueError("fixture mode requires APP_ENV=test")
        return
    require_online_budget()
    retrieve = os.environ.get("V2_RETRIEVER_DSN", "")
    checkpoint = os.environ.get("V2_CHECKPOINT_DSN", "")
    if not retrieve or not checkpoint:
        raise ValueError("retrieval and checkpoint databases are required")
    keys = ("host", "port", "dbname", "user")
    a, b = conninfo_to_dict(retrieve), conninfo_to_dict(checkpoint)
    if any(a.get(k) != b.get(k) for k in keys):
        raise ValueError("AI databases must use the same service role and database")
    if os.environ.get("V2_INGEST_DSN"):
        raise ValueError("AI process must not receive ingest credentials")
    verify_database(retrieve, "rag_v2_ai")


def verify_merged_configuration() -> None:
    """Fail-closed checks for the single ai process that owns both roles.

    The merged process keeps one database and two service roles: retrieval and
    checkpoints write as ``rag_v2_ai``, ingestion RPC and the worker write as
    ``rag_v2_ingest``.  Sharing a process must not merge the two writers, so a
    missing pair, a shared role or a split database is rejected before any
    socket is bound.
    """
    if os.environ.get("V2_FIXTURE_MODE") == "1":
        if os.environ.get("APP_ENV") != "test":
            raise ValueError("fixture mode requires APP_ENV=test")
        return
    require_online_budget()
    retrieve = os.environ.get("V2_RETRIEVER_DSN", "")
    checkpoint = os.environ.get("V2_CHECKPOINT_DSN", "")
    ingest = os.environ.get("V2_INGEST_DSN", "")
    if not retrieve or not checkpoint or not ingest:
        raise ValueError("retrieval, checkpoint and ingest databases are required")
    ai_pair = ("host", "port", "dbname", "user")
    a, b, c = conninfo_to_dict(retrieve), conninfo_to_dict(checkpoint), conninfo_to_dict(ingest)
    if any(a.get(k) != b.get(k) for k in ai_pair):
        raise ValueError("AI databases must use the same service role and database")
    if any(a.get(k) != c.get(k) for k in ("host", "port", "dbname")):
        raise ValueError("ai and ingest must share one database")
    if a.get("user") == c.get("user"):
        raise ValueError("ai and ingest must use distinct service roles")
    verify_database(retrieve, "rag_v2_ai")
    verify_database(ingest, "rag_v2_ingest")


def require_online_budget() -> None:
    """Require an explicit positive budget before any online V2 provider loads."""
    if os.environ.get("RAG_PROFILE") != "v2_langchain_bailian":
        return
    raw = os.environ.get("V2_ONLINE_BUDGET_CNY", "").strip()
    try:
        budget = Decimal(raw)
    except (InvalidOperation, ValueError):
        raise ValueError("online profile requires a positive CNY budget") from None
    if not budget.is_finite() or budget <= 0:
        raise ValueError("online profile requires a positive CNY budget")
    from app.providers.budget import BudgetLedger
    BudgetLedger.from_environment()
