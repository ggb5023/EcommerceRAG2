"""Durable v2 ingestion persistence owned by the Python AI plane.

The Go gateway supplies a signed identity and package bytes.  This module
re-checks the identity and is the only v2 writer for ingestion submission
rows.  Parsing and publication are performed by :mod:`app.ingest.worker`.
"""

from __future__ import annotations

import hashlib
import json
import os
from dataclasses import dataclass
from typing import Any

import psycopg
from psycopg.rows import dict_row


class IngestConflictError(RuntimeError):
    """The idempotency key was reused with a different package."""


class IngestAuthorizationError(PermissionError):
    """The supplied identity is not valid for the requested shop."""


@dataclass(frozen=True)
class SubmittedJob:
    job_id: int
    status: str
    manifest_sha256: str
    package_sha256: str
    item_count: int


def package_payload_hash(
    tenant_id: int,
    shop_id: str,
    manifest_sha256: str,
    files: dict[str, bytes],
) -> str:
    """Match the canonical package hash used by the Go compatibility path."""

    digest = hashlib.sha256()
    digest.update(f"{tenant_id}\0{shop_id}\0{manifest_sha256}\0".encode())
    for relative_path in sorted(files):
        digest.update(relative_path.encode())
        digest.update(b"\0")
        digest.update(hashlib.sha256(files[relative_path]).hexdigest().encode())
        digest.update(b"\0")
    return digest.hexdigest()


class IngestDatabase:
    """Small synchronous psycopg adapter used by gRPC and the worker."""

    def __init__(self, conn_string: str, *, connect=psycopg.connect) -> None:
        if not conn_string.strip():
            raise ValueError("ingest connection string is required")
        self.conn_string = conn_string
        self._connect = connect

    @property
    def connector(self):
        """Raw factory, shared with adapters that keep their own connection."""
        return self._connect

    def connection(self, **kwargs):
        """Check out one connection through the configured factory."""
        return self._connect(self.conn_string, **kwargs)

    @classmethod
    def from_env(cls) -> IngestDatabase | None:
        value = os.environ.get("V2_INGEST_DSN", "").strip()
        return cls(value) if value else None

    def verify_schema(self) -> None:
        required = {
            "tenant",
            "app_user",
            "shop",
            "source",
            "source_manifest",
            "source_file",
            "ingest_job",
            "ingest_job_item",
            "ingest_payload",
            "document",
            "document_version",
            "chunk",
            "ingest_event",
            "ingest_artifact_object",
            "ingest_command_result",
            "schema_migration",
        }
        with (
            self.connection() as connection,
            connection.cursor() as cursor,
        ):
            cursor.execute(
                "SELECT table_name FROM information_schema.tables "
                "WHERE table_schema='public' AND table_name = ANY(%s::text[])",
                (list(required),),
            )
            found = {row[0] for row in cursor.fetchall()}
            cursor.execute(
                "SELECT version FROM schema_migration WHERE version IN ('0010','0013','0022','0023')"
            )
            migrations = {row[0] for row in cursor.fetchall()}
        if not required.issubset(found) or not {"0010", "0013", "0022", "0023"}.issubset(migrations):
            raise RuntimeError("v2 ingestion schema migration is incomplete")

    @staticmethod
    def _validate_scope(
        cursor: Any,
        *,
        tenant_id: str,
        user_id: str,
        shop_id: str,
        tenant_db_id: str,
        user_db_id: str,
        permission_revision: str,
    ) -> tuple[int, int]:
        try:
            tenant_pk = int(tenant_db_id)
            user_pk = int(user_db_id)
        except (TypeError, ValueError) as error:
            raise IngestAuthorizationError("database identity is invalid") from error
        if tenant_pk < 1 or user_pk < 1 or not tenant_id or not user_id or not shop_id:
            raise IngestAuthorizationError("ingestion identity is incomplete")
        cursor.execute(
            "SELECT t.external_id, t.status, u.external_id, u.revoked_at, s.status, u.permission_revision, u.shop_ids, u.role "
            "FROM tenant t JOIN app_user u ON u.tenant_id=t.id "
            "JOIN shop s ON s.tenant_id=t.id "
            "WHERE t.id=%s AND u.id=%s AND s.id=%s",
            (tenant_pk, user_pk, shop_id),
        )
        row = cursor.fetchone()
        if row is None:
            raise IngestAuthorizationError("identity is outside the requested shop")
        tenant_name, tenant_status, external_id, revoked_at, shop_status, revision, shops, role = row
        if (
            tenant_status != "active"
            or shop_status != "active"
            or revoked_at is not None
            or str(tenant_name) != tenant_id
            or str(external_id) != user_id
            or revision != permission_revision or shop_id not in shops
        ):
            raise IngestAuthorizationError(
                "identity is not active for the requested scope"
            )
        cursor.execute("SELECT ecr_lock_ingest_scope(%s,%s,%s,%s,%s,%s,%s,%s)",
                       (tenant_pk, user_pk, tenant_id, user_id, shop_id, role, permission_revision, "merchant_admin"))
        if not cursor.fetchone()[0]:
            raise IngestAuthorizationError("submitter scope changed")
        return tenant_pk, user_pk

    def submit(
        self,
        *,
        tenant_id: str,
        user_id: str,
        shop_id: str,
        tenant_db_id: str,
        user_db_id: str,
        permission_revision: str,
        idempotency_key: str,
        source_id: str,
        source_name: str,
        pipeline_version: str,
        manifest_yaml: str,
        manifest_sha256: str,
        package_sha256: str,
        documents: list[dict[str, Any]],
        files: dict[str, bytes],
    ) -> SubmittedJob:
        if not idempotency_key or not source_id or not source_name:
            raise ValueError("submission identity is incomplete")
        with (
            self.connection() as connection,
            connection.transaction(),
            connection.cursor() as cursor,
        ):
            cursor.execute("SELECT pg_advisory_xact_lock(hashtext(%s)::bigint)", (f"knowledge:{int(tenant_db_id)}",))
            tenant_pk, user_pk = self._validate_scope(
                cursor,
                tenant_id=tenant_id,
                user_id=user_id,
                shop_id=shop_id,
                tenant_db_id=tenant_db_id,
                user_db_id=user_db_id,
                permission_revision=permission_revision,
            )
            cursor.execute(
                "SELECT pg_advisory_xact_lock(hashtext(%s)::bigint)",
                (f"{tenant_pk}:{idempotency_key}",),
            )
            cursor.execute(
                "SELECT id, manifest_sha256, status FROM ingest_job "
                "WHERE tenant_id=%s AND idempotency_key=%s",
                (tenant_pk, idempotency_key),
            )
            existing = cursor.fetchone()
            if existing is not None:
                job_id, old_manifest, status = existing
                cursor.execute(
                    "SELECT payload_hash FROM ingest_payload WHERE tenant_id=%s AND job_id=%s",
                    (tenant_pk, job_id),
                )
                payload = cursor.fetchone()
                if payload is not None and payload[0] != package_sha256:
                    raise IngestConflictError(
                        "idempotency key was reused with different package bytes"
                    )
                cursor.execute(
                    "SELECT count(*) FROM ingest_job_item WHERE tenant_id=%s AND job_id=%s",
                    (tenant_pk, job_id),
                )
                count = int(cursor.fetchone()[0])
                return SubmittedJob(
                    job_id, str(status), str(old_manifest), package_sha256, count
                )

            cursor.execute(
                "INSERT INTO source(tenant_id,shop_id,type,uri) VALUES(%s,%s,'admin_manifest',%s) "
                "ON CONFLICT(tenant_id,shop_id,type,uri) WHERE uri IS NOT NULL "
                "DO UPDATE SET uri=EXCLUDED.uri RETURNING id",
                (tenant_pk, shop_id, f"admin://{source_id}"),
            )
            source_pk = int(cursor.fetchone()[0])
            cursor.execute(
                "INSERT INTO source_manifest(tenant_id,source_id,manifest_sha256,dataset_sha256,"
                "pipeline_version,manifest_yaml,uploaded_by) VALUES(%s,%s,%s,%s,%s,%s,%s) "
                "ON CONFLICT(tenant_id,source_id,manifest_sha256) DO NOTHING "
                "RETURNING id",
                (
                    tenant_pk,
                    source_pk,
                    manifest_sha256,
                    package_sha256,
                    pipeline_version,
                    manifest_yaml,
                    user_pk,
                ),
            )
            manifest_row = cursor.fetchone()
            if manifest_row is None:
                cursor.execute(
                    "SELECT id,pipeline_version,manifest_yaml,dataset_sha256 FROM source_manifest "
                    "WHERE tenant_id=%s AND source_id=%s AND manifest_sha256=%s",
                    (tenant_pk, source_pk, manifest_sha256),
                )
                manifest_row = cursor.fetchone()
                if manifest_row is None or manifest_row[1:] != (
                    pipeline_version,
                    manifest_yaml,
                    package_sha256,
                ):
                    raise IngestConflictError(
                        "manifest hash already belongs to different immutable content"
                    )
            manifest_pk = int(manifest_row[0])
            cursor.execute(
                "INSERT INTO ingest_job(tenant_id,source_id,idempotency_key,stage,status,manifest_sha256,"
                "submitted_by,next_attempt_at,max_attempts) VALUES(%s,%s,%s,'queued','pending',%s,%s,now(),5) RETURNING id",
                (tenant_pk, source_pk, idempotency_key, manifest_sha256, user_pk),
            )
            job_id = int(cursor.fetchone()[0])
            cursor.execute(
                "INSERT INTO ingest_payload(tenant_id,job_id,idempotency_key,payload_hash) VALUES(%s,%s,%s,%s)",
                (tenant_pk, job_id, idempotency_key, package_sha256),
            )
            item_count = 0
            for document in documents:
                cursor.execute(
                    "INSERT INTO document(tenant_id,source_id,logical_key,title,doc_type,meta_json,status) "
                    "VALUES(%s,%s,%s,%s,%s,%s::jsonb,'pending') "
                    "ON CONFLICT(tenant_id,logical_key) DO UPDATE SET title=EXCLUDED.title,source_id=EXCLUDED.source_id "
                    "WHERE document.source_id=EXCLUDED.source_id RETURNING id",
                    (
                        tenant_pk,
                        source_pk,
                        document["document_id"],
                        document["title"],
                        document["format"],
                        json.dumps(
                            {
                                "shop_id": shop_id,
                                "disclosure_class": document["disclosure_class"],
                                "source_name": source_name,
                            },
                            ensure_ascii=False,
                        ),
                    ),
                )
                row = cursor.fetchone()
                if row is None:
                    raise IngestConflictError(
                        "document identifier is owned by another source"
                    )
                cursor.execute(
                    "INSERT INTO ingest_job_item(tenant_id,job_id,data_id,payload_hash,status,stage) "
                    "VALUES(%s,%s,%s,%s,'pending','queued') RETURNING id",
                    (
                        tenant_pk,
                        job_id,
                        document["document_id"],
                        document["source_hash"],
                    ),
                )
                cursor.fetchone()
                cursor.execute(
                    "INSERT INTO source_file(tenant_id,source_manifest_id,document_id,relative_path,media_type,"
                    "content_sha256,size_bytes,content) VALUES(%s,%s,%s,%s,%s,%s,%s,%s) "
                    "ON CONFLICT(tenant_id,source_manifest_id,relative_path) DO NOTHING "
                    "RETURNING id",
                    (
                        tenant_pk,
                        manifest_pk,
                        document["document_id"],
                        document["path"],
                        _media_type(document["path"]),
                        document["source_hash"],
                        len(files[document["path"]]),
                        files[document["path"]],
                    ),
                )
                file_row = cursor.fetchone()
                if file_row is None:
                    cursor.execute(
                        "SELECT content_sha256,size_bytes FROM source_file "
                        "WHERE tenant_id=%s AND source_manifest_id=%s AND relative_path=%s",
                        (tenant_pk, manifest_pk, document["path"]),
                    )
                    existing_file = cursor.fetchone()
                    if existing_file is None or existing_file != (
                        document["source_hash"],
                        len(files[document["path"]]),
                    ):
                        raise IngestConflictError(
                            "source file path already belongs to different immutable content"
                        )
                item_count += 1
            cursor.execute(
                "INSERT INTO ingest_event(tenant_id,actor_user_id,action,resource_type,resource_id,"
                "content_hash,reason,result,metadata_json) VALUES(%s,%s,'ingestion.queued','ingest_job',%s,%s,%s,'success',%s::jsonb)",
                (
                    tenant_pk,
                    user_pk,
                    str(job_id),
                    package_sha256,
                    "v2 Python ingestion submission",
                    json.dumps(
                        {
                            "shop_id": shop_id,
                            "source_id": source_id,
                            "manifest_sha256": manifest_sha256,
                            "document_count": item_count,
                        },
                        ensure_ascii=False,
                    ),
                ),
            )
            return SubmittedJob(
                job_id, "pending", manifest_sha256, package_sha256, item_count
            )

    def get_status(self, *, tenant_db_id: int, job_id: int, shop_id: str) -> dict[str, Any] | None:
        with (
            self.connection(row_factory=dict_row) as connection,
            connection.cursor() as cursor,
        ):
            cursor.execute(
                "SELECT j.id,j.status,j.stage,j.error_code FROM ingest_job j JOIN source s ON s.tenant_id=j.tenant_id AND s.id=j.source_id WHERE j.tenant_id=%s AND j.id=%s AND s.shop_id=%s",
                (tenant_db_id, job_id, shop_id),
            )
            return cursor.fetchone()

    def cancel(self, *, tenant_db_id: int, job_id: int) -> str | None:
        with (
            self.connection() as connection,
            connection.transaction(),
            connection.cursor() as cursor,
        ):
            cursor.execute(
                "UPDATE ingest_job SET status=CASE WHEN status='pending' THEN 'cancelled' ELSE status END, "
                "stage=CASE WHEN status='pending' THEN 'cancelled' ELSE stage END, "
                "cancel_requested=true,owner_token=CASE WHEN status='pending' THEN NULL ELSE owner_token END, "
                "lease_expires_at=CASE WHEN status='pending' THEN NULL ELSE lease_expires_at END, "
                "error_code='cancel_requested',updated_at=now() "
                "WHERE tenant_id=%s AND id=%s AND status IN ('pending','running') RETURNING status",
                (tenant_db_id, job_id),
            )
            row = cursor.fetchone()
            if row is None:
                cursor.execute(
                    "SELECT status FROM ingest_job WHERE tenant_id=%s AND id=%s",
                    (tenant_db_id, job_id),
                )
                existing = cursor.fetchone()
                return str(existing[0]) if existing is not None else None
            cursor.execute(
                "UPDATE ingest_job_item SET status=CASE WHEN status='pending' THEN 'cancelled' ELSE status END, "
                "stage=CASE WHEN status='pending' THEN 'cancelled' ELSE stage END, "
                "cancel_requested=true,lease_expires_at=CASE WHEN status='pending' THEN NULL ELSE lease_expires_at END, "
                "error_code='cancel_requested' "
                "WHERE tenant_id=%s AND job_id=%s AND status IN ('pending','running')",
                (tenant_db_id, job_id),
            )
            return str(row[0])


def _media_type(path: str) -> str:
    lowered = path.lower()
    if lowered.endswith(".csv"):
        return "text/csv"
    if lowered.endswith(".docx"):
        return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
    return "text/markdown"
