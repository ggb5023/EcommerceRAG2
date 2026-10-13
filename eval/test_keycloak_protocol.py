from __future__ import annotations

import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
import run_keycloak_protocol as runner


def test_interrupted_protocol_run_keeps_partial_failure_evidence(tmp_path, monkeypatch):
    output = tmp_path / 'report.json'
    args = ['run_keycloak_protocol', '--live', '--accounts-file', '/protected/accounts',
            '--oidc-config', '/protected/config', '--runtime-env', '/protected/runtime',
            '--capture-dir', str(tmp_path / 'captures'), '--report', str(output)]
    monkeypatch.setattr(sys, 'argv', args)

    def interrupted(config):
        config.observations.append({'check_id': 'initial_check', 'passed': True})
        raise ValueError('upstream exception must never be exported')

    monkeypatch.setattr(runner, 'run', interrupted)
    assert runner.main() == 2
    payload = json.loads(output.read_text())
    assert payload['status'] == 'NOT_PASSED'
    assert payload['results'] == [{'check_id': 'initial_check', 'passed': True}]
    assert payload['fixture_restoration_confirmed'] is False
    assert 'upstream exception' not in output.read_text()
    assert output.stat().st_mode & 0o777 == 0o600
