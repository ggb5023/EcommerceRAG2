"""Offline checks for the after-sales runner boundary and evidence capture."""
import json
import stat
import urllib.parse
from email.message import Message
from pathlib import Path

import pytest
import run_after_sales_service as runner
from run_after_sales_service import (
    CustomerClient,
    RequestBudget,
    ServiceEvalError,
    assert_no_currency,
    load_contract,
    load_protected_env,
    loopback_http,
    main,
    public_messages,
    read_customer_records,
    row_from_capture,
    sanitize_events,
    terminal_event,
    text_digest,
    verify_preconditions,
    wait_for_completion,
    write_capture,
    write_report,
)

ROOT = Path(__file__).resolve().parents[1]


class Response:
    def __init__(self, payload=None, lines=None, content_type="application/json"):
        self.payload = payload
        self.lines = lines or []
        self.status = 200
        self.headers = Message()
        self.headers["Content-Type"] = content_type

    def read(self, *args):
        return json.dumps(self.payload).encode()

    def __enter__(self):
        return self

    def __exit__(self, *args):
        return False

    def __iter__(self):
        return iter([line.encode() for line in self.lines])


class Opener:
    def __init__(self, handler):
        self.handler = handler
        self.requests = []

    def open(self, request, timeout=None):
        self.requests.append(request)
        return self.handler(request)


def header(request, name):
    for key, value in request.headers.items():
        if key.lower() == name.lower():
            return value
    return None


def complete_snapshot(case, can_copy=False, blocked="no_citable_evidence"):
    return {
        "conversation": {
            "id": "conv",
            "state": "AI",
            "service_type": "after_sales",
            "order_id": case["order_id"],
            "epoch": 1,
        },
        "messages": [
            {"id": "customer", "source": "customer", "text": case["query"], "citations": []},
            {"id": "answer", "source": "assistant", "text": "订单当前状态可查。",
             "citations": [], "can_copy": can_copy, "blocked_reason": blocked},
        ],
    }


def execution_record(case, behavior="REPLY_BEHAVIOR_ANSWER", state="DONE"):
    return {
        "id": "exec",
        "state": state,
        "behavior": behavior,
        "reason_code": "grounded",
        "answer_sha256": text_digest("订单当前状态可查。"),
        "is_mock": False,
        "evidence_ids": [],
        "facts": [{
            "tool_name": "get_order_status",
            "resource_id": case["order_id"],
            "observed_at": "2026-09-29T00:00:00Z",
            "is_mock": True,
            "value_sha256": "a" * 64,
        }],
        "messages": [{"id": "answer", "source": "assistant", "citation_count": 0,
                      "citation_ids": [], "content_sha256": text_digest("订单当前状态可查。")}],
        "tickets": [],
        "usage": {"requests": 1, "failed": 0, "input_tokens": 10, "output_tokens": 20},
        "finished_at": None,
    }


def test_customer_client_flow_and_sse_stop():
    case = load_contract()["cases"][0]
    lines = [
        "id: 1\n",
        "event: completed\n",
        'data: {"execution_id": "exec", "behavior": "REPLY_BEHAVIOR_ANSWER"}\n',
        "\n",
        "id: 2\n",
        "event: message\n",
        'data: {"message_id": "never-read"}\n',
        "\n",
    ]

    def handler(request):
        path = urllib.parse.urlsplit(request.full_url).path
        if path == "/v1/customer/session":
            return Response({"csrf_token": "csrf", "shop_id": "shop-demo"})
        if path == "/v1/customer/conversations":
            return Response({"conversation_id": "conv", "service_type": case["lane"],
                             "order_id": case["order_id"]})
        if path.endswith("/messages"):
            return Response({"execution_id": "exec", "message_id": "m"})
        if path.endswith("/events"):
            return Response(lines=lines, content_type="text/event-stream")
        raise AssertionError(path)

    opener = Opener(handler)
    client = CustomerClient("http://127.0.0.1:26181", "http://127.0.0.1:26184", opener=opener)
    csrf = client.session("shop-demo")
    conversation = client.create_conversation(case, csrf, "run")
    assert conversation["conversation_id"] == "conv"
    submit = client.send("conv", case["query"], csrf, "run", case["case_id"])
    assert submit["execution_id"] == "exec"
    events = client.events("conv", "exec")
    assert terminal_event(events, "exec") is True
    assert len(events) == 1
    assert header(opener.requests[0], "Origin") == "http://127.0.0.1:26184"
    assert header(opener.requests[1], "X-CSRF-Token") == "csrf"
    assert header(opener.requests[1], "Idempotency-Key").startswith("as-conv-run-")
    assert header(opener.requests[2], "X-CSRF-Token") == "csrf"
    assert header(opener.requests[2], "Idempotency-Key").startswith("as-turn-run-")


def test_conversation_lane_mismatch_fails_closed():
    case = load_contract()["cases"][0]

    def handler(request):
        return Response({"conversation_id": "conv", "service_type": "pre_sales", "order_id": ""})

    client = CustomerClient("http://127.0.0.1:26181", "http://127.0.0.1:26184", opener=Opener(handler))
    with pytest.raises(ServiceEvalError, match="conversation_lane_mismatch"):
        client.create_conversation(case, "csrf", "run")


def test_run_case_captures_durable_evidence(tmp_path):
    case = load_contract()["cases"][0]
    snapshot = complete_snapshot(case)
    lines = [
        "event: completed\n",
        'data: {"execution_id": "exec", "behavior": "REPLY_BEHAVIOR_ANSWER"}\n',
        "\n",
    ]

    def handler(request):
        path = urllib.parse.urlsplit(request.full_url).path
        if path == "/v1/customer/session":
            return Response({"csrf_token": "csrf"})
        if path == "/v1/customer/conversations":
            return Response({"conversation_id": "conv", "service_type": "after_sales",
                             "order_id": case["order_id"]})
        if path.endswith("/messages"):
            return Response({"execution_id": "exec"})
        if path.endswith("/events"):
            return Response(lines=lines, content_type="text/event-stream")
        return Response(snapshot)

    client = CustomerClient("http://127.0.0.1:26181", "http://127.0.0.1:26184", opener=Opener(handler))
    budget = RequestBudget(1)
    capture_dir = tmp_path / "captures"
    capture = runner.run_case(
        client, case, contract_sha256="b" * 64, shop="shop-demo", budget=budget,
        timeout_s=1, capture_dir=capture_dir, run_id="run",
        reader=lambda conversation_id, execution_id: execution_record(case),
    )
    assert budget.used == 1
    assert capture["case"] == case
    assert capture["execution"]["is_mock"] is False
    assert capture["provider_failure"] is False
    path = capture_dir / f"{case['case_id']}.json"
    assert stat.S_IMODE(path.stat().st_mode) == 0o600
    row = row_from_capture(capture)
    assert row["observed_behavior"] == "ANSWER"
    assert row["can_copy"] is False
    assert row["blocked_reason"] == "no_citable_evidence"
    with pytest.raises(ServiceEvalError, match="capture_already_exists"):
        write_capture(capture_dir, case["case_id"], capture)


def test_failed_handoff_is_observed_as_handoff():
    case = next(item for item in load_contract()["cases"] if item["case_id"] == "after-104")
    capture = {
        "case": case,
        "conversation": {"id": "conv", "state": "WAITING_HUMAN", "service_type": "after_sales",
                         "order_id": case["order_id"]},
        "execution": {
            "id": "exec", "state": "FAILED", "behavior": "", "reason_code": "unsupported_request",
            "answer_sha256": text_digest(""), "is_mock": False, "evidence_ids": [],
            "facts": [], "messages": [], "tickets": [],
            "usage": {"requests": 1, "failed": 0, "input_tokens": 1, "output_tokens": 1},
            "finished_at": None,
        },
        "http": {"messages": [], "ticket": None, "events": []},
        "provider_failure": False,
    }
    row = row_from_capture(capture)
    assert row["behavior"] == ""
    assert row["observed_behavior"] == "HANDOFF"
    assert row["customer_state"] == "WAITING_HUMAN"
    assert row["state"] == "FAILED"


def test_provider_failure_never_counts_as_handoff():
    capture = {
        "conversation": {"state": "WAITING_HUMAN"},
        "execution": {"reason_code": "provider_failed_generation"},
        "sse": [],
    }
    assert runner.provider_failure(capture) is True


def test_wait_for_completion_requires_a_persisted_message():
    class Stuck:
        def snapshot(self, conversation_id):
            return {"conversation": {"active_execution_id": "exec"}, "messages": []}

    with pytest.raises(ServiceEvalError, match="execution_timeout"):
        wait_for_completion(Stuck(), "conv", timeout_s=0.01)


def test_public_messages_and_events_hash_answers():
    messages = public_messages({"messages": [{
        "id": "a", "source": "assistant", "text": "hi",
        "citations": [{"evidence_id": "e1"}], "can_copy": False,
        "blocked_reason": "no_citable_evidence", "created_at": "now",
    }]})
    assert messages[0]["text"] == "hi"
    assert messages[0]["text_sha256"] == text_digest("hi")
    assert messages[0]["citation_ids"] == ["e1"]
    events = sanitize_events([{"id": 1, "event": "message", "data": {
        "message": {"id": "m", "source": "assistant", "text": "hi", "citations": [1, 2]},
    }}])
    assert events[0]["data"]["message"]["text_sha256"] == text_digest("hi")
    assert events[0]["data"]["message"]["citation_count"] == 2


def test_request_budget_is_a_hard_cap():
    budget = RequestBudget(2)
    budget.reserve("one")
    budget.reserve("two")
    with pytest.raises(ServiceEvalError, match="request_budget_exceeded"):
        budget.reserve("three")
    assert budget.used == 2
    for invalid in (0, -1):
        with pytest.raises(ServiceEvalError, match="request_limit_invalid"):
            RequestBudget(invalid)


@pytest.mark.parametrize(
    ("value", "accepted"),
    [
        ("http://127.0.0.1:26181", True),
        ("http://127.0.0.1:26181/", True),
        ("https://127.0.0.1:26181", False),
        ("http://localhost:26181", False),
        ("http://198.51.100.10:26181", False),
        ("http://127.0.0.1:26181/path", False),
        ("http://user:pass@127.0.0.1:26181", False),
        ("http://127.0.0.1", False),
    ],
)
def test_loopback_http_is_explicit(value, accepted):
    if accepted:
        assert loopback_http(value) == "http://127.0.0.1:26181"
    else:
        with pytest.raises(ServiceEvalError, match="loopback_http_required"):
            loopback_http(value)


def protected_env(tmp_path, mode=0o600, extra=""):
    path = tmp_path / "runtime.env"
    path.write_text(
        "V2_GATEWAY_DSN=postgres://hidden\n"
        "HTTP_ADDR=127.0.0.1:26181\n"
        "RAG_PROFILE=v2_langchain_bailian\n"
        "V2_CUSTOMER_ORIGIN=http://127.0.0.1:26184\n" + extra,
        encoding="utf-8",
    )
    path.chmod(mode)
    return path


def test_protected_env_requires_0600(tmp_path):
    values = load_protected_env(protected_env(tmp_path))
    assert values["RAG_PROFILE"] == "v2_langchain_bailian"
    with pytest.raises(ServiceEvalError, match="protected_runtime_env_required"):
        load_protected_env(protected_env(tmp_path, mode=0o644))
    with pytest.raises(ServiceEvalError, match="protected_runtime_env_invalid"):
        load_protected_env(protected_env(tmp_path, extra="HTTP_ADDR=duplicate\n"))


def test_report_and_capture_never_expose_currency(tmp_path):
    assert_no_currency({"usage": {"requests": 1, "input_tokens": 2}})
    with pytest.raises(ServiceEvalError, match="currency_field_forbidden"):
        assert_no_currency({"usage": {"amount": 1}})
    report = tmp_path / "report.json"
    write_report(report, {"status": "EXECUTED", "usage": {"requests": 1}})
    assert stat.S_IMODE(report.stat().st_mode) == 0o600
    assert json.loads(report.read_text())["status"] == "EXECUTED"
    with pytest.raises(ServiceEvalError, match="report_must_be_outside_repository"):
        write_report(ROOT / "eval/should-not-exist.json", {"status": "PLAN"})


class Cursor:
    def __init__(self, rows):
        self.rows = rows

    def fetchone(self):
        return self.rows[0] if self.rows else None

    def fetchall(self):
        return self.rows


class FakeConnection:
    def __init__(self, responses):
        self.responses = responses
        self.statements = []

    def execute(self, sql, params=None):
        self.statements.append(sql)
        if sql.startswith("SET"):
            return Cursor([])
        for marker, rows in self.responses.items():
            if marker in sql:
                return Cursor(rows)
        raise AssertionError(sql)

    def __enter__(self):
        return self

    def __exit__(self, *args):
        return False


def test_read_customer_records_is_read_only(tmp_path):
    case = load_contract()["cases"][0]
    fact_value = '{"order_id":"' + case["order_id"] + '","status":"delivered"}'
    connection = FakeConnection({
        "SELECT state,behavior,reason_code": [(
            "DONE", "REPLY_BEHAVIOR_ANSWER", "grounded", "answer",
            json.dumps([{"id": "e1"}]), json.dumps([{
                "tool_name": "get_order_status", "value_json": fact_value,
                "observed_at": "2026-09-29T00:00:00Z", "is_mock": True,
                "resource_id": case["order_id"],
            }]), False, None,
        )],
        "FROM customer_message": [("answer", "assistant", "answer", "[]", "exec")],
        "FROM customer_ticket": [],
        "FROM usage_log": [(1, 0, 10, 20)],
    })
    values = {"V2_GATEWAY_DSN": "postgres://hidden"}
    records = read_customer_records(
        values, "conv", "exec", connect=lambda *args, **kwargs: connection)
    assert connection.statements[0].startswith("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY")
    assert records["evidence_ids"] == ["e1"]
    assert records["facts"][0]["value_sha256"] == text_digest(fact_value)
    assert records["facts"][0]["is_mock"] is True
    assert records["usage"] == {"requests": 1, "failed": 0, "input_tokens": 10, "output_tokens": 20}


def test_read_customer_records_fails_closed():
    values = {"V2_GATEWAY_DSN": "postgres://hidden"}

    def broken(*args, **kwargs):
        raise RuntimeError("private dsn")

    with pytest.raises(ServiceEvalError, match="customer_records_unavailable"):
        read_customer_records(values, "conv", "exec", connect=broken)


def test_preflight_requires_chat_and_zero_knowledge(monkeypatch, tmp_path):
    contract = load_contract()
    monkeypatch.setattr(runner, "verify_facts_mock", lambda *args: None)

    def connect_with(chat, published):
        def connect(*args, **kwargs):
            return FakeConnection({
                "customer_chat_enabled": [(chat,)],
                "count(*)": [(published,)],
            })
        return connect

    result = verify_preconditions(contract, {"V2_GATEWAY_DSN": "x"},
                                  connect=connect_with(True, 0))
    assert result["published_documents"] == 0
    with pytest.raises(ServiceEvalError, match="customer_chat_disabled"):
        verify_preconditions(contract, {"V2_GATEWAY_DSN": "x"},
                             connect=connect_with(False, 0))
    with pytest.raises(ServiceEvalError, match="published_knowledge_present"):
        verify_preconditions(contract, {"V2_GATEWAY_DSN": "x"},
                             connect=connect_with(True, 1))


def test_live_requires_explicit_arguments(capsys):
    assert main(["--live"]) == 3
    output = json.loads(capsys.readouterr().out)
    assert output["status"] == "FAIL"
    assert output["issues"] == ["live_arguments_required"]
    assert output["online_requests_made"] is False
    assert main([]) == 3
    output = json.loads(capsys.readouterr().out)
    assert output["issues"] == ["explicit_live_or_plan_required"]


def test_terminal_event_accepts_queue_and_completion():
    completed = [{"event": "completed", "data": {"execution_id": "exec"}}]
    queued = [{"event": "state", "data": {"state": "WAITING_HUMAN"}}]
    assert terminal_event(completed, "exec") is True
    assert terminal_event(queued, "exec") is True
    assert terminal_event([{"event": "completed", "data": {"execution_id": "other"}}], "exec") is False
