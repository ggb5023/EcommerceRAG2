from __future__ import annotations

import json
import sys
from pathlib import Path

EVAL_DIR = Path(__file__).parent
sys.path.insert(0, str(EVAL_DIR))

from validate_public_sources import validate_manifest

MANIFEST_PATH = EVAL_DIR / "public-data-sources.manifest.json"


def manifest() -> dict:
    return json.loads(MANIFEST_PATH.read_text(encoding="utf-8"))


def errors_for(source_id: str, mutate) -> list[str]:
    payload = manifest()
    source = next(item for item in payload["sources"] if item["source_id"] == source_id)
    mutate(source)
    return validate_manifest(payload)


def test_current_public_source_register_is_valid() -> None:
    assert validate_manifest(manifest()) == []


def test_unverified_license_cannot_be_marked_downloaded_or_admitted() -> None:
    def claim_download(source: dict) -> None:
        source.update(
            {
                "status": "admitted",
                "revision": "candidate-revision",
                "download_status": "downloaded",
                "download_date": "2026-10-12",
                "actual_filename": "candidate.jsonl",
                "sha256": "a" * 64,
            }
        )

    errors = errors_for("amazon-esci", claim_download)
    assert any("admitted source needs verified license_status" in error for error in errors)
    assert any("downloaded source needs verified license_status" in error for error in errors)


def test_unreviewed_privacy_cannot_be_marked_downloaded_or_admitted() -> None:
    def claim_download(source: dict) -> None:
        source.update(
            {
                "status": "admitted",
                "revision": "candidate-revision",
                "license_status": "verified_scope_excludes_images",
                "original_source_terms_status": "verified",
                "privacy_status": "pending_privacy_and_field_review",
                "download_status": "downloaded",
                "download_date": "2026-10-12",
                "actual_filename": "candidate.jsonl",
                "sha256": "b" * 64,
            }
        )

    errors = errors_for("uci-online-retail-ii", claim_download)
    assert any("admitted source needs completed privacy review" in error for error in errors)
    assert any("downloaded source needs completed privacy review" in error for error in errors)


def test_uci_conflicting_license_metadata_stays_a_candidate() -> None:
    payload = manifest()
    uci = next(item for item in payload["sources"] if item["source_id"] == "uci-online-retail-ii")
    assert uci["status"] == "pending_license_and_privacy_review"
    assert uci["license_status"] == "conflicting_public_metadata"
    assert uci["download_status"] == "not_downloaded"
    assert validate_manifest(payload) == []


def test_source_cannot_extend_a_prohibited_business_truth_scope() -> None:
    errors = errors_for(
        "open-food-facts-products",
        lambda source: source["allowed_use"].append("merchant_policy_truth"),
    )
    assert any("allowed_use contains prohibited truth scope" in error for error in errors)


def test_service_target_cannot_claim_a_dataset_download() -> None:
    errors = errors_for(
        "medusa-2.21.2-rest-local-target",
        lambda source: source.update({"download_status": "downloaded", "sha256": "c" * 64}),
    )
    assert any("open_source_service download_status must be not_applicable" in error for error in errors)
    assert any("open_source_service cannot claim a downloaded dataset" in error for error in errors)


def test_service_integration_requires_distribution_and_nonproduction_evidence() -> None:
    errors = errors_for('medusa-2.21.2-rest-local-target',
                        lambda source: source.update(status='local_integration_passed', revision=None))
    assert any('pinned service artifact' in error for error in errors)
    assert any('non-production acceptance evidence' in error for error in errors)
    assert any('verified version' in error for error in errors)


def test_service_license_alone_cannot_claim_runtime_acceptance() -> None:
    def mutation(source):
        source.update(status='local_integration_passed', revision='26.8.1',
                      original_source_terms_status='verified',
                      license_status='verified_apache_2_0_selected_distribution',
                      service_artifact={'version':'26.8.1','sha256':'a'*64,'license_sha256':'b'*64},
                      local_integration_evidence={'status':'PASS','report_sha256':'c'*64,'real_service_acceptance':True})
    assert any('non-production acceptance evidence' in error for error in errors_for('keycloak-oidc-local-target',mutation))


def test_source_gap_cannot_be_promoted_without_a_real_source_record() -> None:
    errors = errors_for(
        "cn-official-faq-policy-source-gap",
        lambda source: source.update({"status": "admitted", "download_status": "downloaded"}),
    )
    assert any("source_gap must have source_gap status" in error for error in errors)
    assert any("only a dataset can be admitted" in error for error in errors)


def test_manifest_accepts_new_source_ids_without_validator_code_changes() -> None:
    payload = manifest()
    candidate = dict(payload["sources"][2])
    candidate.update(
        {
            "source_id": "future-regional-food-catalog",
            "provider": "Example public repository",
            "dataset": "Future candidate",
        }
    )
    payload["sources"].append(candidate)
    assert validate_manifest(payload) == []


def test_malformed_types_fail_as_validation_errors_not_exceptions() -> None:
    payload = manifest()
    source = payload["sources"][0]
    source.update(
        {
            "record_type": [],
            "category": {},
            "status": [],
            "download_status": {},
            "license_status": [],
            "privacy_status": {},
            "original_source_terms_status": [],
            "allowed_use": [{}],
            "forbidden_use": [{}],
        }
    )
    uci = next(item for item in payload["sources"] if item["source_id"] == "uci-online-retail-ii")
    uci.update(
        {
            "status": "admitted",
            "revision": "candidate-revision",
            "license_status": "verified_scope_excludes_images",
            "original_source_terms_status": "verified",
            "privacy_status": [],
            "download_status": "downloaded",
            "download_date": "2026-10-12",
            "actual_filename": "candidate.xlsx",
            "sha256": "d" * 64,
        }
    )
    errors = validate_manifest(payload)
    assert any("invalid record_type" in error for error in errors)
    assert any("invalid download_status" in error for error in errors)
    assert any("allowed_use must be a non-empty list" in error for error in errors)
