"""Pin the gRPC status codes the ingest RPC aborts with.

The Go gateway maps these codes to HTTP statuses, so a silent change of code (or a
message that leaks SQL/DSN text) is a cross-process contract break.
"""
from __future__ import annotations

import hashlib
import re

import grpc
import pytest
from app.ingest.database import (
    IngestAuthorizationError,
    IngestConflictError,
    IngestSchemaIncompleteError,
)
from app.rpc.ingest import IngestService
from rag.v1 import rag_pb2

LEAK = re.compile(r"(?:password|dsn|postgres|relation|column|table)\s", re.I)


class Aborted(Exception):
    def __init__(self, code, message):
        super().__init__(f"{code}:{message}")
        self.code = code
        self.message = message


class Context:
    def abort(self, code, message):
        raise Aborted(code, message)


class _AuthorizedDatabase:
    # IngestService builds a CommandStore from the database handle, so the fake
    # must look like one even though no connection is ever opened.
    conn_string = "postgresql://unused/unused"
    connector = None

    def __init__(self, error):
        self.error = error

    def submit(self, **_kwargs):
        raise self.error


def _request():
    content = b"# Shipping\n\nOrders leave within two days.\n"
    digest = hashlib.sha256(content).hexdigest()
    manifest = ("schema_version: 1\npipeline_version: v2-abort-test\nsource:\n  id: docs\n  name: Docs\n"
                "documents:\n  - document_id: shipping\n    title: Shipping\n    path: shipping.md\n"
                f"    format: markdown\n    sha256: {digest}\n    disclosure_class: external_allowed\n")
    return rag_pb2.SubmitRequest(
        context=rag_pb2.RequestContext(
            tenant_id="v2-demo-tenant", user_id="dev-user", shop_id="shop-demo",
            allowed_shop_ids=["shop-demo"], scope_audience="ingest", tenant_db_id=1, user_db_id=1,
        ),
        shop_id="shop-demo", manifest_yaml=manifest, idempotency_key="k",
        tenant_db_id="1", user_db_id="1",
        files=[rag_pb2.PackageFile(path="shipping.md", content=content)],
    )


def _submit(database):
    service = IngestService(database, scope_validator=lambda _scope: None)
    with pytest.raises(Aborted) as caught:
        service.Submit(_request(), Context())
    aborted = caught.value
    assert not LEAK.search(aborted.message), aborted.message
    return aborted


def test_authorization_failure_maps_to_permission_denied():
    aborted = _submit(_AuthorizedDatabase(IngestAuthorizationError("document is not authorized")))
    assert aborted.code == grpc.StatusCode.PERMISSION_DENIED


def test_incomplete_schema_maps_to_failed_precondition():
    aborted = _submit(_AuthorizedDatabase(IngestSchemaIncompleteError("ingest schema incomplete")))
    assert aborted.code == grpc.StatusCode.FAILED_PRECONDITION


def test_conflict_maps_to_already_exists():
    aborted = _submit(_AuthorizedDatabase(IngestConflictError("document identifier is owned by another source")))
    assert aborted.code == grpc.StatusCode.ALREADY_EXISTS
