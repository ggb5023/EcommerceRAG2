"""Pin the clarification contract: Python classifies, Go decides the terminal state.

The response key set is asserted exactly, so any new `state`/`terminal`/`handoff`
field fails this test. See decisions/0028.
"""
from __future__ import annotations

import pytest

from app.graph import workflow

FIELDS = set(workflow.CONTROL_SCHEMA["required"])


class FakeChain:
    """Stands in for understand_chain; returns exactly the schema fields."""

    def __init__(self, response: dict):
        self.response = response

    def invoke(self, _payload):
        return dict(self.response)


def response(**overrides):
    base = {
        "rewritten_query": "rewritten",
        "intent": "knowledge",
        "information_source": "knowledge",
        "required_fact_tools": [],
        "entities": [],
        "clarification": "",
        "confidence": 0.9,
    }
    base.update(overrides)
    return base


def run(monkeypatch, payload):
    monkeypatch.setattr(workflow, "understand_chain", FakeChain(payload))
    return workflow.understand_node({"query": "query"})


@pytest.mark.parametrize(
    ("intent", "query"),
    [("knowledge", "退货政策是什么"), ("factual", "SKU-CUP-480 还有库存吗"), ("clarification", "那个怎么样")],
)
def test_intent_is_passed_through(monkeypatch, intent, query):
    result = run(monkeypatch, response(intent=intent))
    assert result["intent"] == intent


def test_clarification_carries_a_candidate_text(monkeypatch):
    result = run(monkeypatch, response(intent="clarification", information_source="knowledge",
                                       clarification="请补充要咨询的商品或订单信息。"))
    assert result["intent"] == "clarification"
    assert result["clarification"].strip()


def test_response_has_exactly_the_control_fields(monkeypatch):
    # Python only classifies; ASKING/DONE/HANDOFF are decided by Go's
    # decideClarification (decision 0028). Any extra field fails here.
    result = run(monkeypatch, response())
    assert set(result) == FIELDS


def test_extra_field_is_rejected(monkeypatch):
    # Proves the assertion above is not vacuous: one injected field must fail it.
    with pytest.raises(AssertionError):
        result = run(monkeypatch, response(state="ASKING"))
        assert set(result) == FIELDS
