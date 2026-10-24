"""Transactional, idempotent ingestion commands. Only Python mutates knowledge."""
from __future__ import annotations

import hashlib
import json
from typing import Any

import psycopg
from psycopg.rows import dict_row

from app.ingest.database import IngestAuthorizationError, IngestConflictError
from rag.v1 import rag_pb2


def payload_digest(request: Any) -> str:
    unsigned = type(request)()
    unsigned.CopyFrom(request)
    unsigned.ClearField("context")
    return hashlib.sha256(unsigned.SerializeToString(deterministic=True)).hexdigest()


def command_action(value: int) -> str:
    return {
        rag_pb2.INGEST_COMMAND_ACTION_REVIEW: "version.review",
        rag_pb2.INGEST_COMMAND_ACTION_PUBLISH: "version.publish",
        rag_pb2.INGEST_COMMAND_ACTION_ROLLBACK: "version.rollback",
        rag_pb2.INGEST_COMMAND_ACTION_REVOKE: "version.revoke",
        rag_pb2.INGEST_COMMAND_ACTION_RETRY: "ingestion.retry",
        rag_pb2.INGEST_COMMAND_ACTION_CANCEL: "ingestion.cancel",
    }.get(value, "")


class CommandStore:
    def __init__(self, conn_string: str, *, artifact_store=None, connect=psycopg.connect):
        self.conn_string = conn_string
        self.artifact_store = artifact_store
        self._connect = connect

    @staticmethod
    def _identity(cursor: Any, scope: Any, action: str) -> None:
        role = "knowledge_reviewer" if action.startswith("version.") else "merchant_admin"
        cursor.execute("SELECT ecr_lock_ingest_scope(%s,%s,%s,%s,%s,%s,%s,%s)",
                       (scope.tenant_db_id, scope.user_db_id, scope.tenant_id, scope.user_id,
                        scope.shop_id, scope.role, scope.permission_revision, role))
        if cursor.fetchone()["ecr_lock_ingest_scope"] is not True:
            raise IngestAuthorizationError("command authorization changed")

    def execute(self, request: Any) -> rag_pb2.IngestCommandResult:
        action = command_action(request.action)
        scope = request.context
        digest = payload_digest(request)
        if (not action or not request.command_id or len(request.command_id) > 200
                or not request.resource_id.isdigit() or int(request.resource_id) <= 0
                or request.expected_fencing_epoch < 0 or not 10 <= len(request.reason.strip()) <= 2000):
            raise ValueError("invalid ingestion command")
        if (scope.scope_audience != "ingest" or scope.command_id != request.command_id
                or scope.command_action != action or scope.command_payload_sha256 != digest):
            raise IngestAuthorizationError("command is not bound to its signed payload")
        with (self._connect(self.conn_string, row_factory=dict_row) as connection,
              connection.transaction(), connection.cursor() as cursor):
            cursor.execute("SELECT pg_advisory_xact_lock(hashtext(%s)::bigint)",
                           (f"knowledge:{scope.tenant_db_id}",))
            self._identity(cursor, scope, action)
            cursor.execute("SELECT pg_advisory_xact_lock(hashtext(%s)::bigint)",
                           (f"command:{scope.tenant_db_id}:{request.command_id}",))
            cursor.execute("SELECT * FROM ingest_command_result WHERE tenant_id=%s AND command_id=%s",
                           (scope.tenant_db_id, request.command_id))
            old = cursor.fetchone()
            if old is not None:
                if (old["payload_sha256"] != digest or old["actor_user_id"] != scope.user_db_id
                        or old["shop_id"] != scope.shop_id or old["action"] != action):
                    raise IngestConflictError("command id was reused with different content")
                return rag_pb2.IngestCommandResult(**old["result_json"], idempotent=True)
            try:
                with connection.transaction():
                    if action.startswith("version."):
                        result = self._version(cursor, request, action)
                    else:
                        result = self._job(cursor, request, action)
            except IngestConflictError as error:
                result = {"status": "rejected", "error_code": "precondition_failed", "error_message": str(error)}
            result.update(command_id=request.command_id, resource_id=request.resource_id)
            cursor.execute(
                "INSERT INTO ingest_command_result(tenant_id,command_id,actor_user_id,shop_id,action,"
                "payload_sha256,result_json) VALUES(%s,%s,%s,%s,%s,%s,%s::jsonb)",
                (scope.tenant_db_id, request.command_id, scope.user_db_id, scope.shop_id,
                 action, digest, json.dumps(result)),
            )
            return rag_pb2.IngestCommandResult(**result)

    def get(self, scope: Any, command_id: str) -> rag_pb2.IngestCommandResult | None:
        with (self._connect(self.conn_string, row_factory=dict_row) as connection,
              connection.cursor() as cursor):
            cursor.execute("SELECT * FROM ingest_command_result WHERE tenant_id=%s AND command_id=%s",
                           (scope.tenant_db_id, command_id))
            row = cursor.fetchone()
            if row is None:
                return None
            if (row["shop_id"] != scope.shop_id or row["actor_user_id"] != scope.user_db_id
                    or scope.command_id != command_id or scope.command_action != row["action"]
                    or scope.command_payload_sha256 != row["payload_sha256"]):
                raise IngestAuthorizationError("command result is outside the scope")
            self._identity(cursor, scope, row["action"])
            return rag_pb2.IngestCommandResult(**row["result_json"], idempotent=True)

    def _version(self, cursor: Any, request: Any, action: str) -> dict[str, Any]:
        scope = request.context
        cursor.execute(
            "SELECT v.*,d.active_version_id,d.logical_key,s.shop_id FROM document_version v "
            "JOIN document d ON d.tenant_id=v.tenant_id AND d.id=v.document_id "
            "JOIN source s ON s.tenant_id=d.tenant_id AND s.id=d.source_id "
            "WHERE v.tenant_id=%s AND v.id=%s AND s.shop_id=%s FOR UPDATE OF d,v",
            (scope.tenant_db_id, int(request.resource_id), scope.shop_id),
        )
        row = cursor.fetchone()
        if (row is None or row["source_hash"] != request.expected_hash
                or row["fencing_epoch"] != request.expected_fencing_epoch):
            raise IngestConflictError("version hash or fencing epoch changed")
        tenant, version = scope.tenant_db_id, int(request.resource_id)
        if action == "version.review":
            if (row["status"] != "ready" or row["review_status"] != "pending"
                    or request.review_decision not in {"approved", "rejected"}
                    or request.disclosure_class not in {"external_allowed", "internal_only", "unclassified"}
                    or request.external_allowed != (request.disclosure_class == "external_allowed")
                    or (request.review_decision == "approved" and request.disclosure_class == "unclassified")):
                raise IngestConflictError("version is not reviewable")
            cursor.execute(
                "UPDATE document_version SET review_status=%s,reviewed_hash=source_hash,reviewed_by=%s,"
                "reviewed_at=now(),review_reason=%s,disclosure_class=%s,external_allowed=%s,"
                "quality_json=quality_json || jsonb_build_object('review',%s::text) WHERE tenant_id=%s AND id=%s",
                (request.review_decision, scope.user_db_id, request.reason, request.disclosure_class,
                 request.external_allowed, request.review_decision, tenant, version),
            )
            cursor.execute("UPDATE ingest_job_item SET stage='reviewed' WHERE tenant_id=%s AND document_version_id=%s",
                           (tenant, version))
            return {"status": row["status"], "review_status": request.review_decision,
                    "reviewed_hash": row["source_hash"], "fencing_epoch": row["fencing_epoch"]}
        cursor.execute(
            "SELECT i.*,j.cancel_requested AS job_cancel FROM ingest_job_item i "
            "JOIN ingest_job j ON j.tenant_id=i.tenant_id AND j.id=i.job_id "
            "WHERE i.tenant_id=%s AND i.document_version_id=%s FOR UPDATE OF i,j",
            (tenant, version),
        )
        item = cursor.fetchone()
        if item is None or item["cancel_requested"] or item["job_cancel"]:
            raise IngestConflictError("version task was cancelled")
        if action == "version.revoke":
            if row["status"] not in {"active", "superseded", "ready"}:
                raise IngestConflictError("version is not revocable")
            cursor.execute("UPDATE document SET active_version_id=NULL,status='pending' "
                           "WHERE tenant_id=%s AND id=%s AND active_version_id=%s",
                           (tenant, row["document_id"], version))
            cursor.execute("UPDATE document_version SET status='revoked',fencing_epoch=fencing_epoch+1 "
                           "WHERE tenant_id=%s AND id=%s", (tenant, version))
            return {"status": "revoked", "fencing_epoch": row["fencing_epoch"] + 1}
        if (row["review_status"] != "approved" or row["reviewed_hash"] != row["source_hash"]
                or row["external_allowed"] != (row["disclosure_class"] == "external_allowed")):
            raise IngestConflictError("version is not approved")
        expected_status = "ready" if action == "version.publish" else "superseded"
        if row["status"] != expected_status:
            raise IngestConflictError("version state changed")
        quality = row["quality_json"]
        self._verify_artifacts(cursor, row, scope.shop_id)
        cursor.execute("SELECT count(*) AS total,count(embedding) AS embedded,count(*) FILTER(WHERE embedding_model=%s) AS models FROM chunk WHERE tenant_id=%s AND version_id=%s",
                       (row["embedding_model"], tenant, version))
        chunks = cursor.fetchone()
        if (quality.get("quality_gate") != "passed" or quality.get("embedding_status") != "passed"
                or chunks["total"] <= 0 or chunks["total"] != row["chunk_count"]
                or chunks["embedded"] != chunks["total"] or chunks["models"] != chunks["total"]):
            raise IngestConflictError("version quality gate failed")
        cursor.execute("UPDATE document_version SET status='superseded' WHERE tenant_id=%s AND id=%s AND status='active'",
                       (tenant, row["active_version_id"]))
        cursor.execute("UPDATE document_version SET status='active',activated_at=now(),fencing_epoch=fencing_epoch+1 "
                       "WHERE tenant_id=%s AND id=%s", (tenant, version))
        cursor.execute("UPDATE document SET active_version_id=%s,status='active',meta_json=meta_json || "
                       "jsonb_build_object('disclosure_class',%s::text,'external_allowed',%s::boolean) "
                       "WHERE tenant_id=%s AND id=%s",
                       (version, row["disclosure_class"], row["external_allowed"], tenant, row["document_id"]))
        if action == "version.publish":
            if item["status"] != "awaiting_review":
                raise IngestConflictError("publication task state changed")
            epoch = item["fencing_epoch"] + 1
            cursor.execute("UPDATE ingest_job_item SET status='running',stage='publishing',owner_token=%s,"
                           "fencing_epoch=%s,lease_expires_at=now()+interval '1 minute' WHERE tenant_id=%s AND id=%s",
                           (f"command:{request.command_id}", epoch, tenant, item["id"]))
            cursor.execute("INSERT INTO ingest_publication(tenant_id,item_id,fencing_epoch,document_version_id) VALUES(%s,%s,%s,%s)",
                           (tenant, item["id"], epoch, version))
            cursor.execute("UPDATE ingest_job SET status='done',stage='published',updated_at=now() "
                           "WHERE tenant_id=%s AND id=%s AND NOT EXISTS(SELECT 1 FROM ingest_job_item "
                           "WHERE tenant_id=%s AND job_id=%s AND status<>'done')",
                           (tenant, item["job_id"], tenant, item["job_id"]))
        return {"status": "active", "review_status": "approved", "reviewed_hash": row["source_hash"],
                "fencing_epoch": row["fencing_epoch"] + 1}

    def _verify_artifacts(self, cursor, version, shop):
        cursor.execute("SELECT sf.content FROM source_file sf JOIN document d ON d.tenant_id=sf.tenant_id AND d.logical_key=sf.document_id "
                       "WHERE sf.tenant_id=%s AND sf.source_manifest_id=%s AND d.id=%s",
                       (version["tenant_id"], version["source_manifest_id"], version["document_id"]))
        source = cursor.fetchone()
        bundle = version["quality_json"].get("artifact_bundle", {})
        if source is None or hashlib.sha256(bytes(source["content"])).hexdigest() != version["source_hash"]:
            raise IngestConflictError("immutable source hash changed")
        if self.artifact_store is None:
            raise RuntimeError("artifact verification is not configured")
        try:
            manifest = bundle["manifest"]
            data, _ = self.artifact_store.get_bytes(manifest["object_key"], expected_sha256=manifest["sha256"])
            body = json.loads(data)
            if (body["shop_id"] != shop or body["artifacts"] != bundle["artifacts"]
                    or body["artifact_set_sha256"] != bundle["artifact_set_sha256"]):
                raise ValueError("artifact binding changed")
            for artifact in bundle["artifacts"]:
                _, metadata = self.artifact_store.get_bytes(artifact["object_key"], expected_sha256=artifact["sha256"])
                if metadata.size_bytes != artifact["size_bytes"]:
                    raise ValueError("artifact size changed")
                if artifact["artifact_type"] == "raw" and artifact["sha256"] != version["source_hash"]:
                    raise ValueError("raw source hash changed")
        except (KeyError, ValueError, OSError, RuntimeError):
            raise IngestConflictError("artifact integrity gate failed") from None

    @staticmethod
    def _job(cursor: Any, request: Any, action: str) -> dict[str, Any]:
        scope = request.context
        tenant, job = scope.tenant_db_id, int(request.resource_id)
        cursor.execute("SELECT j.* FROM ingest_job j JOIN source s ON s.tenant_id=j.tenant_id AND s.id=j.source_id "
                       "WHERE j.tenant_id=%s AND j.id=%s AND s.shop_id=%s FOR UPDATE OF j",
                       (tenant, job, scope.shop_id))
        row = cursor.fetchone()
        if row is None or row["fencing_epoch"] != request.expected_fencing_epoch:
            raise IngestConflictError("job fencing epoch changed")
        if action == "ingestion.retry":
            if row["status"] != "failed":
                raise IngestConflictError("job is not retryable")
            cursor.execute("UPDATE ingest_job SET status='pending',stage='queued',cancel_requested=false,owner_token=NULL,"
                           "lease_expires_at=NULL,next_attempt_at=now(),error=NULL,error_code=NULL WHERE tenant_id=%s AND id=%s",
                           (tenant, job))
            cursor.execute("UPDATE ingest_job_item SET status='pending',stage='queued',cancel_requested=false,"
                           "owner_token=NULL,lease_expires_at=NULL,next_attempt_at=now(),error=NULL,error_code=NULL "
                           "WHERE tenant_id=%s AND job_id=%s AND status IN ('failed','cancelled')", (tenant, job))
            return {"status": "pending", "fencing_epoch": row["fencing_epoch"]}
        if row["status"] in {"done", "failed", "cancelled"}:
            return {"status": row["status"], "fencing_epoch": row["fencing_epoch"]}
        cursor.execute("UPDATE ingest_job SET cancel_requested=true,status=CASE WHEN status='running' THEN status ELSE 'cancelled' END,"
                       "stage='cancel_requested',updated_at=now() WHERE tenant_id=%s AND id=%s", (tenant, job))
        cursor.execute("UPDATE ingest_job_item SET cancel_requested=true,status=CASE WHEN status='running' THEN status ELSE 'cancelled' END "
                       "WHERE tenant_id=%s AND job_id=%s AND status<>'done'", (tenant, job))
        return {"status": "running" if row["status"] == "running" else "cancelled",
                "fencing_epoch": row["fencing_epoch"]}
