#!/usr/bin/env python3
"""Derive a build_eval_revision map from an approved alignment artifact.

The approval records the mapping; the revision map records the eval-set version
bump.  This adapter keeps the two schemas explicit and refuses to run on an
alignment that is not fully approved.
"""
from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
from typing import Any


def _sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def _load(path: Path) -> Any:
    return json.loads(path.read_text(encoding="utf-8"))


def _cases(path: Path) -> list[dict[str, Any]]:
    rows: list[dict[str, Any]] = []
    for line_number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        if not line.strip():
            continue
        value = json.loads(line)
        if not isinstance(value, dict):
            raise TypeError(f"case line {line_number} is not an object")
        case_id = value.get("case_id")
        points = value.get("expected_answer_points")
        if not isinstance(case_id, str) or not case_id:
            raise ValueError(f"case line {line_number} has invalid case_id")
        if not isinstance(points, list) or not points or not all(isinstance(point, str) and point.strip() for point in points):
            raise ValueError(f"case {case_id} has invalid expected_answer_points")
        rows.append(value)
    return rows


def build_revision_map(
    approval_path: Path,
    cases_path: Path,
    metadata_path: Path,
    target_version: str,
) -> dict[str, Any]:
    approval = _load(approval_path)
    metadata = _load(metadata_path)
    cases = _cases(cases_path)
    if not isinstance(approval, dict) or approval.get("status") != "APPROVED":
        raise ValueError("alignment approval must be APPROVED")
    if approval.get("real_service_acceptance") is not False:
        raise ValueError("alignment approval must not claim real service acceptance")
    if approval.get("cases_sha256") != _sha256(cases_path):
        raise ValueError("approval cases_sha256 does not match the source cases")
    source_version = approval.get("eval_set_version")
    if not isinstance(source_version, str) or source_version != metadata.get("eval_set_version"):
        raise ValueError("approval eval_set_version does not match metadata")
    if not isinstance(target_version, str) or not target_version or target_version == source_version:
        raise ValueError("target version must be a new non-empty version")
    return {
        "revision_map_version": "eval-revision-v1",
        "change_kind": "alignment_only_no_point_changes",
        "source_cases_sha256": _sha256(cases_path),
        "source_eval_set_version": source_version,
        "target_eval_set_version": target_version,
        "alignment_sha256": _sha256(approval_path),
        "real_service_acceptance": False,
        "changes": [
            {"case_id": case["case_id"], "fields": {"expected_answer_points": case["expected_answer_points"]}}
            for case in cases
        ],
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--approval", type=Path, required=True)
    parser.add_argument("--cases", type=Path, required=True)
    parser.add_argument("--metadata", type=Path, required=True)
    parser.add_argument("--target-version", required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    revision_map = build_revision_map(args.approval, args.cases, args.metadata, args.target_version)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(revision_map, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({"source": revision_map["source_eval_set_version"],
                      "target": revision_map["target_eval_set_version"],
                      "cases": len(revision_map["changes"]),
                      "change_kind": revision_map["change_kind"]}, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
