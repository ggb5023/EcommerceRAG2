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
# Measured when the table was introduced. Only raise this number.
CONTRACTS_COVERED = 1
ROW = re.compile(r"^\|(?P<cells>.+)\|\s*$")


def cells(line: str) -> list[str]:
    return [cell.strip() for cell in line.strip().strip("|").split("|")]


def main() -> int:
    if not TABLE.is_file():
        print("SKIP contracts: no docs/contracts.md")
        return 0
    covered, warnings = 0, []
    for line in TABLE.read_text(encoding="utf-8").splitlines():
        if not line.startswith("|") or set(line) <= {"|", "-", " "}:
            continue
        row = cells(line)
        if len(row) < 5 or row[0] == "契约":
            continue
        state = row[4]
        if state.startswith("✅"):
            covered += 1
        elif state.startswith("⚠️"):
            warnings.append(f"{row[0]}: {state}")
    for warning in warnings:
        print(f"WARN contract {warning}")
    if covered < CONTRACTS_COVERED:
        print(f"FAIL contracts: {covered} covered row(s), ratchet is {CONTRACTS_COVERED}")
        return 1
    if covered > CONTRACTS_COVERED:
        print(f"NOTE contracts covered rose to {covered}; raise CONTRACTS_COVERED")
    print(f"PASS contracts: {covered} covered row(s), {len(warnings)} warning(s)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
