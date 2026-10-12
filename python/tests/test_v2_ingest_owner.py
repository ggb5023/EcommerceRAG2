from __future__ import annotations

import hashlib

import pytest
from app.ingest.database import SubmittedJob, package_payload_hash
from app.ingest.worker import ClaimedFile, parse_persisted_package
from app.rpc.ingest import IngestService
from rag.v1 import rag_pb2


def _package():
    content = b"# Shipping\n\nOrders leave within two business days.\n"
    source_hash = hashlib.sha256(content).hexdigest()
    manifest = (
        "schema_version: 1\n"
        "source:\n"
        "  id: docs\n"
        "  name: Docs\n"
        "pipeline_version: pipe-v1\n"
        "documents:\n"
        "  - document_id: shipping\n"
        "    title: Shipping\n"
        "    path: shipping.md\n"
        "    format: markdown\n"
        f"    sha256: {source_hash}\n"
        "    disclosure_class: external_allowed\n"
    )
    return manifest, content


def test_package_hash_is_order_independent():
    assert package_payload_hash(7, "shop", "a" * 64, {"b": b"2", "a": b"1"}) == package_payload_hash(
        7, "shop", "a" * 64, {"a": b"1", "b": b"2"}
    )


def test_python_parser_produces_all_chunks_before_persistence():
    manifest, content = _package()
    parsed, dataset_sha = parse_persisted_package(
        manifest,
        (ClaimedFile(1, 1, "shipping", "shipping.md", hashlib.sha256(content).hexdigest(), content),),
        tenant="v2-demo-tenant",
        shop="shop-demo",
    )
    assert parsed["documents"][0]["spec"]["document_id"] == "shipping"
    assert parsed["documents"][0]["chunks"]
    assert len(dataset_sha) == 64


class _FakeDatabase:
    conn_string = "unused-by-this-contract-test"
    def __init__(self):
        self.kwargs = None

    def submit(self, **kwargs):
        self.kwargs = kwargs
        return SubmittedJob(42, "pending", kwargs["manifest_sha256"], kwargs["package_sha256"], 1)


class _Context:
    def abort(self, code, message):
        raise RuntimeError(f"{code}:{message}")


def test_submit_delegates_v2_persistence_to_python_database():
    manifest, content = _package()
    database = _FakeDatabase()
    service = IngestService(database, scope_validator=lambda _: None)
    request = rag_pb2.SubmitRequest(
        context=rag_pb2.RequestContext(
            tenant_id="v2-demo-tenant", user_id="dev-user", shop_id="shop-demo",
            tenant_db_id=7, user_db_id=8, allowed_shop_ids=["shop-demo"],
            permission_revision="1", scope_audience="ingest",
        ),
        idempotency_key="admin:shop-demo:test",
        manifest_yaml=manifest,
        files=[rag_pb2.PackageFile(path="shipping.md", content=content)],
        tenant_db_id="7",
        user_db_id="8",
        shop_id="shop-demo",
    )
    response = service.Submit(request, _Context())
    assert response.job_id == "42"
    assert response.item_count == 1
    assert database.kwargs["documents"][0]["document_id"] == "shipping"


def test_submit_rejects_mismatched_signed_shop():
    manifest, content = _package()
    request = rag_pb2.SubmitRequest(
        context=rag_pb2.RequestContext(tenant_id="v2-demo-tenant", shop_id="shop-a", scope_audience="ingest"),
        idempotency_key="key", manifest_yaml=manifest,
        files=[rag_pb2.PackageFile(path="shipping.md", content=content)],
        shop_id="shop-b",
    )
    with pytest.raises(RuntimeError, match="PERMISSION_DENIED"):
        IngestService(_FakeDatabase(), scope_validator=lambda _: None).Submit(request, _Context())
