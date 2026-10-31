#!/usr/bin/env python3
"""Validate docs/error-taxonomy.md as a table, not as prose.

Five rules, each enforced mechanically so nobody has to remember them:

  1. every row has exactly five columns;
  2. HTTP is one or more three-digit statuses separated by "/" (a code can map
     to different statuses per cause), or an em dash, which the table defines as
     "no HTTP response of its own";
  3. retryable is 是 or 否;
  4. error codes are unique;
  5. every file named in 出现位置 exists in the repository;
  6. a non-retryable code must not tell the reader to retry.

Rule 7 (code <-> table双向覆盖) is deliberately not implemented yet.
Sketch for when it lands: collect every string literal passed to WriteError /
finish / _settle_failure across go/ and python/, and require the set to equal
the table's codes. That needs a parser per language and an allowlist for the
handful of literals that are not error codes, so it stays out until these six
rules have been stable for a while.
"""
from __future__ import annotations

import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parents[2]
TABLE = ROOT / "docs" / "error-taxonomy.md"

ROW = re.compile(r"^\|\s*`(?P<code>[^`]+)`\s*\|(?P<rest>.*)\|\s*$")
HTTP = re.compile(r"^(?:—|\d{3}(?:\s*/\s*\d{3})*)$")
RETRYABLE = {"是", "否"}
LOCATION = re.compile(r"`([^`]+)`")
PATH_PART = re.compile(r"^[A-Za-z0-9._/-]+\.[A-Za-z0-9]+(?::\S*)?$")
ALLOWED_PLACEHOLDERS = ("占位", "待定")


def rows() -> list[tuple[int, str, list[str]]]:
    parsed = []
    for number, line in enumerate(TABLE.read_text(encoding="utf-8").splitlines(), 1):
        match = ROW.match(line.strip())
        if not match:
            continue
        cells = [cell.strip() for cell in match.group("rest").split("|")]
        parsed.append((number, match.group("code"), cells))
    return parsed


def main() -> int:
    problems: list[str] = []
    seen: dict[str, int] = {}

    for number, code, cells in rows():
        # rule 1: five columns (code + four remaining)
        if len(cells) != 4:
            problems.append(f"line {number} {code}: expected 5 columns, found {len(cells) + 1}")
            continue
        http, retryable, wording, location = cells

        # rule 2
        if not HTTP.match(http):
            problems.append(f"line {number} {code}: HTTP must be three digits or —, found {http!r}")

        # rule 3
        if retryable not in RETRYABLE:
            problems.append(f"line {number} {code}: 可重试 must be 是 or 否, found {retryable!r}")

        # rule 6 (checked before 5 so the message is specific)
        if retryable == "否" and "重试" in wording:
            problems.append(
                f"line {number} {code}: non-retryable code must not say 重试 (found {wording!r})")

        # rule 4
        if code in seen:
            problems.append(f"line {number}: duplicate error code {code} (first seen on line {seen[code]})")
        else:
            seen[code] = number

        # rule 5
        for token in LOCATION.findall(location):
            candidate = token.split(":", 1)[0].strip()
            if not PATH_PART.match(candidate):
                continue
            if any(marker in candidate for marker in ALLOWED_PLACEHOLDERS):
                continue
            if not (ROOT / candidate).exists():
                problems.append(f"line {number} {code}: location path does not exist: {candidate}")

    if problems:
        for problem in problems[:40]:
            print("FAIL " + problem)
        print(f"FAIL error taxonomy: {len(problems)} problem(s) across {len(seen)} code(s)")
        return 1

    print(f"PASS error taxonomy: {len(seen)} code(s), five column rules and location paths hold")
    return 0


if __name__ == "__main__":
    sys.exit(main())
