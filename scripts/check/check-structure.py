#!/usr/bin/env python3
"""Enforce two structural anti-regression rules.

A. Handler SQL ratchet: INSERT/UPDATE/DELETE statements in gateway/customer
   non-store files may only decrease. The current count is the baseline; moving
   SQL into a store file lowers it and the NOTE asks for a baseline update.
B. Dependency and frontend boundary:
   - gateway/customer may not import admin except for the two sanctioned
     composition/persistence points already present (baseline below);
   - admin may not import gateway or customer;
   - frontend/src may not contain direct Python gRPC ports or database DSNs.

The archive's P-D table does not define the B criteria, so this script only
implements the three named checks and deliberately adds no line-count rules.
"""
from __future__ import annotations

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
GATEWAY = ROOT / "go" / "internal" / "gateway"
CUSTOMER = ROOT / "go" / "internal" / "customer"
ADMIN = ROOT / "go" / "internal" / "admin"
FRONTEND = ROOT / "frontend" / "src"

SQL = re.compile(r"(?i)\b(?:insert\s+into|update\s+[a-z_][a-z0-9_]*|delete\s+from)\b")
IMPORT = re.compile(r'"github\.com/ggb5023/EcommerceRAG/go/internal/(admin|gateway|customer)"')
FRONTEND_TRACE = re.compile(r"(?i)(?:v2_[a-z_]*(?:dsn|addr)|postgres(?:ql)?://|rag_v2_|:(?:50052|50053)\b)")

# Measured when the guard was introduced. Lower it when handler SQL drops.
HANDLER_SQL_BASELINE = 29
# Existing sanctioned gateway -> admin dependencies: route composition and the
# decision store call inside the terminal transaction. New files must not join.
GATEWAY_ADMIN_BASELINE = {
    "go/internal/gateway/routes_admin.go",
    "go/internal/gateway/turn_outcome.go",
}


def go_files(root: Path) -> list[Path]:
    return sorted(path for path in root.rglob("*.go") if not path.name.endswith("_test.go"))


def handler_sql() -> tuple[int, list[str]]:
    total = 0
    details: list[str] = []
    for root in (GATEWAY, CUSTOMER):
        if not root.is_dir():
            continue
        for path in go_files(root):
            if "store" in path.name.lower():
                continue
            found = len(SQL.findall(path.read_text(encoding="utf-8")))
            if found:
                total += found
                details.append(f"{path.relative_to(ROOT)}={found}")
    return total, details


def imports() -> tuple[list[str], set[str]]:
    problems: list[str] = []
    seen_gateway_admin: set[str] = set()
    for path in go_files(GATEWAY) + go_files(CUSTOMER):
        names = IMPORT.findall(path.read_text(encoding="utf-8"))
        if "admin" not in names:
            continue
        relative = path.relative_to(ROOT).as_posix()
        if path.parent == GATEWAY and relative in GATEWAY_ADMIN_BASELINE:
            seen_gateway_admin.add(relative)
            continue
        problems.append(f"{relative} imports internal/admin outside the allowed baseline")
    for path in go_files(ADMIN):
        names = IMPORT.findall(path.read_text(encoding="utf-8"))
        for name in names:
            if name in {"gateway", "customer"}:
                problems.append(f"{path.relative_to(ROOT)} imports internal/{name} (reverse dependency)")
    return problems, seen_gateway_admin


def frontend_traces() -> list[str]:
    problems: list[str] = []
    if not FRONTEND.is_dir():
        return problems
    for path in sorted(FRONTEND.rglob("*")):
        if not path.is_file() or path.suffix not in {".ts", ".vue", ".js"}:
            continue
        match = FRONTEND_TRACE.search(path.read_text(encoding="utf-8"))
        if match:
            problems.append(f"{path.relative_to(ROOT)} contains direct Python/database trace {match.group(0)!r}")
    return problems


def main() -> int:
    if not GATEWAY.is_dir() or not CUSTOMER.is_dir():
        print("SKIP structure: gateway or customer package missing")
        return 0
    problems: list[str] = []

    sql_total, sql_details = handler_sql()
    if sql_total > HANDLER_SQL_BASELINE:
        problems.append(
            f"handler SQL rose to {sql_total} (baseline {HANDLER_SQL_BASELINE}): " + ", ".join(sql_details))
    elif sql_total < HANDLER_SQL_BASELINE:
        print(f"NOTE handler SQL dropped to {sql_total}; lower HANDLER_SQL_BASELINE from {HANDLER_SQL_BASELINE}")

    import_problems, seen_base = imports()
    problems.extend(import_problems)
    for stale in sorted(GATEWAY_ADMIN_BASELINE - seen_base):
        print(f"NOTE {stale} no longer imports internal/admin; remove it from GATEWAY_ADMIN_BASELINE")

    problems.extend(frontend_traces())

    if problems:
        for problem in problems[:40]:
            print("FAIL structure: " + problem)
        print(f"FAIL structure: {len(problems)} problem(s)")
        return 1
    print(f"PASS structure: handler SQL {sql_total}/{HANDLER_SQL_BASELINE}, "
          f"gateway->admin {len(seen_base)}/{len(GATEWAY_ADMIN_BASELINE)} allowed files, "
          "reverse imports 0, frontend direct traces 0")
    return 0


if __name__ == "__main__":
    sys.exit(main())
