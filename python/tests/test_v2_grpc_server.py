from ipaddress import IPv4Address

from app.rpc.server import build_server, validate_bind_address


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
    monkeypatch.setenv("APP_ENV", "test")
    monkeypatch.setenv("V2_FIXTURE_MODE", "1")
    monkeypatch.setenv("RAG_PROFILE", "v2_langgraph_fixture")
    monkeypatch.setenv("V2_GRPC_REFLECTION", "0")
    server = build_server()
    try:
        handlers = server._state.registered_method_handlers
        assert handlers
        assert any("rag.v1.RagService/Understand" in method for method in handlers)
        assert not any("IngestService" in method for method in handlers)
    finally:
        server.stop(0)


def test_v2_grpc_server_verifies_durable_checkpoint_schema(monkeypatch):
    class Store:
        def __init__(self):
            self.verified = False

        def verify_schema(self):
            self.verified = True

    store = Store()
    monkeypatch.setenv("APP_ENV", "test")
    monkeypatch.setenv("V2_FIXTURE_MODE", "1")
    monkeypatch.setenv("RAG_PROFILE", "v2_langgraph_fixture")
    monkeypatch.setattr("app.rpc.server.load_checkpoint_store", lambda *_args, **_kwargs: store)
    server = build_server()
    try:
        assert store.verified is True
    finally:
        server.stop(0)


def test_active_rpc_server_requires_explicit_database_configuration(monkeypatch):
    monkeypatch.delenv("V2_RETRIEVER_DSN", raising=False)
    monkeypatch.delenv("V2_CHECKPOINT_DSN", raising=False)
    monkeypatch.delenv("V2_FIXTURE_MODE", raising=False)
    monkeypatch.setenv("APP_ENV", "development")
    from app.rpc.server import build_server

    try:
        build_server()
    except ValueError as error:
        assert "databases are required" in str(error)
    else:
        raise AssertionError("active V2 AI server accepted missing database configuration")
