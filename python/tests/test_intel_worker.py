from __future__ import annotations

import os
from datetime import UTC, datetime
from pathlib import Path

import pytest

from app.intel.database import IntelDatabase, IntelJob
from app.intel.limiter import TokenBucket
from app.intel.worker import RECRAWL_INTERVAL_HOURS, IntelWorker, backoff_seconds
from app.providers.contracts import EmbeddingResult, ProviderError, ProviderUsage
from app.providers.tavily import TavilyResponse, TavilyResult


class FakeDatabase:
    def __init__(self, job: IntelJob | None):
        self.job = job
        self.ensured = 0
        self.successes = []
        self.embeddings = []
        self.failures = []
        self.claims = 0

    def ensure_jobs(self, recrawl_hours):
        self.ensured += 1
        return 0

    def claim_job(self, lease_seconds):
        self.claims += 1
        return self.job

    def complete_success(self, job, results, embeddings=None):
        self.successes.append((job, tuple(results)))
        self.embeddings.append(embeddings)
        return True

    def complete_failure(self, job, status, error, retry_at=None):
        self.failures.append((job, status, error, retry_at))
        return True


class FakeProvider:
    def __init__(self, response=None, error=None):
        self.response = response
        self.error = error
        self.calls = 0
        self.queries = []

    async def search(self, query, **_kwargs):
        self.calls += 1
        self.queries.append(query)
        if self.error is not None:
            raise self.error
        return self.response


class FakeUsage:
    def __init__(self):
        self.rows = []

    def record(self, **row):
        self.rows.append(row)
        return True


class FakeLimiter:
    def __init__(self):
        self.calls = 0

    def acquire(self):
        self.calls += 1


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


def dense(value: float = 1.0) -> tuple[float, ...]:
    return (value,) + (0.0,) * 1023


def job() -> IntelJob:
    return IntelJob(7, None, None, "电商", "保温杯", "竞品", "电商 保温杯 竞品", 0, 3)


def response() -> TavilyResponse:
    return TavilyResponse(
        results=(TavilyResult("https://example.test/a", "标题", "摘要", 0.9, "2026-11-01"),),
        request_id="req", usage=None, status="success_usable",
    )


def build(database, provider, usage=None, limiter=None, **kwargs):
    return IntelWorker(database, provider, usage or FakeUsage(), limiter or FakeLimiter(),
                       poll_seconds=1, **kwargs)


def test_success_writes_observations_and_one_search_usage_row():
    database, provider, usage = FakeDatabase(job()), FakeProvider(response()), FakeUsage()
    worker = build(database, provider, usage)
    assert worker.run_once() is True
    assert database.ensured == 1
    assert provider.calls == 1 and provider.queries == ["电商 保温杯 竞品"]
    assert len(database.successes) == 1 and len(database.successes[0][1]) == 1
    assert usage.rows == [{
        "tenant_id": None, "slot": "search", "model": "tavily",
        "input_tokens": None, "output_tokens": None, "http_status": None, "error_code": None,
    }]


def test_embedding_success_is_written_with_one_embedding_usage_row():
    database, provider, usage = FakeDatabase(job()), FakeProvider(response()), FakeUsage()
    embedding = FakeEmbedding([EmbeddingResult(
        dense(), (), "embedding-model", 1024, "req", 7, ProviderUsage(7, 0, 7))])
    worker = build(database, provider, usage, embedding=embedding,
                   embedding_model="embedding-model")
    assert worker.run_once() is True
    assert len(database.embeddings) == 1
    literal = database.embeddings[0][0]
    assert literal is not None and literal.startswith("[1.0,0.0")
    assert len(embedding.calls) == 1
    assert embedding.calls[0][0] == ["标题\n摘要"]
    assert usage.rows[1] == {
        "tenant_id": None, "slot": "embedding", "model": "embedding-model",
        "input_tokens": 7, "output_tokens": 0, "http_status": None, "error_code": None,
    }


def test_embedding_failure_is_fail_open_and_records_embedding_usage():
    database = FakeDatabase(job())
    provider = FakeProvider(response())
    usage = FakeUsage()
    embedding = FakeEmbedding(error=ProviderError("upstream_error", "down", slot="embedding", status=500))
    worker = build(database, provider, usage, embedding=embedding,
                   embedding_model="embedding-model")
    assert worker.run_once() is True
    assert database.successes[0][1] == (response().results[0],)
    assert database.embeddings == [None]
    assert usage.rows[1]["slot"] == "embedding"
    assert usage.rows[1]["error_code"] == "upstream_error"
    assert usage.rows[1]["http_status"] == 500


def test_retryable_429_requeues_and_still_records_usage():
    database = FakeDatabase(job())
    provider = FakeProvider(error=ProviderError("rate_limited", "quota", provider="tavily",
                                                status=429, retryable=True))
    usage = FakeUsage()
    worker = build(database, provider, usage)
    assert worker.run_once() is True
    assert database.successes == []
    _, status, error, retry_at = database.failures[0]
    assert (status, error) == ("pending", "rate_limited")
    assert retry_at is not None and retry_at > datetime.now(UTC)
    assert usage.rows[0]["http_status"] == 429 and usage.rows[0]["error_code"] == "rate_limited"
    assert usage.rows[0]["input_tokens"] is None and usage.rows[0]["output_tokens"] is None


def test_non_retryable_401_fails_and_records_usage():
    database = FakeDatabase(job())
    provider = FakeProvider(error=ProviderError("authentication", "denied", provider="tavily", status=401))
    usage = FakeUsage()
    worker = build(database, provider, usage)
    assert worker.run_once() is True
    _, status, error, retry_at = database.failures[0]
    assert (status, error, retry_at) == ("failed", "authentication", None)
    assert usage.rows[0]["http_status"] == 401
    assert usage.rows[0]["error_code"] == "authentication"


def test_no_due_job_skips_provider_and_usage():
    database = FakeDatabase(None)
    provider = FakeProvider(response())
    usage = FakeUsage()
    worker = build(database, provider, usage)
    assert worker.run_once() is False
    assert database.claims == 1
    assert provider.calls == 0 and usage.rows == []


def test_backoff_is_table_driven_and_capped():
    assert backoff_seconds(0) == 60
    assert backoff_seconds(1) == 120
    assert backoff_seconds(2) == 240
    assert backoff_seconds(6) == 3600
    assert backoff_seconds(100) == 3600
    with pytest.raises(ValueError):
        backoff_seconds(-1)
    assert RECRAWL_INTERVAL_HOURS == 24


class Clock:
    def __init__(self):
        self.now = 0.0

    def __call__(self):
        return self.now

    def sleep(self, seconds):
        self.now += seconds


def test_token_bucket_respects_rpm_boundary():
    clock = Clock()
    bucket = TokenBucket(1, clock=clock, sleep=clock.sleep)
    bucket.acquire()
    assert clock.now == 0
    bucket.acquire()
    assert clock.now == 60
    with pytest.raises(ValueError):
        TokenBucket(0)
    with pytest.raises(ValueError):
        TokenBucket(61)


def test_intel_semantic_query_orders_by_similarity_and_skips_null_embeddings():
    config = os.environ.get("V2_INTEGRATION_ENV")
    if not config:
        pytest.skip("V2_INTEGRATION_ENV is required")
    values = {}
    for line in Path(config).read_text().splitlines():
        if "=" in line:
            key, value = line.split("=", 1)
            values[key] = value.strip().strip("\"'")
    if not values.get("V2_INTEL_DSN"):
        pytest.skip("V2_INTEL_DSN is not configured")
    import psycopg

    database = IntelDatabase(values["V2_INTEL_DSN"])
    stamp = datetime.now(UTC).timestamp()
    market = f"semantic-{int(stamp)}"
    vectors = {
        "close": "[" + ",".join(["1"] + ["0"] * 1023) + "]",
        "far": "[" + ",".join(["0", "1"] + ["0"] * 1022) + "]",
    }
    with psycopg.connect(values["V2_MIGRATION_DSN"]) as conn:
        conn.execute(
            """INSERT INTO market_observation(
                   market,category,platform,observed_at,source_url,payload_json,embedding)
               VALUES(%s,'c','p',now()-interval '1 hour','https://example.test/close','{}'::jsonb,%s::vector),
                     (%s,'c','p',now()-interval '2 hour','https://example.test/far','{}'::jsonb,%s::vector),
                     (%s,'c','p',now(),'https://example.test/null','{}'::jsonb,NULL)""",
            (market, vectors["close"], market, vectors["far"], market),
        )
    try:
        timed = database.list_observations(market, "", "", None, 10)
        assert [row["source_url"] for row in timed] == [
            "https://example.test/null", "https://example.test/close", "https://example.test/far",
        ]
        semantic = database.list_observations(market, "", "", None, 10, vectors["close"])
        assert [row["source_url"] for row in semantic] == [
            "https://example.test/close", "https://example.test/far",
        ]
        assert semantic[0]["similarity"] > semantic[1]["similarity"]
        assert abs(semantic[0]["similarity"] - 1.0) < 1e-9
    finally:
        with psycopg.connect(values["V2_MIGRATION_DSN"]) as conn:
            conn.execute("DELETE FROM market_observation WHERE market=%s", (market,))


def test_intel_database_fencing_integration():
    config = os.environ.get("V2_INTEGRATION_ENV")
    if not config:
        pytest.skip("V2_INTEGRATION_ENV is required")
    values = {}
    for line in Path(config).read_text().splitlines():
        if "=" in line:
            key, value = line.split("=", 1)
            values[key] = value.strip().strip("\"'")
    if not values.get("V2_INTEL_DSN"):
        pytest.skip("V2_INTEL_DSN is not configured")
    import psycopg

    database = IntelDatabase(values["V2_INTEL_DSN"])
    stamp = datetime.now(UTC).timestamp()
    market = f"fencing-{int(stamp)}"
    with psycopg.connect(values["V2_MIGRATION_DSN"]) as conn:
        tenant = conn.execute("SELECT id FROM tenant WHERE external_id='v2-demo-tenant'").fetchone()[0]
        watch_id = conn.execute(
            """INSERT INTO intel_watch_config(tenant_id,market,category,platform,enabled)
               VALUES(%s,%s,'c','p',true) RETURNING id""",
            (tenant, market),
        ).fetchone()[0]
    try:
        assert database.ensure_jobs(24) >= 1
        with psycopg.connect(values["V2_MIGRATION_DSN"]) as conn:
            job_id = conn.execute(
                "SELECT id FROM intel_job WHERE watch_config_id=%s ORDER BY id DESC LIMIT 1",
                (watch_id,),
            ).fetchone()[0]
        first = database.claim_job(300)
        assert first is not None and first.id == job_id
        with psycopg.connect(values["V2_MIGRATION_DSN"]) as conn:
            conn.execute("UPDATE intel_job SET lease_expires_at=now()-interval '1 second' WHERE id=%s", (job_id,))
        second = database.claim_job(300)
        assert second is not None and second.id == job_id and second.fencing_epoch > first.fencing_epoch
        url = f"https://example.test/{int(stamp)}"
        result = TavilyResult(url, "fencing", "content", 0.5, None)
        assert database.complete_success(first, [result]) is False
        assert database.complete_success(second, [result]) is True
        with psycopg.connect(values["V2_MIGRATION_DSN"]) as conn:
            assert conn.execute(
                "SELECT status,observed_count FROM intel_job WHERE id=%s", (job_id,)
            ).fetchone() == ("done", 1)
            assert conn.execute(
                "SELECT count(*) FROM market_observation WHERE source_url=%s", (url,)
            ).fetchone()[0] == 1
    finally:
        with psycopg.connect(values["V2_MIGRATION_DSN"]) as conn:
            conn.execute("DELETE FROM market_observation WHERE market=%s", (market,))
            conn.execute("DELETE FROM intel_job WHERE watch_config_id=%s", (watch_id,))
            conn.execute("DELETE FROM intel_watch_config WHERE id=%s", (watch_id,))
