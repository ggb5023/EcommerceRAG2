"""The sparse-mode probe must tell the truth without restarting the process."""
from __future__ import annotations

import logging

import pytest

from app.retrieval import postgres


class FakeCursor:
    def __init__(self, populated: bool, as_dict: bool = False):
        self.populated = populated
        self.as_dict = as_dict
        self.executions = 0

    def execute(self, *_args, **_kwargs):
        self.executions += 1

    def fetchone(self):
        if self.as_dict:
            return {"populated": self.populated}
        return (self.populated,)


def retriever(mode: str = "legacy"):
    # Build without the constructor; the probe only needs its own attributes.
    instance = object.__new__(postgres.PostgresRetriever)
    instance._sparse_mode = None
    instance._sparse_mode_checked_at = 0.0
    instance._sparse_retrieval = mode
    instance.last_sparse_mode = None
    return instance


@pytest.fixture(autouse=True)
def reset_module_state(monkeypatch):
    monkeypatch.setattr(postgres, "_sparse_mode_reported", False)
    monkeypatch.setattr(postgres, "_sparse_probe_failed_reported", False)


def test_legacy_mode_reports_text_heuristic_without_probe():
    instance = retriever("legacy")
    cursor = FakeCursor(populated=False)
    assert instance._resolve_sparse_mode(cursor, None) == "text_heuristic"
    assert cursor.executions == 0, "legacy mode must not probe the sparse column"


def test_sparsevec_empty_column_falls_back_and_warns_once(caplog):
    instance = retriever("sparsevec")
    cursor = FakeCursor(populated=False)
    with caplog.at_level(logging.WARNING):
        assert instance._resolve_sparse_mode(cursor, "{1:1}/65536") == "dense_only"
        assert instance._resolve_sparse_mode(cursor, "{1:1}/65536") == "dense_only"
    warnings = [record for record in caplog.records if record.levelno == logging.WARNING]
    assert len(warnings) == 1, [record.getMessage() for record in warnings]
    assert "sparse_embedding is empty" in warnings[0].getMessage()


def test_sparsevec_populated_column_runs_sparsevec_without_warning(caplog):
    instance = retriever("sparsevec")
    with caplog.at_level(logging.WARNING):
        assert instance._resolve_sparse_mode(FakeCursor(populated=True), "{1:1}/65536") == "sparsevec"
    assert [record for record in caplog.records if record.levelno == logging.WARNING] == []


def test_column_probe_is_cached_inside_the_ttl():
    instance = retriever("sparsevec")
    cursor = FakeCursor(populated=False)
    assert instance._sparse_column_populated(cursor) is False
    assert instance._sparse_column_populated(cursor) is False
    assert cursor.executions == 1, "the probe must be cached inside the TTL"
    instance._sparse_mode_checked_at = 0.0
    assert instance._sparse_column_populated(cursor) is False
    assert cursor.executions == 2, "a forced re-check must probe again"


def test_dict_rows_are_supported():
    # The production connection uses dict_row; indexing by position would raise.
    instance = retriever("sparsevec")
    assert instance._sparse_column_populated(FakeCursor(populated=True, as_dict=True)) is True


def test_probe_failure_is_reported_not_silent(caplog):
    instance = retriever("sparsevec")

    class Broken:
        def execute(self, *_args, **_kwargs):
            raise postgres.psycopg.OperationalError("the connection is closed")

    with caplog.at_level(logging.WARNING):
        assert instance._sparse_column_populated(Broken()) is False
        assert instance._sparse_column_populated(Broken()) is False
    warnings = [r for r in caplog.records if r.levelno == logging.WARNING]
    assert len(warnings) == 1
    assert "sparse mode probe failed" in warnings[0].getMessage()
