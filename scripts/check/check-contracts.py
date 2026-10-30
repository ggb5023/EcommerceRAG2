#!/usr/bin/env python3
"""Ratchet the cross-process contract table.

A row counts as covered only when both sides name a test. Losing coverage fails
the gate; an uncovered row is reported as a warning, never as a failure.
"""
from __future__ import annotations

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
TABLE = ROOT / "docs" / "contracts.md"
# Two ratchets, both measured when the table was introduced. Only raise them.
CONTRACTS_CROSS = 5
CONTRACTS_SAME = 1
ROW = re.compile(r"^\|(?P<cells>.+)\|\s*$")


def cells(line: str) -> list[str]:
    return [cell.strip() for cell in line.strip().strip("|").split("|")]


def main() -> int:
    if not TABLE.is_file():
        print("SKIP contracts: no docs/contracts.md")
        return 0
    covered = {"跨进程": 0, "同侧": 0}
    totals = {"跨进程": 0, "同侧": 0}
    warnings = []
    for line in TABLE.read_text(encoding="utf-8").splitlines():
        if not line.startswith("|") or set(line) <= {"|", "-", " "}:
            continue
        row = cells(line)
        if len(row) < 6 or row[0] == "契约":
            continue
        kind, state = row[1], row[5]
        if kind not in covered:
            warnings.append(f"{row[0]}: unknown type {kind!r}")
            continue
        totals[kind] += 1
        if state.startswith("✅"):
            covered[kind] += 1
        elif state.startswith("⚠️"):
            warnings.append(f"{row[0]}: {state}")
    for warning in warnings:
        print(f"WARN contract {warning}")
    failed = False
    for kind, ratchet in (("跨进程", CONTRACTS_CROSS), ("同侧", CONTRACTS_SAME)):
        if covered[kind] < ratchet:
            print(f"FAIL contracts: {kind} {covered[kind]}/{ratchet} covered")
            failed = True
        elif covered[kind] > ratchet:
            label = "CONTRACTS_CROSS" if kind == "跨进程" else "CONTRACTS_SAME"
            print(f"NOTE contracts: {kind} rose to {covered[kind]}; raise {label}")
    if failed:
        return 1
    print(f"PASS contracts: cross {covered['跨进程']}/{totals['跨进程']} covered "
          f"(ratchet {CONTRACTS_CROSS}), same {covered['同侧']}/{totals['同侧']} covered "
          f"(ratchet {CONTRACTS_SAME}), warnings {len(warnings)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
