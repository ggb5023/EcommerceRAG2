from __future__ import annotations

import grpc
import pytest

from app.intel.rpc import IntelService
from app.providers.contracts import EmbeddingResult, ProviderError, ProviderUsage
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

    def list_observations(self, market, category, platform, since, limit, query_vector=None):
        self.calls.append((market, category, platform, since, limit, query_vector))
        if self.error is not None:
            raise self.error
        return self.rows


class FakeEmbedding:
    def __init__(self, results=None, error=None):
        self.results = results or []
        self.error = error
        self.calls = []

    async def embed(self, texts, **kwargs):
        self.calls.append((list(texts), kwargs))
        if self.error is not None:
            raise self.error
        return self.results


class FakeUsage:
    def __init__(self):
        self.rows = []

    def record(self, **row):
        self.rows.append(row)
        return True


def dense(value: float = 1.0) -> tuple[float, ...]:
    return (value,) + (0.0,) * 1023


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
    assert item.similarity == 0.0 and response.query_degraded is False
    assert database.calls == [("电商", "保温杯", "", None, 10, None)]


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


def test_query_embeds_and_returns_similarity():
    database = FakeDatabase([{
        "market": "电商", "category": "保温杯", "platform": "竞品",
        "source_url": "https://example.test/a", "title": "标题", "score": 0.9,
        "published_date": "2026-11-01", "observed_at": "2026-11-06T10:00:00+00:00",
        "similarity": 0.81,
    }])
    embedding = FakeEmbedding([EmbeddingResult(
        dense(), (), "embedding-model", 1024, "req", 4, ProviderUsage(4, 0, 4))])
    usage = FakeUsage()
    response = IntelService(database, embedding, usage, "embedding-model").ListObservations(
        intel_pb2.ListObservationsRequest(market="电商", query="保温杯竞品", limit=10),
        FakeContext(),
    )
    assert response.items[0].similarity == 0.81
    assert response.query_degraded is False
    assert database.calls[0][5] is not None
    assert embedding.calls[0][0] == ["保温杯竞品"]
    assert embedding.calls[0][1]["text_type"] == "query"
    assert usage.rows == [{
        "tenant_id": None, "slot": "embedding", "model": "embedding-model",
        "input_tokens": 4, "output_tokens": 0, "http_status": None, "error_code": None,
    }]


def test_query_embedding_failure_falls_back_to_time_order():
    database = FakeDatabase([{
        "market": "电商", "category": "保温杯", "platform": "竞品",
        "source_url": "https://example.test/a", "title": "标题", "score": 0.9,
        "published_date": "2026-11-01", "observed_at": "2026-11-06T10:00:00+00:00",
        "similarity": 0.0,
    }])
    embedding = FakeEmbedding(error=ProviderError("timeout", "slow", slot="embedding"))
    usage = FakeUsage()
    response = IntelService(database, embedding, usage, "embedding-model").ListObservations(
        intel_pb2.ListObservationsRequest(query="保温杯竞品"), FakeContext(),
    )
    assert response.query_degraded is True
    assert response.items[0].similarity == 0.0
    assert database.calls[0][5] is None
    assert usage.rows[0]["slot"] == "embedding" and usage.rows[0]["error_code"] == "timeout"


def test_list_observations_maps_database_failure_to_unavailable():
    context = FakeContext()
    with pytest.raises(RuntimeError):
        IntelService(FakeDatabase(error=RuntimeError("db down"))).ListObservations(
            intel_pb2.ListObservationsRequest(), context)
    assert context.code == grpc.StatusCode.UNAVAILABLE
    assert context.details == "observations unavailable"
