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
# Ratchet: duplicated instructions are tolerated up to this many warnings. Each
# round of cleanup lowers the number; raising it needs a review, not a fix.
WARN_BUDGET = 0
# Frozen at the moment the ratchet was set, used to name what is new.
WARN_BASELINE = frozenset({
    "scripts/check/check-eval-corpus.py",
    "scripts/dev/generate-proto.sh",
    "scripts/dev/provider-usage.py",
    "scripts/dev/v2-local-services.sh --env",
    "scripts/ops/oss-backup.sh",
})
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


def _has_arguments(values: dict[str, set[str]]) -> bool:
    return any((value or "").strip() for value in values)


# Archived documents are frozen history: they intentionally mention retired keys,
# old commands and superseded rules. Drift applies only to maintained documents.
ARCHIVE_PREFIX = 'docs/archive/'


def main() -> int:
    documents = [p for p in scanned_documents()
                 if ARCHIVE_PREFIX not in rel(p).replace('\\', '/')]
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

    # A document that documents three or more distinct invocations of one script is
    # that script's authoritative page (the gate contract lives in the testing plan),
    # so its duplicates elsewhere are pointers, not copies.
    variants: dict[str, set[str]] = defaultdict(set)
    for key in users:
        script = key.split(" ")[0]
        variants[script].add(key)
    authoritative = {script for script, keys in variants.items() if len(keys) >= 3}

    warned: set[str] = set()
    for key, values in sorted(users.items()):
        if key.split(" ")[0] in authoritative and not key.split(" ")[0].startswith(("scripts/dev/", "scripts/ops/", "scripts/check/")):
            continue
        owners = {doc for docs in values.values() for doc in docs}
        if len(owners) < 2:
            continue
        # One document may define every variant on purpose (a tri-state flag, a
        # per-case env mapping); decision 0025 treats that as the authoritative
        # definition, so it is neither drift nor noise.
        universal = set.intersection(*values.values())
        canonical = bool(universal) and len(values) >= 2
        if canonical:
            continue
        if len(values) >= 2:
            warned.add(key)
            warnings.append(f"{key}: conflicting values across {len(owners)} documents: "
                            + "; ".join(f"{value!r} in {', '.join(sorted(docs))}" for value, docs in sorted(values.items())))
            if len(owners) >= 3:
                failures.append(f"{key}: conflicting instructions across {len(owners)} documents")
        elif len(owners) >= 3 and _has_arguments(values):
            # A bare reference ("bash scripts/check.sh") is a pointer to the single
            # definition, not a copy of the instruction.
            warned.add(key)
            warnings.append(f"{key}: instruction with arguments copied into {len(owners)} documents: "
                            + ", ".join(sorted(owners)))

    if len(warnings) > WARN_BUDGET:
        appeared = sorted(warned - WARN_BASELINE)
        failures.append(f"duplicated instructions {len(warnings)} exceed the budget {WARN_BUDGET}"
                        + (f"; new: {', '.join(appeared)}" if appeared else ""))
    elif len(warnings) < WARN_BUDGET:
        print(f"NOTE duplicated instructions dropped to {len(warnings)}; WARN_BUDGET can be lowered to {len(warnings)}")

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
