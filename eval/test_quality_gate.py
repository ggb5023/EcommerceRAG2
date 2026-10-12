import importlib.util
import json
from pathlib import Path

SPEC = importlib.util.spec_from_file_location("quality_gate", Path(__file__).with_name("quality_gate.py"))
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC and SPEC.loader
SPEC.loader.exec_module(MODULE)


def _report(tmp_path, *, evidence=True):
    rows = []
    for index in range(53):
        case_id = f"case-{index:03d}"
        rows.append({
            "case_id": case_id,
            "expected_document_ids": ["doc"],
            "embedding_top5": [{"document_id": "doc"}],
            "rerank_top5": [{"document_id": "doc"}],
            "embedding_hit_at_5": True,
            "rerank_hit_at_5": True,
            "approved_support_chunk_coverage": [{"all_delivered": evidence}],
        })
    payload = {
        "report_version": "bailian-batch-v1",
        "status": "PASS",
        "real_service_acceptance": False,
        "m1_connected": False,
        "eligible_case_count": 53,
        "excluded_case_count": 7,
        "excluded_status_counts": {"INELIGIBLE_REQUIRED_CHUNKS": 7},
        "case_results": rows,
        "provider_identity": {"profile": "aliyun-bailian"},
    }
    path = tmp_path / "report.json"
    path.write_text(json.dumps(payload), encoding="utf-8")
    return path


def test_gate_never_claims_semantic_quality_without_review(tmp_path):
    result = MODULE.evaluate(_report(tmp_path), None)
    assert result["answer_quality_status"] == "NOT_PASSED"
    assert result["semantic"]["status"] == "NOT_RUN"
    assert result["real_service_acceptance"] is False
    assert "semantic_review_not_passed" in result["gate_reasons"]


def test_gate_reports_evidence_delivery_separately_from_document_recall(tmp_path):
    result = MODULE.evaluate(_report(tmp_path, evidence=False), None)
    assert result["retrieval"]["rerank"]["recall_at_5"] == 1.0
    assert result["evidence"]["approved_support_delivery_rate"] == 0.0
    assert "approved_support_delivery_below_0.95" in result["gate_reasons"]


def test_review_sidecar_must_bind_to_exact_report(tmp_path):
    report_path = _report(tmp_path)
    digest = MODULE.sha256(report_path)
    rows = [
        {
            "case_id": f"case-{i:03d}",
            "risk": "ordinary",
            "review_status": "pass",
            "security_error": False,
            "contradiction": False,
            "citation_valid": True,
            "answer_points_supported": 1,
            "answer_points_total": 1,
        }
        for i in range(53)
    ]
    review_path = tmp_path / "review.json"
    review_path.write_text(json.dumps({"review_version": MODULE.REVIEW_VERSION,
                                      "baseline_report_sha256": digest,
                                      "rows": rows}), encoding="utf-8")
    result = MODULE.evaluate(report_path, review_path)
    assert result["semantic"]["status"] == "PASS"
    assert result["answer_quality_status"] == "PASS"
    assert result["model_quality_claim"] is False
