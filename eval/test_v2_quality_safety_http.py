"""Safety evidence must distinguish denial, model calls and data disclosure."""
import json

import pytest
import run_v2_quality_safety_http as runner


@pytest.mark.parametrize("defect", [None, "provider", "disclosure", "citation", "replay", "infrastructure", "unqualified", "answered"])
def test_safe_answer_cannot_hide_provider_or_entrypoint_failure(monkeypatch, defect):
    counts = iter([20, 21 if defect == "provider" else 20])
    monkeypatch.setattr(runner, "attempts", lambda _: next(counts))

    class Client:
        def response(self, method, path, body=None):
            if path == "/v1/conversations":
                return 201, {"id": "conversation"}
            if method == "POST":
                return 202, {"request_id": "turn"}
            return 200, {"status": "DONE" if defect == "answered" else "FAILED",
                         "answer": "restricted answer text" if defect == "answered" else "", "evidence": [
                             {"id": "chunk", "documentId": "internal" if defect == "disclosure" else "allowed",
                              "customerEligible": defect != "unqualified", "sourceType": "external_allowed"}],
                         "citations": [{"evidence_id": "unknown" if defect == "citation" else "chunk"}]}

        def events(self, path, last_event_id=0):
            events = [{"id": "1", "event": "completed", "data": json.dumps({
                "error_code": "database_unavailable" if defect == "infrastructure" else "permission_denied"})}]
            return [] if defect == "replay" else events[last_event_id:]

    row = runner.run_case(Client(), {"case_id": "syn-055", "query": "controlled query"}, None, {"internal"})
    assert row["passed"] is (defect is None)
    assert row["request_reached_provider"] is (defect == "provider")
    assert "restricted answer" not in json.dumps(row) and "controlled query" not in json.dumps(row)


@pytest.mark.parametrize("code", ["identity_revoked", "permission_changed", "shop_not_authorized", "database_unavailable"])
def test_actual_http_denial_is_required(monkeypatch, code):
    monkeypatch.setattr(runner, "attempts", lambda _: 10)

    class Client:
        def response(self, method, path, body=None):
            return 403 if code != "database_unavailable" else 503, {"code": code}

    row = runner.run_case(Client(), {"case_id": "syn-060", "query": "controlled query"}, None, set())
    assert row["passed"] is (code != "database_unavailable")
    assert row["provider_attempt_delta"] == 0


def test_partition_preserves_all_frozen_cases():
    cases = [{"case_id": i} for i in runner.SCENARIOS] + [{"case_id": f"eligible-{i}"} for i in range(53)]
    audit = {"case_results": [dict(r, status="EXCLUDED" if r["case_id"] in runner.SCENARIOS else "WITHIN_BUDGET") for r in cases]}
    assert len(runner.partition(cases, audit)) == 7
    with pytest.raises(runner.E2EFailure, match="partition"):
        runner.partition(cases[:-1], audit)
    with pytest.raises(runner.E2EFailure, match="partition"):
        runner.partition(cases[:-1] + [cases[0]], audit)
