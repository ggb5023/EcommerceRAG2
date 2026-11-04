#!/usr/bin/env python3
"""Check the cheap, static numbers that documentation must not drift from.

Deliberately excluded: domain coverage. It is slow and changes with the test
suite; `go test ./internal/domain/ -cover` is the only source for that value.
"""
from __future__ import annotations

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]


def read(relative: str) -> str:
    return (ROOT / relative).read_text(encoding="utf-8")


def one(pattern: str, text: str, label: str) -> int:
    values = {int(value) for value in re.findall(pattern, text)}
    if len(values) != 1:
        raise SystemExit(f"FAIL doc numbers: {label} must have one value, got {sorted(values)}")
    return values.pop()


def main() -> int:
    migrations = len(list((ROOT / "sql" / "migrations").glob("*.sql")))
    decisions = len(list((ROOT / "docs" / "decisions").glob("[0-9][0-9][0-9][0-9]-*.md")))
    fast = len(re.findall(r"(?m)^\s*run(?:_optional)? fast\b", read("scripts/check.sh")))
    agent = read("docs/agent-entry.md")
    documented_migrations = one(r"(\d+)\s*条迁移", agent, "agent-entry migrations")
    documented_fast = one(r"快速层\s*(\d+)\s*项", agent, "agent-entry fast items")
    documented_decisions = one(r"决策一文件一编号.*?✅\s*(\d+)\s*份", read("docs/constraints.md"),
                               "constraints decisions")
    ownership_script = one(r"UNDECLARED_BUDGET\s*=\s*(\d+)",
                           read("scripts/check/check-ownership.py"), "ownership budget")
    ownership_doc = one(r"未声明预算\s*(\d+)", read("docs/data-ownership.md"), "ownership doc budget")
    expected = {
        "migrations": (documented_migrations, migrations),
        "fast": (documented_fast, fast),
        "decisions": (documented_decisions, decisions),
        "ownership": (ownership_doc, ownership_script),
    }
    failures = [f"{name}: documented={doc} actual={actual}"
                for name, (doc, actual) in expected.items() if doc != actual]
    if failures:
        for failure in failures:
            print("FAIL doc numbers: " + failure)
        return 1
    print(f"PASS doc numbers: migrations={migrations} fast={fast} decisions={decisions} "
          f"ownership_budget={ownership_script}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
