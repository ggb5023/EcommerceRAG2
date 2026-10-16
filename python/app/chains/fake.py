"""Deterministic LangChain runnables for the first v2 graph slice.

These runnables deliberately do not call an online model. They establish the
LangChain input/output boundary while the real Provider adapter is evaluated.
"""
from __future__ import annotations

import re
from typing import Any

from langchain_core.runnables import RunnableLambda


def infer_fact_tools(query: str) -> list[str]:
    """Map explicit factual request words to the Go-owned read-only tools."""
    lowered = query.lower()
    tools: list[str] = []
    if any(word in lowered for word in ("价格", "多少钱", "price")):
        tools.append("get_price")
    if any(word in lowered for word in ("库存", "有货", "stock")):
        tools.append("get_stock")
    if any(word in lowered for word in ("订单", "物流状态", "order")):
        tools.append("get_order_status")
    if any(word in lowered for word in ("比较", "对比", "compare")):
        tools.append("compare_products")
    return tools


def _understand(payload: dict[str, Any]) -> dict[str, Any]:
    query = str(payload.get("query", "")).strip()
    normalized = re.sub(r"[\s\u3000！？!?。；;，,、]+", "", query.lower())
    vague = not query or normalized in {"这个呢", "那个怎么样", "它如何", "还有吗"}
    if vague:
        return {
            "rewritten_query": query,
            "intent": "clarification",
            "information_source": "knowledge",
            "required_fact_tools": [],
            "clarification": "请补充商品或具体问题。",
            "confidence": 0.2,
            "reason": "v2_fixture_missing_subject",
        }
    required_fact_tools = infer_fact_tools(query)
    factual = bool(required_fact_tools)
    return {
        "rewritten_query": query,
        "intent": "factual" if factual else "knowledge",
        "information_source": "realtime_business" if factual else "own_knowledge",
        "required_fact_tools": required_fact_tools,
        "clarification": "",
        "confidence": 0.95,
        "reason": "v2_fixture_router",
    }


def _generate(payload: dict[str, Any]) -> dict[str, Any]:
    clarification = str(payload.get("clarification", ""))
    evidence = payload.get("evidence") or []
    if clarification:
        return {"answer": clarification, "can_copy": False}
    if not evidence:
        return {"answer": "当前资料没有足够依据回答，请补充问题或转人工客服。", "can_copy": False}
    return {
        "answer": "根据当前知识资料：" + "；".join(str(item["content"]) for item in evidence),
        # The fixture chain is only a contract harness.  Synthetic output must
        # never be presented as an approved customer reply, even when the
        # evidence itself is externally disclosable.
        "can_copy": False,
    }


understand_chain = RunnableLambda(_understand)
generation_chain = RunnableLambda(_generate)
