"""Dependency-free metrics for the Python ai process.

The endpoint is intentionally loopback-only. It exposes counters, never request
bodies, prompts, credentials, DSNs or tenant names.
"""
from __future__ import annotations

import ipaddress
import socket
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

_lock = threading.Lock()
_usage_log_write_failures = 0


def usage_log_write_failure() -> None:
    global _usage_log_write_failures
    with _lock:
        _usage_log_write_failures += 1


def render() -> str:
    with _lock:
        failures = _usage_log_write_failures
    return (
        "# HELP usage_log_write_failures_total Usage log inserts that failed.\n"
        "# TYPE usage_log_write_failures_total counter\n"
        f"usage_log_write_failures_total {failures}\n"
    )


def validate_metrics_address(address: str) -> tuple[str, int]:
    value = address.strip()
    if value.startswith("["):
        closing = value.find("]")
        if closing < 0 or closing + 1 >= len(value) or value[closing + 1] != ":":
            raise ValueError("metrics address must be host:port")
        host, raw_port = value[1:closing], value[closing + 2:]
    else:
        if ":" not in value:
            raise ValueError("metrics address must be host:port")
        host, raw_port = value.rsplit(":", 1)
    host = host.strip()
    if host == "localhost":
        host = "127.0.0.1"
    try:
        parsed = ipaddress.ip_address(host)
        port = int(raw_port)
    except ValueError as error:
        raise ValueError("metrics address must be a loopback IP and valid port") from error
    if not parsed.is_loopback or not 1 <= port <= 65535:
        raise ValueError("metrics address must be loopback")
    return host, port


class _Handler(BaseHTTPRequestHandler):
    def do_GET(self) -> None:
        if self.path != "/internal/metrics":
            self.send_error(404)
            return
        body = render().encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "text/plain; version=0.0.4")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, _format: str, *_args: object) -> None:
        # stdout is reserved for the JSON application log.
        return


class MetricsServer:
    def __init__(self, address: str) -> None:
        host, port = validate_metrics_address(address)
        family = socket.AF_INET6 if ":" in host else socket.AF_INET
        server_class = type(
            "_MetricsHTTPServer",
            (ThreadingHTTPServer,),
            {"address_family": family},
        )
        self._server = server_class((host, port), _Handler)
        self._thread = threading.Thread(target=self._server.serve_forever, name="metrics-http", daemon=True)

    def start(self) -> None:
        self._thread.start()

    def stop(self) -> None:
        self._server.shutdown()
        self._server.server_close()
        self._thread.join(timeout=2)


def start_metrics_server(address: str) -> MetricsServer:
    server = MetricsServer(address)
    server.start()
    return server
