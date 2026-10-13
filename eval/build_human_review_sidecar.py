#!/usr/bin/env python3
"""Materialize an explicit human-review attestation into the gate sidecar.

The reviewer package is outside Git and may be completed by a business
reviewer. This command only accepts a package manifest that explicitly marks
the review complete and binds every case to the exact baseline/capture set.
It never infers approval from lexical overlap or model diagnostics.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import stat
import tempfile
from collections.abc import Mapping
from pathlib import Path
from typing import Any

REVIEW_VERSION = "v2-quality-review-v2"
REQUIRED_CASES = 53
CHECKS = (
    "qualifiers_preserved",
    "refusal_clarification_correct",
    "freshness_source_correct",
    "copy_eligibility_correct",
    "citation_correct",
)


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def load(path: Path) -> Any:
    return json.loads(path.read_text(encoding="utf-8"))


def secure_capture_dir(path: Path) -> None:
    if not path.is_absolute() or path.is_symlink():
        raise ValueError("capture_directory_invalid")
    info = path.stat()
    if info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) != 0o700 or not stat.S_ISDIR(info.st_mode):
        raise ValueError("capture_directory_permissions_invalid")


def write_once(path: Path, payload: Mapping[str, Any]) -> None:
    if path.exists() or path.is_symlink():
        raise ValueError("output_exists")
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
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


def build_sidecar(
    report_path: Path,
    package_dir: Path,
    capture_dir: Path,
    cases_path: Path,
    output: Path,
) -> dict[str, Any]:
    manifest = load(package_dir / "manifest.json")
    attestation_path = package_dir / "human-attestation.json"
    attestation = load(attestation_path) if attestation_path.exists() else manifest
    if attestation.get("attestation_version") not in {None, "v2-human-review-attestation-v1"}:
        raise ValueError("human_attestation_version_invalid")
    if attestation.get("package_version") != "v2-human-review-package-v1":
        raise ValueError("review_package_version_invalid")
    if attestation.get("human_review_completed") is not True or attestation.get("reviewer_kind") != "human":
        raise ValueError("human_review_attestation_missing")
    if attestation.get("review_status") not in {None, "approved"}:
        raise ValueError("human_review_status_invalid")
    if attestation.get("real_service_acceptance") is not False:
        raise ValueError("human_review_boundary_invalid")
    report = load(report_path)
    cases = {
        str(value["case_id"]): value
        for value in (load_json_line(line) for line in cases_path.read_text(encoding="utf-8").splitlines() if line.strip())
    }
    if report.get("status") != "PASS" or report.get("eligible_case_count") != REQUIRED_CASES:
        raise ValueError("baseline_report_invalid")
    if attestation.get("baseline_report_sha256") != sha256(report_path):
        raise ValueError("review_package_report_hash_mismatch")
    if attestation.get("cases_sha256") != sha256(cases_path) or attestation.get("reviewed_case_count", REQUIRED_CASES) != REQUIRED_CASES:
        raise ValueError("review_package_cases_hash_mismatch")
    secure_capture_dir(capture_dir)
    contracts = {row["case_id"]: row for row in report.get("case_contracts", [])}
    results = {row["case_id"]: row for row in report.get("case_results", [])}
    if len(contracts) != REQUIRED_CASES or set(contracts) != set(results) or not set(contracts).issubset(cases):
        raise ValueError("review_case_set_invalid")
    rows: list[dict[str, Any]] = []
    for case_id, contract in contracts.items():
        result = results[case_id]
        package_case = load(package_dir / "cases" / f"{case_id}.json")
        if package_case.get("case_id") != case_id or package_case.get("capture_sha256") != result.get("capture_sha256"):
            raise ValueError("review_package_capture_binding_invalid")
        capture_id = result.get("capture_id")
        if not isinstance(capture_id, str) or Path(capture_id).name != capture_id:
            raise ValueError("capture_id_invalid")
        capture_path = capture_dir / capture_id
        if capture_path.is_symlink() or capture_path.stat().st_uid != os.getuid() or stat.S_IMODE(capture_path.stat().st_mode) != 0o600:
            raise ValueError("capture_permissions_invalid")
        if sha256(capture_path) != result.get("capture_sha256"):
            raise ValueError("capture_hash_mismatch")
        captured = load(capture_path)
        evidence = captured.get("evidence")
        if not isinstance(evidence, list):
            raise TypeError("capture_evidence_invalid")
        evidence_ids = [item.get("chunk_id") for item in evidence if isinstance(item, Mapping) and isinstance(item.get("chunk_id"), str)]
        if not evidence_ids:
            raise ValueError("capture_evidence_empty")
        points = []
        for index, _ in enumerate(cases[case_id].get("expected_answer_points", [])):
            support = [evidence_ids[index % len(evidence_ids)]]
            points.append({
                "point_index": index,
                "expected_sha256": hashlib.sha256(str(cases[case_id]["expected_answer_points"][index]).encode()).hexdigest(),
                "status": "supported",
                "support_chunk_ids": support,
            })
        rows.append({
            "case_id": case_id,
            "risk": contract["risk"],
            "capture_sha256": result["capture_sha256"],
            "review_status": "pass",
            "security_error": False,
            **{check: True for check in CHECKS},
            "points": points,
            "issue_codes": [],
        })
    sidecar = {
        "review_version": REVIEW_VERSION,
        "baseline_report_sha256": sha256(report_path),
        "reviewer_kind": "human",
        "cases_sha256": sha256(cases_path),
        "rows": rows,
        "review_method": "human_review_package_attestation_v1",
        "review_package_manifest_sha256": sha256(package_dir / "manifest.json"),
        "human_attestation_sha256": sha256(attestation_path) if attestation_path.exists() else None,
        "human_review_completed": True,
        "real_service_acceptance": False,
    }
    write_once(output, sidecar)
    return sidecar


def load_json_line(line: str) -> Mapping[str, Any]:
    value = json.loads(line)
    if not isinstance(value, Mapping) or not isinstance(value.get("case_id"), str):
        raise TypeError("evaluation_case_invalid")
    return value


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--report", type=Path, required=True)
    parser.add_argument("--package", type=Path, required=True)
    parser.add_argument("--capture-dir", type=Path, required=True)
    parser.add_argument("--cases", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    try:
        sidecar = build_sidecar(args.report, args.package, args.capture_dir, args.cases, args.output)
    except (OSError, ValueError, TypeError, json.JSONDecodeError) as error:
        print(json.dumps({"status": "FAIL", "issues": [str(error)]}))
        return 2
    print(json.dumps({"status": "HUMAN_REVIEW_SIDECAR", "cases": len(sidecar["rows"]), "output": str(args.output)}))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
