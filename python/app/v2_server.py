"""Entrypoint for the v2 FastAPI internal AI-plane service."""
from __future__ import annotations

import ipaddress
import os

import uvicorn


def validate_http_bind(host: str, port: int) -> tuple[str, int]:
    """Allow only loopback/private/link-local binds for the internal API."""
    if not 1 <= port <= 65535:
        raise ValueError("V2_HTTP_PORT is invalid")
    normalized = host.strip().lower().strip("[]")
    if normalized == "localhost":
        return host, port
    try:
        parsed = ipaddress.ip_address(normalized)
    except ValueError as error:
        raise ValueError("V2_HTTP_HOST must use loopback or a private IP") from error
    if parsed.is_unspecified or not (parsed.is_loopback or parsed.is_private or parsed.is_link_local):
        raise ValueError("V2_HTTP_HOST must use loopback or a private IP")
    return host, port


def main() -> None:
    host, port = validate_http_bind(
        os.environ.get("V2_HTTP_HOST", "127.0.0.1"),
        int(os.environ.get("V2_HTTP_PORT", "8001")),
    )
    # Keep the internal HTTP entrypoint on the same explicit v2 profile as
    # the dedicated gRPC entrypoint.  The setting is local to this process.
    os.environ.setdefault("RAG_PROFILE", "v2_langgraph_fixture")
    uvicorn.run(
        "app.api.internal:app",
        host=host,
        port=port,
        workers=1,
    )


if __name__ == "__main__":
    main()
