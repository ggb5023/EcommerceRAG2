from __future__ import annotations

import os
import sys
import uuid
from pathlib import Path

import pytest
from app.retrieval.postgres import PostgresRetriever, _vector_literal


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

    sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "scripts"))
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
        retriever = PostgresRetriever(values["V2_AI_DSN"], dense_candidates=2, sparse_candidates=2)
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
            if "配送" in query:
                # This chunk is absent from dense top-2 and must be recalled by
                # the Chinese lexical channel, even under database locale C.
                assert matching["dense_rank"] == 0
                assert matching["rrf_score"] == pytest.approx(0.7 / 61)
        else:
            assert all(item["sparse_rank"] == 0 for item in result)
            assert result[0]["rrf_score"] == pytest.approx(1 / 61)
        assert result[0]["raw_score"] == pytest.approx(1)
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
