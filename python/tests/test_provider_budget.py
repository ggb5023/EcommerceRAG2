"""Paid-call safety: competing processes, durable reservations and no bypass."""
import asyncio
import hashlib
import json
import os
import sqlite3
import subprocess
import sys
from decimal import Decimal
from pathlib import Path

import pytest

from app.providers.aliyun_bailian import (
    AlibabaBailianProvider,
    HTTPResponse,
    UrllibTransport,
)
from app.providers.budget import BudgetLedger, initialize_ledger
from app.providers.config import validate_values
from app.providers.contracts import ProviderError


@pytest.fixture
def ledger(tmp_path):
    tmp_path.chmod(0o700)
    path = tmp_path / "budget.sqlite"
    initialize_ledger(path, "shared-test", "0.02")
    return BudgetLedger(path, "shared-test", "0.02")


def reserve(ledger):
    return ledger.reserve("control", "qwen3.8-flash", {"max_tokens": 256, "messages": []}, region="cn-beijing")


def config():
    return validate_values({
        "PROVIDER_PROFILE": "aliyun-bailian", "BAILIAN_ENDPOINT": "https://dashscope.example.test",
        "BAILIAN_REGION": "cn-beijing", "CONTROL_MODEL": "qwen3.8-flash",
        "EMBEDDING_MODEL": "qwen3.7-text-embedding", "RERANK_MODEL": "qwen3.7-text-rerank",
        "GENERATION_MODEL": "qwen3.7-plus", "DASHSCOPE_API_KEY": "secret-key-for-test",
    })


def environment(monkeypatch, ledger):
    monkeypatch.setenv("V2_ONLINE_BUDGET_CNY", "0.02")
    monkeypatch.setenv("V2_ONLINE_BUDGET_LEDGER", str(ledger.path))
    monkeypatch.setenv("V2_ONLINE_BUDGET_ID", "shared-test")


def test_failures_usage_and_restart_never_refund(ledger):
    one = reserve(ledger)
    ledger.record(one, error_code="timeout")
    two = reserve(ledger)
    ledger.record(two, data={"id": "request-test", "usage": {"prompt_tokens": 10, "completion_tokens": 2}}, http_status=200)
    summary = ledger.summary()
    assert summary["requests"] == 2 and Decimal(summary["reserved_cny"]) > 0
    assert BudgetLedger(ledger.path, "shared-test", "0.02").summary() == summary
    with sqlite3.connect(ledger.path) as c:
        rows = c.execute("SELECT state,usage_json,request_id_sha256 FROM attempt ORDER BY created_at").fetchall()
    assert rows[0][0] == "error" and rows[1][1] and len(rows[1][2]) == 64


def test_explicit_cap_extension_preserves_attempts_and_blocks_stale_callers(ledger):
    identifier = reserve(ledger)
    ledger.record(identifier, error_code="timeout")
    before = ledger.summary()
    stale = BudgetLedger(ledger.path, "shared-test", "0.02")
    ledger.extend_authorized_cap("0.04", authorization="test-operator-approval")
    after = ledger.summary()
    assert after["requests"] == before["requests"] == 1
    assert after["reserved_cny"] == before["reserved_cny"]
    assert after["cap_cny"] == "0.04" and after["window_id"] == before["window_id"]
    with pytest.raises(ProviderError):
        reserve(stale)
    with sqlite3.connect(ledger.path) as c:
        assert c.execute("SELECT authorization,old_cap_micro,new_cap_micro FROM cap_amendment").fetchall() == [
            ("test-operator-approval", 20000, 40000)]
        assert c.execute("SELECT id,state FROM attempt").fetchall() == [(identifier, "error")]


@pytest.mark.parametrize("cap,authorization", [("0.01", "approval"), ("0.02", "approval"),
                                               ("Infinity", "approval"), ("0.04", ""),
                                               ("0.04", "raw chat or secret")])
def test_invalid_extension_never_changes_existing_budget(ledger, cap, authorization):
    before = ledger.summary()
    with pytest.raises(ProviderError):
        ledger.extend_authorized_cap(cap, authorization=authorization)
    assert ledger.summary() == before


def test_many_processes_share_cap_and_crashed_attempts_remain_reserved(ledger):
    code = """
import sys
from pathlib import Path
from app.providers.budget import BudgetLedger
from app.providers.contracts import ProviderError
l=BudgetLedger(Path(sys.argv[1]),'shared-test','0.02')
try:
 l.reserve('control','qwen3.8-flash',{'max_tokens':256,'messages':[]},region='cn-beijing')
except ProviderError as e:
 assert e.code=='budget_exhausted'
 sys.exit(3)
"""
    env = dict(os.environ, PYTHONPATH=str(Path(__file__).resolve().parents[1]))
    children = [subprocess.Popen([sys.executable, "-c", code, str(ledger.path)], env=env) for _ in range(12)]
    statuses = [child.wait(timeout=20) for child in children]
    assert all(code in (0, 3) for code in statuses) and 3 in statuses
    summary = ledger.summary()
    assert summary["requests"] == statuses.count(0)
    assert Decimal(summary["reserved_cny"]) <= Decimal("0.02")
    with pytest.raises(ProviderError, match="exhausted"):
        reserve(BudgetLedger(ledger.path, "shared-test", "0.02"))


@pytest.mark.parametrize("change", ["cap", "id", "missing", "permissions", "symlink", "corrupt"])
def test_lost_or_changed_ledger_never_creates_a_new_budget(ledger, change):
    cap, identifier = "0.02", "shared-test"
    if change == "cap":
        cap = "20"
    elif change == "id":
        identifier = "new-window"
    elif change == "missing":
        ledger.path.unlink()
    elif change == "permissions":
        ledger.path.chmod(0o644)
    elif change == "symlink":
        target = ledger.path.with_suffix(".backup")
        ledger.path.rename(target)
        ledger.path.symlink_to(target)
    else:
        ledger.path.write_bytes(b"corrupt")
    with pytest.raises(ProviderError):
        BudgetLedger(ledger.path, identifier, cap)
    if change == "missing":
        assert not ledger.path.exists()


def test_init_never_overwrites_authorized_window(ledger):
    with pytest.raises(FileExistsError):
        initialize_ledger(ledger.path, "another-window", "20")


def test_bound_violation_freezes_window_and_unknown_models_block(ledger):
    with pytest.raises(ProviderError):
        ledger.reserve("control", "unknown-model", {"max_tokens": 1}, region="cn-beijing")
    attempt = reserve(ledger)
    with pytest.raises(ProviderError):
        ledger.record(attempt, data={"usage": {"input_tokens": 1_000_000, "output_tokens": 2}})
    with pytest.raises(ProviderError):
        reserve(ledger)


class Transport:
    def __init__(self, response):
        self.response, self.calls = response, 0

    async def request(self, *args, **kwargs):
        self.calls += 1
        if isinstance(self.response, Exception):
            raise self.response
        return self.response


def test_committed_reservation_precedes_transport_and_records_usage(monkeypatch, ledger):
    environment(monkeypatch, ledger)

    class InspectTransport:
        async def request(self, *args, **kwargs):
            assert ledger.summary()["requests"] == 1
            import json
            body = json.loads(kwargs["body"])
            assert body["max_tokens"] == 256 and body["enable_thinking"] is False
            return HTTPResponse(200, {"x-request-id": "header-id"}, json.dumps({
                "choices": [{"message": {"content": '{"ok":true}'}, "finish_reason": "stop"}],
                "usage": {"prompt_tokens": 10, "completion_tokens": 5},
            }).encode())

    result = asyncio.run(AlibabaBailianProvider(config(), InspectTransport()).control([{"role": "user", "content": "fixture"}]))
    assert result.request_id == "header-id"
    with sqlite3.connect(ledger.path) as c:
        row = c.execute("SELECT request_id_sha256,observed_micro FROM attempt").fetchone()
    assert len(row[0]) == 64 and row[1] == 22


def test_failed_reservation_stops_before_transport(monkeypatch, ledger):
    environment(monkeypatch, ledger)
    transport = Transport(HTTPResponse(200, {}, b"{}"))
    provider = AlibabaBailianProvider(config(), transport)
    ledger.path.unlink()
    with pytest.raises(ProviderError):
        asyncio.run(provider.control([{"role": "user", "content": "fixture"}]))
    assert transport.calls == 0 and not ledger.path.exists()


def test_http_failure_keeps_request_fingerprint_without_body(monkeypatch, ledger):
    environment(monkeypatch, ledger)
    transport = Transport(HTTPResponse(400, {"X-Request-ID": "failed-request"}, b"sensitive response"))
    with pytest.raises(ProviderError):
        asyncio.run(AlibabaBailianProvider(config(), transport).control([{"role": "user", "content": "fixture"}]))
    with sqlite3.connect(ledger.path) as conn:
        row = conn.execute("SELECT request_id_sha256,http_status,error_code,state FROM attempt").fetchone()
    assert row == (__import__("hashlib").sha256(b"failed-request").hexdigest(), 400, "http_error", "error")
    assert b"sensitive response" not in ledger.path.read_bytes()


@pytest.mark.parametrize("slot", ["control", "generation"])
@pytest.mark.parametrize("content,finish,usage,error_code", [
    ('{"answer":"private-output"}', "length", {"prompt_tokens": 10, "completion_tokens": 5}, "schema_error"),
    ('{"answer":"private-output"}', "content_filter", {"prompt_tokens": 10, "completion_tokens": 5}, "schema_error"),
    ('{"answer":"private-output"}', "tool_calls", {"prompt_tokens": 10, "completion_tokens": 5}, "schema_error"),
    ('{"answer":"private-output"', "stop", {"prompt_tokens": 10, "completion_tokens": 5}, "schema_error"),
    ('{"other":"private-output"}', "stop", {"prompt_tokens": 10, "completion_tokens": 5}, "schema_error"),
    ('{"answer":"private-output"}', "unknown", {"prompt_tokens": 10, "completion_tokens": 5}, "invalid_response"),
    ('{"answer":"private-output"}', "stop", {"completion_tokens": -1}, "invalid_response"),
])
def test_chat_validation_failure_is_charged_and_classified_without_body(
    monkeypatch, ledger, slot, content, finish, usage, error_code,
):
    ledger.extend_authorized_cap("1", authorization="test-chat-validation")
    environment(monkeypatch, ledger)
    monkeypatch.setenv("V2_ONLINE_BUDGET_CNY", "1")
    response = HTTPResponse(200, {"X-Request-ID": "failed-chat-request"}, json.dumps({
        "choices": [{"message": {"content": content}, "finish_reason": finish}],
        "usage": usage,
    }).encode())
    transport = Transport(response)
    provider = AlibabaBailianProvider(config(), transport)
    method = provider.control if slot == "control" else provider.generate
    with pytest.raises(ProviderError) as raised:
        asyncio.run(method([{"role": "user", "content": "private-input"}],
                           response_schema={"type": "object", "required": ["answer"],
                                            "properties": {"answer": {"type": "string"}}}))
    assert raised.value.code == error_code
    assert transport.calls == 1
    with sqlite3.connect(ledger.path) as conn:
        fingerprint, http_status, code, state, usage_json, reserved = conn.execute(
            "SELECT request_id_sha256,http_status,error_code,state,usage_json,reserved_micro FROM attempt"
        ).fetchone()
    assert (fingerprint, http_status, code, state) == (
        hashlib.sha256(b"failed-chat-request").hexdigest(), 200, error_code, "error")
    assert reserved > 0 and ledger.summary()["requests"] == 1
    sanitized = {key: value for key, value in usage.items() if value >= 0}
    assert (json.loads(usage_json) if usage_json else None) == (sanitized or None)
    raw = ledger.path.read_bytes()
    assert b"private-output" not in raw and b"private-input" not in raw
    assert b"secret-key-for-test" not in raw


def test_constructor_requires_ledger_even_without_v2_profile(monkeypatch):
    monkeypatch.delenv("V2_ONLINE_BUDGET_LEDGER", raising=False)
    with pytest.raises(ProviderError):
        AlibabaBailianProvider(config())
    with pytest.raises(ProviderError):
        AlibabaBailianProvider(config(), UrllibTransport(), test_mode=True)


@pytest.mark.parametrize("slot", ["control", "generation"])
def test_chat_without_request_identity_is_not_recorded_as_success(monkeypatch, ledger, slot):
    ledger.extend_authorized_cap("1", authorization="test-missing-chat-id")
    environment(monkeypatch, ledger)
    monkeypatch.setenv("V2_ONLINE_BUDGET_CNY", "1")
    transport = Transport(HTTPResponse(200, {}, json.dumps({
        "choices": [{"message": {"content": '{"answer":"private-output"}'}, "finish_reason": "stop"}],
        "usage": {"prompt_tokens": 10, "completion_tokens": 5},
    }).encode()))
    provider = AlibabaBailianProvider(config(), transport)
    method = provider.control if slot == "control" else provider.generate
    with pytest.raises(ProviderError) as error:
        asyncio.run(method([{"role": "user", "content": "private-input"}]))
    assert error.value.code == "invalid_response" and transport.calls == 1
    with sqlite3.connect(ledger.path) as conn:
        assert conn.execute("SELECT state,error_code,request_id_sha256 FROM attempt").fetchone() == (
            "error", "invalid_response", None)
    assert ledger.summary()["requests"] == 1


@pytest.mark.parametrize("failure", [ProviderError("timeout", "test"), TimeoutError(), HTTPResponse(429, {}, b"{}"), HTTPResponse(200, {}, b"invalid")])
def test_network_failure_reserves_and_budget_exhaustion_never_sends(monkeypatch, ledger, failure):
    environment(monkeypatch, ledger)
    transport = Transport(failure)
    provider = AlibabaBailianProvider(config(), transport)
    for _ in range(12):
        try:
            asyncio.run(provider.control([{"role": "user", "content": "controlled fixture"}]))
        except ProviderError:
            pass
    assert 0 < transport.calls < 12
    assert ledger.summary()["requests"] == transport.calls
    raw = ledger.path.read_bytes()
    assert b"controlled fixture" not in raw and b"secret-key-for-test" not in raw


def test_input_bounds_block_before_network(monkeypatch, ledger):
    environment(monkeypatch, ledger)
    transport = Transport(HTTPResponse(200, {}, b"{}"))
    provider = AlibabaBailianProvider(config(), transport)
    with pytest.raises(ProviderError, match="bounded"):
        asyncio.run(provider.control([{"role": "user", "content": "x" * 70_000}]))
    assert not transport.calls and ledger.summary()["requests"] == 0
