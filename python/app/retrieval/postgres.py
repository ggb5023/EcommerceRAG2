"""Scope-aware PostgreSQL/pgvector retrieval for the v2 graph.

The Go control plane still signs the scope.  This adapter only narrows that
scope while reading active, reviewed document versions; it never publishes
business state or accepts tenant/shop identifiers from the query text.
"""
from __future__ import annotations

import asyncio
import json
import math
import re
from collections.abc import Callable, Mapping, Sequence
from dataclasses import dataclass
from datetime import date
from typing import Any

import psycopg
from psycopg.rows import dict_row


class RetrievalSchemaError(RuntimeError):
    """Raised when the migration-managed retrieval schema is unavailable."""


@dataclass(frozen=True)
class RetrievalRow:
    """A normalized evidence row returned by the persistent retriever."""

    id: str
    content: str
    source_type: str
    source_ref: str
    document_id: str
    version_id: str
    tenant_id: str
    shop_id: str
    disclosure_class: str
    customer_eligible: bool
    raw_score: float
    dense_rank: int
    sparse_rank: int
    metadata: dict[str, Any]

    def as_evidence(self) -> dict[str, Any]:
        return {
            "id": self.id,
            "content": self.content,
            "source_type": self.source_type,
            "source_ref": self.source_ref,
            "document_id": self.document_id,
            "version_id": self.version_id,
            "tenant_id": self.tenant_id,
            "shop_id": self.shop_id,
            "disclosure_class": self.disclosure_class,
            "customer_eligible": self.customer_eligible,
            "raw_score": self.raw_score,
            "dense_rank": self.dense_rank,
            "sparse_rank": self.sparse_rank,
            "metadata": self.metadata,
        }


_TERM_RE = re.compile(r"[a-z0-9_]+|[\u4e00-\u9fff]+", re.IGNORECASE)


def _vector_literal(values: Sequence[float]) -> str:
    if len(values) != 1024:
        raise ValueError("query embedding must contain 1024 dimensions")
    normalized: list[str] = []
    for value in values:
        if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(float(value)):
            raise ValueError("query embedding contains a non-finite value")
        normalized.append(format(float(value), ".9g"))
    return "[" + ",".join(normalized) + "]"


def _terms(value: str) -> set[str]:
    result: set[str] = set()
    for run in _TERM_RE.findall(value.lower()):
        if run.isascii():
            result.add(run)
        else:
            result.update(run[index:index + size] for size in (2, 3, 4)
                          for index in range(len(run)) if index + size <= len(run))
    return result


class PostgresRetriever:
    """Read active, reviewed chunks through dense and trigram channels."""

    _REQUIRED_TABLES = ("tenant", "document", "document_version", "chunk")
    _REQUIRED_INDEXES = (
        "idx_v2_document_active_scope",
        "idx_v2_chunk_scope_version",
        "idx_v2_chunk_embed_text_trgm",
    )

    def __init__(
        self,
        conn_string: str,
        *,
        connect: Callable[..., Any] = psycopg.connect,
        dense_candidates: int = 32,
        sparse_candidates: int = 32,
    ) -> None:
        if not conn_string.strip():
            raise ValueError("retrieval connection string is required")
        if dense_candidates < 1 or sparse_candidates < 1:
            raise ValueError("retrieval candidate limits must be positive")
        self._conn_string = conn_string
        self._connect = connect
        self._dense_candidates = dense_candidates
        self._sparse_candidates = sparse_candidates

    def verify_schema(self) -> None:
        """Fail closed unless the checksum migration has installed retrieval tables."""
        placeholders = ",".join("%s" for _ in self._REQUIRED_TABLES)
        with self._connect(self._conn_string) as connection, connection.cursor() as cursor:
            cursor.execute(
                f"SELECT table_name FROM information_schema.tables "
                f"WHERE table_schema='public' AND table_name IN ({placeholders})",
                self._REQUIRED_TABLES,
            )
            found = {row[0] for row in cursor.fetchall()}
            cursor.execute("SELECT to_regtype('public.halfvec')")
            halfvec = cursor.fetchone()[0]
            cursor.execute(
                "SELECT indexname FROM pg_indexes "
                "WHERE schemaname='public' AND indexname = ANY(%s::text[])",
                (list(self._REQUIRED_INDEXES),),
            )
            indexes = {row[0] for row in cursor.fetchall()}
            cursor.execute(
                "SELECT EXISTS(SELECT 1 FROM schema_migration WHERE version='0018'), "
                "to_regprocedure('public.similarity(text,text)')",
            )
            migration_applied, similarity = cursor.fetchone()
        missing = set(self._REQUIRED_TABLES) - found
        missing_indexes = set(self._REQUIRED_INDEXES) - indexes
        if missing or missing_indexes or halfvec is None or not migration_applied or similarity is None:
            raise RetrievalSchemaError("retrieval schema migration is incomplete")

    @staticmethod
    def _scope_params(
        scope: Mapping[str, Any], response_purpose: str,
    ) -> tuple[str, str, list[str], list[str], str, str]:
        tenant = str(scope.get("tenant_id", "")).strip()
        tenant_db_id = str(scope.get("tenant_db_id", "")).strip()
        shops = [str(value) for value in scope.get("allowed_shop_ids", []) if str(value).strip()]
        documents = [str(value) for value in scope.get("allowed_document_ids", []) if str(value).strip()]
        selected_shop = str(scope.get("shop_id", "")).strip()
        role = str(scope.get("role", "")).strip()
        if not tenant or not tenant_db_id or not shops or not documents:
            raise PermissionError("persistent retrieval scope is incomplete")
        if not tenant_db_id.isdigit() or int(tenant_db_id) < 1:
            raise PermissionError("persistent retrieval tenant database id is invalid")
        if selected_shop and selected_shop not in set(shops):
            raise PermissionError("selected shop is outside the scope")
        if response_purpose not in {"internal", "customer_reply"}:
            raise ValueError("response purpose is invalid")
        return tenant, tenant_db_id, shops, documents, selected_shop, role

    @staticmethod
    def _disclosure_clause(response_purpose: str, role: str) -> tuple[str, list[Any]]:
        if response_purpose == "customer_reply" or role not in {"owner", "admin"}:
            return "AND v.disclosure_class = 'external_allowed' AND v.external_allowed ", []
        return "", []

    @staticmethod
    def _date_clause(business_date: str | None) -> tuple[str, list[Any]]:
        if not business_date:
            return "", []
        try:
            date.fromisoformat(business_date)
        except ValueError as error:
            raise ValueError("business date is invalid") from error
        clause = "AND (c.effective_from IS NULL OR c.effective_from <= %s::date) AND "
        clause += "(c.effective_to IS NULL OR c.effective_to > %s::date)"
        return clause, [business_date, business_date]

    @staticmethod
    def _base_where(
        response_purpose: str, role: str, business_date: str | None,
    ) -> tuple[str, list[Any]]:
        disclosure_sql, disclosure_params = PostgresRetriever._disclosure_clause(response_purpose, role)
        date_sql, date_params = PostgresRetriever._date_clause(business_date)
        return (
            "JOIN document_version v ON v.tenant_id = c.tenant_id AND v.id = c.version_id "
            "JOIN document d ON d.tenant_id = c.tenant_id AND d.id = c.doc_id "
            "JOIN tenant t ON t.id = c.tenant_id "
            "WHERE t.id = %s::bigint AND t.status = 'active' "
            "AND c.shop_id = ANY(%s::text[]) "
            "AND d.logical_key = ANY(%s::text[]) "
            "AND d.active_version_id = v.id AND d.status = 'active' AND v.status = 'active' "
            "AND v.review_status = 'approved' "
            + disclosure_sql + date_sql,
            disclosure_params + date_params,
        )

    @staticmethod
    def _row_to_candidate(
        row: Mapping[str, Any], *, rank: int, sparse_rank: int, tenant_id: str,
    ) -> RetrievalRow:
        metadata = row.get("meta_json") or {}
        if isinstance(metadata, str):
            try:
                metadata = json.loads(metadata)
            except json.JSONDecodeError:
                metadata = {}
        if not isinstance(metadata, dict):
            metadata = {}
        distance = float(row.get("distance") or 1.0)
        return RetrievalRow(
            id=f"db-chunk-{row['chunk_id']}",
            content=str(row["content"]),
            source_type="postgres_chunk",
            source_ref=str(row.get("source_ref") or f"db://chunk/{row['chunk_id']}"),
            document_id=str(row["document_id"]),
            version_id=str(row["version_id"]),
            tenant_id=tenant_id,
            shop_id=str(row["shop_id"]),
            disclosure_class=str(row["disclosure_class"]),
            customer_eligible=bool(row["external_allowed"] and row["disclosure_class"] == "external_allowed"),
            raw_score=max(-1.0, min(1.0, 1.0 - distance)),
            dense_rank=rank,
            sparse_rank=sparse_rank,
            metadata=metadata,
        )

    def search(
        self,
        *,
        query: str,
        query_vector: Sequence[float],
        scope: Mapping[str, Any],
        response_purpose: str = "internal",
        business_date: str | None = None,
        limit: int = 8,
    ) -> list[dict[str, Any]]:
        if not query.strip():
            raise ValueError("query is required")
        if not 1 <= limit <= 20:
            raise ValueError("retrieval limit must be between 1 and 20")
        vector = _vector_literal(query_vector)
        tenant, tenant_db_id, shops, documents, selected_shop, role = self._scope_params(scope, response_purpose)
        where, date_params = self._base_where(response_purpose, role, business_date)
        selected_sql = "AND c.shop_id = %s" if selected_shop else ""
        selected_params: list[Any] = [selected_shop] if selected_shop else []
        columns = (
            "c.id AS chunk_id, c.content, c.chunk_hash, c.source_object_key AS source_ref, "
            "d.logical_key AS document_id, v.id AS version_id, t.name AS tenant_id, c.shop_id, "
            "v.disclosure_class, v.external_allowed, c.meta_json, "
        )
        dense_sql = (
            "SELECT " + columns + "c.embedding <=> %s::halfvec(1024) AS distance "
            "FROM chunk c " + where + " " + selected_sql +
            " AND c.embedding IS NOT NULL "
            "ORDER BY c.embedding <=> %s::halfvec(1024), c.id LIMIT %s"
        )
        sparse_sql = (
            "SELECT " + columns + "c.embedding <=> %s::halfvec(1024) AS distance, "
            "similarity(c.embed_text, %s) AS sparse_score "
            "FROM chunk c " + where + " " + selected_sql +
            " ORDER BY similarity(c.embed_text, %s) DESC, c.id LIMIT %s"
        )
        params_common = [tenant_db_id, shops, documents, *date_params]
        with self._connect(self._conn_string, row_factory=dict_row) as connection, connection.cursor() as cursor:
            cursor.execute(
                dense_sql,
                [vector, *params_common, *selected_params, vector, self._dense_candidates],
            )
            dense_rows = cursor.fetchall()
            cursor.execute(
                sparse_sql,
                [vector, query, *params_common, *selected_params, query, self._sparse_candidates],
            )
            sparse_rows = cursor.fetchall()

        dense_by_id = {str(row["chunk_id"]): (row, rank) for rank, row in enumerate(dense_rows, 1)}
        sparse_rank_by_id = {str(row["chunk_id"]): rank for rank, row in enumerate(sparse_rows, 1)}
        combined: list[RetrievalRow] = []
        for chunk_id, (row, dense_rank) in dense_by_id.items():
            sparse_rank = sparse_rank_by_id.get(chunk_id, self._sparse_candidates + 1)
            candidate = self._row_to_candidate(row, rank=dense_rank, sparse_rank=sparse_rank, tenant_id=tenant)
            combined.append(candidate)
        for chunk_id, row in ((str(row["chunk_id"]), row) for row in sparse_rows):
            if chunk_id in dense_by_id:
                continue
            sparse_rank = sparse_rank_by_id[chunk_id]
            candidate = self._row_to_candidate(
                row,
                rank=self._dense_candidates + 1,
                sparse_rank=sparse_rank,
                tenant_id=tenant,
            )
            combined.append(candidate)
        combined.sort(key=lambda item: (-(1 / (60 + item.dense_rank) + 0.7 / (60 + item.sparse_rank)), item.id))
        return [item.as_evidence() | {"rrf_score": 1 / (60 + item.dense_rank) + 0.7 / (60 + item.sparse_rank)}
                for item in combined[:limit]]

    async def asearch(self, **kwargs: Any) -> list[dict[str, Any]]:
        """Run blocking psycopg I/O outside the event loop used by gRPC."""
        return await asyncio.to_thread(self.search, **kwargs)
