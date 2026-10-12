"""Validate active V2 submission/parser contracts without legacy RPCs."""
from __future__ import annotations

import hashlib
from dataclasses import replace

import pytest
from app.ingest.package_validation import _inspect_package
from app.ingest.worker import (
    ClaimedFile,
    PackageValidationError,
    parse_persisted_package,
)
from rag.v1 import rag_pb2


class AbortContext:
    def abort(self, code, details):
        raise ValueError(details)


def package(content=b"# Shipping\n\nOrders leave within two days.\n", path="docs/shipping.md"):
    sha = hashlib.sha256(content).hexdigest()
    manifest = ("schema_version: 1\npipeline_version: v2-parser-test\nsource:\n  id: docs\n  name: Docs\n"
                f"documents:\n  - document_id: shipping\n    title: Shipping\n    path: {path}\n"
                f"    format: markdown\n    sha256: {sha}\n    disclosure_class: external_allowed\n"
                "    tenant_id: forged-tenant\n    shop_id: forged-shop\n")
    return rag_pb2.ValidatePackageRequest(manifest_yaml=manifest,
        files=[rag_pb2.PackageFile(path=path, content=content)], tenant_id="server-tenant", shop_id="server-shop")


def test_submission_validation_matches_worker_binding_and_server_scope():
    request = package()
    validated = _inspect_package(request, AbortContext())
    assert validated["manifest_sha256"] == hashlib.sha256(request.manifest_yaml.encode()).hexdigest()
    assert validated["uploaded"] == {request.files[0].path: bytes(request.files[0].content)}
    doc = validated["documents"][0]
    claimed = ClaimedFile(1, 1, doc["document_id"], doc["path"], doc["source_hash"], request.files[0].content)
    parsed, dataset_hash = parse_persisted_package(request.manifest_yaml, (claimed,), tenant=request.tenant_id, shop=request.shop_id)
    chunks = parsed["documents"][0]["chunks"]
    assert chunks and len(dataset_hash) == 64
    assert all(chunk["metadata"]["tenant_id"] == "server-tenant" and chunk["metadata"]["shop_id"] == "server-shop" for chunk in chunks)
    assert parsed["documents"][0]["spec"] == doc


@pytest.mark.parametrize("path", ["../escape.md", "/absolute.md", "docs/../escape.md", "docs\\escape.md", "docs//shipping.md"])
def test_submission_rejects_unsafe_paths(path):
    with pytest.raises(ValueError, match="unsafe"):
        _inspect_package(package(path=path), AbortContext())


@pytest.mark.parametrize("fault", ["hash", "unreferenced", "duplicate_file", "duplicate_document", "format"])
def test_submission_rejects_integrity_and_binding_failures(fault):
    request = package()
    if fault == "hash":
        request.files[0].content = b"changed bytes"
    elif fault == "unreferenced":
        request.files.add(path="extra.md", content=b"extra")
    elif fault == "duplicate_file":
        request.files.add().CopyFrom(request.files[0])
    elif fault == "duplicate_document":
        request.manifest_yaml += request.manifest_yaml.split("documents:\n", 1)[1]
    else:
        request.manifest_yaml = request.manifest_yaml.replace("format: markdown", "format: csv")
    with pytest.raises(ValueError):
        _inspect_package(request, AbortContext())


def test_worker_rejects_empty_document_before_any_persistence():
    request = package(content=b"   \n")
    spec = _inspect_package(request, AbortContext())["documents"][0]
    claimed = ClaimedFile(1, 1, "shipping", spec["path"], spec["source_hash"], request.files[0].content)
    with pytest.raises(PackageValidationError):
        parse_persisted_package(request.manifest_yaml, (claimed,), tenant=request.tenant_id, shop=request.shop_id)


def test_worker_rechecks_claimed_source_hash():
    request = package()
    spec = _inspect_package(request, AbortContext())["documents"][0]
    claimed = ClaimedFile(1, 1, "shipping", spec["path"], spec["source_hash"], request.files[0].content)
    with pytest.raises(PackageValidationError):
        parse_persisted_package(request.manifest_yaml, (replace(claimed, content=b"tampered"),), tenant=request.tenant_id, shop=request.shop_id)
