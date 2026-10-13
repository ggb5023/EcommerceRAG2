"""Supplemental scenarios preserve failures and bind actual identifier followups."""
import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
import run_evidence_boundary_diagnostics as runner


def test_plan_does_not_load_credentials_or_call_provider(monkeypatch, capsys):
    def forbidden(*args):
        raise AssertionError('online work during plan')
    monkeypatch.setattr(runner, 'runtime_identity', forbidden)
    assert runner.main([]) == 0
    result = json.loads(capsys.readouterr().out)
    assert result['status'] == 'PLAN' and result['scenario_count'] == 8
    assert result['online_requests_made'] is False


def test_existing_report_blocks_before_runtime_or_requests(tmp_path, monkeypatch):
    report = tmp_path / 'report.json'; report.write_text('historical result')
    monkeypatch.setattr(runner, 'runtime_identity', lambda *args: (_ for _ in ()).throw(AssertionError()))
    assert runner.main(['--live', '--report', str(report), '--runtime-env', str(tmp_path / 'runtime.env'),
                        '--capture-dir', str(tmp_path / 'capture')]) == 2
    assert report.read_text() == 'historical result'


def test_failed_scenarios_are_all_recorded_and_never_preapproved(tmp_path, monkeypatch):
    def fail(*args, **kwargs):
        raise runner.E2EFailure('scenario_prerequisite_failed')
    monkeypatch.setattr(runner, 'run_case', fail)
    rows = runner.run_diagnostics(object(), tmp_path, tmp_path / 'runtime.env')
    assert len(rows) == 8
    assert all(row['status'] == 'FAILED' and row['semantic_review_status'] == 'not_reviewed' for row in rows)


def test_identifier_followups_use_captured_parent_and_require_fresh_fact_read(tmp_path, monkeypatch):
    calls = []
    checks = dict.fromkeys(('terminal_persisted', 'successful_terminal', 'sse_sequence_monotonic',
                           'idempotent_turn', 'refresh_matches', 'sse_replay_matches', 'citation_ids_valid'), True)
    def execute(http, case, **kwargs):
        cid = case['case_id']; calls.append((cid, kwargs))
        status = 'ASKING' if cid.endswith('-initial') else 'DONE'
        capture = tmp_path / (cid + '.json')
        capture.write_text(json.dumps({'result': {'conversation_id': cid + '-conversation', 'turn_id': cid + '-turn'}}))
        reads = []
        if cid == 'stock_identifier': reads = [{'tool_name': 'get_stock', 'resource_id': 'SKU-EAR-200'}]
        if cid == 'price_identifier': reads = [{'tool_name': 'get_price', 'resource_id': 'V2-CUP-480'}]
        return dict(case_id=cid, status=status, capture_id=capture.name, fact_reads=reads, sse_event_count=1, **checks)
    monkeypatch.setattr(runner, 'run_case', execute)
    monkeypatch.setattr(runner, 'attach_persisted_evidence', lambda *args: None)
    rows = runner.run_diagnostics(object(), tmp_path, tmp_path / 'runtime.env')
    for name, *_ in runner.FOLLOWUPS:
        kwargs = next(kwargs for cid, kwargs in calls if cid == name)
        assert kwargs['parent_turn_id'] == name + '-initial-turn'
        assert kwargs['conversation_id'] == name + '-initial-conversation'
    assert [row['identifier_followup_passed'] for row in rows[-3:]] == [True, True, False]
    assert all(row['semantic_review_status'] == 'not_reviewed' for row in rows)
