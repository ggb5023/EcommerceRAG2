"""V2 LangGraph gRPC adapter with explicit Provider and scope contracts."""
from __future__ import annotations

import asyncio
import hashlib
import json
import logging
import math
import os
from typing import Any

import grpc

from app.authorization import ScopeError, validate_scope
from app.graph.runtime import (
    build_mock_provider_service,
    load_checkpoint_store,
    load_provider_service,
)
from app.graph.service import LangGraphRagService
from app.providers.contracts import ProviderError
from rag.v1 import rag_pb2, rag_pb2_grpc


def _valid_scope(ctx: Any) -> bool:
    shops = set(ctx.allowed_shop_ids)
    if not ctx.request_id or not ctx.tenant_id or (not ctx.user_id) or (not ctx.permission_revision) or (not ctx.enforce_document_scope):
        return False
    if not shops or ctx.role not in {'owner', 'admin', 'operator', 'viewer', 'customer'}:
        return False
    if ctx.role == 'customer' and (ctx.subject_type != 'customer' or not ctx.customer_id
            or not ctx.customer_session_id or not ctx.conversation_epoch or len(shops) != 1 or ctx.all_shops):
        return False
    if ctx.shop_id and ctx.shop_id not in shops:
        return False
    if ctx.all_shops:
        return ctx.role in {'owner', 'admin'}
    return True

class _GrpcCancelEvent:
    """Small adapter that lets async Providers observe gRPC cancellation."""

    def __init__(self, context: Any) -> None:
        self._context = context

    def is_set(self) -> bool:
        checker = getattr(self._context, 'is_active', None)
        return bool(checker and (not checker()))

def _grpc_timeout(context: Any) -> float | None:
    remaining = getattr(context, 'time_remaining', None)
    if not callable(remaining):
        return None
    try:
        value = remaining()
    except (AttributeError, RuntimeError, TypeError):
        return None
    if value is None or not isinstance(value, (int, float)) or (not math.isfinite(value)):
        return None
    return max(0.0, float(value))

def _grpc_cancel_code(context: Any) -> grpc.StatusCode:
    timeout = _grpc_timeout(context)
    return grpc.StatusCode.DEADLINE_EXCEEDED if timeout is not None and timeout <= 0 else grpc.StatusCode.CANCELLED

def _ensure_grpc_active(context: Any) -> None:
    checker = getattr(context, 'is_active', None)
    if callable(checker) and (not checker()):
        context.abort(_grpc_cancel_code(context), 'request cancelled')

def _abort_v2_error(context: Any, error: Exception, *, stage: str = 'unknown', request_id: str = '') -> None:
    # Allowlisted classifications only: arbitrary exception strings can include
    # user content, provider responses or connection secrets.
    reasons = {
        'provider generation evidence limits are invalid': 'evidence_limits_invalid',
        'provider generation delivery capability limit is invalid': 'delivery_limit_invalid',
        'provider generation applicability dimensions are invalid': 'applicability_invalid',
        'provider generation applicability dimensions lack cited support': 'applicability_uncited',
        'provider generation reason evidence is invalid': 'reason_evidence_invalid',
        'provider generation evidence answer has no citation': 'citation_missing',
        'provider generation citation is outside supplied evidence': 'citation_outside_scope',
        'provider generation citation IDs are invalid': 'citation_invalid',
    }
    reason = reasons.get(str(error), 'execution_rejected')
    logging.getLogger(__name__).warning('v2_execution_rejected stage=%s reason=%s request_sha256=%s',
        stage if stage in {'understand', 'search', 'generate'} else 'unknown', reason,
        hashlib.sha256(request_id.encode()).hexdigest())
    if isinstance(error, PermissionError):
        context.abort(grpc.StatusCode.PERMISSION_DENIED, str(error))
    if isinstance(error, (ValueError, TypeError)):
        context.abort(grpc.StatusCode.INVALID_ARGUMENT, str(error))
    if isinstance(error, RuntimeError) and str(error) == 'request cancelled':
        context.abort(_grpc_cancel_code(context), 'request cancelled')
    if isinstance(error, ProviderError):
        code = {'cancelled': _grpc_cancel_code(context), 'timeout': grpc.StatusCode.DEADLINE_EXCEEDED, 'rate_limited': grpc.StatusCode.RESOURCE_EXHAUSTED, 'allocation_quota': grpc.StatusCode.RESOURCE_EXHAUSTED, 'authentication': grpc.StatusCode.UNAUTHENTICATED, 'invalid_input': grpc.StatusCode.INVALID_ARGUMENT, 'schema_error': grpc.StatusCode.FAILED_PRECONDITION}.get(error.code, grpc.StatusCode.UNAVAILABLE if error.retryable else grpc.StatusCode.INTERNAL)
        context.abort(code, error.code)
    context.abort(grpc.StatusCode.INTERNAL, 'v2 provider execution failed')

class RagService(rag_pb2_grpc.RagServiceServicer):

    def __init__(self, *, profile=None, scope_validator=None, checkpoint_store=None, connections=None):
        self.profile = profile or os.environ.get('RAG_PROFILE', '')
        if self.profile not in {'v2_langchain_mock', 'v2_langchain_bailian', 'v2_langgraph_fixture'}:
            raise ValueError('an explicit V2 RAG_PROFILE is required')
        self._scope_validator = scope_validator or validate_scope
        self._checkpoint_store = checkpoint_store if checkpoint_store is not None else load_checkpoint_store(connections)
        self._v2 = LangGraphRagService(checkpoint_store=self._checkpoint_store)
        if self.profile == 'v2_langchain_mock':
            self._provider_v2 = build_mock_provider_service(checkpoint_store=self._checkpoint_store, connections=connections)
        elif self.profile == 'v2_langchain_bailian':
            self._provider_v2 = load_provider_service(os.environ.get('V2_PROVIDER_CONFIG', '/etc/ecommerce-rag/providers.env'), checkpoint_store=self._checkpoint_store, connections=connections)
        else:
            self._provider_v2 = None

    @staticmethod
    def _v2_scope(scope) -> dict[str, Any]:
        return {'request_id': scope.request_id, 'tenant_id': scope.tenant_id, 'tenant_db_id': str(scope.tenant_db_id), 'user_id': scope.user_id, 'allowed_shop_ids': list(scope.allowed_shop_ids), 'allowed_document_ids': list(scope.allowed_document_ids), 'shop_id': scope.shop_id, 'role': scope.role, 'permission_revision': scope.permission_revision, 'conversation_id': scope.conversation_id, 'turn_id': scope.turn_id, 'execution_id': scope.execution_id, 'subject_type': scope.subject_type, 'customer_id': scope.customer_id, 'customer_session_id': scope.customer_session_id, 'conversation_epoch': scope.conversation_epoch}

    def _v2_evidence(self, item: dict[str, Any]) -> rag_pb2.Evidence:
        return rag_pb2.Evidence(id=item['id'], content=item['content'], source_type=item['source_type'], source_ref=item['source_ref'], document_id=item['document_id'], version_id=item['version_id'], rank=int(item.get('rank', 0)), raw_score=float(item.get('raw_score', 0.0)), shop_id=item['shop_id'], disclosure_class=item['disclosure_class'], customer_eligible=bool(item['customer_eligible']), tenant_id=item['tenant_id'])

    @staticmethod
    def _v2_input_evidence(item: Any) -> dict[str, Any]:
        return {'id': item.id, 'content': item.content, 'source_type': item.source_type, 'source_ref': item.source_ref, 'document_id': item.document_id, 'version_id': item.version_id, 'tenant_id': item.tenant_id, 'shop_id': item.shop_id, 'disclosure_class': item.disclosure_class, 'customer_eligible': item.customer_eligible, 'rank': int(item.rank), 'raw_score': float(item.raw_score), 'rrf_score': 0.0}

    def _v2_is_mock(self) -> bool:
        return self.profile != 'v2_langchain_bailian'

    def _v2_checkpoint_scope(self, request, context) -> dict[str, Any]:
        """Validate the signed identity required by an ASKING checkpoint."""
        self._authorize(request.context, context)
        if request.pending_kind not in {'', 'ASKING'}:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, 'unsupported checkpoint kind')
        if not request.context.conversation_id or not request.context.turn_id or (not request.context.execution_id):
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, 'checkpoint execution identity is incomplete')
        _ensure_grpc_active(context)
        return self._v2_scope(request.context)

    @staticmethod
    def _checkpoint_response(record, response_type):
        return response_type(accepted=True, thread_id=record.thread_id, pending_kind=record.pending_kind, expires_at=record.expires_at)

    def _authorize(self, scope, context):
        if scope.scope_audience != 'ai':
            context.abort(grpc.StatusCode.PERMISSION_DENIED, 'invalid scope audience')
        if not _valid_scope(scope):
            context.abort(grpc.StatusCode.PERMISSION_DENIED, 'authorization scope is invalid')
        try:
            self._scope_validator(scope)
        except ScopeError as error:
            context.abort(error.code, 'authorization scope could not be verified')

    def Understand(self, request, context):
        query = request.query.strip()
        self._authorize(request.context, context)
        if not query:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, 'query is required')
        try:
            _ensure_grpc_active(context)
            service = self._provider_v2 or self._v2
            result = asyncio.run(service.arun(query=query, scope=self._v2_scope(request.context), history_summary=request.history_summary, response_purpose='internal', cancel_event=_GrpcCancelEvent(context), timeout_s=_grpc_timeout(context), phase='understand')) if self._provider_v2 else service.run(query=query, scope=self._v2_scope(request.context), history_summary=request.history_summary, response_purpose='internal', phase='understand')
        except (PermissionError, ValueError, TypeError, RuntimeError, ProviderError) as error:
            _abort_v2_error(context, error, stage='understand', request_id=request.context.request_id)
        _ensure_grpc_active(context)
        entities = [rag_pb2.EntitySlot(name=item['name'], value=item['value'], confidence=float(item['confidence'])) for item in result.get('entities', [])]
        return rag_pb2.UnderstandResponse(rewritten_query=result.get('rewritten_query', query), intent=result.get('intent', 'knowledge'), information_source=result.get('information_source', 'own_knowledge'), required_fact_tools=result.get('required_fact_tools', []), clarification=result.get('clarification', ''), entities=entities, confidence=float(result.get('confidence', 0.0)), reason=result.get('reason', 'v2_langgraph_fixture'))

    def Search(self, request, context):
        self._authorize(request.context, context)
        if request.context.subject_type == 'customer' and request.response_purpose != 'customer_chat':
            context.abort(grpc.StatusCode.PERMISSION_DENIED, 'customer purpose required')
        query = request.query.strip()
        if not query:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, 'query is required')
        try:
            _ensure_grpc_active(context)
            service = self._provider_v2 or self._v2
            result = asyncio.run(service.arun(query=query, scope=self._v2_scope(request.context), response_purpose=request.response_purpose or 'internal', cancel_event=_GrpcCancelEvent(context), timeout_s=_grpc_timeout(context), phase='search')) if self._provider_v2 else service.run(query=query, scope=self._v2_scope(request.context), response_purpose=request.response_purpose or 'internal', phase='search')
        except (PermissionError, ValueError, TypeError, RuntimeError, ProviderError) as error:
            _abort_v2_error(context, error, stage='search', request_id=request.context.request_id)
        for item in result.get('evidence', []):
            _ensure_grpc_active(context)
            self._authorize(request.context, context)
            yield rag_pb2.SearchResponse(phase='retrieval', complete=False, evidence=[self._v2_evidence(item)], request_id=request.context.request_id, is_mock=self._v2_is_mock())
        _ensure_grpc_active(context)
        yield rag_pb2.SearchResponse(phase='complete', complete=True, request_id=request.context.request_id, is_mock=self._v2_is_mock())

    def Generate(self, request, context):
        self._authorize(request.context, context)
        if request.context.subject_type == 'customer' and request.response_purpose != 'customer_chat':
            context.abort(grpc.StatusCode.PERMISSION_DENIED, 'customer purpose required')
        try:
            _ensure_grpc_active(context)
            evidence = [self._v2_input_evidence(item) for item in request.evidence]
            facts = self._v2._validate_facts([{'tool_name': item.tool_name, 'value_json': item.value_json, 'observed_at': item.observed_at, 'is_mock': item.is_mock, 'resource_id': item.resource_id} for item in request.facts])
            service = self._provider_v2 or self._v2
            result = asyncio.run(service.arun_with_evidence(query=request.query, scope=self._v2_scope(request.context), evidence=evidence, facts=facts, history_summary=request.history_summary, response_purpose=request.response_purpose or 'internal', cancel_event=_GrpcCancelEvent(context), timeout_s=_grpc_timeout(context), phase='generate')) if self._provider_v2 else service.run_with_evidence(query=request.query, scope=self._v2_scope(request.context), evidence=evidence, history_summary=request.history_summary, response_purpose=request.response_purpose or 'internal', phase='generate')
        except (PermissionError, ValueError, TypeError, RuntimeError, ProviderError) as error:
            _abort_v2_error(context, error, stage='generate', request_id=request.context.request_id)
        sequence = 0
        for chunk in [result.get('answer', '')[i:i + 32] for i in range(0, len(result.get('answer', '')), 32)]:
            _ensure_grpc_active(context)
            self._authorize(request.context, context)
            sequence += 1
            yield rag_pb2.GenerateResponse(sequence=sequence, delta=chunk)
        for item in result.get('citations', []):
            _ensure_grpc_active(context)
            self._authorize(request.context, context)
            sequence += 1
            yield rag_pb2.GenerateResponse(sequence=sequence, citation=self._v2_evidence(item))
        usage_json = json.dumps({'is_mock': self._v2_is_mock(), 'graph': 'langgraph', 'provider': result.get('trace', {}).get('provider', {})}, separators=(',', ':'))
        if request.response_purpose == 'customer_chat':
            recommendation = result.get('reply_decision', {})
            behavior = {'ANSWER': rag_pb2.REPLY_BEHAVIOR_ANSWER, 'CLARIFY': rag_pb2.REPLY_BEHAVIOR_CLARIFY,
                        'HANDOFF': rag_pb2.REPLY_BEHAVIOR_HANDOFF}.get(recommendation.get('behavior'), rag_pb2.REPLY_BEHAVIOR_HANDOFF)
            sequence += 1
            self._authorize(request.context, context)
            yield rag_pb2.GenerateResponse(sequence=sequence, decision=rag_pb2.ReplyDecision(
                behavior=behavior, reason_code=recommendation.get('reason_code', 'knowledge_missing'),
                evidence_ids=recommendation.get('evidence_ids', [])))
        for event in ({'usage_json': usage_json}, {'is_mock': self._v2_is_mock()}, {'can_copy': bool(result.get('can_copy', False))}, {'done': True}):
            _ensure_grpc_active(context)
            self._authorize(request.context, context)
            sequence += 1
            yield rag_pb2.GenerateResponse(sequence=sequence, **event)

    def SaveCheckpoint(self, request, context):
        scope = self._v2_checkpoint_scope(request, context)
        try:
            record = self._v2.save_asking_checkpoint(scope)
        except PermissionError as error:
            context.abort(grpc.StatusCode.PERMISSION_DENIED, str(error))
        except (KeyError, ValueError, TypeError) as error:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, str(error))
        except RuntimeError as error:
            context.abort(grpc.StatusCode.FAILED_PRECONDITION, str(error))
        _ensure_grpc_active(context)
        return self._checkpoint_response(record, rag_pb2.SaveCheckpointResponse)

    def ResumeCheckpoint(self, request, context):
        scope = self._v2_checkpoint_scope(request, context)
        try:
            record = self._v2.resume_checkpoint(scope)
        except KeyError as error:
            context.abort(grpc.StatusCode.NOT_FOUND, str(error))
        except PermissionError as error:
            context.abort(grpc.StatusCode.PERMISSION_DENIED, str(error))
        except (ValueError, TypeError) as error:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, str(error))
        except RuntimeError as error:
            context.abort(grpc.StatusCode.FAILED_PRECONDITION, str(error))
        _ensure_grpc_active(context)
        return self._checkpoint_response(record, rag_pb2.ResumeCheckpointResponse)
