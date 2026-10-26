"""The approved-alignment artifact feeds build_eval_revision through this adapter."""
from __future__ import annotations

import hashlib
import importlib.util
import json
from pathlib import Path

REPO = Path(__file__).resolve().parents[1]


def _load(name: str):
    spec = importlib.util.spec_from_file_location(name, REPO / "eval" / f"{name}.py")
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def test_approval_becomes_a_no_op_revision_map(tmp_path: Path) -> None:
    module = _load("build_revision_map")
    builder = _load("build_eval_revision")
    builder.REQUIRED_CASE_COUNT = 2

    cases = [
        {"case_id": "c-1", "expected_answer_points": ["要点一"], "source": {"source_version": "v1"}},
        {"case_id": "c-2", "expected_answer_points": ["要点二", "要点三"], "source": {"source_version": "v1"}},
    ]
    cases_path = tmp_path / "cases.jsonl"
    cases_path.write_text("".join(json.dumps(case, ensure_ascii=False) + "\n" for case in cases), encoding="utf-8")
    digest = hashlib.sha256(cases_path.read_bytes()).hexdigest()
    metadata_path = tmp_path / "metadata.json"
    metadata_path.write_text(json.dumps({"eval_set_version": "v1", "case_count": 2, "sha256": digest}), encoding="utf-8")
    approval_path = tmp_path / "approval.json"
    approval_path.write_text(json.dumps({"status": "APPROVED", "eval_set_version": "v1",
                                         "cases_sha256": digest, "real_service_acceptance": False}), encoding="utf-8")

    revision_map = module.build_revision_map(approval_path, cases_path, metadata_path, "v2")
    assert revision_map["source_cases_sha256"] == digest
    assert revision_map["target_eval_set_version"] == "v2"
    assert revision_map["change_kind"] == "alignment_only_no_point_changes"
    assert [change["case_id"] for change in revision_map["changes"]] == ["c-1", "c-2"]
    assert revision_map["changes"][1]["fields"]["expected_answer_points"] == ["要点二", "要点三"]

    map_path = tmp_path / "revision-map.json"
    map_path.write_text(json.dumps(revision_map, ensure_ascii=False), encoding="utf-8")
    _revision, updated, target_metadata = builder.build_revision(cases_path, metadata_path, map_path)
    assert target_metadata["eval_set_version"] == "v2"
    assert target_metadata["source_cases_sha256"] == digest
    assert [case["expected_answer_points"] for case in updated] == [["要点一"], ["要点二", "要点三"]]
    assert all(case["source"]["source_version"] == "v2" for case in updated)
