#!/usr/bin/env python3
"""Apply immutable migrations to an explicitly configured V2 database."""
from __future__ import annotations

import argparse
import hashlib
import os
import stat
from pathlib import Path

import psycopg


def load_environment(path: Path) -> None:
    if stat.S_IMODE(path.stat().st_mode) & 0o077:
        raise ValueError("runtime configuration must have mode 0600")
    for raw in path.read_text().splitlines():
        if raw.strip() and not raw.lstrip().startswith("#"):
            key, value = raw.split("=", 1)
            if not key.startswith("V2_"):
                raise ValueError("unexpected runtime configuration key")
            os.environ[key] = value.strip().strip("\"'")


def migrate() -> None:
    directory = os.environ.get("V2_MIGRATION_DIR")
    root = Path(directory) if directory else Path(__file__).resolve().parents[2] / "sql/migrations"
    files = sorted(root.glob("[0-9][0-9][0-9][0-9]_*.sql"))
    versions = [p.name[:4] for p in files]
    if versions != [f"{i:04}" for i in range(1, len(files) + 1)]:
        raise ValueError("migration sequence is incomplete")
    with psycopg.connect(os.environ["V2_MIGRATION_DSN"], autocommit=True) as conn:
        database, superuser, bypass_rls = conn.execute(
            "SELECT current_database(),rolsuper,rolbypassrls FROM pg_roles WHERE rolname=current_user"
        ).fetchone()
        if "v2" not in database:
            raise ValueError("migration requires a dedicated V2 database")
        if superuser or bypass_rls:
            raise ValueError("migration requires a non-superuser without bypass RLS")
        conn.execute("SELECT pg_advisory_lock(742019)")
        exists = conn.execute("SELECT to_regclass('schema_migration') IS NOT NULL").fetchone()[0]
        recorded = {}
        if exists:
            rows = conn.execute("SELECT version,checksum_sha256 FROM schema_migration").fetchall()
            recorded = dict(rows)
            if set(recorded) - set(versions):
                raise ValueError("unknown migration version")
        else:
            tables = conn.execute("SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_type='BASE TABLE'").fetchone()[0]
            if tables:
                raise ValueError("refusing an unversioned nonempty database")
        newly_applied = []
        for path in files:
            version = path.name[:4]
            checksum = hashlib.sha256(path.read_bytes()).hexdigest()
            if version in recorded:
                if recorded[version] != checksum:
                    raise ValueError(f"migration checksum mismatch: {version}")
                continue
            script = path.read_text().replace(":'migration_checksum'", "'" + checksum + "'")
            conn.execute(script)
            newly_applied.append((version, checksum))
            if version == "0004":
                # Only bytes applied by this invocation can establish legacy
                # checksums. Never certify an existing unverified migration.
                for applied_version, applied_checksum in newly_applied:
                    conn.execute("UPDATE schema_migration SET checksum_sha256=%s WHERE version=%s AND checksum_sha256 IS NULL", (applied_checksum, applied_version))
            print(f"PASS migration {version}")
        conn.execute("SELECT pg_advisory_unlock(742019)")
        print(f"PASS {len(files)} migration checksums")


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--env", type=Path)
    args = parser.parse_args()
    try:
        if args.env:
            load_environment(args.env)
        migrate()
    except (psycopg.Error, OSError, KeyError, ValueError) as error:
        # psycopg connection errors can contain host/user/DSN information.
        code = getattr(error, "sqlstate", None) or type(error).__name__
        raise SystemExit(f"FAIL V2 migration ({code})") from None
