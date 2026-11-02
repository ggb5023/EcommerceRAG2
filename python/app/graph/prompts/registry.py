"""Prompt asset manifest: variant -> (module, version), plus the asset texts.

Changing any prompt text requires bumping the owning variant's version here and
updating python/tests/test_prompt_matrix.py in the same change.
"""
from __future__ import annotations

from . import (
    control,
    generation_applicability,
    generation_base,
    generation_customer,
    generation_delivery,
    generation_facts,
    generation_limits,
    generation_plan,
    generation_reason,
    generation_snapshot,
)

VARIANTS: dict[str, tuple[str, str]] = {
    "control": ("app.graph.prompts.control", "v1"),
    "generation_base": ("app.graph.prompts.generation_base", "v2"),
    "generation_facts": ("app.graph.prompts.generation_facts", "v1"),
    "generation_plan": ("app.graph.prompts.generation_plan", "v2"),
    "generation_snapshot": ("app.graph.prompts.generation_snapshot", "v1"),
    "generation_limits": ("app.graph.prompts.generation_limits", "v2"),
    "generation_delivery": ("app.graph.prompts.generation_delivery", "v2"),
    "generation_applicability": ("app.graph.prompts.generation_applicability", "v1"),
    "generation_reason": ("app.graph.prompts.generation_reason", "v1"),
    "generation_customer": ("app.graph.prompts.generation_customer", "v1"),
}

ASSETS: dict[str, str] = {
    "control_system": control.CONTROL_SYSTEM,
    "control_customer_suffix": control.CONTROL_CUSTOMER_SUFFIX,
    "context_gate": control.CONTEXT_GATE_SYSTEM,
    "freshness_supplement": control.FRESHNESS_SUPPLEMENT,
    "missing_reference_supplement": control.MISSING_REFERENCE_SUPPLEMENT,
    "ambiguous_history_supplement": control.AMBIGUOUS_HISTORY_SUPPLEMENT,
    "generation_instruction": generation_base.GENERATION_INSTRUCTION,
    "generation_base_suffix": generation_base.BASE_SUFFIX,
    "capability_base": generation_base.CAPABILITY_BASE,
    "capability_delivery": generation_base.CAPABILITY_DELIVERY,
    "capability_stock": generation_base.CAPABILITY_STOCK,
    "capability_promotion": generation_base.CAPABILITY_PROMOTION,
    "capability_external": generation_base.CAPABILITY_EXTERNAL,
    "capability_recall": generation_base.CAPABILITY_RECALL,
    "fact_reminder": generation_base.FACT_REMINDER,
    "citation_reminder": generation_base.CITATION_REMINDER,
    "limits_reminder": generation_base.LIMITS_REMINDER,
    "user_message_body": generation_base.USER_MESSAGE_BODY,
    "facts": generation_facts.INSTRUCTION,
    "plan": generation_plan.INSTRUCTION,
    "snapshot": generation_snapshot.INSTRUCTION,
    "limits": generation_limits.INSTRUCTION,
    "delivery": generation_delivery.INSTRUCTION,
    "applicability": generation_applicability.INSTRUCTION,
    "reason": generation_reason.INSTRUCTION,
    "customer": generation_customer.INSTRUCTION,
    "customer_suffix": generation_customer.SUFFIX,
}

# Workflow-facing names; no prompt text is defined in workflow.py.
CONTROL_SYSTEM = control.CONTROL_SYSTEM
CONTROL_CUSTOMER_SUFFIX = control.CONTROL_CUSTOMER_SUFFIX
CONTEXT_GATE_SYSTEM = control.CONTEXT_GATE_SYSTEM
CONTROL_FRESHNESS_SUPPLEMENT = control.FRESHNESS_SUPPLEMENT
CONTROL_MISSING_REFERENCE_SUPPLEMENT = control.MISSING_REFERENCE_SUPPLEMENT
CONTROL_AMBIGUOUS_HISTORY_SUPPLEMENT = control.AMBIGUOUS_HISTORY_SUPPLEMENT
GENERATION_INSTRUCTION = generation_base.GENERATION_INSTRUCTION
GENERATION_BASE_SUFFIX = generation_base.BASE_SUFFIX
GENERATION_FACTS_INSTRUCTION = generation_facts.INSTRUCTION
GENERATION_PLAN_INSTRUCTION = generation_plan.INSTRUCTION
GENERATION_SNAPSHOT_INSTRUCTION = generation_snapshot.INSTRUCTION
GENERATION_LIMITS_INSTRUCTION = generation_limits.INSTRUCTION
GENERATION_DELIVERY_INSTRUCTION = generation_delivery.INSTRUCTION
GENERATION_APPLICABILITY_INSTRUCTION = generation_applicability.INSTRUCTION
GENERATION_REASON_INSTRUCTION = generation_reason.INSTRUCTION
GENERATION_CAPABILITY_BASE = generation_base.CAPABILITY_BASE
GENERATION_CAPABILITY_DELIVERY = generation_base.CAPABILITY_DELIVERY
GENERATION_CAPABILITY_STOCK = generation_base.CAPABILITY_STOCK
GENERATION_CAPABILITY_PROMOTION = generation_base.CAPABILITY_PROMOTION
GENERATION_CAPABILITY_EXTERNAL = generation_base.CAPABILITY_EXTERNAL
GENERATION_CAPABILITY_RECALL = generation_base.CAPABILITY_RECALL
GENERATION_CUSTOMER_INSTRUCTION = generation_customer.INSTRUCTION
GENERATION_CUSTOMER_SUFFIX = generation_customer.SUFFIX
GENERATION_FACT_REMINDER = generation_base.FACT_REMINDER
GENERATION_CITATION_REMINDER = generation_base.CITATION_REMINDER
GENERATION_LIMITS_REMINDER = generation_base.LIMITS_REMINDER
GENERATION_USER_MESSAGE_BODY = generation_base.USER_MESSAGE_BODY
