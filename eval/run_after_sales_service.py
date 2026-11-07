#!/usr/bin/env python3
"""Run the frozen after-sales service cases through the Go customer channel.

The default action is a free deterministic plan.  ``--live`` requires the
protected runtime environment of an isolated stack that serves
``shop-demo`` with customer chat enabled, the synthetic commerce facts
(``is_mock=true``) and no published knowledge.  Captures keep the answer text
for review outside Git (0600); reports keep hashes, counts and decisions only.
"""

from __future__ import annotations

import argparse
import hashlib
import http.cookiejar
import json
import os
import re
import stat
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
from collections.abc import Mapping
from pathlib import Path
from typing import Any
from uuid import uuid4

ROOT = Path(__file__).resolve().parents[1]
DEFAULT_CONTRACT = ROOT / "eval/after-sales-service-contract.v1.json"
sys.path.insert(0, str(ROOT / "eval"))
from quality_capture import secure_directory

BEHAVIOR_PREFIX = "REPLY_BEHAVIOR_"
FACT_TOOLS = {"get_order_status", "get_logistics", "get_stock", "get_price"}
FACT_PROBE_RESOURCE = "CLB-DRK-002"
CURRENCY_KEYS = {"currency", "amount", "amount_cents", "cost", "cost_cny", "cny"}
HASH = re.compile(r"^[a-f0-9]{64}$")
MAX_PROTECTED_BYTES = 1024 * 1024

CUSTOMER_DOCUMENT_COUNT_SQL = """
SELECT count(*) FROM document d
 JOIN source s ON s.tenant_id=d.tenant_id AND s.id=d.source_id
 JOIN tenant t ON t.id=d.tenant_id
 JOIN document_version v ON v.tenant_id=d.tenant_id AND v.id=d.active_version_id
 WHERE t.external_id=%s AND s.shop_id=%s AND d.status='active' AND v.status='active'
 AND v.review_status='approved' AND v.external_allowed AND v.disclosure_class='external_allowed'
 AND NOT EXISTS (SELECT 1 FROM (VALUES ('tenant',t.id),('source',s.id),('document',d.id)) gate(kind,id)
   WHERE EXISTS(SELECT 1 FROM acl a WHERE a.tenant_id=t.id AND a.resource_type=gate.kind AND a.resource_id=gate.id)
   AND NOT EXISTS(SELECT 1 FROM acl a WHERE a.tenant_id=t.id AND a.resource_type=gate.kind AND a.resource_id=gate.id
      AND a.subject_type='role' AND a.subject_id='customer' AND a.permission='read'))
"""

EXECUTION_SQL = """
SELECT state,behavior,reason_code,answer,evidence_json,facts_json,is_mock,finished_at
FROM customer_execution WHERE id=%s AND conversation_id=%s
"""

MESSAGE_SQL = """
SELECT id,source,content,citations_json,execution_id FROM customer_message
WHERE tenant_id=(SELECT tenant_id FROM customer_execution WHERE id=%s)
AND conversation_id=%s ORDER BY created_at,id
"""

TICKET_SQL = """
SELECT id,status,reason_code FROM customer_ticket
WHERE conversation_id=%s ORDER BY created_at,id
"""

USAGE_SQL = """
SELECT count(*),count(*) FILTER (WHERE error_code IS NOT NULL),
 coalesce(sum(input_tokens),0),coalesce(sum(output_tokens),0)
FROM usage_log WHERE request_id=%s
"""


class ServiceEvalError(ValueError):
    """A fixed, non-sensitive diagnostic code may leave this process."""


def digest(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def text_digest(value: str) -> str:
    return digest(value.encode("utf-8"))


def strip_behavior(value: object) -> str:
    return str(value or "").removeprefix(BEHAVIOR_PREFIX)


def case_digest(case: Mapping[str, Any]) -> str:
    return digest(json.dumps(case, ensure_ascii=False, sort_keys=True).encode("utf-8"))


def load_contract(path: Path = DEFAULT_CONTRACT, root: Path = ROOT) -> dict[str, Any]:
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeDecodeError, json.JSONDecodeError) as error:
        raise ServiceEvalError("contract_unreadable") from error
    if not isinstance(data, dict) or data.get("schema_version") != 1:
        raise ServiceEvalError("contract_invalid")
    if data.get("contract_id") != "after-sales-service-eval-v1":
        raise ServiceEvalError("contract_identity_invalid")
    source_files = data.get("source_files")
    if not isinstance(source_files, dict) or not source_files:
        raise ServiceEvalError("contract_sources_missing")
    for name, checksum in source_files.items():
        if not isinstance(name, str) or not isinstance(checksum, str) or not HASH.fullmatch(checksum):
            raise ServiceEvalError("contract_source_hash_invalid")
        try:
            actual = digest((root / name).read_bytes())
        except OSError as error:
            raise ServiceEvalError("contract_source_missing") from error
        if actual != checksum:
            raise ServiceEvalError("contract_source_changed")
    dataset = data.get("dataset")
    environment = data.get("environment")
    if not isinstance(dataset, dict) or dataset.get("facts_is_mock") is not True:
        raise ServiceEvalError("contract_dataset_invalid")
    if dataset.get("customer_chat_enabled") is not True or dataset.get("published_knowledge_expected") is not False:
        raise ServiceEvalError("contract_dataset_invalid")
    if not isinstance(environment, dict) or environment.get("profile") != "v2_langchain_bailian":
        raise ServiceEvalError("contract_environment_invalid")
    if environment.get("real_provider_required") is not True or environment.get("execution_is_mock_expected") is not False:
        raise ServiceEvalError("contract_environment_invalid")
    invariants = data.get("invariants")
    if not isinstance(invariants, dict) or invariants.get("evidence_count", 0) != 0 or invariants.get("citation_count", 0) != 0:
        raise ServiceEvalError("contract_invariants_invalid")
    if invariants.get("non_mock_fact_count", 0) != 0 or invariants.get("execution_is_mock") is not False:
        raise ServiceEvalError("contract_invariants_invalid")
    if invariants.get("handoff_states") != ["DONE", "FAILED"]:
        raise ServiceEvalError("contract_invariants_invalid")
    if invariants.get("handoff_requires_open_ticket") is not True or invariants.get("handoff_requires_no_assistant_message") is not True:
        raise ServiceEvalError("contract_invariants_invalid")
    review = data.get("review")
    if not isinstance(review, dict) or review.get("reviewer_kind") != "agent":
        raise ServiceEvalError("contract_review_invalid")
    if review.get("human_review_completed") is not False or not isinstance(review.get("severe_field"), str):
        raise ServiceEvalError("contract_review_invalid")
    fields = review.get("fields")
    if not isinstance(fields, list) or len(fields) != 5 or not all(isinstance(item, str) and item for item in fields):
        raise ServiceEvalError("contract_review_invalid")
    thresholds = data.get("thresholds")
    if not isinstance(thresholds, dict) or thresholds.get("ordinary_case_pass_rate_min") != 0.9:
        raise ServiceEvalError("contract_thresholds_invalid")
    if thresholds.get("high_risk_all_pass_required") is not True or thresholds.get("serious_safety_errors_max") != 0:
        raise ServiceEvalError("contract_thresholds_invalid")
    if thresholds.get("review_bound_all_required") is not True:
        raise ServiceEvalError("contract_thresholds_invalid")
    cases = data.get("cases")
    if not isinstance(cases, list) or not cases:
        raise ServiceEvalError("contract_cases_missing")
    line_index = _questions_line_index(root, source_files)
    seen: set[str] = set()
    for case in cases:
        if not isinstance(case, dict):
            raise ServiceEvalError("contract_case_invalid")
        identifier = case.get("case_id")
        if not isinstance(identifier, str) or not identifier or identifier in seen:
            raise ServiceEvalError("contract_case_id_invalid")
        seen.add(identifier)
        query = case.get("query")
        if not isinstance(query, str) or not query.strip():
            raise ServiceEvalError("contract_query_invalid")
        if case.get("query_sha256") != text_digest(query):
            raise ServiceEvalError("contract_query_hash_invalid")
        if case.get("lane") != "after_sales" or not isinstance(case.get("order_id"), str):
            raise ServiceEvalError("contract_lane_invalid")
        behavior = case.get("expected_behavior")
        if behavior not in {"ANSWER", "CLARIFY", "HANDOFF"}:
            raise ServiceEvalError("contract_behavior_invalid")
        states = case.get("expected_states")
        if not isinstance(states, list) or not states or any(
                state not in {"DONE", "FAILED", "ASKING"} for state in states):
            raise ServiceEvalError("contract_states_invalid")
        if behavior == "ANSWER" and states != ["DONE"]:
            raise ServiceEvalError("contract_states_invalid")
        if behavior == "CLARIFY" and states != ["ASKING"]:
            raise ServiceEvalError("contract_states_invalid")
        if behavior == "HANDOFF" and not set(states) <= {"DONE", "FAILED"}:
            raise ServiceEvalError("contract_states_invalid")
        reason_state = {"ANSWER": "DONE", "CLARIFY": "ASKING", "HANDOFF": "DONE"}[behavior]
        if reason_state not in states:
            raise ServiceEvalError("contract_states_invalid")
        conversation_state = case.get("expected_conversation_state")
        if behavior == "HANDOFF" and conversation_state != "WAITING_HUMAN":
            raise ServiceEvalError("contract_states_invalid")
        if behavior != "HANDOFF" and conversation_state != "AI":
            raise ServiceEvalError("contract_states_invalid")
        reasons = case.get("expected_reasons")
        if not isinstance(reasons, list) or not reasons or not all(isinstance(r, str) and r for r in reasons):
            raise ServiceEvalError("contract_reasons_invalid")
        for key in ("expected_tools", "allowed_tools"):
            tools = case.get(key)
            if not isinstance(tools, list) or any(tool not in FACT_TOOLS for tool in tools):
                raise ServiceEvalError("contract_tools_invalid")
        if not set(case["expected_tools"]) <= set(case["allowed_tools"]):
            raise ServiceEvalError("contract_tools_invalid")
        if type(case.get("max_facts")) is not int or case["max_facts"] < 0:
            raise ServiceEvalError("contract_fact_limit_invalid")
        resources = case.get("expected_resource_ids")
        if not isinstance(resources, list) or any(not isinstance(item, str) or not item for item in resources):
            raise ServiceEvalError("contract_resources_invalid")
        if type(case.get("assistant_message_expected")) is not bool:
            raise ServiceEvalError("contract_flags_invalid")
        if type(case.get("high_risk")) is not bool or type(case.get("expect_ticket")) is not bool:
            raise ServiceEvalError("contract_flags_invalid")
        if type(case.get("write_request")) is not bool:
            raise ServiceEvalError("contract_flags_invalid")
        if behavior == "HANDOFF":
            if case["assistant_message_expected"] or not case["expect_ticket"]:
                raise ServiceEvalError("contract_handoff_invalid")
            if case.get("can_copy") is not None or case.get("blocked_reason") is not None:
                raise ServiceEvalError("contract_handoff_invalid")
        else:
            if not case["assistant_message_expected"] or case["expect_ticket"]:
                raise ServiceEvalError("contract_reply_invalid")
            if type(case.get("can_copy")) is not bool or not isinstance(case.get("blocked_reason"), str):
                raise ServiceEvalError("contract_reply_invalid")
        if case.get("source") == "dataset":
            if case.get("source_line_sha256") is None:
                raise ServiceEvalError("contract_dataset_binding_invalid")
            bound = line_index.get(str(case.get("source_case_id")))
            if bound is None or bound[0] != case["source_line_sha256"]:
                raise ServiceEvalError("contract_dataset_binding_invalid")
            row = bound[1]
            if (row.get("query") != query or row.get("order_id") != case["order_id"]
                    or row.get("expected_tools") != case["expected_tools"]):
                raise ServiceEvalError("contract_dataset_binding_invalid")
        elif case.get("source") == "authored":
            if case.get("source_case_id") is not None or case.get("source_line_sha256") is not None:
                raise ServiceEvalError("contract_dataset_binding_invalid")
        else:
            raise ServiceEvalError("contract_source_kind_invalid")
    policy = data.get("request_policy")
    if not isinstance(policy, dict) or policy.get("case_repeats") != 1 or policy.get("turns_per_case") != 1:
        raise ServiceEvalError("contract_request_policy_invalid")
    if policy.get("planned_requests") != len(cases) or policy.get("default_max_requests") != len(cases):
        raise ServiceEvalError("contract_request_policy_invalid")
    if type(policy.get("hard_max_requests")) is not int or policy["hard_max_requests"] < len(cases):
        raise ServiceEvalError("contract_request_policy_invalid")
    if not isinstance(policy.get("min_interval_seconds"), (int, float)) or policy["min_interval_seconds"] <= 0:
        raise ServiceEvalError("contract_request_policy_invalid")
    if data.get("real_service_acceptance") is not False or data.get("human_review_completed") is not False:
        raise ServiceEvalError("contract_acceptance_claim_invalid")
    return data


def _questions_line_index(root: Path, source_files: Mapping[str, Any]) -> dict[str, tuple[str, dict[str, Any]]]:
    name = "data/synthetic/commerce-lab-v1/questions.jsonl"
    if name not in source_files:
        raise ServiceEvalError("contract_dataset_binding_invalid")
    try:
        lines = (root / name).read_bytes().splitlines(keepends=True)
    except OSError as error:
        raise ServiceEvalError("contract_source_missing") from error
    index: dict[str, tuple[str, dict[str, Any]]] = {}
    for raw in lines:
        try:
            row = json.loads(raw)
        except (UnicodeDecodeError, json.JSONDecodeError) as error:
            raise ServiceEvalError("contract_dataset_binding_invalid") from error
        if not isinstance(row, dict) or not isinstance(row.get("case_id"), str):
            raise ServiceEvalError("contract_dataset_binding_invalid")
        index[row["case_id"]] = (digest(raw), row)
    return index


def select_cases(contract: Mapping[str, Any], selected: list[str] | None) -> list[dict[str, Any]]:
    cases = [dict(case) for case in contract["cases"]]
    if not selected:
        return cases
    wanted = set(selected)
    known = {case["case_id"] for case in cases}
    if not wanted <= known:
        raise ServiceEvalError("targeted_case_partition_invalid")
    return [case for case in cases if case["case_id"] in wanted]


def build_plan(contract: Mapping[str, Any], cases: list[Mapping[str, Any]]) -> dict[str, Any]:
    behaviors: dict[str, int] = {}
    for case in cases:
        key = str(case["expected_behavior"])
        behaviors[key] = behaviors.get(key, 0) + 1
    policy = contract["request_policy"]
    return {
        "status": "PLAN",
        "contract_id": contract["contract_id"],
        "case_count": len(cases),
        "planned_requests": len(cases) * int(policy["turns_per_case"]),
        "case_repeats": policy["case_repeats"],
        "turns_per_case": policy["turns_per_case"],
        "default_max_requests": len(cases) * int(policy["turns_per_case"]),
        "hard_max_requests": int(policy["hard_max_requests"]),
        "min_interval_seconds": float(policy["min_interval_seconds"]),
        "case_ids": [case["case_id"] for case in cases],
        "lanes": {"after_sales": len(cases)},
        "expected_behaviors": dict(sorted(behaviors.items())),
        "request_unit": "customer_message_execution",
        "real_service_acceptance": False,
        "online_requests_made": False,
    }


def load_protected_env(path: Path) -> dict[str, str]:
    try:
        info = path.lstat()
    except OSError as error:
        raise ServiceEvalError("protected_runtime_env_required") from error
    if (not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o600
            or info.st_uid != os.getuid() or info.st_size > MAX_PROTECTED_BYTES):
        raise ServiceEvalError("protected_runtime_env_required")
    values: dict[str, str] = {}
    try:
        lines = path.read_text(encoding="utf-8").splitlines()
    except (OSError, UnicodeDecodeError) as error:
        raise ServiceEvalError("protected_runtime_env_unreadable") from error
    for line in lines:
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        key, separator, value = line.partition("=")
        key = key.strip()
        if not separator or not re.fullmatch(r"[A-Z][A-Z0-9_]*", key) or key in values:
            raise ServiceEvalError("protected_runtime_env_invalid")
        values[key] = value.strip().strip("\"'")
    for required in ("V2_GATEWAY_DSN", "HTTP_ADDR", "RAG_PROFILE", "V2_CUSTOMER_ORIGIN"):
        if not values.get(required):
            raise ServiceEvalError("protected_runtime_env_invalid")
    return values


def loopback_http(value: str, *, allow_path: bool = False) -> str:
    try:
        parsed = urllib.parse.urlsplit(value)
    except ValueError as error:
        raise ServiceEvalError("loopback_http_required") from error
    if (parsed.scheme != "http" or parsed.hostname != "127.0.0.1" or parsed.port is None
            or parsed.username is not None or parsed.password is not None
            or parsed.fragment or parsed.query or (not allow_path and parsed.path not in ("", "/"))):
        raise ServiceEvalError("loopback_http_required")
    return value.rstrip("/")


def assert_no_currency(payload: Any) -> None:
    if isinstance(payload, Mapping):
        for key, value in payload.items():
            if str(key).lower() in CURRENCY_KEYS or "currency" in str(key).lower():
                raise ServiceEvalError("currency_field_forbidden")
            assert_no_currency(value)
    elif isinstance(payload, list):
        for value in payload:
            assert_no_currency(value)


class RequestBudget:
    """Hard cap on customer message executions; a failed call is still spent."""

    def __init__(self, max_requests: int) -> None:
        if type(max_requests) is not int or max_requests <= 0:
            raise ServiceEvalError("request_limit_invalid")
        self.max_requests = max_requests
        self.used = 0

    def reserve(self, operation: str) -> None:
        if self.used + 1 > self.max_requests:
            raise ServiceEvalError(f"request_budget_exceeded:{operation}")
        self.used += 1


class CustomerClient:
    def __init__(self, base_url: str, origin: str, *, opener=None) -> None:
        self.base = loopback_http(base_url)
        self.origin = origin
        if opener is None:
            opener = urllib.request.build_opener(
                urllib.request.ProxyHandler({}),
                urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()),
            )
        self.opener = opener

    def call(self, method: str, path: str, body: Mapping[str, Any] | None = None, *,
             csrf: str = "", key: str | None = None, timeout: float = 20.0) -> Any:
        data = None if body is None else json.dumps(body, ensure_ascii=False).encode("utf-8")
        headers = {"Origin": self.origin, "Accept": "application/json"}
        if data is not None:
            headers["Content-Type"] = "application/json"
            if key:
                headers["Idempotency-Key"] = key
        if csrf:
            headers["X-CSRF-Token"] = csrf
        request = urllib.request.Request(self.base + path, data=data, headers=headers, method=method)
        try:
            with self.opener.open(request, timeout=timeout) as response:
                return json.load(response)
        except urllib.error.HTTPError as error:
            raise ServiceEvalError(f"http_{error.code}") from None
        except (OSError, ValueError, json.JSONDecodeError) as error:
            raise ServiceEvalError(f"transport_{type(error).__name__}") from None

    def session(self, shop: str) -> str:
        value = self.call("POST", "/v1/customer/session", {"shop_slug": shop})
        token = value.get("csrf_token") if isinstance(value, Mapping) else None
        if not isinstance(token, str) or not token:
            raise ServiceEvalError("customer_session_invalid")
        return token

    def create_conversation(self, case: Mapping[str, Any], csrf: str, run_id: str) -> dict[str, Any]:
        body: dict[str, Any] = {"service_type": case["lane"]}
        if case["lane"] == "after_sales":
            body["order_id"] = case["order_id"]
        value = self.call("POST", "/v1/customer/conversations", body, csrf=csrf,
                          key=f"as-conv-{run_id}-{case['case_id']}")
        if not isinstance(value, Mapping) or not isinstance(value.get("conversation_id"), str):
            raise ServiceEvalError("conversation_response_invalid")
        if value.get("service_type") != case["lane"] or (value.get("order_id") or "") != case["order_id"]:
            raise ServiceEvalError("conversation_lane_mismatch")
        return dict(value)

    def send(self, conversation_id: str, query: str, csrf: str, run_id: str, case_id: str) -> dict[str, Any]:
        value = self.call("POST", f"/v1/customer/conversations/{conversation_id}/messages",
                          {"text": query}, csrf=csrf, key=f"as-turn-{run_id}-{case_id}")
        if not isinstance(value, Mapping) or not isinstance(value.get("execution_id"), str) or not value["execution_id"]:
            raise ServiceEvalError("message_response_invalid")
        return dict(value)

    def snapshot(self, conversation_id: str) -> dict[str, Any]:
        value = self.call("GET", f"/v1/customer/conversations/{conversation_id}")
        if not isinstance(value, Mapping):
            raise ServiceEvalError("conversation_snapshot_invalid")
        return dict(value)

    def events(self, conversation_id: str, execution_id: str, timeout: float = 30.0) -> list[dict[str, Any]]:
        request = urllib.request.Request(
            self.base + f"/v1/customer/conversations/{conversation_id}/events",
            headers={"Origin": self.origin, "Accept": "text/event-stream"},
        )
        try:
            with self.opener.open(request, timeout=timeout) as response:
                if response.headers.get_content_type() != "text/event-stream":
                    raise ServiceEvalError("sse_content_type_invalid")
                events: list[dict[str, Any]] = []
                current: dict[str, Any] = {}
                for raw in response:
                    line = raw.decode("utf-8").rstrip("\r\n")
                    if not line:
                        if current:
                            events.append(current)
                            current = {}
                            if terminal_event(events, execution_id):
                                break
                        continue
                    field, _, value = line.partition(":")
                    if field == "id":
                        current["id"] = value.lstrip()
                    elif field == "event":
                        current["event"] = value.lstrip()
                    elif field == "data":
                        current["data"] = json.loads(value.lstrip())
                if current:
                    events.append(current)
                return events
        except urllib.error.HTTPError as error:
            raise ServiceEvalError(f"sse_http_{error.code}") from None
        except (OSError, UnicodeDecodeError, json.JSONDecodeError) as error:
            raise ServiceEvalError(f"sse_transport_{type(error).__name__}") from None


def wait_for_completion(client: CustomerClient, conversation_id: str, timeout_s: float) -> dict[str, Any]:
    deadline = time.monotonic() + timeout_s
    while time.monotonic() < deadline:
        snapshot = client.snapshot(conversation_id)
        conversation = snapshot.get("conversation") or {}
        messages = snapshot.get("messages") or []
        if not conversation.get("active_execution_id") and any(
                isinstance(message, Mapping) and message.get("source") != "customer" for message in messages):
            return snapshot
        time.sleep(0.3)
    raise ServiceEvalError("execution_timeout")


def terminal_event(events: list[Mapping[str, Any]], execution_id: str) -> bool:
    for event in events:
        data = event.get("data")
        if not isinstance(data, Mapping):
            continue
        if event.get("event") == "completed" and data.get("execution_id") == execution_id:
            return True
        if event.get("event") == "state" and data.get("state") == "WAITING_HUMAN":
            return True
    return False


def sanitize_events(events: list[Mapping[str, Any]]) -> list[dict[str, Any]]:
    result = []
    for event in events:
        data = event.get("data")
        if isinstance(data, Mapping):
            clean = dict(data)
            message = clean.get("message")
            if isinstance(message, Mapping):
                text = message.get("text") if isinstance(message.get("text"), str) else ""
                clean["message"] = {
                    "message_id": message.get("id"),
                    "source": message.get("source"),
                    "text_sha256": text_digest(text),
                    "citation_count": len(message.get("citations") or []),
                    "can_copy": message.get("can_copy"),
                    "blocked_reason": message.get("blocked_reason"),
                }
        else:
            clean = data
        result.append({"id": event.get("id"), "event": event.get("event"), "data": clean})
    return result


def public_messages(snapshot: Mapping[str, Any]) -> list[dict[str, Any]]:
    result = []
    for message in snapshot.get("messages") or []:
        if not isinstance(message, Mapping):
            continue
        text = message.get("text") if isinstance(message.get("text"), str) else ""
        citations = message.get("citations") if isinstance(message.get("citations"), list) else []
        result.append({
            "id": message.get("id"),
            "source": message.get("source"),
            "text": text,
            "text_sha256": text_digest(text),
            "citation_ids": [item.get("evidence_id") for item in citations if isinstance(item, Mapping)],
            "citation_count": len(citations),
            "can_copy": message.get("can_copy"),
            "blocked_reason": message.get("blocked_reason"),
            "created_at": message.get("created_at"),
        })
    return result


def read_customer_records(values: Mapping[str, str], conversation_id: str, execution_id: str,
                          connect=None) -> dict[str, Any]:
    """Read the persisted execution, messages, tickets and usage without writes."""
    if connect is None:
        import psycopg
        connect = psycopg.connect
    try:
        with connect(values["V2_GATEWAY_DSN"], connect_timeout=5) as conn:
            conn.execute("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY")
            conn.execute("SET LOCAL statement_timeout = '5s'")
            row = conn.execute(EXECUTION_SQL, (execution_id, conversation_id)).fetchone()
            if row is None:
                raise ServiceEvalError("execution_record_missing")
            state, behavior, reason, answer, evidence_raw, facts_raw, is_mock, finished_at = row
            messages = conn.execute(MESSAGE_SQL, (execution_id, conversation_id)).fetchall()
            tickets = conn.execute(TICKET_SQL, (conversation_id,)).fetchall()
            usage = conn.execute(USAGE_SQL, (execution_id,)).fetchone()
    except ServiceEvalError:
        raise
    except Exception as error:
        raise ServiceEvalError("customer_records_unavailable") from error
    try:
        evidence = json.loads(evidence_raw or "[]")
        facts = json.loads(facts_raw or "[]")
    except (TypeError, json.JSONDecodeError) as error:
        raise ServiceEvalError("execution_snapshot_invalid") from error
    if not isinstance(evidence, list) or not isinstance(facts, list):
        raise ServiceEvalError("execution_snapshot_invalid")
    fact_rows = []
    for fact in facts:
        if not isinstance(fact, Mapping):
            raise ServiceEvalError("execution_snapshot_invalid")
        value_json = fact.get("value_json")
        if not isinstance(value_json, str):
            raise ServiceEvalError("execution_snapshot_invalid")
        fact_rows.append({
            "tool_name": fact.get("tool_name"),
            "resource_id": fact.get("resource_id"),
            "observed_at": fact.get("observed_at"),
            "is_mock": bool(fact.get("is_mock", False)),
            "value_sha256": digest(value_json.encode("utf-8")),
        })
    message_rows = []
    for message_id, source, content, citations_raw, message_execution in messages:
        try:
            citations = json.loads(citations_raw or "[]")
        except (TypeError, json.JSONDecodeError) as error:
            raise ServiceEvalError("message_snapshot_invalid") from error
        if not isinstance(citations, list):
            raise ServiceEvalError("message_snapshot_invalid")
        message_rows.append({
            "id": message_id,
            "source": source,
            "content_sha256": text_digest(content or ""),
            "citation_ids": [item.get("id") for item in citations if isinstance(item, Mapping)],
            "citation_count": len(citations),
            "execution_id": message_execution,
        })
    return {
        "id": execution_id,
        "state": state,
        "behavior": behavior,
        "reason_code": reason,
        "answer_sha256": text_digest(answer or ""),
        "is_mock": bool(is_mock),
        "evidence_ids": [item.get("id") for item in evidence if isinstance(item, Mapping)],
        "facts": fact_rows,
        "messages": message_rows,
        "tickets": [{"id": ticket[0], "status": ticket[1], "reason_code": ticket[2]} for ticket in tickets],
        "usage": {
            "requests": int(usage[0] or 0),
            "failed": int(usage[1] or 0),
            "input_tokens": int(usage[2] or 0),
            "output_tokens": int(usage[3] or 0),
        },
        "finished_at": finished_at.isoformat() if hasattr(finished_at, "isoformat") else None,
    }


def provider_failure(capture: Mapping[str, Any]) -> bool:
    reason = str((capture.get("execution") or {}).get("reason_code") or "")
    if reason.startswith("provider_failed"):
        return True
    return any(
        isinstance(event.get("data"), Mapping)
        and str(event["data"].get("reason_code", "")).startswith("provider_failed")
        for event in capture.get("sse") or []
    )


def run_case(client: CustomerClient, case: Mapping[str, Any], *, contract_sha256: str, shop: str,
             budget: RequestBudget, timeout_s: float, capture_dir: Path, run_id: str,
             reader) -> dict[str, Any]:
    started = time.monotonic()
    csrf = client.session(shop)
    conversation = client.create_conversation(case, csrf, run_id)
    conversation_id = conversation["conversation_id"]
    budget.reserve(f"message:{case['case_id']}")
    submit = client.send(conversation_id, case["query"], csrf, run_id, case["case_id"])
    execution_id = submit["execution_id"]
    snapshot = wait_for_completion(client, conversation_id, timeout_s)
    events = client.events(conversation_id, execution_id)
    if not terminal_event(events, execution_id):
        raise ServiceEvalError("missing_terminal_event")
    execution = reader(conversation_id, execution_id)
    conversation_state = snapshot.get("conversation") or {}
    capture = {
        "capture_version": "after-sales-service-capture-v1",
        "contract_sha256": contract_sha256,
        "case": case,
        "query_sha256": case["query_sha256"],
        "conversation": {
            "id": conversation_id,
            "state": conversation_state.get("state"),
            "service_type": conversation_state.get("service_type"),
            "order_id": conversation_state.get("order_id") or "",
            "epoch": conversation_state.get("epoch"),
        },
        "http": {
            "messages": public_messages(snapshot),
            "ticket": snapshot.get("ticket"),
            "events": sanitize_events(events),
        },
        "execution": execution,
        "elapsed_ms": round((time.monotonic() - started) * 1000),
    }
    capture["provider_failure"] = provider_failure(capture)
    write_capture(capture_dir, str(case["case_id"]), capture)
    return capture


def write_capture(directory: Path, case_id: str, payload: Mapping[str, Any]) -> str:
    try:
        secure_directory(directory)
    except ValueError as error:
        raise ServiceEvalError(str(error)) from None
    body = (json.dumps(payload, ensure_ascii=False, sort_keys=True) + "\n").encode("utf-8")
    path = directory / f"{case_id}.json"
    try:
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    except FileExistsError as error:
        raise ServiceEvalError("capture_already_exists") from error
    with os.fdopen(fd, "wb") as stream:
        stream.write(body)
        stream.flush()
        os.fsync(stream.fileno())
    return digest(body)


def row_from_capture(capture: Mapping[str, Any]) -> dict[str, Any]:
    case = capture["case"]
    execution = capture["execution"]
    messages = capture.get("http", {}).get("messages") or []
    assistant = [message for message in messages if message.get("source") == "assistant"]
    facts = execution.get("facts") or []
    tickets = execution.get("tickets") or []
    customer_state = capture["conversation"].get("state")
    behavior = strip_behavior(execution.get("behavior"))
    observed_behavior = behavior or ("HANDOFF" if customer_state == "WAITING_HUMAN" else "")
    return {
        "case_id": case["case_id"],
        "lane": case["lane"],
        "order_id": case["order_id"],
        "case_sha256": case_digest(case),
        "query_sha256": case["query_sha256"],
        "conversation_id": capture["conversation"]["id"],
        "execution_id": execution["id"],
        "state": execution.get("state"),
        "behavior": behavior,
        "observed_behavior": observed_behavior,
        "customer_state": customer_state,
        "reason_code": execution.get("reason_code"),
        "execution_is_mock": execution.get("is_mock"),
        "evidence_count": len(execution.get("evidence_ids") or []),
        "citation_count": sum(int(message.get("citation_count") or 0) for message in messages),
        "fact_count": len(facts),
        "fact_tools": sorted(str(fact.get("tool_name")) for fact in facts),
        "fact_resource_ids": sorted(str(fact.get("resource_id")) for fact in facts),
        "non_mock_fact_count": sum(1 for fact in facts if fact.get("is_mock") is not True),
        "assistant_message_present": bool(assistant),
        "answer_sha256": assistant[0]["text_sha256"] if assistant else None,
        "can_copy": assistant[0].get("can_copy") if assistant else None,
        "blocked_reason": assistant[0].get("blocked_reason") if assistant else None,
        "ticket_present": bool(tickets),
        "ticket_status": tickets[0]["status"] if tickets else None,
        "provider_failure": bool(capture.get("provider_failure")),
        "sse_event_count": len(capture.get("http", {}).get("events") or []),
        "elapsed_ms": capture.get("elapsed_ms"),
        "usage": execution.get("usage"),
        "capture_sha256": digest((json.dumps(capture, ensure_ascii=False, sort_keys=True) + "\n").encode("utf-8")),
        "review_status": "not_reviewed",
    }


def aggregate_usage(rows: list[Mapping[str, Any]]) -> dict[str, int]:
    total = {"requests": 0, "failed": 0, "input_tokens": 0, "output_tokens": 0}
    for row in rows:
        usage = row.get("usage") or {}
        for key in total:
            total[key] += int(usage.get(key) or 0)
    return total


def verify_preconditions(contract: Mapping[str, Any], values: Mapping[str, str], *, connect=None) -> dict[str, Any]:
    dataset = contract["dataset"]
    tenant, shop = dataset["tenant_external_id"], dataset["shop_id"]
    if connect is None:
        import psycopg
        connect = psycopg.connect
    try:
        with connect(values["V2_GATEWAY_DSN"], connect_timeout=5) as conn:
            conn.execute("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY")
            conn.execute("SET LOCAL statement_timeout = '5s'")
            row = conn.execute(
                "SELECT sh.customer_chat_enabled FROM tenant t JOIN shop sh ON sh.tenant_id=t.id "
                "WHERE t.external_id=%s AND sh.id=%s", (tenant, shop)).fetchone()
            if row is None:
                raise ServiceEvalError("shop_unavailable")
            if row[0] is not True:
                raise ServiceEvalError("customer_chat_disabled")
            published = conn.execute(CUSTOMER_DOCUMENT_COUNT_SQL, (tenant, shop)).fetchone()[0]
            if published != 0:
                raise ServiceEvalError("published_knowledge_present")
    except ServiceEvalError:
        raise
    except Exception as error:
        raise ServiceEvalError("preflight_database_unavailable") from error
    verify_facts_mock(contract, values)
    return {"shop": shop, "published_documents": 0, "customer_chat_enabled": True, "facts_is_mock": True}


def verify_facts_mock(contract: Mapping[str, Any], values: Mapping[str, str]) -> None:
    if not values.get("V2_FACTS_BASE_URL"):
        raise ServiceEvalError("synthetic_facts_configuration_invalid")
    base = loopback_http(values["V2_FACTS_BASE_URL"])
    version = values.get("V2_FACTS_VERSION", "")
    user = values.get("V2_IDENTITY_USER", "")
    if not version or not user:
        raise ServiceEvalError("synthetic_facts_configuration_invalid")
    request = urllib.request.Request(base + "/v1/facts/get_stock", headers={
        "X-Facts-Version": version,
        "X-Tenant-ID": contract["dataset"]["tenant_external_id"],
        "X-User-ID": user,
        "X-Shop-ID": contract["dataset"]["shop_id"],
        "X-Fact-Resource-ID": FACT_PROBE_RESOURCE,
    })
    try:
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        with opener.open(request, timeout=3) as response:
            raw = response.read(65537)
            if response.status != 200 or len(raw) > 65536:
                raise ValueError
            result = json.loads(raw)
        if (result.get("version") != version or result.get("is_mock") is not True
                or result.get("tenant_id") != contract["dataset"]["tenant_external_id"]
                or result.get("shop_id") != contract["dataset"]["shop_id"]
                or result.get("resource_id") != FACT_PROBE_RESOURCE
                or result.get("tool_name") != "get_stock"
                or result.get("value_json", {}).get("sku") != FACT_PROBE_RESOURCE):
            raise ValueError
    except Exception:  # noqa: BLE001 - transport diagnostics can include private configuration
        raise ServiceEvalError("synthetic_facts_not_ready_or_mismatched") from None


def provider_identity(values: Mapping[str, str], base_url: str) -> dict[str, Any]:
    sys.path.insert(0, str(ROOT / "python"))
    from app.graph.prompts.registry import GENERATION_INSTRUCTION
    from app.graph.workflow import CONTROL_SCHEMA, GENERATION_SCHEMA
    from app.providers.config import ensure_secure_config_file, load_provider_config

    if values.get("RAG_PROFILE") != "v2_langchain_bailian":
        raise ServiceEvalError("online_profile_required")
    if base_url.rstrip("/") != "http://" + values.get("HTTP_ADDR", ""):
        raise ServiceEvalError("runtime_identity_invalid")
    config_path = Path(values.get("V2_PROVIDER_CONFIG", ""))
    try:
        ensure_secure_config_file(config_path)
        config = load_provider_config(config_path)
    except Exception as error:
        raise ServiceEvalError("provider_config_unavailable") from error
    if config.profile != "aliyun-bailian":
        raise ServiceEvalError("online_provider_profile_invalid")
    files = (
        "eval/after-sales-service-contract.v1.json",
        "eval/run_after_sales_service.py",
        "eval/after_sales_service_quality.py",
        "go/internal/customer/ai.go",
        "go/internal/customer/service.go",
        "go/internal/customer/store.go",
        "go/internal/auth/customer.go",
        "go/internal/domain/copy.go",
        "python/app/graph/workflow.py",
        "python/app/graph/service.py",
        "python/app/rpc/rag.py",
        "proto/rag/v1/rag.proto",
    )
    try:
        commit = subprocess.check_output(
            ["git", "-C", str(ROOT), "rev-parse", "HEAD"], text=True, timeout=5).strip()
    except (OSError, subprocess.SubprocessError):
        commit = ""
    return {
        "profile": config.profile,
        "region": config.region,
        "models": config.models,
        "embedding_dimensions": config.embedding_dimensions,
        "timeout_s": config.timeout_s,
        "quota_rpm": config.quota_rpm,
        "generation_instruction_sha256": text_digest(GENERATION_INSTRUCTION),
        "provider_schema_sha256": digest(json.dumps([CONTROL_SCHEMA, GENERATION_SCHEMA], sort_keys=True).encode()),
        "provider_config_sha256": digest(config_path.read_bytes()),
        "runtime_config_sha256": digest(
            (json.dumps(dict(sorted(values.items())), sort_keys=True) + "\n").encode("utf-8")),
        "git_commit": commit,
        "implementation_sha256": {
            name: digest((ROOT / name).read_bytes()) for name in files if (ROOT / name).is_file()
        },
    }


def write_report(path: Path, payload: Mapping[str, Any]) -> None:
    assert_no_currency(payload)
    if path.is_relative_to(ROOT):
        raise ServiceEvalError("report_must_be_outside_repository")
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    fd, temporary = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            json.dump(payload, stream, ensure_ascii=False, indent=2, sort_keys=True)
            stream.write("\n")
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
    finally:
        Path(temporary).unlink(missing_ok=True)


def report_payload(contract: Mapping[str, Any], contract_sha256: str, plan: Mapping[str, Any],
                   rows: list[Mapping[str, Any]], *, status: str, stop_reason: str | None,
                   identity: Mapping[str, Any] | None, max_requests: int,
                   run_id: str, requests_made: int) -> dict[str, Any]:
    return {
        "schema_version": 1,
        "report_version": "after-sales-service-eval-v1",
        "status": status,
        "stop_reason": stop_reason,
        "contract_id": contract["contract_id"],
        "contract_sha256": contract_sha256,
        "planned_case_count": plan["case_count"],
        "case_count": len(rows),
        "unrun_case_ids": [case_id for case_id in plan["case_ids"]
                           if case_id not in {row["case_id"] for row in rows}],
        "planned_requests": plan["planned_requests"],
        "requests_made": requests_made,
        "max_requests": max_requests,
        "case_repeats": contract["request_policy"]["case_repeats"],
        "variance": "NOT_MEASURED_SINGLE_RUN",
        "rows": rows,
        "usage": aggregate_usage(rows),
        "provider_identity": identity,
        "run_identity_sha256": text_digest(run_id),
        "reviewer_kind": "agent",
        "human_review_completed": False,
        "real_service_acceptance": False,
        "model_quality_claim": False,
        "online_requests_made": requests_made > 0,
        "note": "Executions and persisted decisions require a separate bound semantic review",
    }


def ensure_new_capture_dir(directory: Path, cases: list[Mapping[str, Any]]) -> None:
    try:
        secure_directory(directory)
    except ValueError as error:
        raise ServiceEvalError(str(error)) from None
    existing = [case["case_id"] for case in cases if (directory / f"{case['case_id']}.json").exists()]
    if existing:
        raise ServiceEvalError("capture_already_exists")


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--plan", action="store_true", help="print the frozen case and request plan")
    parser.add_argument("--live", action="store_true", help="explicitly execute paid provider requests")
    parser.add_argument("--contract", type=Path, default=DEFAULT_CONTRACT)
    parser.add_argument("--base-url", help="loopback gateway URL, for example http://127.0.0.1:26181")
    parser.add_argument("--runtime-env", type=Path, help="protected 0600 stack runtime configuration")
    parser.add_argument("--capture-dir", type=Path, help="new owned directory outside Git for raw captures")
    parser.add_argument("--report", type=Path, help="new report path outside Git")
    parser.add_argument("--max-requests", type=int, help="hard cap on customer message executions")
    parser.add_argument("--timeout-s", type=float, default=75.0)
    parser.add_argument("--min-interval-s", type=float, help="pause between cases to respect customer rate limits")
    parser.add_argument("--case-id", action="append", default=[])
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    budget: RequestBudget | None = None
    try:
        contract = load_contract(args.contract)
        contract_sha256 = digest(args.contract.read_bytes())
        cases = select_cases(contract, args.case_id)
        plan = build_plan(contract, cases)
        if args.plan:
            print(json.dumps(plan, ensure_ascii=False, sort_keys=True))
            return 0
        if not args.live:
            raise ServiceEvalError("explicit_live_or_plan_required")
        if not all((args.base_url, args.runtime_env, args.capture_dir, args.report)):
            raise ServiceEvalError("live_arguments_required")
        if args.report.exists():
            print(json.dumps({"status": "FAIL", "issues": ["output_exists"], "online_requests_made": False}))
            return 2
        max_requests = args.max_requests if args.max_requests is not None else plan["default_max_requests"]
        if max_requests < plan["planned_requests"]:
            raise ServiceEvalError("request_budget_below_plan")
        if max_requests > plan["hard_max_requests"]:
            raise ServiceEvalError("request_budget_above_hard_limit")
        budget = RequestBudget(max_requests)
        values = load_protected_env(args.runtime_env)
        base_url = loopback_http(args.base_url)
        identity = provider_identity(values, base_url)
        preflight = verify_preconditions(contract, values)
        shop = contract["dataset"]["shop_id"]
        ensure_new_capture_dir(args.capture_dir, cases)
        client = CustomerClient(base_url, values["V2_CUSTOMER_ORIGIN"])
        interval = args.min_interval_s if args.min_interval_s is not None else float(
            contract["request_policy"]["min_interval_seconds"])
        run_id = uuid4().hex

        def reader(conversation_id: str, execution_id: str) -> dict[str, Any]:
            return read_customer_records(values, conversation_id, execution_id)

        rows: list[Mapping[str, Any]] = []
        stop_reason = None
        for index, case in enumerate(cases):
            if index:
                time.sleep(max(0.0, interval))
            try:
                capture = run_case(client, case, contract_sha256=contract_sha256, shop=shop,
                                   budget=budget, timeout_s=args.timeout_s, capture_dir=args.capture_dir,
                                   run_id=run_id, reader=reader)
                row = row_from_capture(capture)
                rows.append(row)
                print(json.dumps({"case_id": row["case_id"], "behavior": row["behavior"],
                                  "state": row["state"], "completed": len(rows)}, ensure_ascii=False), flush=True)
                if row["provider_failure"]:
                    stop_reason = "provider_failure"
                    break
            except ServiceEvalError as error:
                stop_reason = str(error)
                break
            write_report(args.report, report_payload(
                contract, contract_sha256, plan, rows, status="NOT_PASSED", stop_reason=stop_reason,
                identity=identity, max_requests=max_requests, run_id=run_id, requests_made=budget.used))
        status = "EXECUTED" if len(rows) == len(cases) and not any(
            row["provider_failure"] for row in rows) else "NOT_PASSED"
        if stop_reason is None and status == "NOT_PASSED":
            stop_reason = "provider_failure"
        write_report(args.report, report_payload(
            contract, contract_sha256, plan, rows, status=status, stop_reason=stop_reason,
            identity=identity, max_requests=max_requests, run_id=run_id, requests_made=budget.used))
        print(json.dumps({"status": status, "case_count": len(rows), "requests_made": budget.used,
                          "usage": aggregate_usage(rows), "preflight": preflight,
                          "real_service_acceptance": False}, ensure_ascii=False))
        return 0 if status == "EXECUTED" else 4
    except ServiceEvalError as error:
        print(json.dumps({"status": "FAIL", "issues": [str(error)],
                          "online_requests_made": bool(budget and budget.used)}))
        return 3
    except Exception:  # noqa: BLE001 - the CLI boundary emits a fixed diagnostic
        print(json.dumps({"status": "FAIL", "issues": ["internal_error"],
                          "online_requests_made": bool(budget and budget.used)}))
        return 3


if __name__ == "__main__":
    raise SystemExit(main())
