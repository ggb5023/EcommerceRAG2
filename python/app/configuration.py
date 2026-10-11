"""Fail-closed startup checks for the independent V2 service processes."""
from __future__ import annotations

import hashlib
import os
from pathlib import Path

import psycopg
from psycopg.conninfo import conninfo_to_dict


def verify_database(dsn: str, expected_role: str) -> None:
    if not dsn:
        raise ValueError("explicit service database configuration is required")
    with psycopg.connect(dsn) as conn:
        role, superuser = conn.execute("SELECT current_user,rolsuper FROM pg_roles WHERE rolname=current_user").fetchone()
        if superuser or role != expected_role:
            raise ValueError("database service role does not match its ownership")
        recorded = dict(conn.execute("SELECT version,checksum_sha256 FROM schema_migration").fetchall())
    files = sorted((Path(__file__).resolve().parents[2] / "sql/migrations").glob("[0-9][0-9][0-9][0-9]_*.sql"))
    expected = {p.name[:4]: hashlib.sha256(p.read_bytes()).hexdigest() for p in files}
    if recorded != expected:
        raise ValueError("database migration checksums do not match this build")


def verify_ai_configuration() -> None:
    if os.environ.get("V2_FIXTURE_MODE") == "1":
        if os.environ.get("APP_ENV") != "test":
            raise ValueError("fixture mode requires APP_ENV=test")
        return
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
