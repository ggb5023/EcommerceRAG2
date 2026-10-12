#!/usr/bin/env python3
"""Validate source provenance, legal/privacy gates, and acquisition state offline.

The manifest is a decision register, not a license grant. Dataset rows may be
downloaded or admitted only after source terms, license scope, privacy review,
revision, and restricted storage are recorded. Open-source service targets and
unresolved source gaps are tracked without pretending they are datasets.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import re
from datetime import date
from pathlib import Path
from typing import Any

REQUIRED_MANIFEST_FIELDS = {
    "manifest_version",
    "recorded_at",
    "real_service_acceptance",
    "download_policy",
    "storage_policy",
    "sources",
}
REQUIRED_SOURCE_FIELDS = {
    "source_id",
    "record_type",
    "category",
    "provider",
    "dataset",
    "canonical_url",
    "dataset_card_url",
    "original_source_terms_url",
    "original_source_terms_status",
    "status",
    "revision",
    "language",
    "jurisdiction",
    "license_status",
    "privacy_status",
    "download_status",
    "download_date",
    "expected_filename",
    "actual_filename",
    "sha256",
    "restricted_storage_location",
    "allowed_use",
    "forbidden_use",
    "real_service_acceptance",
}
RECORD_TYPES = {"dataset", "open_source_service", "source_gap"}
CATEGORIES = {
    "identity_service",
    "merchant_api_service",
    "product_catalog",
    "product_retrieval_evaluation",
    "transaction_records",
    "official_faq_policy",
    "community_qa",
}
SOURCE_STATUSES = {
    "candidate",
    "pending_license_review",
    "pending_license_and_privacy_review",
    "pending_source_and_scope_review",
    "admitted",
    "target_not_integrated",
    "source_gap",
}
DOWNLOAD_STATUSES = {"not_downloaded", "downloaded", "not_applicable"}
TERMS_STATUSES = {
    "pending_direct_verification",
    "pending_dataset_card_and_original_terms",
    "pending_dataset_card_and_original_source_verification",
    "pending_license_and_attribution_review",
    "verified",
    "not_applicable",
}
LICENSE_STATUSES = {
    "pending_direct_verification",
    "pending_dataset_card_and_original_source_verification",
    "pending_selected_distribution_license_verification",
    "pending_license_and_attribution_review",
    "conflicting_public_metadata",
    "no_source_selected",
    "verified_scope_excludes_images",
    "verified_mit_non_enterprise_content_only",
}
PRIVACY_STATUSES = {
    "not_reviewed",
    "pending_field_and_privacy_review",
    "pending_privacy_and_field_review",
    "not_applicable_no_personal_data_imported",
    "reviewed_no_personal_data",
    "reviewed_redacted_personal_data",
    "reviewed_synthetic_test_accounts_only",
}
READY_PRIVACY_STATUSES = {
    "not_applicable_no_personal_data_imported",
    "reviewed_no_personal_data",
    "reviewed_redacted_personal_data",
    "reviewed_synthetic_test_accounts_only",
}
SHA256 = re.compile(r"^[0-9a-f]{64}$")
REQUIRED_FORBIDDEN_USE = {
    "merchant_policy_truth",
    "current_price_inventory_order_truth",
    "tenant_shop_permission_truth",
    "customer_reply_correctness_truth",
    "real_service_acceptance",
}


class InputIntegrityError(ValueError):
    """Raised when a public-source JSON input cannot be parsed safely."""


def _reject_duplicate_json_keys(pairs: list[tuple[str, object]]) -> dict[str, object]:
    result: dict[str, object] = {}
    for key, value in pairs:
        if key in result:
            raise InputIntegrityError(f"duplicate JSON key: {key}")
        result[key] = value
    return result


def strict_json_loads(text: str, label: str) -> Any:
    try:
        return json.loads(text, object_pairs_hook=_reject_duplicate_json_keys)
    except InputIntegrityError as error:
        raise InputIntegrityError(f"{label}: {error}") from error
    except json.JSONDecodeError as error:
        raise InputIntegrityError(f"{label} invalid JSON: {error}") from error


def load_json_value(path: Path, label: str) -> Any:
    try:
        text = path.read_bytes().decode("utf-8")
    except UnicodeDecodeError as error:
        raise InputIntegrityError(f"{label} is not valid UTF-8: {error}") from error
    except OSError as error:
        raise InputIntegrityError(f"{label} cannot be read: {error}") from error
    return strict_json_loads(text, label)


def file_sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def _non_empty_string(value: Any) -> bool:
    return isinstance(value, str) and bool(value.strip())


def _string_list(value: Any) -> bool:
    return isinstance(value, list) and bool(value) and all(_non_empty_string(item) for item in value)


def _date(value: Any) -> bool:
    if not isinstance(value, str):
        return False
    try:
        date.fromisoformat(value)
    except ValueError:
        return False
    return True


def _https_or_null(value: Any) -> bool:
    return value is None or (_non_empty_string(value) and value.startswith("https://"))


def validate_manifest(payload: Any) -> list[str]:
    errors: list[str] = []
    if not isinstance(payload, dict):
        return ["manifest must be a JSON object"]
    missing = REQUIRED_MANIFEST_FIELDS - payload.keys()
    errors.extend(f"manifest missing {field}" for field in sorted(missing))
    if payload.get("real_service_acceptance") is not False:
        errors.append("manifest real_service_acceptance must be false")
    if not _non_empty_string(payload.get("manifest_version")):
        errors.append("manifest manifest_version must be non-empty")
    if not _date(payload.get("recorded_at")):
        errors.append("manifest recorded_at must be an ISO date")
    for field in ("download_policy", "storage_policy"):
        if not _non_empty_string(payload.get(field)):
            errors.append(f"manifest {field} must be non-empty")

    sources = payload.get("sources")
    if not isinstance(sources, list) or not sources:
        return errors + ["manifest sources must be a non-empty list"]

    source_ids: set[str] = set()
    for index, source in enumerate(sources, 1):
        prefix = f"source {index}"
        if not isinstance(source, dict):
            errors.append(f"{prefix} must be an object")
            continue
        missing = REQUIRED_SOURCE_FIELDS - source.keys()
        errors.extend(f"{prefix} missing {field}" for field in sorted(missing))
        source_id = source.get("source_id")
        if not _non_empty_string(source_id):
            errors.append(f"{prefix} source_id must be non-empty")
        elif source_id in source_ids:
            errors.append(f"duplicate source_id: {source_id}")
        else:
            source_ids.add(source_id)

        record_type = source.get("record_type")
        category = source.get("category")
        status = source.get("status")
        download_status = source.get("download_status")
        for field in ("provider", "dataset", "license_status", "privacy_status"):
            if not _non_empty_string(source.get(field)):
                errors.append(f"{prefix} {field} must be non-empty")
        if not isinstance(record_type, str) or record_type not in RECORD_TYPES:
            errors.append(f"{prefix} invalid record_type")
        if not isinstance(category, str) or category not in CATEGORIES:
            errors.append(f"{prefix} invalid category")
        if not isinstance(status, str) or status not in SOURCE_STATUSES:
            errors.append(f"{prefix} invalid status")
        if not isinstance(download_status, str) or download_status not in DOWNLOAD_STATUSES:
            errors.append(f"{prefix} invalid download_status")
        if not isinstance(source.get("license_status"), str) or source.get("license_status") not in LICENSE_STATUSES:
            errors.append(f"{prefix} invalid license_status")
        if not isinstance(source.get("privacy_status"), str) or source.get("privacy_status") not in PRIVACY_STATUSES:
            errors.append(f"{prefix} invalid privacy_status")
        if not isinstance(source.get("original_source_terms_status"), str) or source.get("original_source_terms_status") not in TERMS_STATUSES:
            errors.append(f"{prefix} invalid original_source_terms_status")
        for field in ("canonical_url", "dataset_card_url", "original_source_terms_url"):
            if not _https_or_null(source.get(field)):
                errors.append(f"{prefix} {field} must be null or an https URL")
        if isinstance(record_type, str) and record_type in {"dataset", "open_source_service"}:
            for field in ("canonical_url", "dataset_card_url"):
                value = source.get(field)
                if not _non_empty_string(value) or not value.startswith("https://"):
                    errors.append(f"{prefix} {field} must be an https URL for a selected source")
        if source.get("real_service_acceptance") is not False:
            errors.append(f"{prefix} real_service_acceptance must be false")

        revision = source.get("revision")
        if revision is not None and not _non_empty_string(revision):
            errors.append(f"{prefix} revision must be null or a non-empty string")
        if not _string_list(source.get("language")):
            errors.append(f"{prefix} language must be a non-empty list of strings")
        if not _string_list(source.get("jurisdiction")):
            errors.append(f"{prefix} jurisdiction must be a non-empty list of strings")

        allowed = source.get("allowed_use")
        forbidden = source.get("forbidden_use")
        if not _string_list(allowed):
            errors.append(f"{prefix} allowed_use must be a non-empty list of strings")
        if not _string_list(forbidden):
            errors.append(f"{prefix} forbidden_use must be a non-empty list of strings")
        if _string_list(allowed) and _string_list(forbidden):
            if set(allowed) & set(forbidden):
                errors.append(f"{prefix} allowed_use overlaps forbidden_use")
            unsafe_allowed = set(allowed) & REQUIRED_FORBIDDEN_USE
            if unsafe_allowed:
                errors.append(f"{prefix} allowed_use contains prohibited truth scope: {sorted(unsafe_allowed)}")
            missing_forbidden = REQUIRED_FORBIDDEN_USE - set(forbidden)
            if missing_forbidden:
                errors.append(f"{prefix} forbidden_use is missing a required business boundary")

        sha = source.get("sha256")
        if sha is not None and (not isinstance(sha, str) or not SHA256.fullmatch(sha)):
            errors.append(f"{prefix} sha256 must be null or 64 lowercase hex characters")
        if record_type == "dataset":
            if download_status == "not_applicable":
                errors.append(f"{prefix} dataset download_status cannot be not_applicable")
            storage = source.get("restricted_storage_location")
            if not _non_empty_string(storage) or "outside_git" not in storage:
                errors.append(f"{prefix} storage must explicitly be outside_git restricted storage")
        elif record_type == "open_source_service":
            if not isinstance(category, str) or category not in {"identity_service", "merchant_api_service"}:
                errors.append(f"{prefix} open_source_service must use an identity or merchant API category")
            if download_status != "not_applicable":
                errors.append(f"{prefix} open_source_service download_status must be not_applicable")
            if status != "target_not_integrated":
                errors.append(f"{prefix} open_source_service status must be target_not_integrated")
            if sha is not None or source.get("download_date") is not None or source.get("actual_filename") is not None:
                errors.append(f"{prefix} open_source_service cannot claim a downloaded dataset")
        elif record_type == "source_gap":
            if status != "source_gap" or download_status != "not_applicable":
                errors.append(f"{prefix} source_gap must have source_gap status and not_applicable download_status")
            if source.get("canonical_url") is not None or source.get("dataset_card_url") is not None:
                errors.append(f"{prefix} source_gap must not invent a source URL")
            if sha is not None or source.get("download_date") is not None or source.get("actual_filename") is not None:
                errors.append(f"{prefix} source_gap cannot claim a downloaded dataset")

        if download_status == "not_downloaded":
            if sha is not None:
                errors.append(f"{prefix} not_downloaded source must have null sha256")
            for field in ("download_date", "actual_filename"):
                if source.get(field) is not None:
                    errors.append(f"{prefix} not_downloaded source must have null {field}")
        elif download_status == "downloaded":
            if status != "admitted":
                errors.append(f"{prefix} downloaded source must be admitted")
            if not _date(source.get("download_date")):
                errors.append(f"{prefix} downloaded source needs an ISO download_date")
            if not _non_empty_string(source.get("actual_filename")):
                errors.append(f"{prefix} downloaded source needs actual_filename")
            if not isinstance(sha, str) or not SHA256.fullmatch(sha):
                errors.append(f"{prefix} downloaded source needs a SHA-256")
            if revision is None:
                errors.append(f"{prefix} downloaded source needs revision")
            if not str(source.get("license_status", "")).startswith("verified"):
                errors.append(f"{prefix} downloaded source needs verified license_status")
            if not isinstance(source.get("privacy_status"), str) or source.get("privacy_status") not in READY_PRIVACY_STATUSES:
                errors.append(f"{prefix} downloaded source needs completed privacy review")
            if source.get("original_source_terms_status") != "verified":
                errors.append(f"{prefix} downloaded source needs verified source terms")
            if not _non_empty_string(source.get("original_source_terms_url")):
                errors.append(f"{prefix} downloaded source needs original_source_terms_url")

        if status == "admitted":
            if record_type != "dataset":
                errors.append(f"{prefix} only a dataset can be admitted")
            if not str(source.get("license_status", "")).startswith("verified"):
                errors.append(f"{prefix} admitted source needs verified license_status")
            if not isinstance(source.get("privacy_status"), str) or source.get("privacy_status") not in READY_PRIVACY_STATUSES:
                errors.append(f"{prefix} admitted source needs completed privacy review")
            if source.get("original_source_terms_status") != "verified" or not _non_empty_string(source.get("original_source_terms_url")):
                errors.append(f"{prefix} admitted source needs verified original source terms")
            if revision is None:
                errors.append(f"{prefix} admitted source needs a pinned revision")

        if (
            record_type == "dataset"
            and download_status != "downloaded"
            and status != "admitted"
            and (sha is not None or source.get("download_date") is not None or source.get("actual_filename") is not None)
        ):
            errors.append(f"{prefix} unadmitted dataset cannot claim downloaded artifacts")

    return errors


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    default = Path(__file__).resolve().with_name("public-data-sources.manifest.json")
    parser.add_argument("--manifest", type=Path, default=default)
    args = parser.parse_args()
    try:
        payload = load_json_value(args.manifest, "manifest")
    except InputIntegrityError as exc:
        print(f"FAIL input_integrity: {exc}")
        return 1
    errors = validate_manifest(payload)
    if errors:
        for error in errors:
            print(f"FAIL {error}")
        return 1
    print(f"PASS public source manifest: {len(payload['sources'])} records; sha256={file_sha256(args.manifest)}")
    for source in payload["sources"]:
        state = source["status"].upper()
        print(f"{source['source_id']}: {state}; revision={source['revision'] or 'unverified'}; license={source['license_status']}; download={source['download_status']}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
