from __future__ import annotations

import os
import sys
import uuid
from pathlib import Path

import pytest

from app.ingest.sparse import to_literal
from app.providers.contracts import SparseWeight
from app.retrieval.postgres import PostgresRetriever, _vector_literal


def _create_mixed_retrieval_fixture(dsn: str, key: str, tokenizer: str):
    """Create one approved document with a sparse chunk and a dense-only chunk."""
    import psycopg

    sparse_literal, reason = to_literal([SparseWeight(token_id=0, weight=1.0)])
    assert reason is None
    vector = "[1," + ",".join("0" for _ in range(1023)) + "]"
    source_hash = "a" * 64
    with psycopg.connect(dsn) as connection:
        tenant_id, user_id = connection.execute(
            "SELECT t.id, u.id FROM tenant t JOIN app_user u ON u.tenant_id=t.id "
            "WHERE t.name=%s AND u.external_id=%s",
            ("V2 LangGraph fixture tenant", "dev-user"),
        ).fetchone()
        source_id = connection.execute(
            "INSERT INTO source(tenant_id,shop_id,type,uri) VALUES(%s,%s,%s,%s) RETURNING id",
            (tenant_id, "shop-demo", "v2_retrieval_test", "fixture://" + key),
        ).fetchone()[0]
        document_id = connection.execute(
            "INSERT INTO document(tenant_id,source_id,logical_key,title,doc_type,status,meta_json) "
            "VALUES(%s,%s,%s,%s,%s,'pending',%s::jsonb) RETURNING id",
            (tenant_id, source_id, key, "WP52 mixed fixture", "markdown", '{"shop_id":"shop-demo"}'),
        ).fetchone()[0]
        version_id = connection.execute(
            "INSERT INTO document_version(tenant_id,document_id,source_hash,parser_version,"
            "chunk_rule_version,embedding_model,pipeline_version,object_key,status,chunk_count,"
            "disclosure_class,external_allowed,review_status,reviewed_hash,reviewed_by,reviewed_at) "
            "VALUES(%s,%s,%s,'test-parser','structured-v2','test-embedding','test-pipeline',"
            "concat('db://test/',%s::text),'active',2,'external_allowed',true,'approved',%s,%s,now()) RETURNING id",
            (tenant_id, document_id, source_hash, key, source_hash, user_id),
        ).fetchone()[0]
        connection.execute(
            "UPDATE document SET active_version_id=%s,status='active' WHERE tenant_id=%s AND id=%s",
            (version_id, tenant_id, document_id),
        )
        for index, (content, sparse, sparse_tokenizer) in enumerate((
            ("wp52 sparse policy", sparse_literal, tokenizer),
            ("wp52 dense policy", None, None),
        )):
            connection.execute(
                "INSERT INTO chunk(version_id,doc_id,tenant_id,shop_id,chunk_index,section_seq,"
                "section_chunk_index,char_start,char_end,heading_path,content,embed_text,token_count,"
                "content_type,split_reason,embedding,sparse_embedding,sparse_tokenizer_id,"
                "embedding_model,embedding_dim,tokenizer_id,meta_json,chunk_hash) "
                "VALUES(%s,%s,%s,%s,%s,1,%s,0,%s,%s,%s,%s,4,'text','test',"
                "%s::halfvec(1024),%s::sparsevec,%s,'test',1024,'test-tokenizer','{}'::jsonb,%s)",
                (version_id, document_id, tenant_id, "shop-demo", index, index, len(content),
                 "WP52", content, content, vector, sparse, sparse_tokenizer,
                 f"{key}-{index}"),
            )
        connection.commit()
    return tenant_id, source_id, document_id, version_id


def _delete_mixed_retrieval_fixture(dsn: str, fixture) -> None:
    import psycopg

    _, source_id, document_id, version_id = fixture
    with psycopg.connect(dsn) as connection:
        connection.execute("DELETE FROM chunk WHERE version_id=%s", (version_id,))
        connection.execute("UPDATE document SET active_version_id=NULL WHERE id=%s", (document_id,))
        connection.execute("DELETE FROM document_version WHERE id=%s", (version_id,))
        connection.execute("DELETE FROM document WHERE id=%s", (document_id,))
        connection.execute("DELETE FROM source WHERE id=%s", (source_id,))
        connection.commit()


def test_vector_literal_requires_exact_finite_1024_dimensions():
    with pytest.raises(ValueError, match="1024"):
        _vector_literal([0.0])
    values = [0.0] * 1024
    values[0] = 1.0
    literal = _vector_literal(values)
    assert literal.startswith("[1,")
    assert literal.endswith("]")


def test_retriever_rejects_incomplete_scope_before_database_call():
    retriever = PostgresRetriever("postgresql://unused", connect=lambda *_args, **_kwargs: None)
    with pytest.raises(PermissionError, match="scope"):
        retriever.search(query="question", query_vector=[0.0] * 1024, scope={})


@pytest.mark.skipif(not os.environ.get("V2_INTEGRATION_ENV"), reason="isolated V2 configuration is not supplied")
@pytest.mark.parametrize("query,expected_content", [
    ("persistent bottle", "V2 persistent bottle"),
    ("配送需要几个工作日？", "标准配送通常需要 2-5 个工作日"),
    ("zzzzqqqq", None),
])
def test_postgres_retriever_reads_active_reviewed_chunks_and_filters_scope(query, expected_content):
    """Exercise independent channels in the dedicated DB, then remove fixture rows."""
    import psycopg

    sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "scripts" / "dev"))
    from v2_services import load_values

    values = load_values(Path(os.environ["V2_INTEGRATION_ENV"]))
    dsn = values["V2_INGEST_DSN"]
    for selected in (dsn, values["V2_AI_DSN"]):
        with psycopg.connect(selected) as conn:
            database, superuser, bypass = conn.execute("SELECT current_database(),rolsuper,rolbypassrls FROM pg_roles WHERE rolname=current_user").fetchone()
            assert database.startswith("ecr_v2_test_") and not superuser and not bypass

    key = "v2-retriever-test-" + uuid.uuid4().hex[:12]
    source_hash = "a" * 64
    vector = "[1," + ",".join("0" for _ in range(1023)) + "]"
    opposite_vector = "[-1," + ",".join("0" for _ in range(1023)) + "]"
    tenant_id: int
    document_id: int
    version_id: int
    source_id: int
    with psycopg.connect(dsn) as connection, connection.cursor() as cursor:
        cursor.execute(
            "SELECT t.id, u.id FROM tenant t JOIN app_user u ON u.tenant_id=t.id "
            "WHERE t.name=%s AND u.external_id=%s",
            ("V2 LangGraph fixture tenant", "dev-user"),
        )
        tenant_id, user_id = cursor.fetchone()
        cursor.execute(
            "INSERT INTO source(tenant_id,shop_id,type,uri) VALUES(%s,%s,%s,%s) RETURNING id",
            (tenant_id, "shop-demo", "v2_retrieval_test", "fixture://" + key),
        )
        source_id = cursor.fetchone()[0]
        cursor.execute(
            "INSERT INTO document(tenant_id,source_id,logical_key,title,doc_type,status,meta_json) "
            "VALUES(%s,%s,%s,%s,%s,'pending',%s::jsonb) RETURNING id",
            (tenant_id, source_id, key, "Retriever test", "markdown", '{"shop_id":"shop-demo"}'),
        )
        document_id = cursor.fetchone()[0]
        cursor.execute(
            "INSERT INTO document_version(tenant_id,document_id,source_hash,parser_version,"
            "chunk_rule_version,embedding_model,pipeline_version,object_key,status,chunk_count,"
            "disclosure_class,external_allowed,review_status,reviewed_hash,reviewed_by,reviewed_at) "
            "VALUES(%s,%s,%s,'test-parser','structured-v2','test-embedding','test-pipeline',"
            "concat('db://test/',%s::text),'active',3,'external_allowed',true,'approved',%s,%s,now()) RETURNING id",
            (tenant_id, document_id, source_hash, key, source_hash, user_id),
        )
        version_id = cursor.fetchone()[0]
        cursor.execute(
            "UPDATE document SET active_version_id=%s,status='active' WHERE tenant_id=%s AND id=%s",
            (version_id, tenant_id, document_id),
        )
        for index, content in enumerate(("V2 persistent bottle", "晴野收纳箱颜色和尺寸", "标准配送通常需要 2-5 个工作日")):
            cursor.execute(
                "INSERT INTO chunk(version_id,doc_id,tenant_id,shop_id,chunk_index,section_seq,"
                "section_chunk_index,char_start,char_end,heading_path,content,embed_text,token_count,"
                "content_type,split_reason,embedding,embedding_model,embedding_dim,tokenizer_id,"
                "meta_json,chunk_hash) VALUES(%s,%s,%s,%s,%s,1,%s,0,%s,%s,%s,%s,4,'text','test',"
                "%s::halfvec(1024),'test',1024,'test-tokenizer','{}'::jsonb,%s)",
                (version_id, document_id, tenant_id, "shop-demo", index, index, len(content), "V2",
                 content, content, opposite_vector if index == 2 else vector, str(index + 1) * 64),
            )
        connection.commit()
    try:
        retriever = PostgresRetriever(
            values["V2_AI_DSN"], dense_candidates=2, sparse_candidates=2,
            sparse_retrieval="legacy", sparse_rrf_weight=0.7,
        )
        retriever.verify_schema()
        result = retriever.search(
            query=query,
            query_vector=[1.0] + [0.0] * 1023,
            scope={
                "tenant_id": "v2-demo-tenant",
                "tenant_db_id": str(tenant_id),
                "allowed_shop_ids": ["shop-demo"],
                "allowed_document_ids": [key],
                "shop_id": "shop-demo",
                "role": "operator",
            },
            response_purpose="customer_reply",
            business_date="2026-10-09",
        )
        assert result
        assert result[0]["document_id"] == key
        assert result[0]["disclosure_class"] == "external_allowed"
        assert result[0]["customer_eligible"] is True
        assert result[0]["rrf_score"] > 0
        if expected_content:
            matching = next(item for item in result if item["content"] == expected_content)
            assert matching["sparse_rank"] == 1
            assert result[0]["content"] == expected_content
            # dense_candidates is multiplied by dense_overfetch and may retry to
            # 256 rows when the authorized set is smaller than limit. The sparse
            # channel's recall and RRF score must stay consistent with the actual
            # dense rank instead of assuming the chunk was absent from dense top-2.
            assert matching["rrf_score"] == pytest.approx(
                1 / (60 + matching["dense_rank"]) + 0.7 / 61
            )
        else:
            assert all(item["sparse_rank"] == 0 for item in result)
            assert result[0]["rrf_score"] == pytest.approx(1 / 61)
            assert result[0]["raw_score"] == pytest.approx(1)
        # A sparse-recalled result may outrank the dense top-1 and carry a low or
        # negative dense similarity; raw_score stays clamped to the contract.
        assert all(-1.0 <= item["raw_score"] <= 1.0 for item in result)
        denied_scope = {
            "tenant_id": "v2-demo-tenant", "tenant_db_id": str(tenant_id),
            "allowed_shop_ids": ["shop-demo"], "allowed_document_ids": [key + "-denied"],
            "shop_id": "shop-demo", "role": "operator",
        }
        assert retriever.search(query=query, query_vector=[1.0] + [0.0] * 1023,
                                scope=denied_scope, response_purpose="customer_reply") == []
    finally:
        with psycopg.connect(dsn) as cleanup, cleanup.cursor() as cursor:
            cursor.execute("DELETE FROM chunk WHERE doc_id=%s", (document_id,))
            cursor.execute("UPDATE document SET active_version_id=NULL WHERE id=%s", (document_id,))
            cursor.execute("DELETE FROM document_version WHERE id=%s", (version_id,))
            cursor.execute("DELETE FROM document WHERE id=%s", (document_id,))
            cursor.execute("DELETE FROM source WHERE id=%s", (source_id,))
            cleanup.commit()


@pytest.mark.parametrize('customer', [False, True])
def test_trace_contains_only_ranked_ids_and_scores_not_source_content(customer):
    import asyncio
    import json
    calls = []
    class Connection:
        def __enter__(self): return self
        def __exit__(self, *_): pass
        def execute(self, query, params): calls.append((query, params))
    retriever = PostgresRetriever('test connection', connect=lambda _: Connection())
    item = {'id': 'chunk', 'document_id': 'doc', 'version_id': 'version', 'content': 'restricted body',
            'source_ref': 'restricted origin', 'raw_score': .2, 'rrf_score': .03, 'rerank_score': .8}
    scope = {'tenant_db_id': '1', 'request_id': 'request'}
    if customer:
        scope.update(subject_type='customer', execution_id='request')
    asyncio.run(retriever.arecord_trace(scope, [item], [item]))
    assert len(calls) == 2
    assert 'restricted' not in str(calls)
    assert json.loads(calls[1][1][-1])[0]['rerank_score'] == .8
    assert ('customer_retrieval_trace' in calls[0][0]) is customer


@pytest.mark.parametrize('execution', ['', 'different'])
def test_customer_trace_rejects_missing_or_mismatched_execution(execution):
    import asyncio
    retriever = PostgresRetriever('unused', connect=lambda _: pytest.fail('must not connect'))
    with pytest.raises(ValueError, match='execution'):
        asyncio.run(retriever.arecord_trace({'tenant_db_id': '1', 'request_id': 'request',
                                            'subject_type': 'customer', 'execution_id': execution}, [], []))


@pytest.mark.skipif(not os.environ.get("V2_INTEGRATION_ENV"), reason="isolated V2 configuration is not supplied")
def test_sparse_probe_runs_against_the_real_database(caplog, monkeypatch):
    """Exercise 0029's three modes with explicit retrievers and inputs.

    The mixed fixture commits a sparse chunk and a dense-only chunk before the
    first search, matching the ingest-then-probe order from WP51. Each mode gets
    its own retriever, so the result cannot depend on an earlier global probe.
    """
    import logging

    from app.retrieval import postgres

    sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "scripts" / "dev"))
    from v2_services import load_values

    values = load_values(Path(os.environ["V2_INTEGRATION_ENV"]))
    dsn = values["V2_INGEST_DSN"]
    key = "v2-sparse-probe-" + uuid.uuid4().hex[:12]
    tokenizer = "wp52-retriever:sparse"
    fixture = None
    try:
        fixture = _create_mixed_retrieval_fixture(dsn, key, tokenizer)
        tenant_id = fixture[0]
        scope = {
            "tenant_id": "v2-demo-tenant", "tenant_db_id": str(tenant_id),
            "allowed_shop_ids": ["shop-demo"], "allowed_document_ids": [key],
            "shop_id": "shop-demo", "role": "operator", "request_id": "sparse-probe-test",
        }

        def run(mode: str, *, with_sparse: bool):
            monkeypatch.setattr(postgres, "_sparse_mode_reported", False)
            caplog.clear()
            retriever = PostgresRetriever(
                values["V2_AI_DSN"], sparse_retrieval=mode, sparse_rrf_weight=0.7,
            )
            kwargs = {
                "query": "wp52 sparse policy",
                "query_vector": [1.0] + [0.0] * 1023,
                "scope": scope,
                "response_purpose": "customer_reply",
                "limit": 3,
            }
            if with_sparse:
                kwargs["query_sparse"] = [SparseWeight(token_id=0, weight=1.0)]
                kwargs["query_sparse_tokenizer"] = tokenizer
            with caplog.at_level(logging.WARNING, logger="app.retrieval.postgres"):
                results = retriever.search(**kwargs)
            warnings = [
                record.getMessage() for record in caplog.records
                if record.levelno == logging.WARNING and record.name == "app.retrieval.postgres"
            ]
            return retriever, results, warnings

        retriever, results, warnings = run("legacy", with_sparse=True)
        assert retriever.last_sparse_mode == "text_heuristic"
        assert any("text heuristic" in warning for warning in warnings)
        sparse_hit = next(item for item in results if item["content"] == "wp52 sparse policy")
        assert sparse_hit["sparse_rank"] == 1

        retriever, results, warnings = run("sparsevec", with_sparse=True)
        assert retriever.last_sparse_mode == "sparsevec"
        assert warnings == []
        sparse_hit = next(item for item in results if item["content"] == "wp52 sparse policy")
        dense_hit = next(item for item in results if item["content"] == "wp52 dense policy")
        assert sparse_hit["sparse_rank"] == 1
        assert dense_hit["sparse_rank"] == 0
        assert retriever.last_sparse_skipped_tokenizer == 0

        retriever, results, warnings = run("sparsevec", with_sparse=False)
        assert retriever.last_sparse_mode == "dense_only"
        assert any("produced no sparse weights" in warning for warning in warnings)
        assert all(item["sparse_rank"] == 0 for item in results)
    finally:
        if fixture is not None:
            _delete_mixed_retrieval_fixture(dsn, fixture)


@pytest.mark.skipif(not os.environ.get("V2_INTEGRATION_ENV"), reason="isolated V2 configuration is not supplied")
def test_sparse_probe_sees_mixed_rows_written_before_first_search():
    """WP51 regression: ingest commits sparse rows before the first probe."""
    sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "scripts" / "dev"))
    from v2_services import load_values

    values = load_values(Path(os.environ["V2_INTEGRATION_ENV"]))
    dsn = values["V2_INGEST_DSN"]
    key = "v2-sparse-mixed-" + uuid.uuid4().hex[:12]
    tokenizer = "wp52-mixed:sparse"
    fixture = None
    try:
        fixture = _create_mixed_retrieval_fixture(dsn, key, tokenizer)
        retriever = PostgresRetriever(
            values["V2_AI_DSN"], sparse_retrieval="sparsevec", sparse_rrf_weight=0.7,
        )
        results = retriever.search(
            query="wp52 sparse policy",
            query_vector=[1.0] + [0.0] * 1023,
            scope={
                "tenant_id": "v2-demo-tenant", "tenant_db_id": str(fixture[0]),
                "allowed_shop_ids": ["shop-demo"], "allowed_document_ids": [key],
                "shop_id": "shop-demo", "role": "operator",
            },
            response_purpose="customer_reply",
            limit=3,
            query_sparse=[SparseWeight(token_id=0, weight=1.0)],
            query_sparse_tokenizer=tokenizer,
        )
        assert retriever.last_sparse_mode == "sparsevec"
        sparse_hit = next(item for item in results if item["content"] == "wp52 sparse policy")
        dense_hit = next(item for item in results if item["content"] == "wp52 dense policy")
        assert sparse_hit["sparse_rank"] == 1
        assert dense_hit["sparse_rank"] == 0
    finally:
        if fixture is not None:
            _delete_mixed_retrieval_fixture(dsn, fixture)


def test_catalog_filter_is_normalized_and_parameterized():
    normalized = PostgresRetriever._catalog_filter({
        "catalog_filter": {
            "sku_codes": ["SKU-1"], "colors": ["Blue"], "size_labels": ["M"],
            "size_chart_codes": ["C1"], "attributes": {"capacity_ml": "480"},
        },
    })
    assert normalized == {
        "sku_codes": ["sku-1"], "colors": ["blue"], "size_labels": ["m"],
        "size_chart_codes": ["c1"], "attributes": {"capacity_ml": "480"},
    }
    clause, params = PostgresRetriever._catalog_clause(normalized)
    assert "cs.chunk_index = c.chunk_index" in clause
    assert "lower(cs.sku_code) = ANY(%s::text[])" in clause
    assert "cs.attributes ->> %s = %s" in clause
    assert params == [["sku-1"], ["blue"], ["m"], ["c1"], "capacity_ml", "480"]


def test_catalog_filter_rejects_unknown_keys_and_malformed_values():
    with pytest.raises(PermissionError, match="unknown field"):
        PostgresRetriever._catalog_filter({"catalog_filter": {"free_text": "x"}})
    with pytest.raises(PermissionError, match="attribute key"):
        PostgresRetriever._catalog_filter({"catalog_filter": {"attributes": {"stock": "1"}}})
    with pytest.raises(PermissionError, match="value"):
        PostgresRetriever._catalog_filter({"catalog_filter": {"sku_codes": [1]}})


def test_retriever_rejects_invalid_catalog_filter_before_database_call():
    retriever = PostgresRetriever("postgresql://unused", connect=lambda *_args, **_kwargs: None)
    scope = {
        "tenant_id": "tenant-a", "tenant_db_id": "1", "allowed_shop_ids": ["shop-a"],
        "allowed_document_ids": ["doc"], "shop_id": "shop-a", "role": "operator",
        "catalog_filter": {"attributes": {"unknown": "x"}},
    }
    with pytest.raises(PermissionError, match="attribute key"):
        retriever.search(query="SKU-1", query_vector=[0.0] * 1024, scope=scope)


def test_dense_rows_sort_by_distance_then_chunk_id():
    rows = [
        {"chunk_id": 9, "distance": 0.5},
        {"chunk_id": 2, "distance": 0.5},
        {"chunk_id": 7, "distance": 0.1},
    ]
    ordered = PostgresRetriever._sort_dense_rows(rows)
    assert [row["chunk_id"] for row in ordered] == [7, 2, 9]
