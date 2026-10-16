"""An engineering PASS needs usable results and actual replay/persistence."""
import importlib.util
import json
from pathlib import Path

import pytest

spec = importlib.util.spec_from_file_location("http_quality", Path(__file__).with_name("run_v2_quality_e2e.py"))
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)


@pytest.mark.parametrize("defect", [None, "failed", "replay", "citation", "idempotency", "refresh"])
def test_case_records_real_failures_and_keeps_raw_output_out_of_report(defect, tmp_path):
    class Client:
        def __init__(self):
            self.turn_posts = 0
            self.gets = 0

        def call(self, method, path, body=None, key=None):
            if path == "/v1/conversations":
                return {"id": "conversation"}
            if method == "POST":
                self.turn_posts += 1
                return {"request_id": "changed" if defect == "idempotency" and self.turn_posts == 2 else "turn"}
            self.gets += 1
            return {"status": "FAILED" if defect == "failed" else "DONE",
                    "answer": "source supported private answer" if self.gets == 1 or defect != "refresh" else "changed",
                    "evidence": [{"id": "chunk", "documentId": "doc", "versionId": "version",
                                  "snippet": "restricted source text"}],
                    "citations": [{"evidence_id": "unknown" if defect == "citation" else "chunk"}],
                    "customer_reply": {"can_copy": False}}

        def events(self, path, last_event_id=0):
            events = [{"id": "1", "event": "accepted", "data": "{}"},
                      {"id": "2", "event": "done", "data": "{}"}]
            return [] if defect == "replay" and last_event_id else events[last_event_id:]

    case = {"case_id": "case", "query": "controlled query", "expected_doc_ids": ["doc"]}
    row = runner.run_case(Client(), case, timeout_s=1, capture_dir=tmp_path / "capture")
    assert row["terminal_persisted"] is True
    assert row["successful_terminal"] is (defect != "failed")
    assert row["sse_replay_matches"] is (defect != "replay")
    assert row["citation_ids_valid"] is (defect != "citation")
    assert row["idempotent_turn"] is (defect != "idempotency")
    assert row["refresh_matches"] is (defect != "refresh")
    assert "private answer" not in str(row) and "restricted source" not in str(row)
    captured = tmp_path / "capture" / row["capture_id"]
    assert captured.stat().st_mode & 0o777 == 0o600
    assert "private answer" in captured.read_text()


@pytest.mark.parametrize("prior_status", ["DONE", "ASKING", "FAILED"])
def test_prerequisite_turn_runs_in_the_same_conversation_and_blocks_on_failure(prior_status):
    class Client:
        def __init__(self):
            self.conversations = 0
            self.requests = []

        def call(self, method, path, body=None, key=None):
            if path == "/v1/conversations":
                self.conversations += 1
                return {"id": "same-conversation"}
            if method == "POST":
                self.requests.append((path, body["query"]))
                return {"request_id": "prior" if "context" in key else "current"}
            return {"status": prior_status if "prior" in path else "DONE", "answer": "controlled answer"}

        def events(self, path, last_event_id=0):
            events = [{"id": "1", "event": "completed", "data": "{}"}]
            return events[last_event_id:]

    http = Client()
    case = {"case_id": "case", "query": "current query"}
    if prior_status != "DONE":
        with pytest.raises(runner.E2EFailure, match="prerequisite_failed"):
            runner.run_case(http, case, timeout_s=1, prerequisite_queries=["prior query"])
        assert all(query == "prior query" for _, query in http.requests)
    else:
        row = runner.run_case(http, case, timeout_s=1, prerequisite_queries=["prior query"])
        assert len(row["prerequisite_results"]) == 1
        assert row["prerequisite_results"][0]["query_sha256"] == runner.digest("prior query")
        assert all("same-conversation" in path for path, _ in http.requests)
    assert http.conversations == 1


def test_scenario_rejects_changed_case_or_query_baseline(tmp_path):
    cases = [{"case_id": "case", "query": "frozen query"}]
    source = tmp_path / "cases.jsonl"
    source.write_text(json.dumps(cases[0]) + "\n")
    config = {"scenario_version": "v2-quality-conversation-v1",
              "cases_sha256": runner.hashlib.sha256(source.read_bytes()).hexdigest(),
              "scenarios": [{"case_id": "case", "query_sha256": runner.digest("frozen query"),
                             "prerequisite_queries": ["controlled prerequisite"]}]}
    path = tmp_path / "scenario.json"
    path.write_text(json.dumps(config))
    assert runner.load_scenarios(path, source, cases) == {"case": ["controlled prerequisite"]}
    with pytest.raises(runner.E2EFailure, match="scenario_invalid"):
        runner.load_scenarios(path, source, [{"case_id": "case", "query": "changed"}])
    source.write_text("changed")
    with pytest.raises(runner.E2EFailure, match="baseline_mismatch"):
        runner.load_scenarios(path, source, cases)


@pytest.mark.parametrize('defect', [None, 'absent', 'wrong_version', 'wrong_owner', 'not_fake'])
def test_preflight_requires_the_frozen_versioned_fake_before_paid_calls(monkeypatch, tmp_path, defect):
    import sys
    from io import BytesIO
    sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'scripts' / 'dev'))
    import v2_services
    values = {'V2_FACTS_BASE_URL': 'http://127.0.0.1:18090', 'V2_FACTS_VERSION': 'selected-fixture',
              'V2_IDENTITY_TENANT': 'tenant', 'V2_IDENTITY_USER': 'user', 'V2_IDENTITY_SHOP': 'shop'}
    monkeypatch.setattr(v2_services, 'load_values', lambda _: values)
    payload = {'version': 'wrong' if defect == 'wrong_version' else 'selected-fixture',
               'tenant_id': 'tenant', 'shop_id': 'other' if defect == 'wrong_owner' else 'shop',
               'is_mock': defect != 'not_fake', 'resource_id': 'V2-CUP-480', 'tool_name': 'get_stock',
               'value_json': {'sku': 'V2-CUP-480'}}
    class Response(BytesIO):
        status = 200
    class Opener:
        def open(self, request, timeout):
            if defect == 'absent':
                raise OSError('sensitive transport detail')
            return Response(json.dumps(payload).encode())
    monkeypatch.setattr(runner.urllib.request, 'build_opener', lambda *args: Opener())
    if defect:
        with pytest.raises(runner.E2EFailure, match='^synthetic_facts_not_ready_or_mismatched$'):
            runner.verify_synthetic_facts_ready(tmp_path / 'runtime.env')
    else:
        runner.verify_synthetic_facts_ready(tmp_path / 'runtime.env')


def test_identifier_followup_and_idempotent_repeat_bind_same_parent():
    bodies = []
    class Client:
        def call(self, method, path, body=None, key=None):
            if method == 'POST':
                bodies.append(body)
                return {'request_id': 'child'}
            return {'status': 'DONE', 'answer': 'current fact', 'evidence': [], 'citations': []}
        def events(self, path, last_event_id=0):
            return [{'id': '1', 'event': 'done', 'data': '{}'}][last_event_id:]
    runner.run_case(Client(), {'case_id': 'followup', 'query': 'SKU-1 current stock'},
                    timeout_s=1, conversation_id='conversation', parent_turn_id='parent')
    assert bodies == [{'query': 'SKU-1 current stock', 'parent_turn_id': 'parent'}] * 2


def test_failed_persisted_evidence_read_preserves_execution_and_redacts_error(monkeypatch, tmp_path):
    def fail(*args):
        raise RuntimeError('private connection detail')
    monkeypatch.setattr(runner, 'read_retrieval_trace', fail)
    row = {'request_id': 'turn', 'status': 'FAILED', 'capture_id': 'turn.json'}
    runner.attach_persisted_evidence(row, tmp_path / 'runtime.env', tmp_path)
    assert row['status'] == 'FAILED' and row['persisted_evidence_read_failed'] is True
    assert 'private connection detail' not in str(row)


@pytest.mark.parametrize('ids', [['other'], ['db-chunk-1', 'db-chunk-1']])
def test_context_binding_rejects_foreign_or_duplicate_ids_before_connection(monkeypatch, tmp_path, ids):
    import sys
    sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'scripts' / 'dev'))
    import v2_services
    monkeypatch.setattr(v2_services, 'load_values', lambda path: {})
    capture = tmp_path / 'capture.json'
    capture.write_text(json.dumps({'result': {'evidence': [{'id': i} for i in ids]}}))
    with pytest.raises(runner.E2EFailure, match='identity_invalid'):
        runner.read_context_bindings(tmp_path / 'runtime.env', capture)


@pytest.mark.parametrize('code, expected_count', [('allocation_quota', 1), ('authentication', 1),
    ('budget_blocked', 1), ('budget_exhausted', 1), ('upstream_error', 2), ('schema_error', 2)])
def test_batch_stops_on_global_provider_failure_without_inventing_unrun_rows(monkeypatch, tmp_path, code, expected_count):
    import sqlite3
    ledger = tmp_path / 'ledger.sqlite'
    with sqlite3.connect(ledger) as conn:
        conn.execute('CREATE TABLE attempt(error_code TEXT)')
        conn.execute("INSERT INTO attempt VALUES ('allocation_quota')")  # old failure is irrelevant
    monkeypatch.setattr(runner, 'budget_snapshot', lambda path: {
        'last_sequence': sqlite3.connect(path).execute('SELECT max(rowid) FROM attempt').fetchone()[0]})
    monkeypatch.setattr(runner, 'attach_persisted_evidence', lambda *a: None)
    calls = []
    def execute(http, case, **kwargs):
        calls.append(case['case_id'])
        with sqlite3.connect(ledger) as conn:
            conn.execute('INSERT INTO attempt VALUES (?)', (code,))
        return {'case_id': case['case_id'], 'status': 'FAILED', 'capture_id': case['case_id'] + '.json'}
    monkeypatch.setattr(runner, 'run_case', execute)
    cases = [{'case_id': 'first'}, {'case_id': 'second'}]
    rows, reason = runner.run_cases(object(), cases, scenarios={}, runtime_env=None,
        capture_dir=None, ledger=ledger, timeout_s=1, run_id='test')
    assert len(rows) == expected_count and calls == [c['case_id'] for c in cases[:expected_count]]
    assert reason == (code if expected_count == 1 else None)
    assert all(row['status'] == 'FAILED' and row['capture_id'] for row in rows)


def test_batch_blocker_does_not_expose_arbitrary_ledger_text(tmp_path):
    import sqlite3
    ledger = tmp_path / 'ledger.sqlite'
    with sqlite3.connect(ledger) as conn:
        conn.execute('CREATE TABLE attempt(error_code TEXT)')
        conn.execute('INSERT INTO attempt VALUES (?)', ('private response content',))
    assert runner.blocking_provider_error(ledger, 0) is None
