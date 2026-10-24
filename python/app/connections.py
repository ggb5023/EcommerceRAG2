"""Role-bound PostgreSQL connection pools for the single ai process.

One process, two service roles.  Retrieval and checkpoints check out of the
``rag_v2_ai`` pool; ingestion RPC and the worker check out of the
``rag_v2_ingest`` pool.  The pool is created from a DSN whose ``current_user``
is verified once at startup, and it refuses a connection string that names a
different role or database, so a merged process cannot silently write through
the other plane's identity.
"""
from __future__ import annotations

from collections.abc import Iterator
from contextlib import contextmanager
from dataclasses import dataclass
from typing import Any

from psycopg.conninfo import conninfo_to_dict
from psycopg_pool import ConnectionPool

from app.configuration import verify_database

AI_ROLE = "rag_v2_ai"
INGEST_ROLE = "rag_v2_ingest"
AI_POOL_SIZE = 4
INGEST_POOL_SIZE = 2
_TARGET_KEYS = ("host", "port", "dbname", "user")
_POOL_CHECK = ConnectionPool.check_connection


class ConnectionsError(RuntimeError):
    """A pool was asked for a target it does not own."""


@dataclass(frozen=True)
class RoleConnections:
    role: str
    dsn: str
    pool: ConnectionPool

    def connect(self, conn_string: str | None = None, **kwargs: Any):
        """Connection factory compatible with ``connect(dsn, row_factory=...)``."""
        if conn_string is not None and not self.owns(conn_string):
            raise ConnectionsError("connection pool refused a foreign database identity")
        return self._checkout(**kwargs)

    def owns(self, conn_string: str) -> bool:
        try:
            candidate = conninfo_to_dict(conn_string)
        except Exception:  # noqa: BLE001 - an unparsable DSN is never ours
            return False
        mine = conninfo_to_dict(self.dsn)
        return all(mine.get(key) == candidate.get(key) for key in _TARGET_KEYS)

    def check_role(self, expected_role: str) -> None:
        """Reject a pool handed to the wrong plane without another round trip."""
        if expected_role != self.role:
            raise ConnectionsError("pool role does not match the caller's expectation")

    def verify(self, expected_role: str) -> None:
        self.check_role(expected_role)
        verify_database(self.dsn, self.role)

    def close(self) -> None:
        self.pool.close()

    @contextmanager
    def _checkout(self, **kwargs: Any) -> Iterator[Any]:
        row_factory = kwargs.pop("row_factory", None)
        if kwargs:
            raise ConnectionsError("pooled connections accept no extra options")
        with self.pool.connection() as connection:
            previous = connection.row_factory
            if row_factory is not None:
                connection.row_factory = row_factory
            try:
                yield connection
            finally:
                connection.row_factory = previous


def open_role(role: str, dsn: str, *, size: int, application_name: str) -> RoleConnections:
    """Verify the role and open its pool before any listener is bound."""
    if role not in {AI_ROLE, INGEST_ROLE}:
        raise ConnectionsError("pool role is not an owned service role")
    if size < 1:
        raise ConnectionsError("pool size must be positive")
    verify_database(dsn, role)
    pool = ConnectionPool(
        dsn,
        min_size=1,
        max_size=size,
        open=False,
        kwargs={"application_name": application_name, "connect_timeout": 5},
        check=_POOL_CHECK,
        max_lifetime=1800.0,
        name=application_name,
    )
    pool.open(wait=True, timeout=10)
    return RoleConnections(role=role, dsn=dsn, pool=pool)
