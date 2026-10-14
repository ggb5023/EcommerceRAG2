"""Independent V2 AI RPC process and shared internal server transport."""
from __future__ import annotations

import ipaddress
import os
import signal
from concurrent import futures

import grpc
from grpc_health.v1 import health, health_pb2, health_pb2_grpc
from grpc_reflection.v1alpha import reflection

from app.configuration import verify_ai_configuration
from app.graph.runtime import load_checkpoint_store
from app.rpc.rag import RagService
from app.rpc.transport import enabled, server_credentials
from rag.v1 import rag_pb2_grpc


def _split_address(address: str) -> tuple[str, int]:
    value = address.strip()
    if not value or value.startswith('unix:'):
        raise ValueError('V2_GRPC_ADDR must be a host:port address')
    if value.startswith('['):
        closing = value.find(']')
        if closing < 0 or closing + 1 >= len(value) or value[closing + 1] != ':':
            raise ValueError('V2_GRPC_ADDR must be a host:port address')
        host, raw_port = (value[1:closing], value[closing + 2:])
    else:
        if ':' not in value:
            raise ValueError('V2_GRPC_ADDR must be a host:port address')
        host, raw_port = value.rsplit(':', 1)
    try:
        port = int(raw_port)
    except ValueError as error:
        raise ValueError('V2_GRPC_ADDR port is invalid') from error
    if not 1 <= port <= 65535:
        raise ValueError('V2_GRPC_ADDR port is invalid')
    return (host, port)

def validate_bind_address(address: str) -> str:
    """Reject wildcard and public binds before a socket is opened."""
    host, _ = _split_address(address)
    normalized = host.lower().strip('[]')
    if normalized == 'localhost':
        return address
    try:
        parsed = ipaddress.ip_address(normalized)
    except ValueError as error:
        raise ValueError('V2_GRPC_ADDR must use loopback or a private IP') from error
    if parsed.is_unspecified or not (parsed.is_loopback or parsed.is_private or parsed.is_link_local):
        raise ValueError('V2_GRPC_ADDR must use loopback or a private IP')
    return address


def new_server(service_name, concurrency_key):
    workers = int(os.environ.get(concurrency_key, "4"))
    if not 1 <= workers <= 64:
        raise ValueError("RPC worker concurrency must be between 1 and 64")
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=workers), options=(
        ("grpc.max_receive_message_length", 12 * 1024 * 1024),
        ("grpc.max_send_message_length", 24 * 1024 * 1024)))
    health_service = health.HealthServicer()
    health_pb2_grpc.add_HealthServicer_to_server(health_service, server)
    health_service.set("", health_pb2.HealthCheckResponse.SERVING)
    health_service.set(service_name, health_pb2.HealthCheckResponse.SERVING)
    if os.environ.get("APP_ENV") in {"development", "test"} and os.environ.get("V2_GRPC_REFLECTION", "1") == "1":
        reflection.enable_server_reflection((health.SERVICE_NAME, reflection.SERVICE_NAME, service_name), server)
    return server


def build_server():
    verify_ai_configuration()
    checkpoint = load_checkpoint_store()
    verify_schema = getattr(checkpoint, "verify_schema", None)
    if callable(verify_schema):
        verify_schema()
    service = RagService(checkpoint_store=checkpoint)
    server = new_server("rag.v1.RagService", "V2_GRPC_WORKERS")
    rag_pb2_grpc.add_RagServiceServicer_to_server(service, server)
    return server


def serve(server, address):
    if enabled():
        _split_address(address)
        port = server.add_secure_port(address, server_credentials())
    else:
        validate_bind_address(address)
        port = server.add_insecure_port(address)
    if not port:
        raise RuntimeError("cannot bind internal RPC address")
    server.start()
    signal.signal(signal.SIGTERM, lambda *_: server.stop(5))
    signal.signal(signal.SIGINT, lambda *_: server.stop(5))
    server.wait_for_termination()


def main():
    serve(build_server(), os.environ.get("V2_GRPC_ADDR", "127.0.0.1:50052"))


if __name__ == "__main__":
    main()
