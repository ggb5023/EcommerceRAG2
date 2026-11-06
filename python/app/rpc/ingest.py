"""Authorized ingestion RPC. Business mutations stay in transactional stores."""
from __future__ import annotations

import re
import tempfile
from pathlib import Path, PurePosixPath

import grpc
import psycopg

from app.authorization import ScopeError, validate_scope
from app.ingest.catalog import (
    SIZE_CHART_SCHEMA,
    TYPED_SCHEMAS,
    limit_row_errors,
    validate_typed_document,
)
from app.ingest.commands import CommandStore
from app.ingest.database import (
    IngestAuthorizationError,
    IngestConflictError,
    IngestDatabase,
    IngestSchemaIncompleteError,
    package_payload_hash,
)
from app.ingest.package_validation import _inspect_package
from app.ingest.pipeline import chunk_elements_v2, parser_for
from rag.v1 import rag_pb2, rag_pb2_grpc

_ROW_NUMBER = re.compile(r"(?:row|line)=(\d+)")


def _precheck_parser_issue(error: ValueError, spec: dict) -> dict:
    text = str(error)
    code, _, _ = text.partition(":")
    match = _ROW_NUMBER.search(text)
    row = int(match.group(1)) if match else 1
    mapped = {
        "csv_header_empty": ("header_empty", "", "表头不能为空"),
        "csv_header_duplicate": ("duplicate_header", "", "表头不能重复"),
        "xlsx_header_empty": ("header_empty", "", "表头不能为空"),
        "xlsx_header_duplicate": ("duplicate_header", "", "表头不能重复"),
        "csv_column_mismatch": ("column_mismatch", "", "数据列数与表头不一致"),
        "xlsx_column_mismatch": ("column_mismatch", "", "数据列数与表头不一致"),
        "header_alias_conflict": ("duplicate_header", "", "别名映射后表头重复"),
        "xlsx_formula_not_allowed": ("formula_not_allowed", "", "XLSX 不允许公式"),
        "xlsx_macro_not_allowed": ("macro_not_allowed", "", "XLSX 不允许宏"),
        "xlsx_merged_cell": ("merged_cell", "", "XLSX 不允许合并单元格"),
        "xlsx_hidden_sheet": ("hidden_worksheet", "", "XLSX 不允许隐藏工作表"),
        "xlsx_row_limit": ("row_limit", "", "XLSX 行数超限"),
        "xlsx_column_limit": ("column_limit", "", "XLSX 列数超限"),
        "xlsx_cell_limit": ("cell_limit", "", "XLSX 单元格数超限"),
        "xlsx_empty_sheet": ("empty_table", "", "XLSX 没有数据行"),
        "xlsx_empty_workbook": ("empty_table", "", "XLSX 没有工作表"),
        "xlsx_invalid": ("xlsx_invalid", "", "XLSX 文件无法读取"),
        "xlsx_parser_unavailable": ("xlsx_invalid", "", "XLSX 解析器不可用"),
    }
    code, column, message = mapped.get(
        code, ("parser_error", "", "表格无法解析"),
    )
    return {
        "row": row,
        "column": column,
        "code": code,
        "severity": "block",
        "value": "",
        "message": message,
        "suggested_action": "按模板修正后重新导入",
    }


def _precheck_document(spec: dict, content: bytes, tenant_id: str, shop_id: str,
                       known_size_charts: set[tuple[str, str]],
                       ) -> tuple[int, list[dict], str, list[dict]]:
    schema = str(spec.get("table_schema") or "")
    if schema not in TYPED_SCHEMAS:
        return 0, [], "", []
    metadata = {
        "tenant_id": tenant_id,
        "shop_id": shop_id,
        "title": spec["title"],
        "doc_type": spec.get("doc_type") or spec["format"],
        "table_schema": schema,
        "disclosure_class": spec["disclosure_class"],
        "effective_from": spec["effective_from"] or None,
        "effective_to": spec["effective_to"] or None,
        "source_hash": spec["source_hash"],
    }
    with tempfile.TemporaryDirectory(prefix="ecr-precheck-") as root:
        target = Path(root) / PurePosixPath(spec["path"])
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(content)
        try:
            elements = parser_for(spec["format"]).parse(
                target,
                document_id=spec["document_id"],
                version_id=spec["version_id"],
                metadata=metadata,
            )
            chunks = chunk_elements_v2(elements)
        except (OSError, UnicodeError, ValueError, KeyError, RuntimeError) as error:
            return 0, [_precheck_parser_issue(error, spec)], schema, []
    _rows, charts, issues = validate_typed_document(
        elements, chunks, spec, known_size_charts=known_size_charts,
    )
    return len(elements), issues, schema, charts


class IngestService(rag_pb2_grpc.IngestServiceServicer):
    def __init__(self, database: IngestDatabase, *, scope_validator=validate_scope, artifact_store=None):
        self.database = database
        self.commands = CommandStore(database.conn_string, artifact_store=artifact_store, connect=database.connector)
        self.scope_validator = scope_validator

    def _authorize(self, scope, context, allowed_audiences=frozenset({"ingest"})):
        if scope.scope_audience not in allowed_audiences:
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

    def _submission_shop(self, request, context):
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
        return shop_id

    @staticmethod
    def _validate_identity(request, context) -> None:
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

    @staticmethod
    def _identity_pks(request, context) -> tuple[int, int]:
        try:
            tenant_pk = int(request.tenant_db_id or str(request.context.tenant_db_id))
            user_pk = int(request.user_db_id or str(request.context.user_db_id))
        except ValueError:
            context.abort(
                grpc.StatusCode.INVALID_ARGUMENT, "database identity is invalid"
            )
        return tenant_pk, user_pk

    def _submit_package(self, request, context, shop_id: str) -> rag_pb2.SubmitResponse:
        adoption = request.context.scope_audience == "ingest_adoption"
        package = _inspect_package(self._submission_request(request), context)
        tenant_pk, user_pk = self._identity_pks(request, context)
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
                required_admin_role=None if adoption else "merchant_admin",
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

    def ValidatePackage(self, request, context):
        """Return document-level validation plus typed-row precheck issues.

        This path is read-only: it never writes a document, version or chunk.
        Go calls it before creating an import batch, then stores the returned
        row issues in import_item.precheck_detail.
        """
        package = _inspect_package(request, context)
        documents = package["documents"]
        prepared: dict[int, tuple[int, list[dict], str, list[dict]]] = {}
        known_size_charts: set[tuple[str, str]] = set()
        # Parse size charts first so product rows can be checked against the
        # same package's charts.  Parsing happens once per document.
        for index, spec in enumerate(documents):
            if spec.get("table_schema") != SIZE_CHART_SCHEMA:
                continue
            result = _precheck_document(
                spec, package["uploaded"][spec["path"]],
                request.tenant_id, request.shop_id, set(),
            )
            prepared[index] = result
            known_size_charts.update(
                (row["chart_code"], row["size_label"]) for row in result[3]
            )
        for index, spec in enumerate(documents):
            if index in prepared:
                continue
            prepared[index] = _precheck_document(
                spec, package["uploaded"][spec["path"]],
                request.tenant_id, request.shop_id, known_size_charts,
            )
        response_documents = []
        for index, spec in enumerate(documents):
            row_count, issues, schema, _charts = prepared[index]
            limited, error_count, truncated = limit_row_errors(issues)
            response_documents.append(rag_pb2.ValidatedPackageDocument(
                document_id=spec["document_id"],
                title=spec["title"],
                format=spec["format"],
                path=spec["path"],
                source_hash=spec["source_hash"],
                disclosure_class=spec["disclosure_class"],
                effective_from=spec["effective_from"],
                effective_to=spec["effective_to"],
                version_id=spec["version_id"],
                table_schema=schema,
                row_count=row_count,
                error_count=error_count,
                row_errors=[rag_pb2.ValidatedPackageRowError(**issue) for issue in limited],
                truncated=truncated,
            ))
        return rag_pb2.ValidatePackageResponse(
            source_id=package["source_id"],
            source_name=package["source_name"],
            pipeline_version=package["pipeline_version"],
            manifest_sha256=package["manifest_sha256"],
            documents=response_documents,
        )

    def Submit(self, request, context):
        self._authorize(request.context, context, frozenset({"ingest", "ingest_adoption"}))
        if self.database is None:
            context.abort(
                grpc.StatusCode.UNAVAILABLE, "ingestion persistence is not configured"
            )
        shop_id = self._submission_shop(request, context)
        self._validate_identity(request, context)
        if not request.idempotency_key:
            context.abort(
                grpc.StatusCode.INVALID_ARGUMENT, "idempotency key is required"
            )
        return self._submit_package(request, context, shop_id)

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
