import importlib.util
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("build_human_review_sidecar", ROOT / "eval/build_human_review_sidecar.py")
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(MODULE)


def test_sidecar_rejects_unattested_package(tmp_path: Path):
    package = tmp_path / "package"
    package.mkdir()
    (package / "manifest.json").write_text(
        '{"package_version":"v2-human-review-package-v1","human_review_completed":false,"reviewer_kind":"human"}',
        encoding="utf-8",
    )
    with pytest.raises(ValueError, match="human_review_attestation_missing"):
        MODULE.build_sidecar(
            tmp_path / "report.json", package, tmp_path / "captures", tmp_path / "cases.jsonl", tmp_path / "out.json"
        )
