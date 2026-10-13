"""Typed state shared by the v2 LangGraph nodes."""
from __future__ import annotations

from typing import Any, TypedDict


class Scope(TypedDict, total=False):
    request_id: str
    tenant_id: str
    tenant_db_id: str
    user_id: str
    allowed_shop_ids: list[str]
    allowed_document_ids: list[str]
    shop_id: str
    role: str
    permission_revision: str
    response_purpose: str
    conversation_id: str
    turn_id: str
    expires_at: int


class Evidence(TypedDict):
    id: str
    content: str
    source_type: str
    source_ref: str
    document_id: str
    version_id: str
    tenant_id: str
    shop_id: str
    disclosure_class: str
    customer_eligible: bool
    rank: int
    raw_score: float
    rrf_score: float
    rerank_score: float


class GraphState(TypedDict, total=False):
    query: str
    history_summary: str
    rewritten_query: str
    scope: Scope
    response_purpose: str
    intent: str
    information_source: str
    required_fact_tools: list[str]
    entities: list[dict[str, Any]]
    confidence: float
    clarification: str
    seed_evidence: list[Evidence]
    facts: list[dict[str, Any]]
    candidates: list[Evidence]
    fused: list[Evidence]
    evidence: list[Evidence]
    answer: str
    citations: list[Evidence]
    degraded_stages: list[str]
    is_mock: bool
    can_copy: bool
    trace: dict[str, Any]
    provider_trace: dict[str, Any]
    business_date: str
    # Runtime-only controls supplied by the Go gRPC boundary. They are not
    # persisted in LangGraph checkpoints or returned to callers.
    cancel_event: Any
    provider_timeout_s: float
