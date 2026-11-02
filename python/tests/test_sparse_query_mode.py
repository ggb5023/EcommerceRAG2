"""0029 step 3: the SPARSE_RETRIEVAL switch and its degradation report.

These tests pin the switch and mode selection without a database: the sparse
column probe is replaced by a fake cursor, so the three reported modes
(text_heuristic / dense_only / sparsevec) are exercised deterministically.
"""
from __future__ import annotations

import time

from app.providers.contracts import SparseWeight
from app.retrieval.postgres import PostgresRetriever, RetrievalRow


class _RecordingCursor:
    def __init__(self) -> None:
        self.calls: list[tuple[str, object]] = []

    def __enter__(self):
        return self

    def __exit__(self, *_exc):
        return False

    def execute(self, sql, params=None):
        self.calls.append((sql, params))

    def fetchone(self):
        return {"populated": True}

    def fetchall(self):
        return []


class _RecordingConnection:
    def __init__(self) -> None:
        self.cursor_instance = _RecordingCursor()

    def __enter__(self):
        return self

    def __exit__(self, *_exc):
        return False

    def cursor(self):
        return self.cursor_instance


def test_sparsevec_sql_placeholder_count_matches_parameters():
    # Regression: the sparsevec branch passed the query literal twice while the
    # SQL referenced it once, so every real sparsevec search failed with
    # "the query has 8 placeholders but 9 parameters were passed".
    connection = _RecordingConnection()
    retriever = PostgresRetriever(
        "postgresql://unused", sparse_retrieval="sparsevec",
        connect=lambda *_args, **_kwargs: connection,
    )
    retriever._sparse_mode = "sparsevec"
    retriever._sparse_mode_checked_at = time.monotonic()
    retriever._sparse_column_populated = lambda _cursor: True
    retriever.search(
        query="query",
        query_vector=[0.0] * 1024,
        scope={
            "tenant_id": "tenant", "tenant_db_id": "1", "allowed_shop_ids": ["shop"],
            "allowed_document_ids": ["doc"], "shop_id": "shop", "role": "operator",
        },
        response_purpose="internal",
        business_date="2026-09-29",
        query_sparse=[SparseWeight(token_id=1, weight=1.0)],
    )
    sparse_calls = [call for call in connection.cursor_instance.calls if "<#>" in call[0]]
    assert sparse_calls, "the sparsevec branch must execute a sparse query"
    for sql, params in sparse_calls:
        assert sql.count("%s") == len(params), (sql.count("%s"), len(params), sql)


class _ProbeCursor:
    def __init__(self, populated: bool) -> None:
        self.populated = populated
        self.executed: list[str] = []

    def execute(self, sql, params=None):
        self.executed.append(sql)

    def fetchone(self):
        return {"populated": self.populated}


def test_retriever_rejects_invalid_switch():
    try:
        PostgresRetriever("postgresql://unused", sparse_retrieval="bogus")
    except ValueError as error:
        assert "SPARSE_RETRIEVAL" in str(error)
    else:
        raise AssertionError("bogus switch must be rejected")


def test_retriever_rejects_invalid_rrf_weight():
    try:
        PostgresRetriever("postgresql://unused", sparse_rrf_weight=0.0)
    except ValueError as error:
        assert "SPARSE_RRF_WEIGHT" in str(error)
    else:
        raise AssertionError("non-positive RRF weight must be rejected")


def test_legacy_mode_reports_text_heuristic_without_probe():
    retriever = PostgresRetriever("postgresql://unused", sparse_retrieval="legacy")
    assert retriever._resolve_sparse_mode(_ProbeCursor(True), None) == "text_heuristic"


def test_sparsevec_without_query_sparse_falls_back_to_dense_only():
    retriever = PostgresRetriever("postgresql://unused", sparse_retrieval="sparsevec")
    assert retriever._resolve_sparse_mode(_ProbeCursor(True), None) == "dense_only"


def test_sparsevec_with_empty_column_falls_back_to_dense_only():
    retriever = PostgresRetriever("postgresql://unused", sparse_retrieval="sparsevec")
    assert retriever._resolve_sparse_mode(_ProbeCursor(False), "{1:1}/65536") == "dense_only"


def test_sparsevec_with_populated_column_runs_sparsevec():
    retriever = PostgresRetriever("postgresql://unused", sparse_retrieval="sparsevec")
    assert retriever._resolve_sparse_mode(_ProbeCursor(True), "{1:1}/65536") == "sparsevec"


def test_custom_rrf_weight_reaches_the_score():
    row = RetrievalRow(
        id="x", content="c", source_type="t", source_ref="r", document_id="d",
        version_id="v", tenant_id="tn", shop_id="s", disclosure_class="external_allowed",
        customer_eligible=True, raw_score=0.0, dense_rank=1, sparse_rank=1,
        metadata={}, sparse_rrf_weight=0.5,
    )
    assert abs(row.rrf_score - (1 / 61 + 0.5 / 61)) < 1e-12


def test_query_sparse_literal_is_reused_from_ingest():
    from app.ingest.sparse import to_literal
    literal, reason = to_literal([SparseWeight(token_id=0, weight=1.0)])
    assert reason is None
    assert literal == "{1:1}/65536"
