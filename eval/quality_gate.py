#!/usr/bin/env python3
"""Evaluate a redacted V2 quality report against the frozen quality gate.

The online evaluator deliberately does not retain prompts, source bodies, or
model answers.  This command therefore computes retrieval/evidence metrics from
its metadata report and optionally consumes a separately protected manual review
sidecar.  It never treats lexical diagnostics as semantic approval.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import math
import os
import tempfile
from collections.abc import Mapping, Sequence
from pathlib import Path
from typing import Any

REVIEW_VERSION = "v2-quality-review-v1"
REPORT_VERSION = "bailian-batch-v1"
REQUIRED_CASES = 53
REQUIRED_EXCLUDED = 7


def _reject_duplicate_keys(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f"duplicate JSON key: {key}")
        result[key] = value
    return result


def load_json(path: Path) -> Any:
    return json.loads(path.read_text(encoding="utf-8"), object_pairs_hook=_reject_duplicate_keys)


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def _relevant(row: Mapping[str, Any], field: str) -> set[str]:
    expected = row.get("expected_document_ids", [])
    return {str(value) for value in expected} if isinstance(expected, list) else set()


def _rank_metrics(rows: Sequence[Mapping[str, Any]], field: str) -> dict[str, float | int | None]:
    hits = 0
    reciprocal = 0.0
    ndcg = 0.0
    evaluated = 0
    for row in rows:
        expected = _relevant(row, field)
        ranked = row.get(field.replace("_hit_at_5", "_top5"), [])
        if not expected or not isinstance(ranked, list):
            continue
        evaluated += 1
        relevance = [1 if item.get("document_id") in expected else 0 for item in ranked[:5] if isinstance(item, Mapping)]
        if any(relevance):
            hits += 1
            rank = relevance.index(1) + 1
            reciprocal += 1.0 / rank
            ndcg += 1.0 / math.log2(rank + 1)
    if not evaluated:
        return {"evaluated_cases": 0, "recall_at_5": None, "mrr": None, "ndcg_at_5": None}
    ideal = 1.0
    return {
        "evaluated_cases": evaluated,
        "recall_at_5": round(hits / evaluated, 6),
        "mrr": round(reciprocal / evaluated, 6),
        "ndcg_at_5": round(ndcg / (evaluated * ideal), 6),
    }


def _evidence_metrics(rows: Sequence[Mapping[str, Any]]) -> dict[str, Any]:
    covered = 0
    total = 0
    for row in rows:
        coverage = row.get("approved_support_chunk_coverage")
        if not isinstance(coverage, list) or not coverage:
            continue
        total += 1
        if all(isinstance(item, Mapping) and item.get("all_delivered") is True for item in coverage):
            covered += 1
    return {
        "cases_with_approved_support": total,
        "cases_with_all_support_delivered": covered,
        "approved_support_delivery_rate": round(covered / total, 6) if total else None,
        "citation_validity": "NOT_RUN",
    }


def _load_review(path: Path, report: Mapping[str, Any]) -> tuple[dict[str, Any] | None, list[str]]:
    try:
        review = load_json(path)
    except (OSError, UnicodeDecodeError, json.JSONDecodeError, ValueError) as error:
        return None, [f"review_invalid:{type(error).__name__}"]
    issues: list[str] = []
    if not isinstance(review, Mapping):
        return None, ["review_root_not_object"]
    if review.get("review_version") != REVIEW_VERSION:
        issues.append("review_version_invalid")
    if review.get("baseline_report_sha256") != report.get("report_sha256"):
        issues.append("review_baseline_report_sha256_mismatch")
    rows = review.get("rows")
    if not isinstance(rows, list):
        issues.append("review_rows_not_array")
        return None, issues
    expected_ids = {str(row.get("case_id")) for row in report.get("case_results", [])}
    seen: set[str] = set()
    allowed = {"pass", "fail", "not_reviewed"}
    for row in rows:
        if not isinstance(row, Mapping):
            issues.append("review_row_not_object")
            continue
        case_id = row.get("case_id")
        if not isinstance(case_id, str) or case_id not in expected_ids or case_id in seen:
            issues.append("review_case_id_invalid")
            continue
        seen.add(case_id)
        if row.get("risk") not in {"high", "ordinary"}:
            issues.append(f"review_risk_invalid:{case_id}")
        if row.get("review_status") not in allowed:
            issues.append(f"review_status_invalid:{case_id}")
        for field in ("security_error", "contradiction", "citation_valid"):
            if not isinstance(row.get(field), bool):
                issues.append(f"review_boolean_invalid:{case_id}:{field}")
        for field in ("answer_points_supported", "answer_points_total"):
            value = row.get(field)
            if not isinstance(value, int) or isinstance(value, bool) or value < 0:
                issues.append(f"review_count_invalid:{case_id}:{field}")
    if seen != expected_ids:
        issues.append("review_case_set_incomplete")
    if issues:
        return None, sorted(set(issues))
    return dict(review), []


def evaluate(report_path: Path, review_path: Path | None = None) -> dict[str, Any]:
    report = load_json(report_path)
    issues: list[str] = []
    if not isinstance(report, Mapping):
        raise TypeError("report_root_not_object")
    report_digest = sha256(report_path)
    rows = report.get("case_results")
    if report.get("report_version") != REPORT_VERSION:
        issues.append("report_version_invalid")
    if report.get("status") != "PASS":
        issues.append("provider_batch_not_pass")
    if report.get("real_service_acceptance") is not False or report.get("m1_connected") is not False:
        issues.append("acceptance_boundary_invalid")
    if report.get("eligible_case_count") != REQUIRED_CASES:
        issues.append("eligible_case_count_invalid")
    if report.get("excluded_case_count") != REQUIRED_EXCLUDED:
        issues.append("excluded_case_count_invalid")
    if not isinstance(rows, list) or len(rows) != REQUIRED_CASES:
        issues.append("case_result_count_invalid")
        rows = rows if isinstance(rows, list) else []
    security_exclusions = report.get("excluded_status_counts", {})
    if sum(int(value) for value in security_exclusions.values() if isinstance(value, int)) != REQUIRED_EXCLUDED:
        issues.append("excluded_case_partition_invalid")

    retrieval = {
        "embedding": _rank_metrics(rows, "embedding_hit_at_5"),
        "rerank": _rank_metrics(rows, "rerank_hit_at_5"),
    }
    evidence = _evidence_metrics(rows)
    review = None
    review_issues: list[str] = []
    if review_path is not None:
        # The sidecar must bind to this exact report; inject the digest only for
        # validation and never write it back into the user-supplied file.
        bound_report = dict(report)
        bound_report["report_sha256"] = report_digest
        review, review_issues = _load_review(review_path, bound_report)
        issues.extend(review_issues)

    semantic = {
        "status": "NOT_RUN", "reviewed_cases": 0, "passed_cases": 0,
        "not_reviewed_cases": 0, "security_errors": 0, "contradictions": 0,
        "high_risk_failures": 0, "ordinary_pass_rate": None,
        "answer_point_support_rate": None, "citation_validity_rate": None,
    }
    if review is not None:
        reviewed = review["rows"]
        passed = [row for row in reviewed if row["review_status"] == "pass"]
        ordinary = [row for row in reviewed if row["risk"] == "ordinary"]
        semantic = {
            "status": "PASS" if not review_issues and len(passed) == len(reviewed) else "INCOMPLETE",
            "reviewed_cases": len(reviewed),
            "passed_cases": len(passed),
            "not_reviewed_cases": sum(row["review_status"] == "not_reviewed" for row in reviewed),
            "security_errors": sum(row["security_error"] for row in reviewed),
            "contradictions": sum(row["contradiction"] for row in reviewed),
            "high_risk_failures": sum(
                row["review_status"] != "pass" and row.get("risk") == "high" for row in reviewed
            ),
            "ordinary_pass_rate": (
                round(sum(row["review_status"] == "pass" for row in ordinary) / len(ordinary), 6)
                if ordinary else None
            ),
            "answer_point_support_rate": round(
                sum(row["answer_points_supported"] for row in reviewed)
                / max(1, sum(row["answer_points_total"] for row in reviewed)), 6
            ),
            "citation_validity_rate": round(
                sum(row["citation_valid"] for row in reviewed) / len(reviewed), 6
            ) if reviewed else None,
        }

    gate_reasons: list[str] = []
    if issues:
        gate_reasons.extend(issues)
    if retrieval["rerank"]["recall_at_5"] != 1.0:
        gate_reasons.append("rerank_recall_below_1")
    if evidence["approved_support_delivery_rate"] is None or evidence["approved_support_delivery_rate"] < 0.95:
        gate_reasons.append("approved_support_delivery_below_0.95")
    if semantic["status"] != "PASS":
        gate_reasons.append("semantic_review_not_passed")
    if semantic["ordinary_pass_rate"] is not None and semantic["ordinary_pass_rate"] < 0.90:
        gate_reasons.append("ordinary_case_pass_rate_below_0.90")
    if semantic["citation_validity_rate"] is None or semantic["citation_validity_rate"] < 0.95:
        gate_reasons.append("citation_validity_below_0.95")
    if semantic["security_errors"] or semantic["contradictions"] or semantic["high_risk_failures"]:
        gate_reasons.append("security_or_high_risk_failure")
    return {
        "report_version": "v2-quality-gate-v1",
        "baseline_report_sha256": report_digest,
        "baseline_report": str(report_path),
        "provider_identity": report.get("provider_identity", {}),
        "retrieval": retrieval,
        "evidence": evidence,
        "semantic": semantic,
        "excluded_cases": security_exclusions,
        "answer_quality_status": "PASS" if not gate_reasons else "NOT_PASSED",
        "model_quality_claim": False,
        "real_service_acceptance": False,
        "gate_reasons": sorted(set(gate_reasons)),
    }


def review_template(report_path: Path) -> dict[str, Any]:
    """Create a metadata-only manual review sidecar skeleton."""
    report = load_json(report_path)
    rows = report.get("case_results")
    if not isinstance(rows, list):
        raise TypeError("case_result_count_invalid")
    return {
        "review_version": REVIEW_VERSION,
        "baseline_report_sha256": sha256(report_path),
        "review_status": "pending",
        "rows": [
            {
                "case_id": row.get("case_id"),
                "risk": "high",
                "review_status": "not_reviewed",
                "security_error": False,
                "contradiction": False,
                "citation_valid": False,
                "answer_points_supported": 0,
                "answer_points_total": 0,
                "issue_codes": [],
            }
            for row in rows
        ],
    }


def write_report(path: Path, payload: Mapping[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    fd, temporary = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            json.dump(payload, stream, ensure_ascii=False, indent=2)
            stream.write("\n")
            stream.flush()
            os.fsync(stream.fileno())
        os.link(temporary, path)
    finally:
        Path(temporary).unlink(missing_ok=True)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--report", type=Path, required=True)
    parser.add_argument("--review", type=Path)
    parser.add_argument("--template", type=Path, help="write a metadata-only manual review template")
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    if args.template:
        try:
            if args.template.exists():
                print(json.dumps({"status": "FAIL", "issues": ["output_exists"]}))
                return 2
            write_report(args.template, review_template(args.report))
            print(json.dumps({"status": "TEMPLATE", "output": str(args.template)}))
            return 0
        except (OSError, UnicodeDecodeError, json.JSONDecodeError, TypeError, ValueError) as error:
            print(json.dumps({"status": "FAIL", "issues": [type(error).__name__]}))
            return 2
    try:
        result = evaluate(args.report, args.review)
    except (OSError, UnicodeDecodeError, json.JSONDecodeError, TypeError, ValueError) as error:
        print(json.dumps({"status": "FAIL", "issues": [type(error).__name__]}))
        return 2
    if args.output:
        if args.output.exists():
            print(json.dumps({"status": "FAIL", "issues": ["output_exists"]}))
            return 2
        write_report(args.output, result)
    print(json.dumps({
        "answer_quality_status": result["answer_quality_status"],
        "retrieval": result["retrieval"],
        "evidence": result["evidence"],
        "semantic": result["semantic"],
        "gate_reasons": result["gate_reasons"],
        "real_service_acceptance": False,
    }, ensure_ascii=False))
    return 0 if result["answer_quality_status"] == "PASS" else 4


if __name__ == "__main__":
    raise SystemExit(main())
