from __future__ import annotations

import subprocess
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).resolve().parents[1] / "scripts/check-doc-baseline.py"
AGENTS = """# Rules

## 文档职责
Read handoff first.
Archive: `.local/dev-docs/archive/`; 日常开发不读 archive。
每个新窗口在 `.local/dev-docs/archive/` 新建一份简短对话归档。

## 关键文件索引
Index.

## 开发注意事项
Rules.

## Git 提交规范
Small commits.

## 用户回复模板
Template.
"""
HANDOFF = """# Handoff

## 当前进度
Progress.

## 下一步任务
Next.

## 遗留问题
Issues.
"""
FACTS = """# Facts

## 架构结论
Architecture.

## 工作区职责
Boundaries.

## 证据边界
Evidence.

## 文档职责
Roles.
"""
ARCHITECTURE = """# Architecture

[Rules](../AGENTS.md), [Facts](../FACTS.md), [Startup](../README.md).
"""


class BaselineCheckerTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        (self.root / "AGENTS.md").write_text(AGENTS, encoding="utf-8")
        (self.root / "HANDOFF.md").write_text(HANDOFF, encoding="utf-8")
        (self.root / "FACTS.md").write_text(FACTS, encoding="utf-8")
        (self.root / "docs").mkdir()
        (self.root / "docs/architecture.md").write_text(ARCHITECTURE, encoding="utf-8")
        for name in ("install", "knowledge", "support", "backup", "development"):
            (self.root / f"docs/{name}.md").write_text("[Project](../README.md)\n", encoding="utf-8")

    def tearDown(self) -> None:
        self.temp.cleanup()

    def run_checker(self) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            ["python3", str(SCRIPT), "--repo-root", str(self.root)],
            check=False,
            capture_output=True,
            text=True,
        )

    def test_valid_entry_documents_pass(self) -> None:
        result = self.run_checker()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("PASS documentation entry", result.stdout)

    def test_missing_facts_fails(self) -> None:
        (self.root / "FACTS.md").unlink()
        result = self.run_checker()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("FACTS.md", result.stderr)

    def test_handoff_rejects_extra_history_section(self) -> None:
        (self.root / "HANDOFF.md").write_text(HANDOFF + "\n## 历史记录\nOld.\n", encoding="utf-8")
        result = self.run_checker()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("use only", result.stderr)

    def test_missing_public_architecture_fails(self) -> None:
        (self.root / "docs/architecture.md").unlink()
        result = self.run_checker()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("docs/architecture.md: cannot read", result.stderr)

    def test_architecture_requires_each_root_link(self) -> None:
        for destination in ("../AGENTS.md", "../FACTS.md", "../README.md"):
            with self.subTest(destination=destination):
                (self.root / "docs/architecture.md").write_text(
                    ARCHITECTURE.replace(f"]({destination})", f"]({destination[3:]})"),
                    encoding="utf-8",
                )
                result = self.run_checker()
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(f"missing relative link {destination}", result.stderr)

    def test_private_topics_are_not_required(self) -> None:
        self.assertFalse((self.root / ".local").exists())
        self.assertEqual(self.run_checker().returncode, 0)

    def test_public_usage_document_is_required_with_a_real_root_link(self) -> None:
        path = self.root / "docs/install.md"
        path.unlink()
        self.assertNotEqual(self.run_checker().returncode, 0)
        path.write_text("Mention ../README.md without a link.\n", encoding="utf-8")
        self.assertIn("docs/install.md: missing README link", self.run_checker().stderr)

    def test_archive_is_not_a_baseline_input(self) -> None:
        archive = self.root / ".local/dev-docs/archive"
        archive.mkdir(parents=True)
        (archive / "old-window.md").write_text("historical details\n", encoding="utf-8")
        self.assertEqual(self.run_checker().returncode, 0)


if __name__ == "__main__":
    unittest.main()
