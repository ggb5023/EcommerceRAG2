from __future__ import annotations

import grpc
import pytest

from app.intel.rpc import IntelService
from rag.v1 import intel_pb2


class FakeContext:
    def __init__(self):
        self.code = None
        self.details = None

    def abort(self, code, details):
        self.code = code
        self.details = details
        raise RuntimeError(details)


class FakeDatabase:
    def __init__(self, rows=None, error=None):
        self.rows = rows or []
        self.error = error
        self.calls = []

    def list_observations(self, market, category, platform, since, limit):
        self.calls.append((market, category, platform, since, limit))
        if self.error is not None:
            raise self.error
        return self.rows


def test_list_observations_returns_the_shared_row_shape():
    database = FakeDatabase([{
        "market": "电商", "category": "保温杯", "platform": "竞品",
        "source_url": "https://example.test/a", "title": "标题", "score": 0.9,
        "published_date": "2026-11-01", "observed_at": "2026-11-06T10:00:00+00:00",
    }])
    response = IntelService(database).ListObservations(
        intel_pb2.ListObservationsRequest(market="电商", category="保温杯", limit=10),
        FakeContext(),
    )
    assert len(response.items) == 1
    item = response.items[0]
    assert (item.market, item.category, item.platform) == ("电商", "保温杯", "竞品")
    assert item.source_url == "https://example.test/a"
    assert item.title == "标题" and item.score == 0.9
    assert item.observed_at == "2026-11-06T10:00:00+00:00"
    assert database.calls == [("电商", "保温杯", "", None, 10)]


def test_list_observations_rejects_bad_limit_and_since():
    context = FakeContext()
    with pytest.raises(RuntimeError):
        IntelService(FakeDatabase()).ListObservations(
            intel_pb2.ListObservationsRequest(limit=101), context)
    assert context.code == grpc.StatusCode.INVALID_ARGUMENT
    context = FakeContext()
    with pytest.raises(RuntimeError):
        IntelService(FakeDatabase()).ListObservations(
            intel_pb2.ListObservationsRequest(since="not-a-date"), context)
    assert context.code == grpc.StatusCode.INVALID_ARGUMENT


def test_list_observations_maps_database_failure_to_unavailable():
    context = FakeContext()
    with pytest.raises(RuntimeError):
        IntelService(FakeDatabase(error=RuntimeError("db down"))).ListObservations(
            intel_pb2.ListObservationsRequest(), context)
    assert context.code == grpc.StatusCode.UNAVAILABLE
    assert context.details == "observations unavailable"
