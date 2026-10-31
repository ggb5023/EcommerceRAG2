#!/usr/bin/env python3
"""Every table created by a migration must be granted to a runtime role.

Migration 0039 created turn_decision without a GRANT: the gateway's insert was
rejected with "permission denied for table turn_decision" and every smoke test
still looked green. This guard is that incident's product.
"""
from __future__ import annotations

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
MIGRATIONS = ROOT / "sql" / "migrations"
# Tables the database itself creates (or that need no runtime role) go here.
ALLOWLIST: frozenset[str] = frozenset()

CREATE = re.compile(r"CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z_][a-z0-9_]*)", re.I)
# GRANT ... ON a,b,c TO role — the target list can hold several tables.
GRANT_TABLE = re.compile(r"GRANT\s+[^;]*?\bON\s+([a-z_,\s]+?)\s+TO\b", re.I)
GRANT_ALL = re.compile(r"GRANT\s+[^;]*?\bON\s+ALL\s+TABLES\s+IN\s+SCHEMA", re.I)


def main() -> int:
    if not MIGRATIONS.is_dir():
        print("SKIP migration grants: no migrations directory")
        return 0
    files = sorted(MIGRATIONS.glob("*.sql"))
    created: dict[str, str] = {}
    granted: set[str] = set()
    grant_all_from: str | None = None
    for path in files:
        text = path.read_text(encoding="utf-8")
        for name in CREATE.findall(text):
            created.setdefault(name.lower(), path.name)
        for group in GRANT_TABLE.findall(text):
            for name in group.split(","):
                if name.strip():
                    granted.add(name.strip().lower())
        if grant_all_from is None and GRANT_ALL.search(text):
            grant_all_from = path.name
    problems = []
    for table, source in sorted(created.items()):
        if table in ALLOWLIST or table in granted:
            continue
        if grant_all_from is not None and grant_all_from >= source:
            continue
        problems.append(f"{table} (created in {source}) has no GRANT")
    if problems:
        for problem in problems:
            print(f"FAIL {problem}")
        print(f"FAIL migration grants: {len(problems)} table(s) without a grant")
        return 1
    print(f"PASS migration grants: {len(created)} table(s) checked")
    return 0


if __name__ == "__main__":
    sys.exit(main())
