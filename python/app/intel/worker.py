"""Intelligence worker: watch configs -> jobs -> Tavily -> shared observations.

Runs as its own process (`python -m app.intel.worker`), with its own
rag_v2_intel database role and no answer-process credentials. The Tavily
adapter is called unchanged; this module owns scheduling, leases and the
observation/usage writes.
"""
from __future__ import annotations

import asyncio
import logging
import os
import signal
import threading
from datetime import UTC, datetime, timedelta
from urllib.parse import urlparse

from app.configuration import verify_database
from app.intel.database import IntelDatabase
from app.intel.limiter import TokenBucket
from app.intel.rpc import build_intel_server
from app.providers.contracts import ProviderError
from app.providers.tavily import TavilyProvider, load_tavily_config
from app.providers.usage import UsageRecorder
from app.rpc.server import bind

LOGGER = logging.getLogger(__name__)

RECRAWL_INTERVAL_HOURS = 24
LEASE_SECONDS = 300
DEFAULT_POLL_SECONDS = 60.0
DEFAULT_GRPC_ADDR = "127.0.0.1:50054"


def backoff_seconds(retry_count: int) -> int:
    """Exponential backoff, capped so a permanent outage still retries hourly."""
    if retry_count < 0:
        raise ValueError("retry_count must be non-negative")
    return min(3600, 60 * (2 ** min(retry_count, 6)))


def _env_int(name: str, default: int, low: int, high: int) -> int:
    raw = os.environ.get(name)
    if raw is None or raw.strip() == "":
        return default
    try:
        value = int(raw)
    except ValueError as error:
        raise ValueError(f"{name} must be an integer") from error
    if not low <= value <= high:
        raise ValueError(f"{name} is outside {low}..{high}")
    return value


def _env_float(name: str, default: float, low: float, high: float) -> float:
    raw = os.environ.get(name)
    if raw is None or raw.strip() == "":
        return default
    try:
        value = float(raw)
    except ValueError as error:
        raise ValueError(f"{name} must be numeric") from error
    if not low <= value <= high:
        raise ValueError(f"{name} is outside {low}..{high}")
    return value


class IntelWorker:
    def __init__(self, database: IntelDatabase, provider: TavilyProvider,
                 usage: UsageRecorder | None, limiter: TokenBucket, *,
                 poll_seconds: float = DEFAULT_POLL_SECONDS) -> None:
        if poll_seconds <= 0:
            raise ValueError("poll interval must be positive")
        self.database = database
        self.provider = provider
        self.usage = usage
        self.limiter = limiter
        self.poll_seconds = poll_seconds
        self._stop = threading.Event()

    def stop(self) -> None:
        self._stop.set()

    def _usage(self, http_status: int | None, error_code: str | None) -> None:
        if self.usage is None:
            return
        self.usage.record(tenant_id=None, slot="search", model="tavily",
                          input_tokens=None, output_tokens=None,
                          http_status=http_status, error_code=error_code)

    def run_once(self) -> bool:
        self.database.ensure_jobs(RECRAWL_INTERVAL_HOURS)
        job = self.database.claim_job(LEASE_SECONDS)
        if job is None:
            return False
        self.limiter.acquire()
        try:
            response = asyncio.run(self.provider.search(job.query))
        except ProviderError as error:
            self._usage(error.status, error.code)
            if error.retryable:
                retry_at = datetime.now(UTC) + timedelta(seconds=backoff_seconds(job.retry_count))
                self.database.complete_failure(job, "pending", error.code, retry_at)
            else:
                self.database.complete_failure(job, "failed", error.code)
            return True
        except Exception:  # noqa: BLE001 - never expose provider/database details
            self._usage(None, "worker_error")
            self.database.complete_failure(job, "failed", "worker_error")
            return True
        self._usage(None, None)
        self.database.complete_success(job, response.results)
        return True

    def run(self) -> None:
        while not self._stop.is_set():
            try:
                worked = self.run_once()
            except Exception:  # noqa: BLE001 - loop must survive one bad iteration
                LOGGER.warning("intelligence worker iteration failed")
                worked = False
            self._stop.wait(0.05 if worked else self.poll_seconds)


def _parse_grpc_address(value: str) -> str:
    address = value.strip()
    if not address or address.startswith("unix:"):
        raise ValueError("V2_INTEL_GRPC_ADDR must be host:port")
    host, _, port = address.rpartition(":")
    if not host or not port.isdigit() or not 1 <= int(port) <= 65535:
        raise ValueError("V2_INTEL_GRPC_ADDR must be host:port")
    return address


def main() -> int:
    if os.environ.get("V2_INTEL_ENABLED", "0") != "1":
        return 0
    dsn = os.environ.get("V2_INTEL_DSN", "").strip()
    if not dsn:
        raise SystemExit("V2_INTEL_DSN is required when intelligence is enabled")
    verify_database(dsn, "rag_v2_intel")
    rpm = _env_int("V2_INTEL_RPM", 5, 1, 60)
    poll_seconds = _env_float("V2_INTEL_POLL_SECONDS", DEFAULT_POLL_SECONDS, 1, 3600)
    address = _parse_grpc_address(os.environ.get("V2_INTEL_GRPC_ADDR", DEFAULT_GRPC_ADDR))
    proxy = os.environ.get("V2_INTEL_PROXY_URL", "").strip()
    if proxy and urlparse(proxy).scheme != "https":
        raise SystemExit("V2_INTEL_PROXY_URL must be empty or https")
    provider_config = os.environ.get("V2_PROVIDER_CONFIG", "/etc/ecommerce-rag/providers.env")
    provider = TavilyProvider(load_tavily_config(provider_config))
    database = IntelDatabase(dsn)
    worker = IntelWorker(database, provider, UsageRecorder(dsn), TokenBucket(rpm),
                         poll_seconds=poll_seconds)
    server = build_intel_server(database)
    bind(server, address)
    server.start()
    thread = threading.Thread(target=worker.run, name="intel-worker", daemon=True)
    thread.start()
    stop_event = threading.Event()

    def request_stop(_signum, _frame):
        stop_event.set()
        worker.stop()

    signal.signal(signal.SIGTERM, request_stop)
    signal.signal(signal.SIGINT, request_stop)
    stop_event.wait()
    worker.stop()
    thread.join(timeout=5)
    server.stop(5)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
