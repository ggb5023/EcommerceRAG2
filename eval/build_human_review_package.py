#!/usr/bin/env python3
"""Build a restricted, hash-bound package for human semantic review.

The package is deliberately written outside the repository.  It contains the
query, frozen answer points, generated answer points, and the evidence shown
to the model so a reviewer can assess semantics, qualifiers, citations and
customer-copy eligibility.  It never changes the frozen cases, baseline
report, captures, or engineering review.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import stat
from collections.abc import Mapping
from pathlib import Path
from typing import Any

from quality_capture import secure_directory

REVIEW_PACKAGE_VERSION = "v2-human-review-package-v1"
REQUIRED_CASES = 53


def _sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def _load_json(path: Path) -> Any:
    return json.loads(path.read_text(encoding="utf-8"))


def _load_cases(path: Path) -> dict[str, dict[str, Any]]:
    result: dict[str, dict[str, Any]] = {}
    for line in path.read_text(encoding="utf-8").splitlines():
        if not line.strip():
            continue
        value = json.loads(line)
        if not isinstance(value, dict) or not isinstance(value.get("case_id"), str):
            raise TypeError("evaluation_case_invalid")
        case_id = value["case_id"]
        if case_id in result:
            raise ValueError("evaluation_case_duplicate")
        result[case_id] = value
    return result


def _secure_output(path: Path) -> Path:
    if not path.is_absolute() or path.is_relative_to(Path(__file__).resolve().parents[1]):
        raise ValueError("review_package_must_be_outside_repository")
    for parent in (path, *path.parents):
        if parent.is_symlink():
            raise ValueError("review_package_symlink")
    path.mkdir(mode=0o700, parents=True, exist_ok=False)
    info = path.stat()
    if info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) != 0o700:
        raise ValueError("review_package_requires_owned_0700")
    return path


def _write_once(path: Path, value: Any) -> None:
    encoded = (json.dumps(value, ensure_ascii=False, indent=2, sort_keys=True) + "\n").encode()
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, "wb") as stream:
        stream.write(encoded)
        stream.flush()
        os.fsync(stream.fileno())


def _capture(capture_dir: Path, result: Mapping[str, Any]) -> tuple[dict[str, Any], str]:
    name = result.get("capture_id")
    if not isinstance(name, str) or Path(name).name != name or not name.endswith(".json"):
        raise ValueError("answer_capture_missing")
    path = capture_dir / name
    if path.is_symlink():
        raise ValueError("answer_capture_symlink")
    info = path.stat()
    if info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) != 0o600:
        raise ValueError("answer_capture_permissions_invalid")
    digest = _sha256(path)
    if result.get("capture_sha256") != digest:
        raise ValueError("answer_capture_hash_mismatch")
    value = _load_json(path)
    if not isinstance(value, dict) or value.get("case_id") != result.get("case_id"):
        raise ValueError("answer_capture_identity_mismatch")
    return value, digest


def build_package(report_path: Path, cases_path: Path, capture_dir: Path, output: Path) -> dict[str, Any]:
    report = _load_json(report_path)
    cases = _load_cases(cases_path)
    if not isinstance(report, dict) or report.get("status") != "PASS":
        raise ValueError("baseline_report_not_pass")
    rows = report.get("case_results")
    if not isinstance(rows, list) or len(rows) != REQUIRED_CASES:
        raise ValueError("baseline_case_count_invalid")
    secure_directory(capture_dir)
    package = _secure_output(output)
    case_dir = package / "cases"
    case_dir.mkdir(mode=0o700)
    entries: list[dict[str, Any]] = []
    seen: set[str] = set()
    for result in rows:
        if not isinstance(result, Mapping) or not isinstance(result.get("case_id"), str):
            raise TypeError("baseline_case_invalid")
        case_id = result["case_id"]
        if case_id in seen or case_id not in cases:
            raise ValueError("baseline_case_set_invalid")
        seen.add(case_id)
        captured, capture_sha256 = _capture(capture_dir, result)
        case = cases[case_id]
        structured = captured.get("structured")
        generated = structured.get("answer_points", []) if isinstance(structured, Mapping) else []
        if not isinstance(generated, list) or any(not isinstance(item, str) for item in generated):
            raise ValueError("generated_answer_points_invalid")
        evidence = captured.get("evidence", [])
        if not isinstance(evidence, list):
            raise TypeError("captured_evidence_invalid")
        entry = {
            "package_version": REVIEW_PACKAGE_VERSION,
            "case_id": case_id,
            "risk": "high" if set(case.get("tags", [])) & {"policy", "freshness", "unanswerable", "negative", "safety_and_routing"} else "ordinary",
            "query": case.get("query", ""),
            "query_sha256": captured.get("query_sha256"),
            "expected_answer_points": case.get("expected_answer_points", []),
            "expected_document_ids": case.get("expected_doc_ids", []),
            "tags": case.get("tags", []),
            "business_date": case.get("business_date"),
            "generated_answer_points": generated,
            "capture_id": result.get("capture_id"),
            "capture_sha256": capture_sha256,
            "evidence": evidence,
            "review_fields": {
                "review_status": "not_reviewed",
                "security_error": False,
                "qualifiers_preserved": False,
                "refusal_clarification_correct": False,
                "freshness_source_correct": False,
                "copy_eligibility_correct": False,
                "citation_correct": False,
                "points": [
                    {"point_index": i, "status": "omitted", "support_chunk_ids": []}
                    for i, _ in enumerate(case.get("expected_answer_points", []))
                ],
                "issue_codes": [],
            },
        }
        _write_once(case_dir / f"{case_id}.json", entry)
        entries.append({
            "case_id": case_id,
            "risk": entry["risk"],
            "review_file": f"cases/{case_id}.json",
            "capture_id": entry["capture_id"],
            "capture_sha256": capture_sha256,
        })
    if len(seen) != REQUIRED_CASES:
        raise ValueError("baseline_case_set_incomplete")
    manifest = {
        "package_version": REVIEW_PACKAGE_VERSION,
        "baseline_report_sha256": _sha256(report_path),
        "cases_sha256": _sha256(cases_path),
        "case_count": len(entries),
        "high_risk_count": sum(item["risk"] == "high" for item in entries),
        "ordinary_count": sum(item["risk"] == "ordinary" for item in entries),
        "reviewer_kind": "human",
        "human_review_completed": False,
        "real_service_acceptance": False,
        "instructions": [
            "Review each case against only the frozen expected answer points and delivered evidence.",
            "Record semantic support, omissions, contradictions, distortions, qualifiers, freshness, citations and customer-copy eligibility.",
            "Do not edit query, expected points, evidence, hashes or capture references.",
            "The package is a review input; it is not an approval until a human sidecar is produced and validated.",
        ],
        "entries": entries,
    }
    _write_once(package / "manifest.json", manifest)
    return manifest


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--report", type=Path, required=True)
    parser.add_argument("--cases", type=Path, required=True)
    parser.add_argument("--capture-dir", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    try:
        result = build_package(args.report, args.cases, args.capture_dir, args.output)
    except (OSError, ValueError, TypeError, json.JSONDecodeError) as error:
        print(json.dumps({"status": "FAIL", "issues": [str(error)]}))
        return 2
    print(json.dumps({"status": "READY_FOR_HUMAN_REVIEW", "package": str(args.output), "case_count": result["case_count"]}, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
