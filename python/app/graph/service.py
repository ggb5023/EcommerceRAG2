"""Public service boundary around the v2 LangGraph."""
from __future__ import annotations

import json
import os
from collections.abc import Mapping
from datetime import UTC, datetime
from typing import Any

from app.chains.provider import ProviderChainSet
from app.facts import ALLOWED_FACT_TOOLS

from .checkpoint import Checkpoint, CheckpointStore, InMemoryCheckpointStore
from .state import GraphState, Scope
from .workflow import (
    GRAPH,
    build_graph,
    build_provider_graph,
    phase_nodes,
    validate_scope,
)


class LangGraphRagService:
    """Run one bounded graph execution; Go remains the authorization owner."""

    def __init__(
        self,
        provider_chains: ProviderChainSet | None = None,
        checkpoint_store: CheckpointStore | None = None,
        business_date: str | None = None,
    ) -> None:
        self.provider_chains = provider_chains
        self.checkpoint_store = checkpoint_store or InMemoryCheckpointStore()
        self.business_date = business_date or os.environ.get("SYNTHETIC_BUSINESS_DATE") or datetime.now(UTC).date().isoformat()

    def save_asking_checkpoint(self, scope: Scope) -> Checkpoint:
        """Persist only the minimal identity needed to resume an ASKING turn."""
        validate_scope(scope)
        return self.checkpoint_store.save_asking(scope)

    def resume_checkpoint(self, scope: Scope) -> Checkpoint:
        """Consume a checkpoint after the caller supplies a fresh scope."""
        validate_scope(scope)
        return self.checkpoint_store.resume(scope)

    def _invoke(self, payload: GraphState, phase: str = "full") -> GraphState:
        graph = GRAPH if phase == "full" else build_graph(phase)
        result = graph.invoke(payload)
        result["scope"] = payload["scope"]
        if "trace" in result:
            result["trace"]["nodes"] = list(phase_nodes(phase))
        return result

    async def arun(
        self,
        *,
        query: str,
        scope: Scope,
        history_summary: str = "",
        response_purpose: str = "internal",
        provider_chains: ProviderChainSet | None = None,
        cancel_event: Any | None = None,
        timeout_s: float | None = None,
        phase: str = "full",
    ) -> GraphState:
        """Run the graph asynchronously for real LangChain Provider chains."""
        self._validate_query(query, response_purpose, scope)
        selected_chains = provider_chains if provider_chains is not None else self.provider_chains
        payload: GraphState = {
            "query": query,
            "history_summary": self._validate_history(history_summary),
            "scope": scope,
            "response_purpose": response_purpose,
            "is_mock": selected_chains is None,
            "business_date": self.business_date,
        }
        if cancel_event is not None:
            payload["cancel_event"] = cancel_event
        if timeout_s is not None:
            payload["provider_timeout_s"] = timeout_s
        graph = build_graph(phase) if selected_chains is None else build_provider_graph(selected_chains, phase)
        result = await graph.ainvoke(payload)
        recorder = getattr(getattr(selected_chains, "retriever", None), "arecord_trace", None)
        if phase == "search" and scope.get("request_id") and recorder is not None:
            if cancel_event is not None and cancel_event.is_set():
                raise RuntimeError("request cancelled")
            await recorder(scope, result.get("candidates", []), result.get("evidence", []))
        result["scope"] = scope
        if "trace" in result:
            result["trace"]["nodes"] = list(phase_nodes(phase))
        return result

    async def arun_with_evidence(
        self,
        *,
        query: str,
        scope: Scope,
        evidence: list[dict],
        facts: list[dict] | None = None,
        history_summary: str = "",
        response_purpose: str = "internal",
        provider_chains: ProviderChainSet | None = None,
        cancel_event: Any | None = None,
        timeout_s: float | None = None,
        phase: str = "full",
    ) -> GraphState:
        """Run an async provider graph over Go-approved evidence."""
        self._validate_query(query, response_purpose, scope)
        checked = self._validate_seed_evidence(scope, evidence, response_purpose)
        checked_facts = self._validate_facts(facts or [])
        selected_chains = provider_chains if provider_chains is not None else self.provider_chains
        payload: GraphState = {
            "query": query,
            "scope": scope,
            "response_purpose": response_purpose,
            "seed_evidence": checked,
            "evidence": checked,
            "facts": checked_facts,
            "history_summary": self._validate_history(history_summary),
            "is_mock": selected_chains is None,
            "business_date": self.business_date,
        }
        if cancel_event is not None:
            payload["cancel_event"] = cancel_event
        if timeout_s is not None:
            payload["provider_timeout_s"] = timeout_s
        graph = build_graph(phase) if selected_chains is None else build_provider_graph(selected_chains, phase)
        result = await graph.ainvoke(payload)
        result["scope"] = scope
        if "trace" in result:
            result["trace"]["nodes"] = list(phase_nodes(phase))
        return result

    @staticmethod
    def _validate_history(value: str) -> str:
        if not isinstance(value, str) or len(value.encode("utf-8")) > 8192:
            raise ValueError("conversation history exceeds its bound")
        if not value:
            return ""
        messages = json.loads(value)
        if (not isinstance(messages, list) or len(messages) > 6
                or any(not isinstance(m, dict) or set(m) != {"role", "content"}
                       or m["role"] not in {"user", "assistant"} or not isinstance(m["content"], str)
                       for m in messages)):
            raise ValueError("conversation history is invalid")
        return value

    @staticmethod
    def _validate_query(query: str, response_purpose: str, scope: Scope) -> None:
        if not query.strip():
            raise ValueError("query is required")
        if response_purpose not in {"internal", "customer_reply"}:
            raise ValueError("response_purpose is invalid")
        validate_scope(scope)

    def run(self, *, query: str, scope: Scope, history_summary: str = "", response_purpose: str = "internal", phase: str = "full") -> GraphState:
        if self.provider_chains is not None:
            raise RuntimeError("provider-backed graph requires arun")
        self._validate_query(query, response_purpose, scope)
        return self._invoke({
            "query": query,
            "history_summary": self._validate_history(history_summary),
            "scope": scope,
            "response_purpose": response_purpose,
        }, phase)

    @staticmethod
    def _validate_seed_evidence(scope: Scope, evidence: list[dict], response_purpose: str) -> list[dict]:
        """Validate Go-approved evidence before it enters the Python graph.

        Go is the authorization authority, but the AI plane must fail closed if
        an RPC payload is stale, malformed, or widened in transit.  This check
        deliberately validates scope and disclosure metadata only; it does not
        turn synthetic fixture evidence into production-approved content.
        """
        if not isinstance(evidence, list):
            raise PermissionError("evidence must be a list")
        allowed_shops = set(scope.get("allowed_shop_ids") or [])
        allowed_documents = set(scope.get("allowed_document_ids") or [])
        selected_shop = scope.get("shop_id")
        required = (
            "id", "content", "source_type", "source_ref", "document_id",
            "version_id", "tenant_id", "shop_id", "disclosure_class",
            "customer_eligible",
        )
        checked: list[dict] = []
        for item in evidence:
            if not isinstance(item, Mapping):
                raise PermissionError("evidence item is invalid")
            if any(not str(item.get(key, "")).strip() for key in required[:-1]):
                raise PermissionError("evidence item is incomplete")
            if not isinstance(item.get("customer_eligible"), bool):
                raise PermissionError("evidence customer eligibility is invalid")
            if item["tenant_id"] != scope["tenant_id"]:
                raise PermissionError("evidence tenant is outside the scope")
            if item["shop_id"] not in allowed_shops or (selected_shop and item["shop_id"] != selected_shop):
                raise PermissionError("evidence shop is outside the scope")
            if item["document_id"] not in allowed_documents:
                raise PermissionError("evidence document is outside the scope")
            if item["disclosure_class"] not in {"external_allowed", "internal_only", "unclassified"}:
                raise PermissionError("evidence disclosure class is invalid")
            if response_purpose == "customer_reply" and (
                item["disclosure_class"] != "external_allowed" or not item["customer_eligible"]
            ):
                raise PermissionError("customer reply evidence is not externally eligible")
            checked.append(dict(item))
        return checked

    @staticmethod
    def _validate_facts(facts: list[dict]) -> list[dict]:
        """Validate the Go-approved, read-only fact payload before prompting.

        Go remains the only tool and authorization owner.  Python rejects
        unknown tools, malformed JSON and timestamps instead of allowing a
        stale or widened fact to reach a model.
        """
        if not isinstance(facts, list):
            raise PermissionError("facts must be a list")
        checked: list[dict] = []
        for fact in facts:
            if not isinstance(fact, Mapping):
                raise PermissionError("fact item is invalid")
            name = fact.get("tool_name")
            value = fact.get("value_json")
            observed = fact.get("observed_at")
            if name not in ALLOWED_FACT_TOOLS or not isinstance(value, str) or not value.strip() or not isinstance(observed, str):
                raise PermissionError("fact item is invalid")
            try:
                parsed = json.loads(value)
            except json.JSONDecodeError as error:
                raise PermissionError("fact value is invalid JSON") from error
            if not isinstance(parsed, Mapping):
                raise PermissionError("fact value must be an object")
            resource_id = fact.get("resource_id", "")
            if not isinstance(resource_id, str):
                raise PermissionError("fact resource binding is invalid")
            resource_field = ("order_id" if name in {"get_order_status", "get_logistics"}
                              else "sku" if name in {"get_stock", "get_price"} else None)
            if resource_field:
                if not resource_id.strip() or parsed.get(resource_field) != resource_id:
                    raise PermissionError("fact does not match its authorized resource")
            elif resource_id:
                raise PermissionError("non-order fact contains an unexpected resource binding")
            try:
                datetime.fromisoformat(observed)
            except ValueError as error:
                raise PermissionError("fact observed_at is invalid") from error
            checked.append({
                "tool_name": name,
                "value_json": value,
                "observed_at": observed,
                "is_mock": bool(fact.get("is_mock", False)),
                "resource_id": resource_id,
            })
        return checked

    def run_with_evidence(self, *, query: str, scope: Scope, evidence: list[dict], history_summary: str = "", response_purpose: str = "internal", phase: str = "full") -> GraphState:
        """Render approved evidence without allowing the graph to widen it."""
        if self.provider_chains is not None:
            raise RuntimeError("provider-backed graph requires arun")
        if not query.strip():
            raise ValueError("query is required")
        if response_purpose not in {"internal", "customer_reply"}:
            raise ValueError("response_purpose is invalid")
        validate_scope(scope)
        checked_evidence = self._validate_seed_evidence(scope, evidence, response_purpose)
        return self._invoke({
            "query": query,
            "scope": scope,
            "response_purpose": response_purpose,
            "seed_evidence": checked_evidence,
            "history_summary": self._validate_history(history_summary),
            "evidence": checked_evidence,
        }, phase)
