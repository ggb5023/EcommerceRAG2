"""Assembly checks for the single ai process; no database or socket required."""
from __future__ import annotations

import threading

import pytest

from app import ai_process
from app.configuration import verify_merged_configuration


class FakeServer:
    def __init__(self, name):
        self.name = name
        self.bound = []
        self.started = False
        self.stopped = []

    def add_insecure_port(self, address):
        self.bound.append(address)
        return 50052

    def start(self):
        self.started = True

    def stop(self, grace):
        self.stopped.append(grace)
        return threading.Event()


class FakeWorker:
    def __init__(self):
        self.running = threading.Event()
        self.stop_requested = threading.Event()

    def run(self):
        self.running.set()
        self.stop_requested.wait(5)

    def stop(self):
        self.stop_requested.set()


def merged(monkeypatch, *, worker=None, fixture=False):
    rag = FakeServer("rag")
    ingest = FakeServer("ingest")
    seen = {"ingest_service": None, "database": None}

    monkeypatch.setattr(ai_process, "verify_merged_configuration", lambda: None)
    monkeypatch.setattr(ai_process, "build_rag_service", lambda: object())
    monkeypatch.setattr(ai_process, "rag_server", lambda service: rag)
    monkeypatch.setattr(ai_process, "load_ingest_database", lambda: seen.__setitem__("database", object()) or seen["database"])
    monkeypatch.setattr(ai_process, "build_ingest_service",
                        lambda database: seen.__setitem__("ingest_service", object()) or seen["ingest_service"])
    monkeypatch.setattr(ai_process, "ingest_server", lambda service: ingest)
    if fixture:
        monkeypatch.setenv("V2_FIXTURE_MODE", "1")
    else:
        monkeypatch.delenv("V2_FIXTURE_MODE", raising=False)
        monkeypatch.setattr(ai_process.IngestWorker, "from_env", classmethod(lambda cls: worker))
    return ai_process.build_ai_process(), rag, ingest, seen


def test_merged_process_registers_both_services_and_the_worker(monkeypatch):
    worker = FakeWorker()
    process, rag, ingest, seen = merged(monkeypatch, worker=worker)
    assert process.rag_server is rag and process.ingest_server is ingest
    assert process.worker is worker
    assert seen["ingest_service"] is not None and seen["database"] is not None


def test_fixture_profile_serves_rag_only_without_ingest_credentials(monkeypatch):
    monkeypatch.setenv("APP_ENV", "test")
    process, rag, ingest, seen = merged(monkeypatch, fixture=True)
    assert process.rag_server is rag
    assert process.ingest_server is None and process.worker is None
    assert seen["database"] is None


def test_start_binds_both_listeners_and_stop_drains_worker_last(monkeypatch):
    worker = FakeWorker()
    process, rag, ingest, _ = merged(monkeypatch, worker=worker)
    process.rag_address = "127.0.0.1:50052"
    process.ingest_address = "127.0.0.1:50053"
    process.start()
    try:
        assert rag.bound == ["127.0.0.1:50052"] and ingest.bound == ["127.0.0.1:50053"]
        assert rag.started and ingest.started
        assert worker.running.wait(2)
    finally:
        process.stop(grace=0)
    assert ingest.stopped == [0] and rag.stopped == [0]
    assert worker.stop_requested.is_set()
    assert process._thread is not None and not process._thread.is_alive()


def test_worker_failure_stops_the_process_and_is_reported_by_type(monkeypatch):
    class BrokenWorker(FakeWorker):
        def run(self):
            raise RuntimeError("boom")

    process, _rag, _ingest, _seen = merged(monkeypatch, worker=BrokenWorker())
    process._run_worker()
    assert isinstance(process.worker_error, RuntimeError)
    assert process._stopped.is_set()


def test_merged_configuration_requires_two_distinct_roles_and_one_database(monkeypatch):
    monkeypatch.setenv("RAG_PROFILE", "v2_langchain_mock")
    monkeypatch.delenv("V2_FIXTURE_MODE", raising=False)
    monkeypatch.setattr("app.configuration.verify_database", lambda *_args, **_kwargs: None)
    base = "postgresql://{user}@127.0.0.1:5432/ecr_v2_dev"
    monkeypatch.setenv("V2_RETRIEVER_DSN", base.format(user="rag_v2_ai"))
    monkeypatch.setenv("V2_CHECKPOINT_DSN", base.format(user="rag_v2_ai"))
    monkeypatch.setenv("V2_INGEST_DSN", base.format(user="rag_v2_ingest"))
    verify_merged_configuration()

    monkeypatch.setenv("V2_INGEST_DSN", base.format(user="rag_v2_ai"))
    with pytest.raises(ValueError, match="distinct service roles"):
        verify_merged_configuration()

    monkeypatch.setenv("V2_INGEST_DSN", "postgresql://rag_v2_ingest@127.0.0.1:5432/other")
    with pytest.raises(ValueError, match="share one database"):
        verify_merged_configuration()

    monkeypatch.setenv("V2_INGEST_DSN", "")
    with pytest.raises(ValueError, match="ingest databases are required"):
        verify_merged_configuration()
