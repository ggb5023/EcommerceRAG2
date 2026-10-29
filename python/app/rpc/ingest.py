"""Authorized ingestion RPC. Business mutations stay in transactional stores."""
from __future__ import annotations

import grpc
import psycopg

from app.authorization import ScopeError, validate_scope
from app.ingest.commands import CommandStore
from app.ingest.database import (
    IngestAuthorizationError,
    IngestConflictError,
    IngestDatabase,
    IngestSchemaIncompleteError,
    package_payload_hash,
)
from app.ingest.package_validation import _inspect_package
from rag.v1 import rag_pb2, rag_pb2_grpc


class IngestService(rag_pb2_grpc.IngestServiceServicer):
    def __init__(self, database: IngestDatabase, *, scope_validator=validate_scope, artifact_store=None):
        self.database = database
        self.commands = CommandStore(database.conn_string, artifact_store=artifact_store, connect=database.connector)
        self.scope_validator = scope_validator

    def _authorize(self, scope, context):
        if scope.scope_audience != "ingest":
            context.abort(grpc.StatusCode.PERMISSION_DENIED, "invalid scope audience")
        try:
            self.scope_validator(scope)
        except ScopeError as error:
            context.abort(error.code, "authorization scope could not be verified")

    @staticmethod
    def _status(value: str) -> int:
        return {
            "pending": rag_pb2.JOB_STATUS_PENDING,
            "running": rag_pb2.JOB_STATUS_RUNNING,
            "awaiting_review": rag_pb2.JOB_STATUS_AWAITING_REVIEW,
            "done": rag_pb2.JOB_STATUS_DONE,
            "failed": rag_pb2.JOB_STATUS_FAILED,
            "cancelled": rag_pb2.JOB_STATUS_CANCELLED,
        }.get(value, rag_pb2.JOB_STATUS_UNSPECIFIED)

    @staticmethod
    def _submission_request(request):
        context = request.context
        return rag_pb2.ValidatePackageRequest(
            manifest_yaml=request.manifest_yaml,
            files=list(request.files),
            tenant_id=context.tenant_id,
            shop_id=request.shop_id or context.shop_id,
        )

    def Submit(self, request, context):
        self._authorize(request.context, context)
        if self.database is None:
            context.abort(
                grpc.StatusCode.UNAVAILABLE, "ingestion persistence is not configured"
            )
        context_shop = request.context.shop_id
        shop_id = request.shop_id or context_shop
        if (
            request.shop_id
            and context_shop
            and request.shop_id != context_shop
        ):
            context.abort(
                grpc.StatusCode.PERMISSION_DENIED,
                "submission shop is outside the signed scope",
            )
        if not shop_id or shop_id not in set(request.context.allowed_shop_ids):
            context.abort(
                grpc.StatusCode.PERMISSION_DENIED,
                "submission shop is outside the signed scope",
            )
        try:
            if request.tenant_db_id and int(request.tenant_db_id) != request.context.tenant_db_id:
                context.abort(
                    grpc.StatusCode.PERMISSION_DENIED,
                    "submission tenant identity does not match the signed scope",
                )
            if request.user_db_id and int(request.user_db_id) != request.context.user_db_id:
                context.abort(
                    grpc.StatusCode.PERMISSION_DENIED,
                    "submission user identity does not match the signed scope",
                )
        except ValueError:
            context.abort(
                grpc.StatusCode.INVALID_ARGUMENT, "database identity is invalid"
            )
        if not request.idempotency_key:
            context.abort(
                grpc.StatusCode.INVALID_ARGUMENT, "idempotency key is required"
            )
        package = _inspect_package(self._submission_request(request), context)
        try:
            tenant_pk = int(request.tenant_db_id or str(request.context.tenant_db_id))
            user_pk = int(request.user_db_id or str(request.context.user_db_id))
        except ValueError:
            context.abort(
                grpc.StatusCode.INVALID_ARGUMENT, "database identity is invalid"
            )
        package_sha = package_payload_hash(
            tenant_pk, shop_id, package["manifest_sha256"], package["uploaded"]
        )
        try:
            result = self.database.submit(
                tenant_id=request.context.tenant_id,
                user_id=request.context.user_id,
                shop_id=shop_id,
                tenant_db_id=str(tenant_pk),
                user_db_id=str(user_pk),
                permission_revision=request.context.permission_revision,
                idempotency_key=request.idempotency_key,
                source_id=package["source_id"],
                source_name=package["source_name"],
                pipeline_version=package["pipeline_version"],
                manifest_yaml=request.manifest_yaml,
                manifest_sha256=package["manifest_sha256"],
                package_sha256=package_sha,
                documents=package["documents"],
                files=package["uploaded"],
            )
        except IngestConflictError as error:
            context.abort(grpc.StatusCode.ALREADY_EXISTS, str(error))
        except IngestSchemaIncompleteError:
            context.abort(grpc.StatusCode.FAILED_PRECONDITION, "ingest schema incomplete")
        except IngestAuthorizationError as error:
            context.abort(grpc.StatusCode.PERMISSION_DENIED, str(error))
        except ValueError as error:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, str(error))
        except grpc.RpcError:
            raise
        except (psycopg.Error, OSError, RuntimeError, TypeError):
            context.abort(grpc.StatusCode.UNAVAILABLE, "ingestion persistence failed")
        return rag_pb2.SubmitResponse(
            job_id=str(result.job_id),
            status=self._status(result.status),
            manifest_sha256=result.manifest_sha256,
            package_sha256=result.package_sha256,
            item_count=result.item_count,
        )

    def GetStatus(self, request, context):
        self._authorize(request.context, context)
        if self.database is None:
            context.abort(
                grpc.StatusCode.UNAVAILABLE, "ingestion persistence is not configured"
            )
        try:
            tenant_pk = int(request.context.tenant_db_id)
            job_id = int(request.job_id)
        except ValueError:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, "job identity is invalid")
        try:
            row = self.database.get_status(tenant_db_id=tenant_pk, job_id=job_id, shop_id=request.context.shop_id)
        except (psycopg.Error, OSError, RuntimeError, TypeError, ValueError):
            context.abort(
                grpc.StatusCode.UNAVAILABLE, "ingestion status is unavailable"
            )
        if row is None:
            context.abort(grpc.StatusCode.NOT_FOUND, "ingestion job was not found")
        return rag_pb2.GetStatusResponse(
            job_id=str(row["id"]),
            status=self._status(str(row["status"])),
            stage=str(row["stage"] or ""),
            error_code=str(row["error_code"] or ""),
        )

    def ExecuteCommand(self, request, context):
        self._authorize(request.context, context)
        try:
            result = self.commands.execute(request)
        except IngestConflictError as error:
            context.abort(grpc.StatusCode.ALREADY_EXISTS, str(error))
        except IngestSchemaIncompleteError:
            context.abort(grpc.StatusCode.FAILED_PRECONDITION, "ingest schema incomplete")
        except IngestAuthorizationError as error:
            context.abort(grpc.StatusCode.PERMISSION_DENIED, str(error))
        except ValueError as error:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, str(error))
        except psycopg.Error:
            context.abort(grpc.StatusCode.UNAVAILABLE, "command outcome requires reconciliation")
        return rag_pb2.ExecuteCommandResponse(result=result)

    def GetCommandResult(self, request, context):
        self._authorize(request.context, context)
        if request.context.command_id != request.command_id:
            context.abort(grpc.StatusCode.PERMISSION_DENIED, "command scope mismatch")
        try:
            result = self.commands.get(request.context, request.command_id)
        except IngestAuthorizationError as error:
            context.abort(grpc.StatusCode.PERMISSION_DENIED, str(error))
        except psycopg.Error:
            context.abort(grpc.StatusCode.UNAVAILABLE, "command result is unavailable")
        if result is None:
            context.abort(grpc.StatusCode.NOT_FOUND, "command result is not recorded")
        return rag_pb2.GetCommandResultResponse(result=result)
