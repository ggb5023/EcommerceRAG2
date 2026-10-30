"""Scope-aware PostgreSQL/pgvector retrieval for the v2 graph.

The Go control plane still signs the scope.  This adapter only narrows that
scope while reading active, reviewed document versions; it never publishes
business state or accepts tenant/shop identifiers from the query text.
"""
from __future__ import annotations

import asyncio
import hashlib
import json
import logging
import math
import re
import time
from collections.abc import Callable, Mapping, Sequence
from dataclasses import dataclass
from datetime import date
from typing import Any

import psycopg
from psycopg.rows import dict_row


class RetrievalSchemaError(RuntimeError):
    """Raised when the migration-managed retrieval schema is unavailable."""


# Temporary weight for the sparse branch; the rationale and the A/B plan live in
# docs/decisions/0029-sparse-retrieval.md. Do not tune it without that record.
SPARSE_RRF_WEIGHT = 0.7

# The sparse branch below ranks by a text heuristic; chunk.sparse_embedding is not
# written yet (decisions/0029). Report that honestly instead of pretending the
# sparse vector path is active.
_log = logging.getLogger(__name__)
_sparse_mode_reported = False
# After step 2 lands, a running process switches from text_heuristic to sparsevec
# on its own within this window; no restart is needed.
SPARSE_MODE_TTL_SECONDS = 300.0


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

    @property
    def rrf_score(self) -> float:
        dense = 1 / (60 + self.dense_rank) if self.dense_rank else 0.0
        sparse = SPARSE_RRF_WEIGHT / (60 + self.sparse_rank) if self.sparse_rank else 0.0
        return dense + sparse

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
            "rrf_score": self.rrf_score,
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
    """Read authorized, reviewed chunks through dense and lexical channels."""

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
        self._sparse_mode: str | None = None
        self._sparse_mode_checked_at = 0.0
        self.last_sparse_mode: str | None = None

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
        if response_purpose not in {"internal", "customer_reply", "customer_chat"}:
            raise ValueError("response purpose is invalid")
        return tenant, tenant_db_id, shops, documents, selected_shop, role

    @staticmethod
    def _disclosure_clause(response_purpose: str, role: str) -> tuple[str, list[Any]]:
        if response_purpose in {"customer_reply", "customer_chat"} or role not in {"owner", "admin"}:
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
            "AND c.content_type <> 'heading' AND length(btrim(c.content)) > 0 "
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
        distance_value = row.get("distance")
        distance = float(distance_value) if distance_value is not None else 1.0
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
            "SELECT * FROM (SELECT " + columns + "c.embedding <=> %s::halfvec(1024) AS distance, "
            "GREATEST(similarity(c.embed_text, %s), COALESCE("
            "(SELECT avg((strpos(lower(c.embed_text), term) > 0)::int) "
            "FROM unnest(%s::text[]) AS query_terms(term)), 0)) AS sparse_score "
            "FROM chunk c " + where + " " + selected_sql +
            ") AS authorized WHERE sparse_score > 0 "
            "ORDER BY sparse_score DESC, chunk_id LIMIT %s"
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
                [vector, query, sorted(_terms(query)), *params_common, *selected_params, self._sparse_candidates],
            )
            sparse_rows = cursor.fetchall()
        self._report_sparse_mode(cursor)

        dense_by_id = {str(row["chunk_id"]): (row, rank) for rank, row in enumerate(dense_rows, 1)}
        sparse_rank_by_id = {str(row["chunk_id"]): rank for rank, row in enumerate(sparse_rows, 1)}
        combined: list[RetrievalRow] = []
        for chunk_id, (row, dense_rank) in dense_by_id.items():
            sparse_rank = sparse_rank_by_id.get(chunk_id, 0)
            candidate = self._row_to_candidate(row, rank=dense_rank, sparse_rank=sparse_rank, tenant_id=tenant)
            combined.append(candidate)
        for chunk_id, row in ((str(row["chunk_id"]), row) for row in sparse_rows):
            if chunk_id in dense_by_id:
                continue
            sparse_rank = sparse_rank_by_id[chunk_id]
            candidate = self._row_to_candidate(
                row,
                rank=0,
                sparse_rank=sparse_rank,
                tenant_id=tenant,
            )
            combined.append(candidate)
        combined.sort(key=lambda item: (-item.rrf_score, item.id))
        return [item.as_evidence() for item in combined[:limit]]

    def _report_sparse_mode(self, cursor: Any, ttl: float = SPARSE_MODE_TTL_SECONDS) -> None:
        """Expose which sparse path ran, and warn once while the column stays empty.

        The probe is cached for ``ttl`` seconds (``ttl=0`` forces a re-check), so a
        long-lived process notices step 2 landing without a restart.
        """
        global _sparse_mode_reported
        if self._sparse_mode is not None and ttl > 0 and time.monotonic() - self._sparse_mode_checked_at < ttl:
            return
        try:
            cursor.execute("SELECT EXISTS(SELECT 1 FROM chunk WHERE sparse_embedding IS NOT NULL) AS populated")
            row = cursor.fetchone()
        except psycopg.Error:  # the probe is diagnostic: never fail a search over it
            return
        # The connection uses dict_row, so read by name instead of position.
        if isinstance(row, dict):
            populated = bool(row.get("populated"))
        else:
            populated = bool(row[0]) if row else False
        self._sparse_mode = "sparsevec" if populated else "text_heuristic"
        self._sparse_mode_checked_at = time.monotonic()
        self.last_sparse_mode = self._sparse_mode
        if not populated and not _sparse_mode_reported:
            _sparse_mode_reported = True
            _log.warning(
                "sparse retrieval is running on the text heuristic: chunk.sparse_embedding is never written "
                "(see docs/decisions/0029-sparse-retrieval.md); sparse contribution is zero for vector terms"
            )

    async def asearch(self, **kwargs: Any) -> list[dict[str, Any]]:
        """Run blocking psycopg I/O outside the event loop used by gRPC."""
        return await asyncio.to_thread(self.search, **kwargs)

    async def arecord_trace(self, scope: Mapping[str, Any], candidates: Sequence[Mapping],
                            evidence: Sequence[Mapping]) -> None:
        """AI-owned metadata only; never store prompts, text or credentials."""
        tenant = str(scope.get("tenant_db_id", ""))
        request_id = str(scope.get("request_id", ""))
        if not tenant.isdigit() or not request_id:
            raise ValueError("retrieval trace identity is missing")
        customer = scope.get("subject_type") == "customer"
        execution_id = str(scope.get("execution_id", ""))
        if customer and (not execution_id or request_id != execution_id):
            raise ValueError("customer retrieval trace execution is missing or inconsistent")
        def write():
            with self._connect(self._conn_string) as conn:
                for stage, items in (("v2_candidates", candidates), ("v2_rerank", evidence)):
                    rows = [{"id": e["id"], "document_id": e["document_id"], "version_id": e["version_id"],
                             "rank": i, "raw_score": e.get("raw_score"), "rrf_score": e.get("rrf_score"),
                             "rerank_score": e.get("rerank_score")} for i, e in enumerate(items, 1)]
                    if customer:
                        conn.execute("INSERT INTO customer_retrieval_trace(tenant_id,execution_id,stage,candidates_json) "
                                     "VALUES(%s,%s,%s,%s::jsonb)", (tenant, execution_id, stage, json.dumps(rows)))
                    else:
                        conn.execute("INSERT INTO retrieval_trace(tenant_id,request_id,stage,candidates_json) "
                                     "VALUES(%s,%s,%s,%s::jsonb)", (tenant, request_id, stage, json.dumps(rows)))
        await asyncio.to_thread(write)

    async def arecord_generation_input(self, scope: Mapping[str, Any], evidence: Sequence[Mapping],
                                       *, context_sha256: str, request_sha256: str) -> None:
        """Constructed generation input only, never a provider receipt or text."""
        tenant = str(scope.get("tenant_db_id", ""))
        execution = str(scope.get("execution_id", ""))
        if (scope.get("subject_type") != "customer" or not tenant.isdigit() or not execution
                or scope.get("request_id") != execution):
            raise ValueError("customer generation trace identity is inconsistent")
        rows = [{"id": item["id"], "document_id": item["document_id"], "version_id": item["version_id"],
                 "rank": item.get("rank", index),
                 "content_sha256": hashlib.sha256(item["content"].encode()).hexdigest(),
                 "context_sha256": context_sha256, "request_sha256": request_sha256}
                for index, item in enumerate(evidence, 1)]

        def write():
            with self._connect(self._conn_string) as conn:
                conn.execute("INSERT INTO customer_retrieval_trace(tenant_id,execution_id,stage,candidates_json) "
                             "VALUES(%s,%s,'v2_generation_input',%s::jsonb)",
                             (tenant, execution, json.dumps(rows)))
        await asyncio.to_thread(write)
