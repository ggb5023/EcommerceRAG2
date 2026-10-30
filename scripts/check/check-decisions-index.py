#!/usr/bin/env python3
"""Decision files and docs/decisions/INDEX.md must agree exactly.

Three failure modes this catches:
  - a decision file exists but nobody can find it (missing index row);
  - an index row points at a file that was renamed or deleted;
  - a number is used twice, or a number silently disappears.
"""
from __future__ import annotations

import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parents[2]
DECISIONS = ROOT / "docs" / "decisions"
INDEX = DECISIONS / "INDEX.md"
FILE_RE = re.compile(r"^(\d{4})-[a-z0-9-]+\.md$")
ROW_RE = re.compile(r"^\|\s*(\d{4})\s*\|")
UNUSED_RE = re.compile(r"`(\d{4})`")


def main() -> int:
    files = {}
    for path in sorted(DECISIONS.glob("*.md")):
        match = FILE_RE.match(path.name)
        if match:
            files.setdefault(match.group(1), []).append(path.name)

    if not INDEX.exists():
        print("FAIL docs/decisions/INDEX.md is missing")
        return 1

    rows = []
    unused_section = False
    unused = set()
    for line in INDEX.read_text(encoding="utf-8").splitlines():
        if line.startswith("## "):
            unused_section = "未使用" in line
            continue
        match = ROW_RE.match(line)
        if match:
            rows.append(match.group(1))
            continue
        if unused_section:
            unused.update(UNUSED_RE.findall(line))

    problems: list[str] = []

    duplicated_files = {number: names for number, names in files.items() if len(names) > 1}
    for number, names in duplicated_files.items():
        problems.append(f"decision {number} has more than one file: {', '.join(names)}")

    duplicated_rows = {number for number in rows if rows.count(number) > 1}
    for number in sorted(duplicated_rows):
        problems.append(f"decision {number} appears more than once in INDEX.md")

    missing_rows = sorted(set(files) - set(rows))
    for number in missing_rows:
        problems.append(f"decision {number} has a file but no INDEX.md row")

    missing_files = sorted(set(rows) - set(files))
    for number in missing_files:
        problems.append(f"decision {number} has an INDEX.md row but no file")

    known = set(files) | set(rows) | unused
    highest = max(int(number) for number in known) if known else 0
    for value in range(1, highest + 1):
        number = f"{value:04d}"
        if number not in files and number not in unused:
            problems.append(f"decision number {number} is missing and not declared unused")

    if problems:
        for problem in problems:
            print("FAIL " + problem)
        print(f"FAIL decisions index: {len(problems)} problem(s)")
        return 1

    print(f"PASS decisions index: {len(files)} files, {len(rows)} rows, {len(unused)} unused number(s)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
