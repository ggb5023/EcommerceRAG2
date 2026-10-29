#!/usr/bin/env python3
"""Enforce the P-B persistence boundary for the admin package.

Handlers must hold no SQL; the store files must carry at least the number of
statements the handlers held before the migration (75). The store baseline is a
ratchet: statements may move in, never disappear.
"""
from __future__ import annotations

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
ADMIN = ROOT / "go" / "internal" / "admin"

HANDLER_FILES = (
    "admin.go",
    "imports.go",
    "import.go",
    "review.go",
    "version.go",
    "staff.go",
    "usage.go",
    "shop_settings.go",
    "commands.go",
    "review_suggestion_handlers.go",
)
STORE_GLOB = "store*.go"
# Measured before P-B: 24+22+8+6+5+3+3+2+1+1. Only raise this number.
STORE_SQL_BASELINE = 75
# Per-file allowance while the migration is in flight. Every P-B commit lowers its
# own entry; the target is all zeros. An entry may never grow.
HANDLER_SQL_ALLOWANCE = {
    "admin.go": 0,
    "imports.go": 21,
    "import.go": 8,
    "review_suggestion_handlers.go": 6,
    "review.go": 5,
    "staff.go": 3,
    "commands.go": 3,
    "version.go": 2,
    "usage.go": 1,
    "shop_settings.go": 1,
}
SQL = re.compile(r"\b(?:SELECT|INSERT INTO|UPDATE|DELETE FROM)\b")


def count(path: Path) -> int:
    return len(SQL.findall(path.read_text(encoding="utf-8")))


def main() -> int:
    if not ADMIN.is_dir():
        print("SKIP admin boundaries: no admin package")
        return 0

    problems: list[str] = []
    handlers = [ADMIN / name for name in HANDLER_FILES if (ADMIN / name).is_file()]
    if not handlers:
        print("SKIP admin boundaries: no handler files yet")
        return 0
    for path in handlers:
        found = count(path)
        allowed = HANDLER_SQL_ALLOWANCE.get(path.name, 0)
        if found > allowed:
            problems.append(f"{path.relative_to(ROOT)}: {found} SQL statement(s) in a handler, allowance is {allowed}")
        elif found < allowed:
            print(f"NOTE {path.name} dropped to {found}; lower its allowance from {allowed}")

    stores = sorted(p for p in ADMIN.glob(STORE_GLOB) if not p.name.endswith("_test.go"))
    store_sql = sum(count(path) for path in stores)
    handler_sql = sum(count(path) for path in handlers)
    # Nothing may be lost while statements move: the package total stays at the
    # pre-migration count. The store floor only applies once the handlers are empty.
    if handler_sql + store_sql < STORE_SQL_BASELINE:
        problems.append(f"admin package holds {handler_sql + store_sql} SQL statement(s), baseline is {STORE_SQL_BASELINE}")
    if handler_sql == 0 and store_sql < STORE_SQL_BASELINE:
        problems.append(f"store files hold {store_sql} SQL statement(s), baseline is {STORE_SQL_BASELINE}")

    if problems:
        for problem in problems:
            print(f"FAIL {problem}")
        print(f"FAIL admin boundaries: {len(problems)} violation(s)")
        return 1
    remaining = handler_sql
    print(f"PASS admin boundaries: handlers {remaining} SQL (allowance "
          f"{sum(HANDLER_SQL_ALLOWANCE.values())}), stores {store_sql}/{STORE_SQL_BASELINE} SQL")
    return 0


if __name__ == "__main__":
    sys.exit(main())
