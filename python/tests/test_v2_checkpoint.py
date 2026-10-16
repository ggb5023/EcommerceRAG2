from contextlib import contextmanager

import pytest

from app.graph.checkpoint import InMemoryCheckpointStore, PostgresCheckpointStore
from app.graph.runtime import load_checkpoint_store
from app.graph.service import LangGraphRagService


def scope(revision="1"):
    return {
        "tenant_id": "tenant",
        "user_id": "user",
        "permission_revision": revision,
        "conversation_id": "conversation-1",
        "turn_id": "turn-1",
    }


def test_checkpoint_contains_only_resume_identity_and_is_single_use():
    store = InMemoryCheckpointStore(ttl_s=60, clock=lambda: 100)
    saved = store.save_asking(scope())
    assert saved.pending_kind == "ASKING"
    assert saved.thread_id == "tenant:conversation-1:turn-1"
    assert not hasattr(saved, "query")
    assert store.resume(scope()) == saved
    with pytest.raises(KeyError):
        store.resume(scope())


def test_checkpoint_rejects_stale_permission_revision_and_expiry():
    store = InMemoryCheckpointStore(ttl_s=10, clock=lambda: 100)
    store.save_asking(scope())
    with pytest.raises(PermissionError, match="stale"):
        store.resume(scope("2"))

    now = [100]
    expired = InMemoryCheckpointStore(ttl_s=10, clock=lambda: now[0])
    expired.save_asking(scope())
    now[0] = 111
    with pytest.raises(PermissionError, match="expired"):
        expired.resume(scope())


def test_graph_service_uses_the_checkpoint_store_as_a_separate_boundary():
    graph = LangGraphRagService()
    with pytest.raises(PermissionError, match="scope"):
        graph.save_asking_checkpoint(scope())
    valid = {
        **scope(),
        "role": "operator",
        "allowed_shop_ids": ["shop-demo"],
        "allowed_document_ids": ["v2-doc-bottle"],
        "shop_id": "shop-demo",
    }
    saved = graph.save_asking_checkpoint(valid)
    assert graph.resume_checkpoint(valid) == saved


class _FakeCursor:
    def __init__(self, database):
        self.database = database
        self.result = None

    def __enter__(self):
        return self

    def __exit__(self, *_args):
        return False

    def execute(self, sql, params):
        if "INSERT INTO v2_asking_checkpoint" in sql:
            self.database.rows[params[0]] = {
                "tenant": params[1], "user": params[2], "conversation": params[3],
                "turn": params[4], "revision": params[5], "kind": params[6],
                "created": params[7], "expires": params[8], "checkpoint_id": params[9],
                "consumed": None,
            }
            self.result = None
        elif "UPDATE v2_asking_checkpoint" in sql:
            now, identifier, tenant, user, revision, _ = params
            row = self.database.rows.get(identifier)
            if row and row["tenant"] == tenant and row["user"] == user and row["revision"] == revision and row["consumed"] is None and row["expires"] > now:
                row["consumed"] = now
                self.result = (
                    row["tenant"], row["user"], row["conversation"], row["turn"],
                    row["revision"], row["kind"], row["created"], row["expires"], row["checkpoint_id"],
                )
            else:
                self.result = None
        elif "SELECT tenant_id" in sql:
            row = self.database.rows.get(params[0])
            self.result = None if row is None else (
                row["tenant"], row["user"], row["conversation"], row["turn"], row["revision"],
                row["kind"], row["created"], row["expires"], row["consumed"], row["checkpoint_id"],
            )
        elif "DELETE FROM v2_asking_checkpoint" in sql:
            self.database.rows.pop(params[0], None)
            self.result = None
        else:
            raise AssertionError(f"unexpected SQL: {sql}")

    def fetchone(self):
        return self.result


class _FakeConnection:
    def __init__(self, database):
        self.database = database

    def __enter__(self):
        return self

    def __exit__(self, *_args):
        return False

    def cursor(self):
        return _FakeCursor(self.database)

    def commit(self):
        return None


class _FakeDatabase:
    def __init__(self):
        self.rows = {}
        self.saver_rows = set()

    def connect(self, _dsn):
        return _FakeConnection(self)


class _FakeSaver:
    def __init__(self, database):
        self.database = database

    def __enter__(self):
        return self

    def __exit__(self, *_args):
        return False

    def put(self, config, checkpoint, _metadata, _versions):
        self.database.saver_rows.add(config["configurable"]["thread_id"])

    def delete_thread(self, identifier):
        self.database.saver_rows.discard(identifier)


def test_postgres_checkpoint_store_claims_and_consumes_only_minimal_identity():
    database = _FakeDatabase()
    now = [100]

    @contextmanager
    def saver_factory(_dsn):
        yield _FakeSaver(database)

    store = PostgresCheckpointStore(
        "postgresql://redacted",
        ttl_s=10,
        clock=lambda: now[0],
        connect=database.connect,
        saver_factory=saver_factory,
    )
    saved = store.save_asking({**scope(), "conversation_id": "conversation-1", "turn_id": "turn-1"})
    assert saved.thread_id == "tenant:conversation-1:turn-1"
    assert database.saver_rows == {saved.thread_id}
    assert "query" not in database.rows[saved.thread_id]
    assert store.resume(scope()) == saved
    assert database.rows == {}
    assert database.saver_rows == set()
    with pytest.raises(KeyError, match="not found"):
        store.resume(scope())


def test_postgres_checkpoint_store_keeps_stale_revision_and_expires_closed():
    database = _FakeDatabase()
    now = [100]

    @contextmanager
    def saver_factory(_dsn):
        yield _FakeSaver(database)

    store = PostgresCheckpointStore(
        "postgresql://redacted", ttl_s=10, clock=lambda: now[0],
        connect=database.connect, saver_factory=saver_factory,
    )
    store.save_asking(scope())
    with pytest.raises(PermissionError, match="stale"):
        store.resume(scope("2"))
    now[0] = 111
    with pytest.raises(PermissionError, match="expired"):
        store.resume(scope())
    assert database.rows == {}
    assert database.saver_rows == set()


def test_runtime_checkpoint_store_requires_explicit_dsn(monkeypatch):
    monkeypatch.delenv("V2_CHECKPOINT_DSN", raising=False)
    assert isinstance(load_checkpoint_store(), InMemoryCheckpointStore)
    monkeypatch.setenv("V2_CHECKPOINT_DSN", "postgresql://redacted")
    assert isinstance(load_checkpoint_store(), PostgresCheckpointStore)


def test_postgres_checkpoint_store_never_creates_schema_at_runtime():
    store = PostgresCheckpointStore("postgresql://redacted")
    with pytest.raises(RuntimeError, match="migration-managed"):
        store.setup()
