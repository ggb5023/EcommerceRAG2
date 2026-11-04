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
    created: dict[str, tuple[int, int, str]] = {}
    granted: set[str] = set()
    grant_all_positions: list[tuple[int, int]] = []
    for file_index, path in enumerate(files):
        text = path.read_text(encoding="utf-8")
        for match in CREATE.finditer(text):
            created.setdefault(match.group(1).lower(), (file_index, match.start(), path.name))
        for group in GRANT_TABLE.findall(text):
            for name in group.split(","):
                if name.strip():
                    granted.add(name.strip().lower())
        for match in GRANT_ALL.finditer(text):
            grant_all_positions.append((file_index, match.start()))
    problems = []
    for table, (file_index, offset, source) in sorted(created.items()):
        if table in ALLOWLIST or table in granted:
            continue
        # GRANT ... ON ALL TABLES covers only tables that already existed when
        # the statement ran. A CREATE TABLE after the grant in the same file
        # still needs its own explicit GRANT.
        if any((grant_file, grant_offset) > (file_index, offset)
               for grant_file, grant_offset in grant_all_positions):
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
