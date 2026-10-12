import importlib.util
import json
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("build_human_review_package", ROOT / "eval/build_human_review_package.py")
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(MODULE)


def _fixture(tmp_path: Path):
    capture_dir = tmp_path / "captures"
    capture_dir.mkdir(mode=0o700)
    capture = {
        "capture_version": "v2-quality-answer-v1",
        "case_id": "syn-001",
        "query_sha256": "q" * 64,
        "structured": {"answer_points": ["supported answer"]},
        "text": "answer",
        "evidence": [{"chunk_id": "chunk-1", "content": "evidence"}],
    }
    body = (json.dumps(capture, ensure_ascii=False, sort_keys=True) + "\n").encode()
    path = capture_dir / "capture.json"
    path.write_bytes(body)
    path.chmod(0o600)
    import hashlib
    digest = hashlib.sha256(body).hexdigest()
    report = {
        "status": "PASS",
        "case_results": [{"case_id": "syn-001", "capture_id": "capture.json", "capture_sha256": digest}],
    }
    report_path = tmp_path / "report.json"
    report_path.write_text(json.dumps(report), encoding="utf-8")
    cases_path = tmp_path / "cases.jsonl"
    cases_path.write_text(json.dumps({
        "case_id": "syn-001", "query": "question", "expected_answer_points": ["truth"],
        "expected_doc_ids": ["doc-1"], "tags": [], "business_date": "2026-01-01",
    }) + "\n", encoding="utf-8")
    return report_path, cases_path, capture_dir


def test_package_requires_frozen_53_case_baseline(tmp_path: Path):
    report, cases, captures = _fixture(tmp_path)
    with pytest.raises(ValueError, match="baseline_case_count_invalid"):
        MODULE.build_package(report, cases, captures, tmp_path / "package")


def test_package_is_outside_repository(tmp_path: Path):
    report, cases, captures = _fixture(tmp_path)
    with pytest.raises(ValueError, match="baseline_case_count_invalid"):
        MODULE.build_package(report, cases, captures, ROOT / "package")
