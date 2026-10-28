#!/usr/bin/env python3
"""Detect documentation drift: deprecated keys and instructions copied into many files.

Two rules, both from decision 0025:

R1  An instruction (`--flag`, `ENV_KEY=`, `bash|python3 scripts/...`) belongs to one
    document. Appearing in two documents is a warning; appearing in three or more
    documents with different values is a failure, because that is how one command
    ends up with two meanings.

Deprecated configuration keys (decision 0021) fail immediately outside the documents
that discuss them on purpose.
"""

from __future__ import annotations

import re
import sys
from collections import defaultdict
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
DOCS = [ROOT / "README.md", ROOT / "AGENTS.md", ROOT / "ROADMAP.md", *sorted((ROOT / "docs").rglob("*.md"))]

# Documents that explain the retired keys themselves.
DISCUSSION = re.compile(r"^docs/(?:[a-z0-9-]+-plan|refactor-assessment|refactor-input)\.md$")
DEPRECATED = ("V2_ONLINE_BUDGET", "provider-budget")

# An instruction is a script invocation (with its arguments) or an environment
# assignment. Bare flags are only counted when they follow a script path on the
# same line, so unrelated tools do not collide.
SCRIPT = re.compile(r"(?:bash |python3 )?((?:scripts|eval)/[a-z0-9][a-z0-9/_-]*\.(?:sh|py))([^\n`]*)")
ENV = re.compile(r"(?<![\w-])([A-Z][A-Z0-9_]{2,})=([^\s`'\"]*)")
COMMENT = re.compile(r"\s+#.*$")
PLACEHOLDER = re.compile(r"^(?:\.{2,}|…|<[^>]*>)$")
WARN_LIMIT = 12


def rel(path: Path) -> str:
    return path.relative_to(ROOT).as_posix()


def scanned_documents() -> list[Path]:
    kept = []
    for path in DOCS:
        if not path.is_file():
            continue
        name = rel(path)
        if name.startswith("docs/decisions/"):
            continue
        kept.append(path)
    return kept


def instruction_values(text: str) -> dict[str, str]:
    """Map instruction key to the arguments or value used on that line.

    Inline comments and trailing punctuation are stripped: `bash scripts/check.sh --all  # <= 45s`
    and `bash scripts/check.sh --all` are the same instruction.
    """
    stripped = COMMENT.sub("", text)
    found: dict[str, str] = {}
    for match in SCRIPT.finditer(stripped):
        tokens = re.sub(r"\s+", " ", match.group(2)).strip().strip("`\\.,;|").split()
        if tokens and tokens[0].startswith("["):
            continue  # usage synopsis, not an invocation
        key = match.group(1) + (" " + tokens[0] if tokens else "")
        found.setdefault(key, " ".join(tokens[1:]))
    for match in ENV.finditer(stripped):
        value = match.group(2).strip()
        if PLACEHOLDER.match(value):
            continue
        found.setdefault(match.group(1) + "=", value)
    return found


def main() -> int:
    documents = scanned_documents()
    failures: list[str] = []
    warnings: list[str] = []

    for path in documents:
        name = rel(path)
        text = path.read_text(encoding="utf-8")
        if not DISCUSSION.fullmatch(name):
            for key in DEPRECATED:
                if key in text:
                    failures.append(f"{name}: retired key {key} still documented (decision 0021)")

    users: dict[str, dict[str, set[str]]] = defaultdict(lambda: defaultdict(set))
    for path in documents:
        name = rel(path)
        for line in path.read_text(encoding="utf-8").splitlines():
            for key, value in instruction_values(line).items():
                users[key][value or "(no value)"].add(name)

    for key, values in sorted(users.items()):
        owners = {doc for docs in values.values() for doc in docs}
        if len(owners) < 2:
            continue
        if len(owners) >= 3 and len(values) >= 2:
            # One document may define every variant on purpose (a tri-state flag, a
            # per-case env mapping); that is the canonical definition, not drift.
            canonical = [sorted(docs) for docs in values.values() if len(docs) == len(owners)]
            if not canonical:
                rendered = "; ".join(f"{value!r} in {', '.join(sorted(docs))}" for value, docs in sorted(values.items()))
                failures.append(f"{key}: conflicting instructions across {len(owners)} documents: {rendered}")
                continue
        warnings.append(f"{key}: copied into {len(owners)} documents: {', '.join(sorted(owners))}")

    for warning in warnings[:WARN_LIMIT]:
        print(f"WARN {warning}")
    if len(warnings) > WARN_LIMIT:
        print(f"WARN ... and {len(warnings) - WARN_LIMIT} more duplicated instructions")
    if failures:
        for failure in failures:
            print(f"FAIL {failure}")
        print(f"FAIL documentation drift: {len(failures)} problem(s)")
        return 1
    print(f"PASS documentation drift: {len(documents)} documents, {len(users)} instructions tracked")
    return 0


if __name__ == "__main__":
    sys.exit(main())
