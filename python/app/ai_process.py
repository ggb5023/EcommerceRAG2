"""Single Python ai process: RAG RPC, ingest RPC and the ingest worker.

One process still keeps two database identities.  Retrieval and checkpoints use
``rag_v2_ai``; ingestion RPC and the worker use ``rag_v2_ingest``.  Merging the
processes must not merge the writers, so the two roles and their schemas are
verified before either socket is bound.
"""
from __future__ import annotations

import os
import signal
import sys
import threading

from app.configuration import verify_merged_configuration
from app.ingest.worker import IngestWorker
from app.rpc.ingest_server import (
    build_ingest_service,
    ingest_server,
    load_ingest_database,
)
from app.rpc.server import bind, build_rag_service, rag_server

DEFAULT_RAG_ADDRESS = "127.0.0.1:50052"
DEFAULT_INGEST_ADDRESS = "127.0.0.1:50053"
DEFAULT_STOP_TIMEOUT_SECONDS = 8.0


class AiProcess:
    """Both RPC listeners and the worker loop, owned by one process."""

    def __init__(
        self,
        *,
        rag_server,
        ingest_server,
        worker=None,
        rag_address: str = DEFAULT_RAG_ADDRESS,
        ingest_address: str = DEFAULT_INGEST_ADDRESS,
        worker_timeout: float = DEFAULT_STOP_TIMEOUT_SECONDS,
    ) -> None:
        self.rag_server = rag_server
        self.ingest_server = ingest_server
        self.worker = worker
        self.rag_address = rag_address
        self.ingest_address = ingest_address
        self.worker_timeout = worker_timeout
        self.worker_error: BaseException | None = None
        self._thread: threading.Thread | None = None
        self._stopped = threading.Event()

    def start(self) -> None:
        try:
            bind(self.rag_server, self.rag_address)
            if self.ingest_server is not None:
                bind(self.ingest_server, self.ingest_address)
        except Exception:
            self.stop(grace=0)
            raise
        self.rag_server.start()
        if self.ingest_server is not None:
            self.ingest_server.start()
        if self.worker is not None:
            self._thread = threading.Thread(target=self._run_worker, name="ingest-worker", daemon=True)
            self._thread.start()

    def wait_for_stop(self) -> None:
        self._stopped.wait()

    def stop(self, *, grace: float = 5.0) -> None:
        self._stopped.set()
        # Stop accepting new RPC before the worker, so no new claim arrives
        # while the worker is draining. An unfinished job is left to its lease
        # and fencing epoch instead of being force-committed.
        if self.ingest_server is not None:
            self.ingest_server.stop(grace)
        self.rag_server.stop(grace)
        if self.worker is not None:
            self.worker.stop()
        if self._thread is not None:
            self._thread.join(timeout=self.worker_timeout)

    def _run_worker(self) -> None:
        try:
            self.worker.run()
        except BaseException as error:  # noqa: BLE001 - reported by type only
            self.worker_error = error
        finally:
            self._stopped.set()


def build_ai_process() -> AiProcess:
    verify_merged_configuration()
    worker_timeout = float(os.environ.get("V2_STOP_TIMEOUT_SECONDS", DEFAULT_STOP_TIMEOUT_SECONDS))
    rag = rag_server(build_rag_service())
    if os.environ.get("V2_FIXTURE_MODE") == "1":
        return AiProcess(rag_server=rag, ingest_server=None, worker=None, worker_timeout=worker_timeout)
    database = load_ingest_database()
    worker = IngestWorker.from_env()
    if worker is None:
        raise ValueError("V2_INGEST_DSN is required")
    return AiProcess(
        rag_server=rag,
        ingest_server=ingest_server(build_ingest_service(database)),
        worker=worker,
        rag_address=os.environ.get("V2_GRPC_ADDR", DEFAULT_RAG_ADDRESS),
        ingest_address=os.environ.get("V2_INGEST_GRPC_ADDR", DEFAULT_INGEST_ADDRESS),
        worker_timeout=worker_timeout,
    )


def main() -> int:
    process = build_ai_process()
    stop_event = threading.Event()

    def request_stop(_signum, _frame):
        stop_event.set()

    previous = {sig: signal.signal(sig, request_stop) for sig in (signal.SIGTERM, signal.SIGINT)}
    try:
        process.start()
        stop_event.wait()
    finally:
        process.stop()
        for sig, handler in previous.items():
            signal.signal(sig, handler)
    if process.worker_error is not None:
        print("FAIL ai worker stopped (" + type(process.worker_error).__name__ + ")", file=sys.stderr, flush=True)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
