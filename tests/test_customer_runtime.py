"""Focused unit tests for the controlled-runtime orchestration module.

These tests never touch Docker or the network: compose/command are replaced by
recording fakes. Live behaviour stays in the WP94 acceptance run.
"""
import importlib.util
import json
from argparse import Namespace
from pathlib import Path
from types import SimpleNamespace

import pytest

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('customer_runtime', ROOT / 'scripts/dev/customer_runtime.py')
runtime = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runtime)


def completed(returncode=0, stdout='', stderr=''):
    return SimpleNamespace(returncode=returncode, stdout=stdout, stderr=stderr)


def test_down_uses_oidc_profile_for_source_and_restored_and_removes_runtime(tmp_path, monkeypatch):
    source = tmp_path / 'runtime'
    source.mkdir()
    (source / 'compose.env').write_text('fixture\n')
    restored = tmp_path / 'restored'
    restored.mkdir()
    (restored / 'compose.env').write_text('fixture\n')
    state_file = tmp_path / 'state.json'
    state_file.write_text(json.dumps({'restored': [{'project': 'ecr-restore-wp91-abc123',
                                                    'runtime': str(restored)}]}))
    monkeypatch.setattr(runtime, 'RUNTIME', source)
    monkeypatch.setattr(runtime, 'STATE_FILE', state_file)
    calls = []

    def fake_compose(env_file, args, **kwargs):
        calls.append(list(args))
        return completed()

    def fake_command(argv, **kwargs):
        calls.append(list(argv))
        return completed()

    monkeypatch.setattr(runtime, 'compose', fake_compose)
    monkeypatch.setattr(runtime, 'command', fake_command)
    monkeypatch.setattr(runtime, 'sweep_harness_projects', list)

    assert runtime.down(Namespace(clean=True)) == 0
    assert len(calls) == 2
    for args in calls:
        assert args[args.index('--profile') + 1] == 'oidc'
        assert 'down' in args
    assert not source.exists() and not restored.exists() and not state_file.exists()


def test_down_keeps_runtime_when_compose_teardown_fails(tmp_path, monkeypatch):
    source = tmp_path / 'runtime'
    source.mkdir()
    (source / 'compose.env').write_text('fixture\n')
    state_file = tmp_path / 'state.json'
    state_file.write_text(json.dumps({'restored': []}))
    monkeypatch.setattr(runtime, 'RUNTIME', source)
    monkeypatch.setattr(runtime, 'STATE_FILE', state_file)
    monkeypatch.setattr(runtime, 'compose', lambda env_file, args, **kwargs: completed(returncode=1))
    monkeypatch.setattr(runtime, 'command', lambda argv, **kwargs: completed())
    monkeypatch.setattr(runtime, 'sweep_harness_projects', list)

    assert runtime.down(Namespace(clean=True)) == 1
    assert source.exists() and state_file.exists()


def test_sweep_only_removes_harness_projects_and_warns_about_merchant(tmp_path, monkeypatch, capsys):
    calls = []

    def fake_command(argv, **kwargs):
        calls.append(list(argv))
        if argv[:2] == ['docker', 'ps'] and '-a' in argv and '-aq' not in argv:
            return completed(stdout='ecr-wp91\necr-restore-wp91-20261108t000000z-abcdef\n'
                                    'ecr-merchant\nother-stack\n')
        if argv[:2] == ['docker', 'network'] and 'ls' in argv and '-q' not in argv:
            return completed(stdout='ecr-wp91\n')
        if argv[:2] == ['docker', 'ps'] and '-aq' in argv:
            project = argv[argv.index('--filter') + 1].split('=', 1)[1]
            return completed(stdout='container-' + project + '\n')
        return completed()

    monkeypatch.setattr(runtime, 'command', fake_command)
    removed = runtime.sweep_harness_projects()

    assert removed == ['ecr-restore-wp91-20261108t000000z-abcdef', 'ecr-wp91']
    assert 'WARN Compose project ecr-merchant' in capsys.readouterr().out
    removed_args = [part for argv in calls for part in argv]
    assert not any('ecr-merchant' in part or 'other-stack' in part for part in removed_args)


def test_local_login_environment_requires_stack_files(tmp_path, monkeypatch):
    monkeypatch.setenv('ECR_LOCAL_PORTS_FILE', str(tmp_path / 'ports.json'))
    with pytest.raises(runtime.Unconfigured):
        runtime.local_login_environment()


def test_local_login_environment_propagates_passwords_and_local_mode(tmp_path, monkeypatch):
    ports = tmp_path / 'ports.json'
    ports.write_text(json.dumps({'gateway': '127.0.0.1:26091', 'vite': '127.0.0.1:26094'}))
    (tmp_path / 'browser.env').write_text('LOGIN_USER=stack-login\nLOGIN_PASSWORD=initial-secret\n'
                                          'LOGIN_NEW_PASSWORD=next-secret-123456\n')
    (tmp_path / 'runtime.env').write_text('fixture\n')
    monkeypatch.setenv('ECR_LOCAL_PORTS_FILE', str(ports))
    monkeypatch.setattr(runtime, 'local_identity_state', lambda gateway: {'enabled': True, 'mode': 'local'})

    env = runtime.local_login_environment()

    assert env['WORKSPACE_URL'] == 'http://127.0.0.1:26094'
    assert env['LOGIN_USER'] == 'stack-login'
    assert env['LOGIN_PASSWORD'] == 'initial-secret'
    assert env['LOGIN_NEW_PASSWORD'] == 'next-secret-123456'
    assert env['ECR_LOCAL_RUNTIME_ENV'] == str(tmp_path / 'runtime.env')


def test_local_login_environment_rejects_a_non_local_stack(tmp_path, monkeypatch):
    ports = tmp_path / 'ports.json'
    ports.write_text(json.dumps({'gateway': '127.0.0.1:26091', 'vite': '127.0.0.1:26094'}))
    (tmp_path / 'browser.env').write_text('LOGIN_PASSWORD=initial-secret\nLOGIN_NEW_PASSWORD=next-secret-123456\n')
    (tmp_path / 'runtime.env').write_text('fixture\n')
    monkeypatch.setenv('ECR_LOCAL_PORTS_FILE', str(ports))
    monkeypatch.setattr(runtime, 'local_identity_state', lambda gateway: {'enabled': True, 'mode': 'oidc'})

    with pytest.raises(RuntimeError, match='not running the local profile'):
        runtime.local_login_environment()


def test_reset_local_login_passes_the_password_on_stdin(tmp_path, monkeypatch):
    runtime_env = tmp_path / 'runtime.env'
    runtime_env.write_text('fixture\n')
    calls = []

    def fake_command(argv, **kwargs):
        calls.append((list(argv), kwargs))
        return completed()

    monkeypatch.setattr(runtime, 'command', fake_command)
    runtime.reset_local_login({'ECR_LOCAL_RUNTIME_ENV': str(runtime_env), 'LOGIN_USER': 'stack-login',
                               'LOGIN_PASSWORD': 'initial-secret'})

    argv, kwargs = calls[0]
    assert argv[:3] == ['go', '-C', str(ROOT / 'go')]
    assert argv[3:6] == ['run', './cmd/local-account', 'reset-password']
    assert argv[argv.index('--user') + 1] == 'stack-login'
    assert argv[argv.index('--env') + 1] == str(runtime_env)
    assert kwargs['input'] == 'initial-secret\n'


def test_reset_local_login_failure_never_echoes_the_password(tmp_path, monkeypatch):
    runtime_env = tmp_path / 'runtime.env'
    runtime_env.write_text('fixture\n')
    monkeypatch.setattr(runtime, 'command',
                        lambda argv, **kwargs: completed(returncode=1, stdout='FAIL account or credential not found'))
    with pytest.raises(RuntimeError) as caught:
        runtime.reset_local_login({'ECR_LOCAL_RUNTIME_ENV': str(runtime_env), 'LOGIN_USER': 'stack-login',
                                   'LOGIN_PASSWORD': 'super-secret-password'})
    assert 'stack-login' in str(caught.value)
    assert 'super-secret-password' not in str(caught.value)


def test_wait_login_rate_window_waits_after_a_recent_run(tmp_path, monkeypatch):
    marker = tmp_path / 'last-run'
    marker.write_text('')
    monkeypatch.setattr(runtime, 'LOGIN_MARKER', marker)
    slept = []
    monkeypatch.setattr(runtime.time, 'time', lambda: marker.stat().st_mtime + 10)
    monkeypatch.setattr(runtime.time, 'sleep', lambda seconds: slept.append(seconds))

    runtime.wait_login_rate_window()

    assert len(slept) == 1
    assert 50 <= slept[0] <= 60


def test_wait_login_rate_window_skips_without_a_previous_run(tmp_path, monkeypatch):
    monkeypatch.setattr(runtime, 'LOGIN_MARKER', tmp_path / 'missing')
    slept = []
    monkeypatch.setattr(runtime.time, 'sleep', lambda seconds: slept.append(seconds))

    runtime.wait_login_rate_window()

    assert slept == []


def test_mark_login_rate_window_is_private(tmp_path, monkeypatch):
    monkeypatch.setattr(runtime, 'LOGIN_MARKER', tmp_path / 'nested' / 'last-run')

    runtime.mark_login_rate_window()

    assert runtime.LOGIN_MARKER.exists()
    assert runtime.LOGIN_MARKER.stat().st_mode & 0o777 == 0o600
