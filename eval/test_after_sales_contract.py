"""The after-sales fact contract is frozen before any paid request."""
import json
from pathlib import Path

import pytest
from run_after_sales_service import (
    ServiceEvalError,
    build_plan,
    load_contract,
    main,
    select_cases,
)

ROOT = Path(__file__).resolve().parents[1]
EXPECTED_IDS = [
    "after-011",
    "after-012",
    "after-013",
    "after-014",
    "after-015",
    "after-101",
    "after-102",
    "after-103",
    "after-104",
    "after-105",
    "after-106",
    "after-107",
]


def test_contract_freezes_twelve_fact_cases():
    contract = load_contract()
    cases = contract["cases"]
    assert [case["case_id"] for case in cases] == EXPECTED_IDS
    assert sum(case["source"] == "dataset" for case in cases) == 5
    assert sum(case["source"] == "authored" for case in cases) == 7
    assert all(case["lane"] == "after_sales" for case in cases)
    assert all(case["order_id"] for case in cases)
    assert {case["expected_behavior"] for case in cases} == {"ANSWER", "CLARIFY", "HANDOFF"}
    assert sum(case["high_risk"] for case in cases) == 6
    assert contract["dataset"]["facts_is_mock"] is True
    assert contract["environment"]["execution_is_mock_expected"] is False
    assert contract["invariants"]["non_mock_fact_count"] == 0
    assert contract["invariants"]["evidence_count"] == 0
    assert contract["invariants"]["citation_count"] == 0
    assert contract["reviewer_kind"] == "agent"
    assert contract["human_review_completed"] is False
    assert contract["real_service_acceptance"] is False


@pytest.mark.parametrize(
    ("case_id", "behavior", "states", "conversation", "ticket"),
    [
        ("after-011", "ANSWER", ["DONE"], "AI", False),
        ("after-101", "ANSWER", ["DONE"], "AI", False),
        ("after-102", "CLARIFY", ["ASKING"], "AI", False),
        ("after-103", "CLARIFY", ["ASKING"], "AI", False),
        ("after-104", "HANDOFF", ["DONE", "FAILED"], "WAITING_HUMAN", True),
        ("after-106", "HANDOFF", ["DONE", "FAILED"], "WAITING_HUMAN", True),
        ("after-107", "HANDOFF", ["DONE", "FAILED"], "WAITING_HUMAN", True),
    ],
)
def test_case_outcomes_are_frozen(case_id, behavior, states, conversation, ticket):
    case = next(item for item in load_contract()["cases"] if item["case_id"] == case_id)
    assert case["expected_behavior"] == behavior
    assert case["expected_states"] == states
    assert case["expected_conversation_state"] == conversation
    assert case["expect_ticket"] is ticket


def test_plan_counts_cases_and_message_executions(capsys):
    assert main(["--plan"]) == 0
    plan = json.loads(capsys.readouterr().out)
    assert plan["status"] == "PLAN"
    assert plan["case_count"] == 12
    assert plan["planned_requests"] == 12
    assert plan["default_max_requests"] == 12
    assert plan["hard_max_requests"] == 24
    assert plan["request_unit"] == "customer_message_execution"
    assert plan["online_requests_made"] is False
    assert plan["expected_behaviors"] == {"ANSWER": 6, "CLARIFY": 2, "HANDOFF": 4}


def test_targeted_selection_is_part_of_the_contract():
    contract = load_contract()
    chosen = select_cases(contract, ["after-104"])
    assert [case["case_id"] for case in chosen] == ["after-104"]
    assert build_plan(contract, chosen)["planned_requests"] == 1
    with pytest.raises(ServiceEvalError, match="targeted_case_partition_invalid"):
        select_cases(contract, ["after-999"])


def test_contract_tamper_fails_closed(tmp_path):
    contract = load_contract()
    contract["cases"][0]["query"] = "tampered"
    path = tmp_path / "contract.json"
    path.write_text(json.dumps(contract, ensure_ascii=False), encoding="utf-8")
    with pytest.raises(ServiceEvalError, match="contract_query_hash_invalid"):
        load_contract(path)


def test_handoff_state_tamper_fails_closed(tmp_path):
    contract = load_contract()
    handoff = next(case for case in contract["cases"] if case["expected_behavior"] == "HANDOFF")
    handoff["expected_states"] = ["ASKING"]
    path = tmp_path / "contract.json"
    path.write_text(json.dumps(contract, ensure_ascii=False), encoding="utf-8")
    with pytest.raises(ServiceEvalError, match="contract_states_invalid"):
        load_contract(path)


def test_dataset_drift_fails_closed(tmp_path):
    source = tmp_path / "data/synthetic/commerce-lab-v1/questions.jsonl"
    source.parent.mkdir(parents=True)
    source.write_text("{}\n", encoding="utf-8")
    with pytest.raises(ServiceEvalError, match="contract_source_changed"):
        load_contract(root=tmp_path)


def test_missing_source_fails_closed(tmp_path):
    with pytest.raises(ServiceEvalError, match="contract_source_missing"):
        load_contract(root=tmp_path)
