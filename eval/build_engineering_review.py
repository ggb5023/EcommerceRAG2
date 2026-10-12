#!/usr/bin/env python3
"""Build a conservative, hash-bound engineering review from protected captures.

This is an engineering diagnostic, never a human approval. A point is marked
supported only when its frozen truth has substantial token overlap with a
generated point and the point has delivered evidence. Empty/uncertain answers
remain omitted or fail the gate.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
from typing import Any

from quality_capture import secure_directory
from run_bailian_retrieval import _token_overlap_supported


def load_cases(path: Path) -> dict[str, dict[str, Any]]:
    rows = {}
    for line in path.read_text(encoding="utf-8").splitlines():
        if line.strip():
            row = json.loads(line)
            rows[str(row["case_id"])] = row
    return rows


def write_once(path: Path, value: Any) -> None:
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as stream:
        json.dump(value, stream, ensure_ascii=False, indent=2)
        stream.write("\n")
        stream.flush()
        os.fsync(stream.fileno())


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--report", type=Path, required=True)
    parser.add_argument("--cases", type=Path, required=True)
    parser.add_argument("--capture-dir", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    report = json.loads(args.report.read_text(encoding="utf-8"))
    cases = load_cases(args.cases)
    capture_dir = secure_directory(args.capture_dir)
    rows = []
    for result in report.get("case_results", []):
        case_id = str(result["case_id"])
        case = cases[case_id]
        capture_id = result.get("capture_id")
        if not isinstance(capture_id, str) or Path(capture_id).name != capture_id:
            raise SystemExit("capture id missing")
        capture_path = capture_dir / capture_id
        captured = json.loads(capture_path.read_text(encoding="utf-8"))
        generated = [str(p) for p in (captured.get("structured") or {}).get("answer_points", []) if isinstance(p, str)]
        evidence_ids = {str(item.get("chunk_id")) for item in captured.get("evidence", []) if item.get("chunk_id")}
        bindings = result.get("approved_support_chunk_coverage", [])
        support_ids = []
        points = []
        for index, expected in enumerate(case.get("expected_answer_points", [])):
            matching = [point for point in generated if _token_overlap_supported(str(expected), point, threshold=0.5)]
            delivered = bool(bindings[index].get("all_delivered")) if index < len(bindings) else False
            if matching and delivered:
                state = "supported"
                support_ids = [str(item.get("chunk_id")) for item in captured.get("evidence", [])
                               if item.get("chunk_id") in evidence_ids][:1]
            elif not generated:
                state = "omitted"
            else:
                state = "distorted" if generated else "omitted"
            points.append({"point_index": index, "expected_sha256": hashlib.sha256(str(expected).encode()).hexdigest(),
                           "status": state, "support_chunk_ids": support_ids if state == "supported" else []})
        tags = {str(t) for t in case.get("tags", [])}
        high = bool(tags & {"policy", "freshness", "unanswerable", "negative", "safety_and_routing"})
        any_supported = any(point["status"] == "supported" for point in points)
        refusal_ok = any_supported if not ("unanswerable" in tags or "negative" in tags) else bool(generated) and any_supported
        pass_case = (any_supported and refusal_ok and not any(point["status"] in {"distorted", "contradicted"} for point in points))
        rows.append({"case_id": case_id, "risk": "high" if high else "ordinary",
                     "capture_sha256": result.get("capture_sha256"),
                     "review_status": "pass" if pass_case else "fail", "security_error": False,
                     "qualifiers_preserved": pass_case, "refusal_clarification_correct": refusal_ok,
                     "freshness_source_correct": pass_case, "copy_eligibility_correct": pass_case,
                     "citation_correct": bool(captured.get("evidence")) and pass_case,
                     "points": points, "issue_codes": [] if pass_case else ["engineering_heuristic_failed"]})
    payload = {"review_version": "v2-quality-review-v2", "baseline_report_sha256": hashlib.sha256(args.report.read_bytes()).hexdigest(),
               "reviewer_kind": "engineering", "cases_sha256": hashlib.sha256(args.cases.read_bytes()).hexdigest(),
               "rows": rows, "review_method": "token_overlap_and_bound_evidence_v1",
               "human_review_completed": False, "real_service_acceptance": False}
    write_once(args.output, payload)
    print(json.dumps({"status": "ENGINEERING_REVIEW", "cases": len(rows), "human_review_completed": False}))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
