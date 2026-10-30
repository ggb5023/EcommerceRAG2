#!/usr/bin/env python3
"""Fail when a running service is completely silent.

A service that starts but writes nothing is indistinguishable from one that is
wedged; the stage-budget class of regressions shows up here first.
"""
from __future__ import annotations

import os
import sys
from pathlib import Path

LOG_DIR = Path(os.environ.get("ECR_SERVICE_LOG_DIR", "/tmp/ecr-dev-logs"))
REQUIRED = ("gateway", "ai")
OPTIONAL = ("ingest",)


def rows(path: Path) -> int:
    if not path.is_file():
        return -1
    with path.open("rb") as handle:
        return sum(1 for line in handle if line.strip())


def main() -> int:
    if not LOG_DIR.is_dir():
        print(f"UNCONFIGURED service logs: {LOG_DIR} does not exist (start a stack to configure)")
        return 0
    counts, problems = {}, []
    for name in REQUIRED:
        count = rows(LOG_DIR / f"{name}.log")
        counts[name] = count
        if count <= 0:
            problems.append(f"{name}.log has no output" if count == 0 else f"{name}.log is missing")
    skipped = []
    for name in OPTIONAL:
        count = rows(LOG_DIR / f"{name}.log")
        counts[name] = count
        if count < 0:
            skipped.append(name)
        elif count == 0:
            problems.append(f"{name}.log has no output")
    summary = ", ".join(
        f"{name} {counts[name]} row{'s' if counts[name] != 1 else ''}" if counts.get(name, -1) >= 0
        else f"{name} skipped"
        for name in REQUIRED + OPTIONAL
    )
    if problems:
        for problem in problems:
            print(f"FAIL {problem}")
        print(f"FAIL service logs: {summary}")
        return 1
    print(f"PASS service logs: {summary}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
