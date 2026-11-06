from __future__ import annotations

from datetime import UTC, datetime

import pytest

from app.intel.database import IntelJob
from app.intel.limiter import TokenBucket
from app.intel.worker import RECRAWL_INTERVAL_HOURS, IntelWorker, backoff_seconds
from app.providers.contracts import ProviderError
from app.providers.tavily import TavilyResponse, TavilyResult


class FakeDatabase:
    def __init__(self, job: IntelJob | None):
        self.job = job
        self.ensured = 0
        self.successes = []
        self.failures = []
        self.claims = 0

    def ensure_jobs(self, recrawl_hours):
        self.ensured += 1
        return 0

    def claim_job(self, lease_seconds):
        self.claims += 1
        return self.job

    def complete_success(self, job, results):
        self.successes.append((job, tuple(results)))
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


def job() -> IntelJob:
    return IntelJob(7, None, None, "电商", "保温杯", "竞品", "电商 保温杯 竞品", 0, 3)


def response() -> TavilyResponse:
    return TavilyResponse(
        results=(TavilyResult("https://example.test/a", "标题", "摘要", 0.9, "2026-11-01"),),
        request_id="req", usage=None, status="success_usable",
    )


def build(database, provider, usage=None, limiter=None):
    return IntelWorker(database, provider, usage or FakeUsage(), limiter or FakeLimiter(),
                       poll_seconds=1)


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
