"""Minimal ASKING/HITL checkpoint with scope-bound resume semantics.

This store intentionally excludes query text, evidence, chat history and model
output.  Go remains the source of business state; a caller must provide a new
query and a freshly validated scope when resuming.
"""
from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass
from datetime import UTC, datetime
from threading import RLock
from time import time
from typing import Any, Protocol
from uuid import uuid4

import psycopg
from langgraph.checkpoint.base import Checkpoint as LangGraphCheckpoint
from langgraph.checkpoint.postgres import PostgresSaver

from .state import Scope


@dataclass(frozen=True)
class Checkpoint:
    thread_id: str
    tenant_id: str
    user_id: str
    conversation_id: str
    turn_id: str
    permission_revision: str
    pending_kind: str
    created_at: int
    expires_at: int


def _validate_checkpoint_scope(scope: Scope) -> str:
    required = ("tenant_id", "conversation_id", "turn_id")
    if any(not str(scope.get(key, "")).strip() for key in required):
        raise PermissionError("checkpoint scope is missing conversation or turn identity")
    return ":".join(str(scope[key]) for key in required)


def thread_id(scope: Scope) -> str:
    return _validate_checkpoint_scope(scope)


def _new_checkpoint(scope: Scope, *, ttl_s: int, clock: Callable[[], float]) -> Checkpoint:
    current = int(clock())
    identifier = thread_id(scope)
    revision = str(scope.get("permission_revision", "")).strip()
    if not revision or not str(scope.get("user_id", "")).strip():
        raise PermissionError("checkpoint scope is missing identity")
    return Checkpoint(
        thread_id=identifier,
        tenant_id=str(scope["tenant_id"]),
        user_id=str(scope["user_id"]),
        conversation_id=str(scope["conversation_id"]),
        turn_id=str(scope["turn_id"]),
        permission_revision=revision,
        pending_kind="ASKING",
        created_at=current,
        expires_at=current + ttl_s,
    )


class CheckpointStore(Protocol):
    def save_asking(self, scope: Scope) -> Checkpoint: ...

    def resume(self, scope: Scope) -> Checkpoint: ...


class InMemoryCheckpointStore:
    """Development store; production must replace it with an audited saver."""

    def __init__(self, *, ttl_s: int = 900, clock=time) -> None:
        if ttl_s <= 0:
            raise ValueError("checkpoint ttl must be positive")
        self._ttl_s = ttl_s
        self._clock = clock
        self._records: dict[str, Checkpoint] = {}
        self._lock = RLock()

    def save_asking(self, scope: Scope) -> Checkpoint:
        current = int(self._clock())
        record = _new_checkpoint(scope, ttl_s=self._ttl_s, clock=lambda: current)
        with self._lock:
            self._records[record.thread_id] = record
        return record

    def resume(self, scope: Scope) -> Checkpoint:
        identifier = thread_id(scope)
        now = int(self._clock())
        with self._lock:
            record = self._records.get(identifier)
            if record is None:
                raise KeyError("checkpoint not found")
            if record.expires_at <= now:
                del self._records[identifier]
                raise PermissionError("checkpoint expired")
            if (
                record.tenant_id != scope.get("tenant_id")
                or record.user_id != scope.get("user_id")
                or record.permission_revision != scope.get("permission_revision")
            ):
                raise PermissionError("checkpoint scope is stale")
            del self._records[identifier]
            return record

    def clear(self) -> None:
        with self._lock:
            self._records.clear()


class PostgresCheckpointStore:
    """Durable minimal ASKING checkpoint backed by PostgresSaver.

    The LangGraph saver stores an empty graph checkpoint in its own tables;
    the small control table created by migration 0015 provides TTL, permission
    revision checks and an atomic single-consumer claim. No query, evidence or
    chat content is written by this class.
    """

    _UPSERT_SQL = """
        INSERT INTO v2_asking_checkpoint
          (thread_id, tenant_id, user_id, conversation_id, turn_id,
           permission_revision, pending_kind, created_at, expires_at,
           checkpoint_id, consumed_at)
        VALUES (%s, %s, %s, %s, %s, %s, %s, to_timestamp(%s),
                to_timestamp(%s), %s, NULL)
        ON CONFLICT (thread_id) DO UPDATE SET
          tenant_id=EXCLUDED.tenant_id,
          user_id=EXCLUDED.user_id,
          conversation_id=EXCLUDED.conversation_id,
          turn_id=EXCLUDED.turn_id,
          permission_revision=EXCLUDED.permission_revision,
          pending_kind=EXCLUDED.pending_kind,
          created_at=EXCLUDED.created_at,
          expires_at=EXCLUDED.expires_at,
          checkpoint_id=EXCLUDED.checkpoint_id,
          consumed_at=NULL
    """
    _CLAIM_SQL = """
        UPDATE v2_asking_checkpoint
           SET consumed_at=to_timestamp(%s)
         WHERE thread_id=%s AND tenant_id=%s AND user_id=%s
           AND permission_revision=%s AND consumed_at IS NULL
           AND expires_at > to_timestamp(%s)
         RETURNING tenant_id, user_id, conversation_id, turn_id,
                   permission_revision, pending_kind,
                   EXTRACT(EPOCH FROM created_at)::bigint,
                   EXTRACT(EPOCH FROM expires_at)::bigint,
                   checkpoint_id
    """
    _LOOKUP_SQL = """
        SELECT tenant_id, user_id, conversation_id, turn_id,
               permission_revision, pending_kind,
               EXTRACT(EPOCH FROM created_at)::bigint,
               EXTRACT(EPOCH FROM expires_at)::bigint,
               consumed_at, checkpoint_id
          FROM v2_asking_checkpoint
         WHERE thread_id=%s
    """
    _DELETE_SQL = "DELETE FROM v2_asking_checkpoint WHERE thread_id=%s"

    def __init__(
        self,
        conn_string: str,
        *,
        ttl_s: int = 900,
        clock: Callable[[], float] = time,
        connect: Callable[..., Any] = psycopg.connect,
        saver_factory: Callable[..., Any] = PostgresSaver.from_conn_string,
    ) -> None:
        if not conn_string.strip():
            raise ValueError("checkpoint connection string is required")
        if ttl_s <= 0:
            raise ValueError("checkpoint ttl must be positive")
        self._conn_string = conn_string
        self._ttl_s = ttl_s
        self._clock = clock
        self._connect = connect
        self._saver_factory = saver_factory

    def setup(self) -> None:
        """Reject runtime schema creation; migration 0015 owns these tables.

        ``PostgresSaver.setup`` performs DDL and needs a migration role.  The
        AI application role must never create or upgrade its own checkpoint
        schema, so deployments apply ``sql/migrations/0015_v2_graph_checkpoint.sql``
        before starting this service.
        """
        raise RuntimeError("checkpoint schema is migration-managed; apply migration 0015")

    def verify_schema(self) -> None:
        """Fail closed when the migration-managed saver schema is absent."""
        with self._connect(self._conn_string) as connection, connection.cursor() as cursor:
            cursor.execute(
                """
                SELECT
                  to_regclass('public.v2_asking_checkpoint'),
                  to_regclass('public.checkpoint_migrations'),
                  to_regclass('public.checkpoints'),
                  to_regclass('public.checkpoint_blobs'),
                  to_regclass('public.checkpoint_writes'),
                  (SELECT max(v) FROM checkpoint_migrations)
                """
            )
            row = cursor.fetchone()
        if not row or any(value is None for value in row[:5]) or row[5] is None or row[5] < 9:
            raise RuntimeError("checkpoint schema migration 0015 is not applied")

    @staticmethod
    def _langgraph_checkpoint(record: Checkpoint, checkpoint_id: str) -> LangGraphCheckpoint:
        return {
            "v": 1,
            "id": checkpoint_id,
            "ts": datetime.fromtimestamp(record.created_at, UTC).isoformat(),
            "channel_values": {},
            "channel_versions": {},
            "versions_seen": {},
            "updated_channels": [],
        }

    def save_asking(self, scope: Scope) -> Checkpoint:
        record = _new_checkpoint(scope, ttl_s=self._ttl_s, clock=self._clock)
        checkpoint_id = uuid4().hex
        with self._connect(self._conn_string) as connection:
            with connection.cursor() as cursor:
                cursor.execute(
                    self._UPSERT_SQL,
                    (
                        record.thread_id,
                        record.tenant_id,
                        record.user_id,
                        record.conversation_id,
                        record.turn_id,
                        record.permission_revision,
                        record.pending_kind,
                        record.created_at,
                        record.expires_at,
                        checkpoint_id,
                    ),
                )
            connection.commit()
        try:
            with self._saver_factory(self._conn_string) as saver:
                saver.put(
                    {"configurable": {"thread_id": record.thread_id, "checkpoint_ns": "v2_asking"}},
                    self._langgraph_checkpoint(record, checkpoint_id),
                    {
                        "source": "update",
                        "step": -1,
                        "parents": {},
                        "writes": {
                            "pending_kind": record.pending_kind,
                            "tenant_id": record.tenant_id,
                            "user_id": record.user_id,
                            "conversation_id": record.conversation_id,
                            "turn_id": record.turn_id,
                            "permission_revision": record.permission_revision,
                            "expires_at": record.expires_at,
                        },
                    },
                    {},
                )
        except (OSError, RuntimeError, ValueError, psycopg.Error):
            self._delete_control(record.thread_id)
            raise
        return record

    def resume(self, scope: Scope) -> Checkpoint:
        record = _new_checkpoint(scope, ttl_s=self._ttl_s, clock=self._clock)
        now = int(self._clock())
        with self._connect(self._conn_string) as connection:
            with connection.cursor() as cursor:
                cursor.execute(
                    self._CLAIM_SQL,
                    (
                        now,
                        record.thread_id,
                        record.tenant_id,
                        record.user_id,
                        record.permission_revision,
                        now,
                    ),
                )
                row = cursor.fetchone()
            connection.commit()
        if row is None:
            self._raise_resume_failure(record.thread_id, scope, now)
        claimed = Checkpoint(
            thread_id=record.thread_id,
            tenant_id=row[0],
            user_id=row[1],
            conversation_id=row[2],
            turn_id=row[3],
            permission_revision=row[4],
            pending_kind=row[5],
            created_at=int(row[6]),
            expires_at=int(row[7]),
        )
        try:
            self._delete_saver(claimed.thread_id)
        except (OSError, RuntimeError, ValueError, psycopg.Error) as error:
            raise RuntimeError("checkpoint saver cleanup failed") from error
        self._delete_control(claimed.thread_id)
        return claimed

    def _delete_saver(self, identifier: str) -> None:
        with self._saver_factory(self._conn_string) as saver:
            saver.delete_thread(identifier)

    def _delete_control(self, identifier: str) -> None:
        with self._connect(self._conn_string) as connection:
            with connection.cursor() as cursor:
                cursor.execute(self._DELETE_SQL, (identifier,))
            connection.commit()

    def _raise_resume_failure(self, identifier: str, scope: Scope, now: int) -> None:
        with self._connect(self._conn_string) as connection, connection.cursor() as cursor:
            cursor.execute(self._LOOKUP_SQL, (identifier,))
            row = cursor.fetchone()
        if row is None:
            raise KeyError("checkpoint not found")
        if row[8] is not None:
            raise KeyError("checkpoint already consumed")
        if int(row[7]) <= now:
            try:
                self._delete_saver(identifier)
            except (OSError, RuntimeError, ValueError, psycopg.Error) as error:
                raise RuntimeError("checkpoint saver cleanup failed") from error
            self._delete_control(identifier)
            raise PermissionError("checkpoint expired")
        if (
            row[0] != scope.get("tenant_id")
            or row[1] != scope.get("user_id")
            or row[4] != scope.get("permission_revision")
        ):
            raise PermissionError("checkpoint scope is stale")
        raise PermissionError("checkpoint could not be claimed")
