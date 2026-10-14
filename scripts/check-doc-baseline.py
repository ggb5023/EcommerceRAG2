#!/usr/bin/env python3
"""Check the concise, versioned project document entry points."""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path


REQUIRED_AGENT_SECTIONS = (
    "文档职责",
    "关键文件索引",
    "开发注意事项",
    "Git 提交规范",
    "用户回复模板",
)
HANDOFF_SECTIONS = ("当前进度", "下一步任务", "遗留问题")
REQUIRED_FACTS_SECTIONS = ("架构结论", "工作区职责", "证据边界", "文档职责")
ARCHITECTURE_PATH = "docs/architecture.md"
ARCHITECTURE_LINKS = ("../AGENTS.md", "../FACTS.md", "../README.md")


def check(repo_root: Path) -> list[str]:
    errors: list[str] = []

    def read(name: str) -> str:
        path = repo_root / name
        try:
            return path.read_text(encoding="utf-8")
        except OSError as exc:
            errors.append(f"{name}: cannot read required document ({exc.strerror})")
            return ""

    for name in ("install", "knowledge", "support", "backup", "development"):
        text = read(f"docs/{name}.md")
        links = set(re.findall(r"\[[^\]\n]+\]\(([^)\s]+)\)", text))
        if "../README.md" not in links:
            errors.append(f"docs/{name}.md: missing README link")

    agents = read("AGENTS.md")
    handoff = read("HANDOFF.md")
    facts = read("FACTS.md")
    architecture = read(ARCHITECTURE_PATH)

    architecture_links = set(re.findall(r"\[[^\]\n]+\]\(([^)\s]+)\)", architecture))
    for destination in ARCHITECTURE_LINKS:
        if destination not in architecture_links:
            errors.append(f"{ARCHITECTURE_PATH}: missing relative link {destination}")

    for heading in REQUIRED_AGENT_SECTIONS:
        if f"## {heading}" not in agents:
            errors.append(f"AGENTS.md: missing section {heading}")

    actual_handoff_sections = tuple(
        match.group(1).strip()
        for match in re.finditer(r"^##\s+(.+?)\s*$", handoff, re.MULTILINE)
    )
    if actual_handoff_sections != HANDOFF_SECTIONS:
        errors.append("HANDOFF.md: use only 当前进度, 下一步任务 and 遗留问题 sections")

    for heading in REQUIRED_FACTS_SECTIONS:
        if f"## {heading}" not in facts:
            errors.append(f"FACTS.md: missing section {heading}")

    if ".local/dev-docs/archive/" not in agents:
        errors.append("AGENTS.md: must define the private conversation archive location")
    if "日常开发不读 archive" not in agents:
        errors.append("AGENTS.md: must keep archives out of routine reading")
    if "每个新窗口在 `.local/dev-docs/archive/` 新建" not in agents:
        errors.append("AGENTS.md: must require one archive entry per new window")

    return errors


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo-root", type=Path, default=Path(__file__).resolve().parents[1])
    args = parser.parse_args()

    errors = check(args.repo_root.resolve())
    if errors:
        for message in errors:
            print(f"FAIL documentation entry: {message}", file=sys.stderr)
        return 1
    print(f"PASS documentation entry: AGENTS.md, HANDOFF.md, FACTS.md and {ARCHITECTURE_PATH}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
