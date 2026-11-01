#!/usr/bin/env python3
"""Detect a silently broken development database.

The gateway's development identity fails when the database was recreated without
running migrations. This probe compares schema_migration rows with the migration
files and checks the document/document_version consistency described in
docs/development.md.

Exit codes: 0 PASS, 1 FAIL, 2 UNCONFIGURED (no DSN, missing env file, or the
database is unreachable).
"""
from __future__ import annotations

import argparse
import os
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
DEFAULT_MIGRATIONS = ROOT / "sql" / "migrations"


def env_dsn(path: Path) -> str:
    if not path.is_file():
        return ""
    for line in path.read_text(encoding="utf-8").splitlines():
        if line.startswith("V2_GATEWAY_DSN="):
            return line.split("=", 1)[1].strip().strip("\"'")
    return ""


def psql(dsn: str, sql: str) -> tuple[int, str, str]:
    result = subprocess.run(
        ["psql", dsn, "-X", "-At", "-F", "|", "-v", "ON_ERROR_STOP=1", "-c", sql],
        capture_output=True, text=True)
    return result.returncode, result.stdout.strip(), result.stderr.strip()


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--env", type=Path)
    parser.add_argument("--dsn", default="")
    parser.add_argument("--migrations-dir", type=Path, default=DEFAULT_MIGRATIONS)
    args = parser.parse_args()

    dsn = args.dsn
    if not dsn and args.env is not None:
        if not args.env.is_file():
            print(f"UNCONFIGURED environment file is missing: {args.env}")
            return 2
        dsn = env_dsn(args.env)
    if not dsn:
        dsn = os.environ.get("V2_GATEWAY_DSN", "").strip()
    if not dsn:
        print("UNCONFIGURED V2_GATEWAY_DSN is not configured (use --env or --dsn)")
        return 2

    files = sorted(args.migrations_dir.glob("[0-9][0-9][0-9][0-9]_*.sql"))
    if not files:
        print(f"FAIL no migration files under {args.migrations_dir}")
        return 1

    sql = ("SELECT (SELECT count(*) FROM schema_migration),"
           " (SELECT count(*) FROM document),"
           " (SELECT count(*) FROM document_version)")
    status, stdout, stderr = psql(dsn, sql)
    if status == 2 or "could not connect" in stderr.lower() or "connection to server" in stderr.lower():
        print("UNCONFIGURED database is unreachable")
        return 2
    if status != 0:
        # Reachable but the probe failed: this is the silent-failure shape.
        print("FAIL database probe failed (schema_migration/document tables are missing or unreadable)")
        return 1
    try:
        migrations, documents, versions = (int(value) for value in stdout.split("|"))
    except ValueError:
        print("FAIL database probe returned an unexpected result")
        return 1
    if migrations != len(files):
        print(f"FAIL schema_migration has {migrations} rows, expected {len(files)} migration files")
        return 1
    if documents > 0 and versions == 0:
        print(f"FAIL document has {documents} rows but document_version has 0 rows")
        return 1
    print(f"PASS db env health: migrations {migrations}/{len(files)}, documents {documents}, versions {versions}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
