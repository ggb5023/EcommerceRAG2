#!/usr/bin/env python3
"""Check the concise, versioned project document entry points."""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path


LINK = re.compile(r"\[[^\]\n]+\]\(([^)\s]+)\)")
REQUIRED_AGENT_SECTIONS = (
    "开始前必读",
    "防跑偏",
    "架构红线",
    "检查",
    "Git",
    "文档维护",
)
REQUIRED_ROADMAP_SECTIONS = ("阶段", "当前任务")
MERCHANT_DOCS = ("install", "knowledge", "support", "backup")
# Each entry document must link to the documents a new reader needs next.
REQUIRED_LINKS = {
    "README.md": ("docs/product.md", "docs/architecture.md", "ROADMAP.md", "docs/development.md", "AGENTS.md"),
    "AGENTS.md": ("docs/product.md", "ROADMAP.md", "docs/architecture.md"),
    "ROADMAP.md": ("README.md", "docs/product.md", "docs/architecture.md"),
    "docs/product.md": ("../README.md", "architecture.md", "../ROADMAP.md"),
    "docs/architecture.md": ("../README.md", "product.md", "../ROADMAP.md"),
    "docs/development.md": ("../README.md", "architecture.md", "../AGENTS.md"),
}
RETIRED_DOCUMENTS = ("FACTS.md", "HANDOFF.md")


def check(repo_root: Path) -> list[str]:
    errors: list[str] = []

    def read(name: str) -> str:
        path = repo_root / name
        try:
            return path.read_text(encoding="utf-8")
        except OSError as exc:
            errors.append(f"{name}: cannot read required document ({exc.strerror})")
            return ""

    texts = {name: read(name) for name in REQUIRED_LINKS}
    for name, destinations in REQUIRED_LINKS.items():
        links = set(LINK.findall(texts[name]))
        for destination in destinations:
            if destination not in links:
                errors.append(f"{name}: missing relative link {destination}")

    for name in MERCHANT_DOCS:
        if "../README.md" not in set(LINK.findall(read(f"docs/{name}.md"))):
            errors.append(f"docs/{name}.md: missing README link")

    for heading in REQUIRED_AGENT_SECTIONS:
        if f"## {heading}" not in texts["AGENTS.md"]:
            errors.append(f"AGENTS.md: missing section {heading}")
    for heading in REQUIRED_ROADMAP_SECTIONS:
        if f"## {heading}" not in texts["ROADMAP.md"]:
            errors.append(f"ROADMAP.md: missing section {heading}")

    decisions = sorted((repo_root / "docs/decisions").glob("[0-9][0-9][0-9][0-9]-*.md"))
    if not decisions:
        errors.append("docs/decisions/: at least one numbered decision record is required")

    for retired in RETIRED_DOCUMENTS:
        if (repo_root / retired).exists():
            errors.append(f"{retired}: retired document must not be restored")
    for path in [repo_root / "README.md", repo_root / "AGENTS.md", repo_root / "ROADMAP.md",
                 *sorted((repo_root / "docs").rglob("*.md"))]:
        if not path.is_file():
            continue
        name = path.relative_to(repo_root).as_posix()
        if name == "docs/decisions/0003-documentation-system.md":
            continue  # The decision record explains why they were retired.
        text = path.read_text(encoding="utf-8")
        for retired in RETIRED_DOCUMENTS:
            if retired in text:
                errors.append(f"{name}: references retired {retired}")

    return errors


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo-root", type=Path, default=Path(__file__).resolve().parents[2])
    args = parser.parse_args()

    errors = check(args.repo_root.resolve())
    if errors:
        for message in errors:
            print(f"FAIL documentation entry: {message}", file=sys.stderr)
        return 1
    print("PASS documentation entry: README, AGENTS, ROADMAP, product, architecture, development and decisions")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
