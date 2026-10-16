"""Dedicated v2 Python AI-plane gRPC entrypoint.

The legacy ``app.server`` entrypoint also exposes the synthetic ingestion
service for the v1 test harness.  v2 has a separate process boundary so that
the AI plane cannot accidentally become a public gateway or inherit the v1
worker surface.  Binding is fail-closed: only loopback or private IP
addresses are accepted.
"""
from __future__ import annotations

import ipaddress
import os
import signal
from concurrent import futures

import grpc
from grpc_health.v1 import health, health_pb2, health_pb2_grpc
from grpc_reflection.v1alpha import reflection

from rag.v1 import rag_pb2_grpc

_RAG_SERVICE = "rag.v1.RagService"
_V2_PROFILES = {"v2_langgraph_fixture", "v2_langchain_mock", "v2_langchain_bailian"}


def _split_address(address: str) -> tuple[str, int]:
    value = address.strip()
    if not value or value.startswith("unix:"):
        raise ValueError("V2_GRPC_ADDR must be a host:port address")
    if value.startswith("["):
        closing = value.find("]")
        if closing < 0 or closing + 1 >= len(value) or value[closing + 1] != ":":
            raise ValueError("V2_GRPC_ADDR must be a host:port address")
        host, raw_port = value[1:closing], value[closing + 2 :]
    else:
        if ":" not in value:
            raise ValueError("V2_GRPC_ADDR must be a host:port address")
        host, raw_port = value.rsplit(":", 1)
    try:
        port = int(raw_port)
    except ValueError as error:
        raise ValueError("V2_GRPC_ADDR port is invalid") from error
    if not 1 <= port <= 65535:
        raise ValueError("V2_GRPC_ADDR port is invalid")
    return host, port


def validate_bind_address(address: str) -> str:
    """Reject wildcard and public binds before a socket is opened."""
    host, _ = _split_address(address)
    normalized = host.lower().strip("[]")
    if normalized == "localhost":
        return address
    try:
        parsed = ipaddress.ip_address(normalized)
    except ValueError as error:
        raise ValueError("V2_GRPC_ADDR must use loopback or a private IP") from error
    if parsed.is_unspecified or not (parsed.is_loopback or parsed.is_private or parsed.is_link_local):
        raise ValueError("V2_GRPC_ADDR must use loopback or a private IP")
    return address


def build_server() -> grpc.Server:
    """Build the v2-only server without binding or starting it."""
    # A clean v2 process needs a default profile, but leaving that default in
    # the process environment would leak it into legacy synthetic subprocesses
    # started by the same test runner.  Set it only around the first import;
    # an already-loaded module keeps its explicit profile and is checked below.
    requested_profile = os.environ.get("RAG_PROFILE")
    if requested_profile is None:
        os.environ["RAG_PROFILE"] = "v2_langgraph_fixture"
    try:
        from app import rag_service
    finally:
        if requested_profile is None:
            os.environ.pop("RAG_PROFILE", None)

    # A durable v2 process must start only after migration 0015 has created
    # both the control table and the LangGraph saver tables.  The store's
    # ``setup`` method deliberately rejects runtime DDL; this read-only check
    # makes a missing migration fail before the gRPC socket is opened.
    from app.graph.runtime import load_checkpoint_store

    checkpoint_store = load_checkpoint_store()
    verify_schema = getattr(checkpoint_store, "verify_schema", None)
    if callable(verify_schema):
        verify_schema()

    if rag_service._PROFILE not in _V2_PROFILES:
        raise RuntimeError("v2 gRPC entrypoint requires a v2 RAG_PROFILE")
    server = grpc.server(
        futures.ThreadPoolExecutor(max_workers=int(os.environ.get("V2_GRPC_WORKERS", "4"))),
        options=(
            ("grpc.max_receive_message_length", 12 * 1024 * 1024),
            ("grpc.max_send_message_length", 24 * 1024 * 1024),
        ),
    )
    health_service = health.HealthServicer()
    health_pb2_grpc.add_HealthServicer_to_server(health_service, server)
    health_service.set("", health_pb2.HealthCheckResponse.SERVING)
    health_service.set(_RAG_SERVICE, health_pb2.HealthCheckResponse.SERVING)
    rag_pb2_grpc.add_RagServiceServicer_to_server(
        rag_service.RagService(checkpoint_store=checkpoint_store), server
    )
    app_env = os.environ.get("APP_ENV", "development")
    if app_env in {"development", "test"} and os.environ.get("V2_GRPC_REFLECTION", "1") != "0":
        reflection.enable_server_reflection(
            (health.SERVICE_NAME, reflection.SERVICE_NAME, _RAG_SERVICE), server
        )
    return server


def main() -> None:
    address = validate_bind_address(os.environ.get("V2_GRPC_ADDR", "127.0.0.1:50052"))
    server = build_server()
    if not server.add_insecure_port(address):
        raise RuntimeError("cannot bind v2 gRPC address")
    server.start()
    print(f"v2 Python AI plane listening on {address}", flush=True)

    def stop(_signum, _frame):
        server.stop(5)

    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    server.wait_for_termination()


if __name__ == "__main__":
    main()
