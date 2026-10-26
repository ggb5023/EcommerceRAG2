"""Safety evidence must distinguish denial, model calls and data disclosure."""
import json

import pytest
import run_v2_quality_safety_http as runner


@pytest.mark.parametrize("database", ["ecr_v2_accept_20261026", "ecr_v2_test_accept", "ecr_v2_dev_local"])
def test_acceptance_prefix_is_allowed_for_isolated_safety(database):
    assert runner.isolated_database_name_valid(database, database, database)


@pytest.mark.parametrize("database", ["ecr_v2_prod", "postgres", "accept"])
def test_production_database_names_remain_rejected(database):
    assert not runner.isolated_database_name_valid(database, database, database)


def test_expected_database_must_match_lifecycle_report():
    assert not runner.isolated_database_name_valid("ecr_v2_accept_one", "ecr_v2_accept_two", "ecr_v2_accept_two")
    assert not runner.isolated_database_name_valid("ecr_v2_accept_one", "ecr_v2_accept_one", "ecr_v2_accept_two")


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


@pytest.mark.parametrize("defect", [None, "changed_history", "missing_history", "bad_append", "missing_file", "duplicate"])
def test_append_only_schema_revalidation_preserves_historical_evidence(tmp_path, defect):
    import hashlib

    historical = []
    for version in ("0001", "0002"):
        body = f"migration {version}".encode()
        (tmp_path / f"{version}_test.sql").write_bytes(body)
        historical.append({"version": version, "checksum_sha256": hashlib.sha256(body).hexdigest()})
    current = [*historical]
    if defect == "changed_history":
        current[0] = dict(current[0], checksum_sha256="wrong")
    elif defect == "missing_history":
        current = []
    elif defect == "bad_append":
        current[-1] = dict(current[-1], checksum_sha256="wrong")
        historical = historical[:1]
    elif defect == "missing_file":
        (tmp_path / "0002_test.sql").unlink()
    elif defect == "duplicate":
        current.append(current[-1])
    if defect:
        with pytest.raises(runner.E2EFailure, match="migration_identity_changed"):
            runner.validate_migrations(current, historical, tmp_path)
    else:
        before = json.dumps(historical)
        assert runner.validate_migrations(current, historical[:1], tmp_path) == current[1:]
        assert json.dumps(historical) == before


@pytest.mark.parametrize("defect", [None, "failed_batch", "missing_case", "duplicate_case", "wrong_hash", "missing_persisted_result"])
def test_safety_can_be_measured_for_a_complete_failed_batch_without_upgrading_it(defect):
    cases = [{"case_id": i} for i in runner.SCENARIOS] + [{"case_id": f"eligible-{i}"} for i in range(53)]
    report = {"report_version": "v2-quality-e2e-v2", "status": "NOT_PASSED" if defect == "failed_batch" else "PASS",
              "case_count": 53, "cases_sha256": "cases", "lifecycle_report_sha256": "lifecycle",
              "real_service_acceptance": False,
              "results": [{"case_id": f"eligible-{i}", "request_id": f"request-{i}", "terminal_persisted": True}
                          for i in range(53)]}
    if defect == "missing_case":
        report["results"].pop()
    elif defect == "duplicate_case":
        report["results"][-1] = report["results"][0]
    elif defect == "wrong_hash":
        report["lifecycle_report_sha256"] = "different"
    elif defect == "missing_persisted_result":
        report["results"][0]["terminal_persisted"] = False
    if defect in {None, "failed_batch"}:
        runner.validate_e2e_identity(report, cases, "cases", "lifecycle")
        assert report["status"] == ("NOT_PASSED" if defect == "failed_batch" else "PASS")
    else:
        with pytest.raises(runner.E2EFailure, match="identity"):
            runner.validate_e2e_identity(report, cases, "cases", "lifecycle")
