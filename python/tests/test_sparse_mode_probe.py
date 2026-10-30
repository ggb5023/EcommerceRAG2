"""The sparse-mode probe must tell the truth without restarting the process."""
from __future__ import annotations

import logging

import pytest

from app.retrieval import postgres


class FakeCursor:
    def execute(self, *_args, **_kwargs):
        self.executions += 1

    def __init__(self, populated: bool, as_dict: bool = False):
        self.populated = populated
        self.as_dict = as_dict
        self.executions = 0

    def fetchone(self):
        if self.as_dict:
            return {"populated": self.populated}
        return (self.populated,)


def retriever():
    # Build without the constructor: the probe only needs its own attributes.
    return object.__new__(postgres.PostgresRetriever)


@pytest.fixture(autouse=True)
def reset_module_state(monkeypatch):
    monkeypatch.setattr(postgres, "_sparse_mode_reported", False)


def test_empty_column_reports_text_heuristic_and_warns_once(caplog):
    instance = retriever()
    instance._sparse_mode = None
    instance._sparse_mode_checked_at = 0.0
    cursor = FakeCursor(populated=False)
    with caplog.at_level(logging.WARNING):
        instance._report_sparse_mode(cursor, ttl=0)
        instance._report_sparse_mode(cursor, ttl=0)
    assert instance.last_sparse_mode == "text_heuristic"
    warnings = [record for record in caplog.records if record.levelno == logging.WARNING]
    assert len(warnings) == 1, [record.getMessage() for record in warnings]
    assert "sparse_embedding" in warnings[0].getMessage()


def test_populated_column_reports_sparsevec_without_warning(caplog):
    instance = retriever()
    instance._sparse_mode = None
    instance._sparse_mode_checked_at = 0.0
    with caplog.at_level(logging.WARNING):
        instance._report_sparse_mode(FakeCursor(populated=True), ttl=0)
    assert instance.last_sparse_mode == "sparsevec"
    assert [record for record in caplog.records if record.levelno == logging.WARNING] == []


def test_ttl_caches_the_probe_and_zero_forces_a_recheck():
    instance = retriever()
    instance._sparse_mode = None
    instance._sparse_mode_checked_at = 0.0
    cursor = FakeCursor(populated=False)
    instance._report_sparse_mode(cursor, ttl=300)
    instance._report_sparse_mode(cursor, ttl=300)
    assert cursor.executions == 1, "the probe must be cached inside the TTL"
    instance._report_sparse_mode(cursor, ttl=0)
    assert cursor.executions == 2, "ttl=0 must re-check"


def test_dict_rows_are_supported():
    # The production connection uses dict_row; indexing by position would raise.
    instance = retriever()
    instance._sparse_mode = None
    instance._sparse_mode_checked_at = 0.0
    instance._report_sparse_mode(FakeCursor(populated=True, as_dict=True), ttl=0)
    assert instance.last_sparse_mode == "sparsevec"
