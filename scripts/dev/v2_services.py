#!/usr/bin/env python3
"""Run the owned V2 services: the Go gateway and the single Python ai process.

The ai process owns both RPC listeners and the ingest worker, so one child
receives the two service roles it needs.  Credentials stay inside that child:
the gateway still never sees a Python database role.
"""
from __future__ import annotations

import argparse
import ipaddress
import json
import os
import signal
import stat
import subprocess
import sys
import time
import urllib.request
from pathlib import Path
from uuid import uuid4

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "python"))
SYSTEM_KEYS = {"PATH", "HOME", "LANG", "LC_ALL", "TMPDIR", "SSL_CERT_FILE", "SSL_CERT_DIR"}
COMMON_KEYS = {"APP_ENV", "RAG_PROFILE", "PYTHONPATH", "PYTHONUNBUFFERED", "V2_RPC_TLS", "V2_RPC_CA_FILE", "V2_RPC_CERT_FILE", "V2_RPC_KEY_FILE", "V2_AUTHORITY_GRPC_ADDR"}
STORAGE_KEYS = {"INGEST_STORAGE_PROFILE", "INGEST_ARTIFACT_ROOT"}
PROVIDER_KEYS = {"V2_PROVIDER_CONFIG"}
SERVICE_KEYS = {
    "gateway": {"V2_GATEWAY_DSN", "V2_IDENTITY_TENANT", "V2_IDENTITY_USER", "V2_IDENTITY_SHOP",
                "SCOPE_SIGNING_KEY_FILE", "HTTP_ADDR", "V2_GRPC_ADDR", "V2_INGEST_GRPC_ADDR",
                "V2_CUSTOMER_ORIGIN", "V2_CUSTOMER_LOOPBACK_HTTP", "V2_CUSTOMER_VISITOR_RPM", "V2_CUSTOMER_IP_RPM", "V2_CUSTOMER_PROXY_HOST", "V2_FACTS_BASE_URL", "V2_FACTS_VERSION", "V2_IDENTITY_PROFILE", "V2_OIDC_CONFIG_FILE"},
    "ai": {"V2_RETRIEVER_DSN", "V2_CHECKPOINT_DSN", "V2_GRPC_ADDR", "V2_GRPC_WORKERS",
           "V2_GRPC_REFLECTION", "AUTHORITY_HTTP_BASE", "SYNTHETIC_BUSINESS_DATE",
           "V2_INGEST_DSN", "V2_INGEST_GRPC_ADDR", "V2_INGEST_RPC_WORKERS",
           "V2_INGEST_POLL_SECONDS", "V2_INGEST_LEASE_SECONDS", "V2_INGEST_WORKER_ID"}
          | STORAGE_KEYS | PROVIDER_KEYS,
}
CONFIG_KEYS = set().union(*SERVICE_KEYS.values(), COMMON_KEYS) | {
    "V2_AI_DSN", "V2_MIGRATION_DSN", "V2_START_TIMEOUT_SECONDS", "V2_STOP_TIMEOUT_SECONDS",
}


class StartupError(Exception):
    """Diagnostics are fixed messages and never contain DSN/exception values."""


def load_values(path: Path | None) -> dict[str, str]:
    values = dict(os.environ)
    if path is not None:
        info = path.lstat()
        if (not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o600
                or info.st_uid != os.geteuid()):
            raise StartupError("runtime config must be an owned regular 0600 file")
        seen = set()
        for raw in path.read_text().splitlines():
            if not raw.strip() or raw.lstrip().startswith("#"):
                continue
            key, separator, value = raw.partition("=")
            key = key.strip()
            if not separator or key not in CONFIG_KEYS or key in seen:
                raise StartupError("runtime config contains an invalid or duplicate key")
            seen.add(key)
            values[key] = value.strip().strip("\"'")
    return values


def address(value: str, *, loopback: bool = False) -> str:
    try:
        host, port = value.rsplit(":", 1)
        host = host.strip("[]")
        if host == "localhost":
            host = "127.0.0.1"
        parsed = ipaddress.ip_address(host)
        if (parsed.is_unspecified or not 1 <= int(port) <= 65535
                or (loopback and not parsed.is_loopback)
                or not (parsed.is_private or parsed.is_loopback)):
            raise ValueError
        return f"[{host}]:{int(port)}" if parsed.version == 6 else f"{host}:{int(port)}"
    except (ValueError, TypeError):
        raise StartupError("service addresses must be explicit loopback/private host:port") from None


def configure(values: dict[str, str]) -> dict[str, str]:
    values = dict(values)
    values.setdefault("APP_ENV", "development")
    values.setdefault("RAG_PROFILE", "v2_langchain_mock")
    if values["APP_ENV"] not in {"development", "test"}:
        raise StartupError("APP_ENV must be development or test")
    if values["RAG_PROFILE"] not in {"v2_langchain_mock", "v2_langchain_bailian", "v2_langgraph_fixture"}:
        raise StartupError("an explicit V2 profile is required")
    for key in ("V2_RETRIEVER_DSN", "V2_CHECKPOINT_DSN"):
        if not values.get(key):
            values[key] = values.get("V2_AI_DSN", "")
    required = {"V2_GATEWAY_DSN", "V2_RETRIEVER_DSN", "V2_CHECKPOINT_DSN", "V2_INGEST_DSN"}
    identity_profile = values.setdefault("V2_IDENTITY_PROFILE", "development")
    if identity_profile == "development":
        required |= {"V2_IDENTITY_TENANT", "V2_IDENTITY_USER"}
    elif identity_profile == "local":
        required.add("V2_IDENTITY_TENANT")
    elif identity_profile in {"oidc", "oidc_browser"}:
        required.add("V2_OIDC_CONFIG_FILE")
    else:
        raise StartupError("an explicit supported identity profile is required")
    if any(not values.get(key) for key in required):
        raise StartupError("explicit service databases and development identity are required")
    if values["RAG_PROFILE"] == "v2_langchain_bailian" and not values.get("V2_PROVIDER_CONFIG"):
        raise StartupError("online profile requires an explicit protected Provider config")
    if values.get("INGEST_STORAGE_PROFILE") not in {"filesystem", "oss"}:
        raise StartupError("INGEST_STORAGE_PROFILE must be filesystem or oss")
    if values["INGEST_STORAGE_PROFILE"] == "filesystem" and not Path(values.get("INGEST_ARTIFACT_ROOT", "")).is_absolute():
        raise StartupError("filesystem artifacts require an absolute root")
    for key, default in (("HTTP_ADDR", "127.0.0.1:8080"), ("V2_GRPC_ADDR", "127.0.0.1:50052"),
                         ("V2_INGEST_GRPC_ADDR", "127.0.0.1:50053")):
        values[key] = address(values.get(key, default), loopback=key == "HTTP_ADDR")
    if len({values[key] for key in ("HTTP_ADDR", "V2_GRPC_ADDR", "V2_INGEST_GRPC_ADDR")}) != 3:
        raise StartupError("service bind addresses must be distinct")
    values["AUTHORITY_HTTP_BASE"] = "http://" + values["HTTP_ADDR"]
    values["PYTHONPATH"] = str(ROOT / "python")
    values["PYTHONUNBUFFERED"] = "1"
    values["V2_INGEST_WORKER_ID"] = "v2-local-" + uuid4().hex
    return values


def service_environment(name: str, values: dict[str, str]) -> dict[str, str]:
    return {key: value for key, value in values.items()
            if key in SYSTEM_KEYS | COMMON_KEYS | SERVICE_KEYS[name]}


def rpc_serving(values: dict[str, str], address: str, service: str) -> bool:
    import grpc
    from grpc_health.v1 import health_pb2, health_pb2_grpc
    if values.get("V2_RPC_TLS") == "mtls":
        creds = grpc.ssl_channel_credentials(Path(values["V2_RPC_CA_FILE"]).read_bytes(),
            Path(values["V2_RPC_KEY_FILE"]).read_bytes(), Path(values["V2_RPC_CERT_FILE"]).read_bytes())
        rpc_channel = grpc.secure_channel(address, creds)
    else:
        rpc_channel = grpc.insecure_channel(address)
    with rpc_channel as channel:
        result = health_pb2_grpc.HealthStub(channel).Check(
            health_pb2.HealthCheckRequest(service=service), timeout=0.5)
        return result.status == health_pb2.HealthCheckResponse.SERVING


def worker_heartbeat(values: dict[str, str]) -> bool:
    import psycopg
    with psycopg.connect(values["V2_GATEWAY_DSN"], connect_timeout=1,
                        options="-c statement_timeout=1000") as connection:
        row = connection.execute(
            "SELECT status IN ('idle','working') AND heartbeat_at > now()-interval '10 seconds' "
            "FROM ingest_worker_heartbeat WHERE worker_id=%s", (values["V2_INGEST_WORKER_ID"],)
        ).fetchone()
    return row is not None and row[0] is True


def ready(name: str, values: dict[str, str]) -> bool:
    """Probe HTTP/RPC health and the heartbeat of this invocation's worker.

    The ai child serves both RPC services and runs the worker loop, so its
    readiness covers three facets: RAG health, ingest health and the heartbeat
    of this supervisor invocation's worker id.
    """
    try:
        if name == "gateway":
            opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
            with opener.open("http://" + values["HTTP_ADDR"] + "/healthz", timeout=0.5) as response:
                return response.status == 200 and json.loads(response.read(4096)).get("status") == "ok"
        return (rpc_serving(values, values["V2_GRPC_ADDR"], "rag.v1.RagService")
                and rpc_serving(values, values["V2_INGEST_GRPC_ADDR"], "rag.v1.IngestService")
                and worker_heartbeat(values))
    except Exception:  # noqa: BLE001 - bounded probe failures do not expose credentials
        return False


def stop_processes(processes: dict[str, subprocess.Popen], timeout: float) -> None:
    # Children lead their own process groups, so cleanup includes descendants.
    for process in processes.values():
        try:
            os.killpg(process.pid, signal.SIGTERM)
        except ProcessLookupError:
            pass
    deadline = time.monotonic() + timeout
    while any(p.poll() is None for p in processes.values()) and time.monotonic() < deadline:
        time.sleep(0.05)
    for process in processes.values():
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        process.wait()


def supervise(commands: dict[str, list[str]], environments: dict[str, dict[str, str]],
              probe, *, cwd: Path, start_timeout: float, stop_timeout: float) -> int:
    processes: dict[str, subprocess.Popen] = {}
    stopped = False

    def request_stop(_signum, _frame):
        nonlocal stopped
        stopped = True

    previous = {sig: signal.signal(sig, request_stop) for sig in (signal.SIGINT, signal.SIGTERM)}
    try:
        for name, command in commands.items():
            if stopped:
                return 0
            log_dir = os.environ.get("V2_SERVICE_LOG_DIR")
            if log_dir:
                Path(log_dir).mkdir(parents=True, exist_ok=True, mode=0o700)
                log = (Path(log_dir) / f"{name}.log").open("ab")
                processes[name] = subprocess.Popen(command, cwd=cwd, env=environments[name],
                                                   start_new_session=True, stdout=log, stderr=subprocess.STDOUT)
            else:
                processes[name] = subprocess.Popen(command, cwd=cwd, env=environments[name], start_new_session=True)
        pending = set(processes)
        deadline = time.monotonic() + start_timeout
        announced = False
        while not stopped:
            for name, process in processes.items():
                code = process.poll()
                if code is not None:
                    print(f"FAIL V2 service {name} exited ({code})", file=sys.stderr, flush=True)
                    return code if code > 0 else 1
            if pending:
                pending = {name for name in pending if not probe(name)}
                if pending and time.monotonic() >= deadline:
                    print("FAIL V2 readiness timeout: " + ",".join(sorted(pending)), file=sys.stderr, flush=True)
                    return 1
            if not pending and not announced:
                print("READY V2 gateway,ai", flush=True)
                announced = True
            time.sleep(0.1)
        return 0
    finally:
        stop_processes(processes, stop_timeout)
        for sig, handler in previous.items():
            signal.signal(sig, handler)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--env", type=Path, help="owned 0600 runtime config; values are never printed")
    args = parser.parse_args()
    try:
        values = configure(load_values(args.env))
        start_timeout = float(values.get("V2_START_TIMEOUT_SECONDS", "30"))
        stop_timeout = float(values.get("V2_STOP_TIMEOUT_SECONDS", "8"))
        if not 0 < start_timeout <= 300 or not 0 < stop_timeout <= 60:
            raise StartupError("startup/stop timeout is outside the allowed bounds")
        binary = ROOT / ".local/v2-gateway"
        binary.parent.mkdir(parents=True, exist_ok=True)
        built = subprocess.run(["go", "build", "-p", "2", "-o", str(binary), "./cmd/gateway"],
                               cwd=ROOT / "go", env={k: v for k, v in values.items() if k in SYSTEM_KEYS}, check=False)
        if built.returncode:
            raise StartupError("V2 gateway build failed")
        commands = {"gateway": [str(binary)], "ai": [sys.executable, "-m", "app.ai_process"]}
        return supervise(commands, {name: service_environment(name, values) for name in commands},
                         lambda name: ready(name, values), cwd=ROOT / "python",
                         start_timeout=start_timeout, stop_timeout=stop_timeout)
    except StartupError as error:
        print("FAIL V2 startup: " + str(error), file=sys.stderr)
    except Exception as error:  # noqa: BLE001 - runtime exceptions can include credentials
        print("FAIL V2 startup (" + type(error).__name__ + ")", file=sys.stderr)
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
