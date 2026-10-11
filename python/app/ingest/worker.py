"""Durable Python-owned ingestion worker for v2.

The worker claims jobs with a fencing epoch, parses source files using the
same parser/chunker as the gRPC validation path, and commits a complete
reviewable version in one transaction.  A failed parse never leaves a
partial version or chunk set.
"""

from __future__ import annotations

import hashlib
import json
import os
import shutil
import threading
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path, PurePosixPath
from typing import Any
from uuid import uuid4

import psycopg
from psycopg.rows import dict_row

from app.ingest.database import IngestDatabase
from app.ingest.pipeline import chunk_elements_v2, parser_for
from app.ingest.service import MAX_CHUNKS, _inspect_package
from rag.v1 import rag_pb2


class PackageValidationError(ValueError):
    """A persisted package no longer satisfies the manifest contract."""


class _LocalContext:
    def abort(self, _code: Any, message: str) -> None:
        raise PackageValidationError(message)


@dataclass(frozen=True)
class ClaimedFile:
    item_id: int
    item_epoch: int
    document_id: str
    relative_path: str
    content_sha256: str
    content: bytes


@dataclass(frozen=True)
class IngestClaim:
    tenant_id: int
    job_id: int
    source_id: int
    manifest_id: int
    submitted_by: int
    fencing_epoch: int
    retry_count: int
    max_attempts: int
    owner_token: str
    manifest: str
    manifest_sha256: str
    shop_id: str
    files: tuple[ClaimedFile, ...]


def _package_for_parse(
    manifest_yaml: str, files: tuple[ClaimedFile, ...], tenant: str, shop: str
) -> dict[str, Any]:
    request = rag_pb2.ValidatePackageRequest(
        manifest_yaml=manifest_yaml,
        files=[
            rag_pb2.PackageFile(path=item.relative_path, content=item.content)
            for item in files
        ],
        tenant_id=tenant,
        shop_id=shop,
    )
    package = _inspect_package(request, _LocalContext())
    expected = {item.relative_path: item for item in files}
    if set(package["uploaded"]) != set(expected):
        raise PackageValidationError("persisted package file set changed")
    return package


def parse_persisted_package(
    manifest_yaml: str,
    files: tuple[ClaimedFile, ...],
    *,
    tenant: str,
    shop: str,
) -> tuple[dict[str, Any], str]:
    """Parse all documents before any database version/chunk write occurs."""

    package = _package_for_parse(manifest_yaml, files, tenant, shop)
    by_path = {item.relative_path: item.content for item in files}
    parsed_documents: list[dict[str, Any]] = []
    dataset_rows: list[dict[str, Any]] = []
    chunk_total = 0
    for spec in package["documents"]:
        temporary_root = Path("/tmp") / f"ecr-v2-{uuid4().hex}"
        target = temporary_root / PurePosixPath(spec["path"])
        try:
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(by_path[spec["path"]])
            metadata = {
                "tenant_id": tenant,
                "shop_id": shop,
                "title": spec["title"],
                "disclosure_class": spec["disclosure_class"],
                "effective_from": spec["effective_from"] or None,
                "effective_to": spec["effective_to"] or None,
                "source_hash": spec["source_hash"],
            }
            elements = parser_for(spec["format"]).parse(
                target,
                document_id=spec["document_id"],
                version_id=spec["version_id"],
                metadata=metadata,
            )
            chunks = chunk_elements_v2(elements)
        except (OSError, UnicodeError, ValueError, KeyError, RuntimeError) as error:
            raise PackageValidationError("document could not be parsed") from error
        finally:
            shutil.rmtree(temporary_root, ignore_errors=True)
        if not chunks:
            raise PackageValidationError("document produced no content chunks")
        serialized_chunks: list[dict[str, Any]] = []
        for index, chunk in enumerate(chunks):
            chunk_total += 1
            if chunk_total > MAX_CHUNKS:
                raise PackageValidationError("parsed package exceeds the chunk limit")
            position = chunk.source_position
            serialized_chunks.append(
                {
                    "chunk_index": index,
                    "section_seq": chunk.section_seq,
                    "section_chunk_index": chunk.chunk_index,
                    "char_start": int(position.get("char_start", 0)),
                    "char_end": int(position.get("char_end", len(chunk.content))),
                    "title": chunk.title,
                    "heading_path": " / ".join(chunk.heading),
                    "content": chunk.content,
                    "token_count": max(1, len(chunk.content.split())),
                    "content_type": chunk.content_type,
                    "split_reason": chunk.split_reason,
                    "chunk_hash": chunk.chunk_hash,
                    "metadata": dict(chunk.metadata),
                }
            )
        parsed_documents.append({"spec": spec, "chunks": serialized_chunks})
        dataset_rows.append(
            {
                "document_id": spec["document_id"],
                "source_hash": spec["source_hash"],
                "chunks": len(serialized_chunks),
            }
        )
    canonical = json.dumps(
        {"manifest_sha256": package["manifest_sha256"], "documents": dataset_rows},
        sort_keys=True,
        separators=(",", ":"),
    ).encode()
    return {"package": package, "documents": parsed_documents}, hashlib.sha256(
        canonical
    ).hexdigest()


class IngestWorker:
    """Single Python worker process for v2 package claims."""

    def __init__(
        self,
        database: IngestDatabase,
        *,
        poll_seconds: float = 0.9,
        lease_seconds: int = 45,
        worker_id: str | None = None,
    ) -> None:
        self.database = database
        self.poll_seconds = max(0.1, poll_seconds)
        self.lease_seconds = max(5, lease_seconds)
        self.worker_id = worker_id or f"python-ingest-{uuid4().hex}"
        self._stop = threading.Event()

    @classmethod
    def from_env(cls) -> IngestWorker | None:
        database = IngestDatabase.from_env()
        if database is None:
            return None
        return cls(
            database,
            poll_seconds=float(os.environ.get("V2_INGEST_POLL_SECONDS", "0.9")),
            lease_seconds=int(os.environ.get("V2_INGEST_LEASE_SECONDS", "45")),
        )

    def stop(self) -> None:
        self._stop.set()

    def run(self, stop_event: threading.Event | None = None) -> None:
        external = stop_event or self._stop
        started = datetime.now(UTC)
        self.database.verify_schema()
        self._heartbeat("idle", started=started)
        while not external.is_set():
            try:
                claim = self._claim()
                if claim is None:
                    self._heartbeat("idle", started=started)
                    external.wait(self.poll_seconds)
                    continue
                self._heartbeat("working", started=started, claim=claim)
                self._process(claim)
            except Exception as error:  # noqa: BLE001 - worker stays alive and records a redacted code
                self._heartbeat(
                    "degraded", started=started, error_code=type(error).__name__
                )
                external.wait(min(5.0, self.poll_seconds * 2))
        self._heartbeat("stopping", started=started)

    def _heartbeat(
        self,
        status: str,
        *,
        started: datetime,
        claim: IngestClaim | None = None,
        error_code: str = "",
    ) -> None:
        try:
            with (
                psycopg.connect(self.database.conn_string) as connection,
                connection.cursor() as cursor,
            ):
                cursor.execute(
                    "INSERT INTO ingest_worker_heartbeat(worker_id,status,started_at,heartbeat_at,"
                    "active_tenant_id,active_job_id,last_error_code) VALUES(%s,%s,%s,now(),%s,%s,%s) "
                    "ON CONFLICT(worker_id) DO UPDATE SET status=EXCLUDED.status,heartbeat_at=now(),"
                    "active_tenant_id=EXCLUDED.active_tenant_id,active_job_id=EXCLUDED.active_job_id,"
                    "last_error_code=EXCLUDED.last_error_code",
                    (
                        self.worker_id,
                        status,
                        started,
                        claim.tenant_id if claim else None,
                        claim.job_id if claim else None,
                        error_code or None,
                    ),
                )
        except psycopg.Error:
            # A heartbeat cannot turn a transient database outage into a worker crash.
            return

    def _claim(self) -> IngestClaim | None:
        owner = f"{self.worker_id}:{uuid4().hex}"
        with (
            psycopg.connect(
                self.database.conn_string, row_factory=dict_row
            ) as connection,
            connection.transaction(),
            connection.cursor() as cursor,
        ):
            cursor.execute(
                "SELECT j.tenant_id,j.id,j.source_id,sm.id AS manifest_id,j.submitted_by,"
                "j.fencing_epoch,j.retry_count,j.max_attempts,sm.manifest_yaml,sm.manifest_sha256,"
                "s.shop_id FROM ingest_job j JOIN source s ON s.tenant_id=j.tenant_id AND s.id=j.source_id "
                "JOIN source_manifest sm ON sm.tenant_id=j.tenant_id AND sm.source_id=j.source_id "
                "AND sm.manifest_sha256=j.manifest_sha256 WHERE NOT j.cancel_requested "
                "AND j.next_attempt_at<=now() AND (j.status='pending' OR "
                "(j.status='running' AND j.lease_expires_at<=now())) "
                "ORDER BY j.next_attempt_at,j.created_at,j.id LIMIT 1 FOR UPDATE OF j SKIP LOCKED",
            )
            row = cursor.fetchone()
            if row is None:
                return None
            epoch = int(row["fencing_epoch"]) + 1
            cursor.execute(
                "UPDATE ingest_job SET status='running',stage='parsing',owner_token=%s,"
                "fencing_epoch=%s,lease_expires_at=now()+%s::interval,updated_at=now(),error_code=NULL "
                "WHERE tenant_id=%s AND id=%s",
                (
                    owner,
                    epoch,
                    f"{self.lease_seconds} seconds",
                    row["tenant_id"],
                    row["id"],
                ),
            )
            cursor.execute(
                "SELECT i.id,i.fencing_epoch,i.data_id,sf.relative_path,sf.content_sha256,sf.content "
                "FROM ingest_job_item i JOIN source_file sf ON sf.tenant_id=i.tenant_id "
                "AND sf.document_id=i.data_id JOIN source_manifest sm ON sm.tenant_id=sf.tenant_id "
                "AND sm.id=sf.source_manifest_id AND sm.id=%s WHERE i.tenant_id=%s AND i.job_id=%s "
                "AND i.status IN ('pending','running') AND NOT i.cancel_requested ORDER BY sf.relative_path",
                (row["manifest_id"], row["tenant_id"], row["id"]),
            )
            files = []
            for item in cursor.fetchall():
                item_epoch = int(item["fencing_epoch"]) + 1
                item_owner = f"{owner}:item:{item['id']}"
                cursor.execute(
                    "UPDATE ingest_job_item SET status='running',stage='parsing',owner_token=%s,"
                    "fencing_epoch=%s,lease_expires_at=now()+%s::interval,error_code=NULL,error=NULL "
                    "WHERE tenant_id=%s AND id=%s AND status IN ('pending','running') "
                    "AND NOT cancel_requested",
                    (
                        item_owner,
                        item_epoch,
                        f"{self.lease_seconds} seconds",
                        row["tenant_id"],
                        item["id"],
                    ),
                )
                files.append(
                    ClaimedFile(
                        item_id=int(item["id"]),
                        item_epoch=item_epoch,
                        document_id=str(item["data_id"]),
                        relative_path=str(item["relative_path"]),
                        content_sha256=str(item["content_sha256"]),
                        content=bytes(item["content"]),
                    )
                )
            if not files:
                raise RuntimeError("queued ingestion job has no claimable files")
            return IngestClaim(
                tenant_id=int(row["tenant_id"]),
                job_id=int(row["id"]),
                source_id=int(row["source_id"]),
                manifest_id=int(row["manifest_id"]),
                submitted_by=int(row["submitted_by"]),
                fencing_epoch=epoch,
                retry_count=int(row["retry_count"]),
                max_attempts=int(row["max_attempts"]),
                owner_token=owner,
                manifest=str(row["manifest_yaml"]),
                manifest_sha256=str(row["manifest_sha256"]),
                shop_id=str(row["shop_id"]),
                files=tuple(files),
            )

    def _process(self, claim: IngestClaim) -> None:
        stop_renew = threading.Event()
        lost_lease = threading.Event()
        renewer = threading.Thread(
            target=self._renew_loop,
            args=(claim, stop_renew, lost_lease),
            daemon=True,
        )
        renewer.start()
        try:
            # The parser takes semantic scope identifiers for metadata binding.
            tenant_alias = self._tenant_alias(claim.tenant_id)
            parsed, dataset_sha = parse_persisted_package(
                claim.manifest,
                claim.files,
                tenant=tenant_alias,
                shop=claim.shop_id,
            )
            if lost_lease.is_set():
                raise RuntimeError("ingest lease lost")
            self._publish_reviewable(claim, parsed, dataset_sha)
        except PackageValidationError as error:
            self._settle_failure(
                claim, "parse_invalid", retryable=False, detail=str(error)
            )
        except Exception as error:  # noqa: BLE001 - error detail is not exposed to clients
            self._settle_failure(
                claim, "parse_failed", retryable=True, detail=type(error).__name__
            )
        finally:
            stop_renew.set()
            renewer.join(timeout=2)

    def _renew_loop(
        self, claim: IngestClaim, stop: threading.Event, lost: threading.Event
    ) -> None:
        while not stop.wait(self.lease_seconds / 3):
            try:
                with (
                    psycopg.connect(self.database.conn_string) as connection,
                    connection.cursor() as cursor,
                ):
                    cursor.execute(
                        "UPDATE ingest_job SET lease_expires_at=now()+%s::interval,updated_at=now() "
                        "WHERE tenant_id=%s AND id=%s AND owner_token=%s AND fencing_epoch=%s "
                        "AND status='running' AND NOT cancel_requested",
                        (
                            f"{self.lease_seconds} seconds",
                            claim.tenant_id,
                            claim.job_id,
                            claim.owner_token,
                            claim.fencing_epoch,
                        ),
                    )
                    if cursor.rowcount != 1:
                        lost.set()
                        return
                    cursor.execute(
                        "UPDATE ingest_job_item SET lease_expires_at=now()+%s::interval "
                        "WHERE tenant_id=%s AND job_id=%s AND status='running' "
                        "AND owner_token LIKE %s",
                        (
                            f"{self.lease_seconds} seconds",
                            claim.tenant_id,
                            claim.job_id,
                            f"{claim.owner_token}:item:%",
                        ),
                    )
            except psycopg.Error:
                lost.set()
                return

    def _publish_reviewable(
        self, claim: IngestClaim, parsed: dict[str, Any], dataset_sha: str
    ) -> None:
        with (
            psycopg.connect(self.database.conn_string) as connection,
            connection.transaction(),
            connection.cursor() as cursor,
        ):
            cursor.execute(
                "SELECT cancel_requested FROM ingest_job WHERE tenant_id=%s AND id=%s "
                "AND owner_token=%s AND fencing_epoch=%s FOR UPDATE",
                (claim.tenant_id, claim.job_id, claim.owner_token, claim.fencing_epoch),
            )
            row = cursor.fetchone()
            if row is None:
                raise RuntimeError("ingest fencing owner is stale")
            if row[0]:
                self._mark_cancelled(cursor, claim)
                return
            version_ids: list[int] = []
            item_by_document = {item.document_id: item for item in claim.files}
            for document in parsed["documents"]:
                spec = document["spec"]
                file = item_by_document[spec["document_id"]]
                cursor.execute(
                    "SELECT d.id FROM document d WHERE d.tenant_id=%s AND d.source_id=%s "
                    "AND d.logical_key=%s FOR UPDATE",
                    (claim.tenant_id, claim.source_id, spec["document_id"]),
                )
                document_row = cursor.fetchone()
                if document_row is None:
                    raise RuntimeError("document row disappeared during ingestion")
                cursor.execute(
                    "INSERT INTO document_version(tenant_id,document_id,source_hash,parser_version,"
                    "chunk_rule_version,embedding_model,pipeline_version,object_key,status,disclosure_class,"
                    "external_allowed,review_status,quality_json,chunk_count,fencing_epoch,source_manifest_id) "
                    "VALUES(%s,%s,%s,'grpc-local-parser-v1','structured-v2','not_run',%s,%s,'ready',%s,false,"
                    "'pending',%s::jsonb,%s,%s,%s) RETURNING id",
                    (
                        claim.tenant_id,
                        document_row[0],
                        spec["source_hash"],
                        parsed["package"]["pipeline_version"],
                        f"db://source_file/{claim.manifest_id}/{spec['path']}",
                        spec["disclosure_class"],
                        json.dumps(
                            {
                                "parse_status": "passed",
                                "embedding_status": "not_run",
                                "index_mode": "deterministic_keyword",
                                "quality_gate": "passed",
                                "chunk_count": len(document["chunks"]),
                                "empty_chunk_count": 0,
                            },
                            ensure_ascii=False,
                        ),
                        len(document["chunks"]),
                        claim.fencing_epoch,
                        claim.manifest_id,
                    ),
                )
                version_id = int(cursor.fetchone()[0])
                version_ids.append(version_id)
                for chunk in document["chunks"]:
                    metadata = dict(chunk["metadata"])
                    metadata.update(
                        {
                            "tenant_id": str(claim.tenant_id),
                            "shop_id": claim.shop_id,
                            "source_hash": spec["source_hash"],
                            "disclosure_class": spec["disclosure_class"],
                            "effective_from": spec["effective_from"],
                            "effective_to": spec["effective_to"],
                        }
                    )
                    cursor.execute(
                        "INSERT INTO chunk(tenant_id,doc_id,version_id,shop_id,chunk_index,section_seq,"
                        "section_chunk_index,char_start,char_end,heading_path,content,embed_text,token_count,"
                        "content_type,split_reason,embedding_model,embedding_dim,tokenizer_id,effective_from,"
                        "effective_to,meta_json,source_object_key,chunk_hash,lang) VALUES(%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,"
                        "'not_run',1024,'whitespace-v1',%s,%s,%s,%s,%s,'zh')",
                        (
                            claim.tenant_id,
                            document_row[0],
                            version_id,
                            claim.shop_id,
                            chunk["chunk_index"],
                            chunk["section_seq"],
                            chunk["section_chunk_index"],
                            chunk["char_start"],
                            chunk["char_end"],
                            chunk["heading_path"],
                            chunk["content"],
                            chunk["content"],
                            chunk["token_count"],
                            chunk["content_type"],
                            chunk["split_reason"],
                            spec["effective_from"] or None,
                            spec["effective_to"] or None,
                            json.dumps(metadata, ensure_ascii=False, sort_keys=True),
                            f"db://source_file/{claim.manifest_id}/{spec['path']}",
                            chunk["chunk_hash"],
                        ),
                    )
                cursor.execute(
                    "UPDATE ingest_job_item SET status='awaiting_review',stage='review',document_version_id=%s,"
                    "lease_expires_at=NULL,error_code=NULL WHERE tenant_id=%s AND id=%s "
                    "AND owner_token=%s AND fencing_epoch=%s AND status='running'",
                    (
                        version_id,
                        claim.tenant_id,
                        file.item_id,
                        f"{claim.owner_token}:item:{file.item_id}",
                        file.item_epoch,
                    ),
                )
                if cursor.rowcount != 1:
                    raise RuntimeError("ingest item fencing owner is stale")
                # Release the owner only after the item leaves the running
                # state.  The fencing trigger must still reject active lease
                # takeover, while allowing the current owner to hand off a
                # reviewable result.
                cursor.execute(
                    "UPDATE ingest_job_item SET owner_token=NULL WHERE tenant_id=%s AND id=%s "
                    "AND owner_token=%s AND fencing_epoch=%s AND status='awaiting_review'",
                    (
                        claim.tenant_id,
                        file.item_id,
                        f"{claim.owner_token}:item:{file.item_id}",
                        file.item_epoch,
                    ),
                )
                if cursor.rowcount != 1:
                    raise RuntimeError("ingest item owner release was fenced")
            # source_manifest is an immutable submission record.  The parsed
            # dataset identity belongs to the job result, so only ingest_job
            # receives the derived hash below.  This also keeps the worker
            # within the application role's least-privilege grants.
            cursor.execute(
                "UPDATE ingest_job SET status='awaiting_review',stage='review',dataset_sha256=%s,"
                "lease_expires_at=NULL,updated_at=now() WHERE tenant_id=%s AND id=%s "
                "AND owner_token=%s AND fencing_epoch=%s",
                (
                    dataset_sha,
                    claim.tenant_id,
                    claim.job_id,
                    claim.owner_token,
                    claim.fencing_epoch,
                ),
            )
            if cursor.rowcount != 1:
                raise RuntimeError("ingest job fencing owner is stale")
            cursor.execute(
                "UPDATE ingest_job SET owner_token=NULL WHERE tenant_id=%s AND id=%s "
                "AND owner_token=%s AND fencing_epoch=%s AND status='awaiting_review'",
                (claim.tenant_id, claim.job_id, claim.owner_token, claim.fencing_epoch),
            )
            if cursor.rowcount != 1:
                raise RuntimeError("ingest job owner release was fenced")
            cursor.execute(
                "INSERT INTO admin_audit(tenant_id,actor_user_id,action,resource_type,resource_id,"
                "content_hash,fencing_epoch,reason,result,metadata_json) VALUES(%s,%s,'ingestion.parsed',"
                "'ingest_job',%s,%s,%s,'Python worker produced reviewable versions','success',%s::jsonb)",
                (
                    claim.tenant_id,
                    claim.submitted_by,
                    str(claim.job_id),
                    dataset_sha,
                    claim.fencing_epoch,
                    json.dumps(
                        {
                            "document_count": len(parsed["documents"]),
                            "status": "awaiting_review",
                        }
                    ),
                ),
            )

    def _settle_failure(
        self, claim: IngestClaim, error_code: str, *, retryable: bool, detail: str
    ) -> None:
        try:
            with (
                psycopg.connect(self.database.conn_string) as connection,
                connection.transaction(),
                connection.cursor() as cursor,
            ):
                cursor.execute(
                    "SELECT cancel_requested FROM ingest_job WHERE tenant_id=%s AND id=%s "
                    "AND owner_token=%s AND fencing_epoch=%s FOR UPDATE",
                    (
                        claim.tenant_id,
                        claim.job_id,
                        claim.owner_token,
                        claim.fencing_epoch,
                    ),
                )
                row = cursor.fetchone()
                if row is None:
                    return
                if row[0]:
                    self._mark_cancelled(cursor, claim)
                    return
                next_retry = retryable and claim.retry_count + 1 < claim.max_attempts
                status = "pending" if next_retry else "failed"
                stage = "queued" if next_retry else "failed"
                cursor.execute(
                    "UPDATE ingest_job SET status=%s,stage=%s,retry_count=retry_count+1,"
                    "next_attempt_at=now()+CASE WHEN %s THEN interval '5 seconds' ELSE interval '0 seconds' END,"
                    "lease_expires_at=NULL,owner_token=NULL,error_code=%s,error=%s,updated_at=now() "
                    "WHERE tenant_id=%s AND id=%s AND owner_token=%s AND fencing_epoch=%s",
                    (
                        status,
                        stage,
                        next_retry,
                        error_code,
                        detail[:200],
                        claim.tenant_id,
                        claim.job_id,
                        claim.owner_token,
                        claim.fencing_epoch,
                    ),
                )
                cursor.execute(
                    "UPDATE ingest_job_item SET status=%s,stage=%s,retry_count=retry_count+1,"
                    "lease_expires_at=NULL,owner_token=NULL,error_code=%s,error=%s "
                    "WHERE tenant_id=%s AND job_id=%s AND status='running' AND owner_token LIKE %s",
                    (
                        "pending" if next_retry else "failed",
                        stage,
                        error_code,
                        detail[:200],
                        claim.tenant_id,
                        claim.job_id,
                        f"{claim.owner_token}:item:%",
                    ),
                )
        except psycopg.Error:
            return

    @staticmethod
    def _mark_cancelled(cursor: Any, claim: IngestClaim) -> None:
        cursor.execute(
            "UPDATE ingest_job_item SET status='cancelled',stage='cancelled',lease_expires_at=NULL,"
            "owner_token=NULL,error_code='cancel_requested' WHERE tenant_id=%s AND job_id=%s AND status='running' "
            "AND owner_token LIKE %s",
            (claim.tenant_id, claim.job_id, f"{claim.owner_token}:item:%"),
        )
        cursor.execute(
            "UPDATE ingest_job SET status='cancelled',stage='cancelled',lease_expires_at=NULL,owner_token=NULL,"
            "error_code='cancel_requested',updated_at=now() WHERE tenant_id=%s AND id=%s",
            (claim.tenant_id, claim.job_id),
        )

    def _tenant_alias(self, tenant_pk: int) -> str:
        with (
            psycopg.connect(self.database.conn_string) as connection,
            connection.cursor() as cursor,
        ):
            cursor.execute("SELECT name FROM tenant WHERE id=%s", (tenant_pk,))
            row = cursor.fetchone()
        aliases = {
            "V2 LangGraph fixture tenant": "v2-demo-tenant",
            "M1 isolated prototype": "m1-tenant",
            "Synthetic ecommerce demo v1": "demo-tenant-a",
            "Synthetic ecommerce tenant b": "demo-tenant-b",
        }
        if row is None or row[0] not in aliases:
            raise PackageValidationError("tenant identity is invalid")
        return aliases[row[0]]
