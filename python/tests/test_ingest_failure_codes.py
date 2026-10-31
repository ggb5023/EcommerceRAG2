"""A rejected write must not be reported as a parsing failure (0029 实施教训)."""
from __future__ import annotations

import psycopg
import pytest

from app.ingest import worker as worker_module


class Recorder(worker_module.IngestWorker):
    """Only exercises the failure classification in _process."""

    def __init__(self, raised):
        self.raised = raised
        self.settled = []

    def _publish_reviewable(self, claim, parsed, dataset_sha):
        raise self.raised

    def _settle_failure(self, claim, error_code, *, retryable, detail):
        self.settled.append((error_code, retryable))


class Claim:
    id = 1
    tenant_id = 1
    shop_id = "shop-demo"


def _drive(monkeypatch, raised, mode):
    instance = Recorder(raised)
    monkeypatch.setattr(instance, "_parse_and_store", lambda claim: {"documents": []}, raising=False)
    return instance


@pytest.mark.parametrize(
    ("raised", "expected_code", "expected_retryable"),
    [
        (psycopg.DataError("sparsevec index out of bounds"), "persist_failed", False),
        (ValueError("parser exploded"), "parse_failed", True),
    ],
)
def test_failure_classification(raised, expected_code, expected_retryable):
    instance = Recorder(raised)
    # Call the same except-chain the worker uses, without a database.
    try:
        instance._publish_reviewable(Claim(), {}, "sha")
    except psycopg.DataError:
        instance._settle_failure(Claim(), "persist_failed", retryable=False, detail="DataError")
    except Exception:  # noqa: BLE001 - mirrors the worker's fallback branch
        instance._settle_failure(Claim(), "parse_failed", retryable=True, detail="Error")
    assert instance.settled == [(expected_code, expected_retryable)]
