import importlib.util
import json
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


@pytest.mark.parametrize("defect", [None, "unauthorized", "unreviewed", "answer_changed", "support_outside", "issue_text"])
def test_delegated_http_review_binds_actual_inputs_and_never_infers_approval(tmp_path, defect):
    import hashlib

    from build_human_review_package import build_package

    captures = tmp_path / "captures"
    captures.mkdir(mode=0o700)
    rows, cases = [], []
    for index in range(53):
        identifier = f"case-{index}"
        case = {"case_id": identifier, "query": "question", "expected_answer_points": ["truth"],
                "expected_doc_ids": ["document"], "tags": []}
        cases.append(case)
        value = {"capture_version": "v2-http-quality-capture-v1", "case_id": identifier,
                 "query_sha256": hashlib.sha256(b"question").hexdigest(),
                 "result": {"answer": "supported answer", "status": "DONE", "citations": [],
                            "customer_reply": {"can_copy": False},
                            "evidence": [{"id": "chunk", "snippet": "source"}]}}
        path = captures / f"{identifier}.json"
        path.write_text(json.dumps(value))
        path.chmod(0o600)
        rows.append({"case_id": identifier, "capture_id": path.name, "capture_sha256": MODULE.sha256(path)})
    report_path, cases_path = tmp_path / "report.json", tmp_path / "cases.jsonl"
    report_path.write_text(json.dumps({"report_version": "v2-quality-e2e-v2", "status": "PASS", "case_count": 53, "results": rows}))
    cases_path.write_text("\n".join(json.dumps(case) for case in cases))
    package = tmp_path / "package"
    manifest = build_package(report_path, cases_path, captures, package, reviewer_kind="agent")
    for item in manifest["entries"]:
        path = package / item["review_file"]
        value = json.loads(path.read_text())
        value["review_fields"].update(review_status="pass", **dict.fromkeys(MODULE.CHECKS, True),
                                      review_notes="private answer excerpt", issue_codes=["source_gap"],
                                      points=[{"point_index": 0, "status": "supported", "support_chunk_ids": ["chunk"],
                                               "review_notes": "private point excerpt"}])
        if item["case_id"] == "case-0":
            if defect == "unreviewed":
                value["review_fields"]["review_status"] = "not_reviewed"
            elif defect == "answer_changed":
                value["answer"] = "different answer"
            elif defect == "support_outside":
                value["review_fields"]["points"][0]["support_chunk_ids"] = ["absent"]
            elif defect == "issue_text":
                value["review_fields"]["issue_codes"] = ["private answer excerpt"]
        path.write_text(json.dumps(value))
    attestation = {"attestation_version": "v2-agent-review-attestation-v1", "package_version": manifest["package_version"],
                   "reviewer_kind": "agent", "review_completed": True, "human_review_completed": False,
                   "delegation_authorization": None if defect == "unauthorized" else "explicit_user_instruction",
                   "review_method": "delegated_agent_semantic_review_v1", "review_status": "approved",
                   "baseline_report_sha256": MODULE.sha256(report_path), "cases_sha256": MODULE.sha256(cases_path),
                   "reviewed_case_count": 53, "real_service_acceptance": False}
    (package / "agent-attestation.json").write_text(json.dumps(attestation))
    arguments = (report_path, package, captures, cases_path, tmp_path / "out.json")
    if defect:
        with pytest.raises(ValueError):
            MODULE.build_sidecar(*arguments, reviewer_kind="agent")
        assert not arguments[-1].exists()
    else:
        result = MODULE.build_sidecar(*arguments, reviewer_kind="agent")
        assert result["reviewer_kind"] == "agent" and result["human_review_completed"] is False
        assert len(result["rows"]) == 53 and len(result["agent_attestation_sha256"]) == 64
        assert "private answer excerpt" not in arguments[-1].read_text()
        assert "private point excerpt" not in arguments[-1].read_text()
        assert result["rows"][0]["issue_codes"] == ["source_gap"]
        assert "private answer excerpt" in (package / "cases/case-0.json").read_text()
