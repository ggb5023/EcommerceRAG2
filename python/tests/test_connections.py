"""Unit checks for the role-bound pools; real PostgreSQL lives in the DB layer."""
from __future__ import annotations

from contextlib import contextmanager

import pytest

from app import connections as pools

AI_DSN = "postgresql://rag_v2_ai@127.0.0.1:5432/ecr_v2_dev"
AI_DSN_WITH_OPTIONS = "postgresql://rag_v2_ai@127.0.0.1:5432/ecr_v2_dev?application_name=other"
INGEST_DSN = "postgresql://rag_v2_ingest@127.0.0.1:5432/ecr_v2_dev"


class FakeConnection:
    def __init__(self):
        self.row_factory = None


class FakePool:
    def __init__(self):
        self.checkouts = 0
        self.closed = False

    @contextmanager
    def connection(self):
        self.checkouts += 1
        yield FakeConnection()

    def close(self):
        self.closed = True


def role(role_name="rag_v2_ai", dsn=AI_DSN):
    pool = FakePool()
    return pools.RoleConnections(role=role_name, dsn=dsn, pool=pool), pool


def test_pool_accepts_only_its_own_role_and_database():
    connections, _pool = role()
    assert connections.owns(AI_DSN)
    assert connections.owns(AI_DSN_WITH_OPTIONS)
    assert not connections.owns(INGEST_DSN)
    assert not connections.owns("postgresql://rag_v2_ai@127.0.0.1:5432/other_db")
    assert not connections.owns("not-a-dsn://")
    with pytest.raises(pools.ConnectionsError, match="foreign database identity"):
        connections.connect(INGEST_DSN)
    with connections.connect(AI_DSN) as connection:
        assert isinstance(connection, FakeConnection)


def test_row_factory_is_scoped_to_one_checkout():
    connections, pool = role()
    with connections.connect(AI_DSN, row_factory=dict) as connection:
        assert connection.row_factory is dict
    assert pool.checkouts == 1
    with connections.connect(AI_DSN) as connection:
        assert connection.row_factory is None
    with pytest.raises(pools.ConnectionsError, match="no extra options"):
        with connections.connect(AI_DSN, autocommit=True):
            pass


def test_check_role_rejects_a_pool_handed_to_the_other_plane():
    connections, _pool = role()
    connections.check_role("rag_v2_ai")
    with pytest.raises(pools.ConnectionsError, match="does not match"):
        connections.check_role("rag_v2_ingest")


def test_verify_requires_the_bound_role(monkeypatch):
    seen = []
    monkeypatch.setattr(pools, "verify_database", lambda dsn, expected: seen.append((dsn, expected)))
    connections, _pool = role()
    connections.verify("rag_v2_ai")
    assert seen == [(AI_DSN, "rag_v2_ai")]
    with pytest.raises(pools.ConnectionsError, match="does not match"):
        connections.verify("rag_v2_ingest")


def test_open_role_rejects_unknown_roles_and_invalid_sizes_before_connecting(monkeypatch):
    called = []
    monkeypatch.setattr(pools, "verify_database", lambda dsn, expected: called.append(expected))
    with pytest.raises(pools.ConnectionsError, match="owned service role"):
        pools.open_role("rag_app", AI_DSN, size=1, application_name="x")
    with pytest.raises(pools.ConnectionsError, match="positive"):
        pools.open_role("rag_v2_ai", AI_DSN, size=0, application_name="x")
    assert called == []


def test_open_role_verifies_then_opens_a_named_pool(monkeypatch):
    created = {}

    class Pool(FakePool):
        def __init__(self, dsn, **kwargs):
            super().__init__()
            created.update(dsn=dsn, options=kwargs)

        def open(self, wait, timeout):
            created["opened"] = (wait, timeout)

    monkeypatch.setattr(pools, "verify_database", lambda dsn, expected: created.setdefault("verified", (dsn, expected)))
    monkeypatch.setattr(pools, "ConnectionPool", Pool)
    connections = pools.open_role("rag_v2_ingest", INGEST_DSN, size=2, application_name="ecr-ingest")
    assert created["verified"] == (INGEST_DSN, "rag_v2_ingest")
    assert created["dsn"] == INGEST_DSN and created["opened"] == (True, 10)
    assert created["options"]["name"] == "ecr-ingest"
    assert created["options"]["max_size"] == 2
    assert created["options"]["kwargs"]["application_name"] == "ecr-ingest"
    connections.close()
    assert connections.pool.closed is True
