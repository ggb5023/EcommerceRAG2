"""LangGraph state machine for the v2 deterministic AI-plane slice."""
from __future__ import annotations

import hashlib
import json
import math
import re
from collections.abc import Iterable, Mapping
from dataclasses import dataclass
from itertools import pairwise
from typing import Any

from langgraph.graph import END, START, StateGraph

from app.chains.fake import generation_chain, infer_fact_tools, understand_chain
from app.chains.provider import ProviderChainSet
from app.facts import ALLOWED_FACT_TOOLS
from app.providers.contracts import SparseWeight

from .fixtures import FIXTURE_CORPUS
from .prompts import registry
from .state import Evidence, GraphState, Scope

CONTROL_SCHEMA: dict[str, Any] = {
    "type": "object",
    "required": ["rewritten_query", "intent", "information_source", "required_fact_tools", "entities", "clarification", "confidence"],
    "properties": {
        "rewritten_query": {"type": "string", "maxLength": 100},
        "intent": {"type": "string", "enum": ["knowledge", "factual", "clarification"]},
        "information_source": {"type": "string", "enum": ["knowledge", "realtime_business"]},
        "required_fact_tools": {"type": "array",
                                "items": {"type": "string", "enum": sorted(ALLOWED_FACT_TOOLS)}},
        "clarification": {"type": "string", "maxLength": 80},
        "confidence": {"type": "number"},
        "entities": {"type": "array", "maxItems": 2, "items": {"type": "object", "properties": {"name": {"type": "string"}, "value": {"type": "string"}, "confidence": {"type": "number"}}, "required": ["name", "value", "confidence"]}},
    },
}
GENERATION_SCHEMA: dict[str, Any] = {
    "type": "object",
    "required": ["answer", "citation_ids"],
    "properties": {"answer": {"type": "string", "minLength": 1, "maxLength": 260},
                   "citation_ids": {"type": "array", "items": {"type": "string"}},
                   "clarification_plan": {
                       "type": "object",
                       "required": ["object_question", "question_type_question", "time_question", "location_question", "source_scope_question"],
                       "properties": {name: {"type": "string", "minLength": 1, "maxLength": 40}
                                      for name in ("object_question", "question_type_question", "time_question", "location_question", "source_scope_question")},
                       "additionalProperties": False,
                   }},
}
# 本正则只产出 intent 分类，不决定终态；终态由 Go 的 decideClarification 决定（见 0028）。
CLARIFICATION_WORKFLOW = re.compile(
    r"澄清|(?:应该|应|该).*先.*(?:问|确认)|(?:时效|时间|范围).*(?:不明确|不清楚)|clarif|(?:time|scope).*unclear",
    re.IGNORECASE,
)


def validate_scope(scope: Scope) -> None:
    required = ("tenant_id", "user_id", "permission_revision")
    if any(not str(scope.get(key, "")).strip() for key in required):
        raise PermissionError("scope is missing required identity fields")
    if scope.get("role") not in {"owner", "admin", "operator", "viewer", "customer"}:
        raise PermissionError("scope role is invalid")
    if scope.get("role") == "customer" and (
        scope.get("subject_type") != "customer" or not scope.get("customer_id")
        or not scope.get("customer_session_id") or not scope.get("conversation_epoch")
        or len(scope.get("allowed_shop_ids", [])) != 1
    ):
        raise PermissionError("customer scope is invalid")
    if scope.get("role") == "customer":
        service_type = scope.get("service_type") or "pre_sales"
        order_id = str(scope.get("customer_order_id") or "")
        if service_type not in {"pre_sales", "after_sales"}:
            raise PermissionError("customer service lane is invalid")
        if (service_type == "after_sales") != bool(order_id):
            raise PermissionError("customer order binding is invalid")
    shops = set(scope.get("allowed_shop_ids") or [])
    documents = set(scope.get("allowed_document_ids") or [])
    service_type = scope.get("service_type") or "pre_sales"
    empty_grant_allowed = (scope.get("role") == "customer" and service_type == "after_sales"
                           and bool(scope.get("customer_order_id")))
    if not shops or (not documents and not empty_grant_allowed):
        raise PermissionError("scope must contain finite shop and document grants")
    selected = scope.get("shop_id")
    if selected and selected not in shops:
        raise PermissionError("selected shop is outside the scope")


def _terms(text: str) -> set[str]:
    """Return stable word and Chinese n-gram terms for fixture retrieval."""
    lowered = text.lower()
    terms = {part for part in re.findall(r"[a-z0-9_]+", lowered) if len(part) > 1}
    for match in re.findall(r"[\u4e00-\u9fff]+", lowered):
        if len(match) >= 2:
            terms.update(match[index:index + size] for size in (2, 3, 4) for index in range(len(match) - size + 1))
    return terms


def understand_node(state: GraphState) -> dict:
    return understand_chain.invoke({"query": state["query"]})


def retrieve_node(state: GraphState) -> dict:
    scope = state["scope"]
    validate_scope(scope)
    if state.get("seed_evidence") is not None:
        return {"candidates": list(state.get("seed_evidence", []))}
    query_terms = _terms(state.get("rewritten_query", state["query"]))
    candidates: list[Evidence] = []
    for item in FIXTURE_CORPUS:
        # This is the defensive Python-side filter. It may only narrow Go's scope.
        if item["tenant_id"] != scope["tenant_id"]:
            continue
        if item["shop_id"] not in set(scope["allowed_shop_ids"] or []):
            continue
        if item["document_id"] not in set(scope["allowed_document_ids"] or []):
            continue
        if scope.get("shop_id") and item["shop_id"] != scope["shop_id"]:
            continue
        overlap = len(query_terms & _terms(item["content"]))
        if overlap:
            copy = dict(item)
            copy["raw_score"] = float(overlap)
            candidates.append(copy)
    return {"candidates": candidates}


def _validate_embedding_batch(value: object, expected_count: int, label: str) -> list[Mapping[str, Any]]:
    """Validate the provider embedding contract before using a vector.

    The current v2 fixture has no persistent ANN index.  Calling the embedding
    Runnable here still exercises the production boundary and keeps the
    ``query``/``document`` and ``dense&sparse`` parameters visible.  Retrieval
    remains explicitly fixture backed until the pgvector index is connected.
    """
    if not isinstance(value, Mapping) or not isinstance(value.get("items"), list):
        raise TypeError(f"provider {label} embedding output is invalid")
    items = value["items"]
    if len(items) != expected_count:
        raise ValueError(f"provider {label} embedding count is invalid")
    checked: list[Mapping[str, Any]] = []
    for item in items:
        if not isinstance(item, Mapping):
            raise TypeError(f"provider {label} embedding item is invalid")
        dense = item.get("dense")
        sparse = item.get("sparse")
        if item.get("dimensions") != 1024 or not isinstance(dense, list) or len(dense) != 1024:
            raise ValueError(f"provider {label} embedding dimension is invalid")
        if not all(isinstance(number, (int, float)) and not isinstance(number, bool) and math.isfinite(float(number)) for number in dense):
            raise ValueError(f"provider {label} embedding dense values are invalid")
        if not isinstance(sparse, list) or not sparse:
            raise ValueError(f"provider {label} embedding sparse values are invalid")
        tokenizer_id = item.get("sparse_tokenizer_id")
        if not isinstance(tokenizer_id, str) or not tokenizer_id.strip():
            raise ValueError(f"provider {label} sparse tokenizer id is invalid")
        for weight in sparse:
            if not isinstance(weight, Mapping):
                raise TypeError(f"provider {label} sparse item is invalid")
            token_id, token_weight = weight.get("token_id"), weight.get("weight")
            if not isinstance(token_id, int) or isinstance(token_id, bool) or token_id < 0:
                raise ValueError(f"provider {label} sparse token is invalid")
            if not isinstance(token_weight, (int, float)) or isinstance(token_weight, bool) or not math.isfinite(float(token_weight)):
                raise ValueError(f"provider {label} sparse weight is invalid")
        checked.append(item)
    return checked


def _cosine(left: list[float], right: list[float]) -> float:
    numerator = sum(a * b for a, b in zip(left, right))
    left_norm = math.sqrt(sum(a * a for a in left))
    right_norm = math.sqrt(sum(b * b for b in right))
    if left_norm == 0 or right_norm == 0:
        return 0.0
    return numerator / (left_norm * right_norm)


def scoped(state: GraphState, payload: dict[str, Any]) -> dict[str, Any]:
    """Attach the authoritative tenant/shop to every provider chain call."""
    scope = state.get("scope") or {}
    # usage_log stores the numeric tenant database id; the alias remains the
    # authorization identity and is only a fallback for legacy fixtures.
    tenant = scope.get("tenant_db_id") or scope.get("tenant_id")
    shop = scope.get("shop_id")
    if tenant is None or isinstance(tenant, bool) or not isinstance(tenant, (int, str)) or (isinstance(tenant, str) and not tenant.strip()):
        raise ValueError("provider chain requires tenant_id")
    if shop is None or isinstance(shop, bool) or not isinstance(shop, str) or not shop.strip():
        raise ValueError("provider chain requires shop_id")
    return {**payload, "tenant_id": tenant, "shop_id": shop, "domain": scope.get("domain") or "answer"}


async def provider_retrieve_node(state: GraphState, chains: ProviderChainSet) -> dict[str, Any]:
    """Retrieve through the configured persistent index or the bounded fixture."""
    if state.get("seed_evidence") is not None:
        return {"candidates": list(state.get("seed_evidence", []))}
    base = retrieve_node(state)
    candidates = list(base.get("candidates", []))
    if chains.retriever is None and not candidates:
        return {"candidates": []}
    cancel_event = state.get("cancel_event")
    if cancel_event is not None and cancel_event.is_set():
        raise RuntimeError("request cancelled")
    query = state.get("rewritten_query", state["query"])
    query_value = await chains.embedding.ainvoke(scoped(state, {
        "texts": [query], "text_type": "query", "dimensions": 1024,
        "output_type": "dense&sparse", "timeout_s": state.get("provider_timeout_s"),
        "cancel_event": cancel_event,
    }))
    query_items = _validate_embedding_batch(query_value, 1, "query")
    if chains.retriever is not None:
        retrieved = await chains.retriever.asearch(
            query=query,
            query_vector=query_items[0]["dense"],
            query_sparse=[
                SparseWeight(token_id=int(weight["token_id"]), weight=float(weight["weight"]))
                for weight in query_items[0]["sparse"]
            ],
            query_sparse_tokenizer=query_items[0]["sparse_tokenizer_id"],
            scope=state["scope"],
            response_purpose=state.get("response_purpose", "internal"),
            business_date=state.get("business_date"),
            limit=20,
        )
        return {
            "candidates": retrieved,
            "provider_trace": {
                "retrieval_mode": "pgvector_trigram_rrf",
                "query": query_items[0].get("provider", {}),
                "candidate_count": len(retrieved),
                "skipped_tokenizer_mismatch": getattr(
                    chains.retriever, "last_sparse_skipped_tokenizer", 0
                ),
            },
        }
    document_value = await chains.embedding.ainvoke(scoped(state, {
        "texts": [item["content"] for item in candidates], "text_type": "document",
        "dimensions": 1024, "output_type": "dense&sparse",
        "timeout_s": state.get("provider_timeout_s"), "cancel_event": cancel_event,
    }))
    document_items = _validate_embedding_batch(document_value, len(candidates), "document")
    query_dense = [float(number) for number in query_items[0]["dense"]]
    for item, vector in zip(candidates, document_items):
        item["embedding_score"] = _cosine(query_dense, [float(number) for number in vector["dense"]])
        # Keep lexical overlap as the primary fixture ranking signal until the
        # persistent vector index is wired; embedding is a deterministic tie
        # breaker and an observed contract, not a production ANN claim.
        item["raw_score"] = float(item.get("raw_score", 0.0)) + item["embedding_score"] * 1e-6
    return {
        "candidates": candidates,
        "provider_trace": {
            "retrieval_mode": "fixture_embedding_contract",
            "query": query_items[0].get("provider", {}),
            "documents": document_items[0].get("provider", {}) if document_items else {},
        },
    }


def fuse_rrf_node(state: GraphState) -> dict:
    # Generate receives evidence that Go already authorized and persisted from
    # Search.  It is a signed snapshot, not a new retrieval candidate set;
    # changing its order or rank would make the returned citations differ from
    # the canonical evidence and correctly fail the Go boundary check.
    if state.get("seed_evidence") is not None:
        return {"fused": [dict(item) for item in state.get("candidates", [])]}

    # Persistent retrieval supplies independent dense/trigram ranks. Fixture
    # retrieval has one bounded list, so both channels use its deterministic
    # rank while retaining the same RRF formula.
    fused: list[Evidence] = []
    candidates = list(state.get("candidates", []))
    if candidates and any("dense_rank" in item or "sparse_rank" in item for item in candidates):
        ordered = sorted(candidates, key=lambda item: (-float(item.get("rrf_score", 0.0)), item["id"]))
    else:
        ordered = sorted(candidates, key=lambda x: (-x["raw_score"], x["id"]))
    for rank, item in enumerate(ordered, 1):
        copy = dict(item)
        dense_rank = int(copy.get("dense_rank", rank))
        sparse_rank = int(copy.get("sparse_rank", rank))
        copy["rrf_score"] = float(copy.get("rrf_score", 0.0)) or (1.0 / (60 + dense_rank) + 0.7 / (60 + sparse_rank))
        copy["rank"] = rank
        fused.append(copy)
    return {"fused": fused}


def rerank_node(state: GraphState) -> dict:
    ranked = sorted(state.get("fused", []), key=lambda x: (-x["raw_score"], -x["rrf_score"], x["id"]))[:8]
    for rank, item in enumerate(ranked, 1):
        item["rank"] = rank
    return {"evidence": ranked}


def context_gate_node(state: GraphState) -> dict:
    purpose = state.get("response_purpose", "internal")
    allowed: Iterable[Evidence] = state.get("evidence", [])
    if purpose in {"customer_reply", "customer_chat"}:
        allowed = (item for item in allowed if item["disclosure_class"] == "external_allowed" and item["customer_eligible"])
    selected = list(allowed)
    # Similarity is a ranking signal, not proof of applicability. A useful
    # prerequisite/source-limit rule can score below a topical summary.
    return {"evidence": selected[:8], "is_mock": bool(state.get("is_mock", True))}


def generate_node(state: GraphState) -> dict:
    result = generation_chain.invoke({
        "clarification": state.get("clarification", ""),
        "evidence": state.get("evidence", []),
    })
    return {
        "answer": result["answer"],
        "can_copy": bool(result["can_copy"]),
        "citations": list(state.get("evidence", [])),
        "trace": {"nodes": ["understand", "retrieve", "fuse_rrf", "rerank", "context_gate", "generate"]},
    }


def phase_nodes(phase: str) -> tuple[str, ...]:
    paths = {
        "full": ("understand", "retrieve", "fuse_rrf", "rerank", "context_gate", "generate"),
        "understand": ("understand",),
        "search": ("retrieve", "fuse_rrf", "rerank", "context_gate"),
        "generate": ("context_gate", "generate"),
    }
    if phase not in paths:
        raise ValueError("graph execution phase is invalid")
    return paths[phase]


def _compile_graph(nodes: Mapping[str, Any], phase: str):
    path = phase_nodes(phase)
    graph = StateGraph(GraphState)
    for name in path:
        graph.add_node(name, nodes[name])
    graph.add_edge(START, path[0])
    for previous, following in pairwise(path):
        graph.add_edge(previous, following)
    graph.add_edge(path[-1], END)
    return graph.compile()


def build_graph(phase: str = "full"):
    return _compile_graph({
        "understand": understand_node, "retrieve": retrieve_node,
        "fuse_rrf": fuse_rrf_node, "rerank": rerank_node,
        "context_gate": context_gate_node, "generate": generate_node,
    }, phase)


GRAPH = build_graph()


def _checked_fact_tools(value: object) -> list[str]:
    """Keep model routing output inside the Go-owned read-only tool registry."""
    if not isinstance(value, list) or any(not isinstance(item, str) for item in value):
        raise TypeError("provider required_fact_tools is invalid")
    result = [item for item in value if item in ALLOWED_FACT_TOOLS]
    if len(result) != len(value) or len(set(result)) != len(result):
        raise ValueError("provider required_fact_tools contains an unknown or duplicate tool")
    return result


def _provider_understand_result(value: object, query: str) -> dict[str, Any]:
    if not isinstance(value, Mapping):
        raise TypeError("provider control output is invalid")
    structured = value.get("structured")
    if not isinstance(structured, Mapping):
        raise TypeError("provider control structured output is invalid")
    confidence = structured.get("confidence", 0.0)
    if not isinstance(confidence, (int, float)) or isinstance(confidence, bool) or not math.isfinite(confidence):
        raise TypeError("provider control confidence is invalid")
    raw_entities = structured.get("entities", [])
    if not isinstance(raw_entities, list):
        raise TypeError("provider control entities are invalid")
    entities: list[dict[str, Any]] = []
    for item in raw_entities:
        if not isinstance(item, Mapping) or not isinstance(item.get("name"), str) or not isinstance(item.get("value"), str):
            raise TypeError("provider control entity is invalid")
        entity_confidence = item.get("confidence", 0.0)
        if not isinstance(entity_confidence, (int, float)) or isinstance(entity_confidence, bool) or not math.isfinite(entity_confidence):
            raise TypeError("provider control entity confidence is invalid")
        entities.append({"name": item["name"].strip(), "value": item["value"].strip(), "confidence": max(0.0, min(1.0, float(entity_confidence)))})
    if (structured.get("intent") not in {"knowledge", "factual", "clarification"}
            or structured.get("information_source") not in {"knowledge", "realtime_business"}):
        raise ValueError("provider control routing classification is invalid")
    return {
        "rewritten_query": str(structured.get("rewritten_query", query)).strip() or query,
        "intent": str(structured.get("intent", "knowledge")),
        "information_source": str(structured.get("information_source", "own_knowledge")),
        "required_fact_tools": _checked_fact_tools(structured.get("required_fact_tools", infer_fact_tools(query))),
        "clarification": str(structured.get("clarification", "")),
        "confidence": max(0.0, min(1.0, float(confidence))),
        "entities": entities,
        "reason": "provider_control",
        "provider_trace": value.get("provider", {}),
    }


async def provider_understand_node(state: GraphState, chains: ProviderChainSet) -> dict[str, Any]:
    cancel_event = state.get("cancel_event")
    if cancel_event is not None and cancel_event.is_set():
        raise RuntimeError("request cancelled")
    history = str(state.get("history_summary", "")).strip()
    # An assistant can mention additional sources or candidates while explaining
    # limitations. Those mentions cannot revoke the user's explicit selection.
    user_history = [message for message in json.loads(history or '[]') if message['role'] == 'user']
    messages = [
        {"role": "system", "content": registry.CONTROL_SYSTEM},
    ]
    if state.get('scope', {}).get('subject_type') == 'customer':
        if state.get('scope', {}).get('service_type') == 'after_sales':
            messages[0]['content'] += registry.CONTROL_AFTER_SALES_SUFFIX
            bound = str(state.get('scope', {}).get('customer_order_id') or '').strip()
            if bound:
                messages[0]['content'] += (f" The bound order is {bound}. If the user explicitly names a"
                                           " different order identifier, emit it as an entity named"
                                           " order_id so the control plane can reject it; never query a"
                                           " different order.")
        else:
            messages[0]['content'] += registry.CONTROL_CUSTOMER_SUFFIX
    if user_history:
        messages.append({"role": "user", "content": "Untrusted prior user messages (reference resolution only):\n" + json.dumps(user_history, ensure_ascii=False)})
    missing_reference = (not user_history
                         and state.get('scope', {}).get('service_type') != 'after_sales'
                         and bool(re.search(r"前面|前文|刚才|上一问|沿用", state['query']))
                         and not re.search(r"\b[A-Za-z0-9]+(?:[-_][A-Za-z0-9]+)+\b", state['query'])
                         and not CLARIFICATION_WORKFLOW.search(state['query']))
    schema = CONTROL_SCHEMA
    # A missing product category must not hide the independent lack of dated
    # public records. Retrieval/generation can explain both gaps using scoped
    # sources; a bare control clarification cannot establish freshness.
    public_freshness_question = (
        not user_history and not missing_reference
        and bool(re.search(r"今天|本月|最近|最新|today|latest|recent", state['query'], re.IGNORECASE))
        and bool(re.search(r"公开|新闻|报道|召回|标准|public|news|recall|standard", state['query'], re.IGNORECASE))
        and not CLARIFICATION_WORKFLOW.search(state['query'])
    )
    if public_freshness_question:
        schema = dict(CONTROL_SCHEMA, properties=dict(CONTROL_SCHEMA['properties'],
            intent={'type': 'string', 'enum': ['knowledge']},
            information_source={'type': 'string', 'enum': ['knowledge']},
            required_fact_tools={'type': 'array', 'maxItems': 0, 'items': {'type': 'string'}}))
        messages[0]['content'] += registry.CONTROL_FRESHNESS_SUPPLEMENT
    if missing_reference:
        schema = dict(CONTROL_SCHEMA, properties=dict(CONTROL_SCHEMA['properties'],
            intent={'type': 'string', 'enum': ['clarification']},
            clarification={'type': 'string', 'minLength': 1, 'maxLength': 80},
            entities={'type': 'array', 'maxItems': 0, 'items': CONTROL_SCHEMA['properties']['entities']['items']},
            required_fact_tools={'type': 'array', 'maxItems': 0, 'items': {'type': 'string'}}))
        messages[0]['content'] += registry.CONTROL_MISSING_REFERENCE_SUPPLEMENT
    elif user_history:
        messages[0]['content'] += registry.CONTROL_AMBIGUOUS_HISTORY_SUPPLEMENT
    messages.append({"role": "user", "content": state["query"]})
    result = await chains.control.ainvoke(scoped(state, {
        "messages": messages,
        "response_schema": schema,
        "timeout_s": state.get("provider_timeout_s"),
        "cancel_event": cancel_event,
    }))
    return _provider_understand_result(result, state["query"])


async def provider_rerank_node(state: GraphState, chains: ProviderChainSet) -> dict[str, Any]:
    cancel_event = state.get("cancel_event")
    if cancel_event is not None and cancel_event.is_set():
        raise RuntimeError("request cancelled")
    fused = list(state.get("fused", []))
    if not fused:
        return {"evidence": []}
    if state.get("seed_evidence") is not None:
        return {
            "evidence": [dict(item) for item in fused],
            "provider_trace": {"retrieval": state.get("provider_trace", {}), "rerank": {"preserved_seed": True}},
        }
    result = await chains.rerank.ainvoke(scoped(state, {
        "query": state.get("rewritten_query", state["query"]),
        "candidates": [item["content"] for item in fused],
        "top_n": min(8, len(fused)),
        "timeout_s": state.get("provider_timeout_s"),
        "cancel_event": cancel_event,
    }))
    if not isinstance(result, Mapping) or not isinstance(result.get("items"), list):
        raise TypeError("provider rerank output is invalid")
    ranked: list[Evidence] = []
    seen: set[int] = set()
    for rank, row in enumerate(result["items"], 1):
        if not isinstance(row, Mapping):
            raise TypeError("provider rerank item is invalid")
        index, score = row.get("index"), row.get("score")
        if (
            not isinstance(index, int)
            or isinstance(index, bool)
            or index < 0
            or index >= len(fused)
            or index in seen
            or not isinstance(score, (int, float))
            or isinstance(score, bool)
        ):
            raise ValueError("provider rerank index or score is invalid")
        seen.add(index)
        item = dict(fused[index])
        item["rank"] = rank
        item["rerank_score"] = float(score)
        ranked.append(item)
    return {
        "evidence": ranked,
        "reranked": ranked,
        "provider_trace": {
            "retrieval": state.get("provider_trace", {}),
            "rerank": result.get("provider", {}),
        },
    }


async def provider_context_gate_node(state: GraphState, chains: ProviderChainSet) -> dict[str, Any]:
    gated = context_gate_node(state)
    evidence = gated['evidence']
    if (state.get('seed_evidence') is not None
            or CLARIFICATION_WORKFLOW.search(state['query'])
            or len({e['document_id'] for e in evidence}) <= 1):
        # Workflow prerequisites can rank below the policy they qualify. Keep
        # the already scoped/disclosure-filtered top eight together; another
        # topical selection must not discard their clarification context.
        return gated
    # Rerank similarity alone cannot distinguish an order-policy conflict from
    # a news-source conflict. Select applicability only; never grant new scope
    # or supply an answer. Generate later receives Go's canonical snapshot.
    schema = {'type': 'object', 'required': ['selected_ids'], 'additionalProperties': False,
              'properties': {'selected_ids': {'type': 'array', 'maxItems': 8,
                                             'items': {'type': 'string'}}}}
    messages = [
        {'role': 'system', 'content': registry.CONTEXT_GATE_SYSTEM},
        {'role': 'user', 'content': f"Question: {state.get('rewritten_query', state['query'])}\n" +
         '\n'.join(f"[{e['id']}] document_id={e['document_id']}\n{e['content']}" for e in evidence)},
    ]
    result = await chains.control.ainvoke(scoped(state, {'messages': messages, 'response_schema': schema,
        'timeout_s': state.get('provider_timeout_s'), 'cancel_event': state.get('cancel_event')}))
    ids = result.get('structured', {}).get('selected_ids')
    supplied = {e['id'] for e in evidence}
    if (not isinstance(ids, list) or any(not isinstance(i, str) for i in ids)
            or len(set(ids)) != len(ids) or not set(ids).issubset(supplied)):
        raise ValueError('context applicability selection is outside supplied evidence')
    gated['evidence'] = [e for e in evidence if e['id'] in ids]
    return gated


def snapshot_fallback_allowed(intent: object, clarification: str, evidence: list, facts: list) -> bool:
    """A missing identifier must not skip retrieval when a published snapshot exists."""
    return bool(clarification) and intent == "factual" and bool(evidence) and not facts




@dataclass(frozen=True)
class _GenerationMode:
    customer_chat: bool
    snapshot_fallback: bool
    needs_plan: bool
    needs_delivery_limit: bool
    needs_evidence_limits: bool
    needs_applicability: bool
    needs_reason: bool


def _generation_capabilities(state: GraphState) -> str:
    text = registry.GENERATION_CAPABILITY_BASE
    if re.search(r"配送|物流|到货|送到|deliver|logistic", state['query'], re.IGNORECASE):
        text += registry.GENERATION_CAPABILITY_DELIVERY
    if re.search(r"库存|价格|售价|stock|price", state['query'], re.IGNORECASE):
        text += registry.GENERATION_CAPABILITY_STOCK
    if re.search(r"促销|名额|满减|promotion", state['query'], re.IGNORECASE):
        text += registry.GENERATION_CAPABILITY_PROMOTION
    if re.search(r"公开|新闻|报道|标准|补检|外部|搜索", state['query']):
        text += registry.GENERATION_CAPABILITY_EXTERNAL
    if re.search(r"召回|recall", state['query'], re.IGNORECASE):
        text += registry.GENERATION_CAPABILITY_RECALL
    return text


def _schema_with_plan(schema: dict[str, Any]) -> dict[str, Any]:
    return dict(schema, required=[*schema["required"], "clarification_plan"],
                properties=dict(schema["properties"],
                                answer={"type": "string", "minLength": 1, "maxLength": 120}))


def _schema_with_delivery(schema: dict[str, Any]) -> dict[str, Any]:
    return dict(schema, required=[*schema['required'], 'delivery_capability_limit'],
                properties=dict(schema['properties'],
                                delivery_capability_limit={'type': 'string', 'minLength': 1, 'maxLength': 80}))


def _schema_with_limits(schema: dict[str, Any]) -> dict[str, Any]:
    citation_schema = schema['properties']['citation_ids']
    return dict(schema, required=[*schema['required'], 'evidence_limits', 'next_inputs', 'limit_citation_ids'],
                properties=dict(schema['properties'],
                                evidence_limits={'type': 'string', 'maxLength': 200},
                                next_inputs={'type': 'string', 'maxLength': 70},
                                limit_citation_ids=dict(citation_schema, minItems=0)))


def _schema_with_applicability(schema: dict[str, Any]) -> dict[str, Any]:
    return dict(schema, required=[*schema['required'], 'applicability_dimensions'],
                properties=dict(schema['properties'], applicability_dimensions={
                    'type': 'array', 'maxItems': 8,
                    'items': {'type': 'string', 'minLength': 1, 'maxLength': 20}}))


def _schema_with_reason(schema: dict[str, Any]) -> dict[str, Any]:
    return dict(schema, required=[*schema['required'], 'reason_evidence'],
                properties=dict(schema['properties'], reason_evidence={
                    'type': 'string', 'minLength': 1, 'maxLength': 60}))


def _generation_schema(evidence: list, facts: list) -> dict[str, Any]:
    citation_schema = dict(GENERATION_SCHEMA['properties']['citation_ids'],
                           maxItems=len(evidence),
                           items={'type': 'string'})
    if evidence:
        citation_schema['items']['enum'] = [item['id'] for item in evidence]
    if evidence and not facts:
        citation_schema['minItems'] = 1
    return dict(GENERATION_SCHEMA,
                properties=dict(GENERATION_SCHEMA['properties'], citation_ids=citation_schema))


def _generation_mode(state: GraphState, evidence: list, facts: list, snapshot_fallback: bool) -> _GenerationMode:
    customer_chat = state.get("response_purpose") == "customer_chat"
    needs_plan = bool(not customer_chat and CLARIFICATION_WORKFLOW.search(state["query"]))
    needs_delivery_limit = bool(not customer_chat and re.search(r"送到|到货|配送.*时效|delivery.*(?:promise|arrive)",
                                                                 state['query'], re.IGNORECASE))
    needs_evidence_limits = bool(not customer_chat and evidence and not facts and not needs_plan and not needs_delivery_limit)
    needs_applicability = bool(needs_evidence_limits and re.search(r'规则|标准|运输|policy|standard|transport',
                                                                  state['query'], re.IGNORECASE))
    needs_reason = bool(needs_evidence_limits and re.search(r'为什么|为何|原因|why|reason', state['query'], re.IGNORECASE))
    return _GenerationMode(customer_chat=customer_chat, snapshot_fallback=snapshot_fallback,
                           needs_plan=needs_plan, needs_delivery_limit=needs_delivery_limit,
                           needs_evidence_limits=needs_evidence_limits,
                           needs_applicability=needs_applicability, needs_reason=needs_reason)


def _generation_request_spec(state: GraphState, evidence: list, facts: list,
                             mode: _GenerationMode) -> tuple[dict[str, Any], str]:
    schema = _generation_schema(evidence, facts)
    instruction = registry.GENERATION_INSTRUCTION + registry.GENERATION_BASE_SUFFIX
    if facts:
        instruction = registry.GENERATION_FACTS_INSTRUCTION
    if mode.needs_plan:
        schema = _schema_with_plan(schema)
        instruction = registry.GENERATION_PLAN_INSTRUCTION
    if mode.snapshot_fallback:
        instruction = registry.GENERATION_SNAPSHOT_INSTRUCTION
    if mode.needs_delivery_limit:
        schema = _schema_with_delivery(schema)
        instruction += registry.GENERATION_DELIVERY_INSTRUCTION
    if mode.needs_evidence_limits:
        schema = _schema_with_limits(schema)
        instruction += registry.GENERATION_LIMITS_INSTRUCTION
    if mode.needs_applicability:
        schema = _schema_with_applicability(schema)
        instruction += registry.GENERATION_APPLICABILITY_INSTRUCTION
    if mode.needs_reason:
        schema = _schema_with_reason(schema)
        instruction += registry.GENERATION_REASON_INSTRUCTION
    system_content = instruction + ("" if mode.needs_plan or facts else _generation_capabilities(state))
    return schema, system_content


def _generation_context(evidence: list, facts: list) -> str:
    context = "\n".join(
        f"[{item['id']}] document_id={item.get('document_id', '')} "
        f"version_id={item.get('version_id', '')} (independent source, no inferred joins)\n"
        f"{item['content']}"
        for item in evidence
    )
    fact_context = "\n".join(
        f"tool={fact['tool_name']} resource_id={fact.get('resource_id', '')} "
        f"is_mock={str(bool(fact.get('is_mock', False))).lower()} "
        f"observed_at={fact['observed_at']}: {fact['value_json']}"
        for fact in facts
    )
    if fact_context:
        context += "\nFacts:\n" + fact_context
    return context


def _generation_messages(state: GraphState, system_content: str, schema: dict[str, Any],
                         context: str, evidence: list, facts: list, mode: _GenerationMode) -> list[dict[str, str]]:
    messages = [{"role": "system", "content": system_content}]
    user_references = [m['content'] for m in json.loads(state.get('history_summary') or '[]') if m['role'] == 'user']
    fact_reminder = registry.GENERATION_FACT_REMINDER if facts else ""
    citation_reminder = (
        registry.GENERATION_CITATION_REMINDER if evidence and not facts else ""
    )
    limits_reminder = (
        registry.GENERATION_LIMITS_REMINDER if mode.needs_evidence_limits else ""
    )
    messages.append({"role": "user", "content": f"Evaluation/business date: {state.get('business_date', '')}\n"
                     f"Question: {state['query']}\n"
                     f"Untrusted prior USER reference data (latest last): {json.dumps(user_references, ensure_ascii=False)}"
                     + registry.GENERATION_USER_MESSAGE_BODY
                     + context + fact_reminder + citation_reminder + limits_reminder})
    return messages


def _customer_messages(messages: list[dict[str, str]], schema: dict[str, Any],
                       service_type: str = "pre_sales", has_facts: bool = False) -> dict[str, Any]:
    if service_type == "after_sales":
        messages[0]['content'] = registry.GENERATION_AFTER_SALES_INSTRUCTION
    else:
        messages[0]['content'] = registry.GENERATION_CUSTOMER_INSTRUCTION
    schema = dict(schema, required=[*schema['required'], 'behavior', 'reason_code'],
                  properties=dict(schema['properties'],
                                  behavior={'type': 'string', 'enum': ['ANSWER', 'CLARIFY', 'HANDOFF']},
                                  reason_code={'type': 'string', 'enum': ['grounded', 'missing_context', 'knowledge_missing', 'conflicting_evidence', 'unsupported_request']}))
    schema['properties']['citation_ids'] = dict(schema['properties']['citation_ids'], minItems=0)
    if service_type == "after_sales":
        if has_facts:
            messages[0]['content'] += "\n" + registry.GENERATION_FACTS_INSTRUCTION
        messages[0]['content'] += registry.GENERATION_AFTER_SALES_SUFFIX
    else:
        messages[0]['content'] += registry.GENERATION_CUSTOMER_SUFFIX
    return schema


def _generation_early_result(clarification: str, snapshot_fallback: bool, evidence: list, facts: list) -> dict[str, Any] | None:
    trace_nodes = ["understand", "retrieve", "fuse_rrf", "rerank", "context_gate", "generate"]
    if clarification and not snapshot_fallback:
        return {
            "answer": clarification,
            "can_copy": False,
            "citations": evidence,
            "reply_decision": {"behavior": "CLARIFY", "reason_code": "missing_context", "evidence_ids": []},
            "trace": {"nodes": trace_nodes},
        }
    if not evidence and not facts:
        return {
            "answer": "当前资料没有足够依据回答，请补充问题或转人工客服。",
            "can_copy": False,
            "citations": [],
            "reply_decision": {"behavior": "HANDOFF", "reason_code": "knowledge_missing", "evidence_ids": []},
            "trace": {"nodes": trace_nodes},
        }
    return None


def _generation_inputs(state: GraphState) -> tuple[Any, str, list, list, bool]:
    cancel_event = state.get("cancel_event")
    if cancel_event is not None and cancel_event.is_set():
        raise RuntimeError("request cancelled")
    clarification = str(state.get("clarification", ""))
    evidence = list(state.get("evidence", []))
    facts = state.get("facts", [])
    snapshot_fallback = snapshot_fallback_allowed(state.get("intent"), clarification, evidence, facts)
    return cancel_event, clarification, evidence, facts, snapshot_fallback


async def _record_generation_input(state: GraphState, chains: ProviderChainSet, evidence: list,
                                   messages: list[dict[str, str]], schema: dict[str, Any], context: str) -> None:
    recorder = getattr(getattr(chains, "retriever", None), "arecord_generation_input", None)
    if state.get("scope", {}).get("subject_type") == "customer" and recorder is not None:
        # Record the final constructed input before invocation, including on
        # provider failure. This does not prove the provider received it.
        fingerprint = hashlib.sha256(json.dumps(
            {"messages": messages, "response_schema": schema}, ensure_ascii=False,
            sort_keys=True, separators=(",", ":"),
        ).encode()).hexdigest()
        await recorder(state["scope"], evidence,
                       context_sha256=hashlib.sha256(context.encode()).hexdigest(),
                       request_sha256=fingerprint)


def _apply_generation_plan(answer: str, structured: Any) -> str:
    plan = structured.get("clarification_plan") if isinstance(structured, Mapping) else None
    keys = GENERATION_SCHEMA["properties"]["clarification_plan"]["required"]
    if (not isinstance(plan, Mapping) or set(plan) != set(keys)
            or any(not isinstance(plan[key], str) or not plan[key].strip() or len(plan[key]) > 40 for key in keys)):
        raise ValueError("provider generation clarification plan is invalid")
    if len(answer) > 120:
        raise ValueError("provider generation clarification answer is too long")
    # Render the model's actual questions, without inventing missing values
    # or treating schema validity as semantic approval.
    return "\n".join(f"{index}. {plan[key].strip()}" for index, key in enumerate(keys, 1)) + "\n\n" + answer


def _apply_generation_delivery(answer: str, structured: Any) -> str:
    limit = structured.get('delivery_capability_limit') if isinstance(structured, Mapping) else None
    if not isinstance(limit, str) or not limit.strip() or len(limit) > 80:
        raise ValueError('provider generation delivery capability limit is invalid')
    # Render the actual model statement, not a substituted answer. Its
    # meaning still requires the separate semantic review.
    return answer.rstrip() + '\n\n' + limit.strip()


def _apply_generation_limits(answer: str, structured: Any) -> tuple[str, list]:
    limits = [structured.get(key) if isinstance(structured, Mapping) else None
              for key in ('evidence_limits', 'next_inputs')]
    if any(not isinstance(value, str) or len(value) > maximum
           for value, maximum in zip(limits, (200, 70))):
        raise ValueError('provider generation evidence limits are invalid')
    # Preserve the model's actual qualifications and requested inputs.
    # No claim is inferred from schema compliance or filled from eval truth.
    combined = '\n\n'.join([answer.rstrip(), *(value.strip() for value in limits if value.strip())])
    return combined, limits


def _apply_generation_applicability(answer: str, structured: Any, evidence: list) -> tuple[str, list | None]:
    dimensions = structured.get('applicability_dimensions') if isinstance(structured, Mapping) else None
    if (not isinstance(dimensions, list) or len(dimensions) > 8
            or any(not isinstance(value, str) or not value.strip() or len(value) > 20 for value in dimensions)
            or len(set(dimensions)) != len(dimensions)
            or any(not any(value in item['content'] for item in evidence) for value in dimensions)):
        raise ValueError('provider generation applicability dimensions are invalid')
    if dimensions:
        answer += '\n\n资料列出的适用核验维度：' + '、'.join(dimensions) + '；其具体值和当前适用性仍须核验。'
    return answer, dimensions


def _apply_generation_reason(answer: str, structured: Any) -> str:
    reason = structured.get('reason_evidence') if isinstance(structured, Mapping) else None
    if not isinstance(reason, str) or not reason.strip() or len(reason) > 60:
        raise ValueError('provider generation reason evidence is invalid')
    return answer + '\n\n' + reason.strip()


def _generation_citations(structured: Any, evidence: list, mode: _GenerationMode, limits: list | None) -> list[str]:
    citation_ids = structured.get("citation_ids") if isinstance(structured, Mapping) else None
    if (not isinstance(citation_ids, list) or any(not isinstance(identifier, str) for identifier in citation_ids)
            or len(citation_ids) != len(set(citation_ids))):
        raise ValueError("provider generation citation IDs are invalid")
    supplied = {item["id"]: item for item in evidence}
    if mode.needs_evidence_limits:
        if not citation_ids:
            raise ValueError("provider generation evidence answer has no citation")
        limit_ids = structured.get('limit_citation_ids')
        if (not isinstance(limit_ids, list) or any(not isinstance(i, str) for i in limit_ids)
                or len(limit_ids) != len(set(limit_ids)) or any(i not in supplied for i in limit_ids)
                or (limits[0].strip() and not limit_ids)):
            raise ValueError('provider generation evidence limits lack cited support')
        citation_ids = list(dict.fromkeys([*citation_ids, *limit_ids]))
    if any(identifier not in supplied for identifier in citation_ids):
        raise ValueError("provider generation citation is outside supplied evidence")
    return citation_ids


def _generation_behavior(structured: Any, mode: _GenerationMode) -> tuple[str, str]:
    behavior = structured.get('behavior') if mode.customer_chat else 'ANSWER'
    reason_code = structured.get('reason_code') if mode.customer_chat else 'grounded'
    if mode.customer_chat and (behavior not in {'ANSWER', 'CLARIFY', 'HANDOFF'}
            or reason_code not in {'grounded', 'missing_context', 'knowledge_missing', 'conflicting_evidence', 'unsupported_request'}
            or (behavior == 'ANSWER' and reason_code != 'grounded')
            or (behavior == 'CLARIFY' and reason_code != 'missing_context')
            or (behavior == 'HANDOFF' and reason_code in {'grounded', 'missing_context'})):
        raise ValueError('provider reply decision is invalid')
    return behavior, reason_code


def _append_generation_versions(answer: str, citation_ids: list[str], evidence: list, mode: _GenerationMode) -> str:
    # Provenance is authoritative metadata, not another generated source fact.
    # Render it from precisely the cited evidence, never an invented external
    # standard revision. This also survives a model omitting the identifier.
    supplied = {item["id"]: item for item in evidence}
    versions = list(dict.fromkeys(str(supplied[i].get("version_id", "")) for i in citation_ids))
    versions = [version for version in versions if version]
    if versions and not mode.customer_chat:
        return answer.rstrip() + "\n\n引用资料存储版本：" + "、".join(versions) + "（不代表外部标准版本或发布日期）。"
    return answer


def _generation_result(result: Any, state: GraphState, evidence: list, facts: list,
                       mode: _GenerationMode) -> dict[str, Any]:
    if not isinstance(result, Mapping):
        raise TypeError("provider generation output is invalid")
    structured = result.get("structured")
    answer = structured.get("answer") if isinstance(structured, Mapping) else result.get("text")
    if not isinstance(answer, str) or not answer.strip():
        raise ValueError("provider generation answer is empty")
    limits: list | None = None
    dimensions: list | None = None
    if mode.needs_plan:
        answer = _apply_generation_plan(answer, structured)
    if mode.needs_delivery_limit:
        answer = _apply_generation_delivery(answer, structured)
    if mode.needs_evidence_limits:
        answer, limits = _apply_generation_limits(answer, structured)
    if mode.needs_applicability:
        answer, dimensions = _apply_generation_applicability(answer, structured, evidence)
    if mode.needs_reason:
        answer = _apply_generation_reason(answer, structured)
    citation_ids = _generation_citations(structured, evidence, mode, limits)
    behavior, reason_code = _generation_behavior(structured, mode)
    if evidence and not facts and not citation_ids and behavior == 'ANSWER':
        raise ValueError("provider generation evidence answer has no citation")
    if mode.needs_applicability and any(not any(value in item['content'] for item in
                                                [e for e in evidence if e['id'] in citation_ids])
                                        for value in dimensions):
        raise ValueError('provider generation applicability dimensions lack cited support')
    answer = _append_generation_versions(answer, citation_ids, evidence, mode)
    supplied = {item["id"]: item for item in evidence}
    return {
        "answer": answer,
        "can_copy": False,
        "reply_decision": {"behavior": behavior, "reason_code": reason_code, "evidence_ids": citation_ids},
        "citations": [supplied[identifier] for identifier in citation_ids],
        "trace": {
            "nodes": ["understand", "retrieve", "fuse_rrf", "rerank", "context_gate", "generate"],
            "provider": result.get("provider", {}),
            "retrieval": state.get("provider_trace", {}),
        },
    }


async def provider_generate_node(state: GraphState, chains: ProviderChainSet) -> dict[str, Any]:
    cancel_event, clarification, evidence, facts, snapshot_fallback = _generation_inputs(state)
    early = _generation_early_result(clarification, snapshot_fallback, evidence, facts)
    if early is not None:
        return early
    context = _generation_context(evidence, facts)
    mode = _generation_mode(state, evidence, facts, snapshot_fallback)
    schema, system_content = _generation_request_spec(state, evidence, facts, mode)
    messages = _generation_messages(state, system_content, schema, context, evidence, facts, mode)
    if mode.customer_chat:
        schema = _customer_messages(messages, schema,
                                    str(state.get('scope', {}).get('service_type') or 'pre_sales'),
                                    bool(facts))
    request = {
        "messages": messages,
        "response_schema": schema,
        "timeout_s": state.get("provider_timeout_s"),
        "cancel_event": cancel_event,
    }
    await _record_generation_input(state, chains, evidence, messages, schema, context)
    if cancel_event is not None and cancel_event.is_set():
        raise RuntimeError("request cancelled")
    result = await chains.generation.ainvoke(scoped(state, request))
    return _generation_result(result, state, evidence, facts, mode)

def build_provider_graph(chains: ProviderChainSet, phase: str = "full"):
    """Compile an async graph using the LangChain Provider boundaries."""
    async def understand(state: GraphState) -> dict[str, Any]:
        return await provider_understand_node(state, chains)

    async def retrieve(state: GraphState) -> dict[str, Any]:
        return await provider_retrieve_node(state, chains)

    async def rerank(state: GraphState) -> dict[str, Any]:
        return await provider_rerank_node(state, chains)

    async def generate(state: GraphState) -> dict[str, Any]:
        return await provider_generate_node(state, chains)

    async def context_gate(state: GraphState) -> dict[str, Any]:
        return await provider_context_gate_node(state, chains)

    return _compile_graph({
        "understand": understand, "retrieve": retrieve,
        "fuse_rrf": fuse_rrf_node, "rerank": rerank,
        "context_gate": context_gate, "generate": generate,
    }, phase)
