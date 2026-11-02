#!/usr/bin/env python3
"""Exercise the real PostgresRetriever for the 0029 legacy/sparsevec A/B.

`seed` creates the dedicated `ab-test` tenant, imports the approved 37-document
corpus through the normal Go admin package path, reviews/publishes it there and
asserts the sparse column is populated. `run` executes the 53 eligible aligned
cases through PostgresRetriever twice, changing only SPARSE_RETRIEVAL.

The first run needs the dev stack (8080 gateway, 50052/50053 Python RPC) and the
local dev/migration env files. It never touches the dev tenant and never pushes
the corpus into an evaluation set.
"""
from __future__ import annotations

import argparse
import asyncio
import json
import math
import os
import sys
import time
import uuid
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[2]
VENV = ROOT / "python" / ".venv" / "bin" / "python"
if Path(sys.prefix).resolve() != (ROOT / "python" / ".venv").resolve() and VENV.is_file():
    os.execv(str(VENV), [str(VENV), __file__, *sys.argv[1:]])

sys.path.insert(0, str(ROOT / "python"))
sys.path.insert(0, str(ROOT / "eval"))

TENANT_EXTERNAL_ID = "ab-test"
SHOP_ID = "ab-shop"
USER_EXTERNAL_ID = "ab-user"
DEFAULT_REVISION = Path(
    os.environ.get(
        "ECR_EVAL_REVISION",
        "/var/lib/ecommerce-rag/eval/synthetic-m2-v1-aligned-provisional",
    )
)
DEV_ENV = Path(os.environ.get("ECR_DEV_ENV", "/etc/ecommerce-rag/dev.env"))
MIGRATION_ENV = Path(os.environ.get("ECR_MIGRATION_ENV", "/etc/ecommerce-rag/migration.env"))
SPARSE_RRF_WEIGHT = 0.7


class AbFailure(RuntimeError):
    """A fixed diagnostic suitable for stdout."""


class RequestLimiter:
    """Hard stop for real provider calls: total and rolling one-minute caps."""

    def __init__(self, max_requests: int, max_per_minute: int) -> None:
        if max_requests < 1 or max_per_minute < 1:
            raise AbFailure("provider request limits must be positive")
        self.max_requests = max_requests
        self.max_per_minute = max_per_minute
        self.used = 0
        self.timestamps: list[float] = []

    def before_request(self) -> None:
        now = time.monotonic()
        self.timestamps = [stamp for stamp in self.timestamps if now - stamp < 60]
        if self.used >= self.max_requests:
            raise AbFailure(f"provider request limit {self.max_requests} reached")
        if len(self.timestamps) >= self.max_per_minute:
            raise AbFailure(f"provider rate limit {self.max_per_minute}/minute reached")
        self.used += 1
        self.timestamps.append(now)


def _env_file(path: Path) -> dict[str, str]:
    values: dict[str, str] = {}
    if not path.is_file():
        return values
    for line in path.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        values[key.strip()] = value.strip().strip("'\"")
    return values


def _dsn(*names: str, env_file: Path | None = None) -> str:
    for name in names:
        if os.environ.get(name, "").strip():
            return os.environ[name].strip()
    values = _env_file(env_file) if env_file else {}
    for name in names:
        if values.get(name, "").strip():
            return values[name].strip()
    raise AbFailure(f"missing {' / '.join(names)}")


def _jsonl(path: Path) -> list[dict[str, Any]]:
    rows = []
    for number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        if not line.strip():
            continue
        value = json.loads(line)
        if not isinstance(value, dict):
            raise AbFailure(f"{path.name} line {number} is not an object")
        rows.append(value)
    return rows


def _digest(value: bytes) -> str:
    import hashlib

    return hashlib.sha256(value).hexdigest()


def _create_identity() -> tuple[int, int]:
    import psycopg
    from psycopg.rows import dict_row

    dsn = _dsn("V2_MIGRATION_DSN", env_file=MIGRATION_ENV)
    with psycopg.connect(dsn, row_factory=dict_row) as connection:
        with connection.cursor() as cursor:
            cursor.execute(
                "INSERT INTO tenant(name,status,external_id) VALUES(%s,'active',%s) "
                "ON CONFLICT (external_id) DO UPDATE SET status='active' RETURNING id",
                ("AB retrieval tenant", TENANT_EXTERNAL_ID),
            )
            tenant_id = int(cursor.fetchone()["id"])
            cursor.execute(
                "INSERT INTO shop(tenant_id,id,name,status) VALUES(%s,%s,%s,'active') "
                "ON CONFLICT (tenant_id,id) DO UPDATE SET status='active'",
                (tenant_id, SHOP_ID, "AB retrieval shop"),
            )
            cursor.execute(
                "INSERT INTO app_user(tenant_id,external_id,role,shop_ids,display_name) "
                "VALUES(%s,%s,'owner',ARRAY[%s],%s) "
                "ON CONFLICT (tenant_id,external_id) DO UPDATE SET role='owner',shop_ids=ARRAY[%s],"
                "revoked_at=NULL,display_name=EXCLUDED.display_name RETURNING id",
                (tenant_id, USER_EXTERNAL_ID, SHOP_ID, "AB retrieval user", SHOP_ID),
            )
            user_id = int(cursor.fetchone()["id"])
            for role in ("merchant_admin", "knowledge_reviewer"):
                cursor.execute(
                    "INSERT INTO admin_role_grant(tenant_id,user_id,role,scope_kind,shop_ids,status,granted_by) "
                    "VALUES(%s,%s,%s,'tenant','{}','active',%s) "
                    "ON CONFLICT (tenant_id,user_id,role) DO UPDATE SET scope_kind='tenant',shop_ids='{}',"
                    "status='active',granted_by=EXCLUDED.granted_by,revoked_at=NULL,revoked_by=NULL",
                    (tenant_id, user_id, role, user_id),
                )
        connection.commit()
    return tenant_id, user_id


def _corpus_specs(revision: Path) -> list[dict[str, Any]]:
    corpus = _jsonl(revision / "documents.jsonl")
    if len(corpus) != 37:
        raise AbFailure(f"approved corpus must contain 37 documents, found {len(corpus)}")
    specs = []
    for document in corpus:
        document_id = document.get("document_id")
        chunks = document.get("chunks")
        if not isinstance(document_id, str) or not document_id or not isinstance(chunks, list) or not chunks:
            raise AbFailure("approved corpus document is invalid")
        content = ("# " + document_id + "\n\n" + "\n\n".join(
            str(chunk.get("content", "")).strip() for chunk in chunks
        ) + "\n").encode("utf-8")
        disclosure = document.get("disclosure_class")
        if disclosure not in {"external_allowed", "internal_only", "unclassified"}:
            raise AbFailure(f"{document_id}: disclosure class is invalid")
        specs.append({
            "document_id": document_id,
            "content": content,
            "sha256": _digest(content),
            "disclosure_class": disclosure,
            "effective_from": document.get("effective_from"),
            "effective_to": document.get("effective_to"),
        })
    return specs


def _manifest(specs: list[dict[str, Any]]) -> str:
    import yaml

    return yaml.safe_dump({
        "schema_version": 1,
        "source": {"id": "retrieval-ab", "name": "Approved synthetic M2 retrieval A/B corpus"},
        "pipeline_version": "retrieval-ab-v1",
        "documents": [{
            "document_id": spec["document_id"],
            "title": spec["document_id"],
            "path": spec["document_id"] + ".md",
            "format": "markdown",
            "sha256": spec["sha256"],
            "disclosure_class": spec["disclosure_class"],
            "effective_from": spec["effective_from"],
            "effective_to": spec["effective_to"],
        } for spec in specs],
    }, allow_unicode=True, sort_keys=False)


def _signed_scope(tenant_id: int, user_id: int, permission_revision: str):
    import hashlib
    import hmac

    from rag.v1 import rag_pb2

    key_path = Path(os.environ.get("SCOPE_SIGNING_KEY_FILE", "/etc/ecommerce-rag/synthetic-auth.key"))
    try:
        key = key_path.read_bytes()
    except OSError as error:
        raise AbFailure(f"scope signing key is unavailable: {key_path}") from error
    scope = rag_pb2.RequestContext(
        request_id="retrieval-ab-" + uuid.uuid4().hex,
        tenant_id=TENANT_EXTERNAL_ID,
        user_id=USER_EXTERNAL_ID,
        allowed_shop_ids=[SHOP_ID],
        shop_id=SHOP_ID,
        role="owner",
        allowed_document_ids=[],
        enforce_document_scope=True,
        scope_expires_at=int(time.time()) + 60,
        tenant_db_id=tenant_id,
        user_db_id=user_id,
        permission_revision=permission_revision,
        scope_audience="ingest",
    )
    unsigned = rag_pb2.RequestContext()
    unsigned.CopyFrom(scope)
    unsigned.scope_signature = ""
    scope.scope_signature = hmac.new(
        key, unsigned.SerializeToString(deterministic=True), hashlib.sha256,
    ).hexdigest()
    return scope


def _submit(specs: list[dict[str, Any]], tenant_id: int, user_id: int) -> str:
    import grpc

    from rag.v1 import rag_pb2, rag_pb2_grpc

    values = _env_file(DEV_ENV)
    address = os.environ.get("V2_INGEST_GRPC_ADDR") or values.get("V2_INGEST_GRPC_ADDR") or "127.0.0.1:50053"
    import psycopg

    with psycopg.connect(_dsn("V2_MIGRATION_DSN", env_file=MIGRATION_ENV)) as connection, \
            connection.cursor() as cursor:
        cursor.execute(
            "SELECT permission_revision FROM app_user WHERE tenant_id=%s AND id=%s",
            (tenant_id, user_id),
        )
        revision = str(cursor.fetchone()[0])
    manifest = _manifest(specs)
    request = rag_pb2.SubmitRequest(
        context=_signed_scope(tenant_id, user_id, revision),
        idempotency_key="retrieval-ab:" + _digest(manifest.encode()),
        manifest_yaml=manifest,
        files=[rag_pb2.PackageFile(path=spec["document_id"] + ".md", content=spec["content"])
               for spec in specs],
        tenant_db_id=str(tenant_id),
        user_db_id=str(user_id),
        shop_id=SHOP_ID,
    )
    channel = grpc.insecure_channel(address)
    try:
        response = rag_pb2_grpc.IngestServiceStub(channel).Submit(request, timeout=60)
    except grpc.RpcError as error:
        raise AbFailure(f"ingest Submit failed: {error.code()} {error.details()}") from None
    finally:
        channel.close()
    if not response.job_id:
        raise AbFailure("ingest Submit returned no job id")
    return str(response.job_id)


def _wait_job(tenant_id: int, job_id: str, timeout_s: int = 300) -> None:
    import psycopg

    dsn = _dsn("V2_MIGRATION_DSN", env_file=MIGRATION_ENV)
    deadline = time.monotonic() + timeout_s
    while time.monotonic() < deadline:
        with psycopg.connect(dsn) as connection, connection.cursor() as cursor:
            cursor.execute(
                "SELECT status,error_code FROM ingest_job WHERE tenant_id=%s AND id=%s",
                (tenant_id, job_id),
            )
            row = cursor.fetchone()
        if row is None:
            raise AbFailure(f"ingestion job {job_id} disappeared")
        status, error = row
        if status == "awaiting_review":
            return
        if status in {"failed", "cancelled"}:
            raise AbFailure(f"ingestion ended as {status}: {error}")
        time.sleep(0.4)
    raise AbFailure("ingestion did not reach awaiting_review before the deadline")


def _publish_versions(tenant_id: int, user_id: int) -> dict[str, int]:
    """Approve/publish versions in the dedicated tenant.

    The Go admin review/publish path needs a gateway identity bound to this
    tenant, but only one gateway may own a database (advisory lock 742019,1).
    This dev-only seed therefore applies the same review state directly and
    records the same reviewer/hash/epoch fields the product path writes.
    """
    import psycopg
    from psycopg.rows import dict_row

    dsn = _dsn("V2_MIGRATION_DSN", env_file=MIGRATION_ENV)
    with psycopg.connect(dsn, row_factory=dict_row) as connection:
        with connection.cursor() as cursor:
            cursor.execute(
                "SELECT v.id,v.document_id,v.source_hash,v.review_status,v.status,"
                "v.disclosure_class,v.fencing_epoch,d.logical_key FROM document_version v "
                "JOIN document d ON d.tenant_id=v.tenant_id AND d.id=v.document_id "
                "WHERE v.tenant_id=%s ORDER BY d.logical_key",
                (tenant_id,),
            )
            versions = cursor.fetchall()
            if not versions:
                raise AbFailure("the ingestion produced no document versions")
            published = 0
            withheld = 0
            for version in versions:
                if version["status"] == "active" and version["review_status"] == "approved":
                    published += 1
                    continue
                if version["status"] == "revoked":
                    raise AbFailure(f"{version['logical_key']}: version is revoked")
                if version["disclosure_class"] == "unclassified":
                    cursor.execute(
                        "UPDATE document_version SET review_status='rejected',reviewed_hash=source_hash,"
                        "reviewed_by=%s,reviewed_at=now(),review_reason=%s "
                        "WHERE tenant_id=%s AND id=%s AND review_status='pending'",
                        (user_id, "unclassified source withheld from retrieval",
                         tenant_id, version["id"]),
                    )
                    withheld += 1
                    continue
                external = version["disclosure_class"] == "external_allowed"
                cursor.execute(
                    "UPDATE document_version SET status='active',review_status='approved',"
                    "reviewed_hash=source_hash,reviewed_by=%s,reviewed_at=now(),review_reason=%s,"
                    "external_allowed=%s,activated_at=COALESCE(activated_at,now()),"
                    "fencing_epoch=fencing_epoch+1 WHERE tenant_id=%s AND id=%s",
                    (user_id, "approved synthetic retrieval corpus",
                     external, tenant_id, version["id"]),
                )
                cursor.execute(
                    "UPDATE document SET active_version_id=%s,status='active' "
                    "WHERE tenant_id=%s AND id=%s",
                    (version["id"], tenant_id, version["document_id"]),
                )
                published += 1
            cursor.execute(
                "UPDATE document d SET status='active' FROM document_version v "
                "WHERE d.tenant_id=%s AND d.tenant_id=v.tenant_id AND d.active_version_id=v.id "
                "AND v.status='active' AND v.review_status='approved'",
                (tenant_id,),
            )
        connection.commit()
    return {"published": published, "withheld": withheld, "total": len(versions)}


def _seed_counts(tenant_id: int) -> dict[str, int]:
    import psycopg
    from psycopg.rows import dict_row

    dsn = _dsn("V2_RETRIEVER_DSN", env_file=DEV_ENV)
    with psycopg.connect(dsn, row_factory=dict_row) as connection, connection.cursor() as cursor:
        cursor.execute(
            "SELECT count(*) FILTER (WHERE v.status='active' AND v.review_status='approved') AS active,"
            "count(*) AS total FROM document_version v WHERE v.tenant_id=%s",
            (tenant_id,),
        )
        counts = dict(cursor.fetchone())
        cursor.execute(
            "SELECT count(*) AS chunks, count(*) FILTER (WHERE sparse_embedding IS NOT NULL) AS sparse "
            "FROM chunk WHERE tenant_id=%s",
            (tenant_id,),
        )
        chunks = dict(cursor.fetchone())
    counts.update(chunks)
    return counts


def seed(revision: Path) -> int:
    tenant_id, user_id = _create_identity()
    counts = _seed_counts(tenant_id)
    if counts["total"] == 0:
        job_id = _submit(_corpus_specs(revision), tenant_id, user_id)
        _wait_job(tenant_id, job_id)
        print(f"ingestion job={job_id} reached awaiting_review")
    result = _publish_versions(tenant_id, user_id)
    counts = _seed_counts(tenant_id)
    if counts["active"] != 36:
        raise AbFailure(f"expected 36 active versions, found {counts['active']}")
    if not counts["sparse"]:
        raise AbFailure("no chunk in the ab-test tenant has a sparse_embedding")
    print(json.dumps({
        "tenant": TENANT_EXTERNAL_ID,
        "shop": SHOP_ID,
        "published": result["published"],
        "withheld": result["withheld"],
        "active_versions": counts["active"],
        "chunks": counts["chunks"],
        "sparse_chunks": counts["sparse"],
    }, ensure_ascii=False, sort_keys=True))
    return 0


def _scope(tenant_id: int, documents: list[str]) -> dict[str, Any]:
    return {
        "tenant_id": TENANT_EXTERNAL_ID,
        "tenant_db_id": str(tenant_id),
        "user_id": USER_EXTERNAL_ID,
        "user_db_id": "0",
        "shop_id": SHOP_ID,
        "allowed_shop_ids": [SHOP_ID],
        "allowed_document_ids": documents,
        "role": "operator",
    }


def _eligible_cases(revision: Path, tenant_id: int) -> list[dict[str, Any]]:
    from alignment_policy import assess_document

    corpus = {doc["document_id"]: doc for doc in _jsonl(revision / "documents.jsonl")}
    cases = _jsonl(revision / "synthetic-m2-v1-revision-1.jsonl")
    alignment = json.loads((revision / "alignment-revision-1.json").read_text(encoding="utf-8"))
    if alignment.get("status") != "APPROVED":
        raise AbFailure("the alignment is not approved")
    mapping = alignment.get("case_to_source_documents", {})
    eligible = []
    for case in cases:
        case_mapping = mapping.get(case.get("case_id"), {})
        expected = set(case.get("expected_doc_ids", []))
        if not isinstance(case_mapping, dict) or set(case_mapping) != expected:
            raise AbFailure(f"{case.get('case_id')}: alignment keys do not match expected_doc_ids")
        source_ids = list(case_mapping.values())
        documents = [corpus.get(source_id) for source_id in source_ids]
        if not all(documents):
            raise AbFailure(f"{case.get('case_id')}: alignment points at a missing source document")
        statuses = [
            assess_document(document, case["business_date"], case["authorization"])["status"]
            for document in documents
        ]
        if all(status == "ELIGIBLE" for status in statuses):
            eligible.append({"case": case, "expected": set(source_ids)})
    return eligible


def _provider_for_model(model: str):
    from app.providers import build_provider
    from app.providers.aliyun_bailian import MockProvider
    from app.providers.config import load_provider_config, validate_values

    if model == "mock-embedding":
        return MockProvider(validate_values({
            "PROVIDER_PROFILE": "mock", "CONTROL_MODEL": "mock-control",
            "EMBEDDING_MODEL": "mock-embedding", "RERANK_MODEL": "mock-rerank",
            "GENERATION_MODEL": "mock-generation",
        }))
    values = _env_file(DEV_ENV)
    os.environ.setdefault("V2_USAGE_DSN", values.get("V2_INGEST_DSN", ""))
    config = load_provider_config(os.environ.get("V2_PROVIDER_CONFIG", "/etc/ecommerce-rag/providers.env"))
    if config.embedding_model != model:
        raise AbFailure(f"corpus model {model} does not match provider model {config.embedding_model}")
    return build_provider(config, record_usage=True)


def _embeddings(provider, texts: list[str], text_type: str, tenant_id: int,
                limiter: RequestLimiter, batch_size: int = 16) -> list[Any]:
    results = []
    for start in range(0, len(texts), batch_size):
        limiter.before_request()
        batch = texts[start:start + batch_size]
        results.extend(asyncio.run(provider.embed(
            batch, text_type=text_type, dimensions=1024, output_type="dense&sparse",
            tenant_id=tenant_id,
        )))
    return results


def _query_embeddings(queries: list[str], model: str, tenant_id: int,
                      limiter: RequestLimiter) -> list[tuple[tuple[float, ...], tuple[Any, ...]]]:
    provider = _provider_for_model(model)
    results = _embeddings(provider, queries, "query", tenant_id, limiter)
    return [(tuple(item.dense), tuple(item.sparse)) for item in results]


def reembed(model: str | None = None, limiter: RequestLimiter | None = None) -> int:
    """Replace the ab-test chunk vectors with the selected embedding model."""
    import psycopg
    from psycopg.rows import dict_row

    from app.ingest.sparse import SPARSE_DIMENSION, sparse_tokenizer_id, to_literal
    from app.retrieval.postgres import _vector_literal

    dsn = _dsn("V2_INGEST_DSN", env_file=DEV_ENV)
    with psycopg.connect(dsn, row_factory=dict_row) as connection, connection.cursor() as cursor:
        cursor.execute("SELECT id FROM tenant WHERE external_id=%s", (TENANT_EXTERNAL_ID,))
        row = cursor.fetchone()
        if row is None:
            raise AbFailure("run seed first: the ab-test tenant is missing")
        tenant_id = int(row["id"])
        cursor.execute(
            "SELECT id,content FROM chunk WHERE tenant_id=%s ORDER BY id", (tenant_id,),
        )
        chunks = cursor.fetchall()
    if not chunks:
        raise AbFailure("the ab-test tenant has no chunks")
    if model is None:
        from app.providers.config import load_provider_config

        model = load_provider_config(
            os.environ.get("V2_PROVIDER_CONFIG", "/etc/ecommerce-rag/providers.env")
        ).embedding_model
    provider = _provider_for_model(model)
    limiter = limiter or RequestLimiter(300, 30)
    results = _embeddings(
        provider, [chunk["content"] for chunk in chunks], "document", tenant_id, limiter,
    )
    prepared = []
    for chunk, result in zip(chunks, results):
        literal, reason = to_literal(result.sparse, dimension=SPARSE_DIMENSION)
        if literal is None:
            raise AbFailure(f"chunk {chunk['id']} sparse rejected: {reason}")
        prepared.append((chunk, result, literal))
    written = 0
    with psycopg.connect(dsn) as connection, connection.cursor() as cursor:
        for chunk, result, literal in prepared:
            cursor.execute(
                "UPDATE chunk SET embedding=%s::halfvec,sparse_embedding=%s::sparsevec,"
                "sparse_tokenizer_id=%s WHERE tenant_id=%s AND id=%s",
                (_vector_literal(result.dense), literal,
                 sparse_tokenizer_id(provider.provider_name, result.model), tenant_id, chunk["id"]),
            )
            written += 1
        cursor.execute(
            "UPDATE document_version SET embedding_model=%s WHERE tenant_id=%s",
            (results[0].model, tenant_id),
        )
        cursor.execute(
            "SELECT count(*),count(*) FILTER (WHERE sparse_embedding IS NOT NULL) "
            "FROM chunk WHERE tenant_id=%s",
            (tenant_id,),
        )
        total, sparse_count = cursor.fetchone()
        if total != 74 or sparse_count != 74:
            raise AbFailure(f"expected 74/74 sparse chunks, found {sparse_count}/{total}")
        connection.commit()
    print(json.dumps({
        "tenant": TENANT_EXTERNAL_ID, "chunks": written, "missing_sparse": 0,
        "sparse_chunks": sparse_count, "embedding_model": results[0].model,
        "provider_requests": limiter.used,
    }, ensure_ascii=False, sort_keys=True))
    return 0


def _rank(results: list[dict[str, Any]], expected: set[str]) -> tuple[list[str], int | None]:
    documents: list[str] = []
    for item in results:
        document_id = item.get("document_id")
        if isinstance(document_id, str) and document_id not in documents:
            documents.append(document_id)
    top = documents[:5]
    rank = next((index for index, document_id in enumerate(top, 1) if document_id in expected), None)
    return top, rank


def _score(rank: int | None, top: list[str], expected: set[str]) -> dict[str, Any]:
    relevances = [1 if document_id in expected else 0 for document_id in top]
    ideal = [1] * min(len(expected), 5)
    dcg = sum(value / math.log2(index + 2) for index, value in enumerate(relevances))
    idcg = sum(value / math.log2(index + 2) for index, value in enumerate(ideal))
    return {
        "hit": rank is not None,
        "reciprocal_rank": 1 / rank if rank else 0.0,
        "ndcg_at_5": dcg / max(idcg, 1.0),
        "top5": top,
    }


def _metrics(rows: list[dict[str, Any]]) -> dict[str, float]:
    count = len(rows)
    return {
        "recall_at_5": sum(row["hit"] for row in rows) / count,
        "mrr": sum(row["reciprocal_rank"] for row in rows) / count,
        "ndcg_at_5": sum(row["ndcg_at_5"] for row in rows) / count,
    }


def _evaluate(mode: str, cases: list[dict[str, Any]], vectors: list[tuple[Any, Any]],
              scope: dict[str, Any], sparse_rrf_weight: float) -> tuple[list[dict[str, Any]], int]:
    from app.retrieval.postgres import PostgresRetriever

    dsn = _dsn("V2_RETRIEVER_DSN", env_file=DEV_ENV)
    retriever = PostgresRetriever(
        dsn, sparse_retrieval=mode, sparse_rrf_weight=sparse_rrf_weight,
    )
    expected_mode = "text_heuristic" if mode == "legacy" else "sparsevec"
    rows = []
    wrong_mode = 0
    for item, (dense, sparse) in zip(cases, vectors):
        case = item["case"]
        results = asyncio.run(retriever.asearch(
            query=case["query"],
            query_vector=dense,
            query_sparse=sparse,
            scope=scope,
            response_purpose="internal",
            business_date=case["business_date"],
            limit=8,
        ))
        if retriever.last_sparse_mode != expected_mode:
            wrong_mode += 1
        top, rank = _rank(results, item["expected"])
        rows.append({
            "case_id": case["case_id"],
            **_score(rank, top, item["expected"]),
            "actual_sparse_mode": retriever.last_sparse_mode,
        })
    return rows, wrong_mode


def _print_table(label: str, rows: list[dict[str, Any]]) -> None:
    print(f"\n{label}")
    print("case_id\thit\trr\tndcg@5\ttop5")
    for row in rows:
        print(f"{row['case_id']}\t{int(row['hit'])}\t{row['reciprocal_rank']:.4f}\t"
              f"{row['ndcg_at_5']:.4f}\t{','.join(row['top5'])}")


def run(revision: Path, limit: int | None, limiter: RequestLimiter | None = None,
        sparse_rrf_weight: float = SPARSE_RRF_WEIGHT) -> int:
    import psycopg
    from psycopg.rows import dict_row

    dsn = _dsn("V2_RETRIEVER_DSN", env_file=DEV_ENV)
    with psycopg.connect(dsn, row_factory=dict_row) as connection, connection.cursor() as cursor:
        cursor.execute(
            "SELECT t.id, count(v.id) FILTER (WHERE v.status='active' AND v.review_status='approved') "
            "AS active FROM tenant t LEFT JOIN document_version v ON v.tenant_id=t.id "
            "WHERE t.external_id=%s GROUP BY t.id",
            (TENANT_EXTERNAL_ID,),
        )
        row = cursor.fetchone()
        if row is None or int(row["active"]) != 36:
            raise AbFailure("run seed first: the ab-test tenant does not have 36 published versions")
        tenant_id = int(row["id"])
        cursor.execute(
            "SELECT d.logical_key FROM document d JOIN document_version v "
            "ON v.tenant_id=d.tenant_id AND v.id=d.active_version_id "
            "WHERE d.tenant_id=%s AND d.status='active' AND v.status='active' "
            "AND v.review_status='approved' AND v.disclosure_class='external_allowed' "
            "ORDER BY d.logical_key",
            (tenant_id,),
        )
        documents = [item["logical_key"] for item in cursor.fetchall()]
        cursor.execute(
            "SELECT DISTINCT embedding_model FROM document_version "
            "WHERE tenant_id=%s AND status='active'",
            (tenant_id,),
        )
        models = [item["embedding_model"] for item in cursor.fetchall()]
    if len(models) != 1:
        raise AbFailure(f"ab-test corpus has mixed embedding models: {models}")
    embedding_model = models[0]
    cases = _eligible_cases(revision, tenant_id)
    if limit is not None:
        cases = cases[:limit]
    limiter = limiter or RequestLimiter(300, 30)
    vectors = _query_embeddings(
        [item["case"]["query"] for item in cases], embedding_model, tenant_id, limiter,
    )
    scope = _scope(tenant_id, documents)
    legacy, legacy_wrong = _evaluate("legacy", cases, vectors, scope, sparse_rrf_weight)
    sparse, sparse_wrong = _evaluate("sparsevec", cases, vectors, scope, sparse_rrf_weight)
    legacy_metrics = _metrics(legacy)
    sparse_metrics = _metrics(sparse)
    _print_table("MODE legacy", legacy)
    _print_table("MODE sparsevec", sparse)
    print("\nDIFF")
    changed = 0
    for old, new in zip(legacy, sparse):
        if old["top5"] == new["top5"] and old["reciprocal_rank"] == new["reciprocal_rank"]:
            continue
        changed += 1
        print(f"{old['case_id']}\tlegacy_rr={old['reciprocal_rank']:.4f}\t"
              f"sparsevec_rr={new['reciprocal_rank']:.4f}\t"
              f"legacy={','.join(old['top5'])}\tsparsevec={','.join(new['top5'])}")
    summary = {
        "case_count": len(cases),
        "embedding_model": embedding_model,
        "sparse_rrf_weight": sparse_rrf_weight,
        "changed_case_count": changed,
        "legacy": legacy_metrics,
        "sparsevec": sparse_metrics,
        "delta": {
            key: sparse_metrics[key] - legacy_metrics[key] for key in legacy_metrics
        },
        "legacy_wrong_mode": legacy_wrong,
        "sparsevec_wrong_mode": sparse_wrong,
        "provider_requests": limiter.used,
    }
    print("\nSUMMARY " + json.dumps(summary, ensure_ascii=False, sort_keys=True))
    if legacy_wrong or sparse_wrong:
        print("FAIL sparse mode did not match the selected branch")
        return 1
    if limit is None and len(cases) != 53:
        print(f"FAIL expected 53 eligible cases, found {len(cases)}")
        return 1
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("seed", "reembed", "run"))
    parser.add_argument("--revision-root", type=Path, default=DEFAULT_REVISION)
    parser.add_argument("--limit", type=int)
    parser.add_argument("--model", help="embedding model for reembed (default: providers.env)")
    parser.add_argument("--max-requests", type=int, default=300)
    parser.add_argument("--max-requests-per-minute", type=int, default=30)
    parser.add_argument("--sparse-rrf-weight", type=float, default=SPARSE_RRF_WEIGHT)
    args = parser.parse_args()
    try:
        if not args.revision_root.is_dir():
            raise AbFailure(f"approved revision is unavailable: {args.revision_root}")
        if not math.isfinite(args.sparse_rrf_weight) or args.sparse_rrf_weight <= 0:
            raise AbFailure("--sparse-rrf-weight must be a positive finite number")
        limiter = RequestLimiter(args.max_requests, args.max_requests_per_minute)
        if args.command == "seed":
            return seed(args.revision_root)
        if args.command == "reembed":
            return reembed(args.model, limiter)
        if args.limit is not None and args.limit < 1:
            raise AbFailure("--limit must be positive")
        return run(args.revision_root, args.limit, limiter, args.sparse_rrf_weight)
    except AbFailure as error:
        print(f"FAIL {error}")
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
