#!/usr/bin/env python3
"""Every repository path written in backticks in a maintained document must exist.

Why: an agent executes what the document says. A path that was renamed or deleted
turns the document into an instruction that cannot work, and nothing else notices.
Placeholders (containing *, ..., <, >, or a trailing /path/to/) are ignored.
Archived documents are frozen history and are not scanned.
"""
from __future__ import annotations

import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parents[2]
ARCHIVE_PREFIX = "docs/archive/"
TOP_LEVEL = ("docs", "scripts", "go", "python", "frontend", "eval", "sql", "proto", "docker", "tests", "data")
TICKED = re.compile(r"`([^`\n]+)`")
PATH_RE = re.compile(r"^(?:" + "|".join(TOP_LEVEL) + r")/[A-Za-z0-9._/-]+$")
PLACEHOLDER = re.compile(r"[*<>]|\.\.\.|/path/to/|xxx")


def documents() -> list[pathlib.Path]:
    files = [ROOT / "README.md", ROOT / "AGENTS.md", ROOT / "ROADMAP.md"]
    files += sorted((ROOT / "docs").rglob("*.md"))
    return [p for p in files if p.exists()
            and ARCHIVE_PREFIX not in p.relative_to(ROOT).as_posix().replace("\\", "/")]


def main() -> int:
    problems: list[str] = []
    checked = 0
    for path in documents():
        rel_doc = path.relative_to(ROOT).as_posix()
        for line_no, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
            if "待建" in line:
                continue  # a planned file is declared, not missing
            for token in TICKED.findall(line):
                candidate = token.split("#", 1)[0].strip()
                if not PATH_RE.match(candidate) or PLACEHOLDER.search(candidate):
                    continue
                checked += 1
                if not (ROOT / candidate).exists():
                    problems.append(f"{rel_doc}:{line_no}: path does not exist: {candidate}")

    if problems:
        for problem in problems[:40]:
            print("FAIL " + problem)
        print(f"FAIL doc paths: {len(problems)} missing path(s) out of {checked} checked")
        return 1

    print(f"PASS doc paths: {checked} path(s) checked across {len(documents())} document(s)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
