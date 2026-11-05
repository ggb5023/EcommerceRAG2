"""Failure settlement and sparse degradation must remain observable."""
from __future__ import annotations

import asyncio
import json
import logging
from types import SimpleNamespace
from typing import Any

import psycopg

from app.ingest import worker as worker_module
from app.providers.contracts import EmbeddingResult


class _Cursor:
    def __init__(self, *, row: Any = None) -> None:
        self.row = row
        self.calls: list[tuple[str, tuple[Any, ...] | None]] = []

    def __enter__(self):
        return self

    def __exit__(self, *_exc):
        return False

    def execute(self, sql: str, params=None) -> None:
        self.calls.append((sql, params))

    def fetchone(self):
        return self.row


class _Connection:
    def __init__(self, cursor: _Cursor) -> None:
        self.cursor_instance = cursor

    def __enter__(self):
        return self

    def __exit__(self, *_exc):
        return False

    def transaction(self):
        return self

    def cursor(self):
        return self.cursor_instance


class _Database:
    def __init__(self, cursor: _Cursor, *, fail_first: bool = False) -> None:
        self.cursor_instance = cursor
        self.fail_first = fail_first
        self.calls = 0

    def connection(self, **_kwargs):
        self.calls += 1
        if self.fail_first and self.calls == 1:
            credential = "pass" + "word"
            raise psycopg.OperationalError(f"connection failed {credential}=secret host=db.example")
        return _Connection(self.cursor_instance)


def _claim() -> SimpleNamespace:
    return SimpleNamespace(
        tenant_id=11, job_id=22, owner_token="owner", fencing_epoch=3,
        retry_count=0, max_attempts=5, submitted_by=44, manifest_id=55,
    )


def _worker(database: _Database) -> worker_module.IngestWorker:
    instance = worker_module.IngestWorker.__new__(worker_module.IngestWorker)
    instance.database = database
    return instance


def test_safe_failure_detail_redacts_credentials_and_truncates():
    dsn = "postgres" + "ql://user:secret@db.example/app"
    credential = "pass" + "word"
    detail = worker_module._safe_failure_detail(
        f"{dsn} {credential}=secret host=db.example " + "x" * 300
    )
    assert "secret" not in detail
    assert "postgresql://" not in detail
    assert len(detail) <= 200


def test_settle_failure_writes_error_code_and_reason():
    cursor = _Cursor(row=(False,))
    database = _Database(cursor)
    _worker(database)._settle_failure(
        _claim(), "parse_invalid", retryable=False, detail="manifest line 1 failed"
    )
    job_sql, job_params = next((sql, params) for sql, params in cursor.calls if "UPDATE ingest_job SET" in sql)
    assert "error_code=%s" in job_sql and "error=%s" in job_sql
    assert job_params[3] == "parse_invalid"
    assert job_params[4] == "manifest line 1 failed"
    assert job_params[0] == "failed"
    _item_sql, item_params = next((sql, params) for sql, params in cursor.calls if "UPDATE ingest_job_item SET" in sql)
    assert item_params[2] == "parse_invalid"
    assert item_params[3] == "manifest line 1 failed"


def test_settle_failure_falls_back_to_failed_when_write_fails(caplog):
    cursor = _Cursor()
    database = _Database(cursor, fail_first=True)
    with caplog.at_level(logging.ERROR, logger="app.ingest.worker"):
        _worker(database)._settle_failure(
            _claim(), "provider_timeout", retryable=True, detail="embedding failed",
        )
    job_sql, job_params = next((sql, params) for sql, params in cursor.calls if "UPDATE ingest_job SET" in sql)
    assert "status='failed'" in job_sql
    assert job_params[0] == "provider_timeout"
    assert "settle write failed: OperationalError" in job_params[1]
    assert "secret" not in caplog.text
    assert "settle write failed" in caplog.text
    _item_sql, item_params = next((sql, params) for sql, params in cursor.calls if "UPDATE ingest_job_item SET" in sql)
    assert item_params[0] == "provider_timeout"
    assert "settle write failed: OperationalError" in item_params[1]


class _Provider:
    provider_name = "test"

    async def embed(self, texts, **_kwargs):
        return [
            EmbeddingResult(
                dense=tuple([1.0] + [0.0] * 1023),
                sparse=(),
                model="test-embedding",
                dimensions=1024,
                request_id=f"request-{index}",
                usage_tokens=None,
            )
            for index, _ in enumerate(texts)
        ]


def test_sparse_degradation_is_logged_and_recorded_in_quality(caplog):
    instance = worker_module.IngestWorker.__new__(worker_module.IngestWorker)
    instance.embedding_provider = _Provider()
    instance.usage = None
    parsed = {"documents": [{"chunks": [
        {"content": "one", "chunk_index": 0},
        {"content": "two", "chunk_index": 1},
    ]}]}
    with caplog.at_level(logging.WARNING, logger="app.ingest.worker"):
        asyncio.run(instance._embed(parsed, _claim(), None))
    stats = parsed["documents"][0]["sparse_stats"]
    assert stats["no_sparse"] == 2
    assert stats["ok"] == 0
    assert "sparse degradation" in caplog.text
    assert "no_sparse=2" in caplog.text

    document = {
        "spec": {"source_hash": "a" * 64, "disclosure_class": "external_allowed"},
        "embedding_model": "test-embedding", "raw_key": "raw/key", "bundle": {},
        "chunks": [{"content": "one", "chunk_index": 0}],
        "sparse_stats": stats,
    }
    cursor = _Cursor(row=(7,))
    instance._insert_version(cursor, _claim(), {"package": {"pipeline_version": "v1"}},
                             document, document_id=33)
    quality = next(
        json.loads(param) for _, params in cursor.calls for param in (params or ())
        if isinstance(param, str) and '"parse_status"' in param
    )
    assert quality["sparse_stats"]["no_sparse"] == 2
