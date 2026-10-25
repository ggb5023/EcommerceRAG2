"""Smoke the deterministic revision build chain: fixture -> proposal."""
from __future__ import annotations

import importlib.util
import json
import sys
from pathlib import Path

import yaml

REPO = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(REPO / "python"))

MANIFEST = REPO / "data/synthetic/ecommerce-m2-aligned-v1/manifest.yaml"
CASES = REPO / "eval/synthetic_cases.jsonl"


def _load(name: str):
    spec = importlib.util.spec_from_file_location(name, REPO / "eval" / f"{name}.py")
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def test_fixture_to_proposal_is_complete(tmp_path: Path) -> None:
    from app.ingest.pipeline import load_manifest_index

    fixture = _load("build_aligned_fixture")
    proposal_module = _load("build_alignment_proposal")

    index = load_manifest_index(MANIFEST)
    documents = fixture.build_documents(index.chunks)
    corpus = tmp_path / "documents.jsonl"
    corpus.write_text(
        "\n".join(json.dumps(documents[key], ensure_ascii=False, sort_keys=True) for key in sorted(documents)) + "\n",
        encoding="utf-8",
    )

    expected_documents = yaml.safe_load(MANIFEST.read_text(encoding="utf-8"))["documents"]
    assert len(documents) == len(expected_documents)

    proposal = proposal_module.build_proposal(CASES, MANIFEST, corpus)
    assert proposal["status"] == "PENDING_REVIEW"
    assert proposal["issues"] == []
    assert proposal["case_count"] == proposal["mapped_case_count"]
    for check in proposal["case_checks"]:
        assert check["document_checks"], check["case_id"]
        mapped = {item["expected_doc_id"] for item in check["document_checks"]}
        assert mapped == set(check["expected_doc_ids"]), check["case_id"]
        assert all(item["source_document_id"] for item in check["document_checks"])
