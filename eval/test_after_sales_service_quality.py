"""The after-sales gate never approves an unbound or contradictory capture."""
import hashlib
import json
import stat
from pathlib import Path

import after_sales_service_quality as gate
import pytest

ROOT = Path(__file__).resolve().parents[1]
CONTRACT_PATH = ROOT / "eval/after-sales-service-contract.v1.json"


def sha_bytes(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def sha_text(value: str) -> str:
    return sha_bytes(value.encode("utf-8"))


def capture_for(case, contract_sha):
    behavior = case["expected_behavior"]
    if behavior == "HANDOFF":
        state, observed = "FAILED", ""
    else:
        state, observed = case["expected_states"][0], behavior
    facts = []
    if behavior == "ANSWER":
        resource = case["expected_resource_ids"][0]
        for tool in case["expected_tools"]:
            facts.append({
                "tool_name": tool,
                "resource_id": resource,
                "observed_at": "2026-09-29T00:00:00Z",
                "is_mock": True,
                "value_sha256": "a" * 64,
            })
    assistant_expected = case["assistant_message_expected"]
    if assistant_expected:
        text = "订单信息如下。"
        reply = {"id": "answer", "source": "assistant", "text": text,
                 "text_sha256": sha_text(text), "citations": [], "citation_count": 0,
                 "can_copy": case["can_copy"], "blocked_reason": case["blocked_reason"],
                 "created_at": "now"}
        http_messages = [reply]
        stored = [{"id": "answer", "source": "assistant", "content_sha256": sha_text(text),
                   "citation_count": 0}]
    else:
        http_messages = [{"id": "handoff", "source": "system", "text": "已转人工。",
                          "text_sha256": sha_text("已转人工。"), "citations": [],
                          "citation_count": 0}]
        stored = [{"id": "handoff", "source": "system", "content_sha256": sha_text("已转人工。"),
                   "citation_count": 0}]
    ticket = None
    tickets = []
    if case["expect_ticket"]:
        ticket = {"id": "ticket", "status": "OPEN", "reason_code": case["expected_reasons"][0]}
        tickets = [dict(ticket)]
    return {
        "capture_version": "after-sales-service-capture-v1",
        "contract_sha256": contract_sha,
        "case": case,
        "query_sha256": case["query_sha256"],
        "conversation": {"id": "conv", "state": case["expected_conversation_state"],
                         "service_type": case["lane"], "order_id": case["order_id"], "epoch": 1},
        "http": {"messages": http_messages, "ticket": ticket, "events": []},
        "execution": {
            "id": f"exec-{case['case_id']}",
            "state": state,
            "behavior": observed,
            "reason_code": case["expected_reasons"][0],
            "answer_sha256": sha_text("订单信息如下。" if assistant_expected else ""),
            "is_mock": False,
            "evidence_ids": [],
            "facts": facts,
            "messages": stored,
            "tickets": tickets,
            "usage": {"requests": 1, "failed": 0, "input_tokens": 10, "output_tokens": 20},
            "finished_at": None,
        },
        "elapsed_ms": 1000,
        "provider_failure": False,
    }


def make_fixture(tmp_path, contract_path=CONTRACT_PATH):
    contract = json.loads(contract_path.read_text(encoding="utf-8"))
    contract_sha = sha_bytes(contract_path.read_bytes())
    capture_dir = tmp_path / "captures"
    capture_dir.mkdir()
    entries = []
    for case in contract["cases"]:
        path = capture_dir / f"{case['case_id']}.json"
        path.write_text(json.dumps(capture_for(case, contract_sha), ensure_ascii=False,
                                   sort_keys=True), encoding="utf-8")
        entry = {"case_id": case["case_id"], "capture_sha256": sha_bytes(path.read_bytes()),
                 "severe_safety_error": False}
        entry.update({field: True for field in gate.FIELDS})
        entries.append(entry)
    review = {"contract_sha256": contract_sha, "reviewer_kind": "agent",
              "human_review_completed": False, "cases": entries}
    return contract, capture_dir, review


def case_by_id(contract, case_id):
    return next(case for case in contract["cases"] if case["case_id"] == case_id)


def rebind(capture_dir, review, case_id, mutate):
    path = capture_dir / f"{case_id}.json"
    capture = json.loads(path.read_text(encoding="utf-8"))
    mutate(capture)
    path.write_text(json.dumps(capture, ensure_ascii=False, sort_keys=True), encoding="utf-8")
    entry = next(item for item in review["cases"] if item["case_id"] == case_id)
    entry["capture_sha256"] = sha_bytes(path.read_bytes())


def scored(contract_path, capture_dir, review):
    return gate.score(contract_path, capture_dir, review)


def row(result, case_id):
    return next(item for item in result["rows"] if item["case_id"] == case_id)


def test_bound_review_passes_and_keeps_claims_false(tmp_path):
    _, capture_dir, review = make_fixture(tmp_path)
    result = scored(CONTRACT_PATH, capture_dir, review)
    assert result["status"] == "PASS"
    assert result["high_risk"] == [6, 6]
    assert result["ordinary"] == [6, 6]
    assert result["review_bound_all"] is True
    assert result["severe_safety_errors"] == 0
    assert result["reviewer_kind"] == "agent"
    assert result["human_review_completed"] is False
    assert result["real_service_acceptance"] is False
    assert result["model_quality_claim"] is False
    assert row(result, "after-104")["passed"] is True
    assert row(result, "after-104")["deterministic_issues"] == []


def test_missing_review_entry_is_not_passed(tmp_path):
    _, capture_dir, review = make_fixture(tmp_path)
    review["cases"] = [entry for entry in review["cases"] if entry["case_id"] != "after-011"]
    result = scored(CONTRACT_PATH, capture_dir, review)
    assert result["status"] == "NOT_PASSED"
    assert result["review_bound_all"] is False
    assert row(result, "after-011")["review_bound"] is False


def test_changed_capture_after_review_is_not_bound(tmp_path):
    _, capture_dir, review = make_fixture(tmp_path)
    path = capture_dir / "after-011.json"
    path.write_text(path.read_text(encoding="utf-8") + " ", encoding="utf-8")
    result = scored(CONTRACT_PATH, capture_dir, review)
    assert result["status"] == "NOT_PASSED"
    assert row(result, "after-011")["review_bound"] is False


@pytest.mark.parametrize(
    ("case_id", "mutate", "issue"),
    [
        ("after-011", lambda capture: capture["execution"].update(behavior="HANDOFF"), "behavior"),
        ("after-011", lambda capture: capture["execution"].update(state="FAILED"), "execution_state"),
        ("after-011", lambda capture: capture["execution"].update(reason_code="missing_context"), "reason_code"),
        ("after-011", lambda capture: capture["execution"]["facts"][0].update(is_mock=False), "non_mock_fact"),
        ("after-011", lambda capture: capture["http"]["messages"][0].update(
            citations=[{"evidence_id": "e1"}], citation_count=1), "citations_present"),
        ("after-104", lambda capture: capture["http"]["messages"].append(
            {"id": "answer", "source": "assistant", "text": "已转人工。",
             "text_sha256": sha_text("已转人工。"), "citation_count": 0}), "assistant_message"),
        ("after-104", lambda capture: (capture["execution"].update(tickets=[]),
                                       capture["http"].update(ticket=None)), "ticket"),
        ("after-011", lambda capture: capture.update(provider_failure=True), "provider_failure"),
        ("after-011", lambda capture: capture["execution"].pop("usage"), "usage"),
    ],
)
def test_deterministic_contradictions_fail(tmp_path, case_id, mutate, issue):
    _, capture_dir, review = make_fixture(tmp_path)
    rebind(capture_dir, review, case_id, mutate)
    result = scored(CONTRACT_PATH, capture_dir, review)
    assert result["status"] == "NOT_PASSED"
    assert row(result, case_id)["review_bound"] is True
    assert issue in row(result, case_id)["deterministic_issues"]
    assert row(result, case_id)["passed"] is False


def test_write_completion_claim_is_rejected(tmp_path):
    _, capture_dir, review = make_fixture(tmp_path)
    text = "已为您退款，请查收。"

    def mutate(capture):
        capture["http"]["messages"][0].update(text=text, text_sha256=sha_text(text))

    rebind(capture_dir, review, "after-011", mutate)
    result = scored(CONTRACT_PATH, capture_dir, review)
    assert "write_completion_claim" in row(result, "after-011")["deterministic_issues"]
    assert result["status"] == "NOT_PASSED"


def test_failed_execution_handoff_is_accepted(tmp_path):
    _, capture_dir, review = make_fixture(tmp_path)
    capture = json.loads((capture_dir / "after-104.json").read_text(encoding="utf-8"))
    assert capture["execution"]["state"] == "FAILED"
    assert capture["execution"]["behavior"] == ""
    assert capture["conversation"]["state"] == "WAITING_HUMAN"
    result = scored(CONTRACT_PATH, capture_dir, review)
    assert row(result, "after-104")["passed"] is True


def test_false_review_field_fails(tmp_path):
    _, capture_dir, review = make_fixture(tmp_path)
    review["cases"][0]["behavior_correct"] = False
    result = scored(CONTRACT_PATH, capture_dir, review)
    assert result["status"] == "NOT_PASSED"
    assert row(result, review["cases"][0]["case_id"])["review_passed"] is False


def test_severe_safety_error_fails_even_when_review_passes(tmp_path):
    _, capture_dir, review = make_fixture(tmp_path)
    review["cases"][0]["severe_safety_error"] = True
    result = scored(CONTRACT_PATH, capture_dir, review)
    assert result["status"] == "NOT_PASSED"
    assert result["severe_safety_errors"] == 1


@pytest.mark.parametrize(
    ("mutate", "message"),
    [
        (lambda review: review.update(reviewer_kind="human"), "review provenance mismatch"),
        (lambda review: review.update(human_review_completed=True), "review provenance mismatch"),
        (lambda review: review.update(contract_sha256="0" * 64), "contract mismatch"),
        (lambda review: review["cases"].append(dict(review["cases"][0])), "duplicate review"),
        (lambda review: review["cases"].append({"case_id": "after-999"}), "unknown case"),
    ],
)
def test_review_provenance_fails_closed(tmp_path, mutate, message):
    _, capture_dir, review = make_fixture(tmp_path)
    mutate(review)
    with pytest.raises(ValueError, match=message):
        scored(CONTRACT_PATH, capture_dir, review)


def test_ordinary_threshold_is_read_from_the_contract(tmp_path):
    contract = json.loads(CONTRACT_PATH.read_text(encoding="utf-8"))
    contract["thresholds"]["ordinary_case_pass_rate_min"] = 0.5
    contract_path = tmp_path / "contract.json"
    contract_path.write_text(json.dumps(contract, ensure_ascii=False), encoding="utf-8")
    _, capture_dir, review = make_fixture(tmp_path, contract_path)
    rebind(capture_dir, review, "after-011",
           lambda capture: capture["execution"].update(behavior="HANDOFF"))
    result = scored(contract_path, capture_dir, review)
    assert row(result, "after-011")["passed"] is False
    assert result["ordinary"] == [5, 6]
    assert result["status"] == "PASS"


def test_main_writes_pass_outside_repository(tmp_path):
    _, capture_dir, review = make_fixture(tmp_path)
    review_path = tmp_path / "review.json"
    review_path.write_text(json.dumps(review), encoding="utf-8")
    output = tmp_path / "quality.json"
    assert gate.main(["--contract", str(CONTRACT_PATH), "--capture-dir", str(capture_dir),
                      "--review", str(review_path), "--output", str(output)]) == 0
    assert stat.S_IMODE(output.stat().st_mode) == 0o600
    assert json.loads(output.read_text(encoding="utf-8"))["status"] == "PASS"


def test_main_reports_not_passed(tmp_path, capsys):
    _, capture_dir, review = make_fixture(tmp_path)
    review["cases"].pop()
    review_path = tmp_path / "review.json"
    review_path.write_text(json.dumps(review), encoding="utf-8")
    assert gate.main(["--contract", str(CONTRACT_PATH), "--capture-dir", str(capture_dir),
                      "--review", str(review_path), "--output", str(tmp_path / "out.json")]) == 1
    assert capsys.readouterr().out.strip() == "NOT_PASSED"


def test_main_refuses_unsafe_outputs(tmp_path, capsys):
    _, capture_dir, review = make_fixture(tmp_path)
    review_path = tmp_path / "review.json"
    review_path.write_text(json.dumps(review), encoding="utf-8")
    args = ["--contract", str(CONTRACT_PATH), "--capture-dir", str(capture_dir),
            "--review", str(review_path)]
    assert gate.main([*args, "--output", str(ROOT / "eval/gate-out.json")]) == 2
    assert capsys.readouterr().out.strip() == "FAIL output_must_be_outside_repository"
    existing = tmp_path / "existing.json"
    existing.write_text("{}", encoding="utf-8")
    assert gate.main([*args, "--output", str(existing)]) == 2
    assert capsys.readouterr().out.strip() == "FAIL output_exists"
    assert gate.main([*args, "--review", str(tmp_path / "missing.json"),
                      "--output", str(tmp_path / "out.json")]) == 2
    assert capsys.readouterr().out.strip() == "FAIL review_unreadable"
