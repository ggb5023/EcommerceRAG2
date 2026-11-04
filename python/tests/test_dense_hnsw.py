from __future__ import annotations

import json
import os
import statistics
import sys
import time
import uuid
from pathlib import Path

import pytest

pytestmark = pytest.mark.skipif(
    not os.environ.get("V2_INTEGRATION_ENV"), reason="isolated V2 configuration is not supplied"
)

VECTOR = "[1," + ",".join("0" for _ in range(1023)) + "]"


def _load_retriever_env():
    sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "scripts" / "dev"))
    from v2_services import load_values

    values = load_values(Path(os.environ["V2_INTEGRATION_ENV"]))
    return values["V2_GATEWAY_DSN"], values["V2_MIGRATION_DSN"]


def _insert_test_chunks(ingest_dsn: str):
    import psycopg

    key = "hnsw-" + uuid.uuid4().hex[:16]
    with psycopg.connect(ingest_dsn) as connection, connection.cursor() as cursor:
        tenant_id = cursor.execute("INSERT INTO tenant(name) VALUES(%s) RETURNING id", ("HNSW test " + key,)).fetchone()[0]
        shop = key
        cursor.execute("INSERT INTO shop(tenant_id,id,name,status) VALUES(%s,%s,%s,'active')", (tenant_id, shop, shop))
        source_id = cursor.execute(
            "INSERT INTO source(tenant_id,shop_id,type,uri) VALUES(%s,%s,'hnsw-test',%s) RETURNING id",
            (tenant_id, shop, "fixture://" + key),
        ).fetchone()[0]
        document_id = cursor.execute(
            "INSERT INTO document(tenant_id,source_id,logical_key,title,doc_type,status,meta_json) "
            "VALUES(%s,%s,%s,'HNSW test','markdown','pending','{}'::jsonb) RETURNING id",
            (tenant_id, source_id, key),
        ).fetchone()[0]
        user_id = cursor.execute(
            "INSERT INTO app_user(tenant_id,external_id,role,shop_ids) VALUES(%s,%s,'operator',ARRAY[%s]) RETURNING id",
            (tenant_id, "hnsw-user-" + key, shop),
        ).fetchone()[0]
        version_id = cursor.execute(
            "INSERT INTO document_version(tenant_id,document_id,source_hash,parser_version,chunk_rule_version,"
            "embedding_model,pipeline_version,object_key,status,chunk_count,disclosure_class,external_allowed,"
            "review_status,reviewed_hash,reviewed_by,reviewed_at) "
            "VALUES(%s,%s,%s,'test','test','test','test',%s,'active',50000,'external_allowed',true,"
            "'approved',%s,%s,now()) RETURNING id",
            (tenant_id, document_id, "a" * 64, "db://" + key, "a" * 64, user_id),
        ).fetchone()[0]
        cursor.execute("UPDATE document SET active_version_id=%s,status='active' WHERE tenant_id=%s AND id=%s",
                       (version_id, tenant_id, document_id))
        rows = []
        for index in range(50000):
            text = f"hnsw deterministic chunk {index}"
            rows.append((version_id, document_id, tenant_id, shop, index, index, 0, len(text), "%s",
                         text, text, 4, "text", "test", VECTOR, "test", 1024, "test", "{}", f"{index:064x}"))
        cursor.executemany(
            "INSERT INTO chunk(version_id,doc_id,tenant_id,shop_id,chunk_index,section_seq,"
            "section_chunk_index,char_start,char_end,heading_path,content,embed_text,token_count,"
            "content_type,split_reason,embedding,embedding_model,embedding_dim,tokenizer_id,"
            "meta_json,chunk_hash) VALUES(%s,%s,%s,%s,%s,1,%s,%s,%s,%s,%s,%s,%s,%s,%s,"
            "%s::halfvec(1024),%s,%s,%s,%s::jsonb,%s)",
            rows,
        )
        connection.commit()
        cursor.execute("ANALYZE chunk")
        connection.commit()
    return key, tenant_id, shop


def _cleanup(ingest_dsn: str, tenant_id: int) -> None:
    import psycopg

    with psycopg.connect(ingest_dsn) as connection, connection.cursor() as cursor:
        cursor.execute("DELETE FROM chunk WHERE tenant_id=%s", (tenant_id,))
        cursor.execute("DELETE FROM document_version WHERE tenant_id=%s", (tenant_id,))
        cursor.execute("DELETE FROM document WHERE tenant_id=%s", (tenant_id,))
        cursor.execute("DELETE FROM source WHERE tenant_id=%s", (tenant_id,))
        cursor.execute("DELETE FROM app_user WHERE tenant_id=%s", (tenant_id,))
        cursor.execute("DELETE FROM shop WHERE tenant_id=%s", (tenant_id,))
        cursor.execute("DELETE FROM permission_revision WHERE tenant_id=%s", (tenant_id,))
        cursor.execute("DELETE FROM tenant WHERE id=%s", (tenant_id,))
        connection.commit()


def test_dense_query_uses_hnsw_and_is_deterministic(capsys):
    from app.retrieval.postgres import PostgresRetriever

    ai_dsn, ingest_dsn = _load_retriever_env()
    key = tenant_id = shop = None
    try:
        key, tenant_id, shop = _insert_test_chunks(ingest_dsn)
        retriever = PostgresRetriever(ai_dsn, dense_candidates=8, sparse_retrieval="sparsevec")
        retriever.verify_schema()
        scope = {
            "tenant_id": key, "tenant_db_id": str(tenant_id), "allowed_shop_ids": [shop],
            "allowed_document_ids": [key], "shop_id": shop, "role": "operator",
        }
        original = retriever._execute_search
        captured = {}

        def capture(**kwargs):
            captured.update(kwargs)
            return original(**kwargs)

        retriever._execute_search = capture
        first = retriever.search(query="deterministic", query_vector=[1.0] + [0.0] * 1023,
                                 scope=scope, response_purpose="customer_reply", business_date="2026-10-09")
        second = retriever.search(query="deterministic", query_vector=[1.0] + [0.0] * 1023,
                                  scope=scope, response_purpose="customer_reply", business_date="2026-10-09")
        assert [row["id"] for row in first] == [row["id"] for row in second]
        assert len(first) == 8

        import psycopg

        dense_sql = captured["dense_sql"]
        dense_params = captured["dense_params"]
        with psycopg.connect(ai_dsn) as connection, connection.cursor() as cursor:
            cursor.execute("EXPLAIN (FORMAT JSON) " + dense_sql, dense_params)
            natural = json.dumps(cursor.fetchone()[0])
            if "idx_chunk_embedding" not in natural or '"Node Type": "Index Scan"' not in natural:
                cursor.execute("SET LOCAL enable_seqscan=off")
                cursor.execute("EXPLAIN (FORMAT JSON) " + dense_sql, dense_params)
                diagnostic = json.dumps(cursor.fetchone()[0])
                print("NATURAL PLAN MISSING HNSW; diagnostic enable_seqscan=off plan follows", file=sys.stderr)
                print(diagnostic[:2000], file=sys.stderr)
                pytest.fail("production dense plan did not naturally select idx_chunk_embedding")
            print("EXPLAIN_JSON", natural[:3000])

            # Measure the same dense statement 20 times with and without the
            # old tie-breaker. This is test-data scale, not a production claim.
            old_sql = dense_sql.replace(" LIMIT %s", ", c.id LIMIT %s")
            timings = {"old": [], "new": []}
            for label, statement in (("old", old_sql), ("new", dense_sql)):
                for _ in range(20):
                    started = time.perf_counter()
                    cursor.execute(statement, dense_params)
                    cursor.fetchall()
                    timings[label].append((time.perf_counter() - started) * 1000)
            print("DENSE_P50_MS_OLD", round(statistics.median(timings["old"]), 3))
            print("DENSE_P50_MS_NEW", round(statistics.median(timings["new"]), 3))
    finally:
        if tenant_id is not None:
            _cleanup(ingest_dsn, tenant_id)


def test_cleanup_removed_temporary_tenant():
    if not os.environ.get("V2_INTEGRATION_ENV"):
        pytest.skip("isolated V2 configuration is not supplied")
    import psycopg
    _, ingest_dsn = _load_retriever_env()
    with psycopg.connect(ingest_dsn) as connection:
        count = connection.execute("SELECT count(*) FROM tenant WHERE name LIKE 'HNSW test %'").fetchone()[0]
    assert count == 0


def _insert_visibility_fixture(ingest_dsn: str):
    import psycopg

    key = "hnsw-vis-" + uuid.uuid4().hex[:16]
    close = "[1," + ",".join("0" for _ in range(1023)) + "]"
    far = "[0,1," + ",".join("0" for _ in range(1022)) + "]"
    with psycopg.connect(ingest_dsn) as connection, connection.cursor() as cursor:
        tenant_id = cursor.execute("INSERT INTO tenant(name) VALUES(%s) RETURNING id", ("HNSW test " + key,)).fetchone()[0]
        shop = key
        cursor.execute("INSERT INTO shop(tenant_id,id,name,status) VALUES(%s,%s,%s,'active')", (tenant_id, shop, shop))
        source_id = cursor.execute(
            "INSERT INTO source(tenant_id,shop_id,type,uri) VALUES(%s,%s,'hnsw-test',%s) RETURNING id",
            (tenant_id, shop, "fixture://" + key),
        ).fetchone()[0]
        document_id = cursor.execute(
            "INSERT INTO document(tenant_id,source_id,logical_key,title,doc_type,status,meta_json) "
            "VALUES(%s,%s,%s,'HNSW visibility','markdown','pending','{}'::jsonb) RETURNING id",
            (tenant_id, source_id, key),
        ).fetchone()[0]
        user_id = cursor.execute(
            "INSERT INTO app_user(tenant_id,external_id,role,shop_ids) VALUES(%s,%s,'operator',ARRAY[%s]) RETURNING id",
            (tenant_id, "hnsw-vis-user-" + key, shop),
        ).fetchone()[0]
        version_id = cursor.execute(
            "INSERT INTO document_version(tenant_id,document_id,source_hash,parser_version,chunk_rule_version,"
            "embedding_model,pipeline_version,object_key,status,chunk_count,disclosure_class,external_allowed,"
            "review_status,reviewed_hash,reviewed_by,reviewed_at) "
            "VALUES(%s,%s,%s,'test','test','test','test',%s,'active',34,'external_allowed',true,"
            "'approved',%s,%s,now()) RETURNING id",
            (tenant_id, document_id, "b" * 64, "db://" + key, "b" * 64, user_id),
        ).fetchone()[0]
        cursor.execute("UPDATE document SET active_version_id=%s,status='active' WHERE tenant_id=%s AND id=%s",
                       (version_id, tenant_id, document_id))
        active_ids = []
        for index in range(34):
            expired = index < 32
            text = f"visibility chunk {index}"
            vector = close if expired else far
            chunk_id = cursor.execute(
                "INSERT INTO chunk(version_id,doc_id,tenant_id,shop_id,chunk_index,section_seq,"
                "section_chunk_index,char_start,char_end,heading_path,content,embed_text,token_count,"
                "content_type,split_reason,embedding,embedding_model,embedding_dim,tokenizer_id,"
                "effective_to,meta_json,chunk_hash) VALUES(%s,%s,%s,%s,%s,1,%s,0,%s,%s,%s,%s,4,'text','test',"
                "%s::halfvec(1024),'test',1024,'test',%s,'{}'::jsonb,%s) RETURNING id",
                (version_id, document_id, tenant_id, shop, index, index, len(text), "HNSW", text, text,
                 vector, "2020-01-01" if expired else None, f"{index:064x}"),
            ).fetchone()[0]
            if not expired:
                active_ids.append(f"db-chunk-{chunk_id}")
        connection.commit()
    return key, tenant_id, shop, active_ids


def test_overfetch_recovers_visible_chunks_after_expired_top_n():
    from app.retrieval.postgres import PostgresRetriever

    ai_dsn, ingest_dsn = _load_retriever_env()
    key = tenant_id = shop = None
    try:
        key, tenant_id, shop, active_ids = _insert_visibility_fixture(ingest_dsn)
        retriever = PostgresRetriever(ai_dsn, dense_candidates=4, dense_overfetch=2,
                                      sparse_retrieval="sparsevec")
        scope = {
            "tenant_id": key, "tenant_db_id": str(tenant_id), "allowed_shop_ids": [shop],
            "allowed_document_ids": [key], "shop_id": shop, "role": "operator",
        }
        first = retriever.search(query="visibility", query_vector=[1.0] + [0.0] * 1023,
                                 scope=scope, response_purpose="customer_reply", business_date="2026-10-09")
        second = retriever.search(query="visibility", query_vector=[1.0] + [0.0] * 1023,
                                  scope=scope, response_purpose="customer_reply", business_date="2026-10-09")
        assert [row["id"] for row in first] == active_ids
        assert [row["id"] for row in first] == [row["id"] for row in second]
        assert retriever.last_dense_fetch_limit == 256
    finally:
        if tenant_id is not None:
            _cleanup(ingest_dsn, tenant_id)
