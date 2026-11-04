#!/usr/bin/env python3
"""Enforce the logging contract: no new bare Go log.Printf or Python print.

The five gateway log.Printf calls are the measured pre-observability baseline.
The CLI module keeps its intentional stdout output; any other print in app/ is
new code and must use the JSON logger instead.
"""
from __future__ import annotations

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
GO_ALLOWED = {
    'log.Printf("Gateway recovered %d interrupted executions", recovered)',
    'log.Printf("V2 Gateway listening on %s", runtime.addr)',
    'log.Printf("SETUP_TOKEN=%s", localToken)',
    'log.Printf("finish transaction unavailable")',
    'log.Printf("finish execution transaction failed")',
}
GO_ALLOWED = {re.sub(r"\s+", " ", value).strip() for value in GO_ALLOWED}
PYTHON_PRINT_ALLOWED = {"python/app/ingest/cli.py"}


def go_problems() -> list[str]:
    problems = []
    for path in sorted((ROOT / "go" / "internal" / "gateway").rglob("*.go")):
        if path.name.endswith("_test.go"):
            continue
        for line_no, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
            if "log.Printf(" not in line:
                continue
            normalized = re.sub(r"\s+", " ", line).strip()
            if normalized not in GO_ALLOWED:
                problems.append(f"{path.relative_to(ROOT)}:{line_no}: bare log.Printf")
    return problems


def python_problems() -> list[str]:
    problems = []
    print_pattern = re.compile(r"(?<![A-Za-z0-9_])print\s*\(")
    for path in sorted((ROOT / "python" / "app").rglob("*.py")):
        relative = path.relative_to(ROOT).as_posix()
        if relative in PYTHON_PRINT_ALLOWED:
            continue
        for line_no, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
            if print_pattern.search(line):
                problems.append(f"{relative}:{line_no}: print() in app/")
    return problems


def main() -> int:
    problems = go_problems() + python_problems()
    if problems:
        for problem in problems:
            print("FAIL " + problem)
        return 1
    print("PASS logs: gateway log.Printf baseline 5, app print allowlist 1")
    return 0


if __name__ == "__main__":
    sys.exit(main())
