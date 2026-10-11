from ipaddress import IPv4Address

from fastapi.testclient import TestClient
from pytest import MonkeyPatch

from app.api.internal import app
from app.v2_server import validate_http_bind

client = TestClient(app)


def test_fastapi_bind_rejects_public_and_wildcard_addresses():
    assert validate_http_bind("127.0.0.1", 8001) == ("127.0.0.1", 8001)
    assert validate_http_bind(str(IPv4Address(0x0A170005)), 8001) == (str(IPv4Address(0x0A170005)), 8001)
    for host in ("0.0.0.0", str(IPv4Address(0x08080808)), "public.example"):
        try:
            validate_http_bind(host, 8001)
        except ValueError:
            pass
        else:
            raise AssertionError(f"public HTTP host was accepted: {host}")


def test_health_and_ready_are_internal_service_contracts():
    assert client.get("/health").json()["service"] == "python-ai-plane"
    assert client.get("/ready").json() == {"status": "ready", "graph": "compiled", "real_service_acceptance": False}


def test_dev_graph_requires_finite_scope():
    response = client.post("/dev/graph", json={
        "query": "保温杯容量是多少？",
        "scope": {
            "tenant_id": "v2-demo-tenant", "user_id": "dev-user", "permission_revision": "1",
            "role": "operator", "allowed_shop_ids": ["shop-demo"],
            "allowed_document_ids": ["v2-doc-bottle"], "shop_id": "shop-demo",
        },
        "response_purpose": "customer_reply",
    })
    assert response.status_code == 200
    body = response.json()
    assert body["is_mock"] is True
    assert body["can_copy"] is False
    assert body["evidence"][0]["document_id"] == "v2-doc-bottle"


def test_dev_graph_honors_internal_token_when_configured(monkeypatch: MonkeyPatch):
    monkeypatch.setenv("V2_DEV_TOKEN", "dev-only")
    payload = {
        "query": "配送多久？",
        "scope": {
            "tenant_id": "v2-demo-tenant", "user_id": "dev-user", "permission_revision": "1",
            "role": "operator", "allowed_shop_ids": ["shop-demo"],
            "allowed_document_ids": ["v2-doc-shipping"], "shop_id": "shop-demo",
        },
    }
    assert client.post("/dev/graph", json=payload).status_code == 403
    assert client.post("/dev/graph", json=payload, headers={"X-Internal-Token": "dev-only"}).status_code == 200
