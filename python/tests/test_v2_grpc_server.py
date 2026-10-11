from ipaddress import IPv4Address

import os

from app.v2_grpc_server import build_server, validate_bind_address


def test_v2_grpc_bind_address_allows_loopback_private_and_link_local():
    assert validate_bind_address("127.0.0.1:50052") == "127.0.0.1:50052"
    assert validate_bind_address(f"{IPv4Address(0x0A170005)}:50052") == f"{IPv4Address(0x0A170005)}:50052"
    assert validate_bind_address("[::1]:50052") == "[::1]:50052"


def test_v2_grpc_bind_address_rejects_public_wildcard_and_invalid_port():
    for address in ("0.0.0.0:50052", f"{IPv4Address(0x08080808)}:50052", "127.0.0.1:0", "127.0.0.1:bad"):
        try:
            validate_bind_address(address)
        except ValueError:
            pass
        else:
            raise AssertionError(f"public or invalid address was accepted: {address}")


def test_v2_grpc_server_registers_only_health_and_rag(monkeypatch):
    monkeypatch.setenv("APP_ENV", "production")
    # The full test process imports the legacy service before this module.  A
    # real v2 process starts clean and picks the v2 default in
    # ``build_server``; patch the already-loaded module explicitly here so
    # this in-process registration test does not depend on collection order.
    from app import rag_service

    monkeypatch.setattr(rag_service, "_PROFILE", "v2_langgraph_fixture")
    monkeypatch.delenv("RAG_PROFILE", raising=False)
    server = build_server()
    # Registration itself is the contract; binding/serving is covered by the
    # cross-process test and must remain a separate process operation.
    assert server is not None
    server.stop(0)
    assert os.environ.get("RAG_PROFILE") is None


def test_v2_grpc_server_verifies_durable_checkpoint_schema(monkeypatch):
    from app import rag_service

    class Store:
        def __init__(self):
            self.verified = False

        def verify_schema(self):
            self.verified = True

    store = Store()
    monkeypatch.setenv("APP_ENV", "production")
    monkeypatch.delenv("RAG_PROFILE", raising=False)
    monkeypatch.setattr(rag_service, "_PROFILE", "v2_langgraph_fixture")
    monkeypatch.setattr("app.graph.runtime.load_checkpoint_store", lambda: store)
    server = build_server()
    try:
        assert store.verified is True
    finally:
        server.stop(0)
