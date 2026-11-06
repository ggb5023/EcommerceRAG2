"""The adoption audience may submit, but only Submit; admin grants stay required otherwise."""
from __future__ import annotations

import hashlib
from types import SimpleNamespace

import grpc
import pytest

from app.rpc.ingest import IngestService
from rag.v1 import rag_pb2


class Aborted(Exception):
    def __init__(self, code, message):
        super().__init__(f"{code}:{message}")
        self.code = code


class Context:
    def abort(self, code, message=""):
        raise Aborted(code, message)


class Database:
    conn_string = "postgresql://unused/unused"
    connector = None

    def __init__(self):
        self.kwargs = None

    def submit(self, **kwargs):
        self.kwargs = kwargs
        return SimpleNamespace(job_id=7, status="pending", manifest_sha256="m",
                               package_sha256="p", item_count=1)


def request(audience: str) -> rag_pb2.SubmitRequest:
    content = "# 退货\n\n七天内可退。\n".encode()
    digest = hashlib.sha256(content).hexdigest()
    manifest = ("schema_version: 1\npipeline_version: staff-adoption-v1\nsource:\n"
                "  id: staff-suggestions\n  name: 客服建议采纳\ndocuments:\n"
                "  - document_id: staff-faq-1\n    title: 退货\n    path: documents/staff-faq-1.md\n"
                f"    format: markdown\n    sha256: {digest}\n    disclosure_class: internal_only\n")
    return rag_pb2.SubmitRequest(
        context=rag_pb2.RequestContext(
            tenant_id="v2-demo-tenant", user_id="dev-user", shop_id="shop-demo",
            allowed_shop_ids=["shop-demo"], scope_audience=audience,
            tenant_db_id=1, user_db_id=1,
        ),
        shop_id="shop-demo", manifest_yaml=manifest, idempotency_key="k",
        tenant_db_id="1", user_db_id="1",
        files=[rag_pb2.PackageFile(path="documents/staff-faq-1.md", content=content)],
    )


def submit(audience: str):
    database = Database()
    service = IngestService(database, scope_validator=lambda _scope: None)
    service.Submit(request(audience), Context())
    return database.kwargs


def test_normal_ingest_still_requires_a_merchant_admin_grant():
    assert submit("ingest")["required_admin_role"] == "merchant_admin"


def test_adoption_audience_submits_without_the_merchant_admin_lock():
    assert submit("ingest_adoption")["required_admin_role"] is None


def test_unknown_audience_is_rejected():
    with pytest.raises(Aborted) as caught:
        submit("intel")
    assert caught.value.code == grpc.StatusCode.PERMISSION_DENIED
