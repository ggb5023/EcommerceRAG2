#!/usr/bin/env python3
"""Exercise the V2 artifact cleanup boundary with a bounded fault.

This offline smoke uses the content-addressed bundle and object-store code used
by the V2 ingestion worker. One incomplete delete is injected, then later
cleanup must continue and leave no objects. It does not start a legacy server,
use a database, or call a provider.
"""
from __future__ import annotations

import argparse
import json
import os
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
PYTHON_ROOT = ROOT / "python"
if str(PYTHON_ROOT) not in sys.path:
    sys.path.insert(0, str(PYTHON_ROOT))

from app.ingest.artifacts import ArtifactInput, store_artifact_bundle
from app.ingest.bundles import _delete_bundles
from app.storage import FilesystemObjectStore, ObjectStoreError


def assert_missing(store, bundle):
    for record in bundle.all_records:
        try:
            store.head(record.object_key)
        except ObjectStoreError:
            continue
        raise RuntimeError("cleanup left an artifact object behind")


def run(python: Path) -> dict[str, object]:
    if not python.is_file():
        raise FileNotFoundError("python virtualenv is unavailable")
    with tempfile.TemporaryDirectory(prefix="ecr-cleanup-fault-") as directory:
        store = FilesystemObjectStore(Path(directory))
        inputs = (
            ArtifactInput("raw", b"synthetic raw"),
            ArtifactInput("parsed", b"synthetic parsed", "application/json"),
            ArtifactInput("chunks", b"synthetic chunks", "application/jsonl"),
            ArtifactInput("parse-report", b"synthetic report", "application/json"),
        )
        first = store_artifact_bundle(store, tenant_id="fault-tenant", shop_id="fault-shop", document_version_id="v-11111111111111111111", artifacts=inputs)
        second = store_artifact_bundle(store, tenant_id="fault-tenant", shop_id="fault-shop", document_version_id="v-22222222222222222222", artifacts=inputs)
        class FaultStore:
            failures = 1

            def delete(self, key):
                if self.failures:
                    self.failures -= 1
                    raise ObjectStoreError("injected storage deletion failure")
                store.delete(key)

        faulty = FaultStore()
        # Exercise the shared bundler's real cleanup, including partial progress
        # and idempotent deletion of objects already removed by earlier attempts.
        if _delete_bundles(faulty, [first]):
            raise RuntimeError("cleanup hid a failed deletion")
        if not any((store.root / record.object_key).exists() for record in first.all_records):
            raise RuntimeError("injection did not leave an observable orphan")
        if not _delete_bundles(faulty, [second]) or not _delete_bundles(faulty, [first]):
            raise RuntimeError("cleanup did not recover after the injected failure")
        assert_missing(store, first)
        assert_missing(store, second)
    return {"status": "PASS", "profile": "v2_ingest_artifact_fixture", "failure_budget": 1, "bundles_called": 3, "first_response": "injected_incomplete", "later_cleanup": "continued_and_recovered", "raw_content_saved": False, "real_service_acceptance": False, "online_provider_called": False, "public_crawler_called": False}


def main():
    parser = argparse.ArgumentParser()
    default_env = Path(os.environ.get("UV_PROJECT_ENVIRONMENT") or ROOT / "python/.venv")
    parser.add_argument("--python", type=Path, default=default_env / "bin/python")
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    try:
        report = run(args.python)
    except Exception as exc:  # noqa: BLE001 - fixed redacted diagnostic
        print(f"FAIL cleanup fault smoke: {type(exc).__name__}", file=sys.stderr)
        return 1
    rendered = json.dumps(report, ensure_ascii=False, sort_keys=True, indent=2) + "\n"
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(rendered, encoding="utf-8")
    print(rendered, end="")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
