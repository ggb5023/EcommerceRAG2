from __future__ import annotations

import subprocess
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).resolve().parents[1] / "scripts/check/check-doc-baseline.py"
DOCUMENTS = {
    "README.md": "[P](docs/product.md) [A](docs/architecture.md) [R](ROADMAP.md) [D](docs/development.md) [G](AGENTS.md)\n",
    "AGENTS.md": (
        "[P](docs/product.md) [R](ROADMAP.md) [A](docs/architecture.md)\n"
        "## 开始前必读\n## 防跑偏\n## 架构红线\n## 检查\n## Git\n## 文档维护\n"
    ),
    "ROADMAP.md": "[H](README.md) [P](docs/product.md) [A](docs/architecture.md)\n## 阶段\n## 当前任务\n",
    "docs/product.md": "[H](../README.md) [A](architecture.md) [R](../ROADMAP.md)\n",
    "docs/architecture.md": "[H](../README.md) [P](product.md) [R](../ROADMAP.md)\n",
    "docs/development.md": "[H](../README.md) [A](architecture.md) [G](../AGENTS.md)\n",
    "docs/decisions/0001-first.md": "# 0001\n",
}


class BaselineCheckerTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        for name, text in DOCUMENTS.items():
            self.write(name, text)
        for name in ("install", "knowledge", "support", "backup"):
            self.write(f"docs/{name}.md", "[Project](../README.md)\n")

    def tearDown(self) -> None:
        self.temp.cleanup()

    def write(self, name: str, text: str) -> None:
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")

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

    def test_each_entry_document_is_required(self) -> None:
        for name in ("README.md", "AGENTS.md", "ROADMAP.md", "docs/product.md", "docs/architecture.md"):
            with self.subTest(name=name):
                (self.root / name).unlink()
                result = self.run_checker()
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(f"{name}: cannot read", result.stderr)
                self.write(name, DOCUMENTS[name])

    def test_architecture_requires_each_link(self) -> None:
        for destination in ("../README.md", "product.md", "../ROADMAP.md"):
            with self.subTest(destination=destination):
                self.write("docs/architecture.md", DOCUMENTS["docs/architecture.md"].replace(f"]({destination})", "]"))
                result = self.run_checker()
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(f"missing relative link {destination}", result.stderr)

    def test_agents_and_roadmap_require_sections(self) -> None:
        self.write("AGENTS.md", DOCUMENTS["AGENTS.md"].replace("## 防跑偏\n", ""))
        self.write("ROADMAP.md", DOCUMENTS["ROADMAP.md"].replace("## 当前任务\n", ""))
        stderr = self.run_checker().stderr
        self.assertIn("AGENTS.md: missing section 防跑偏", stderr)
        self.assertIn("ROADMAP.md: missing section 当前任务", stderr)

    def test_decision_record_is_required(self) -> None:
        (self.root / "docs/decisions/0001-first.md").unlink()
        self.assertIn("numbered decision record", self.run_checker().stderr)

    def test_retired_documents_cannot_return_or_be_referenced(self) -> None:
        self.write("HANDOFF.md", "# Handoff\n")
        self.write("docs/support.md", "[Project](../README.md) see FACTS.md\n")
        stderr = self.run_checker().stderr
        self.assertIn("HANDOFF.md: retired document must not be restored", stderr)
        self.assertIn("docs/support.md: references retired FACTS.md", stderr)

    def test_merchant_document_needs_a_real_root_link(self) -> None:
        self.write("docs/install.md", "Mention ../README.md without a link.\n")
        self.assertIn("docs/install.md: missing README link", self.run_checker().stderr)

    def test_private_archive_is_not_a_baseline_input(self) -> None:
        self.write(".local/dev-docs/archive/old-window.md", "HANDOFF.md history\n")
        self.assertEqual(self.run_checker().returncode, 0)


if __name__ == "__main__":
    unittest.main()
