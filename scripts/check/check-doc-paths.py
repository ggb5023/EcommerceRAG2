#!/usr/bin/env python3
"""Every repository path written in backticks must exist.

Default-deny: a backticked token that looks like a path (``dir/...``) is checked
unless its first segment is a known non-repository prefix (URL pieces, API route
roots, package names). An unknown first segment fails on purpose: a whitelist of
directories only ever chases the omissions, while an unknown root is visible the
first time somebody writes it.

Placeholders (``*``, ``<>``, ``...``, ``/path/to/``) are ignored. A line that says
待建 declares a planned file and is skipped. Archived documents are frozen.
"""
from __future__ import annotations

import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parents[2]
ARCHIVE_PREFIX = "docs/archive/"
TICKED = re.compile(r"`([^`\n]+)`")
LOOKS_LIKE_PATH = re.compile(r"^([A-Za-z0-9._-]+)/[A-Za-z0-9._/-]+$")
PLACEHOLDER = re.compile(r"[*<>]|\.\.\.|/path/to/|xxx")

# Repository roots: a token starting with one of these must exist on disk.
REPO_ROOTS = {
    "docs", "scripts", "go", "python", "frontend", "eval", "sql",
    "proto", "docker", "tests", "data", "project-records",
}
# Notation that looks like a path but is not one: URL pieces, API route roots,
# SQL/JSON keys, package names. Keep this list short and review every addition.
NON_REPO_PREFIXES = {
    "http", "https", "www", "io", "com", "org", "net", "github",
    "admin", "api", "v1", "rag", "internal", "public", "static",
    "slot", "model", "tenant", "shop", "source", "document", "chunk",
}


def documents() -> list[pathlib.Path]:
    files = [ROOT / "README.md", ROOT / "AGENTS.md", ROOT / "ROADMAP.md"]
    files += sorted((ROOT / "docs").rglob("*.md"))
    return [p for p in files
            if p.exists()
            and ARCHIVE_PREFIX not in p.relative_to(ROOT).as_posix().replace("\\", "/")]


def main() -> int:
    problems: list[str] = []
    checked = 0
    ignored = 0
    for path in documents():
        rel_doc = path.relative_to(ROOT).as_posix()
        for line_no, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
            if "待建" in line:
                continue
            for token in TICKED.findall(line):
                candidate = token.split("#", 1)[0].strip()
                if candidate.startswith(("/", "~", ".", "http")) or PLACEHOLDER.search(candidate):
                    continue
                match = LOOKS_LIKE_PATH.match(candidate)
                if not match:
                    continue
                root = match.group(1)
                if "." in root:
                    ignored += 1  # hostname such as github.com; repo roots never contain a dot
                    continue
                if root in NON_REPO_PREFIXES:
                    ignored += 1
                    continue
                checked += 1
                if root not in REPO_ROOTS:
                    problems.append(f"{rel_doc}:{line_no}: unknown top-level directory: {candidate}")
                elif not (ROOT / candidate).exists():
                    problems.append(f"{rel_doc}:{line_no}: path does not exist: {candidate}")

    if problems:
        for problem in problems[:40]:
            print("FAIL " + problem)
        print(f"FAIL doc paths: {len(problems)} problem(s) out of {checked} path(s) checked")
        return 1

    print(f"PASS doc paths: {checked} path(s) checked, {ignored} non-repo notation ignored, "
          f"{len(documents())} document(s)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
