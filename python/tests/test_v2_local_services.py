"""Exercise service credential boundaries and real supervisor process cleanup."""
from __future__ import annotations

import importlib.util
import json
import os
import signal
import subprocess
import sys
import time
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("v2_services", ROOT / "scripts/v2_services.py")
services = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(services)


def values(tmp_path):
    return services.configure({
        "PATH": os.environ["PATH"], "HOME": os.environ["HOME"],
        "V2_GATEWAY_DSN": "gateway-connection", "V2_AI_DSN": "ai-connection",
        "V2_INGEST_DSN": "ingest-connection", "V2_MIGRATION_DSN": "migration-connection",
        "V2_IDENTITY_TENANT": "test-tenant", "V2_IDENTITY_USER": "test-user",
        "INGEST_STORAGE_PROFILE": "filesystem", "INGEST_ARTIFACT_ROOT": str(tmp_path),
        "V2_PROVIDER_CONFIG": "provider-config", "UNRELATED_SECRET": "do-not-propagate",
        "V2_FIXTURE_MODE": "1",
    })


def test_real_children_receive_only_their_service_credentials(tmp_path):
    configured = values(tmp_path)
    for name in services.SERVICE_KEYS:
        env = services.service_environment(name, configured)
        child = subprocess.run([sys.executable, "-c", "import os,json;print(json.dumps(dict(os.environ)))"],
                               env=env, text=True, capture_output=True, check=True)
        received = json.loads(child.stdout)
        assert "V2_MIGRATION_DSN" not in received
        assert "UNRELATED_SECRET" not in received
        assert "V2_FIXTURE_MODE" not in received
        if name == "gateway":
            assert received["V2_GATEWAY_DSN"] == "gateway-connection"
            assert "V2_INGEST_DSN" not in received and "V2_RETRIEVER_DSN" not in received
        elif name == "ai":
            assert received["V2_RETRIEVER_DSN"] == received["V2_CHECKPOINT_DSN"] == "ai-connection"
            assert "V2_INGEST_DSN" not in received and "V2_GATEWAY_DSN" not in received
        else:
            assert received["V2_INGEST_DSN"] == "ingest-connection"
            assert "V2_GATEWAY_DSN" not in received and "V2_RETRIEVER_DSN" not in received
        if name == "ingest":
            assert "V2_PROVIDER_CONFIG" not in received


@pytest.mark.parametrize("invalid", [{"HTTP_ADDR": "0.0.0.0:8080"}, {"V2_GRPC_ADDR": "public.example:50052"},
                                     {"INGEST_ARTIFACT_ROOT": "relative"}, {"APP_ENV": "production"},
                                     {"RAG_PROFILE": "v2_langchain_bailian", "V2_PROVIDER_CONFIG": ""}])
def test_invalid_configuration_fails_before_any_service_starts(tmp_path, invalid):
    with pytest.raises(services.StartupError):
        services.configure(values(tmp_path) | invalid)


@pytest.mark.parametrize("raw_budget", ["", "0", "-1", "NaN", "Infinity", "not-a-number"])
def test_online_profile_rejects_invalid_budget(tmp_path, raw_budget):
    base = values(tmp_path) | {"RAG_PROFILE": "v2_langchain_bailian"}
    with pytest.raises(services.StartupError, match="budget"):
        services.configure(base | {"V2_ONLINE_BUDGET_CNY": raw_budget})


def test_online_profile_accepts_positive_finite_budget(tmp_path):
    base = values(tmp_path) | {"RAG_PROFILE": "v2_langchain_bailian"}
    configured = services.configure(base | {"V2_ONLINE_BUDGET_CNY": "12.50"})
    assert configured["V2_ONLINE_BUDGET_CNY"] == "12.50"
    assert services.service_environment("ai", configured)["V2_ONLINE_BUDGET_CNY"] == "12.50"
    assert "V2_ONLINE_BUDGET_CNY" not in services.service_environment("gateway", configured)


def test_runtime_file_permissions_symlinks_and_duplicate_keys_are_rejected(tmp_path):
    path = tmp_path / "runtime.env"
    path.write_text("APP_ENV=test\nAPP_ENV=development\n")
    path.chmod(0o600)
    with pytest.raises(services.StartupError, match="duplicate"):
        services.load_values(path)
    path.write_text("APP_ENV=test\n")
    path.chmod(0o644)
    with pytest.raises(services.StartupError, match="0600"):
        services.load_values(path)
    path.chmod(0o600)
    link = tmp_path / "runtime-link.env"
    link.symlink_to(path)
    with pytest.raises(services.StartupError, match="regular"):
        services.load_values(link)


CHILD = "import os,time;from pathlib import Path;Path(os.environ['PID_PATH']).write_text(str(os.getpid()));time.sleep(60)"


def child_commands(tmp_path):
    commands = {name: [sys.executable, "-c", CHILD] for name in services.SERVICE_KEYS}
    environments = {name: {"PATH": os.environ["PATH"], "PID_PATH": str(tmp_path / name)} for name in commands}
    return commands, environments


def assert_children_stopped(tmp_path):
    for name in services.SERVICE_KEYS:
        path = tmp_path / name
        if path.exists():
            with pytest.raises(ProcessLookupError):
                os.kill(int(path.read_text()), 0)


def test_readiness_timeout_stops_all_started_children(tmp_path, capsys):
    commands, environments = child_commands(tmp_path)
    result = services.supervise(commands, environments, lambda _: False,
                                cwd=tmp_path, start_timeout=0.5, stop_timeout=0.5)
    assert result == 1
    assert "READY" not in capsys.readouterr().out
    assert_children_stopped(tmp_path)


def test_even_clean_early_child_exit_is_a_failure_and_cleans_peers(tmp_path):
    commands, environments = child_commands(tmp_path)
    commands["ai"] = [sys.executable, "-c", "import time;time.sleep(.2)"]
    result = services.supervise(commands, environments, lambda _: False,
                                cwd=tmp_path, start_timeout=2, stop_timeout=0.5)
    assert result == 1
    assert_children_stopped(tmp_path)


def test_sigterm_stops_ready_stack_without_killing_unrelated_process(tmp_path):
    commands, environments = child_commands(tmp_path)
    script = (
        "import importlib.util,json;from pathlib import Path;"
        f"s=importlib.util.spec_from_file_location('supervisor',{str(ROOT / 'scripts/v2_services.py')!r});"
        "m=importlib.util.module_from_spec(s);s.loader.exec_module(m);"
        f"commands=json.loads({json.dumps(commands)!r});envs=json.loads({json.dumps(environments)!r});"
        f"raise SystemExit(m.supervise(commands,envs,lambda n:Path({str(tmp_path)!r},n).exists(),"
        f"cwd=Path({str(tmp_path)!r}),start_timeout=5,stop_timeout=.5))"
    )
    unrelated = subprocess.Popen([sys.executable, "-c", "import time;time.sleep(60)"])
    process = subprocess.Popen([sys.executable, "-c", script], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    try:
        deadline = time.monotonic() + 5
        while not all((tmp_path / name).exists() for name in services.SERVICE_KEYS):
            assert process.poll() is None
            assert time.monotonic() < deadline
            time.sleep(0.05)
        # All children are running; readiness is observed before operator stop.
        assert process.stdout.readline().startswith("READY V2")
        process.send_signal(signal.SIGTERM)
        assert process.wait(timeout=5) == 0
        assert_children_stopped(tmp_path)
        assert unrelated.poll() is None
    finally:
        if process.poll() is None:
            process.kill()
            process.wait(timeout=5)
        unrelated.terminate()
        unrelated.wait(timeout=5)
