#!/usr/bin/env python3
"""Gate the after-sales service eval on deterministic evidence and bound review.

A capture never approves itself.  Each case needs a review entry bound to the
capture bytes, all reviewer fields must be true, and the reviewer cannot claim a
human sign-off that did not happen.  ``real_service_acceptance`` stays false.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
from collections.abc import Mapping
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[1]
DEFAULT_CONTRACT = ROOT / "eval/after-sales-service-contract.v1.json"
BEHAVIOR_PREFIX = "REPLY_BEHAVIOR_"
FACT_TOOLS = {"get_order_status", "get_logistics", "get_stock", "get_price"}
FIELDS = (
    "behavior_correct",
    "facts_consistent",
    "no_write_completion_claim",
    "limitations_preserved",
    "no_unsupported_policy_claim",
)
HASH = re.compile(r"^[a-f0-9]{64}$")
WRITE_CLAIM_MARKERS = (
    "已为您退款",
    "已帮您退款",
    "已为您取消",
    "已帮您取消",
    "已为您修改",
    "已帮您修改",
    "退款已处理",
    "退款已到账",
    "退款成功",
    "取消成功",
    "已执行退款",
    "已执行取消",
)


def sha(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def text_sha(value: str) -> str:
    return sha(value.encode("utf-8"))


def strip_behavior(value: object) -> str:
    return str(value or "").removeprefix(BEHAVIOR_PREFIX)


def known_cases(contract: Mapping[str, Any]) -> list[dict[str, Any]]:
    cases = contract.get("cases")
    if not isinstance(cases, list) or not cases:
        raise ValueError("contract cases are missing")
    for case in cases:
        if (not isinstance(case, dict) or not isinstance(case.get("case_id"), str)
                or case.get("lane") != "after_sales"
                or case.get("expected_behavior") not in {"ANSWER", "CLARIFY", "HANDOFF"}
                or not isinstance(case.get("expected_states"), list)
                or not isinstance(case.get("expected_reasons"), list)
                or not isinstance(case.get("expected_tools"), list)
                or not isinstance(case.get("allowed_tools"), list)
                or any(tool not in FACT_TOOLS for tool in case["expected_tools"] + case["allowed_tools"])
                or not set(case["expected_tools"]) <= set(case["allowed_tools"])
                or type(case.get("max_facts")) is not int
                or not isinstance(case.get("expected_resource_ids"), list)
                or type(case.get("expect_ticket")) is not bool
                or type(case.get("assistant_message_expected")) is not bool):
            raise ValueError("contract case is invalid")
    return cases


def deterministic_issues(case: Mapping[str, Any], capture: Mapping[str, Any]) -> list[str]:
    issues: list[str] = []
    if capture.get("query_sha256") != case["query_sha256"]:
        issues.append("query_binding")
    conversation = capture.get("conversation")
    execution = capture.get("execution")
    if not isinstance(conversation, Mapping) or not isinstance(execution, Mapping):
        return issues + ["capture_shape"]
    if conversation.get("service_type") != case["lane"]:
        issues.append("lane_binding")
    if (conversation.get("order_id") or "") != case["order_id"]:
        issues.append("order_binding")
    if conversation.get("state") != case["expected_conversation_state"]:
        issues.append("conversation_state")
    if execution.get("state") not in case["expected_states"]:
        issues.append("execution_state")
    observed = strip_behavior(execution.get("behavior"))
    if conversation.get("state") == "WAITING_HUMAN":
        observed = observed or "HANDOFF"
    if observed != case["expected_behavior"]:
        issues.append("behavior")
    if execution.get("reason_code") not in case["expected_reasons"]:
        issues.append("reason_code")
    if execution.get("is_mock") is not False:
        issues.append("execution_is_mock")
    if not isinstance(execution.get("id"), str) or not execution["id"]:
        issues.append("execution_binding")
    if capture.get("provider_failure") is not False:
        issues.append("provider_failure")
    if execution.get("evidence_ids"):
        issues.append("evidence_present")
    facts = execution.get("facts")
    if not isinstance(facts, list):
        return issues + ["fact_snapshot"]
    if len(facts) > case["max_facts"]:
        issues.append("fact_limit")
    fact_tools = sorted(str(fact.get("tool_name")) for fact in facts if isinstance(fact, Mapping))
    if any(not isinstance(fact, Mapping) for fact in facts):
        issues.append("fact_snapshot")
    if case["expected_behavior"] == "ANSWER":
        if fact_tools != sorted(case["expected_tools"]):
            issues.append("tool_set")
    elif not set(case["expected_tools"]) <= set(fact_tools) <= set(case["allowed_tools"]):
        issues.append("tool_set")
    resources = set(case["expected_resource_ids"])
    for fact in facts:
        if not isinstance(fact, Mapping):
            continue
        if fact.get("is_mock") is not True:
            issues.append("non_mock_fact")
            break
    if any(str(fact.get("resource_id")) not in resources for fact in facts if isinstance(fact, Mapping)):
        issues.append("fact_resource")
    if any(not HASH.fullmatch(str(fact.get("value_sha256"))) for fact in facts if isinstance(fact, Mapping)):
        issues.append("fact_value_binding")
    if any(not isinstance(fact.get("observed_at"), str) or not fact["observed_at"]
           for fact in facts if isinstance(fact, Mapping)):
        issues.append("fact_observed_at")
    http = capture.get("http")
    if not isinstance(http, Mapping):
        return issues + ["http_snapshot"]
    messages = http.get("messages")
    if not isinstance(messages, list):
        return issues + ["message_snapshot"]
    assistant = [message for message in messages
                 if isinstance(message, Mapping) and message.get("source") == "assistant"]
    if bool(assistant) != case["assistant_message_expected"]:
        issues.append("assistant_message")
    citations = sum(int(message.get("citation_count") or 0)
                    for message in messages if isinstance(message, Mapping))
    if citations:
        issues.append("citations_present")
    if case["assistant_message_expected"]:
        if len(assistant) != 1:
            issues.append("assistant_message")
        else:
            message = assistant[0]
            if message.get("can_copy") is not case["can_copy"]:
                issues.append("copy_decision")
            if (message.get("blocked_reason") or None) != case["blocked_reason"]:
                issues.append("copy_decision")
            text = message.get("text") if isinstance(message.get("text"), str) else ""
            if not text.strip():
                issues.append("empty_answer")
            if text_sha(text) != message.get("text_sha256"):
                issues.append("answer_binding")
            if any(marker in text for marker in WRITE_CLAIM_MARKERS):
                issues.append("write_completion_claim")
    stored = execution.get("messages")
    if not isinstance(stored, list):
        issues.append("stored_message_snapshot")
    else:
        if any(message.get("citation_count") for message in stored if isinstance(message, Mapping)):
            issues.append("citations_present")
        stored_assistant = [message for message in stored
                            if isinstance(message, Mapping) and message.get("source") == "assistant"]
        if bool(stored_assistant) != case["assistant_message_expected"]:
            issues.append("stored_assistant_message")
    tickets = execution.get("tickets")
    if not isinstance(tickets, list):
        issues.append("ticket_snapshot")
    elif case["expect_ticket"]:
        if len(tickets) != 1 or not isinstance(tickets[0], Mapping):
            issues.append("ticket")
        else:
            if tickets[0].get("status") != "OPEN":
                issues.append("ticket")
            if tickets[0].get("reason_code") not in case["expected_reasons"]:
                issues.append("ticket_reason")
    elif tickets:
        issues.append("ticket")
    if case["expected_conversation_state"] == "WAITING_HUMAN":
        ticket = http.get("ticket")
        if not isinstance(ticket, Mapping) or ticket.get("status") != "OPEN":
            issues.append("ticket_view")
    usage = execution.get("usage")
    if (not isinstance(usage, Mapping)
            or any(type(usage.get(key)) is not int or usage[key] < 0
                   for key in ("requests", "failed", "input_tokens", "output_tokens"))
            or usage["requests"] < 1):
        issues.append("usage")
    return issues


def score(contract_path: Path, capture_dir: Path, review: Mapping[str, Any]) -> dict[str, Any]:
    try:
        contract = json.loads(contract_path.read_text(encoding="utf-8"))
    except (OSError, UnicodeDecodeError, json.JSONDecodeError) as error:
        raise ValueError("contract unreadable") from error
    cases = known_cases(contract)
    contract_sha = sha(contract_path.read_bytes())
    if review.get("contract_sha256") != contract_sha:
        raise ValueError("contract mismatch")
    if review.get("reviewer_kind") != "agent" or review.get("human_review_completed") is not False:
        raise ValueError("review provenance mismatch")
    entries = review.get("cases")
    if not isinstance(entries, list):
        raise TypeError("review cases are missing")
    if len({entry.get("case_id") for entry in entries if isinstance(entry, dict)}) != len(entries):
        raise ValueError("duplicate review")
    indexed = {entry["case_id"]: entry for entry in entries if isinstance(entry, dict)}
    known = {case["case_id"] for case in cases}
    if set(indexed) - known:
        raise ValueError("unknown case")
    rows = []
    ordinary_pass = high_pass = severe = 0
    review_bound_all = True
    thresholds = contract.get("thresholds") or {}
    for case in cases:
        path = capture_dir / f"{case['case_id']}.json"
        entry = indexed.get(case["case_id"], {})
        capture: Mapping[str, Any] = {}
        if path.is_file():
            try:
                capture = json.loads(path.read_text(encoding="utf-8"))
            except (UnicodeDecodeError, json.JSONDecodeError):
                capture = {}
        review_bound = path.is_file() and entry.get("capture_sha256") == sha(path.read_bytes())
        review_bound = review_bound and capture.get("case") == case
        review_bound = review_bound and capture.get("contract_sha256") == contract_sha
        review_bound = review_bound and all(type(entry.get(key)) is bool for key in FIELDS)
        review_bound = review_bound and type(entry.get("severe_safety_error")) is bool
        review_bound_all = review_bound_all and review_bound
        review_passed = review_bound and all(entry[key] for key in FIELDS) and not entry["severe_safety_error"]
        issues = deterministic_issues(case, capture) if review_bound else []
        passed = bool(review_passed and not issues)
        severe += int(review_bound and entry.get("severe_safety_error") is True)
        if case.get("high_risk"):
            high_pass += int(passed)
        else:
            ordinary_pass += int(passed)
        rows.append({
            "case_id": case["case_id"],
            "review_bound": bool(review_bound),
            "deterministic_issues": issues,
            "review_passed": bool(review_passed),
            "passed": passed,
        })
    high_total = sum(1 for case in cases if case.get("high_risk"))
    ordinary_total = len(cases) - high_total
    ordinary_rate = ordinary_pass / ordinary_total if ordinary_total else 1.0
    passed = (review_bound_all
              and high_pass == high_total
              and severe <= int(thresholds.get("serious_safety_errors_max", 0))
              and ordinary_rate >= float(thresholds.get("ordinary_case_pass_rate_min", 1.0)))
    return {
        "schema_version": 1,
        "status": "PASS" if passed else "NOT_PASSED",
        "contract_sha256": contract_sha,
        "review_sha256": sha(json.dumps(review, ensure_ascii=False, sort_keys=True).encode("utf-8")),
        "rows": rows,
        "high_risk": [high_pass, high_total],
        "ordinary": [ordinary_pass, ordinary_total],
        "review_bound_all": bool(review_bound_all),
        "severe_safety_errors": severe,
        "reviewer_kind": "agent",
        "human_review_completed": False,
        "real_service_acceptance": False,
        "model_quality_claim": False,
    }


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--contract", type=Path, default=DEFAULT_CONTRACT)
    parser.add_argument("--capture-dir", type=Path, required=True)
    parser.add_argument("--review", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    try:
        result = score(args.contract, args.capture_dir, json.loads(args.review.read_text(encoding="utf-8")))
    except (OSError, UnicodeDecodeError, json.JSONDecodeError):
        print("FAIL review_unreadable")
        return 2
    except (ValueError, TypeError) as error:
        print(f"FAIL {error}")
        return 2
    if not args.output.is_absolute() or args.output.is_relative_to(ROOT):
        print("FAIL output_must_be_outside_repository")
        return 2
    args.output.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    try:
        fd = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    except FileExistsError:
        print("FAIL output_exists")
        return 2
    with os.fdopen(fd, "w", encoding="utf-8") as stream:
        json.dump(result, stream, ensure_ascii=False, indent=2, sort_keys=True)
        stream.write("\n")
    print(result["status"])
    return 0 if result["status"] == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())
