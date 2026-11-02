"""Prompt asset and variant matrix.

A fixed set of selector samples pins which prompt variant a request receives.
Changing prompt text requires a registry version bump; changing variant logic
requires updating this matrix in the same change.
"""
from __future__ import annotations

import importlib

import pytest

from app.graph.prompts import registry
from app.graph.workflow import (
    _customer_messages,
    _generation_context,
    _generation_messages,
    _generation_mode,
    _generation_request_spec,
)

EVIDENCE = [{
    "id": "chunk-1",
    "document_id": "doc-1",
    "version_id": "version-1",
    "content": "source body",
}]
FACTS = [{
    "tool_name": "get_stock",
    "resource_id": "sku-1",
    "is_mock": True,
    "observed_at": "2026-11-02T00:00:00Z",
    "value_json": '{"stock": 3}',
}]


def _system_for(query: str, *, evidence=None, facts=None, snapshot=False,
                purpose="internal") -> tuple[object, str]:
    state = {"query": query, "response_purpose": purpose, "business_date": "2026-11-02"}
    evidence = [] if evidence is None else evidence
    facts = [] if facts is None else facts
    mode = _generation_mode(state, evidence, facts, snapshot)
    schema, system = _generation_request_spec(state, evidence, facts, mode)
    messages = _generation_messages(
        state, system, schema, _generation_context(evidence, facts), evidence, facts, mode,
    )
    if mode.customer_chat:
        _customer_messages(messages, schema)
    return mode, messages[0]["content"]


SAMPLES = [
    pytest.param(
        "base", "商品知识是什么？", [], [], False, "internal",
        {"generation_instruction", "generation_base_suffix", "capability_base"},
        {"facts", "plan", "snapshot", "limits", "delivery", "applicability", "reason", "customer"},
        id="base-no-variant",
    ),
    pytest.param(
        "facts", "库存还有多少？", [], FACTS, False, "internal",
        {"facts"}, {"generation_instruction", "limits", "plan", "customer"},
        id="facts",
    ),
    pytest.param(
        "plan", "时效不明确时应该先问什么？", EVIDENCE, [], False, "internal",
        {"plan"}, {"generation_instruction", "limits", "delivery", "facts", "customer"},
        id="plan",
    ),
    pytest.param(
        "snapshot", "订单状态如何？", EVIDENCE, FACTS, True, "internal",
        {"snapshot"}, {"generation_instruction", "limits", "delivery", "customer"},
        id="snapshot",
    ),
    pytest.param(
        "limits", "退货政策是什么？", EVIDENCE, [], False, "internal",
        {"limits"}, {"facts", "plan", "snapshot", "delivery", "customer"},
        id="limits",
    ),
    pytest.param(
        "delivery", "送到需要几天？", EVIDENCE, [], False, "internal",
        {"delivery"}, {"facts", "plan", "snapshot", "limits", "customer"},
        id="delivery",
    ),
    pytest.param(
        "applicability", "运输规则有哪些？", EVIDENCE, [], False, "internal",
        {"limits", "applicability"}, {"facts", "plan", "snapshot", "delivery", "customer"},
        id="applicability",
    ),
    pytest.param(
        "reason", "为什么需要补检？", EVIDENCE, [], False, "internal",
        {"limits", "reason"}, {"facts", "plan", "snapshot", "delivery", "customer"},
        id="reason",
    ),
    pytest.param(
        "customer", "保温杯怎么清洗？", EVIDENCE, [], False, "customer_chat",
        {"customer", "customer_suffix"}, {"generation_instruction", "facts", "plan", "limits", "delivery"},
        id="customer",
    ),
]


@pytest.mark.parametrize(
    "variant,query,evidence,facts,snapshot,purpose,expected,absent",
    SAMPLES,
)
def test_prompt_matrix_selects_the_expected_variant(
    variant, query, evidence, facts, snapshot, purpose, expected, absent,
):
    _mode, system = _system_for(
        query, evidence=evidence, facts=facts, snapshot=snapshot, purpose=purpose,
    )
    for name in expected:
        assert registry.ASSETS[name], f"{variant}: empty asset {name}"
        assert registry.ASSETS[name] in system, f"{variant}: missing {name}"
    for name in absent:
        assert registry.ASSETS[name] not in system, f"{variant}: unexpected {name}"
    assert f"generation_{variant}" in registry.VARIANTS


def test_every_registry_variant_has_a_version_and_non_empty_text():
    for variant, (module_name, version) in registry.VARIANTS.items():
        assert version, f"{variant}: missing version"
        module = importlib.import_module(module_name)
        texts = [
            value for name, value in vars(module).items()
            if name.isupper() and isinstance(value, str) and value.strip()
        ]
        assert texts, f"{variant}: no non-empty prompt text in {module_name}"
    assert registry.ASSETS, "registry has no assets"
    assert all(value.strip() for value in registry.ASSETS.values())
